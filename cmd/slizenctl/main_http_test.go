package main

import (
	"strings"
	"testing"
)

func TestDecodeBoundedAdminJSONRejectsOversizedBody(t *testing.T) {
	body := `{"payload":"` + strings.Repeat("x", maxAdminJSONResponseBytes) + `"}`
	if _, err := decodeBoundedAdminJSON(strings.NewReader(body)); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("decode error = %v, want response-size error", err)
	}
}

func TestDecodeBoundedAdminJSONAcceptsBoundedBody(t *testing.T) {
	value, err := decodeBoundedAdminJSON(strings.NewReader(`{"ok":true}`))
	if err != nil {
		t.Fatal(err)
	}
	object, ok := value.(map[string]any)
	if !ok || object["ok"] != true {
		t.Fatalf("decoded value = %#v", value)
	}
}
