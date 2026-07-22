package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/slizendb/slizen/internal/config"
	"github.com/slizendb/slizen/internal/hotness"
	"github.com/slizendb/slizen/internal/metrics"
	"github.com/slizendb/slizen/internal/testutil"
	"github.com/slizendb/slizen/internal/upstream"
)

func newTestService(up upstream.Client, clock *testutil.FakeClock) *Service {
	return newTestServiceWithConfig(up, clock, nil)
}

func newTestServiceWithConfig(up upstream.Client, clock *testutil.FakeClock, edit func(*config.Config)) *Service {
	cfg := testConfig()
	if edit != nil {
		edit(&cfg)
	}
	return New(Options{
		Config:   cfg,
		Upstream: up,
		Metrics:  metrics.New(),
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		Clock:    clock,
		Version:  "test",
	})
}

type snapshotReadUpstream struct {
	*testutil.FakeUpstream

	mu             sync.Mutex
	blockedCommand string
	started        chan struct{}
	release        chan struct{}
}

type observedDoneContext struct {
	context.Context
	once     sync.Once
	observed chan struct{}
}

func (c *observedDoneContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.observed) })
	return nil
}

func newSnapshotReadUpstream() *snapshotReadUpstream {
	return &snapshotReadUpstream{FakeUpstream: testutil.NewFakeUpstream()}
}

func (u *snapshotReadUpstream) blockNext(command string) (<-chan struct{}, chan struct{}) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.blockedCommand = command
	u.started = make(chan struct{})
	u.release = make(chan struct{})
	return u.started, u.release
}

func (u *snapshotReadUpstream) Get(ctx context.Context, key string) (upstream.Value, error) {
	value, err := u.FakeUpstream.Get(ctx, key)
	if err != nil {
		return upstream.Value{}, err
	}
	if err := u.waitForRelease(ctx, "GET"); err != nil {
		return upstream.Value{}, err
	}
	return value, nil
}

func (u *snapshotReadUpstream) MGet(ctx context.Context, keys []string) ([]upstream.Value, error) {
	values, err := u.FakeUpstream.MGet(ctx, keys)
	if err != nil {
		return nil, err
	}
	if err := u.waitForRelease(ctx, "MGET"); err != nil {
		return nil, err
	}
	return values, nil
}

func (u *snapshotReadUpstream) waitForRelease(ctx context.Context, command string) error {
	u.mu.Lock()
	if u.blockedCommand != command {
		u.mu.Unlock()
		return nil
	}
	started := u.started
	release := u.release
	u.blockedCommand = ""
	u.mu.Unlock()

	close(started)
	select {
	case <-release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func testConfig() config.Config {
	cfg := config.Default()
	// Service behavior tests opt into cache mode explicitly; the production
	// default is observe-first.
	cfg.Mode = "cache"
	cfg.Cache.MaxBytes = 1 << 20
	cfg.Cache.MaxEntries = 1000
	cfg.Cache.MaxLocalTTL = time.Minute
	cfg.Hotness.Window = time.Second
	cfg.Hotness.EWMAAlpha = 1
	cfg.Hotness.PromotionThreshold = 1
	cfg.Hotness.DemotionThreshold = 0.1
	cfg.Hotness.MinimumHotWindows = 1
	cfg.Hotness.Cooldown = time.Second
	cfg.Hotness.MaxTrackedKeys = 1000
	return cfg
}

func promoteAndCache(t *testing.T, svc *Service, clock *testutil.FakeClock, key string) {
	t.Helper()
	if _, err := svc.Get(context.Background(), key); err != nil {
		t.Fatal(err)
	}
	clock.Advance(time.Second)
	if _, err := svc.Get(context.Background(), key); err != nil {
		t.Fatal(err)
	}
}

func scrapeMetrics(t *testing.T, svc *Service) string {
	t.Helper()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest("GET", "/metrics", nil)
	svc.metrics.Handler().ServeHTTP(recorder, request)
	if recorder.Code != 200 {
		t.Fatalf("metrics status = %d, want 200", recorder.Code)
	}
	return recorder.Body.String()
}

func TestGETMissThenPromotedHit(t *testing.T) {
	clock := testutil.NewFakeClock(time.Unix(0, 0))
	up := testutil.NewFakeUpstream()
	up.Put("k", []byte("value"), 0)
	svc := newTestService(up, clock)

	promoteAndCache(t, svc, clock, "k")
	if calls := up.GetCallCount("k"); calls != 2 {
		t.Fatalf("expected two upstream calls before local hit, got %d", calls)
	}
	got, err := svc.Get(context.Background(), "k")
	if err != nil {
		t.Fatal(err)
	}
	if !got.Exists || string(got.Data) != "value" {
		t.Fatalf("unexpected value: %+v", got)
	}
	if calls := up.GetCallCount("k"); calls != 2 {
		t.Fatalf("expected local cache hit without upstream call, got %d", calls)
	}
}

func TestGETCachesOnGuaranteedPromotion(t *testing.T) {
	clock := testutil.NewFakeClock(time.Unix(0, 0))
	up := testutil.NewFakeUpstream()
	up.Put("k", []byte("value"), 0)
	svc := newTestServiceWithConfig(up, clock, func(cfg *config.Config) {
		cfg.Hotness.PromotionThreshold = 2
		cfg.Hotness.DemotionThreshold = 1
		cfg.Hotness.MinimumHotWindows = 2
	})

	// The first two GETs form the required completed hot window. In the next
	// window, the second GET makes promotion mathematically inevitable and its
	// upstream response fills the cache immediately.
	for range 2 {
		if _, err := svc.Get(context.Background(), "k"); err != nil {
			t.Fatal(err)
		}
	}
	clock.Advance(time.Second)
	for range 2 {
		if _, err := svc.Get(context.Background(), "k"); err != nil {
			t.Fatal(err)
		}
	}
	if calls := up.GetCallCount("k"); calls != 4 {
		t.Fatalf("upstream GETs through guaranteed promotion = %d, want 4", calls)
	}
	if _, ok := svc.cache.Inspect("k"); !ok {
		t.Fatal("guaranteed promotion response was not cached")
	}

	value, err := svc.Get(context.Background(), "k")
	if err != nil {
		t.Fatal(err)
	}
	if !value.Exists || string(value.Data) != "value" {
		t.Fatalf("cached GET = %+v, want value", value)
	}
	if calls := up.GetCallCount("k"); calls != 4 {
		t.Fatalf("GET after guaranteed promotion reached upstream: %d calls", calls)
	}
	snapshot := svc.metrics.Snapshot()
	if snapshot.Promotions != 1 || snapshot.CacheHits != 1 {
		t.Fatalf("metrics after guaranteed promotion = %+v, want one promotion and one hit", snapshot)
	}
}

func TestGETFreshHitSkipsSlowPathContextAndCacheObservation(t *testing.T) {
	clock := testutil.NewFakeClock(time.Unix(0, 0))
	up := testutil.NewFakeUpstream()
	up.Put("k", []byte("value"), 0)
	svc := newTestService(up, clock)
	promoteAndCache(t, svc, clock, "k")

	// A fresh hit does not change these aggregates. Seed conspicuous values so
	// the test detects an unnecessary cache observation on the hit path.
	svc.metrics.ObserveCache(99, 123, 0)
	contextObserved := make(chan struct{})
	ctx := &observedDoneContext{Context: context.Background(), observed: contextObserved}
	value, err := svc.Get(ctx, "k")
	if err != nil {
		t.Fatal(err)
	}
	if !value.Exists || string(value.Data) != "value" {
		t.Fatalf("fresh hit = %+v, want cached value", value)
	}
	select {
	case <-contextObserved:
		t.Fatal("fresh hit derived a slow-path cancellation context")
	default:
	}

	metricsBody := scrapeMetrics(t, svc)
	for _, want := range []string{
		"slizen_cache_entries 99",
		"slizen_cache_bytes 123",
		"slizen_hot_keys 1",
	} {
		if !strings.Contains(metricsBody, want) {
			t.Fatalf("metrics missing %q:\n%s", want, metricsBody)
		}
	}
}

func TestGETObservationUpdatesHotnessMetrics(t *testing.T) {
	clock := testutil.NewFakeClock(time.Unix(0, 0))
	up := testutil.NewFakeUpstream()
	up.Put("k", []byte("value"), 0)
	svc := newTestServiceWithConfig(up, clock, func(cfg *config.Config) {
		cfg.Mode = "observe"
	})

	if _, err := svc.Get(context.Background(), "k"); err != nil {
		t.Fatal(err)
	}
	clock.Advance(time.Second)
	if _, err := svc.Get(context.Background(), "k"); err != nil {
		t.Fatal(err)
	}
	if body := scrapeMetrics(t, svc); !strings.Contains(body, "slizen_hot_keys 1") {
		t.Fatalf("promotion did not update hot-key gauge:\n%s", body)
	}

	oversized := strings.Repeat("k", hotness.MaxTrackedKeyBytes+1)
	if _, err := svc.Get(context.Background(), oversized); err != nil {
		t.Fatal(err)
	}
	if body := scrapeMetrics(t, svc); !strings.Contains(body, "slizen_hotness_oversized_observations_dropped_total 1") {
		t.Fatalf("oversized observation did not update metric:\n%s", body)
	}

	clock.Advance(2 * time.Second)
	if _, err := svc.Get(context.Background(), "k"); err != nil {
		t.Fatal(err)
	}
	if body := scrapeMetrics(t, svc); !strings.Contains(body, "slizen_hot_keys 0") {
		t.Fatalf("demotion did not update hot-key gauge:\n%s", body)
	}
}

func TestHotTrackingEvictionPromptlyDeletesCachedVictim(t *testing.T) {
	clock := testutil.NewFakeClock(time.Unix(0, 0))
	up := testutil.NewFakeUpstream()
	up.Put("victim", []byte("old"), 0)
	up.Put("replacement", []byte("other"), 0)
	svc := newTestServiceWithConfig(up, clock, func(cfg *config.Config) {
		cfg.Hotness.MaxTrackedKeys = 1
	})

	promoteAndCache(t, svc, clock, "victim")
	if _, ok := svc.cache.Inspect("victim"); !ok {
		t.Fatal("test setup did not cache hot victim")
	}
	if _, err := svc.Get(context.Background(), "replacement"); err != nil {
		t.Fatal(err)
	}
	if _, ok := svc.cache.Inspect("victim"); ok {
		t.Fatal("tracking eviction left a cached victim")
	}

	up.Put("victim", []byte("new"), 0)
	value, err := svc.Get(context.Background(), "victim")
	if err != nil {
		t.Fatal(err)
	}
	if string(value.Data) != "new" {
		t.Fatalf("GET after tracking eviction = %q, want new upstream value", value.Data)
	}
	if calls := up.GetCallCount("victim"); calls != 3 {
		t.Fatalf("victim upstream GETs = %d, want 3", calls)
	}
	if demotions := svc.metrics.Snapshot().Demotions; demotions != 1 {
		t.Fatalf("demotions = %d, want 1", demotions)
	}
}

func TestObserveModeNeverUsesLocalCache(t *testing.T) {
	clock := testutil.NewFakeClock(time.Unix(0, 0))
	up := testutil.NewFakeUpstream()
	up.Put("k", []byte("value"), 0)
	svc := newTestServiceWithConfig(up, clock, func(cfg *config.Config) {
		cfg.Mode = "observe"
		cfg.Hotness.PromotionThreshold = 2
		cfg.Hotness.DemotionThreshold = 1
		cfg.Hotness.MinimumHotWindows = 2
	})

	for range 2 {
		if _, err := svc.Get(context.Background(), "k"); err != nil {
			t.Fatal(err)
		}
	}
	clock.Advance(time.Second)
	for range 2 {
		if _, err := svc.Get(context.Background(), "k"); err != nil {
			t.Fatal(err)
		}
	}
	if !svc.tracker.IsHot("k") {
		t.Fatal("test setup did not exercise guaranteed promotion")
	}
	if stats := svc.cache.Stats(); stats.Entries != 0 {
		t.Fatalf("observe mode should not store cache entries after guaranteed promotion: %+v", stats)
	}

	got, err := svc.Get(context.Background(), "k")
	if err != nil {
		t.Fatal(err)
	}
	if !got.Exists || string(got.Data) != "value" {
		t.Fatalf("unexpected value: %+v", got)
	}
	if calls := up.GetCallCount("k"); calls != 5 {
		t.Fatalf("observe mode should forward every GET, got %d upstream calls", calls)
	}
	status := svc.Status(context.Background())
	if status.Mode != "observe" {
		t.Fatalf("status mode = %q", status.Mode)
	}
	if status.CacheHits != 0 {
		t.Fatalf("observe mode should not record cache hits: %+v", status)
	}
}

func TestObserveModeDoesNotCoalesceGETs(t *testing.T) {
	clock := testutil.NewFakeClock(time.Unix(0, 0))
	up := testutil.NewFakeUpstream()
	up.Put("k", []byte("value"), 0)
	up.SetDelay(50 * time.Millisecond)
	svc := newTestServiceWithConfig(up, clock, func(cfg *config.Config) {
		cfg.Mode = "observe"
	})

	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < 25; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			value, err := svc.Get(context.Background(), "k")
			if err != nil {
				t.Errorf("get failed: %v", err)
				return
			}
			if !value.Exists || string(value.Data) != "value" {
				t.Errorf("bad value: %+v", value)
			}
		}()
	}
	close(start)
	wg.Wait()

	if calls := up.GetCallCount("k"); calls != 25 {
		t.Fatalf("observe mode should forward every GET, got %d upstream calls", calls)
	}
}

func TestPrivacyHashModeDoesNotLeakRawKeyOrSecret(t *testing.T) {
	clock := testutil.NewFakeClock(time.Unix(0, 0))
	up := testutil.NewFakeUpstream()
	up.Put("product:iphone_17", []byte("value"), 0)
	svc := newTestServiceWithConfig(up, clock, func(cfg *config.Config) {
		cfg.Privacy.KeyVisibility = "hash"
		cfg.Privacy.KeyHashSecret = "super-secret"
	})
	promoteAndCache(t, svc, clock, "product:iphone_17")

	hotKeysJSON, err := json.Marshal(svc.HotKeys(10))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(hotKeysJSON), "product:iphone_17") || strings.Contains(string(hotKeysJSON), "iphone") {
		t.Fatalf("hotkeys leaked raw key: %s", hotKeysJSON)
	}
	if !strings.Contains(string(hotKeysJSON), "hmac-sha256:") {
		t.Fatalf("hotkeys should expose HMAC identifier: %s", hotKeysJSON)
	}

	statusJSON, err := json.Marshal(svc.Status(context.Background()))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(statusJSON), "super-secret") {
		t.Fatalf("status leaked key hash secret: %s", statusJSON)
	}
	if !strings.Contains(string(statusJSON), `"key_visibility":"hash"`) {
		t.Fatalf("status did not expose key visibility: %s", statusJSON)
	}
}

func TestMGETMixedLocalHitsAndUpstreamMisses(t *testing.T) {
	clock := testutil.NewFakeClock(time.Unix(0, 0))
	up := testutil.NewFakeUpstream()
	up.Put("hot", []byte("v1"), 0)
	up.Put("cold", []byte("v2"), 0)
	svc := newTestService(up, clock)
	promoteAndCache(t, svc, clock, "hot")

	values, err := svc.MGet(context.Background(), []string{"hot", "cold", "missing"})
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 3 {
		t.Fatalf("len = %d", len(values))
	}
	if !values[0].Exists || string(values[0].Data) != "v1" {
		t.Fatalf("bad hot value: %+v", values[0])
	}
	if !values[1].Exists || string(values[1].Data) != "v2" {
		t.Fatalf("bad cold value: %+v", values[1])
	}
	if values[2].Exists {
		t.Fatal("expected missing key")
	}
}

func TestMGETStaleFallbackRequiresEveryMissingKey(t *testing.T) {
	clock := testutil.NewFakeClock(time.Unix(0, 0))
	up := testutil.NewFakeUpstream()
	up.Put("stale", []byte("cached"), 0)
	svc := newTestServiceWithConfig(up, clock, func(cfg *config.Config) {
		cfg.Cache.MaxLocalTTL = time.Second
		cfg.Cache.AllowStaleOnUpstreamError = true
		cfg.Cache.StaleGrace = 5 * time.Second
	})
	promoteAndCache(t, svc, clock, "stale")

	clock.Advance(1500 * time.Millisecond)
	up.SetFailure(true)

	if _, err := svc.MGet(context.Background(), []string{"stale", "uncached"}); err == nil {
		t.Fatal("expected upstream error when only part of MGET can be served stale")
	}

	values, err := svc.MGet(context.Background(), []string{"stale"})
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 1 || !values[0].Exists || string(values[0].Data) != "cached" {
		t.Fatalf("expected stale cached value, got %+v", values)
	}
}

func TestStaleFallbackSurvivesUnrelatedCacheObservation(t *testing.T) {
	tests := []struct {
		name    string
		observe func(context.Context, *Service) error
	}{
		{
			name: "cache stats",
			observe: func(_ context.Context, svc *Service) error {
				_ = svc.CacheInfo()
				return nil
			},
		},
		{
			name: "hot key inspection",
			observe: func(_ context.Context, svc *Service) error {
				_ = svc.HotKeys(10)
				return nil
			},
		},
		{
			name: "unrelated traffic",
			observe: func(ctx context.Context, svc *Service) error {
				_, err := svc.Get(ctx, "other")
				return err
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clock := testutil.NewFakeClock(time.Unix(0, 0))
			up := testutil.NewFakeUpstream()
			up.Put("stale", []byte("cached"), 0)
			up.Put("other", []byte("other"), 0)
			svc := newTestServiceWithConfig(up, clock, func(cfg *config.Config) {
				cfg.Cache.MaxLocalTTL = time.Second
				cfg.Cache.AllowStaleOnUpstreamError = true
				cfg.Cache.StaleGrace = 5 * time.Second
			})
			promoteAndCache(t, svc, clock, "stale")
			clock.Advance(1500 * time.Millisecond)

			if err := tt.observe(context.Background(), svc); err != nil {
				t.Fatal(err)
			}
			up.SetFailure(true)
			value, err := svc.Get(context.Background(), "stale")
			if err != nil {
				t.Fatalf("stale fallback failed after %s: %v", tt.name, err)
			}
			if !value.Exists || string(value.Data) != "cached" {
				t.Fatalf("stale fallback after %s = %+v", tt.name, value)
			}
		})
	}
}

func TestSupportedWritesInvalidateCachedValues(t *testing.T) {
	tests := []struct {
		name    string
		command string
		args    []string
		keys    []string
	}{
		{name: "SET", command: "SET", args: []string{"k", "new"}, keys: []string{"k"}},
		{name: "SETEX", command: "SETEX", args: []string{"k", "60", "new"}, keys: []string{"k"}},
		{name: "PSETEX", command: "PSETEX", args: []string{"k", "60000", "new"}, keys: []string{"k"}},
		{name: "DEL multiple keys", command: "DEL", args: []string{"k1", "k2"}, keys: []string{"k1", "k2"}},
		{name: "UNLINK multiple keys", command: "UNLINK", args: []string{"k1", "k2"}, keys: []string{"k1", "k2"}},
		{name: "EXPIRE", command: "EXPIRE", args: []string{"k", "60"}, keys: []string{"k"}},
		{name: "PEXPIRE", command: "PEXPIRE", args: []string{"k", "60000"}, keys: []string{"k"}},
		{name: "PERSIST", command: "PERSIST", args: []string{"k"}, keys: []string{"k"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clock := testutil.NewFakeClock(time.Unix(0, 0))
			up := testutil.NewFakeUpstream()
			for _, key := range tt.keys {
				up.Put(key, []byte("old"), 0)
			}
			svc := newTestService(up, clock)
			for _, key := range tt.keys {
				promoteAndCache(t, svc, clock, key)
				if _, ok := svc.cache.Get(key); !ok {
					t.Fatalf("expected %q to be cached before %s", key, tt.command)
				}
			}

			before := svc.metrics.Snapshot().Invalidations
			if _, err := svc.ExecuteWrite(context.Background(), tt.command, tt.args, tt.keys); err != nil {
				t.Fatal(err)
			}
			for _, key := range tt.keys {
				if _, ok := svc.cache.Get(key); ok {
					t.Fatalf("expected %s to invalidate %q", tt.command, key)
				}
			}
			if got := svc.metrics.Snapshot().Invalidations - before; got != uint64(len(tt.keys)) {
				t.Fatalf("invalidations = %d, want %d", got, len(tt.keys))
			}
		})
	}
}

func TestWriteNilReplyStillInvalidatesCachedValue(t *testing.T) {
	clock := testutil.NewFakeClock(time.Unix(0, 0))
	up := testutil.NewFakeUpstream()
	up.Put("k", []byte("old"), 0)
	svc := newTestService(up, clock)
	promoteAndCache(t, svc, clock, "k")
	if _, ok := svc.cache.Get("k"); !ok {
		t.Fatal("expected cached value before write")
	}

	up.SetDoNil(true)
	result, err := svc.ExecuteWrite(context.Background(), "SET", []string{"k", "new", "NX"}, []string{"k"})
	if err != nil {
		t.Fatal(err)
	}
	if result != nil {
		t.Fatalf("expected nil write reply, got %#v", result)
	}
	if _, ok := svc.cache.Get("k"); ok {
		t.Fatal("expected nil-reply write to invalidate local cache")
	}
}

func TestConcurrentWritesDoNotRefillStaleValues(t *testing.T) {
	tests := []struct {
		name string
		read func(context.Context, *Service) (upstream.Value, error)
	}{
		{
			name: "GET",
			read: func(ctx context.Context, svc *Service) (upstream.Value, error) {
				return svc.Get(ctx, "k")
			},
		},
		{
			name: "MGET",
			read: func(ctx context.Context, svc *Service) (upstream.Value, error) {
				values, err := svc.MGet(ctx, []string{"k"})
				if err != nil {
					return upstream.Value{}, err
				}
				return values[0], nil
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clock := testutil.NewFakeClock(time.Unix(0, 0))
			up := newSnapshotReadUpstream()
			up.Put("k", []byte("old"), 0)
			svc := newTestService(up, clock)
			promoteAndCache(t, svc, clock, "k")
			svc.PurgeCache("k")

			started, release := up.blockNext(tt.name)
			type result struct {
				value upstream.Value
				err   error
			}
			readDone := make(chan result, 1)
			go func() {
				value, err := tt.read(context.Background(), svc)
				readDone <- result{value: value, err: err}
			}()
			<-started

			if _, err := svc.ExecuteWrite(context.Background(), "SET", []string{"k", "new"}, []string{"k"}); err != nil {
				t.Fatal(err)
			}
			postWrite, err := tt.read(context.Background(), svc)
			if err != nil {
				t.Fatal(err)
			}
			if string(postWrite.Data) != "new" {
				t.Fatalf("%s started after write = %q, want new value", tt.name, postWrite.Data)
			}
			close(release)
			readResult := <-readDone
			if readResult.err != nil {
				t.Fatal(readResult.err)
			}
			if string(readResult.value.Data) != "old" {
				t.Fatalf("concurrent %s = %q, want pre-write value", tt.name, readResult.value.Data)
			}

			got, err := svc.Get(context.Background(), "k")
			if err != nil {
				t.Fatal(err)
			}
			if string(got.Data) != "new" {
				t.Fatalf("GET after write = %q, stale refill was cached", got.Data)
			}
		})
	}
}

func TestEpochGuardSerializesCacheMutationWithInvalidation(t *testing.T) {
	clock := testutil.NewFakeClock(time.Unix(0, 0))
	svc := newTestService(testutil.NewFakeUpstream(), clock)
	const key = "k"
	epoch := svc.cacheEpoch(key)

	mutationEntered := make(chan struct{})
	releaseMutation := make(chan struct{})
	mutationDone := make(chan bool, 1)
	go func() {
		mutationDone <- svc.withCurrentCacheEpoch(key, epoch, func() {
			close(mutationEntered)
			<-releaseMutation
			svc.cache.Put(key, []byte("old"), time.Minute)
		})
	}()
	<-mutationEntered

	invalidationDone := make(chan int, 1)
	go func() {
		invalidationDone <- svc.invalidateCacheKeys([]string{key})
	}()
	select {
	case invalidated := <-invalidationDone:
		close(releaseMutation)
		<-mutationDone
		t.Fatalf("invalidation completed inside an epoch-guarded mutation; invalidated=%d", invalidated)
	case <-time.After(100 * time.Millisecond):
	}

	close(releaseMutation)
	if current := <-mutationDone; !current {
		t.Fatal("epoch unexpectedly changed before guarded mutation")
	}
	if invalidated := <-invalidationDone; invalidated != 1 {
		t.Fatalf("invalidated entries = %d, want 1", invalidated)
	}
	if _, ok := svc.cache.Get(key); ok {
		t.Fatal("invalidation returned with a stale refill in cache")
	}
}

func TestFullPurgeIsAtomicWithConcurrentRefills(t *testing.T) {
	clock := testutil.NewFakeClock(time.Unix(0, 0))
	up := newSnapshotReadUpstream()
	key := ""
	for i := 0; ; i++ {
		candidate := "purge-race-" + strconv.Itoa(i)
		if cacheEpochStripe(candidate) == 1 {
			key = candidate
			break
		}
	}
	up.Put(key, []byte("value"), 0)
	svc := newTestService(up, clock)
	promoteAndCache(t, svc, clock, key)
	svc.cache.Delete(key)

	// Hold a later stripe so the purge must retain the earlier key stripe
	// while establishing its all-cache barrier.
	barrier := &svc.cacheEpochs[2]
	barrier.mu.Lock()
	barrierLocked := true
	defer func() {
		if barrierLocked {
			barrier.mu.Unlock()
		}
	}()

	purgeDone := make(chan bool, 1)
	go func() {
		purgeDone <- svc.PurgeCache("")
	}()

	keyStripe := &svc.cacheEpochs[1]
	deadline := time.Now().Add(time.Second)
	for {
		if !keyStripe.mu.TryLock() {
			break
		}
		keyStripe.mu.Unlock()
		if time.Now().After(deadline) {
			t.Fatal("full purge did not acquire the cache-epoch barrier")
		}
		time.Sleep(time.Millisecond)
	}

	started, releaseRead := up.blockNext("GET")
	type readResult struct {
		value upstream.Value
		err   error
	}
	readDone := make(chan readResult, 1)
	go func() {
		value, err := svc.Get(context.Background(), key)
		readDone <- readResult{value: value, err: err}
	}()
	<-started

	barrier.mu.Unlock()
	barrierLocked = false
	if purged := <-purgeDone; !purged {
		t.Fatal("full purge returned false")
	}

	close(releaseRead)
	result := <-readDone
	if result.err != nil {
		t.Fatal(result.err)
	}
	if !result.value.Exists || string(result.value.Data) != "value" {
		t.Fatalf("concurrent GET = %+v, want upstream value", result.value)
	}
	if _, ok := svc.cache.Get(key); ok {
		t.Fatal("read that overlapped full purge repopulated the cache")
	}
}

func TestConcurrentMissingReadDoesNotDeletePostWriteRefill(t *testing.T) {
	clock := testutil.NewFakeClock(time.Unix(0, 0))
	up := newSnapshotReadUpstream()
	svc := newTestService(up, clock)

	started, release := up.blockNext("GET")
	type result struct {
		value upstream.Value
		err   error
	}
	oldReadDone := make(chan result, 1)
	go func() {
		value, err := svc.Get(context.Background(), "k")
		oldReadDone <- result{value: value, err: err}
	}()
	<-started

	if _, err := svc.ExecuteWrite(context.Background(), "SET", []string{"k", "new"}, []string{"k"}); err != nil {
		t.Fatal(err)
	}
	clock.Advance(time.Second)
	postWrite, err := svc.Get(context.Background(), "k")
	if err != nil {
		t.Fatal(err)
	}
	if !postWrite.Exists || string(postWrite.Data) != "new" {
		t.Fatalf("post-write GET = %+v, want new", postWrite)
	}
	if _, ok := svc.cache.Get("k"); !ok {
		t.Fatal("post-write refill was not cached")
	}

	close(release)
	oldRead := <-oldReadDone
	if oldRead.err != nil {
		t.Fatal(oldRead.err)
	}
	if oldRead.value.Exists {
		t.Fatalf("pre-write GET = %+v, want missing snapshot", oldRead.value)
	}
	item, ok := svc.cache.Get("k")
	if !ok || string(item.Value) != "new" {
		t.Fatalf("pre-write missing response deleted post-write refill: %+v, cached=%t", item, ok)
	}
}

func TestWriteErrorConservativelyInvalidatesCachedValue(t *testing.T) {
	clock := testutil.NewFakeClock(time.Unix(0, 0))
	up := testutil.NewFakeUpstream()
	up.Put("k", []byte("old"), 0)
	svc := newTestService(up, clock)
	promoteAndCache(t, svc, clock, "k")

	up.SetFailure(true)
	if _, err := svc.ExecuteWrite(context.Background(), "SET", []string{"k", "new"}, []string{"k"}); err == nil {
		t.Fatal("expected write error")
	}
	if _, ok := svc.cache.Get("k"); ok {
		t.Fatal("ambiguous write outcome left a cached value")
	}
}

func TestGETRespectsCanceledContext(t *testing.T) {
	clock := testutil.NewFakeClock(time.Unix(0, 0))
	up := testutil.NewFakeUpstream()
	up.Put("k", []byte("value"), 0)
	svc := newTestService(up, clock)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := svc.Get(ctx, "k"); err == nil {
		t.Fatal("expected canceled context error")
	}
	if calls := up.GetCallCount("k"); calls != 0 {
		t.Fatalf("expected upstream to see cancellation before request accounting, got %d calls", calls)
	}
}

func TestUpstreamFailureAndReadiness(t *testing.T) {
	clock := testutil.NewFakeClock(time.Unix(0, 0))
	up := testutil.NewFakeUpstream()
	up.Put("k", []byte("value"), 0)
	svc := newTestService(up, clock)
	svc.SetProxyActive(true)
	if err := svc.Ready(context.Background()); err != nil {
		t.Fatal(err)
	}
	up.SetFailure(true)
	if err := svc.Ready(context.Background()); err == nil {
		t.Fatal("expected readiness failure")
	}
	if _, err := svc.Get(context.Background(), "k"); err == nil {
		t.Fatal("expected upstream error")
	}
}

func TestSingleflightCoalescesGETMisses(t *testing.T) {
	clock := testutil.NewFakeClock(time.Unix(0, 0))
	up := testutil.NewFakeUpstream()
	up.Put("k", []byte("value"), 0)
	up.SetDelay(50 * time.Millisecond)
	svc := newTestService(up, clock)

	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			value, err := svc.Get(context.Background(), "k")
			if err != nil {
				t.Errorf("get failed: %v", err)
				return
			}
			if !value.Exists || string(value.Data) != "value" {
				t.Errorf("bad value: %+v", value)
			}
		}()
	}
	close(start)
	wg.Wait()

	if calls := up.GetCallCount("k"); calls != 1 {
		t.Fatalf("expected one upstream GET, got %d", calls)
	}
}

func TestSingleflightCanceledLeaderDoesNotPoisonFollower(t *testing.T) {
	clock := testutil.NewFakeClock(time.Unix(0, 0))
	up := newSnapshotReadUpstream()
	up.Put("k", []byte("value"), 0)
	started, release := up.blockNext("GET")
	svc := newTestService(up, clock)

	leaderCtx, cancelLeader := context.WithCancel(context.Background())
	leaderResult := make(chan error, 1)
	go func() {
		_, err := svc.Get(leaderCtx, "k")
		leaderResult <- err
	}()
	<-started

	type followerRead struct {
		value upstream.Value
		err   error
	}
	followerResult := make(chan followerRead, 1)
	followerWaiting := make(chan struct{})
	followerCtx := &observedDoneContext{Context: context.Background(), observed: followerWaiting}
	go func() {
		value, err := svc.Get(followerCtx, "k")
		followerResult <- followerRead{value: value, err: err}
	}()
	<-followerWaiting

	cancelLeader()
	select {
	case err := <-leaderResult:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("leader error = %v, want context canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled leader did not return while shared fetch remained active")
	}
	close(release)
	select {
	case result := <-followerResult:
		if result.err != nil {
			t.Fatalf("follower GET failed: %v", result.err)
		}
		if !result.value.Exists || string(result.value.Data) != "value" {
			t.Fatalf("follower value = %+v", result.value)
		}
	case <-time.After(time.Second):
		t.Fatal("follower did not receive shared result")
	}
	if calls := up.GetCallCount("k"); calls != 1 {
		t.Fatalf("upstream GET calls = %d, want 1", calls)
	}
}

func TestSharedReadTimeoutUsesStricterConfiguredBudget(t *testing.T) {
	tests := []struct {
		name     string
		proxy    time.Duration
		upstream time.Duration
		want     time.Duration
	}{
		{name: "proxy is stricter", proxy: 25 * time.Millisecond, upstream: time.Second, want: 25 * time.Millisecond},
		{name: "upstream is stricter", proxy: time.Second, upstream: 40 * time.Millisecond, want: 40 * time.Millisecond},
		{name: "invalid upstream uses fallback", proxy: time.Second, upstream: 0, want: time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := testConfig()
			cfg.Proxy.ReadTimeout = tt.proxy
			cfg.Upstream.ReadTimeout = tt.upstream
			svc := &Service{cfg: cfg}
			if got := svc.sharedReadTimeout(); got != tt.want {
				t.Fatalf("shared timeout = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestSingleflightSharedFetchIsBoundedByProxyTimeout(t *testing.T) {
	clock := testutil.NewFakeClock(time.Unix(0, 0))
	up := newSnapshotReadUpstream()
	up.Put("k", []byte("value"), 0)
	started, _ := up.blockNext("GET")
	svc := newTestServiceWithConfig(up, clock, func(cfg *config.Config) {
		cfg.Proxy.ReadTimeout = 20 * time.Millisecond
		cfg.Upstream.ReadTimeout = time.Hour
	})
	t.Cleanup(func() { _ = svc.Close() })

	result := make(chan error, 1)
	go func() {
		_, err := svc.Get(context.Background(), "k")
		result <- err
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("shared upstream GET did not start")
	}
	select {
	case err := <-result:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("shared GET error = %v, want deadline exceeded", err)
		}
	case <-time.After(time.Second):
		t.Fatal("shared GET outlived proxy read timeout")
	}
}

func TestCloseCancelsActiveSingleflightFetch(t *testing.T) {
	clock := testutil.NewFakeClock(time.Unix(0, 0))
	up := newSnapshotReadUpstream()
	up.Put("k", []byte("value"), 0)
	started, _ := up.blockNext("GET")
	svc := newTestServiceWithConfig(up, clock, func(cfg *config.Config) {
		cfg.Proxy.ReadTimeout = time.Hour
		cfg.Upstream.ReadTimeout = time.Hour
	})

	result := make(chan error, 1)
	go func() {
		_, err := svc.Get(context.Background(), "k")
		result <- err
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("shared upstream GET did not start")
	}
	if err := svc.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("shared GET error after Close = %v, want context canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Service.Close did not cancel active shared GET")
	}
}
