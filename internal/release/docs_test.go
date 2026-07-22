package release

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRedisCompatibilityDocMatchesKnownCommands(t *testing.T) {
	root := repoRoot(t)
	data, err := os.ReadFile(filepath.Join(root, "docs", "REDIS_COMPATIBILITY.md"))
	if err != nil {
		t.Fatal(err)
	}
	allowed := map[string]string{
		"PING":       "supported",
		"GET":        "supported",
		"MGET":       "supported",
		"SET":        "supported",
		"SETEX":      "supported",
		"PSETEX":     "supported",
		"DEL":        "supported",
		"UNLINK":     "supported",
		"EXPIRE":     "supported",
		"PEXPIRE":    "supported",
		"PERSIST":    "supported",
		"MSET":       "rejected",
		"RENAME":     "rejected",
		"HSET":       "rejected",
		"HDEL":       "rejected",
		"LPUSH":      "rejected",
		"RPUSH":      "rejected",
		"LPOP":       "rejected",
		"RPOP":       "rejected",
		"SADD":       "rejected",
		"SREM":       "rejected",
		"TTL":        "pass-through",
		"PTTL":       "pass-through",
		"EXISTS":     "pass-through",
		"MULTI":      "rejected",
		"EXEC":       "rejected",
		"WATCH":      "rejected",
		"SUBSCRIBE":  "rejected",
		"PSUBSCRIBE": "rejected",
		"MONITOR":    "rejected",
		"SELECT":     "supported",
		"BLPOP":      "rejected",
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
	assertContains(filepath.Join("deploy", "kubernetes", "observe-sidecar.yaml"), "image: ghcr.io/slizendb/slizen:0.2.1")
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
