package cache

import (
	"strconv"
	"testing"
	"time"
)

func BenchmarkCacheHit(b *testing.B) {
	c := New(1<<20, 1000, nil)
	if !c.Put("key", []byte("value"), time.Minute) {
		b.Fatal("failed to seed cache")
	}
	b.ReportAllocs()
	for b.Loop() {
		_, _ = c.Get("key")
	}
}

func BenchmarkCacheMiss(b *testing.B) {
	c := New(1<<20, 1000, nil)
	b.ReportAllocs()
	for b.Loop() {
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

func BenchmarkCacheStats(b *testing.B) {
	for _, entries := range []int{1, 1_000, 100_000} {
		b.Run(strconv.Itoa(entries), func(b *testing.B) {
			c := New(1<<30, entries, nil)
			for i := 0; i < entries; i++ {
				if !c.Put("key:"+strconv.Itoa(i), []byte("value"), time.Hour) {
					b.Fatal("failed to seed cache")
				}
			}

			b.ReportAllocs()
			for b.Loop() {
				_ = c.Stats()
			}
		})
	}
}
