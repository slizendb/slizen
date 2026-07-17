# Public Release Checklist

Use this checklist before publishing `v0.2.0`.

The canonical identity is `github.com/slizendb/slizen` with images under
`ghcr.io/slizendb/slizen`. The repository transfer, legacy redirect, and local
remote were verified on 2026-07-18.

- [x] Canonical public repository, Go module, and image identity resolved.
- [ ] Immutable multi-architecture container image built and pushed to the canonical registry.
- [ ] Helm and raw-manifest image references resolve, and the release digest is recorded.
- [ ] GHCR package is linked to `slizendb/slizen` with intended visibility and Actions access.
- [x] CI green on GitHub.
- [x] Docker Compose smoke green.
- [x] Kubernetes raw manifests and Helm render validation green.
- [x] Demo-report artifact generated.
- [ ] README commands verified.
- [x] `make release-check` green.
- [ ] Version tag created.
- [ ] Release notes pasted from `docs/RELEASE_NOTES_v0.2.md`.
- [ ] Known limitations included in the GitHub Release.
- [x] Demo-report artifact attached if CI generated it.
