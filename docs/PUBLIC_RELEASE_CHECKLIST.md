# Public Release Checklist

This checklist records the completed `v0.2.x` public releases.

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

Status: released on 2026-07-22. The annotated `v0.2.1` tag resolves to commit `4ba2c1c5c9a1c89073ba47d90c83f98441dfe9a1`; all published image aliases resolve to index digest `sha256:4006733aa64b6f55f25855f48a026d7b488ed44b5ad82d1a52ef5968d08daece`. Image-bound and 100,000-key evidence is attached to the [GitHub Release](https://github.com/slizendb/slizen/releases/tag/v0.2.1).

- [x] Observe-first and privacy-safe defaults are reflected in binary, example config, Docker, Helm, raw Kubernetes, and docs.
- [x] Request/connection bounds, stale-grace retention, hotness summary accounting, and value-verified workload evidence have regression coverage.
- [x] Canonical Apache-2.0 `LICENSE`, `NOTICE`, OCI license metadata, pinned base-image digests, pinned Actions, and Dependabot configuration are present.
- [x] Release image workflow waits for the tagged-source gate, publishes provenance/SBOM, creates a GitHub-native attestation, and produces evidence from the exact published digest.
- [x] GHCR install, design-partner CTA, issue forms, pull-request template, CODEOWNERS, support path, and private security-report link are documented.
- [x] Clean `make check`, `make validate-k8s`, and `make release-check` pass at the intended release commit.
- [x] Manually dispatch `extended-validation`; preserve the five-run 100,000-cardinality benchmarks and 100,000-key workload artifact without treating runner latency as a release threshold.
- [x] Public GitHub CI passes for that commit.
- [x] Main branch rules require the green CI checks and prevent deletion/force-push while retaining a practical owner bypass for the solo maintainer.
- [x] Dependabot alerts/security updates, secret scanning/push protection, and private vulnerability reporting are enabled in GitHub settings.
- [x] Repository topics are configured, and the 1280×640 social preview asset is versioned at [`.github/assets/social-preview.png`](../.github/assets/social-preview.png).
- [x] Annotated `v0.2.1` tag, GitHub Release, GHCR aliases/digest, attestation verification, and immutable-image evidence artifact are verified publicly.
- [x] The release evidence bundle is attached to the GitHub Release and its exact digest is copied into the staging guide.

Non-blocking UI polish: upload the versioned social preview asset in GitHub repository settings. GitHub does not expose this mutation through its public REST or GraphQL API.
