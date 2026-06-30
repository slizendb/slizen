package privacy

import (
	"crypto/sha256"
	"encoding/hex"
)

// KeyIdentifier returns either the raw key or a stable salted hash. The hash is
// intentionally short enough for humans and long enough to avoid practical
// collisions in admin output.
func KeyIdentifier(key, salt string, exposeRaw bool) string {
	if exposeRaw {
		return key
	}
	sum := sha256.Sum256([]byte(salt + "\x00" + key))
	return "sha256:" + hex.EncodeToString(sum[:16])
}
