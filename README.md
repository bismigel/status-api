# status-api

Template app for the Bakuran homelab. Everything needed to build and deploy is
already wired: **CI → GHCR → ArgoCD**. You write the app; this repo handles the rest.

## Quick start

1. Create a repo from this template (or copy it).
2. Run the initializer to set your app's name/domain/port:
   ```bash
   ./scripts/init.sh myapp myapp.example.com 80
   ```
3. Replace the placeholder in `app/` and the `Dockerfile` with your real app.
4. Push to `main` → CI builds an image, pushes to GHCR, and bumps the tag in
   `deploy/k8s/deployment.yaml`.
5. Ask ArgoCD to deploy it by adding `applications/sitaw/status-api.yaml` in
   **`bakuran-gitops`** (see "Register with ArgoCD" below).

## What's pre-wired

| Concern | File |
| --- | --- |
| Container image | `Dockerfile`, `.dockerignore` |
| Kubernetes | `deploy/k8s/` (Namespace, Deployment, Service, Ingress) |
| CI: PR build check | `.github/workflows/check.yml` |
| CI: release (build+push+bump) | `.github/workflows/release.yml` |
| Agent/LLM instructions | `AGENTS.md` |

## Register with ArgoCD

In `bakuran-gitops`, add `applications/sitaw/status-api.yaml`:

```yaml
apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: status-api
  namespace: argocd
  annotations:
    argocd.argoproj.io/sync-wave: "1"
  finalizers: [resources-finalizer.argocd.argoproj.io]
spec:
  project: default
  source:
    repoURL: https://github.com/<owner>/<this-repo>
    targetRevision: main
    path: deploy/k8s
  destination:
    server: https://kubernetes.default.svc
    namespace: apps
  syncPolicy:
    automated: { prune: true, selfHeal: true }
    syncOptions: [CreateNamespace=true]
```

## Image & registry

CI pushes `ghcr.io/<owner>/<repo>:main-<timestamp>-<sha>` and `:latest`.
The first push creates a **private** package — make it public (or add an
`imagePullSecrets` entry) so the cluster can pull it.

## Secrets

Never commit secrets. Cluster secrets come from **Bitwarden Secrets Manager via
External Secrets Operator** (`ExternalSecret` referencing the `bitwarden`
ClusterSecretStore). See `homelab-app-template` for the snippet. Sealed Secrets is
no longer used.
