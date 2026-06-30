package proxy

import (
	"testing"

	"github.com/tidwall/redcon"
)

func TestCommandParsingIsCaseInsensitive(t *testing.T) {
	parsed, err := ParseCommand(redcon.Command{Args: [][]byte{[]byte("gEt"), []byte("key")}})
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Name != "GET" || parsed.Args[1] != "key" {
		t.Fatalf("unexpected parsed command: %+v", parsed)
	}
}

func TestUnsafeCommandsAreRejected(t *testing.T) {
	for _, command := range []string{"MULTI", "EXEC", "WATCH", "SUBSCRIBE", "BLPOP", "BZPOPMAX"} {
		if !isUnsafeCommand(command) {
			t.Fatalf("expected %s to be unsafe", command)
		}
	}
}

func TestSetGetOptionIsDetected(t *testing.T) {
	tests := []struct {
		name    string
		options []string
		want    bool
	}{
		{name: "no options", options: nil, want: false},
		{name: "ttl option value named get", options: []string{"EX", "GET"}, want: false},
		{name: "get option", options: []string{"GET"}, want: true},
		{name: "lowercase get option", options: []string{"px", "100", "get"}, want: true},
		{name: "conditional options", options: []string{"NX"}, want: false},
	}

	for _, tt := range tests {
		if got := setUsesGetOption(tt.options); got != tt.want {
			t.Fatalf("%s: setUsesGetOption(%v) = %t, want %t", tt.name, tt.options, got, tt.want)
		}
	}
}
