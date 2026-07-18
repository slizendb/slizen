# Public Release Checklist

This checklist records the completed `v0.2.0` public release.

The canonical identity is `github.com/slizendb/slizen` with images under
`ghcr.io/slizendb/slizen`. The repository transfer, legacy redirect, and local
remote were verified on 2026-07-18.

The public multi-architecture OCI image and all published version aliases resolve
to index digest
`sha256:3d38c65add8b95c54e0b62a91d5ee82d18ecd9f5d637dbdc69bb8cb76ffaf804`.

- [x] Canonical public repository, Go module, and image identity resolved.
- [x] Immutable multi-architecture container image built and pushed to the canonical registry.
- [x] Helm and raw-manifest image references resolve, and the release digest is recorded.
- [x] GHCR package is linked to `slizendb/slizen` with intended visibility and Actions access.
- [x] CI green on GitHub.
- [x] Docker Compose smoke green.
- [x] Kubernetes raw manifests and Helm render validation green.
- [x] Demo-report artifact generated.
- [x] README commands verified.
- [x] `make release-check` green.
- [x] Version tag created.
- [x] Release notes pasted from `docs/RELEASE_NOTES_v0.2.md`.
- [x] Known limitations included in the GitHub Release.
- [x] Demo-report artifact attached if CI generated it.
