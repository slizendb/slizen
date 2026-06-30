package metrics

import "testing"

func TestCommandLabelBoundsUserInput(t *testing.T) {
	tests := map[string]string{
		"GET":       "GET",
		"get":       "GET",
		"MULTI":     "unsafe",
		"BLPOP":     "unsafe",
		"RANDOM123": "unsupported",
		"":          "invalid",
		"UNKNOWN":   "invalid",
	}

	for command, want := range tests {
		if got := commandLabel(command); got != want {
			t.Fatalf("commandLabel(%q) = %q, want %q", command, got, want)
		}
	}
}
