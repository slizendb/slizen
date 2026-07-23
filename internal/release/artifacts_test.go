package release

import (
	"archive/tar"
	"compress/gzip"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const releaseArtifactTestDigest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
const releaseArtifactTestCommit = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

type sourceStableIdentity struct {
	version string
	commit  string
	digest  string
}

func TestPackageReleaseArtifactsUsesPublishedIdentityWithoutMutatingSource(t *testing.T) {
	if testing.Short() {
		t.Skip("runs the release artifact shell pipeline")
	}
	for _, command := range []string{"bash", "tar"} {
		if _, err := exec.LookPath(command); err != nil {
			t.Skipf("%s is unavailable: %v", command, err)
		}
	}

	root := repoRoot(t)
	sourceStable := readSourceStableIdentity(t, root)
	version := readSourceChartVersion(t, root)
	fakeHelm := writeFakeHelm(t)
	fakeGit := writeFakeGit(t, releaseArtifactTestCommit)
	artifactDir := t.TempDir()
	sourceFiles := []string{
		filepath.Join(root, "charts", "slizen", "Chart.yaml"),
		filepath.Join(root, "charts", "slizen", "README.md"),
		filepath.Join(root, "charts", "slizen", "values.yaml"),
		filepath.Join(root, "docs", "STAGING_QUICKSTART.md"),
		filepath.Join(root, "docs", "STAGING_ROLLOUT.md"),
		filepath.Join(root, "docs", "FAILURE_MODES.md"),
		filepath.Join(root, "docs", "STAGING_RELEASE_GATE.md"),
		filepath.Join(root, "docs", "REDIS_COMPATIBILITY.md"),
		filepath.Join(root, "docs", "OBSERVABILITY.md"),
		filepath.Join(root, "deploy", "kubernetes", "README.md"),
		filepath.Join(root, "deploy", "kubernetes", "observe-sidecar.yaml"),
		filepath.Join(root, "deploy", "observability", "grafana-dashboard.json"),
		filepath.Join(root, "deploy", "observability", "prometheus-rules.yaml"),
	}
	before := make(map[string]string, len(sourceFiles))
	for _, name := range sourceFiles {
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		before[name] = string(data)
	}

	image := "ghcr.io/slizendb/slizen@" + releaseArtifactTestDigest
	cmd := exec.Command("bash", filepath.Join(root, "scripts", "package_release_artifacts.sh"))
	cmd.Dir = root
	cmd.Env = append(os.Environ(),
		"HELM_BIN="+fakeHelm,
		"GIT_BIN="+fakeGit,
		"SLIZEN_ARTIFACT_DIR="+artifactDir,
		"SLIZEN_IMAGE="+image,
		"SLIZEN_VERSION="+version,
		"SLIZEN_COMMIT="+releaseArtifactTestCommit,
	)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("package artifacts: %v\n%s", err, output)
	}

	for _, name := range sourceFiles {
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != before[name] {
			t.Errorf("%s was mutated by release packaging", name)
		}
	}

	chartPath := filepath.Join(artifactDir, "slizen-"+version+".tgz")
	chartFiles := readTarGzip(t, chartPath)
	assertArtifactContains(t, chartFiles["slizen/Chart.yaml"], "version: "+version)
	assertArtifactContains(t, chartFiles["slizen/Chart.yaml"], `appVersion: "`+version+`"`)
	assertArtifactContains(t, chartFiles["slizen/values.yaml"], `tag: "`+version+`"`)
	assertArtifactContains(t, chartFiles["slizen/values.yaml"], `digest: "`+releaseArtifactTestDigest+`"`)
	assertArtifactContains(t, chartFiles["slizen/README.md"], "This release chart deploys Slizen v"+version)
	assertArtifactContains(t, chartFiles["slizen/README.md"], image)
	assertArtifactContains(t, chartFiles["slizen/README.md"], "RELEASE_DIGEST")
	assertArtifactContains(t, chartFiles["slizen/README.md"], "export RELEASE_DIGEST="+releaseArtifactTestDigest)
	assertArtifactContains(t, chartFiles["slizen/README.md"], "export CHART_REF=./slizen-"+version+".tgz")
	assertArtifactContains(t, chartFiles["slizen/README.md"], `tar -xOf "$CHART_REF" slizen/examples/staging-values.yaml`)
	assertArtifactContains(t, chartFiles["slizen/README.md"], `tar -xzf "$CHART_REF" slizen/operator-docs`)
	assertArtifactContains(t, chartFiles["slizen/README.md"], "operator-docs/docs/STAGING_ROLLOUT.md")
	assertArtifactContains(t, chartFiles["slizen/examples/staging-values.yaml"], "mode: observe")
	if strings.Contains(chartFiles["slizen/README.md"], "source-tree release candidate") ||
		strings.Contains(chartFiles["slizen/README.md"], sourceStable.digest) ||
		strings.Contains(chartFiles["slizen/README.md"], "./charts/slizen") ||
		strings.Contains(chartFiles["slizen/README.md"], "github.com/slizendb/slizen/blob/") {
		t.Error("packaged chart README retained source-only release identity")
	}

	operatorDocs := []string{
		"slizen/operator-docs/docs/STAGING_QUICKSTART.md",
		"slizen/operator-docs/docs/STAGING_ROLLOUT.md",
		"slizen/operator-docs/docs/FAILURE_MODES.md",
		"slizen/operator-docs/docs/STAGING_RELEASE_GATE.md",
		"slizen/operator-docs/docs/REDIS_COMPATIBILITY.md",
		"slizen/operator-docs/docs/OBSERVABILITY.md",
		"slizen/operator-docs/deploy/kubernetes/README.md",
	}
	for _, name := range operatorDocs {
		document, ok := chartFiles[name]
		if !ok {
			t.Errorf("packaged chart does not contain %s", name)
			continue
		}
		assertArtifactContains(t, document, "slizen-"+version+".tgz")
		for _, forbidden := range []string{
			"source-tree release candidate",
			"source candidate",
			"unpublished",
			"v" + sourceStable.version,
			sourceStable.commit,
			sourceStable.digest,
			"./charts/slizen",
			"github.com/slizendb/slizen/blob/",
		} {
			if strings.Contains(document, forbidden) {
				t.Errorf("%s retained release contradiction %q", name, forbidden)
			}
		}
		assertShellFencesParse(t, name, document)
	}

	quickstart := chartFiles["slizen/operator-docs/docs/STAGING_QUICKSTART.md"]
	assertArtifactContains(t, quickstart, "export SLIZEN_VERSION="+version)
	assertArtifactContains(t, quickstart, "export SLIZEN_COMMIT="+releaseArtifactTestCommit)
	assertArtifactContains(t, quickstart, "export SLIZEN_IMAGE="+image)
	assertArtifactContains(t, quickstart, "export CHART_REF=./slizen-"+version+".tgz")
	assertArtifactContains(t, quickstart, `helm lint "$CHART_REF"`)
	assertArtifactContains(t, quickstart, "[staging rollout](STAGING_ROLLOUT.md)")
	assertArtifactContains(t, quickstart, "[raw sidecar guide](../deploy/kubernetes/README.md)")

	runbook := chartFiles["slizen/operator-docs/docs/STAGING_ROLLOUT.md"]
	assertArtifactContains(t, runbook, "export SLIZEN_VERSION="+version)
	assertArtifactContains(t, runbook, "export SLIZEN_COMMIT="+releaseArtifactTestCommit)
	assertArtifactContains(t, runbook, "export SLIZEN_IMAGE="+image)
	assertArtifactContains(t, runbook, "export CHART_REF=./slizen-"+version+".tgz")
	assertArtifactContains(t, runbook, `helm lint "$CHART_REF"`)
	assertArtifactContains(t, runbook, "$OPERATOR_DOCS_DIR/deploy/kubernetes/observe-sidecar.yaml")
	assertArtifactContains(t, runbook, "[FAILURE_MODES.md](FAILURE_MODES.md)")
	assertArtifactContains(t, runbook, "[OBSERVABILITY.md](OBSERVABILITY.md)")

	sidecarGuide := chartFiles["slizen/operator-docs/deploy/kubernetes/README.md"]
	assertArtifactContains(t, sidecarGuide, "[the staging runbook](../../docs/STAGING_ROLLOUT.md)")
	assertArtifactContains(t, sidecarGuide, "[failure modes](../../docs/FAILURE_MODES.md)")
	assertArtifactContains(t, sidecarGuide, "[self-service gate](../../docs/STAGING_RELEASE_GATE.md)")

	assertArtifactContains(t, chartFiles["slizen/operator-docs/RELEASE_IDENTITY.env"], "SLIZEN_VERSION="+version)
	assertArtifactContains(t, chartFiles["slizen/operator-docs/RELEASE_IDENTITY.env"], "SLIZEN_COMMIT="+releaseArtifactTestCommit)
	assertArtifactContains(t, chartFiles["slizen/operator-docs/RELEASE_IDENTITY.env"], "SLIZEN_IMAGE="+image)
	assertArtifactContains(t, chartFiles["slizen/operator-docs/deploy/kubernetes/observe-sidecar.yaml"], "image: "+image)
	if len(chartFiles["slizen/operator-docs/deploy/observability/grafana-dashboard.json"]) == 0 {
		t.Error("packaged chart does not contain the Grafana dashboard")
	}
	if len(chartFiles["slizen/operator-docs/deploy/observability/prometheus-rules.yaml"]) == 0 {
		t.Error("packaged chart does not contain the Prometheus rules")
	}

	extractedValues := filepath.Join(t.TempDir(), "reviewed-values.yaml")
	extract := exec.Command("tar", "-xOf", chartPath, "slizen/examples/staging-values.yaml")
	extracted, err := extract.Output()
	if err != nil {
		t.Fatalf("extract bundled staging example: %v", err)
	}
	if err := os.WriteFile(extractedValues, extracted, 0o600); err != nil {
		t.Fatal(err)
	}
	lint := exec.Command(fakeHelm, "lint", chartPath, "-f", extractedValues)
	if output, err := lint.CombinedOutput(); err != nil {
		t.Fatalf("artifact-only Helm lint: %v\n%s", err, output)
	}

	rawPath := filepath.Join(artifactDir, "slizen-observe-sidecar-"+version+".yaml")
	raw, err := os.ReadFile(rawPath)
	if err != nil {
		t.Fatal(err)
	}
	assertArtifactContains(t, string(raw), "image: "+image)
}

func TestPackageReleaseArtifactsRejectsSourceChartVersionMismatch(t *testing.T) {
	if testing.Short() {
		t.Skip("runs the release artifact shell pipeline")
	}
	root := repoRoot(t)
	cmd := exec.Command("bash", filepath.Join(root, "scripts", "package_release_artifacts.sh"))
	cmd.Dir = root
	cmd.Env = append(os.Environ(),
		"HELM_BIN="+writeFakeHelm(t),
		"GIT_BIN="+writeFakeGit(t, releaseArtifactTestCommit),
		"SLIZEN_ARTIFACT_DIR="+t.TempDir(),
		"SLIZEN_IMAGE=ghcr.io/slizendb/slizen@"+releaseArtifactTestDigest,
		"SLIZEN_VERSION=9.8.7",
		"SLIZEN_COMMIT="+releaseArtifactTestCommit,
	)
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatal("source chart version mismatch was accepted")
	}
	if !strings.Contains(string(output), "does not match SLIZEN_VERSION") {
		t.Fatalf("unexpected rejection message:\n%s", output)
	}
}

func TestPackageReleaseArtifactsRejectsMutableImageReference(t *testing.T) {
	root := repoRoot(t)
	cmd := exec.Command("bash", filepath.Join(root, "scripts", "package_release_artifacts.sh"))
	cmd.Dir = root
	cmd.Env = append(os.Environ(),
		"SLIZEN_IMAGE=ghcr.io/slizendb/slizen:0.2.3",
		"SLIZEN_VERSION=0.2.3",
		"SLIZEN_COMMIT="+releaseArtifactTestCommit,
	)
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatal("mutable image reference was accepted")
	}
	if !strings.Contains(string(output), "exact sha256 digest") {
		t.Fatalf("unexpected rejection message:\n%s", output)
	}
}

func TestPackageReleaseArtifactsRejectsCommitMismatchAndDirtyTree(t *testing.T) {
	if testing.Short() {
		t.Skip("runs the release artifact shell pipeline")
	}
	root := repoRoot(t)
	fakeHelm := writeFakeHelm(t)
	image := "ghcr.io/slizendb/slizen@" + releaseArtifactTestDigest

	tests := []struct {
		name        string
		gitCommit   string
		gitStatus   string
		wantMessage string
	}{
		{
			name:        "commit mismatch",
			gitCommit:   "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
			wantMessage: "does not match SLIZEN_COMMIT",
		},
		{
			name:        "dirty source",
			gitCommit:   releaseArtifactTestCommit,
			gitStatus:   " M charts/slizen/README.md",
			wantMessage: "require a clean source tree",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cmd := exec.Command("bash", filepath.Join(root, "scripts", "package_release_artifacts.sh"))
			cmd.Dir = root
			cmd.Env = append(os.Environ(),
				"HELM_BIN="+fakeHelm,
				"GIT_BIN="+writeFakeGitWithStatus(t, test.gitCommit, test.gitStatus),
				"SLIZEN_ARTIFACT_DIR="+t.TempDir(),
				"SLIZEN_IMAGE="+image,
				"SLIZEN_VERSION=9.8.7",
				"SLIZEN_COMMIT="+releaseArtifactTestCommit,
			)
			output, err := cmd.CombinedOutput()
			if err == nil {
				t.Fatalf("%s was accepted", test.name)
			}
			if !strings.Contains(string(output), test.wantMessage) {
				t.Fatalf("unexpected rejection message:\n%s", output)
			}
		})
	}
}

func TestReleaseWorkflowPublishesDeploymentArtifactEvidence(t *testing.T) {
	root := repoRoot(t)
	checks := map[string][]string{
		filepath.Join(".github", "workflows", "release-image.yml"): {
			"Generate immutable deployment artifacts and image evidence",
			"tmp/slizen-${{ needs.validate.outputs.version }}.tgz",
			"tmp/slizen-observe-sidecar-${{ needs.validate.outputs.version }}.yaml",
			"Attest release Helm chart",
			"Attest release raw sidecar manifest",
			"flavor: latest=false",
			"Promote verified stable aliases",
			"docker buildx imagetools create",
		},
		filepath.Join("scripts", "release_evidence.sh"): {
			"slizen.release-evidence.v2",
			"deployment_artifacts",
			"helm_chart_sha256",
			"raw_sidecar_sha256",
			"SLIZEN_COMMIT",
			"def known_version:",
			".runtime_versions.origin | known_version",
		},
	}
	for name, wants := range checks {
		data, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		for _, want := range wants {
			if !strings.Contains(string(data), want) {
				t.Errorf("%s does not contain %q", name, want)
			}
		}
	}

	workflow, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "release-image.yml"))
	if err != nil {
		t.Fatal(err)
	}
	evidenceIndex := strings.Index(string(workflow), "Generate immutable deployment artifacts and image evidence")
	uploadIndex := strings.Index(string(workflow), "Upload immutable-image evidence")
	promotionIndex := strings.Index(string(workflow), "Promote verified stable aliases")
	if evidenceIndex < 0 || uploadIndex < 0 || promotionIndex < 0 ||
		!(evidenceIndex < uploadIndex && uploadIndex < promotionIndex) {
		t.Error("stable aliases are not promoted strictly after evidence generation and upload")
	}
}

func writeFakeHelm(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "helm")
	const script = `#!/bin/sh
set -eu

command_name=$1
shift
case "$command_name" in
  lint)
    [ -e "$1" ]
    exit 0
    ;;
  package)
    [ "$1" = "--destination" ]
    destination=$2
    chart_dir=$3
    version=$(awk '/^version:/ { print $2; exit }' "$chart_dir/Chart.yaml")
    tar -czf "$destination/slizen-$version.tgz" -C "$(dirname "$chart_dir")" "$(basename "$chart_dir")"
    ;;
  show)
    content=$1
    chart=$2
    case "$content" in
      chart) tar -xOf "$chart" slizen/Chart.yaml ;;
      values) tar -xOf "$chart" slizen/values.yaml ;;
      readme) tar -xOf "$chart" slizen/README.md ;;
      *)
        echo "unexpected fake helm show target: $content" >&2
        exit 1
        ;;
    esac
    ;;
  template)
    release_name=$1
    chart=$2
    case "$chart" in
      *.tgz)
        chart_yaml=$(tar -xOf "$chart" slizen/Chart.yaml)
        values_yaml=$(tar -xOf "$chart" slizen/values.yaml)
        ;;
      *)
        if [ -f "$chart/templates/sidecar.yaml" ]; then
          cat "$chart/templates/sidecar.yaml"
          exit 0
        fi
        chart_yaml=$(cat "$chart/Chart.yaml")
        values_yaml=$(cat "$chart/values.yaml")
        ;;
    esac
    app_version=$(printf '%s\n' "$chart_yaml" | awk '/^appVersion:/ { gsub(/"/, "", $2); print $2; exit }')
    repository=$(printf '%s\n' "$values_yaml" | awk '/^  repository:/ { print $2; exit }')
    digest=$(printf '%s\n' "$values_yaml" | awk '/^  digest:/ { gsub(/"/, "", $2); print $2; exit }')
    printf 'app.kubernetes.io/version: "%s"\n' "$app_version"
    printf 'image: "%s@%s"\n' "$repository" "$digest"
    ;;
  *)
    echo "unexpected fake helm command: $command_name" >&2
    exit 1
    ;;
esac
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeFakeGit(t *testing.T, commit string) string {
	return writeFakeGitWithStatus(t, commit, "")
}

func writeFakeGitWithStatus(t *testing.T, commit, status string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "git")
	script := `#!/bin/sh
set -eu

case "$1" in
  status)
    printf '%s\n' '` + status + `'
    exit 0
    ;;
  rev-parse)
    printf '%s\n' '` + commit + `'
    ;;
  *)
    echo "unexpected fake git command: $*" >&2
    exit 1
    ;;
esac
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func readTarGzip(t *testing.T, path string) map[string]string {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	gzipReader, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	defer gzipReader.Close()
	reader := tar.NewReader(gzipReader)
	files := make(map[string]string)
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if header.Typeflag != tar.TypeReg {
			continue
		}
		data, err := io.ReadAll(reader)
		if err != nil {
			t.Fatal(err)
		}
		files[header.Name] = string(data)
	}
	return files
}

func assertArtifactContains(t *testing.T, text, want string) {
	t.Helper()
	if !strings.Contains(text, want) {
		t.Errorf("artifact does not contain %q", want)
	}
}

func readSourceStableIdentity(t *testing.T, root string) sourceStableIdentity {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, "docs", "STAGING_QUICKSTART.md"))
	if err != nil {
		t.Fatal(err)
	}

	values := make(map[string][]string)
	for _, line := range strings.Split(string(data), "\n") {
		for _, name := range []string{"STABLE_VERSION", "STABLE_COMMIT", "STABLE_DIGEST"} {
			prefix := "export " + name + "="
			if strings.HasPrefix(line, prefix) {
				values[name] = append(values[name], strings.TrimSpace(strings.TrimPrefix(line, prefix)))
			}
		}
	}
	for _, name := range []string{"STABLE_VERSION", "STABLE_COMMIT", "STABLE_DIGEST"} {
		if len(values[name]) != 1 || values[name][0] == "" {
			t.Fatalf("expected exactly one non-empty %s export in source quickstart", name)
		}
	}
	return sourceStableIdentity{
		version: values["STABLE_VERSION"][0],
		commit:  values["STABLE_COMMIT"][0],
		digest:  values["STABLE_DIGEST"][0],
	}
}

func readSourceChartVersion(t *testing.T, root string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, "charts", "slizen", "Chart.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var versions []string
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "version:") {
			versions = append(versions, strings.TrimSpace(strings.TrimPrefix(line, "version:")))
		}
	}
	if len(versions) != 1 || versions[0] == "" {
		t.Fatal("expected exactly one non-empty source chart version")
	}
	return versions[0]
}

func assertShellFencesParse(t *testing.T, documentName, document string) {
	t.Helper()
	inShellFence := false
	fenceIndex := 0
	var snippet strings.Builder

	for _, line := range strings.Split(document, "\n") {
		switch {
		case !inShellFence && strings.TrimSpace(line) == "```sh":
			inShellFence = true
			snippet.Reset()
		case inShellFence && strings.TrimSpace(line) == "```":
			fenceIndex++
			scriptPath := filepath.Join(t.TempDir(), "snippet.sh")
			if err := os.WriteFile(scriptPath, []byte(snippet.String()), 0o600); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command("bash", "-n", scriptPath)
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Errorf("%s shell fence %d does not parse: %v\n%s\n%s",
					documentName, fenceIndex, err, output, snippet.String())
			}
			inShellFence = false
		case inShellFence:
			snippet.WriteString(line)
			snippet.WriteByte('\n')
		}
	}
	if inShellFence {
		t.Errorf("%s has an unterminated shell fence", documentName)
	}
}
