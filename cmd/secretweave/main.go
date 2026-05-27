package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/blazingbrainz/secretweave/internal/config"
	"github.com/blazingbrainz/secretweave/internal/logger"
	"github.com/blazingbrainz/secretweave/internal/synchronizer"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

func main() {
	cfg := config.Load()
	log := logger.New(cfg.LogDir, cfg.LogRetentionDays)

	log.Info("starting secretweave",
		"parent_namespace", cfg.ParentNamespace,
		"annotation_key", cfg.AnnotationKey,
		"sync_interval", cfg.SyncInterval,
		"full_sync_interval", cfg.FullSyncInterval,
		"namespace_filter", cfg.NamespaceFilter,
		"workers", cfg.WorkerCount,
	)

	k8sCfg, err := buildConfig()
	if err != nil {
		log.Error("failed to build kubernetes config", "err", err)
		os.Exit(1)
	}

	client, err := kubernetes.NewForConfig(k8sCfg)
	if err != nil {
		log.Error("failed to create kubernetes client", "err", err)
		os.Exit(1)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	s := synchronizer.New(client, cfg, log)
	if err := s.Run(ctx); err != nil {
		log.Error("synchronizer exited with error", "err", err)
		os.Exit(1)
	}
}

// buildConfig returns an in-cluster config when running inside a Pod.
// For local development, set KUBE_APISERVER (e.g. http://127.0.0.1:8001 via
// kubectl proxy) or KUBERNETES_SERVICE_HOST / KUBERNETES_SERVICE_PORT to
// point at a reachable API server.
func buildConfig() (*rest.Config, error) {
	if cfg, err := rest.InClusterConfig(); err == nil {
		return cfg, nil
	}
	host := os.Getenv("KUBE_APISERVER")
	if host == "" {
		host = "http://127.0.0.1:8001" // default kubectl proxy address
	}
	return &rest.Config{Host: host}, nil
}
