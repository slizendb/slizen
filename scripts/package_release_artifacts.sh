#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${ROOT_DIR}"

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

helm_bin="${HELM_BIN:-helm}"
git_bin="${GIT_BIN:-git}"
if ! command -v "${helm_bin}" >/dev/null 2>&1; then
  echo "missing required command: ${helm_bin}" >&2
  exit 1
fi
if ! command -v "${git_bin}" >/dev/null 2>&1; then
  echo "missing required command: ${git_bin}" >&2
  exit 1
fi

if [[ -n "$("${git_bin}" status --porcelain --untracked-files=all)" ]]; then
  echo "release artifacts require a clean source tree" >&2
  exit 1
fi
head_commit="$("${git_bin}" rev-parse HEAD)"
if [[ "${head_commit}" != "${SLIZEN_COMMIT}" ]]; then
  echo "HEAD ${head_commit} does not match SLIZEN_COMMIT ${SLIZEN_COMMIT}" >&2
  exit 1
fi
tag_commit="$("${git_bin}" rev-parse "v${SLIZEN_VERSION}^{commit}")"
if [[ "${tag_commit}" != "${SLIZEN_COMMIT}" ]]; then
  echo "tag v${SLIZEN_VERSION} does not resolve to SLIZEN_COMMIT ${SLIZEN_COMMIT}" >&2
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
  echo "could not resolve exactly one source chart version" >&2
  exit 1
}
if [[ "${source_chart_version}" != "${SLIZEN_VERSION}" ]]; then
  echo "source chart version ${source_chart_version} does not match SLIZEN_VERSION ${SLIZEN_VERSION}" >&2
  exit 1
fi

artifact_dir="${SLIZEN_ARTIFACT_DIR:-${ROOT_DIR}/tmp}"
case "${artifact_dir}" in
  /*) ;;
  *) artifact_dir="${ROOT_DIR}/${artifact_dir#./}" ;;
esac
mkdir -p "${artifact_dir}"

temp_root="${TMPDIR:-/tmp}"
work_dir="$(mktemp -d "${temp_root%/}/slizen-release-artifacts.XXXXXX")"
cleanup() {
  rm -rf -- "${work_dir}"
}
trap cleanup EXIT HUP INT TERM

chart_dir="${work_dir}/slizen"
raw_chart_dir="${work_dir}/raw-sidecar-validation"
stage_dir="${work_dir}/stage"
mkdir -p "${stage_dir}"
cp -R "${ROOT_DIR}/charts/slizen" "${chart_dir}"

chart_yaml="${chart_dir}/Chart.yaml"
values_yaml="${chart_dir}/values.yaml"
chart_readme="${chart_dir}/README.md"
chart_yaml_new="${work_dir}/Chart.yaml"
values_yaml_new="${work_dir}/values.yaml"
chart_readme_new="${work_dir}/README.md"

awk -v version="${SLIZEN_VERSION}" '
  /^# A source-tree release candidate may intentionally default/ {
    print "# Post-publish package: chart and application identity are aligned"
    getline
    print "# with the exact published image digest."
    next
  }
  /^version:[[:space:]]*/ {
    print "version: " version
    chart_version++
    next
  }
  /^appVersion:[[:space:]]*/ {
    print "appVersion: \"" version "\""
    app_version++
    next
  }
  { print }
  END {
    if (chart_version != 1 || app_version != 1) {
      exit 42
    }
  }
' "${chart_yaml}" > "${chart_yaml_new}" || {
  echo "could not set exactly one chart version and appVersion" >&2
  exit 1
}
mv "${chart_yaml_new}" "${chart_yaml}"

image_digest="${SLIZEN_IMAGE##*@}"
awk -v version="${SLIZEN_VERSION}" -v digest="${image_digest}" '
  /^  # Main may describe an unreleased candidate\./ {
    print "  # Post-publish package pinned to the exact published image."
    getline
    next
  }
  /^  tag:[[:space:]]*/ {
    print "  tag: \"" version "\""
    tag_count++
    next
  }
  /^  digest:[[:space:]]*/ {
    print "  digest: \"" digest "\""
    digest_count++
    next
  }
  { print }
  END {
    if (tag_count != 1 || digest_count != 1) {
      exit 42
    }
  }
' "${values_yaml}" > "${values_yaml_new}" || {
  echo "could not set exactly one chart image tag and digest" >&2
  exit 1
}
mv "${values_yaml_new}" "${values_yaml}"

source_image="$(awk '
  /^ghcr\.io\/slizendb\/slizen@sha256:/ {
    print
    exit
  }
' "${chart_readme}")"
if [[ ! "${source_image}" =~ ^ghcr\.io/slizendb/slizen@sha256:[0-9a-f]{64}$ ]]; then
  echo "could not resolve the source chart README image identity" >&2
  exit 1
fi
source_digest="${source_image##*@}"
awk -v version="${SLIZEN_VERSION}" \
  -v source_image="${source_image}" \
  -v source_digest="${source_digest}" \
  -v image="${SLIZEN_IMAGE}" \
  -v image_digest="${image_digest}" '
  BEGIN {
    RS = ""
    ORS = "\n\n"
  }
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
  /source-tree release candidate/ {
    next
  }
  /^The source chart has candidate/ {
    print "This post-publish package was generated from the tagged source after the image was published. Its chart version, appVersion, default image tag, image digest, and rendered application label are all bound to v" version " and `" image "`."
    print "The same archive contains release-bound operator documentation, the pinned raw sidecar pattern, and the observability pack. Extract them next to the archive before following the quickstart:"
    print "```sh"
    print "export CHART_REF=./slizen-" version ".tgz"
    print "tar -xzf \"$CHART_REF\" slizen/operator-docs"
    print "export OPERATOR_DOCS_DIR=./slizen/operator-docs"
    print "less \"$OPERATOR_DOCS_DIR/docs/STAGING_QUICKSTART.md\""
    print "```"
    print "All Helm commands in the bundled guide continue to use `$CHART_REF`; the extracted directory supplies documentation and supporting sidecar/observability files, not a second chart source."
    next
  }
  {
    sub(/^The stable public image is v[^:]+:/, "This release chart deploys Slizen v" version ":")
    $0 = replace_literal($0, source_image, image)
    $0 = replace_literal($0, source_digest, image_digest)
    $0 = replace_literal($0, "STABLE_DIGEST", "RELEASE_DIGEST")
    $0 = replace_literal($0, "immutable stable image", "immutable release image")
    $0 = replace_literal($0, "../../docs/STAGING_QUICKSTART.md", "operator-docs/docs/STAGING_QUICKSTART.md")
    $0 = replace_literal($0, "../../docs/STAGING_ROLLOUT.md", "operator-docs/docs/STAGING_ROLLOUT.md")
    $0 = replace_literal($0, "../../docs/FAILURE_MODES.md", "operator-docs/docs/FAILURE_MODES.md")
    $0 = replace_literal($0, "../../docs/STAGING_RELEASE_GATE.md", "operator-docs/docs/STAGING_RELEASE_GATE.md")
    $0 = replace_literal($0, "`deploy/kubernetes/observe-sidecar.yaml`", "`operator-docs/deploy/kubernetes/observe-sidecar.yaml`")
    $0 = replace_literal($0, "export CHART_REF=./charts/slizen", "export CHART_REF=./slizen-" version ".tgz")
    $0 = replace_literal($0, "cp \"$CHART_REF/examples/staging-values.yaml\" \"$REVIEWED_VALUES\"", "tar -xOf \"$CHART_REF\" slizen/examples/staging-values.yaml > \"$REVIEWED_VALUES\"")
    print
  }
' "${chart_readme}" > "${chart_readme_new}"
mv "${chart_readme_new}" "${chart_readme}"

bash "${ROOT_DIR}/scripts/render_release_operator_docs.sh" \
  "${chart_dir}/operator-docs"

if grep -Eiq 'source-tree release candidate|unreleased candidate|latest verified public' \
  "${chart_yaml}" "${values_yaml}" "${chart_readme}"; then
  echo "release chart retained source-only candidate guidance" >&2
  exit 1
fi
grep -Fq "${SLIZEN_IMAGE}" "${chart_readme}" || {
  echo "release chart README is not bound to ${SLIZEN_IMAGE}" >&2
  exit 1
}
grep -Fq "export CHART_REF=./slizen-${SLIZEN_VERSION}.tgz" "${chart_readme}" || {
  echo "release chart README does not use its packaged chart archive" >&2
  exit 1
}
grep -Fq 'tar -xOf "$CHART_REF" slizen/examples/staging-values.yaml' "${chart_readme}" || {
  echo "release chart README cannot extract its bundled staging example" >&2
  exit 1
}
grep -Fq 'tar -xzf "$CHART_REF" slizen/operator-docs' "${chart_readme}" || {
  echo "release chart README cannot extract its bundled operator documentation" >&2
  exit 1
}
grep -Fq 'operator-docs/docs/STAGING_QUICKSTART.md' "${chart_readme}" || {
  echo "release chart README does not point to its bundled operator quickstart" >&2
  exit 1
}
if grep -Fq './charts/slizen' "${chart_readme}"; then
  echo "release chart README retained a source-tree chart path" >&2
  exit 1
fi
if [[ "${source_digest}" != "${image_digest}" ]] && grep -Fq "${source_digest}" "${chart_readme}"; then
  echo "release chart README retained the previous image digest" >&2
  exit 1
fi

raw_source="${ROOT_DIR}/deploy/kubernetes/observe-sidecar.yaml"
raw_stage="${stage_dir}/slizen-observe-sidecar-${SLIZEN_VERSION}.yaml"
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
' "${raw_source}" > "${raw_stage}" || {
  echo "could not pin exactly one Slizen image in the raw sidecar manifest" >&2
  exit 1
}

chart_render="${work_dir}/chart-render.yaml"
"${helm_bin}" lint "${chart_dir}"
"${helm_bin}" template slizen-release "${chart_dir}" \
  --namespace slizen-staging > "${chart_render}"
grep -Fq "image: \"${SLIZEN_IMAGE}\"" "${chart_render}" || {
  echo "release chart render is not pinned to ${SLIZEN_IMAGE}" >&2
  exit 1
}
grep -Fq "app.kubernetes.io/version: \"${SLIZEN_VERSION}\"" "${chart_render}" || {
  echo "release chart render does not carry application version ${SLIZEN_VERSION}" >&2
  exit 1
}

"${helm_bin}" package \
  --destination "${stage_dir}" \
  "${chart_dir}" >/dev/null
chart_stage="${stage_dir}/slizen-${SLIZEN_VERSION}.tgz"
if [[ ! -s "${chart_stage}" ]]; then
  echo "helm did not create ${chart_stage}" >&2
  exit 1
fi

packaged_metadata="${work_dir}/packaged-chart.yaml"
packaged_values="${work_dir}/packaged-values.yaml"
packaged_readme="${work_dir}/packaged-readme.md"
"${helm_bin}" show chart "${chart_stage}" > "${packaged_metadata}"
"${helm_bin}" show values "${chart_stage}" > "${packaged_values}"
"${helm_bin}" show readme "${chart_stage}" > "${packaged_readme}"
awk -F ':[[:space:]]*' -v expected="${SLIZEN_VERSION}" '
  $1 == "version" {
    gsub(/^["\047]|["\047]$/, "", $2)
    found = ($2 == expected)
  }
  END { exit !found }
' "${packaged_metadata}" || {
  echo "packaged chart metadata does not have version ${SLIZEN_VERSION}" >&2
  exit 1
}
awk -F ':[[:space:]]*' -v expected="${SLIZEN_VERSION}" '
  $1 == "appVersion" {
    gsub(/^["\047]|["\047]$/, "", $2)
    found = ($2 == expected)
  }
  END { exit !found }
' "${packaged_metadata}" || {
  echo "packaged chart metadata does not have appVersion ${SLIZEN_VERSION}" >&2
  exit 1
}
grep -Fq "tag: \"${SLIZEN_VERSION}\"" "${packaged_values}" || {
  echo "packaged chart values do not have image tag ${SLIZEN_VERSION}" >&2
  exit 1
}
grep -Fq "digest: \"${image_digest}\"" "${packaged_values}" || {
  echo "packaged chart values do not have image digest ${image_digest}" >&2
  exit 1
}
grep -Fq "${SLIZEN_IMAGE}" "${packaged_readme}" || {
  echo "packaged chart README is not bound to ${SLIZEN_IMAGE}" >&2
  exit 1
}
grep -Fq "export CHART_REF=./slizen-${SLIZEN_VERSION}.tgz" "${packaged_readme}" || {
  echo "packaged chart README does not use its packaged chart archive" >&2
  exit 1
}
grep -Fq 'tar -xOf "$CHART_REF" slizen/examples/staging-values.yaml' "${packaged_readme}" || {
  echo "packaged chart README cannot extract its bundled staging example" >&2
  exit 1
}
grep -Fq 'tar -xzf "$CHART_REF" slizen/operator-docs' "${packaged_readme}" || {
  echo "packaged chart README cannot extract its bundled operator documentation" >&2
  exit 1
}
if grep -Fq './charts/slizen' "${packaged_readme}"; then
  echo "packaged chart README retained a source-tree chart path" >&2
  exit 1
fi
if [[ "${source_digest}" != "${image_digest}" ]] && grep -Fq "${source_digest}" "${packaged_readme}"; then
  echo "packaged chart README retained the previous image digest" >&2
  exit 1
fi
if grep -Eiq 'source-tree release candidate|unreleased candidate|latest verified public' \
  "${packaged_metadata}" "${packaged_values}" "${packaged_readme}"; then
  echo "packaged chart retained source-only candidate guidance" >&2
  exit 1
fi

packaged_render="${work_dir}/packaged-chart-render.yaml"
"${helm_bin}" template slizen-release "${chart_stage}" \
  --namespace slizen-staging > "${packaged_render}"
grep -Fq "image: \"${SLIZEN_IMAGE}\"" "${packaged_render}" || {
  echo "packaged chart render is not pinned to ${SLIZEN_IMAGE}" >&2
  exit 1
}
grep -Fq "app.kubernetes.io/version: \"${SLIZEN_VERSION}\"" "${packaged_render}" || {
  echo "packaged chart render does not carry application version ${SLIZEN_VERSION}" >&2
  exit 1
}

mkdir -p "${raw_chart_dir}/templates"
cp "${raw_stage}" "${raw_chart_dir}/templates/sidecar.yaml"
cat > "${raw_chart_dir}/Chart.yaml" <<EOF
apiVersion: v2
name: slizen-raw-sidecar-validation
type: application
version: ${SLIZEN_VERSION}
appVersion: "${SLIZEN_VERSION}"
EOF
raw_render="${work_dir}/raw-sidecar-render.yaml"
"${helm_bin}" template slizen-sidecar-validation "${raw_chart_dir}" > "${raw_render}"
grep -Fq "image: ${SLIZEN_IMAGE}" "${raw_render}" || {
  echo "raw sidecar render is not pinned to ${SLIZEN_IMAGE}" >&2
  exit 1
}

chart_artifact="${artifact_dir}/slizen-${SLIZEN_VERSION}.tgz"
raw_artifact="${artifact_dir}/slizen-observe-sidecar-${SLIZEN_VERSION}.yaml"
cp "${chart_stage}" "${chart_artifact}.tmp"
cp "${raw_stage}" "${raw_artifact}.tmp"
mv "${chart_artifact}.tmp" "${chart_artifact}"
mv "${raw_artifact}.tmp" "${raw_artifact}"

echo "release Helm chart: ${chart_artifact}"
echo "release raw sidecar: ${raw_artifact}"
