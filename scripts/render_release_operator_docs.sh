#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

: "${SLIZEN_IMAGE:?set SLIZEN_IMAGE to ghcr.io/slizendb/slizen@sha256:...}"
: "${SLIZEN_VERSION:?set SLIZEN_VERSION to the release version without v}"
: "${SLIZEN_COMMIT:?set SLIZEN_COMMIT to the full tagged release commit}"

if [[ ! "${SLIZEN_VERSION}" =~ ^[0-9]+\.[0-9]+\.[0-9]+([.-][0-9A-Za-z.-]+)?$ ]]; then
  echo "SLIZEN_VERSION is not a supported release version: ${SLIZEN_VERSION}" >&2
  exit 1
fi
if [[ ! "${SLIZEN_IMAGE}" =~ ^ghcr\.io/slizendb/slizen@sha256:[0-9a-f]{64}$ ]]; then
  echo "SLIZEN_IMAGE must be the canonical image at an exact sha256 digest" >&2
  exit 1
fi
if [[ ! "${SLIZEN_COMMIT}" =~ ^[0-9a-f]{40}$ ]]; then
  echo "SLIZEN_COMMIT must be a full lowercase 40-hex commit" >&2
  exit 1
fi
if [[ "$#" -ne 1 ]]; then
  echo "usage: render_release_operator_docs.sh OUTPUT_DIRECTORY" >&2
  exit 1
fi

read_single_export() {
  local variable_name="$1"
  local source_file="$2"

  awk -v prefix="export ${variable_name}=" '
    index($0, prefix) == 1 {
      value = substr($0, length(prefix) + 1)
      count++
    }
    END {
      if (count != 1 || value == "") {
        exit 42
      }
      print value
    }
  ' "${source_file}"
}

source_identity_file="${ROOT_DIR}/docs/STAGING_QUICKSTART.md"
if ! source_stable_version="$(read_single_export STABLE_VERSION "${source_identity_file}")" ||
  ! source_stable_commit="$(read_single_export STABLE_COMMIT "${source_identity_file}")" ||
  ! source_stable_digest="$(read_single_export STABLE_DIGEST "${source_identity_file}")"; then
  echo "could not resolve exactly one source stable identity from ${source_identity_file}" >&2
  exit 1
fi
source_chart_version="$(
  awk '
    /^version:[[:space:]]*/ {
      print $2
      count++
    }
    END {
      if (count != 1) {
        exit 42
      }
    }
  ' "${ROOT_DIR}/charts/slizen/Chart.yaml"
)" || {
  echo "could not resolve exactly one source candidate version" >&2
  exit 1
}

if [[ ! "${source_stable_version}" =~ ^[0-9]+\.[0-9]+\.[0-9]+([.-][0-9A-Za-z.-]+)?$ ]]; then
  echo "source STABLE_VERSION is invalid: ${source_stable_version}" >&2
  exit 1
fi
if [[ ! "${source_stable_commit}" =~ ^[0-9a-f]{40}$ ]]; then
  echo "source STABLE_COMMIT is invalid: ${source_stable_commit}" >&2
  exit 1
fi
if [[ ! "${source_stable_digest}" =~ ^sha256:[0-9a-f]{64}$ ]]; then
  echo "source STABLE_DIGEST is invalid: ${source_stable_digest}" >&2
  exit 1
fi
if [[ ! "${source_chart_version}" =~ ^[0-9]+\.[0-9]+\.[0-9]+([.-][0-9A-Za-z.-]+)?$ ]]; then
  echo "source chart version is invalid: ${source_chart_version}" >&2
  exit 1
fi
source_candidate_version="${source_chart_version%%-*}"

output_dir="$1"
if [[ -e "${output_dir}" ]]; then
  echo "operator documentation output already exists: ${output_dir}" >&2
  exit 1
fi

image_digest="${SLIZEN_IMAGE##*@}"
mkdir -p \
  "${output_dir}/docs" \
  "${output_dir}/deploy/kubernetes" \
  "${output_dir}/deploy/observability"

render_document() {
  local document_name="$1"
  local source_path="$2"
  local target_path="$3"

  awk \
    -v document_name="${document_name}" \
    -v version="${SLIZEN_VERSION}" \
    -v commit="${SLIZEN_COMMIT}" \
    -v image="${SLIZEN_IMAGE}" \
    -v digest="${image_digest}" \
    -v source_stable_version="${source_stable_version}" \
    -v source_stable_commit="${source_stable_commit}" \
    -v source_stable_digest="${source_stable_digest}" \
    -v source_candidate_version="${source_candidate_version}" '
    function replace_literal(text, old, replacement, position, result, rest) {
      if (old == "" || old == replacement) {
        return text
      }
      result = ""
      rest = text
      while ((position = index(rest, old)) != 0) {
        result = result substr(rest, 1, position - 1) replacement
        rest = substr(rest, position + length(old))
      }
      return result rest
    }

    function print_release_identity() {
      print "This operator copy is bound to the published Slizen v" version " artifacts:"
      print ""
      print "```text"
      print "version: v" version
      print "commit:  " commit
      print "image:   " image
      print "chart:   slizen-" version ".tgz"
      print "```"
      print ""
    }

    NR == 1 {
      print
      print ""
      print "> Generated from the tagged source for the immutable v" version
      print "> release. Use this bundled copy with `CHART_REF=./slizen-" version ".tgz`;"
      print "> do not substitute a source checkout or floating image reference."
      next
    }

    document_name == "STAGING_ROLLOUT.md" &&
      /^The stable public image is currently v[^:]+:$/ {
      print_release_identity()
      rollout_identity = 1
      next
    }
    rollout_identity {
      if (/^Read \[FAILURE_MODES\.md\]/) {
        rollout_identity = 0
      } else {
        next
      }
    }

    document_name == "FAILURE_MODES.md" &&
      /^The stable public image is currently v[^:]+:$/ {
      print_release_identity()
      print "The matrix below is the failure contract for this exact published release."
      print ""
      failure_identity = 1
      next
    }
    failure_identity {
      if (/^## Failure-mode matrix/) {
        failure_identity = 0
      } else {
        next
      }
    }

    document_name == "STAGING_RELEASE_GATE.md" &&
      /^The stable public image is v[^ ]+ at commit$/ {
      print_release_identity()
      gate_identity = 1
      next
    }
    gate_identity {
      if (/^## Result definitions/) {
        gate_identity = 0
      } else {
        next
      }
    }

    document_name == "STAGING_RELEASE_GATE.md" &&
      /^## Current candidate disposition/ {
      print "## Current release disposition"
      print ""
      print "This package has immutable runtime and chart identity. A self-service staging"
      print "**Pass** still requires a fresh operator who did not develop Slizen to complete"
      print "this gate without maintainer intervention. Engineering checks alone do not"
      print "establish that product result."
      print ""
      print "The bundled observability pack matches this release, including cache-limit"
      print "gauges, miss reasons, tracker-capacity telemetry, Go/process collectors, and"
      print "the active-connection gauge."
      print ""
      print "The executable procedure is in [STAGING_ROLLOUT.md](STAGING_ROLLOUT.md)."
      gate_tail = 1
      next
    }
    gate_tail {
      next
    }

    document_name == "OBSERVABILITY.md" && /^## Version scope/ {
      print
      print "## Version scope"
      print ""
      print "The bundled dashboard and rules match Slizen v" version ". This release exports"
      print "the core request, upstream, latency, cache, coalescing, invalidation, and"
      print "oversized-key series plus cache miss reasons, configured cache-limit gauges,"
      print "tracker-capacity telemetry, standard Go/process collectors, and active"
      print "downstream connections. A missing expected series is missing telemetry, not a"
      print "zero value."
      print ""
      observability_scope = 1
      next
    }
    observability_scope {
      if (/^## Expose metrics deliberately/) {
        observability_scope = 0
      } else {
        next
      }
    }

    document_name == "REDIS_COMPATIBILITY.md" &&
      /^Slizen v0\.2 is a Redis-compatible read proxy/ {
      print "Slizen v0.2 is a Redis-compatible read proxy for a deliberately small command"
      print "subset. Redis or Valkey remains the source of truth. The table below is the"
      print "complete command contract compiled into this published v" version " release;"
      print "any other command is classified as `unsupported`."
      print ""
      compatibility_intro = 1
      next
    }
    compatibility_intro {
      if (/^\| Command \| Status \|/) {
        compatibility_intro = 0
      } else {
        next
      }
    }

    document_name == "REDIS_COMPATIBILITY.md" &&
      /^No standalone CLI archive is published today\./ {
      print "No standalone CLI archive is published. Run the CLI embedded in the exact"
      print "published image above and retain its version plus compatibility report:"
      print ""
      compatibility_cli = 1
      next
    }
    compatibility_cli {
      if (/^```sh/) {
        compatibility_cli = 0
      } else {
        next
      }
    }

    document_name == "KUBERNETES_SIDECAR.md" &&
      /^The stable public image is v[^:]+:$/ {
      print_release_identity()
      sidecar_identity = 1
      next
    }
    sidecar_identity {
      if (/^Follow \[the staging runbook\]/) {
        sidecar_identity = 0
      } else {
        next
      }
    }

    document_name == "STAGING_QUICKSTART.md" &&
      /^Run the complete quickstart in one Bash session from a clean, pushed Slizen/ {
      print "Run the complete quickstart in one Bash session with the published chart archive:"
      quickstart_checkout = 1
      next
    }
    quickstart_checkout {
      if (/^checkout:$/) {
        quickstart_checkout = 0
      }
      next
    }

    document_name == "STAGING_QUICKSTART.md" &&
      /^The runtime and deployment source are separate identities:/ {
      print "The runtime and Helm chart in this archive are both generated from the tagged"
      print "release commit and pinned to the exact published image digest. Only set"
      print "`REVIEWED_HELM_VERSION` before this session when the platform owner has reviewed"
      print "that exact version'\''s `--atomic`, wait, and timeout behavior. Otherwise the"
      print "executable check stops the install."
      print ""
      quickstart_mapping = 1
      next
    }
    quickstart_mapping {
      if (/^## 2\. Prepare one reviewed values file/) {
        quickstart_mapping = 0
      } else {
        next
      }
    }

    document_name == "STAGING_QUICKSTART.md" &&
      /^Copy \[`charts\/slizen\/examples\/staging-values\.yaml`\]/ {
      print "Extract the bundled `slizen/examples/staging-values.yaml` member from"
      print "`$CHART_REF` into the team'\''s configuration repository. Replace all example"
      print "namespaces, Pod labels, and the origin address. The application peer must"
      print "identify only the intended canary Pods."
      quickstart_values = 1
      next
    }
    quickstart_values {
      if ($0 == "") {
        quickstart_values = 0
        print
      }
      next
    }

    document_name == "STAGING_QUICKSTART.md" &&
      /^test -z "\$\(git status --porcelain --untracked-files=all\)"$/ {
      print "test -s \"$CHART_REF\""
      print "export CHART_COMMIT=\"$SLIZEN_COMMIT\""
      skip_quickstart_git_lines = 3
      next
    }
    skip_quickstart_git_lines > 0 {
      skip_quickstart_git_lines--
      next
    }

    document_name == "STAGING_ROLLOUT.md" &&
      /^The chart checkout must be clean and its commit must exist/ {
      print "The published chart archive must match the release identity and remain"
      print "byte-for-byte unchanged throughout the trial:"
      chart_identity_prose = 1
      next
    }
    chart_identity_prose {
      if ($0 == "") {
        chart_identity_prose = 0
        print
      }
      next
    }

    document_name == "STAGING_ROLLOUT.md" &&
      /^test -z "\$\(git status --porcelain --untracked-files=all\)"$/ {
      chart_git_blocks++
      if (chart_git_blocks == 1) {
        print "test -s \"$CHART_REF\""
        print "export CHART_COMMIT=\"$SLIZEN_COMMIT\""
        print "export CHART_SHA256=\"$("
        print "  checksum256 \"$CHART_REF\" | awk '\''{print $1}'\''"
        print ")\""
        print "export CHART_REMOTE_REFS=\"release-tag:v$SLIZEN_VERSION\""
        skip_chart_git_lines = 3
        next
      }
      if (chart_git_blocks == 3) {
        print "test -s \"$CHART_REF\""
        print "test \"$(checksum256 \"$CHART_REF\" | awk '\''{print $1}'\'')\" = \"$CHART_SHA256\""
        print "test \"$CHART_COMMIT\" = \"$SLIZEN_COMMIT\""
        skip_chart_git_lines = 2
        next
      }
    }
    skip_chart_git_lines > 0 {
      skip_chart_git_lines--
      next
    }

    document_name == "STAGING_ROLLOUT.md" &&
      /^Stop if the remote-containment output does not identify/ {
      print "Record the chart archive hash and release tag with the staging evidence. The"
      print "environment values file must contain Secret references, never Secret contents."
      rollout_chart_evidence = 1
      next
    }
    rollout_chart_evidence {
      if ($0 == "") {
        rollout_chart_evidence = 0
        print
      }
      next
    }

    document_name == "STAGING_ROLLOUT.md" &&
      /^After v[^ ]+ is published, its image includes/ {
      print "This published release includes the offline command-catalog gate. Review the"
      print "exact argument shapes for commands reported with limitations before"
      print "acknowledging them. Use the exact image exported above and override its normal"
      print "`slizend` entrypoint:"
      rollout_compatibility = 1
      next
    }
    rollout_compatibility {
      if (/^```sh/) {
        rollout_compatibility = 0
      } else {
        next
      }
    }

    {
      line = $0
      line = replace_literal(line, source_stable_commit, commit)
      line = replace_literal(line, source_stable_digest, digest)
      line = replace_literal(line, "ghcr.io/slizendb/slizen@sha256:REPLACE_WITH_VERIFIED_PUBLISHED_DIGEST", image)
      line = replace_literal(line, "ghcr.io/slizendb/slizen@sha256:REPLACE_WITH_VERIFIED_DIGEST", image)
      line = replace_literal(line, "STABLE_VERSION", "SLIZEN_VERSION")
      line = replace_literal(line, "STABLE_COMMIT", "SLIZEN_COMMIT")
      line = replace_literal(line, "STABLE_DIGEST", "SLIZEN_DIGEST")
      line = replace_literal(line, "export SLIZEN_VERSION=" source_stable_version, "export SLIZEN_VERSION=" version)
      line = replace_literal(line, "./charts/slizen", "\"$CHART_REF\"")
      line = replace_literal(line, "`charts/slizen`", "`$CHART_REF`")
      line = replace_literal(line, "`deploy/kubernetes/observe-sidecar.yaml`", "`$OPERATOR_DOCS_DIR/deploy/kubernetes/observe-sidecar.yaml`")
      line = replace_literal(line, "`deploy/observability/grafana-dashboard.json`", "`$OPERATOR_DOCS_DIR/deploy/observability/grafana-dashboard.json`")
      line = replace_literal(line, "`deploy/observability/prometheus-rules.yaml`", "`$OPERATOR_DOCS_DIR/deploy/observability/prometheus-rules.yaml`")
      line = replace_literal(line, "`docs/STAGING_ROLLOUT.md`", "`$OPERATOR_DOCS_DIR/docs/STAGING_ROLLOUT.md`")
      line = replace_literal(line, "outside the Slizen chart checkout", "outside the extracted operator-doc directory")
      line = replace_literal(line, "the Slizen chart checkout", "the extracted operator-doc directory")
      line = replace_literal(line, "v" source_candidate_version " source-tree release candidate", "this published release")
      line = replace_literal(line, "v" source_candidate_version " source candidate", "this published release")
      line = replace_literal(line, "v" source_candidate_version " candidate", "this published release")
      line = replace_literal(line, "The stable v" source_stable_version, "An earlier v0.2 release")
      line = replace_literal(line, "Stable v" source_stable_version, "Earlier v0.2 release")
      line = replace_literal(line, "the stable v" source_stable_version, "an earlier v0.2 release")
      line = replace_literal(line, "The published v" source_stable_version, "An earlier published v0.2 release")
      line = replace_literal(line, "v" source_stable_version, "an earlier v0.2 release")
      line = replace_literal(line, "v" source_candidate_version, "v" version)
      line = replace_literal(line, "Candidate-only", "Release-specific")
      line = replace_literal(line, "candidate-only", "release-specific")
      line = replace_literal(line, "source-tree", "development-source")
      line = replace_literal(line, "unpublished", "pre-release")
      line = replace_literal(line, "candidate", "release")
      print line

      if ((document_name == "STAGING_QUICKSTART.md" ||
           document_name == "STAGING_ROLLOUT.md") &&
          line == "export SLIZEN_DIGEST=" digest) {
        print "export SLIZEN_IMAGE=" image
        print "export CHART_REF=./slizen-" version ".tgz"
        print "export OPERATOR_DOCS_DIR=./slizen/operator-docs"
      }
      if (document_name == "STAGING_QUICKSTART.md" &&
          line == "export REVIEWED_VALUES=/path/to/reviewed/slizen-staging-values.yaml") {
        print "tar -xOf \"$CHART_REF\" slizen/examples/staging-values.yaml > \"$REVIEWED_VALUES\""
      }
    }
  ' "${source_path}" > "${target_path}"
}

render_observability_asset() {
  local source_path="$1"
  local target_path="$2"

  awk \
    -v version="${SLIZEN_VERSION}" \
    -v source_stable_version="${source_stable_version}" \
    -v source_candidate_version="${source_candidate_version}" '
    function replace_literal(text, old, replacement, position, result, rest) {
      if (old == "" || old == replacement) {
        return text
      }
      result = ""
      rest = text
      while ((position = index(rest, old)) != 0) {
        result = result substr(rest, 1, position - 1) replacement
        rest = substr(rest, position + length(old))
      }
      return result rest
    }
    {
      line = $0
      line = replace_literal(line, "Core panels support v" source_stable_version "; candidate-only panels say so and show no series on v" source_stable_version ".", "All bundled panels match the published v" version " release.")
      line = replace_literal(line, "v" source_candidate_version " candidate and later", "v" version " and later")
      line = replace_literal(line, "the v" source_candidate_version " candidate", "v" version)
      line = replace_literal(line, "v" source_candidate_version " candidate", "v" version)
      line = replace_literal(line, "Stable v" source_stable_version " has only aggregate misses.", "This release also exports the detailed miss reasons.")
      line = replace_literal(line, "v" source_stable_version " still shows used bytes.", "This release exports both used bytes and the configured maximum.")
      line = replace_literal(line, "Oversized drops exist in v" source_stable_version "; capacity drops require v" version ".", "This release exports both oversized-key and tracker-capacity drops.")
      line = replace_literal(line, "works with v" source_stable_version " and", "works with v" version " and")
      line = replace_literal(line, "v" source_stable_version, "an earlier v0.2 release")
      line = replace_literal(line, "v" source_candidate_version, "v" version)
      line = replace_literal(line, "candidate-only", "release-specific")
      line = replace_literal(line, "candidate", "release")
      print line
    }
  ' "${source_path}" > "${target_path}"
}

render_document \
  "STAGING_QUICKSTART.md" \
  "${ROOT_DIR}/docs/STAGING_QUICKSTART.md" \
  "${output_dir}/docs/STAGING_QUICKSTART.md"
render_document \
  "STAGING_ROLLOUT.md" \
  "${ROOT_DIR}/docs/STAGING_ROLLOUT.md" \
  "${output_dir}/docs/STAGING_ROLLOUT.md"
render_document \
  "FAILURE_MODES.md" \
  "${ROOT_DIR}/docs/FAILURE_MODES.md" \
  "${output_dir}/docs/FAILURE_MODES.md"
render_document \
  "STAGING_RELEASE_GATE.md" \
  "${ROOT_DIR}/docs/STAGING_RELEASE_GATE.md" \
  "${output_dir}/docs/STAGING_RELEASE_GATE.md"
render_document \
  "REDIS_COMPATIBILITY.md" \
  "${ROOT_DIR}/docs/REDIS_COMPATIBILITY.md" \
  "${output_dir}/docs/REDIS_COMPATIBILITY.md"
render_document \
  "OBSERVABILITY.md" \
  "${ROOT_DIR}/docs/OBSERVABILITY.md" \
  "${output_dir}/docs/OBSERVABILITY.md"
render_document \
  "KUBERNETES_SIDECAR.md" \
  "${ROOT_DIR}/deploy/kubernetes/README.md" \
  "${output_dir}/deploy/kubernetes/README.md"

awk -v image="${SLIZEN_IMAGE}" '
  /^[[:space:]]*image:[[:space:]]*ghcr\.io\/slizendb\/slizen@sha256:/ {
    indent = $0
    sub(/image:.*/, "", indent)
    print indent "image: " image
    image_count++
    next
  }
  { print }
  END {
    if (image_count != 1) {
      exit 42
    }
  }
' "${ROOT_DIR}/deploy/kubernetes/observe-sidecar.yaml" \
  > "${output_dir}/deploy/kubernetes/observe-sidecar.yaml" || {
  echo "could not pin exactly one Slizen image in the bundled sidecar manifest" >&2
  exit 1
}

render_observability_asset \
  "${ROOT_DIR}/deploy/observability/grafana-dashboard.json" \
  "${output_dir}/deploy/observability/grafana-dashboard.json"
render_observability_asset \
  "${ROOT_DIR}/deploy/observability/prometheus-rules.yaml" \
  "${output_dir}/deploy/observability/prometheus-rules.yaml"

cat > "${output_dir}/RELEASE_IDENTITY.env" <<EOF
SLIZEN_VERSION=${SLIZEN_VERSION}
SLIZEN_COMMIT=${SLIZEN_COMMIT}
SLIZEN_IMAGE=${SLIZEN_IMAGE}
SLIZEN_DIGEST=${image_digest}
CHART_REF=./slizen-${SLIZEN_VERSION}.tgz
OPERATOR_DOCS_DIR=./slizen/operator-docs
EOF

if grep -ERiq \
  'candidate|unpublished|REPLACE_WITH_VERIFIED_(PUBLISHED_)?DIGEST|\./charts/slizen' \
  "${output_dir}"; then
  echo "release-bound operator documentation retained pre-publish identity or source paths" >&2
  exit 1
fi
for source_identity in \
  "v${source_stable_version}" \
  "${source_stable_commit}" \
  "${source_stable_digest}"; do
  if [[ "${source_identity}" == "v${SLIZEN_VERSION}" ||
    "${source_identity}" == "${SLIZEN_COMMIT}" ||
    "${source_identity}" == "${image_digest}" ]]; then
    continue
  fi
  if grep -FRq "${source_identity}" "${output_dir}"; then
    echo "release-bound operator documentation retained source stable identity ${source_identity}" >&2
    exit 1
  fi
done

grep -FRq "CHART_REF=./slizen-${SLIZEN_VERSION}.tgz" "${output_dir}" || {
  echo "release-bound operator documentation does not identify its chart archive" >&2
  exit 1
}
grep -FRq "${SLIZEN_COMMIT}" "${output_dir}" || {
  echo "release-bound operator documentation does not identify its release commit" >&2
  exit 1
}
grep -FRq "${SLIZEN_IMAGE}" "${output_dir}" || {
  echo "release-bound operator documentation does not identify its release image" >&2
  exit 1
}
