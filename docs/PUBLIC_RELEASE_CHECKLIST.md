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

## v0.2.1 launch hardening

Status: in progress. This section does not alter the completed v0.2.0 record above.

- [x] Observe-first and privacy-safe defaults are reflected in binary, example config, Docker, Helm, raw Kubernetes, and docs.
- [x] Request/connection bounds, stale-grace retention, hotness summary accounting, and value-verified workload evidence have regression coverage.
- [x] Canonical Apache-2.0 `LICENSE`, `NOTICE`, OCI license metadata, pinned base-image digests, pinned Actions, and Dependabot configuration are present.
- [x] Release image workflow waits for the tagged-source gate, publishes provenance/SBOM, creates a GitHub-native attestation, and produces evidence from the exact published digest.
- [x] GHCR install, design-partner CTA, issue forms, pull-request template, CODEOWNERS, support path, and private security-report link are documented.
- [ ] Clean `make check`, `make validate-k8s`, and `make release-check` pass at the intended release commit.
- [ ] Manually dispatch `extended-validation`; preserve the five-run 100,000-cardinality benchmarks and 100,000-key workload artifact without treating runner latency as a release threshold.
- [ ] Public GitHub CI passes for that commit.
- [ ] Main branch rules require the green CI checks and prevent deletion/force-push while retaining a practical owner bypass for the solo maintainer.
- [ ] Dependabot alerts/security updates, secret scanning/push protection, and private vulnerability reporting are enabled in GitHub settings.
- [ ] Repository topics and a 1280×640 social preview are configured in GitHub settings.
- [ ] Annotated `v0.2.1` tag, GitHub Release, GHCR aliases/digest, attestation verification, and immutable-image evidence artifact are verified publicly.
- [ ] The release evidence bundle is attached to the GitHub Release and its exact digest is copied into the staging guide.
