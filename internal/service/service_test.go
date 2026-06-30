package service

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/slizendb/slizen/internal/config"
	"github.com/slizendb/slizen/internal/metrics"
	"github.com/slizendb/slizen/internal/testutil"
)

func newTestService(up *testutil.FakeUpstream, clock *testutil.FakeClock) *Service {
	return newTestServiceWithConfig(up, clock, nil)
}

func newTestServiceWithConfig(up *testutil.FakeUpstream, clock *testutil.FakeClock, edit func(*config.Config)) *Service {
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

func testConfig() config.Config {
	cfg := config.Default()
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

func TestSETInvalidatesCachedValue(t *testing.T) {
	clock := testutil.NewFakeClock(time.Unix(0, 0))
	up := testutil.NewFakeUpstream()
	up.Put("k", []byte("old"), 0)
	svc := newTestService(up, clock)
	promoteAndCache(t, svc, clock, "k")

	if _, err := svc.ExecuteWrite(context.Background(), "SET", []string{"k", "new"}, []string{"k"}); err != nil {
		t.Fatal(err)
	}
	got, err := svc.Get(context.Background(), "k")
	if err != nil {
		t.Fatal(err)
	}
	if string(got.Data) != "new" {
		t.Fatalf("expected new value, got %q", got.Data)
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

func TestDELInvalidatesCachedValue(t *testing.T) {
	clock := testutil.NewFakeClock(time.Unix(0, 0))
	up := testutil.NewFakeUpstream()
	up.Put("k", []byte("value"), 0)
	svc := newTestService(up, clock)
	promoteAndCache(t, svc, clock, "k")

	if _, err := svc.ExecuteWrite(context.Background(), "DEL", []string{"k"}, []string{"k"}); err != nil {
		t.Fatal(err)
	}
	got, err := svc.Get(context.Background(), "k")
	if err != nil {
		t.Fatal(err)
	}
	if got.Exists {
		t.Fatal("expected deleted key")
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
