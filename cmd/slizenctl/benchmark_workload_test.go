package main

import (
	"encoding/json"
	"errors"
	"math"
	"strings"
	"testing"
	"time"
)

func TestValidateWorkloadConfig(t *testing.T) {
	valid := validWorkloadConfig()
	if err := validateWorkloadConfig(valid); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
	smallUniform := valid
	smallUniform.Scenario = workloadScenarioUniform
	smallUniform.KeyCount = 2
	if err := validateWorkloadConfig(smallUniform); err != nil {
		t.Fatalf("small uniform config rejected: %v", err)
	}

	tests := []struct {
		name   string
		change func(*workloadConfig)
	}{
		{name: "scenario", change: func(cfg *workloadConfig) { cfg.Scenario = "zipf" }},
		{name: "origin", change: func(cfg *workloadConfig) { cfg.OriginAddr = "" }},
		{name: "proxy", change: func(cfg *workloadConfig) { cfg.ProxyAddr = "" }},
		{name: "admin", change: func(cfg *workloadConfig) { cfg.AdminURL = "" }},
		{name: "empty prefix", change: func(cfg *workloadConfig) { cfg.KeyPrefix = "" }},
		{name: "long prefix", change: func(cfg *workloadConfig) { cfg.KeyPrefix = strings.Repeat("x", maxWorkloadKeyPrefix+1) }},
		{name: "too few keys", change: func(cfg *workloadConfig) { cfg.KeyCount = 1 }},
		{name: "too few keys for all", change: func(cfg *workloadConfig) { cfg.KeyCount = 99 }},
		{name: "too few keys for 80/20", change: func(cfg *workloadConfig) { cfg.Scenario = workloadScenarioSkew80; cfg.KeyCount = 4 }},
		{name: "too many keys", change: func(cfg *workloadConfig) { cfg.KeyCount = maxWorkloadKeys + 1 }},
		{name: "empty value", change: func(cfg *workloadConfig) { cfg.ValueSize = 0 }},
		{name: "value too small for version evidence", change: func(cfg *workloadConfig) { cfg.ValueSize = minWorkloadValueBytes - 1 }},
		{name: "large value", change: func(cfg *workloadConfig) { cfg.ValueSize = maxWorkloadValueBytes + 1 }},
		{name: "large aggregate dataset", change: func(cfg *workloadConfig) { cfg.KeyCount = maxWorkloadKeys; cfg.ValueSize = maxWorkloadValueBytes }},
		{name: "zero reads", change: func(cfg *workloadConfig) { cfg.ReadRatio = 0 }},
		{name: "read ratio over 100", change: func(cfg *workloadConfig) { cfg.ReadRatio = 101 }},
		{name: "zero concurrency", change: func(cfg *workloadConfig) { cfg.Concurrency = 0 }},
		{name: "large concurrency", change: func(cfg *workloadConfig) { cfg.Concurrency = maxWorkloadConcurrency + 1 }},
		{name: "concurrency exceeds requests", change: func(cfg *workloadConfig) { cfg.Concurrency = 11; cfg.MaxRequests = 10 }},
		{name: "zero requests", change: func(cfg *workloadConfig) { cfg.MaxRequests = 0 }},
		{name: "too many requests", change: func(cfg *workloadConfig) { cfg.MaxRequests = maxWorkloadRequests + 1 }},
		{name: "zero duration", change: func(cfg *workloadConfig) { cfg.Duration = 0 }},
		{name: "long duration", change: func(cfg *workloadConfig) { cfg.Duration = maxWorkloadDuration + time.Nanosecond }},
		{name: "zero flash interval", change: func(cfg *workloadConfig) { cfg.FlashEvery = 0 }},
		{name: "large flash interval", change: func(cfg *workloadConfig) { cfg.FlashEvery = maxWorkloadRequests + 1 }},
		{name: "flash interval never moves", change: func(cfg *workloadConfig) { cfg.FlashEvery = cfg.MaxRequests }},
		{name: "output", change: func(cfg *workloadConfig) { cfg.Output = "csv" }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := valid
			tt.change(&cfg)
			if err := validateWorkloadConfig(cfg); err == nil {
				t.Fatal("invalid config accepted")
			}
		})
	}
}

func TestWorkloadSelectionIsDeterministicAndBounded(t *testing.T) {
	cfg := validWorkloadConfig()
	cfg.KeyCount = 100
	cfg.Seed = 42

	for _, scenario := range workloadScenarios() {
		for operation := uint64(0); operation < 10000; operation++ {
			firstKey, firstRead := workloadRequestAt(scenario, cfg, operation)
			secondKey, secondRead := workloadRequestAt(scenario, cfg, operation)
			if firstKey != secondKey || firstRead != secondRead {
				t.Fatalf("%s operation %d was not deterministic", scenario, operation)
			}
			if firstKey < 0 || firstKey >= cfg.KeyCount {
				t.Fatalf("%s operation %d selected out-of-range key %d", scenario, operation, firstKey)
			}
		}
	}

	differentSeed := cfg
	differentSeed.Seed++
	different := false
	for operation := uint64(0); operation < 100; operation++ {
		firstKey, firstRead := workloadRequestAt(workloadScenarioUniform, cfg, operation)
		secondKey, secondRead := workloadRequestAt(workloadScenarioUniform, differentSeed, operation)
		if firstKey != secondKey || firstRead != secondRead {
			different = true
			break
		}
	}
	if !different {
		t.Fatal("different seeds produced the same sampled workload")
	}
}

func TestWorkloadScenarioShapes(t *testing.T) {
	cfg := validWorkloadConfig()
	cfg.KeyCount = 100
	cfg.ReadRatio = 95
	cfg.FlashEvery = 1000
	const operations = 100000

	readCount := 0
	hot80 := 0
	hot99 := 0
	uniformCounts := make([]int, cfg.KeyCount)
	for operation := uint64(0); operation < operations; operation++ {
		uniformKey, _ := workloadRequestAt(workloadScenarioUniform, cfg, operation)
		uniformCounts[uniformKey]++
		key80, read := workloadRequestAt(workloadScenarioSkew80, cfg, operation)
		if read {
			readCount++
		}
		if key80 < 20 {
			hot80++
		}
		key99, _ := workloadRequestAt(workloadScenarioSkew99, cfg, operation)
		if key99 == 0 {
			hot99++
		}
	}
	assertRatioNear(t, "reads", readCount, operations, 0.95, 0.01)
	assertRatioNear(t, "80/20 hot set", hot80, operations, 0.80, 0.01)
	assertRatioNear(t, "99/1 hot set", hot99, operations, 0.99, 0.005)
	for key, count := range uniformCounts {
		if count < 800 || count > 1200 {
			t.Fatalf("uniform key %d count = %d, want 800..1200", key, count)
		}
	}

	firstWindow := make([]int, cfg.KeyCount)
	secondWindow := make([]int, cfg.KeyCount)
	for operation := uint64(0); operation < uint64(cfg.FlashEvery*2); operation++ {
		key, _ := workloadRequestAt(workloadScenarioMovingFlash, cfg, operation)
		if operation < uint64(cfg.FlashEvery) {
			firstWindow[key]++
		} else {
			secondWindow[key]++
		}
	}
	firstFlash := mostFrequentIndex(firstWindow)
	secondFlash := mostFrequentIndex(secondWindow)
	if secondFlash != (firstFlash+1)%cfg.KeyCount {
		t.Fatalf("moving flash key did not advance: first=%d second=%d", firstFlash, secondFlash)
	}
	if firstWindow[firstFlash] < 980 || secondWindow[secondFlash] < 980 {
		t.Fatalf("moving flash key was not dominant: first=%d second=%d", firstWindow[firstFlash], secondWindow[secondFlash])
	}
}

func TestSummarizeWorkloadScenarioMath(t *testing.T) {
	origin := benchmarkPhase{
		Reads:              1000,
		UpstreamGETs:       1000,
		UpstreamGETsSource: originGETsSourceCommandstats,
		P50Milliseconds:    0.1,
	}
	slizen := benchmarkPhase{
		Reads:                    1000,
		UpstreamGETs:             200,
		UpstreamGETsSource:       originGETsSourceCommandstats,
		SlizenStatusUpstreamGETs: 200,
		CacheHits:                800,
		CacheMisses:              200,
		CacheHitRatio:            80,
		P50Milliseconds:          0.2,
		P95Milliseconds:          0.5,
		P99Milliseconds:          0.9,
	}

	result := summarizeWorkloadScenario(workloadScenarioSkew80, origin, slizen, true, true)
	if result.OriginGETReductionPercent != 80 {
		t.Fatalf("origin GET reduction = %v, want 80", result.OriginGETReductionPercent)
	}
	if result.CacheHitRatioPercent != 80 {
		t.Fatalf("cache hit ratio = %v, want 80", result.CacheHitRatioPercent)
	}
	if result.P50Milliseconds != 0.2 || result.P95Milliseconds != 0.5 || result.P99Milliseconds != 0.9 {
		t.Fatalf("summary percentiles do not match Slizen phase: %+v", result)
	}
	if !result.ProvedOriginGETReduction {
		t.Fatal("expected reduction to be proved")
	}
	if !result.EvidenceValid {
		t.Fatal("expected evidence to be valid")
	}

	increase := workloadOriginGETReduction(origin, benchmarkPhase{
		Reads:              1000,
		UpstreamGETs:       1100,
		UpstreamGETsSource: originGETsSourceCommandstats,
	})
	if math.Abs(increase-(-10)) > 0.000001 {
		t.Fatalf("origin GET increase = %v, want -10", increase)
	}
	if reduction := workloadOriginGETReduction(benchmarkPhase{}, slizen); reduction != 0 {
		t.Fatalf("zero-denominator reduction = %v, want 0", reduction)
	}

	failedOrigin := origin
	failedOrigin.Failures = 1
	failed := summarizeWorkloadScenario(workloadScenarioSkew80, failedOrigin, slizen, true, true)
	if failed.OriginGETReductionPercent != 0 || failed.CacheHitRatioPercent != 0 || failed.EvidenceValid || failed.ProvedOriginGETReduction {
		t.Fatalf("failed phase produced reduction evidence: %+v", failed)
	}
	if reduction := workloadOriginGETReduction(failedOrigin, slizen); reduction != 0 {
		t.Fatalf("failed-phase reduction = %v, want 0", reduction)
	}

	invalidStatus := summarizeWorkloadScenario(workloadScenarioSkew80, origin, slizen, false, true)
	if invalidStatus.OriginGETReductionPercent != 0 || invalidStatus.CacheHitRatioPercent != 0 || invalidStatus.EvidenceValid || invalidStatus.ProvedOriginGETReduction {
		t.Fatalf("invalid status delta produced reduction evidence: %+v", invalidStatus)
	}

	mismatched := slizen
	mismatched.ValueMismatches = 1
	mismatchResult := summarizeWorkloadScenario(workloadScenarioSkew80, origin, mismatched, true, true)
	if mismatchResult.EvidenceValid || mismatchResult.ProvedOriginGETReduction {
		t.Fatalf("value mismatch produced valid evidence: %+v", mismatchResult)
	}

	invalidPhysical := summarizeWorkloadScenario(workloadScenarioSkew80, origin, slizen, true, false)
	if invalidPhysical.OriginGETReductionPercent != 0 || invalidPhysical.EvidenceValid || invalidPhysical.ProvedOriginGETReduction {
		t.Fatalf("invalid physical origin counter produced evidence: %+v", invalidPhysical)
	}

	noPhysicalSource := slizen
	noPhysicalSource.UpstreamGETsSource = originGETsSourceUnavailable
	if reduction := workloadOriginGETReduction(origin, noPhysicalSource); reduction != 0 {
		t.Fatalf("non-physical source reduction = %v, want 0", reduction)
	}
}

func TestWorkloadValuesAreKeyAndVersionSpecificAndDetectCorruption(t *testing.T) {
	values := newWorkloadValues([]string{"key:0", "key:1"}, 97)
	first := values.Fill(0, 0, nil)
	second := values.Fill(1, 0, nil)
	updated := values.Fill(0, 1, nil)
	if len(first) != 97 || len(second) != 97 {
		t.Fatalf("value lengths = %d and %d, want 97", len(first), len(second))
	}
	if string(first) == string(second) {
		t.Fatal("different keys received identical benchmark values")
	}
	if string(first) == string(updated) {
		t.Fatal("different write versions received identical benchmark values")
	}
	if !values.Matches(0, 0, first) || !values.Matches(1, 0, second) || !values.Matches(0, 1, updated) {
		t.Fatal("generated benchmark value did not validate")
	}
	if values.Matches(0, 1, first) {
		t.Fatal("stale write generation validated as the current value")
	}
	corrupted := append([]byte(nil), first...)
	corrupted[len(corrupted)/2] ^= 0xff
	if values.Matches(0, 0, corrupted) {
		t.Fatal("corrupted benchmark value validated")
	}
	if values.Matches(0, 0, first[:len(first)-1]) {
		t.Fatal("truncated benchmark value validated")
	}
}

func TestHotKeyReductionRejectsValueMismatch(t *testing.T) {
	origin := benchmarkPhase{Requests: 100, UpstreamGETs: 100, UpstreamGETsSource: originGETsSourceCommandstats}
	hot := benchmarkPhase{Requests: 100, UpstreamGETs: 10, UpstreamGETsSource: originGETsSourceCommandstats, ValueMismatches: 1}
	if reduction := upstreamReduction(origin, hot); reduction != 0 {
		t.Fatalf("reduction with value mismatch = %v, want 0", reduction)
	}
	hot.ValueMismatches = 0
	hot.UpstreamGETsSource = originGETsSourceUnavailable
	if reduction := upstreamReduction(origin, hot); reduction != 0 {
		t.Fatalf("reduction without physical source = %v, want 0", reduction)
	}
}

func TestWorkloadBenchmarkJSONContainsReleaseEvidence(t *testing.T) {
	result := workloadBenchmarkResult{
		RuntimeVersions: runtimeVersions{Slizen: "0.2.0", Origin: "valkey 8.1.0", Go: "go1.26"},
		Scenarios: []workloadScenarioResult{{
			Name: workloadScenarioUniform,
			Slizen: benchmarkPhase{
				OperationAttempts:        100,
				TerminationReason:        "request_limit",
				ReadLatency:              &latencyDistribution{Samples: 95},
				WriteLatency:             &latencyDistribution{Samples: 5},
				ReadOrderingWaitLatency:  &latencyDistribution{Samples: 95},
				WriteOrderingWaitLatency: &latencyDistribution{Samples: 5},
				FinalValidationLatency:   &latencyDistribution{Samples: 4},
			},
			P50Milliseconds:           1,
			P95Milliseconds:           2,
			P99Milliseconds:           3,
			OriginGETReductionPercent: 40,
			CacheHitRatioPercent:      50,
		}},
	}
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{
		`"runtime_versions"`,
		`"isolated_key_prefix"`,
		`"p50_ms"`,
		`"p95_ms"`,
		`"p99_ms"`,
		`"origin_get_reduction_percent"`,
		`"cache_hit_ratio_percent"`,
		`"cache_misses_policy_bypass"`,
		`"cache_misses_not_admitted"`,
		`"cache_misses_not_present"`,
		`"upstream_gets_source"`,
		`"origin_run_id"`,
		`"slizen_status_upstream_gets"`,
		`"evidence_valid"`,
		`"value_mismatches"`,
		`"validation_reads"`,
		`"validation_mismatches"`,
		`"operation_attempts"`,
		`"termination_reason"`,
		`"read_latency"`,
		`"write_latency"`,
		`"read_ordering_wait_latency"`,
		`"write_ordering_wait_latency"`,
		`"final_validation_latency"`,
		`"samples"`,
	} {
		if !strings.Contains(string(data), field) {
			t.Fatalf("JSON missing %s: %s", field, data)
		}
	}
}

func TestHotKeyBenchmarkJSONOmitsWorkloadLatencyObjects(t *testing.T) {
	data, err := json.Marshal(benchmarkResult{
		Phases: []benchmarkPhase{{Name: "slizen hot", Requests: 10, P99Milliseconds: 1}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Phases []map[string]json.RawMessage `json:"phases"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Phases) != 1 {
		t.Fatalf("decoded phases = %d, want 1", len(decoded.Phases))
	}
	for _, field := range []string{
		"read_latency",
		"write_latency",
		"read_ordering_wait_latency",
		"write_ordering_wait_latency",
		"final_validation_latency",
	} {
		if _, exists := decoded.Phases[0][field]; exists {
			t.Fatalf("hot-key phase unexpectedly contains %q: %s", field, data)
		}
	}
}

func TestWorkloadLatencyDistributionsAndTerminationReason(t *testing.T) {
	values := []time.Duration{100 * time.Millisecond, 10 * time.Millisecond, 50 * time.Millisecond}
	distribution := latencyDistributionFor(values)
	if distribution.Samples != 3 || distribution.P50Milliseconds != 50 || distribution.P95Milliseconds != 100 || distribution.P99Milliseconds != 100 {
		t.Fatalf("latency distribution = %+v", distribution)
	}
	if got := workloadTerminationReason(100, 100); got != "request_limit" {
		t.Fatalf("request-bound reason = %q", got)
	}
	if got := workloadTerminationReason(99, 100); got != "duration_limit" {
		t.Fatalf("duration-bound reason = %q", got)
	}
	if got := latencyDistributionPointer(nil); got != nil {
		t.Fatalf("empty latency distribution = %+v, want nil", got)
	}
}

func TestPercentileMillisUsesNearestRank(t *testing.T) {
	values := make([]time.Duration, 100)
	for i := range values {
		values[i] = time.Duration(i+1) * time.Millisecond
	}
	if got := percentileMillis(values, 50); got != 50 {
		t.Fatalf("p50 = %v, want 50", got)
	}
	if got := percentileMillis(values, 95); got != 95 {
		t.Fatalf("p95 = %v, want 95", got)
	}
	if got := percentileMillis(values, 99); got != 99 {
		t.Fatalf("p99 = %v, want 99", got)
	}
}

func TestParseOriginVersion(t *testing.T) {
	valkeyInfo := "# Server\r\nredis_version:7.2.4\r\nserver_name:valkey\r\nvalkey_version:8.1.0\r\n"
	if got := parseOriginVersion(valkeyInfo); got != "valkey 8.1.0" {
		t.Fatalf("Valkey version = %q", got)
	}
	if got := parseOriginVersion("server_name:redis\r\nredis_version:7.4.0\r\nvalkey_version:9.9.9\r\n"); got != "redis 7.4.0" {
		t.Fatalf("Redis version = %q", got)
	}
	if got := parseOriginVersion("redis_version:7.2.0\r\n"); got != "Redis-compatible 7.2.0" {
		t.Fatalf("compatible Redis version = %q", got)
	}
	if got := parseOriginVersion("valkey_version:8.0.2\r\n"); got != "Valkey 8.0.2" {
		t.Fatalf("unnamed Valkey version = %q", got)
	}
}

func TestWorkloadIsolatedKeyPrefixSeparatesRuns(t *testing.T) {
	first := workloadIsolatedKeyPrefix("slizen:test", time.Unix(100, 1))
	second := workloadIsolatedKeyPrefix("slizen:test", time.Unix(100, 2))
	if first == second {
		t.Fatalf("separate runs reused key prefix %q", first)
	}
	if !strings.HasPrefix(first, "slizen:test:run-") || !strings.HasPrefix(second, "slizen:test:run-") {
		t.Fatalf("isolated prefixes do not preserve requested namespace: %q %q", first, second)
	}
}

func TestApplyWorkloadStatusDeltaRequiresIsolatedMonotonicCounters(t *testing.T) {
	before := statusSnapshot{
		Version:                 "0.2.0",
		Commit:                  "abc123",
		Mode:                    "cache",
		KeyVisibility:           "hmac",
		Uptime:                  "10s",
		RequestsTotal:           100,
		CacheHitsTotal:          20,
		CacheMissesTotal:        30,
		CacheMissesPolicyBypass: 4,
		CacheMissesNotAdmitted:  16,
		CacheMissesNotPresent:   10,
		UpstreamRequestsTotal:   60,
		UpstreamGETsTotal:       40,
		InvalidationsTotal:      2,
		PromotionsTotal:         3,
		DemotionsTotal:          1,
	}
	validAfter := before
	validAfter.Uptime = "11s"
	validAfter.RequestsTotal += 100
	validAfter.CacheHitsTotal += 60
	validAfter.CacheMissesTotal += 20
	validAfter.CacheMissesPolicyBypass += 2
	validAfter.CacheMissesNotAdmitted += 11
	validAfter.CacheMissesNotPresent += 7
	validAfter.UpstreamRequestsTotal += 40
	validAfter.UpstreamGETsTotal += 20

	phase := benchmarkPhase{Requests: 100, Reads: 80, Writes: 20}
	if valid := applyWorkloadStatusDelta(&phase, before, validAfter); !valid {
		t.Fatalf("isolated status delta rejected: %+v", phase)
	}
	if phase.CacheHits != 60 || phase.CacheMisses != 20 || phase.SlizenStatusUpstreamGETs != 20 || phase.UpstreamGETs != 0 || phase.CacheHitRatio != 75 {
		t.Fatalf("status delta = %+v", phase)
	}
	if phase.CacheMissesPolicyBypass != 2 || phase.CacheMissesNotAdmitted != 11 || phase.CacheMissesNotPresent != 7 {
		t.Fatalf("cache miss reason delta = %+v", phase)
	}

	tests := []struct {
		name   string
		change func(*statusSnapshot)
	}{
		{
			name: "concurrent proxy traffic",
			change: func(after *statusSnapshot) {
				after.RequestsTotal++
			},
		},
		{
			name: "counter decrease",
			change: func(after *statusSnapshot) {
				after.UpstreamGETsTotal = before.UpstreamGETsTotal - 1
			},
		},
		{
			name: "cache miss reason counter decrease",
			change: func(after *statusSnapshot) {
				after.CacheMissesNotAdmitted = before.CacheMissesNotAdmitted - 1
			},
		},
		{
			name: "daemon restart",
			change: func(after *statusSnapshot) {
				after.Uptime = "1s"
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			after := validAfter
			tt.change(&after)
			got := benchmarkPhase{Requests: 100, Reads: 80, Writes: 20}
			if valid := applyWorkloadStatusDelta(&got, before, after); valid {
				t.Fatalf("invalid status evidence accepted: %+v", got)
			}
			if len(got.Notes) == 0 {
				t.Fatalf("invalid status evidence did not produce a warning: %+v", got)
			}
		})
	}
}

func TestApplyHotKeyStatusDeltaKeepsLogicalOriginGETsSeparate(t *testing.T) {
	before := statusSnapshot{
		Version:               "0.2.3",
		Commit:                "abc123",
		Mode:                  "cache",
		KeyVisibility:         "hash",
		Uptime:                "10s",
		RequestsTotal:         100,
		CacheHitsTotal:        80,
		CacheMissesTotal:      20,
		UpstreamRequestsTotal: 20,
		UpstreamGETsTotal:     20,
	}
	after := before
	after.Uptime = "11s"
	after.RequestsTotal += 100
	after.CacheHitsTotal += 90
	after.CacheMissesTotal += 10
	after.UpstreamRequestsTotal += 10
	after.UpstreamGETsTotal += 10

	phase := benchmarkPhase{Requests: 100}
	if valid := applyHotKeyStatusDelta(&phase, before, after); !valid {
		t.Fatalf("isolated hot-key status rejected: %+v", phase)
	}
	if phase.SlizenStatusUpstreamGETs != 10 || phase.UpstreamGETs != 0 || phase.CacheHitRatio != 90 {
		t.Fatalf("hot-key status delta = %+v", phase)
	}

	after.RequestsTotal++
	invalid := benchmarkPhase{Requests: 100}
	if valid := applyHotKeyStatusDelta(&invalid, before, after); valid {
		t.Fatalf("concurrent hot-key status traffic accepted: %+v", invalid)
	}
}

func TestParseOriginGETCallsFromCommandstats(t *testing.T) {
	tests := []struct {
		name    string
		info    string
		want    uint64
		wantErr string
	}{
		{
			name: "redis",
			info: "# Commandstats\r\ncmdstat_get:calls=123,usec=456,usec_per_call=3.71,rejected_calls=0,failed_calls=0\r\n",
			want: 123,
		},
		{
			name: "valkey upper section",
			info: "# Commandstats\ncmdstat_set:calls=4,usec=8\ncmdstat_get:calls=0,usec=0\n",
			want: 0,
		},
		{
			name: "no GETs yet",
			info: "# Commandstats\r\ncmdstat_set:calls=1,usec=1\r\n",
			want: 0,
		},
		{
			name:    "wrong section",
			info:    "# Server\r\nredis_version:7.2.0\r\n",
			wantErr: "Commandstats section",
		},
		{
			name:    "malformed calls",
			info:    "# Commandstats\r\ncmdstat_get:calls=not-a-number,usec=1\r\n",
			wantErr: "cmdstat_get calls",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseOriginGETCalls(tt.info)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %v, want containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Fatalf("calls = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestParseOriginRunID(t *testing.T) {
	if got, err := parseOriginRunID("# Server\r\nrun_id:0123456789abcdef\r\nredis_version:7.2.0\r\n"); err != nil || got != "0123456789abcdef" {
		t.Fatalf("run_id = %q, err = %v", got, err)
	}
	if got, err := parseOriginRunID("# Server\r\nredis_version:7.2.0\r\n"); err == nil || got != "" {
		t.Fatalf("missing run_id = %q, err = %v", got, err)
	}
}

func TestApplyOriginGETCounterDeltaFailsClosed(t *testing.T) {
	valid := benchmarkPhase{}
	if ok := applyOriginGETCounterDelta(
		&valid,
		originGETCounterSnapshot{Calls: 100, RunID: "run-a"},
		originGETCounterSnapshot{Calls: 140, RunID: "run-a"},
	); !ok {
		t.Fatalf("valid commandstats delta rejected: %+v", valid)
	}
	if valid.UpstreamGETs != 40 || valid.UpstreamGETsSource != originGETsSourceCommandstats {
		t.Fatalf("physical delta = %+v", valid)
	}

	unavailable := benchmarkPhase{}
	if ok := applyOriginGETCounterDelta(
		&unavailable,
		originGETCounterSnapshot{Err: errors.New("NOPERM")},
		originGETCounterSnapshot{Calls: 140, RunID: "run-a"},
	); ok {
		t.Fatalf("unavailable commandstats accepted: %+v", unavailable)
	}
	if unavailable.UpstreamGETs != 0 || unavailable.UpstreamGETsSource != originGETsSourceUnavailable || len(unavailable.Notes) == 0 {
		t.Fatalf("unavailable commandstats did not fail closed: %+v", unavailable)
	}

	nonMonotonic := benchmarkPhase{}
	if ok := applyOriginGETCounterDelta(
		&nonMonotonic,
		originGETCounterSnapshot{Calls: 140, RunID: "run-a"},
		originGETCounterSnapshot{Calls: 100, RunID: "run-a"},
	); ok {
		t.Fatalf("non-monotonic commandstats accepted: %+v", nonMonotonic)
	}
	if nonMonotonic.UpstreamGETs != 0 || nonMonotonic.UpstreamGETsSource != originGETsSourceCommandstats || len(nonMonotonic.Notes) == 0 {
		t.Fatalf("non-monotonic commandstats did not fail closed: %+v", nonMonotonic)
	}

	restarted := benchmarkPhase{}
	restartedValid := applyOriginGETCounterDelta(
		&restarted,
		originGETCounterSnapshot{Calls: 100, RunID: "run-a"},
		originGETCounterSnapshot{Calls: 140, RunID: "run-b"},
	)
	if restartedValid {
		t.Fatalf("changed origin run_id was accepted: %+v", restarted)
	}
	if restarted.UpstreamGETs != 0 || restarted.UpstreamGETsSource != originGETsSourceCommandstats || len(restarted.Notes) == 0 {
		t.Fatalf("changed origin run_id did not fail closed: %+v", restarted)
	}
	restartedResult := summarizeWorkloadScenario(workloadScenarioUniform, benchmarkPhase{}, restarted, true, restartedValid)
	if restartedResult.EvidenceValid || restartedResult.ProvedOriginGETReduction {
		t.Fatalf("changed origin run_id produced scenario evidence: %+v", restartedResult)
	}

	missingRunID := benchmarkPhase{}
	missingRunIDValid := applyOriginGETCounterDelta(
		&missingRunID,
		originGETCounterSnapshot{Calls: 100},
		originGETCounterSnapshot{Calls: 140},
	)
	if missingRunIDValid {
		t.Fatalf("missing origin run_id was accepted: %+v", missingRunID)
	}
	if missingRunID.UpstreamGETs != 0 || missingRunID.UpstreamGETsSource != originGETsSourceCommandstats || len(missingRunID.Notes) == 0 {
		t.Fatalf("missing origin run_id did not fail closed: %+v", missingRunID)
	}
	missingRunIDResult := summarizeWorkloadScenario(workloadScenarioUniform, benchmarkPhase{}, missingRunID, true, missingRunIDValid)
	if missingRunIDResult.EvidenceValid || missingRunIDResult.ProvedOriginGETReduction {
		t.Fatalf("missing origin run_id produced scenario evidence: %+v", missingRunIDResult)
	}
}

func TestPhysicalOriginGETIsolationMatchesDirectAndSlizenEvidence(t *testing.T) {
	direct := benchmarkPhase{Reads: 100, UpstreamGETs: 100, UpstreamGETsSource: originGETsSourceCommandstats}
	if !validatePhysicalOriginGETIsolation(&direct, direct.Reads, "direct phase reads") {
		t.Fatalf("isolated direct evidence rejected: %+v", direct)
	}

	slizen := benchmarkPhase{
		UpstreamGETs:             10,
		UpstreamGETsSource:       originGETsSourceCommandstats,
		SlizenStatusUpstreamGETs: 10,
	}
	if !validatePhysicalOriginGETIsolation(&slizen, slizen.SlizenStatusUpstreamGETs, "Slizen status upstream GET delta") {
		t.Fatalf("isolated Slizen evidence rejected: %+v", slizen)
	}

	slizen.UpstreamGETs++
	if validatePhysicalOriginGETIsolation(&slizen, slizen.SlizenStatusUpstreamGETs, "Slizen status upstream GET delta") {
		t.Fatalf("unrelated origin traffic was accepted: %+v", slizen)
	}
	if len(slizen.Notes) == 0 {
		t.Fatalf("isolation mismatch did not produce a note: %+v", slizen)
	}
}

func TestSameOriginRunIDIsRequiredAcrossComparisonPhases(t *testing.T) {
	origin := benchmarkPhase{OriginRunID: "run-a"}
	cold := benchmarkPhase{OriginRunID: "run-a"}
	hot := benchmarkPhase{OriginRunID: "run-a"}
	if !validateSameOriginRunID(&origin, &cold, &hot) {
		t.Fatalf("same origin run_id was rejected: origin=%+v cold=%+v hot=%+v", origin, cold, hot)
	}

	hot.OriginRunID = "run-b"
	continuityValid := validateSameOriginRunID(&origin, &cold, &hot)
	if continuityValid {
		t.Fatalf("restart between phases was accepted: origin=%+v cold=%+v hot=%+v", origin, cold, hot)
	}
	if len(origin.Notes) == 0 || len(cold.Notes) == 0 || len(hot.Notes) == 0 {
		t.Fatalf("cross-phase restart did not annotate all phases: origin=%+v cold=%+v hot=%+v", origin, cold, hot)
	}
	result := summarizeWorkloadScenario(workloadScenarioUniform, origin, hot, true, continuityValid)
	if result.EvidenceValid || result.ProvedOriginGETReduction {
		t.Fatalf("cross-phase restart produced evidence: %+v", result)
	}

	missing := benchmarkPhase{}
	if validateSameOriginRunID(&origin, &missing) {
		t.Fatalf("missing cross-phase run_id was accepted: origin=%+v missing=%+v", origin, missing)
	}
}

func TestKnownOriginRuntimeVersionFailsClosed(t *testing.T) {
	for _, version := range []string{"", " ", "unknown", "UNKNOWN"} {
		if knownOriginRuntimeVersion(version) {
			t.Fatalf("unknown origin runtime version %q was accepted", version)
		}
	}
	for _, version := range []string{"Valkey 8.1.9", "Redis 7.4.2", "Redis-compatible 1.0"} {
		if !knownOriginRuntimeVersion(version) {
			t.Fatalf("known origin runtime version %q was rejected", version)
		}
	}
}

func TestDecodeStatusSnapshotBoundsResponseBody(t *testing.T) {
	_, err := decodeStatusSnapshot(strings.NewReader(strings.Repeat("x", maxStatusResponseBytes+1)))
	if err == nil || !strings.Contains(err.Error(), "response exceeds") {
		t.Fatalf("oversized status response error = %v", err)
	}
}

func TestNewWorkloadClientsBoundsCountBeforeDial(t *testing.T) {
	for _, count := range []int{0, maxWorkloadConcurrency + 1} {
		if _, err := newWorkloadClients("invalid.test:6379", count); err == nil {
			t.Fatalf("client count %d was accepted", count)
		}
	}
}

func validWorkloadConfig() workloadConfig {
	return workloadConfig{
		OriginAddr:  "127.0.0.1:6379",
		ProxyAddr:   "127.0.0.1:6380",
		AdminURL:    "http://127.0.0.1:9090",
		Scenario:    workloadScenarioAll,
		KeyPrefix:   "slizen:test",
		KeyCount:    1000,
		ValueSize:   1024,
		ReadRatio:   95,
		Concurrency: 32,
		MaxRequests: 100000,
		Duration:    10 * time.Second,
		Seed:        1,
		FlashEvery:  5000,
		Output:      "json",
	}
}

func assertRatioNear(t *testing.T, name string, count, total int, want, tolerance float64) {
	t.Helper()
	got := float64(count) / float64(total)
	if math.Abs(got-want) > tolerance {
		t.Fatalf("%s ratio = %.4f, want %.4f ± %.4f", name, got, want, tolerance)
	}
}

func mostFrequentIndex(counts []int) int {
	best := 0
	for i := 1; i < len(counts); i++ {
		if counts[i] > counts[best] {
			best = i
		}
	}
	return best
}
