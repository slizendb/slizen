package proxy

import (
	"bytes"
	"testing"

	"github.com/tidwall/redcon"
)

func FuzzParseCommand(f *testing.F) {
	f.Add([]byte{}, []byte{})
	f.Add([]byte("gEt"), []byte("key"))
	f.Add([]byte("EVAL"), []byte("unsupported"))
	f.Add([]byte("GET"), []byte{0x00, 0xff, 0x80, '\r', '\n'})
	f.Add([]byte("SET"), bytes.Repeat([]byte("x"), 64<<10))

	f.Fuzz(func(t *testing.T, name, arg []byte) {
		var cmd redcon.Command
		if len(name) != 0 || len(arg) != 0 {
			cmd.Args = [][]byte{name, arg}
		}

		parsed, err := ParseCommand(cmd)
		if len(cmd.Args) == 0 {
			if err == nil {
				t.Fatal("ParseCommand accepted an empty command")
			}
			return
		}
		if err != nil {
			t.Fatalf("ParseCommand returned an error for %d arguments: %v", len(cmd.Args), err)
		}
		if parsed.Name != string(bytes.ToUpper(name)) {
			t.Fatalf("Name = %q, want %q", parsed.Name, bytes.ToUpper(name))
		}
		if len(parsed.Args) != 2 || parsed.Args[0] != string(name) || parsed.Args[1] != string(arg) {
			t.Fatalf("Args were not preserved: %#v", parsed.Args)
		}
	})
}

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

func TestMutatingCommandsAreExplicitlyRejected(t *testing.T) {
	for _, command := range []string{"MSET", "RENAME", "HSET", "HDEL", "LPUSH", "RPUSH", "LPOP", "RPOP", "SADD", "SREM"} {
		if !isRejectedMutation(command) {
			t.Fatalf("expected %s to be an explicitly rejected mutation", command)
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
