package privacy

import (
	"strings"
	"testing"
)

func TestKeyIdentifierUsesStableSaltedHash(t *testing.T) {
	id1 := KeyIdentifier("product:iphone_17", "secret", VisibilityHash)
	id2 := KeyIdentifier("product:iphone_17", "secret", VisibilityHash)
	if id1 != id2 {
		t.Fatal("hash should be stable")
	}
	if other := KeyIdentifier("product:iphone_17", "other-secret", VisibilityHash); other == id1 {
		t.Fatal("different secrets should produce different identifiers")
	}
	if strings.Contains(id1, "iphone") {
		t.Fatalf("hash leaked raw key: %s", id1)
	}
	if !strings.HasPrefix(id1, "hmac-sha256:") {
		t.Fatalf("unexpected key identifier prefix: %s", id1)
	}
	if raw := KeyIdentifier("product:iphone_17", "secret", VisibilityPlain); raw != "product:iphone_17" {
		t.Fatalf("raw key not exposed when requested: %s", raw)
	}
}
