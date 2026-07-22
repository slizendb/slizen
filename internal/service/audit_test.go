package service

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/slizendb/slizen/internal/config"
	"github.com/slizendb/slizen/internal/hotness"
	"github.com/slizendb/slizen/internal/testutil"
)

func TestAuditObserveReportIsPrivateAndDeterministic(t *testing.T) {
	start := time.Date(2026, time.July, 17, 12, 0, 0, 0, time.FixedZone("test", 3*60*60))
	clock := testutil.NewFakeClock(start)
	up := testutil.NewFakeUpstream()
	const (
		key          = "private-policy:customer-42"
		value        = "redis-value-must-not-appear"
		policyPrefix = "private-policy:"
	)
	up.Put(key, []byte(value), 0)
	svc := newTestServiceWithConfig(up, clock, func(cfg *config.Config) {
		cfg.Mode = "observe"
		cfg.Privacy.KeyVisibility = "hash"
		cfg.Privacy.KeyHashSecret = "audit-secret"
		cfg.Cache.Policies = []config.CachePolicyConfig{{
			Prefix:       policyPrefix,
			Mode:         "cache",
			MaxItemBytes: cfg.Cache.MaxBytes,
			MaxLocalTTL:  cfg.Cache.MaxLocalTTL,
		}}
	})

	for range 5 {
		if _, err := svc.Get(context.Background(), key); err != nil {
			t.Fatal(err)
		}
	}
	clock.Advance(time.Second)

	got := svc.Audit(1)
	if got.SchemaVersion != AuditSchemaVersion || got.GeneratedAt != "2026-07-17T09:00:01Z" {
		t.Fatalf("audit metadata = %+v", got)
	}
	if got.MeasurementWindow != "1s" || got.Mode != "observe" || got.KeyVisibility != "hash" {
		t.Fatalf("audit context = %+v", got)
	}
	if got.TrackedKeys != 1 || got.ReturnedEntries != 1 || got.Truncated {
		t.Fatalf("audit bounds = %+v", got)
	}
	if len(got.Entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(got.Entries))
	}
	entry := got.Entries[0]
	if !strings.HasPrefix(entry.ID, "hmac-sha256:") {
		t.Fatalf("id = %q, want HMAC identifier", entry.ID)
	}
	if entry.RequestRate != 5 || entry.HotnessState != "HOT" || entry.EffectivePolicyMode != "observe" {
		t.Fatalf("entry telemetry = %+v", entry)
	}
	if entry.Recommendation != AuditRecommendationReviewForCache {
		t.Fatalf("recommendation = %q", entry.Recommendation)
	}
	wantReasons := []string{AuditReasonEffectivePolicyObserve, AuditReasonHotnessStateHot}
	if !reflect.DeepEqual(entry.ReasonCodes, wantReasons) {
		t.Fatalf("reason codes = %v, want %v", entry.ReasonCodes, wantReasons)
	}

	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{key, policyPrefix, value, "audit-secret"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("audit leaked %q: %s", forbidden, encoded)
		}
	}
	if again := svc.Audit(1); !reflect.DeepEqual(got, again) {
		t.Fatalf("audit was not deterministic:\nfirst:  %+v\nsecond: %+v", got, again)
	}
}

func TestAuditRespectsPlainKeyVisibility(t *testing.T) {
	clock := testutil.NewFakeClock(time.Unix(1, 0))
	up := testutil.NewFakeUpstream()
	up.Put("plain:key", []byte("value"), 0)
	svc := newTestServiceWithConfig(up, clock, func(cfg *config.Config) {
		cfg.Mode = "observe"
		cfg.Privacy.KeyVisibility = "plain"
	})
	if _, err := svc.Get(context.Background(), "plain:key"); err != nil {
		t.Fatal(err)
	}

	report := svc.Audit(1)
	if report.KeyVisibility != "plain" || len(report.Entries) != 1 || report.Entries[0].ID != "plain:key" {
		t.Fatalf("plain audit = %+v", report)
	}
	if report.Entries[0].Recommendation != AuditRecommendationContinueObserving {
		t.Fatalf("recommendation = %q", report.Entries[0].Recommendation)
	}
}

func TestAuditDoesNotReportStaleRateAfterIdleWindows(t *testing.T) {
	clock := testutil.NewFakeClock(time.Unix(1, 0))
	up := testutil.NewFakeUpstream()
	up.Put("idle:key", []byte("value"), 0)
	svc := newTestServiceWithConfig(up, clock, func(cfg *config.Config) {
		cfg.Mode = "observe"
	})
	if _, err := svc.Get(context.Background(), "idle:key"); err != nil {
		t.Fatal(err)
	}

	clock.Advance(5 * time.Second)
	report := svc.Audit(1)
	if len(report.Entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(report.Entries))
	}
	if report.Entries[0].RequestRate != 0 {
		t.Fatalf("idle request rate = %v, want 0", report.Entries[0].RequestRate)
	}
}

func TestAuditMarksTelemetryIncompleteAfterTrackingEviction(t *testing.T) {
	clock := testutil.NewFakeClock(time.Unix(1, 0))
	svc := newTestServiceWithConfig(testutil.NewFakeUpstream(), clock, func(cfg *config.Config) {
		cfg.Mode = "observe"
		cfg.Hotness.MaxTrackedKeys = 1
	})
	for _, key := range []string{"first", "second"} {
		if _, err := svc.Get(context.Background(), key); err != nil {
			t.Fatal(err)
		}
	}

	report := svc.Audit(1)
	if report.TrackingEvictions != 1 || report.TelemetryComplete {
		t.Fatalf("audit completeness after eviction = %+v", report)
	}
	if report.TrackedKeys != 1 || report.ReturnedEntries != 1 || report.Truncated {
		t.Fatalf("audit bounds after eviction = %+v", report)
	}
}

func TestAuditMarksTelemetryIncompleteAfterCapacityDrop(t *testing.T) {
	clock := testutil.NewFakeClock(time.Unix(1, 0))
	svc := newTestServiceWithConfig(testutil.NewFakeUpstream(), clock, func(cfg *config.Config) {
		cfg.Hotness.MaxTrackedKeys = 1
	})
	svc.tracker.Observe("hot")
	svc.tracker.Admit("hot")
	if _, err := svc.Get(context.Background(), "unseen"); err != nil {
		t.Fatal(err)
	}

	report := svc.Audit(1)
	if report.CapacityObservationsDropped != 1 || report.TrackingEvictions != 0 || report.TelemetryComplete {
		t.Fatalf("audit completeness after capacity drop = %+v", report)
	}
	if report.TrackedKeys != 1 || len(report.Entries) != 1 {
		t.Fatalf("HOT victim was not retained: %+v", report)
	}
}

func TestAuditMarksTelemetryIncompleteAfterOversizedObservation(t *testing.T) {
	clock := testutil.NewFakeClock(time.Unix(1, 0))
	svc := newTestServiceWithConfig(testutil.NewFakeUpstream(), clock, func(cfg *config.Config) {
		cfg.Mode = "observe"
	})
	if _, err := svc.Get(context.Background(), strings.Repeat("k", hotness.MaxTrackedKeyBytes+1)); err != nil {
		t.Fatal(err)
	}

	report := svc.Audit(1)
	if report.OversizedObservationsDropped != 1 || report.TelemetryComplete {
		t.Fatalf("audit completeness after oversized observation = %+v", report)
	}
	if report.TrackedKeys != 0 || len(report.Entries) != 0 {
		t.Fatalf("oversized key entered audit: %+v", report)
	}
}

func TestNormalizeAuditLimit(t *testing.T) {
	tests := []struct {
		input int
		want  int
	}{
		{input: -1, want: DefaultAuditLimit},
		{input: 0, want: DefaultAuditLimit},
		{input: 1, want: 1},
		{input: MaxAuditLimit, want: MaxAuditLimit},
		{input: MaxAuditLimit + 1, want: MaxAuditLimit},
	}
	for _, tt := range tests {
		if got := normalizeAuditLimit(tt.input); got != tt.want {
			t.Errorf("normalizeAuditLimit(%d) = %d, want %d", tt.input, got, tt.want)
		}
	}
}

func TestAuditCacheRecommendationIncludesHotnessReason(t *testing.T) {
	for _, tt := range []struct {
		state      hotness.State
		wantReason string
	}{
		{state: hotness.StateHot, wantReason: AuditReasonHotnessStateHot},
		{state: hotness.StateWarm, wantReason: AuditReasonHotnessStateNotHot},
	} {
		recommendation, reasons := auditRecommendation("cache", tt.state)
		if recommendation != AuditRecommendationNoChange {
			t.Fatalf("recommendation = %q", recommendation)
		}
		want := []string{AuditReasonEffectivePolicyCache, tt.wantReason}
		if !reflect.DeepEqual(reasons, want) {
			t.Fatalf("reasons for %s = %v, want %v", tt.state, reasons, want)
		}
	}
}
