# status-api

## Why "Bakuran"

Bakuran is Tagalog for the yard or enclosure behind a house, from *bakod*, a
fence. It is the space where things get built, repaired, and occasionally taken
apart to see how they work. This small service is part of that backyard lab:
personal, observable, and safe to break on purpose.

A Go HTTP service backing `status.bismigel.dev`. It is deliberately small: a
single JSON response and a health endpoint.

## Endpoints

| Path | Response |
| --- | --- |
| `/` | JSON `{status, message, host}` |
| `/healthz` | `ok` |

`PORT` defaults to `8080`.

## Layout

```text
app/                      Go service
deploy/base/              shared Deployment and Service
deploy/overlays/prod/     production ingress for status.bismigel.dev
deploy/overlays/staging/  staging overlay
```

ArgoCD watches `deploy/overlays/prod` through
`bakuran-gitops/applications/sitaw/status-api.yaml`. The image is built by
GitHub Actions and pushed to GHCR.

## Local

```bash
cd app
go run .
curl http://localhost:8080/
```

## Related

- `bakuran-gitops`: ArgoCD Application.
- `bakuran-infra`: hosts and edge.
