## What changed

Describe the measured failure mode or user workflow this change addresses.

## Safety boundaries

- [ ] Redis or Valkey remains the source of truth.
- [ ] Memory, input, and telemetry cardinality remain bounded.
- [ ] No cached values, credentials, or raw sensitive keys are logged or exposed as metric labels.
- [ ] User-visible compatibility or consistency changes are documented.

## Verification

- [ ] `make check`
- [ ] `make validate-k8s` when packaging changes
- [ ] `make release-check` when release, proxy, cache, or workload behavior changes
- [ ] Benchmark evidence is included for hot-path changes

Paste the relevant results and describe anything that was not run.
