package cache

import (
	"bytes"
	"sync"
	"testing"
	"time"

	"github.com/slizendb/slizen/internal/testutil"
)

func TestCacheInsertionRetrievalAndCopy(t *testing.T) {
	clock := testutil.NewFakeClock(time.Unix(0, 0))
	c := New(1024, 10, clock)
	value := []byte("value")
	if !c.Put("key", value, time.Minute) {
		t.Fatal("put failed")
	}
	value[0] = 'X'
	got, ok := c.Get("key")
	if !ok {
		t.Fatal("expected hit")
	}
	if string(got.Value) != "value" {
		t.Fatalf("cached value mutated: %q", got.Value)
	}
	got.Value[0] = 'Y'
	again, _ := c.Get("key")
	if string(again.Value) != "value" {
		t.Fatalf("returned value was mutable: %q", again.Value)
	}
}

func TestCacheTTLExpiration(t *testing.T) {
	clock := testutil.NewFakeClock(time.Unix(0, 0))
	c := New(1024, 10, clock)
	c.Put("key", []byte("value"), time.Second)
	clock.Advance(time.Second)
	if _, ok := c.Get("key"); ok {
		t.Fatal("expected expired entry")
	}
	if stats := c.Stats(); stats.Entries != 0 {
		t.Fatalf("fresh lookup did not remove expired entry: %+v", stats)
	}
}

func TestExpiredEntrySurvivesStatsAndInspectForStaleFallback(t *testing.T) {
	clock := testutil.NewFakeClock(time.Unix(0, 0))
	c := New(1024, 10, clock)
	if !c.Put("key", []byte("value"), time.Second) {
		t.Fatal("put failed")
	}
	clock.Advance(2 * time.Second)

	if stats := c.Stats(); stats.Entries != 1 || stats.Bytes != EstimateSize("key", []byte("value")) {
		t.Fatalf("stats did not report retained stale entry: %+v", stats)
	}
	if _, ok := c.Inspect("key"); ok {
		t.Fatal("inspect reported expired entry as fresh")
	}
	item, ok := c.GetStale("key", 5*time.Second)
	if !ok || string(item.Value) != "value" {
		t.Fatalf("stats or inspect destroyed stale entry: item=%+v ok=%t", item, ok)
	}

	clock.Advance(4 * time.Second)
	if _, ok := c.GetStale("key", 5*time.Second); ok {
		t.Fatal("entry survived past stale grace")
	}
	if stats := c.Stats(); stats.Entries != 0 || stats.Bytes != 0 {
		t.Fatalf("expired stale lookup did not release entry: %+v", stats)
	}
}

func TestCacheLRUEviction(t *testing.T) {
	clock := testutil.NewFakeClock(time.Unix(0, 0))
	c := New(4096, 2, clock)
	c.Put("a", []byte("a"), time.Minute)
	c.Put("b", []byte("b"), time.Minute)
	if _, ok := c.Get("a"); !ok {
		t.Fatal("expected a")
	}
	c.Put("c", []byte("c"), time.Minute)
	if _, ok := c.Get("b"); ok {
		t.Fatal("expected b to be evicted")
	}
	if _, ok := c.Get("a"); !ok {
		t.Fatal("expected a to remain")
	}
}

func TestCacheByteSizeEnforcement(t *testing.T) {
	clock := testutil.NewFakeClock(time.Unix(0, 0))
	c := New(EstimateSize("a", []byte("1")), 10, clock)
	if !c.Put("a", []byte("1"), time.Minute) {
		t.Fatal("expected first entry to fit")
	}
	if !c.Put("b", []byte("2"), time.Minute) {
		t.Fatal("expected second entry to fit after eviction")
	}
	if stats := c.Stats(); stats.Entries != 1 || stats.Bytes > stats.MaxBytes {
		t.Fatalf("limits not enforced: %+v", stats)
	}
}

func TestConcurrentCacheAccess(t *testing.T) {
	clock := testutil.NewFakeClock(time.Unix(0, 0))
	c := New(1<<20, 1000, clock)
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			key := string(rune('a' + (i % 10)))
			c.Put(key, bytes.Repeat([]byte{byte(i)}, 8), time.Minute)
			_, _ = c.Get(key)
		}(i)
	}
	wg.Wait()
	if stats := c.Stats(); stats.Entries == 0 {
		t.Fatal("expected entries after concurrent access")
	}
}
