# AGENTS.md — instructions for AI/LLM agents

This repo is an **app** using the homelab's standard deployment wiring. When an
agent is asked to build/change/deploy the app here, follow these rules.

## Structure

```
app/                    # your application source (replace the placeholder)
Dockerfile              # builds the container image (must expose $8080)
deploy/k8s/             # kustomize manifests ArgoCD applies
  ├── namespace.yaml
  ├── deployment.yaml    # image tag is bumped by CI — DO NOT hand-edit
  ├── service.yaml
  ├── ingress.yaml       # host = status.bismigel.dev
  ├── external-secret.yaml  # (optional) secret from Bitwarden via ESO
  └── kustomization.yaml
.github/workflows/
  ├── check.yml          # builds image on PR (must pass to merge)
  └── release.yml        # on main: build -> GHCR -> bump deploy/k8s tag
scripts/init.sh          # one-time placeholder replacement
```

## Rules

1. **Do the app work in `app/`** (and the `Dockerfile`). Don't touch CI or
   `deploy/k8s` unless the app needs a new port, env var, volume, or hostname.
2. **Never hand-edit the image tag** in `deploy/k8s/deployment.yaml` — CI owns it.
3. **Never commit plaintext secrets.** Use an `ExternalSecret` (Bitwarden via ESO) — see README.
4. If the app listens on a different port, update `deploy/k8s/deployment.yaml`
   (`containerPort`) and `service.yaml` (`targetPort`), and `Dockerfile` `EXPOSE`.
5. Keep `INGRESS_CLASS = nginx`; ingress is served by ingress-nginx on the host.

## Deploy flow (already wired — don't reinvent)

```
push to main
  └─ release.yml: docker build ─▶ GHCR ─▶ bump tag in deploy/k8s/deployment.yaml
                                              └─ ArgoCD (bakuran-gitops) syncs the new tag
```

To publish the app for the first time, add an `Application` in `bakuran-gitops`
(`applications/sitaw/<name>.yaml`). See the README.

## Validate before pushing

```bash
docker build -t app-test .          # image builds
kubectl kustomize deploy/k8s        # manifests render
```

## Environment variables

Put non-secret config in `deploy/k8s/deployment.yaml` under `env:`.
Secrets: create a `secretKeyRef` and an `ExternalSecret` (Bitwarden via ESO).
