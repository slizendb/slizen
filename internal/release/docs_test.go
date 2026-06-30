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
