package metrics

import (
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestGetFastPathRecordsExistingMetricSeries(t *testing.T) {
	recorder := New()
	recorder.CacheHit("get")
	recorder.ObserveRequest("get", "ok", time.Millisecond)

	response := httptest.NewRecorder()
	recorder.Handler().ServeHTTP(response, httptest.NewRequest("GET", "/metrics", nil))
	if response.Code != 200 {
		t.Fatalf("metrics status = %d, want 200", response.Code)
	}
	body := response.Body.String()
	for _, want := range []string{
		`slizen_cache_hits_total{command="GET"} 1`,
		`slizen_requests_total{command="GET",result="ok"} 1`,
		`slizen_request_duration_seconds_count{command="GET",result="ok"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("metrics missing %q:\n%s", want, body)
		}
	}
}

func TestCommandLabelBoundsUserInput(t *testing.T) {
	tests := map[string]string{
		"GET":       "GET",
		"get":       "GET",
		"MULTI":     "unsafe",
		"BLPOP":     "unsafe",
		"RANDOM123": "unsupported",
		"":          "invalid",
		"UNKNOWN":   "invalid",
	}

	for command, want := range tests {
		if got := commandLabel(command); got != want {
			t.Fatalf("commandLabel(%q) = %q, want %q", command, got, want)
		}
	}
}

func TestCacheMissWithReasonRecordsFixedReasonSeries(t *testing.T) {
	recorder := New()
	recorder.CacheMissWithReason("get", CacheMissReasonPolicyBypass)
	recorder.CacheMissWithReason("get", CacheMissReasonNotAdmitted)
	recorder.CacheMissWithReason("get", CacheMissReasonNotPresent)
	recorder.CacheMissWithReason("get", CacheMissReason(255))

	snapshot := recorder.Snapshot()
	if snapshot.CacheMisses != 4 {
		t.Fatalf("cache misses = %d, want 4", snapshot.CacheMisses)
	}
	if snapshot.CacheMissesUnclassified != 1 ||
		snapshot.CacheMissesPolicyBypass != 1 ||
		snapshot.CacheMissesNotAdmitted != 1 ||
		snapshot.CacheMissesNotPresent != 1 {
		t.Fatalf("cache miss reason totals = %+v, want one of each", snapshot)
	}

	response := httptest.NewRecorder()
	recorder.Handler().ServeHTTP(response, httptest.NewRequest("GET", "/metrics", nil))
	if response.Code != 200 {
		t.Fatalf("metrics status = %d, want 200", response.Code)
	}
	body := response.Body.String()
	for _, want := range []string{
		`slizen_cache_misses_total{command="GET"} 4`,
		`slizen_cache_miss_reasons_total{reason="not_admitted"} 1`,
		`slizen_cache_miss_reasons_total{reason="not_present"} 1`,
		`slizen_cache_miss_reasons_total{reason="policy_bypass"} 1`,
		`slizen_cache_miss_reasons_total{reason="unclassified"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("metrics missing %q:\n%s", want, body)
		}
	}
	if got := strings.Count(body, "slizen_cache_miss_reasons_total{"); got != int(cacheMissReasonCount) {
		t.Fatalf("cache miss reason series = %d, want %d:\n%s", got, cacheMissReasonCount, body)
	}
	if strings.Contains(body, `reason="255"`) {
		t.Fatalf("invalid enum value escaped into a metric label:\n%s", body)
	}
}

func TestCacheMissUsesUnclassifiedReason(t *testing.T) {
	recorder := New()
	recorder.CacheMiss("MGET")

	snapshot := recorder.Snapshot()
	if snapshot.CacheMisses != 1 || snapshot.CacheMissesUnclassified != 1 {
		t.Fatalf("snapshot = %+v, want one unclassified cache miss", snapshot)
	}
}

func TestHotnessCapacityDropsUseMonotonicHighWater(t *testing.T) {
	recorder := New()
	recorder.ObserveHotnessCapacityDrops(2)
	recorder.ObserveHotnessCapacityDrops(1)
	recorder.ObserveHotnessCapacityDrops(3)

	response := httptest.NewRecorder()
	recorder.Handler().ServeHTTP(response, httptest.NewRequest("GET", "/metrics", nil))
	if !strings.Contains(response.Body.String(), "slizen_hotness_capacity_observations_dropped_total 3") {
		t.Fatalf("capacity-drop metric did not preserve monotonic total:\n%s", response.Body.String())
	}
}

func TestAdvanceHighWaterNeverRegressesOrDoubleCounts(t *testing.T) {
	var mark atomic.Uint64
	if delta := advanceHighWater(&mark, 2); delta != 2 {
		t.Fatalf("first delta = %d, want 2", delta)
	}
	if delta := advanceHighWater(&mark, 1); delta != 0 {
		t.Fatalf("out-of-order delta = %d, want 0", delta)
	}
	if delta := advanceHighWater(&mark, 3); delta != 1 {
		t.Fatalf("next delta = %d, want 1", delta)
	}

	mark.Store(0)
	var sum atomic.Uint64
	var wg sync.WaitGroup
	for total := uint64(1); total <= 1000; total++ {
		wg.Add(1)
		go func(value uint64) {
			defer wg.Done()
			sum.Add(advanceHighWater(&mark, value))
		}(total)
	}
	wg.Wait()
	if got := mark.Load(); got != 1000 {
		t.Fatalf("high-water mark = %d, want 1000", got)
	}
	if got := sum.Load(); got != 1000 {
		t.Fatalf("accepted deltas sum = %d, want 1000", got)
	}
}
