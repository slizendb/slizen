package privacy

import "testing"

func BenchmarkKeyHashing(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_ = KeyIdentifier("product:iphone_17", "salt", false)
	}
}
