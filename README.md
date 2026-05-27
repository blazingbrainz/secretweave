# SecretWeave

**SecretWeave** is a lightweight Kubernetes operator that keeps Secrets in sync across namespaces. Annotate a Secret once in a parent namespace and SecretWeave automatically propagates it — and every subsequent update — to every namespace in the cluster, or to a controlled subset of them.

---

## Why SecretWeave

Multi-tenant clusters routinely need the same credentials in many namespaces: registry pull secrets, TLS certificates, database passwords, API tokens. Without automation you either duplicate Secrets by hand (drift-prone) or write ad-hoc scripts that break silently.

SecretWeave solves this with a single annotation:

```yaml
metadata:
  annotations:
    secretweave.io/sync: "true"
```

From that point on, the Secret is the single source of truth. SecretWeave handles creation, updates, and — optionally — deletion across all target namespaces, with a configurable worker pool sized for clusters with thousands of namespaces.

---

## How it works

- **Secret watch** — a namespace-scoped informer reacts to add/update/delete events on annotated Secrets in the parent namespace immediately.
- **Namespace watch** — a cluster-scoped informer detects new namespace creation and seeds all annotated Secrets into it straight away, without waiting for the next poll tick. Deleted namespaces require no action — Kubernetes removes their resources automatically.
- **Poll** — a configurable interval re-checks for newly annotated Secrets (default 30 s).
- **Full sync** — a separate, longer interval re-applies every annotated Secret to every target namespace to correct any drift caused by manual edits (default 5 m).
- **One-way only** — changes in target namespaces are overwritten on the next sync; the parent namespace is always authoritative.
- **Namespace targeting** — an optional allowlist (`includeNamespaces`) and denylist (`excludeNamespaces`) give precise control over which namespaces receive secrets; both default to empty (sync to all).

---

## Getting started

### Prerequisites

- Kubernetes 1.24+
- Helm 3.x
- A namespace to act as the secret source (default: `default`)

### 1. Annotate a Secret in the parent namespace

```bash
kubectl annotate secret my-registry-secret secretweave.io/sync=true
```

Or add the annotation in the manifest:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: my-registry-secret
  namespace: default
  annotations:
    secretweave.io/sync: "true"
type: kubernetes.io/dockerconfigjson
data:
  .dockerconfigjson: <base64-encoded-config>
```

### 2. Install SecretWeave via Helm

```bash
helm install secretweave oci://ghcr.io/blazingbrainz/helm-charts/secretweave \
  -n secretweave --create-namespace
```

Or pull the chart first and inspect before installing:

```bash
helm pull oci://ghcr.io/blazingbrainz/helm-charts/secretweave --untar
helm install secretweave ./secretweave -n secretweave --create-namespace
```

### 3. Override common values at install time

```bash
# Sync from a custom parent namespace, restrict to specific target namespaces
helm install secretweave oci://ghcr.io/blazingbrainz/helm-charts/secretweave \
  -n secretweave --create-namespace \
  --set parentNamespace=platform \
  --set includeNamespaces={team-a,team-b,team-c} \
  --set syncInterval=60s

# Sync to all namespaces except system ones
helm install secretweave oci://ghcr.io/blazingbrainz/helm-charts/secretweave \
  -n secretweave --create-namespace \
  --set excludeNamespaces={kube-system,kube-public,monitoring}
```

### 4. Verify

```bash
# Check the pod is running
kubectl get pods -n secretweave

# Confirm a target namespace received the Secret
kubectl get secret my-registry-secret -n <target-namespace>

# Stream logs
kubectl logs -n secretweave -l app.kubernetes.io/name=secretweave -f
```

---

## Artifacts

| Artifact | Registry path |
|---|---|
| Docker image | `ghcr.io/blazingbrainz/secretweave` |
| Helm chart (OCI) | `oci://ghcr.io/blazingbrainz/helm-charts/secretweave` |

```bash
# Pull the Docker image
docker pull ghcr.io/blazingbrainz/secretweave:0.1.0

# Pull the Helm chart
helm pull oci://ghcr.io/blazingbrainz/helm-charts/secretweave --version 0.1.0
```

---

## Configuration

All values can be set via `--set` or a custom `values.yaml`.

| Value | Default | Description |
|---|---|---|
| `parentNamespace` | `default` | Namespace SecretWeave watches for annotated Secrets |
| `annotationKey` | `secretweave.io/sync` | Annotation key that marks a Secret for syncing |
| `annotationValue` | `true` | Required value of the annotation |
| `syncInterval` | `30s` | How often to poll for new annotated Secrets |
| `fullSyncInterval` | `5m` | How often to re-sync all Secrets (drift correction) |
| `includeNamespaces` | `[]` | Allowlist of namespace names to sync to; empty = all namespaces |
| `excludeNamespaces` | `[]` | Denylist of namespace names to never sync to; applied after `includeNamespaces`; empty = nothing blocked |
| `workerCount` | `20` | Parallel sync workers (increase for large clusters) |
| `deleteOnRemove` | `true` | Delete target Secrets when the source Secret is deleted |
| `log.dir` | `/var/log/secretweave` | Directory for daily rotated log files |
| `log.retentionDays` | `30` | Days to keep log files; `0` disables cleanup |
| `persistence.enabled` | `true` | Mount a PVC for log files |
| `persistence.size` | `1Gi` | PVC size |
| `persistence.storageClass` | `""` | Storage class; empty uses the cluster default |

### Namespace targeting rules

Both lists are optional and applied in order:

| `includeNamespaces` | `excludeNamespaces` | Result |
|---|---|---|
| `[]` | `[]` | all namespaces |
| `[team-a, team-b]` | `[]` | only `team-a` and `team-b` |
| `[]` | `[kube-system, monitoring]` | all namespaces except those two |
| `[team-a, team-b, team-c]` | `[team-c]` | `team-a` and `team-b` only |

---

## Troubleshooting

### A new namespace did not receive secrets immediately

SecretWeave watches for namespace creation events via a cluster-scoped informer and seeds all annotated Secrets as soon as a new namespace is detected. If the pod was starting up at the exact moment the namespace was created, the next `syncInterval` tick (default 30 s) will catch it.

For namespace deletions, no action is required — Kubernetes automatically removes all resources inside a terminating namespace, including any Secrets SecretWeave placed there.

### Secrets are not appearing in target namespaces

1. Confirm the annotation is present and spelled correctly:
   ```bash
   kubectl get secret <name> -n <parent-ns> -o jsonpath='{.metadata.annotations}'
   ```
2. Check SecretWeave logs for errors:
   ```bash
   kubectl logs -n secretweave -l app.kubernetes.io/name=secretweave
   ```
3. Verify the service account has the required RBAC permissions:
   ```bash
   kubectl auth can-i create secrets --as=system:serviceaccount:secretweave:secretweave -n <target-ns>
   ```

### A namespace is unexpectedly excluded

Check the active `includeNamespaces` and `excludeNamespaces` values in the running pod:
```bash
kubectl exec -n secretweave deploy/secretweave -- env | grep -E 'INCLUDE|EXCLUDE'
```
A namespace must appear in `includeNamespaces` (if the list is non-empty) **and** must not appear in `excludeNamespaces` to receive secrets.

### Pod is crash-looping

The most common cause is missing RBAC. Ensure the Helm release created the ClusterRole and ClusterRoleBinding:
```bash
kubectl get clusterrole,clusterrolebinding | grep secretweave
```

### Log files are not being written

Confirm the PVC is bound and mounted:
```bash
kubectl describe pod -n secretweave -l app.kubernetes.io/name=secretweave | grep -A5 Volumes
kubectl get pvc -n secretweave
```

---

## Building from source

```bash
# Build binary
make build

# Run tests
make test

# Build and push Docker image
make publish GITHUB_USERNAME=<user> GITHUB_PAT=<token>

# Or load credentials from .env
echo "GITHUB_USERNAME=myuser" > .env
echo "GITHUB_PAT=ghp_xxxx"   >> .env
make publish
```

---

## License

Apache License 2.0 — see [LICENSE](LICENSE).
