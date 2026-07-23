package release

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/slizendb/slizen/internal/buildinfo"
	"github.com/slizendb/slizen/internal/compatibility"
)

func TestRedisCompatibilityDocMatchesKnownCommands(t *testing.T) {
	root := repoRoot(t)
	data, err := os.ReadFile(filepath.Join(root, "docs", "REDIS_COMPATIBILITY.md"))
	if err != nil {
		t.Fatal(err)
	}
	allowed := make(map[string]string)
	for _, entry := range compatibility.Catalog() {
		allowed[entry.Command] = string(entry.Status)
	}
	found := map[string]bool{}
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, "| `") {
			continue
		}
		parts := strings.Split(line, "|")
		if len(parts) < 5 {
			continue
		}
		command := strings.Trim(strings.TrimSpace(parts[1]), "`")
		status := strings.TrimSpace(parts[2])
		expected, ok := allowed[command]
		if !ok {
			t.Fatalf("compatibility doc mentions unknown command %q", command)
		}
		if found[command] {
			t.Fatalf("compatibility doc mentions %s more than once", command)
		}
		found[command] = true
		if status != expected {
			t.Fatalf("%s status = %q, want %q", command, status, expected)
		}
	}
	for command := range allowed {
		if !found[command] {
			t.Fatalf("compatibility doc missing %s", command)
		}
	}
	for _, want := range []string{
		"any other command is classified as `unsupported`",
		"`slizenctl compatibility report`",
		"does **not** capture, inspect, or scan an application's workload",
		"NX`, `XX`, `GT`, and `LT` options are unsupported",
	} {
		if !strings.Contains(string(data), want) {
			t.Errorf("compatibility doc missing contract text %q", want)
		}
	}
}

func TestCanonicalReleaseIdentity(t *testing.T) {
	root := repoRoot(t)
	formerRepository := "github.com/" + "gazakov/" + "slizen"
	formerImage := "ghcr.io/" + "gazakov/" + "slizen"
	files := []string{
		"go.mod",
		"Dockerfile",
		"README.md",
		"README.ru.md",
		filepath.Join(".github", "workflows", "release-image.yml"),
		filepath.Join("charts", "slizen", "Chart.yaml"),
		filepath.Join("charts", "slizen", "README.md"),
		filepath.Join("charts", "slizen", "values.yaml"),
		filepath.Join("deploy", "kubernetes", "observe-sidecar.yaml"),
		filepath.Join("docs", "RELEASE_NOTES_v0.1.md"),
		filepath.Join("docs", "RELEASE_NOTES_v0.2.2.md"),
		filepath.Join("docs", "RELEASE_NOTES_v0.2.3.md"),
	}
	for _, name := range files {
		data, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		text := string(data)
		if strings.Contains(text, formerRepository) || strings.Contains(text, formerImage) {
			t.Errorf("%s contains the former repository identity", name)
		}
	}

	assertContains := func(name, want string) {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if !strings.Contains(string(data), want) {
			t.Errorf("%s does not contain %q", name, want)
		}
	}
	assertContains("go.mod", "module github.com/slizendb/slizen")
	assertContains("Dockerfile", `org.opencontainers.image.source="https://github.com/slizendb/slizen"`)
	assertContains(filepath.Join(".github", "workflows", "release-image.yml"), "ghcr.io/slizendb/slizen")
	assertContains(filepath.Join("charts", "slizen", "values.yaml"), "repository: ghcr.io/slizendb/slizen")
	assertContains(filepath.Join("deploy", "kubernetes", "observe-sidecar.yaml"), "image: ghcr.io/slizendb/slizen@sha256:")
}

func TestReleaseCandidateDocsDoNotClaimPublishedArtifacts(t *testing.T) {
	root := repoRoot(t)
	checks := []struct {
		name  string
		wants []string
	}{
		{filepath.Join("docs", "RELEASE_NOTES_v0.2.3.md"), []string{"Release candidate, not a published release", "94,961", "798–803", "99.154390%–99.159655%", "not proof of physical wire-command reduction"}},
		{filepath.Join("docs", "ROADMAP.md"), []string{"Status: release candidate in the source tree; not tagged or published", "Publish and verify the `v0.2.3` tag"}},
		{"README.md", []string{"releases/download/v0.2.2/slizen-workload-result.json", "v0.2.3 source-tree release candidate"}},
	}
	for _, check := range checks {
		data, err := os.ReadFile(filepath.Join(root, check.name))
		if err != nil {
			t.Fatalf("read %s: %v", check.name, err)
		}
		for _, want := range check.wants {
			if !strings.Contains(string(data), want) {
				t.Errorf("%s does not contain release-candidate marker %q", check.name, want)
			}
		}
	}
}

func TestReleaseVersionSurfacesMatch(t *testing.T) {
	root := repoRoot(t)
	version := buildinfo.Version
	if version == "" || version == "dev" || strings.ContainsAny(version, " \t\r\n") {
		t.Fatalf("build version %q is not a release version", version)
	}
	if releaseVersion := os.Getenv("SLIZEN_VERSION"); releaseVersion != "" && version != releaseVersion {
		t.Fatalf("source release version %q does not match SLIZEN_VERSION %q", version, releaseVersion)
	}

	checks := []struct {
		name  string
		wants []string
	}{
		{filepath.Join("charts", "slizen", "Chart.yaml"), []string{"version: " + version}},
		{filepath.Join("scripts", "release_check.sh"), []string{`SLIZEN_VERSION:-` + version}},
		{"CHANGELOG.md", []string{"## v" + version + " - "}},
		{filepath.Join("docs", "RELEASE_CHECKLIST.md"), []string{"RELEASE_NOTES_v" + version + ".md", "git tag -a v" + version, "ghcr.io/slizendb/slizen@$RELEASE_DIGEST"}},
		{filepath.Join("docs", "RELEASE_NOTES_v"+version+".md"), []string{"# Slizen v" + version + " —"}},
		{filepath.Join(".github", "ISSUE_TEMPLATE", "bug.yml"), []string{version + " (full commit SHA)"}},
	}

	for _, check := range checks {
		data, err := os.ReadFile(filepath.Join(root, check.name))
		if err != nil {
			t.Fatalf("read %s: %v", check.name, err)
		}
		for _, want := range check.wants {
			if !strings.Contains(string(data), want) {
				t.Errorf("%s does not contain release version marker %q", check.name, want)
			}
		}
	}
}

func TestStagingArtifactsPinPublishedImage(t *testing.T) {
	root := repoRoot(t)
	const stableVersion = "0.2.2"
	const stableDigest = "sha256:7989b6ff17659b3f1b2f1d3feec8af6422b48f1f5486eb77247a5c82ba86b627"
	const image = "ghcr.io/slizendb/slizen@sha256:7989b6ff17659b3f1b2f1d3feec8af6422b48f1f5486eb77247a5c82ba86b627"
	checks := []struct {
		name  string
		wants []string
	}{
		{
			filepath.Join("charts", "slizen", "Chart.yaml"),
			[]string{`appVersion: "` + stableVersion + `"`},
		},
		{
			filepath.Join("charts", "slizen", "values.yaml"),
			[]string{`tag: "` + stableVersion + `"`, `digest: "` + stableDigest + `"`},
		},
		{
			filepath.Join("deploy", "kubernetes", "observe-sidecar.yaml"),
			[]string{"image: " + image},
		},
	}
	for _, check := range checks {
		data, err := os.ReadFile(filepath.Join(root, check.name))
		if err != nil {
			t.Fatalf("read %s: %v", check.name, err)
		}
		for _, want := range check.wants {
			if !strings.Contains(string(data), want) {
				t.Errorf("%s does not contain stable artifact identity %q", check.name, want)
			}
		}
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found")
		}
		dir = parent
	}
}
