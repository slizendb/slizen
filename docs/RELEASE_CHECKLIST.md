# Release Checklist

Current state: v0.2.3-rc.1 published as a staging prerelease on 2026-07-23. The annotated tag resolves to commit `7662a1fb5974a6fc369ca486d2a59c85f68cd3b7`; the verified multi-architecture image index is `sha256:e30ad22f4cb23462af9f05322ff97d6796fc521e2e80dc181c42107e4193b92a`.

## Before Release

- Confirm the worktree is clean.
- Confirm `README.md`, `README.ru.md`, `START_HERE_RU.md`, and `AGENT_PROMPT.md` match the real commands.
- Confirm `slizen.example.toml` matches `internal/config.Config`.
- Confirm limitations still say developer preview, single-node, and not a source of truth.

## Local Checks

```sh
go fmt ./...
go vet ./...
go test ./...
go test -race ./...
go build ./...
make check
make release-check
```

## Docker Checks

```sh
make demo-up
make smoke
make demo
make demo-down
```

## Kubernetes Packaging

```sh
make validate-k8s
```

Confirm the raw sidecar example and default Helm render use `observe` mode, loopback admin access, bounded resources, exec probes, and a documented endpoint rollback. Confirm the chart renders no `ServiceMonitor` unless explicitly enabled.

The chart and raw manifest in the tagged source may still default to the latest
previously published stable image (currently v0.2.2). This is intentional:
prerelease publication does not move stable source defaults. The publish job
derived the exact v0.2.3-rc.1 digest from the successful registry push and ran
`scripts/package_release_artifacts.sh` from the clean tagged checkout. The
packager verifies that the supplied full commit equals both `HEAD` and the
`v0.2.3-rc.1` tag before copying source into its private staging directory.

## Benchmark

```sh
make demo-up
make benchmark
make benchmark-workload
make demo-report
cat ./tmp/demo-report.md
make demo-down
```

Check that the workload JSON has four scenarios, runtime versions, an invocation-specific key prefix, explicit `evidence_valid` fields, and zero `value_mismatches`, `validation_failures`, and `validation_mismatches` in both phases. Use an exclusive Slizen process and origin. Every phase must use `upstream_gets_source="origin_info_commandstats"` and the compared phases must share one non-empty `origin_run_id`. Direct physical GETs must equal direct reads; Slizen physical GETs must equal the separate logical `slizen_status_upstream_gets` delta. Cache hit ratio and miss reasons still come from `/v1/status`. Confirm `cache_misses_policy_bypass`, `cache_misses_not_admitted`, and `cache_misses_not_present` are present and sum to each Slizen phase's cache misses.

`make release-check` enforces this benchmark as a release gate against its own Docker Compose stack: all four expected scenario names must appear exactly once, the Slizen version and full commit must match the built image and CLI, the origin version must be known, the isolated prefix must be invocation-specific, and every scenario must have valid isolated evidence with zero value mismatches. Every origin and Slizen phase must stop at exactly 100,000 generated operation attempts (`request_limit`), with 30 seconds retained only as a safety cap; the gate also checks that attributed read/write samples equal those attempts and that final-validation samples equal validation reads. The moving-flash scenario must retain the configured 20,000-operation movement interval. The stable 99/1 skew must additionally have real cache hits and `proved_origin_get_reduction=true`; a uniform or rapidly moving workload is allowed to report no win without invalidating otherwise sound evidence. The release gate uses 1,000 keys, concurrency 32, and no flaky latency threshold. Helm, Docker Compose, a running Docker daemon, and `jq` are required; none of these checks are skipped.

The pre-release v0.2.3-rc.1 check also has five historical local Docker repeats of the unchanged cold request-bound `skew-99-1` workload: seed 42, 1,000 keys, 100,000 generated operations, 95/5 reads/writes, 128-byte values, and concurrency 32. Direct phases had 94,961 successful GETs; Slizen recorded 798–803 logical upstream GET calls, a 99.154390–99.159655% proxy-side avoidance estimate, with a 99.121745–99.151231% hit ratio and zero failures or mismatches. Those runs predate physical `commandstats` capture. Slizen read p99 was 1.175–1.251 ms versus 0.986–1.042 ms direct. Preserve this as local candidate evidence only; the release still requires regenerated exact-image commandstats-backed evidence and makes no speed, physical-origin, or universal 99% claim.

After tagged-source validation succeeds, the release workflow publishes the image and runs `scripts/release_evidence.sh` against its exact `ghcr.io/slizendb/slizen@sha256:...` reference. That job must produce:

- `slizen-0.2.3-rc.1.tgz`, with Helm chart version, application version, image tag,
  rendered version label, and exact image digest all bound to v0.2.3-rc.1;
- `slizen-observe-sidecar-0.2.3-rc.1.yaml`, with the Slizen container pinned to that
  same digest;
- `release-evidence-manifest.json`, whose `deployment_artifacts` entries bind
  both files and their SHA-256 values to the version, full source commit, and
  image digest;
- `SHA256SUMS`, covering both deployment artifacts and all evidence files.

Verify the generated chart and raw manifest appear in the uploaded
`slizen-0.2.3-rc.1-release-evidence` Actions artifact. Do not mutate or retag the
source commit after publication to make its stable defaults look like the
generated release artifacts.

## Docs

- `docs/REDIS_COMPATIBILITY.md` matches command handling.
- `docs/BENCHMARKING.md` explains how to reproduce the benchmark.
- `docs/STAGING_ROLLOUT.md` contains observe-to-cache and rollback gates.
- `docs/RELEASE_NOTES_v0.2.3-rc.1.md` records the published prerelease identity, evidence, and limitations.

## Tag

```sh
git tag -a v0.2.3-rc.1 -m "Slizen v0.2.3-rc.1"
git push origin v0.2.3-rc.1
```

## GitHub Release Notes

The [GitHub prerelease](https://github.com/slizendb/slizen/releases/tag/v0.2.3-rc.1) uses `docs/RELEASE_NOTES_v0.2.3-rc.1.md` and contains `slizen-0.2.3-rc.1.tgz`, `slizen-observe-sidecar-0.2.3-rc.1.yaml`, `release-evidence-manifest.json`, `SHA256SUMS`, and the remaining immutable-image evidence files.

Verify GitHub-native provenance for the image and both deployment artifacts:

```sh
export RELEASE_DIGEST=sha256:e30ad22f4cb23462af9f05322ff97d6796fc521e2e80dc181c42107e4193b92a
gh attestation verify "oci://ghcr.io/slizendb/slizen@$RELEASE_DIGEST" \
  --repo slizendb/slizen
gh attestation verify ./slizen-0.2.3-rc.1.tgz \
  --repo slizendb/slizen
gh attestation verify ./slizen-observe-sidecar-0.2.3-rc.1.yaml \
  --repo slizendb/slizen
```

## Known Limitations

- Developer preview.
- Single-node only.
- Redis or Valkey remains the source of truth.
- Cache and hotness state are disposable.
- Direct upstream writes can remain stale until local TTL expiration.
- Admin API has no built-in authentication.
- Limited Redis command compatibility.
