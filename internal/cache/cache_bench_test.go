package cache

import (
	"testing"
	"time"
)

func BenchmarkCacheHit(b *testing.B) {
	c := New(1<<20, 1000, nil)
	c.Put("key", []byte("value"), time.Minute)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = c.Get("key")
	}
}

func BenchmarkCacheMiss(b *testing.B) {
	c := New(1<<20, 1000, nil)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = c.Get("missing")
	}
}

func BenchmarkConcurrentReads(b *testing.B) {
	c := New(1<<20, 1000, nil)
	c.Put("key", []byte("value"), time.Minute)
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, _ = c.Get("key")
		}
	})
}
