package service

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/slizendb/slizen/internal/cache"
	"github.com/slizendb/slizen/internal/config"
	"github.com/slizendb/slizen/internal/testutil"
	"github.com/slizendb/slizen/internal/upstream"
)

func TestPerPrefixGETModes(t *testing.T) {
	tests := []struct {
		name         string
		mode         string
		wantUpstream int
		wantCached   bool
		wantTracked  int
	}{
		{name: "cache", mode: "cache", wantUpstream: 2, wantCached: true, wantTracked: 1},
		{name: "observe", mode: "observe", wantUpstream: 3, wantTracked: 1},
		{name: "deny", mode: "deny", wantUpstream: 3, wantTracked: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clock := testutil.NewFakeClock(time.Unix(0, 0))
			up := testutil.NewFakeUpstream()
			up.Put("policy:key", []byte("value"), 0)
			svc := newTestServiceWithConfig(up, clock, func(cfg *config.Config) {
				cfg.Cache.Policies = []config.CachePolicyConfig{policyRule("policy:", tt.mode, cfg)}
			})

			if _, err := svc.Get(context.Background(), "policy:key"); err != nil {
				t.Fatal(err)
			}
			clock.Advance(time.Second)
			if _, err := svc.Get(context.Background(), "policy:key"); err != nil {
				t.Fatal(err)
			}
			if _, err := svc.Get(context.Background(), "policy:key"); err != nil {
				t.Fatal(err)
			}

			if calls := up.GetCallCount("policy:key"); calls != tt.wantUpstream {
				t.Fatalf("upstream GETs = %d, want %d", calls, tt.wantUpstream)
			}
			_, cached := svc.cache.Inspect("policy:key")
			if cached != tt.wantCached {
				t.Fatalf("cached = %t, want %t", cached, tt.wantCached)
			}
			tracked, _ := svc.tracker.Stats()
			if tracked != tt.wantTracked {
				t.Fatalf("tracked keys = %d, want %d", tracked, tt.wantTracked)
			}
		})
	}
}

func TestPerPrefixObserveAndDenyDoNotCoalesceConcurrentGETs(t *testing.T) {
	const requests = 25
	tests := []struct {
		mode        string
		wantTracked int
	}{
		{mode: "observe", wantTracked: 1},
		{mode: "deny", wantTracked: 0},
	}
	for _, tt := range tests {
		t.Run(tt.mode, func(t *testing.T) {
			clock := testutil.NewFakeClock(time.Unix(0, 0))
			up := testutil.NewFakeUpstream()
			up.Put("policy:key", []byte("value"), 0)
			up.SetDelay(50 * time.Millisecond)
			svc := newTestServiceWithConfig(up, clock, func(cfg *config.Config) {
				cfg.Mode = "cache"
				cfg.Cache.Policies = []config.CachePolicyConfig{policyRule("policy:", tt.mode, cfg)}
			})

			var wg sync.WaitGroup
			start := make(chan struct{})
			for range requests {
				wg.Add(1)
				go func() {
					defer wg.Done()
					<-start
					value, err := svc.Get(context.Background(), "policy:key")
					if err != nil {
						t.Errorf("GET failed: %v", err)
						return
					}
					if !value.Exists || string(value.Data) != "value" {
						t.Errorf("GET value = %+v, want value", value)
					}
				}()
			}
			close(start)
			wg.Wait()

			if calls := up.GetCallCount("policy:key"); calls != requests {
				t.Fatalf("upstream GETs = %d, want %d", calls, requests)
			}
			if _, ok := svc.cache.Inspect("policy:key"); ok {
				t.Fatalf("%s policy cached a value", tt.mode)
			}
			tracked, _ := svc.tracker.Stats()
			if tracked != tt.wantTracked {
				t.Fatalf("tracked keys = %d, want %d", tracked, tt.wantTracked)
			}
		})
	}
}

func TestGlobalObserveModeOverridesCachePolicy(t *testing.T) {
	clock := testutil.NewFakeClock(time.Unix(0, 0))
	up := testutil.NewFakeUpstream()
	up.Put("catalog:key", []byte("value"), 0)
	svc := newTestServiceWithConfig(up, clock, func(cfg *config.Config) {
		cfg.Mode = "observe"
		cfg.Cache.Policies = []config.CachePolicyConfig{policyRule("catalog:", "cache", cfg)}
	})

	promoteAndCache(t, svc, clock, "catalog:key")
	if _, err := svc.Get(context.Background(), "catalog:key"); err != nil {
		t.Fatal(err)
	}
	if calls := up.GetCallCount("catalog:key"); calls != 3 {
		t.Fatalf("global observe forwarded %d GETs, want 3", calls)
	}
	if _, ok := svc.cache.Inspect("catalog:key"); ok {
		t.Fatal("global observe allowed a prefix cache rule to store locally")
	}
}

func TestPerPrefixItemAndTTLLimits(t *testing.T) {
	clock := testutil.NewFakeClock(time.Unix(0, 0))
	up := testutil.NewFakeUpstream()
	fitKey, largeKey := "fit:key", "large:key"
	value := []byte("value")
	up.Put(fitKey, value, 0)
	up.Put(largeKey, value, 0)
	svc := newTestServiceWithConfig(up, clock, func(cfg *config.Config) {
		cfg.Cache.Policies = []config.CachePolicyConfig{
			{Prefix: "fit:", Mode: "cache", MaxItemBytes: cache.EstimateSize(fitKey, value), MaxLocalTTL: 2 * time.Second},
			{Prefix: "large:", Mode: "cache", MaxItemBytes: cache.EstimateSize(largeKey, value) - 1, MaxLocalTTL: 2 * time.Second},
		}
	})

	promoteAndCache(t, svc, clock, fitKey)
	item, ok := svc.cache.Inspect(fitKey)
	if !ok {
		t.Fatal("entry exactly at max_item_bytes was not cached")
	}
	if item.TTL != 2*time.Second {
		t.Fatalf("local TTL = %s, want 2s", item.TTL)
	}
	if _, err := svc.Get(context.Background(), fitKey); err != nil {
		t.Fatal(err)
	}
	if calls := up.GetCallCount(fitKey); calls != 2 {
		t.Fatalf("fit key upstream GETs = %d, want 2", calls)
	}

	promoteAndCache(t, svc, clock, largeKey)
	if _, ok := svc.cache.Inspect(largeKey); ok {
		t.Fatal("entry above max_item_bytes was cached")
	}
	if _, err := svc.Get(context.Background(), largeKey); err != nil {
		t.Fatal(err)
	}
	if calls := up.GetCallCount(largeKey); calls != 3 {
		t.Fatalf("oversized key upstream GETs = %d, want 3", calls)
	}
}

func TestPerPrefixTTLUsesUpstreamMinimumAndSkipsZero(t *testing.T) {
	clock := testutil.NewFakeClock(time.Unix(0, 0))
	svc := newTestServiceWithConfig(testutil.NewFakeUpstream(), clock, func(cfg *config.Config) {
		cfg.Cache.Policies = []config.CachePolicyConfig{{
			Prefix: "ttl:", Mode: "cache", MaxItemBytes: cfg.Cache.MaxBytes, MaxLocalTTL: 2 * time.Second,
		}}
	})
	policy := svc.policies.Match("ttl:key")
	tests := []struct {
		name        string
		upstreamTTL time.Duration
		wantTTL     time.Duration
		wantCached  bool
	}{
		{name: "no upstream expiry", upstreamTTL: -1, wantTTL: 2 * time.Second, wantCached: true},
		{name: "upstream longer", upstreamTTL: 10 * time.Second, wantTTL: 2 * time.Second, wantCached: true},
		{name: "upstream shorter", upstreamTTL: 500 * time.Millisecond, wantTTL: 500 * time.Millisecond, wantCached: true},
		{name: "already expired", upstreamTTL: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key := "ttl:" + tt.name
			svc.storeLocal(key, upstream.Value{Exists: true, Data: []byte("value"), PTTL: tt.upstreamTTL}, policy)
			item, cached := svc.cache.Inspect(key)
			if cached != tt.wantCached {
				t.Fatalf("cached = %t, want %t", cached, tt.wantCached)
			}
			if cached && item.TTL != tt.wantTTL {
				t.Fatalf("TTL = %s, want %s", item.TTL, tt.wantTTL)
			}
		})
	}
}

func TestOversizedRefreshDeletesOlderStaleEntry(t *testing.T) {
	clock := testutil.NewFakeClock(time.Unix(0, 0))
	up := testutil.NewFakeUpstream()
	key := "limited:key"
	small := []byte("small")
	large := []byte("this value is too large")
	up.Put(key, small, 0)
	svc := newTestServiceWithConfig(up, clock, func(cfg *config.Config) {
		cfg.Cache.AllowStaleOnUpstreamError = true
		cfg.Cache.StaleGrace = 5 * time.Second
		cfg.Cache.Policies = []config.CachePolicyConfig{{
			Prefix: "limited:", Mode: "cache", MaxItemBytes: cache.EstimateSize(key, small), MaxLocalTTL: time.Second,
		}}
	})
	promoteAndCache(t, svc, clock, key)
	clock.Advance(1500 * time.Millisecond)
	up.Put(key, large, 0)

	value, err := svc.Get(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	if string(value.Data) != string(large) {
		t.Fatalf("refresh value = %q, want %q", value.Data, large)
	}
	if _, ok := svc.cache.GetStale(key, 5*time.Second); ok {
		t.Fatal("oversized refresh left the superseded stale value cached")
	}
	up.SetFailure(true)
	if _, err := svc.Get(context.Background(), key); err == nil {
		t.Fatal("superseded stale value was served after oversized refresh")
	}
}

func TestTrackingEvictionDeletesCachedEntryBeforeStaleGrace(t *testing.T) {
	clock := testutil.NewFakeClock(time.Unix(0, 0))
	up := testutil.NewFakeUpstream()
	key := "limited:key"
	up.Put(key, []byte("old"), 0)
	up.Put("other:key", []byte("other"), 0)
	svc := newTestServiceWithConfig(up, clock, func(cfg *config.Config) {
		cfg.Cache.MaxLocalTTL = time.Second
		cfg.Cache.AllowStaleOnUpstreamError = true
		cfg.Cache.StaleGrace = 5 * time.Second
		cfg.Hotness.MaxTrackedKeys = 1
		cfg.Cache.Policies = []config.CachePolicyConfig{policyRule("limited:", "cache", cfg)}
	})

	promoteAndCache(t, svc, clock, key)
	if _, err := svc.Get(context.Background(), "other:key"); err != nil {
		t.Fatal(err)
	}
	for _, snapshot := range svc.tracker.Snapshots(10) {
		if snapshot.Key == key {
			t.Fatal("test setup did not evict the cached key from hotness tracking")
		}
	}
	if _, ok := svc.cache.GetStale(key, 5*time.Second); ok {
		t.Fatal("tracking eviction retained a cached entry inside stale grace")
	}

	up.Put(key, []byte("new"), 0)
	value, err := svc.Get(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	if string(value.Data) != "new" {
		t.Fatalf("refresh value = %q, want new", value.Data)
	}
	if _, ok := svc.cache.GetStale(key, 5*time.Second); ok {
		t.Fatal("successful cold refresh left the superseded stale value cached")
	}

	up.SetFailure(true)
	if _, err := svc.Get(context.Background(), key); err == nil {
		t.Fatal("superseded stale value was served after a cold refresh")
	}
}

func TestMGETAppliesPolicyPerKeyAndPreservesOneBatch(t *testing.T) {
	clock := testutil.NewFakeClock(time.Unix(0, 0))
	up := newRecordingMGetUpstream()
	for key, value := range map[string]string{
		"cache:hit": "hit", "cache:miss": "miss", "observe:key": "observed", "deny:key": "denied",
	} {
		up.Put(key, []byte(value), 0)
	}
	svc := newTestServiceWithConfig(up, clock, func(cfg *config.Config) {
		cfg.Cache.Policies = []config.CachePolicyConfig{
			policyRule("cache:", "cache", cfg),
			policyRule("observe:", "observe", cfg),
			policyRule("deny:", "deny", cfg),
		}
	})
	promoteAndCache(t, svc, clock, "cache:hit")
	if _, err := svc.Get(context.Background(), "cache:miss"); err != nil {
		t.Fatal(err)
	}
	clock.Advance(time.Second)

	keys := []string{"cache:hit", "cache:miss", "observe:key", "deny:key"}
	values, err := svc.MGet(context.Background(), keys)
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range []string{"hit", "miss", "observed", "denied"} {
		if !values[i].Exists || string(values[i].Data) != want {
			t.Fatalf("value[%d] = %+v, want %q", i, values[i], want)
		}
	}
	if got, want := up.lastMGet(), []string{"cache:miss", "observe:key", "deny:key"}; !equalStrings(got, want) {
		t.Fatalf("upstream MGET keys = %#v, want %#v", got, want)
	}
	if _, ok := svc.cache.Inspect("cache:miss"); !ok {
		t.Fatal("hot cache-mode MGET miss was not stored")
	}
	if _, ok := svc.cache.Inspect("observe:key"); ok {
		t.Fatal("observe-mode MGET key was cached")
	}
	if _, ok := svc.cache.Inspect("deny:key"); ok {
		t.Fatal("deny-mode MGET key was cached")
	}
	for _, snapshot := range svc.tracker.Snapshots(100) {
		if snapshot.Key == "deny:key" {
			t.Fatal("deny-mode MGET key was tracked")
		}
	}
}

func TestMGETKeepsPerKeyCacheLimitsAligned(t *testing.T) {
	clock := testutil.NewFakeClock(time.Unix(0, 0))
	up := newRecordingMGetUpstream()
	tightKey, allowedKey := "tight:key", "allow:key"
	tightValue, allowedValue := []byte("large"), []byte("value")
	up.Put(tightKey, tightValue, 0)
	up.Put(allowedKey, allowedValue, 0)
	itemSize := cache.EstimateSize(allowedKey, allowedValue)
	svc := newTestServiceWithConfig(up, clock, func(cfg *config.Config) {
		cfg.Cache.Policies = []config.CachePolicyConfig{
			{Prefix: "tight:", Mode: "cache", MaxItemBytes: itemSize - 1, MaxLocalTTL: 4 * time.Second},
			{Prefix: "allow:", Mode: "cache", MaxItemBytes: itemSize, MaxLocalTTL: 1500 * time.Millisecond},
		}
	})

	for _, key := range []string{tightKey, allowedKey} {
		if _, err := svc.Get(context.Background(), key); err != nil {
			t.Fatal(err)
		}
	}
	clock.Advance(time.Second)

	keys := []string{tightKey, allowedKey}
	values, err := svc.MGet(context.Background(), keys)
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range []string{"large", "value"} {
		if !values[i].Exists || string(values[i].Data) != want {
			t.Fatalf("value[%d] = %+v, want %q", i, values[i], want)
		}
	}
	if calls := up.mgetCallCount(); calls != 1 {
		t.Fatalf("upstream MGET calls = %d, want 1", calls)
	}
	if got := up.lastMGet(); !equalStrings(got, keys) {
		t.Fatalf("upstream MGET keys = %#v, want %#v", got, keys)
	}
	if _, ok := svc.cache.Inspect(tightKey); ok {
		t.Fatal("value above the matching item limit was cached")
	}
	item, ok := svc.cache.Inspect(allowedKey)
	if !ok {
		t.Fatal("value at the matching item limit was not cached")
	}
	if item.TTL != 1500*time.Millisecond {
		t.Fatalf("allowed key TTL = %s, want 1.5s", item.TTL)
	}
}

func policyRule(prefix, mode string, cfg *config.Config) config.CachePolicyConfig {
	rule := config.CachePolicyConfig{Prefix: prefix, Mode: mode}
	if mode == "cache" {
		rule.MaxItemBytes = cfg.Cache.MaxBytes
		rule.MaxLocalTTL = cfg.Cache.MaxLocalTTL
	}
	return rule
}

type recordingMGetUpstream struct {
	*testutil.FakeUpstream
	mu        sync.Mutex
	lastKeys  []string
	mgetCalls int
}

func newRecordingMGetUpstream() *recordingMGetUpstream {
	return &recordingMGetUpstream{FakeUpstream: testutil.NewFakeUpstream()}
}

func (u *recordingMGetUpstream) MGet(ctx context.Context, keys []string) ([]upstream.Value, error) {
	u.mu.Lock()
	u.lastKeys = append([]string(nil), keys...)
	u.mgetCalls++
	u.mu.Unlock()
	return u.FakeUpstream.MGet(ctx, keys)
}

func (u *recordingMGetUpstream) lastMGet() []string {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]string(nil), u.lastKeys...)
}

func (u *recordingMGetUpstream) mgetCallCount() int {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.mgetCalls
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}
