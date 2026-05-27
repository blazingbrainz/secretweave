package synchronizer

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"

	"github.com/blazingbrainz/secretweave/internal/config"
)

const (
	managedByAnnotation = "secretweave.io/managed-by"
	sourceNSAnnotation  = "secretweave.io/source-ns"
)

type Synchronizer struct {
	client *kubernetes.Clientset
	cfg    *config.Config
	log    *slog.Logger
}

type syncJob struct {
	secret    *corev1.Secret
	namespace string
}

func New(client *kubernetes.Clientset, cfg *config.Config, log *slog.Logger) *Synchronizer {
	return &Synchronizer{client: client, cfg: cfg, log: log}
}

func (s *Synchronizer) Run(ctx context.Context) error {
	jobs := make(chan syncJob, 10000)

	var workerWg sync.WaitGroup
	for i := 0; i < s.cfg.WorkerCount; i++ {
		workerWg.Add(1)
		go func() {
			defer workerWg.Done()
			for job := range jobs {
				// Check context before each job so shutdown drains fast.
				select {
				case <-ctx.Done():
					return
				default:
				}
				if err := s.syncSecretToNamespace(ctx, job.secret, job.namespace); err != nil {
					s.log.Error("sync failed",
						"secret", job.secret.Name,
						"source_ns", job.secret.Namespace,
						"target_ns", job.namespace,
						"err", err,
					)
				}
			}
		}()
	}

	// secretFactory is namespace-scoped: watches annotated Secrets in the parent namespace.
	secretFactory := informers.NewSharedInformerFactoryWithOptions(
		s.client, 0,
		informers.WithNamespace(s.cfg.ParentNamespace),
	)
	secretInformer := secretFactory.Core().V1().Secrets().Informer()
	secretInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			secret, ok := obj.(*corev1.Secret)
			if ok && s.isAnnotated(secret) {
				s.enqueueSecretSync(ctx, secret, jobs)
			}
		},
		UpdateFunc: func(_, newObj interface{}) {
			secret, ok := newObj.(*corev1.Secret)
			if ok && s.isAnnotated(secret) {
				s.enqueueSecretSync(ctx, secret, jobs)
			}
		},
		DeleteFunc: func(obj interface{}) {
			if !s.cfg.DeleteOnRemove {
				return
			}
			secret := extractSecret(obj)
			if secret != nil && s.isAnnotated(secret) {
				go s.deleteFromTargets(ctx, secret)
			}
		},
	})

	// nsFactory is cluster-scoped: watches Namespace events so new namespaces
	// receive secrets immediately rather than waiting for the next poll tick.
	//
	// initialSyncDone gates the AddFunc handler: during the informer's initial
	// list phase AddFunc fires for every existing namespace, but those are
	// already covered by enqueueAllAnnotated below — we only want to react
	// to namespaces that arrive after startup is complete.
	var initialSyncDone atomic.Bool
	nsFactory := informers.NewSharedInformerFactory(s.client, 0)
	nsInformer := nsFactory.Core().V1().Namespaces().Informer()
	nsInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			if !initialSyncDone.Load() {
				return
			}
			ns, ok := obj.(*corev1.Namespace)
			if !ok || ns.Name == s.cfg.ParentNamespace {
				return
			}
			if !s.isTargetNamespace(ns.Name) {
				return
			}
			s.log.Info("new namespace detected, seeding annotated secrets", "namespace", ns.Name)
			go s.seedNamespace(ctx, ns.Name, jobs)
		},
		// DeleteFunc: no-op — Kubernetes terminates all resources inside a
		// deleted namespace automatically; nothing for SecretWeave to clean up.
	})

	secretFactory.Start(ctx.Done())
	nsFactory.Start(ctx.Done())

	s.log.Info("waiting for informer cache sync")
	if !cache.WaitForCacheSync(ctx.Done(), secretInformer.HasSynced, nsInformer.HasSynced) {
		close(jobs)
		workerWg.Wait()
		return fmt.Errorf("informer cache sync timed out")
	}

	// Enable namespace event handling before the initial sync so that any
	// namespace created in this narrow window is not missed.
	initialSyncDone.Store(true)

	s.log.Info("cache synced — running initial full sync")
	if err := s.enqueueAllAnnotated(ctx, jobs); err != nil {
		s.log.Error("initial full sync failed", "err", err)
	}

	fullSyncTicker := time.NewTicker(s.cfg.FullSyncInterval)
	defer fullSyncTicker.Stop()
	syncTicker := time.NewTicker(s.cfg.SyncInterval)
	defer syncTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			close(jobs)
			workerWg.Wait()
			s.log.Info("shutdown complete")
			return nil

		case <-fullSyncTicker.C:
			s.log.Info("starting periodic full sync")
			if err := s.enqueueAllAnnotated(ctx, jobs); err != nil {
				s.log.Error("periodic full sync failed", "err", err)
			}

		case <-syncTicker.C:
			s.log.Info("checking for new annotated secrets")
			if err := s.enqueueAllAnnotated(ctx, jobs); err != nil {
				s.log.Error("interval sync check failed", "err", err)
			}
		}
	}
}

// seedNamespace pushes all currently-annotated secrets to a single newly-created
// target namespace. Called in a goroutine from the namespace informer's AddFunc.
func (s *Synchronizer) seedNamespace(ctx context.Context, targetNS string, jobs chan<- syncJob) {
	secrets, err := s.client.CoreV1().Secrets(s.cfg.ParentNamespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		s.log.Error("seedNamespace: failed to list secrets", "namespace", targetNS, "err", err)
		return
	}
	for i := range secrets.Items {
		if !s.isAnnotated(&secrets.Items[i]) {
			continue
		}
		select {
		case jobs <- syncJob{secret: &secrets.Items[i], namespace: targetNS}:
		case <-ctx.Done():
			return
		}
	}
}

// enqueueAllAnnotated lists all annotated secrets in the parent namespace
// and pushes a sync job for each (secret, target-namespace) pair.
func (s *Synchronizer) enqueueAllAnnotated(ctx context.Context, jobs chan<- syncJob) error {
	secrets, err := s.client.CoreV1().Secrets(s.cfg.ParentNamespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("list secrets: %w", err)
	}
	namespaces, err := s.listTargetNamespaces(ctx)
	if err != nil {
		return fmt.Errorf("list namespaces: %w", err)
	}

	s.log.Info("enqueueing full sync",
		"annotated_secrets", countAnnotated(secrets.Items, s.isAnnotated),
		"target_namespaces", len(namespaces),
	)

	for i := range secrets.Items {
		if !s.isAnnotated(&secrets.Items[i]) {
			continue
		}
		for _, ns := range namespaces {
			select {
			case jobs <- syncJob{secret: &secrets.Items[i], namespace: ns}:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
	return nil
}

// enqueueSecretSync pushes one secret to all target namespaces.
func (s *Synchronizer) enqueueSecretSync(ctx context.Context, secret *corev1.Secret, jobs chan<- syncJob) {
	namespaces, err := s.listTargetNamespaces(ctx)
	if err != nil {
		s.log.Error("failed to list target namespaces", "err", err)
		return
	}
	for _, ns := range namespaces {
		select {
		case jobs <- syncJob{secret: secret, namespace: ns}:
		case <-ctx.Done():
			return
		}
	}
}

func (s *Synchronizer) listTargetNamespaces(ctx context.Context) ([]string, error) {
	nsList, err := s.client.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(nsList.Items))
	for _, ns := range nsList.Items {
		if ns.Name == s.cfg.ParentNamespace {
			continue
		}
		if s.isTargetNamespace(ns.Name) {
			out = append(out, ns.Name)
		}
	}
	return out, nil
}

// isTargetNamespace returns true when name should receive synced secrets.
// includeNamespaces is an allowlist (empty = all pass).
// excludeNamespaces is a denylist applied after the allowlist (empty = none blocked).
func (s *Synchronizer) isTargetNamespace(name string) bool {
	if len(s.cfg.IncludeNamespaces) > 0 {
		found := false
		for _, ns := range s.cfg.IncludeNamespaces {
			if ns == name {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	for _, ns := range s.cfg.ExcludeNamespaces {
		if ns == name {
			return false
		}
	}
	return true
}

func (s *Synchronizer) syncSecretToNamespace(ctx context.Context, secret *corev1.Secret, targetNS string) error {
	existing, err := s.client.CoreV1().Secrets(targetNS).Get(ctx, secret.Name, metav1.GetOptions{})
	if kerrors.IsNotFound(err) {
		_, createErr := s.client.CoreV1().Secrets(targetNS).Create(ctx, buildSecret(secret, targetNS), metav1.CreateOptions{})
		if createErr != nil {
			return fmt.Errorf("create in %s: %w", targetNS, createErr)
		}
		s.log.Info("created secret", "name", secret.Name, "namespace", targetNS)
		return nil
	}
	if err != nil {
		return fmt.Errorf("get from %s: %w", targetNS, err)
	}

	if dataEqual(existing.Data, secret.Data) && existing.Type == secret.Type {
		return nil
	}

	updated := existing.DeepCopy()
	updated.Data = secret.Data
	updated.StringData = secret.StringData
	updated.Type = secret.Type
	_, err = s.client.CoreV1().Secrets(targetNS).Update(ctx, updated, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("update in %s: %w", targetNS, err)
	}
	s.log.Info("updated secret", "name", secret.Name, "namespace", targetNS)
	return nil
}

func (s *Synchronizer) deleteFromTargets(ctx context.Context, secret *corev1.Secret) {
	namespaces, err := s.listTargetNamespaces(ctx)
	if err != nil {
		s.log.Error("delete: failed to list namespaces", "err", err)
		return
	}
	for _, ns := range namespaces {
		if err := s.client.CoreV1().Secrets(ns).Delete(ctx, secret.Name, metav1.DeleteOptions{}); err != nil && !kerrors.IsNotFound(err) {
			s.log.Error("failed to delete secret", "name", secret.Name, "namespace", ns, "err", err)
		}
	}
}

func (s *Synchronizer) isAnnotated(secret *corev1.Secret) bool {
	v, ok := secret.Annotations[s.cfg.AnnotationKey]
	return ok && v == s.cfg.AnnotationValue
}

func buildSecret(src *corev1.Secret, targetNS string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      src.Name,
			Namespace: targetNS,
			Labels:    src.Labels,
			Annotations: map[string]string{
				managedByAnnotation: "secretweave",
				sourceNSAnnotation:  src.Namespace,
			},
		},
		Type:       src.Type,
		Data:       src.Data,
		StringData: src.StringData,
	}
}

func dataEqual(a, b map[string][]byte) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		bv, ok := b[k]
		if !ok || !bytes.Equal(v, bv) {
			return false
		}
	}
	return true
}

func extractSecret(obj interface{}) *corev1.Secret {
	switch v := obj.(type) {
	case *corev1.Secret:
		return v
	case cache.DeletedFinalStateUnknown:
		s, ok := v.Obj.(*corev1.Secret)
		if ok {
			return s
		}
	}
	return nil
}

func countAnnotated(secrets []corev1.Secret, isAnnotated func(*corev1.Secret) bool) int {
	n := 0
	for i := range secrets {
		if isAnnotated(&secrets[i]) {
			n++
		}
	}
	return n
}
