package service

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/slizendb/slizen/internal/cache"
	"github.com/slizendb/slizen/internal/config"
	"github.com/slizendb/slizen/internal/hotness"
	"github.com/slizendb/slizen/internal/testutil"
	"github.com/slizendb/slizen/internal/upstream"
)

func TestAdmissionFirstMissThenLaterReadPromotesCandidate(t *testing.T) {
	clock := testutil.NewFakeClock(time.Unix(0, 0))
	up := testutil.NewFakeUpstream()
	up.Put("k", []byte("value"), 0)
	svc := newTestService(up, clock)

	first, err := svc.Get(context.Background(), "k")
	if err != nil {
		t.Fatal(err)
	}
	if !first.Exists || string(first.Data) != "value" {
		t.Fatalf("first GET = %+v, want upstream value", first)
	}
	if _, ok := svc.cache.Inspect("k"); ok {
		t.Fatal("first GET stored directly in the protected cache")
	}
	if candidate, ok := svc.probationary.Inspect("k"); !ok || string(candidate.Value) != "value" {
		t.Fatalf("probationary candidate after first GET = %q, present=%t", candidate.Value, ok)
	}
	firstMetrics := svc.metrics.Snapshot()
	if firstMetrics.CacheMisses != 1 || firstMetrics.CacheMissesNotAdmitted != 1 || firstMetrics.CacheHits != 0 {
		t.Fatalf("metrics after first GET = %+v, want one not-admitted miss", firstMetrics)
	}

	second, err := svc.Get(context.Background(), "k")
	if err != nil {
		t.Fatal(err)
	}
	if !second.Exists || string(second.Data) != "value" {
		t.Fatalf("second GET = %+v, want admitted value", second)
	}
	if calls := up.GetCallCount("k"); calls != 1 {
		t.Fatalf("upstream GETs = %d, want one", calls)
	}
	if _, ok := svc.probationary.Inspect("k"); ok {
		t.Fatal("promoted candidate remained in probationary cache")
	}
	if protected, ok := svc.cache.Inspect("k"); !ok || string(protected.Value) != "value" {
		t.Fatalf("protected value after admission = %q, present=%t", protected.Value, ok)
	}
	secondMetrics := svc.metrics.Snapshot()
	if secondMetrics.CacheMisses != 1 || secondMetrics.CacheHits != 1 || secondMetrics.Promotions != 1 {
		t.Fatalf("metrics after admission = %+v, want one miss, hit, and promotion", secondMetrics)
	}
}

func TestAdmissionPromotionPreservesCandidateExpiry(t *testing.T) {
	clock := testutil.NewFakeClock(time.Unix(0, 0))
	up := testutil.NewFakeUpstream()
	up.Put("k", []byte("value"), 0)
	svc := newTestServiceWithConfig(up, clock, func(cfg *config.Config) {
		cfg.Cache.MaxLocalTTL = 10 * time.Second
		cfg.Hotness.Window = 5 * time.Second
	})

	if _, err := svc.Get(context.Background(), "k"); err != nil {
		t.Fatal(err)
	}
	candidate, ok := svc.probationary.Inspect("k")
	if !ok {
		t.Fatal("first GET did not retain a probationary candidate")
	}
	if candidate.TTL != 5*time.Second {
		t.Fatalf("candidate TTL = %s, want hotness-window bound 5s", candidate.TTL)
	}

	clock.Advance(2 * time.Second)
	if _, err := svc.Get(context.Background(), "k"); err != nil {
		t.Fatal(err)
	}
	protected, ok := svc.cache.Inspect("k")
	if !ok {
		t.Fatal("second GET did not promote the candidate")
	}
	if !protected.ExpiresAt.Equal(candidate.ExpiresAt) {
		t.Fatalf("promotion restarted TTL: candidate expires %s, protected expires %s", candidate.ExpiresAt, protected.ExpiresAt)
	}
	if protected.TTL != 3*time.Second {
		t.Fatalf("protected TTL after delayed promotion = %s, want remaining 3s", protected.TTL)
	}

	clock.Advance(3 * time.Second)
	if _, ok := svc.cache.Get("k"); ok {
		t.Fatal("promoted value survived the candidate's original expiry")
	}
}

func TestAdmissionConcurrentPromotionLoserRechecksProtectedCache(t *testing.T) {
	clock := testutil.NewFakeClock(time.Unix(0, 0))
	up := testutil.NewFakeUpstream()
	up.Put("k", []byte("value"), 0)
	svc := newTestService(up, clock)

	if _, err := svc.Get(context.Background(), "k"); err != nil {
		t.Fatal(err)
	}
	candidate, ok := svc.probationary.Inspect("k")
	if !ok {
		t.Fatal("first GET did not retain a probationary candidate")
	}

	// Hold the key stripe in the exact transfer window: the tracker is HOT and
	// probation is empty, but the protected value is not visible yet.
	stripe := &svc.cacheEpochs[cacheEpochStripe("k")]
	stripe.mu.Lock()
	observation := svc.tracker.Admit("k")
	if observation.State != hotness.StateHot {
		stripe.mu.Unlock()
		t.Fatalf("admission state = %s, want HOT", observation.State)
	}
	stripe.promotionInProgress.Store(true)
	svc.probationary.Delete("k")

	type result struct {
		item cache.EntrySnapshot
		ok   bool
	}
	started := make(chan struct{})
	resultCh := make(chan result, 1)
	go func() {
		close(started)
		item, ok := svc.getFreshCachedInState("k", hotness.StateCold)
		resultCh <- result{item: item, ok: ok}
	}()
	<-started

	var early *result
	select {
	case got := <-resultCh:
		early = &got
	case <-time.After(20 * time.Millisecond):
	}
	if !svc.cache.PutUntil("k", candidate.Value, candidate.ExpiresAt) {
		stripe.promotionInProgress.Store(false)
		stripe.mu.Unlock()
		t.Fatal("failed to install protected candidate")
	}
	stripe.promotionInProgress.Store(false)
	stripe.mu.Unlock()

	if early != nil {
		t.Fatalf("promotion loser returned during transfer: present=%t value=%q", early.ok, early.item.Value)
	}
	got := <-resultCh
	if !got.ok || string(got.item.Value) != "value" {
		t.Fatalf("promotion loser = present:%t value:%q, want protected value", got.ok, got.item.Value)
	}
	if calls := up.GetCallCount("k"); calls != 1 {
		t.Fatalf("upstream GETs = %d, want initial miss only", calls)
	}
}

func TestAdmissionIneligibleReadsNeverRetainCandidates(t *testing.T) {
	oversizedKey := strings.Repeat("k", hotness.MaxTrackedKeyBytes+1)
	tests := []struct {
		name string
		key  string
		put  bool
		edit func(*config.Config, string, []byte)
	}{
		{
			name: "observe",
			key:  "observe:k",
			put:  true,
			edit: func(cfg *config.Config, _ string, _ []byte) {
				cfg.Mode = "observe"
			},
		},
		{
			name: "deny",
			key:  "deny:k",
			put:  true,
			edit: func(cfg *config.Config, _ string, _ []byte) {
				cfg.Cache.Policies = []config.CachePolicyConfig{{Prefix: "deny:", Mode: "deny"}}
			},
		},
		{name: "missing", key: "missing:k"},
		{name: "oversized key", key: oversizedKey, put: true},
		{
			name: "item above policy limit",
			key:  "large:k",
			put:  true,
			edit: func(cfg *config.Config, key string, value []byte) {
				cfg.Cache.Policies = []config.CachePolicyConfig{{
					Prefix:       "large:",
					Mode:         "cache",
					MaxItemBytes: cache.EstimateSize(key, value) - 1,
					MaxLocalTTL:  cfg.Cache.MaxLocalTTL,
				}}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clock := testutil.NewFakeClock(time.Unix(0, 0))
			up := testutil.NewFakeUpstream()
			value := []byte("value")
			if tt.put {
				up.Put(tt.key, value, 0)
			}
			svc := newTestServiceWithConfig(up, clock, func(cfg *config.Config) {
				if tt.edit != nil {
					tt.edit(cfg, tt.key, value)
				}
			})

			if _, err := svc.Get(context.Background(), tt.key); err != nil {
				t.Fatal(err)
			}
			if _, ok := svc.cache.Inspect(tt.key); ok {
				t.Fatal("ineligible read retained a protected value")
			}
			if svc.probationary != nil {
				if _, ok := svc.probationary.Inspect(tt.key); ok {
					t.Fatal("ineligible read retained a probationary candidate")
				}
			}
		})
	}
}

type admissionBlockingGetUpstream struct {
	*testutil.FakeUpstream
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func newAdmissionBlockingGetUpstream() *admissionBlockingGetUpstream {
	return &admissionBlockingGetUpstream{
		FakeUpstream: testutil.NewFakeUpstream(),
		started:      make(chan struct{}),
		release:      make(chan struct{}),
	}
}

func (u *admissionBlockingGetUpstream) Get(ctx context.Context, key string) (upstream.Value, error) {
	u.once.Do(func() { close(u.started) })
	select {
	case <-u.release:
		return u.FakeUpstream.Get(ctx, key)
	case <-ctx.Done():
		return upstream.Value{}, ctx.Err()
	}
}

func TestAdmissionSingleflightWaitersDoNotCountAsSecondRead(t *testing.T) {
	const requests = 32
	clock := testutil.NewFakeClock(time.Unix(0, 0))
	up := newAdmissionBlockingGetUpstream()
	up.Put("k", []byte("value"), 0)
	svc := newTestService(up, clock)

	start := make(chan struct{})
	errs := make(chan error, requests)
	for range requests {
		go func() {
			<-start
			_, err := svc.Get(context.Background(), "k")
			errs <- err
		}()
	}
	close(start)
	<-up.started

	deadline := time.Now().Add(2 * time.Second)
	for svc.metrics.Snapshot().CacheMisses != requests {
		if time.Now().After(deadline) {
			t.Fatalf("only %d/%d callers joined the cold miss", svc.metrics.Snapshot().CacheMisses, requests)
		}
		time.Sleep(time.Millisecond)
	}
	// Every caller has crossed the miss accounting point and is now either in
	// the shared flight or about to join it; yield once before completing it.
	time.Sleep(10 * time.Millisecond)
	close(up.release)
	for range requests {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}

	if calls := up.GetCallCount("k"); calls != 1 {
		t.Fatalf("coalesced upstream GETs = %d, want one", calls)
	}
	if _, ok := svc.cache.Inspect("k"); ok {
		t.Fatal("singleflight waiters incorrectly promoted the first response")
	}
	if _, ok := svc.probationary.Inspect("k"); !ok {
		t.Fatal("shared first response did not leave one probationary candidate")
	}
	snapshot := svc.metrics.Snapshot()
	if snapshot.CacheHits != 0 || snapshot.Promotions != 0 {
		t.Fatalf("singleflight waiters counted as later reads: %+v", snapshot)
	}

	if _, err := svc.Get(context.Background(), "k"); err != nil {
		t.Fatal(err)
	}
	if calls := up.GetCallCount("k"); calls != 1 {
		t.Fatalf("later admission read reached upstream: %d calls", calls)
	}
	if snapshot := svc.metrics.Snapshot(); snapshot.CacheHits != 1 || snapshot.Promotions != 1 {
		t.Fatalf("later read did not admit exactly once: %+v", snapshot)
	}
}

type admissionBlockingWriteUpstream struct {
	*testutil.FakeUpstream
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func newAdmissionBlockingWriteUpstream() *admissionBlockingWriteUpstream {
	return &admissionBlockingWriteUpstream{
		FakeUpstream: testutil.NewFakeUpstream(),
		started:      make(chan struct{}),
		release:      make(chan struct{}),
	}
}

func (u *admissionBlockingWriteUpstream) Do(ctx context.Context, args ...string) (any, error) {
	result, err := u.FakeUpstream.Do(ctx, args...)
	if err != nil {
		return nil, err
	}
	u.once.Do(func() { close(u.started) })
	select {
	case <-u.release:
		return result, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func TestAdmissionCandidateIsInvalidatedBeforeAndAfterInflightWrite(t *testing.T) {
	clock := testutil.NewFakeClock(time.Unix(0, 0))
	up := newAdmissionBlockingWriteUpstream()
	up.Put("k", []byte("old"), 0)
	svc := newTestService(up, clock)

	if _, err := svc.Get(context.Background(), "k"); err != nil {
		t.Fatal(err)
	}
	if _, ok := svc.probationary.Inspect("k"); !ok {
		t.Fatal("test setup did not retain the old candidate")
	}

	writeDone := make(chan error, 1)
	go func() {
		_, err := svc.ExecuteWrite(context.Background(), "SET", []string{"k", "new"}, []string{"k"})
		writeDone <- err
	}()
	<-up.started
	if _, ok := svc.probationary.Inspect("k"); ok {
		t.Fatal("candidate remained visible while the write was in flight")
	}

	value, err := svc.Get(context.Background(), "k")
	if err != nil {
		t.Fatal(err)
	}
	if !value.Exists || string(value.Data) != "new" {
		t.Fatalf("GET during write = %+v, want new upstream value", value)
	}
	if candidate, ok := svc.probationary.Inspect("k"); !ok || string(candidate.Value) != "new" {
		t.Fatalf("refill during write = %q, present=%t", candidate.Value, ok)
	}

	close(up.release)
	if err := <-writeDone; err != nil {
		t.Fatal(err)
	}
	if _, ok := svc.cache.Inspect("k"); ok {
		t.Fatal("write finalization left a protected value for a cold key")
	}
	if _, ok := svc.probationary.Inspect("k"); ok {
		t.Fatal("write finalization did not reject a refill created during the write")
	}
}
