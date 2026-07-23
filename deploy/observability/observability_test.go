package observability

import (
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"
)

type dashboard struct {
	Title  string `json:"title"`
	UID    string `json:"uid"`
	Panels []struct {
		Title   string `json:"title"`
		Targets []struct {
			Expr string `json:"expr"`
		} `json:"targets"`
	} `json:"panels"`
}

func TestGrafanaDashboardIsImportableAndCoversStagingSignals(t *testing.T) {
	data, err := os.ReadFile("grafana-dashboard.json")
	if err != nil {
		t.Fatal(err)
	}

	var got dashboard
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("dashboard is not valid JSON: %v", err)
	}
	if got.Title == "" || got.UID == "" {
		t.Fatal("dashboard must have a stable title and UID")
	}

	requiredPanels := []string{
		"Proxy requests by result",
		"GET latency p95 / p99",
		"Upstream latency p95 / p99",
		"Upstream requests and errors",
		"Cache hits and misses",
		"Cache miss reasons",
		"GET upstream-call ratio / avoidance",
		"Cache bytes",
		"Cache entries",
		"Cache eviction rate",
		"Promotions and demotions",
		"Request coalescing",
		"Hotness telemetry drops",
		"Process CPU",
		"Process resident memory",
		"Go goroutines",
		"Go allocation rate",
		"Active downstream connections",
	}
	titles := make(map[string]bool, len(got.Panels))
	for _, panel := range got.Panels {
		titles[panel.Title] = true
		for _, target := range panel.Targets {
			if strings.Contains(target.Expr, "histogram_quantile") &&
				strings.Contains(target.Expr, " - histogram_quantile") {
				t.Fatalf("panel %q subtracts histogram quantiles", panel.Title)
			}
		}
	}
	for _, title := range requiredPanels {
		if !titles[title] {
			t.Errorf("dashboard missing panel %q", title)
		}
	}
}

func TestPrometheusRulesUseBoundedMetricsAndDocumentTuning(t *testing.T) {
	data, err := os.ReadFile("prometheus-rules.yaml")
	if err != nil {
		t.Fatal(err)
	}
	rules := string(data)

	requiredAlerts := []string{
		"SlizenMetricsMissing",
		"SlizenProxyErrorRateHigh",
		"SlizenUpstreamErrorRateHigh",
		"SlizenGetP99LatencyHigh",
		"SlizenUpstreamP99LatencyHigh",
		"SlizenCacheBytePressure",
		"SlizenCacheEntryPressure",
		"SlizenHotnessOversizedTelemetryDropped",
		"SlizenHotnessCapacityTelemetryDropped",
	}
	for _, alert := range requiredAlerts {
		if !strings.Contains(rules, "- alert: "+alert) {
			t.Errorf("rules missing alert %q", alert)
		}
	}
	for _, metric := range []string{
		"slizen_requests_total",
		"slizen_upstream_errors_total",
		"slizen_request_duration_seconds_bucket",
		"slizen_upstream_duration_seconds_bucket",
		"slizen_cache_max_bytes",
		"slizen_cache_max_entries",
		"slizen_hotness_capacity_observations_dropped_total",
		"slizen_hotness_oversized_observations_dropped_total",
	} {
		if !strings.Contains(rules, metric) {
			t.Errorf("rules missing metric %q", metric)
		}
	}
	if !strings.Contains(strings.ToLower(rules), "tune") {
		t.Error("rules must explicitly require threshold tuning")
	}
	if !strings.Contains(rules, "absent_over_time(") ||
		!strings.Contains(rules, `slizen_requests_total{command="GET",result="ok"}[5m]`) {
		t.Error("rules must detect global or single-canary loss with the pre-initialized GET/ok series")
	}
	if strings.Contains(rules, "up == 0") {
		t.Error("rules must not use an unscoped generic up alert")
	}
	if strings.Contains(rules, "slizen_hotness_capacity_observations_dropped_total[15m])\n          +") {
		t.Error("capacity and oversized telemetry alerts must stay separate so a missing candidate-only series cannot suppress v0.2.2 alerts")
	}
	if strings.Contains(rules, "SlizenOriginGetAmplification") ||
		strings.Contains(strings.ToLower(rules), "physical upstream") {
		t.Error("Slizen-side logical call counters must not be presented as a physical origin-amplification alert")
	}

	quantileSubtraction := regexp.MustCompile(`histogram_quantile(?s:.*?)\s-\s*histogram_quantile`)
	if quantileSubtraction.MatchString(rules) {
		t.Error("rules must not estimate proxy tax by subtracting histogram quantiles")
	}
}

func TestDashboardIncludesCandidateRuntimeSignals(t *testing.T) {
	data, err := os.ReadFile("grafana-dashboard.json")
	if err != nil {
		t.Fatal(err)
	}
	dashboardJSON := string(data)
	for _, metric := range []string{
		"process_cpu_seconds_total",
		"process_resident_memory_bytes",
		"go_goroutines",
		"go_memstats_alloc_bytes_total",
		"slizen_active_connections",
	} {
		if !strings.Contains(dashboardJSON, metric) {
			t.Errorf("dashboard missing candidate process metric %q", metric)
		}
	}
}
