package privacy

import (
	"strings"
	"testing"
)

func TestKeyIdentifierUsesStableSaltedHash(t *testing.T) {
	id1 := KeyIdentifier("product:iphone_17", "salt", false)
	id2 := KeyIdentifier("product:iphone_17", "salt", false)
	if id1 != id2 {
		t.Fatal("hash should be stable")
	}
	if strings.Contains(id1, "iphone") {
		t.Fatalf("hash leaked raw key: %s", id1)
	}
	if raw := KeyIdentifier("product:iphone_17", "salt", true); raw != "product:iphone_17" {
		t.Fatalf("raw key not exposed when requested: %s", raw)
	}
}
