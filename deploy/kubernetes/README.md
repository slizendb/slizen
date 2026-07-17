# Kubernetes packaging

`observe-sidecar.yaml` is a concrete, runnable parent Deployment containing an
example Redis client and a Slizen sidecar. It is a pattern, not an injector:
replace `example-app` with the real application, preserve the Slizen container
and ConfigMap, and change the application's Redis endpoint to
`127.0.0.1:6380`. The admin API is loopback-only and has no Service.
Because the config uses a `subPath` mount, bump the Pod-template annotation
`slizen.dev/config-revision` on every configuration or policy change.

Helm cannot mutate an existing Deployment. `charts/slizen` instead deploys a
standalone, cluster-internal proxy Service. Slizen v0.2 does not ship an
Operator, admission webhook, or automatic sidecar injection.

Follow `docs/STAGING_ROLLOUT.md` for compatibility checks, canary rollout, and
the rollback order. Do not apply the example unchanged: set the upstream
address and pin the Slizen image to a release digest first.
