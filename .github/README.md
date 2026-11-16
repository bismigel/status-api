# A shop window, not a CI system

Since 2026-11-16 the source of truth is **Forgejo** (`git.bismigel.dev`); GitHub receives every push through a push
mirror. CI lives in `.forgejo/workflows/` and runs on a runner inside the homelab cluster.

The GitHub workflows were deleted rather than disabled: GitHub Actions' monthly minutes are spent, two CI systems is two
places to look, and a disabled workflow is one somebody re-enables by accident. They remain in the history:
`git log -- .github/workflows`.
