package privacy

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
)

const (
	VisibilityHash  = "hash"
	VisibilityPlain = "plain"
)

// KeyIdentifier returns either the raw key or a stable HMAC-based identifier.
// The hash is intentionally short enough for humans and long enough to avoid
// practical collisions in admin output.
func KeyIdentifier(key, secret, visibility string) string {
	if visibility == VisibilityPlain {
		return key
	}
	return HMACKeyIdentifier(key, secret)
}

// HMACKeyIdentifier returns a privacy-safe stable key identifier.
func HMACKeyIdentifier(key, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(key))
	sum := mac.Sum(nil)
	return "hmac-sha256:" + hex.EncodeToString(sum[:16])
}
