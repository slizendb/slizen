package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/slizendb/slizen/internal/buildinfo"
)

const (
	workloadScenarioAll         = "all"
	workloadScenarioUniform     = "uniform"
	workloadScenarioSkew80      = "skew-80-20"
	workloadScenarioSkew99      = "skew-99-1"
	workloadScenarioMovingFlash = "moving-flash"

	maxWorkloadConcurrency = 1024
	maxWorkloadRequests    = 1_000_000
	maxWorkloadKeys        = 100_000
	minWorkloadValueBytes  = 16
	maxWorkloadValueBytes  = 1 << 20
	maxWorkloadDataset     = 256 << 20
	maxWorkloadDuration    = time.Hour
	maxWorkloadKeyPrefix   = 128
	seedPipelineBytes      = 4 << 20
	maxStatusResponseBytes = 64 << 10

	originGETsSourceCommandstats = "origin_info_commandstats"
	originGETsSourceUnavailable  = "unavailable"
)

type benchmarkResult struct {
	Name                 string           `json:"name"`
	Key                  string           `json:"key"`
	Concurrency          int              `json:"concurrency"`
	DurationSeconds      float64          `json:"duration_seconds"`
	WarmupSeconds        float64          `json:"warmup_seconds"`
	MaxRequests          int              `json:"max_requests"`
	Mode                 string           `json:"mode"`
	KeyVisibility        string           `json:"key_visibility"`
	StartedAt            string           `json:"started_at"`
	FinishedAt           string           `json:"finished_at"`
	Phases               []benchmarkPhase `json:"phases"`
	UpstreamGetReduction float64          `json:"upstream_get_reduction_percent"`
	CacheHitRatio        float64          `json:"cache_hit_ratio_percent"`
	EvidenceValid        bool             `json:"evidence_valid"`
	ProvedReduction      bool             `json:"proved_reduction"`
	Notes                []string         `json:"notes,omitempty"`
	StatusBefore         statusSnapshot   `json:"status_before"`
	StatusAfter          statusSnapshot   `json:"status_after"`
	RuntimeVersions      runtimeVersions  `json:"runtime_versions"`
}

type benchmarkPhase struct {
	Name                     string               `json:"name"`
	Address                  string               `json:"address"`
	Requests                 uint64               `json:"requests"`
	OperationAttempts        uint64               `json:"operation_attempts,omitempty"`
	TerminationReason        string               `json:"termination_reason,omitempty"`
	Reads                    uint64               `json:"reads,omitempty"`
	Writes                   uint64               `json:"writes,omitempty"`
	Failures                 uint64               `json:"failures"`
	ValueMismatches          uint64               `json:"value_mismatches"`
	ValidationReads          uint64               `json:"validation_reads"`
	ValidationFailures       uint64               `json:"validation_failures"`
	ValidationMismatches     uint64               `json:"validation_mismatches"`
	ElapsedSeconds           float64              `json:"elapsed_seconds"`
	OpsPerSecond             float64              `json:"ops_per_second"`
	P50Milliseconds          float64              `json:"p50_ms"`
	P95Milliseconds          float64              `json:"p95_ms"`
	P99Milliseconds          float64              `json:"p99_ms"`
	ReadLatency              *latencyDistribution `json:"read_latency,omitempty"`
	WriteLatency             *latencyDistribution `json:"write_latency,omitempty"`
	ReadOrderingWaitLatency  *latencyDistribution `json:"read_ordering_wait_latency,omitempty"`
	WriteOrderingWaitLatency *latencyDistribution `json:"write_ordering_wait_latency,omitempty"`
	FinalValidationLatency   *latencyDistribution `json:"final_validation_latency,omitempty"`
	UpstreamGETs             uint64               `json:"upstream_gets"`
	UpstreamGETsSource       string               `json:"upstream_gets_source"`
	OriginRunID              string               `json:"origin_run_id"`
	SlizenStatusUpstreamGETs uint64               `json:"slizen_status_upstream_gets"`
	CacheHits                uint64               `json:"cache_hits"`
	CacheMisses              uint64               `json:"cache_misses"`
	CacheMissesPolicyBypass  uint64               `json:"cache_misses_policy_bypass"`
	CacheMissesNotAdmitted   uint64               `json:"cache_misses_not_admitted"`
	CacheMissesNotPresent    uint64               `json:"cache_misses_not_present"`
	CacheHitRatio            float64              `json:"cache_hit_ratio_percent"`
	Notes                    []string             `json:"notes,omitempty"`
}

type latencyDistribution struct {
	Samples         uint64  `json:"samples"`
	P50Milliseconds float64 `json:"p50_ms"`
	P95Milliseconds float64 `json:"p95_ms"`
	P99Milliseconds float64 `json:"p99_ms"`
}

type runtimeVersions struct {
	Slizenctl   string `json:"slizenctl"`
	Slizen      string `json:"slizen"`
	Commit      string `json:"slizen_commit,omitempty"`
	Origin      string `json:"origin"`
	Go          string `json:"go"`
	OperatingOS string `json:"os"`
	Arch        string `json:"arch"`
}

type workloadConfig struct {
	OriginAddr  string
	ProxyAddr   string
	AdminURL    string
	Scenario    string
	KeyPrefix   string
	KeyCount    int
	ValueSize   int
	ReadRatio   int
	Concurrency int
	MaxRequests int
	Duration    time.Duration
	Seed        int64
	FlashEvery  int
	Output      string
	JSONFile    string
}

type workloadBenchmarkResult struct {
	Name              string                   `json:"name"`
	ScenarioSelection string                   `json:"scenario_selection"`
	Seed              int64                    `json:"seed"`
	KeyPrefix         string                   `json:"key_prefix"`
	IsolatedKeyPrefix string                   `json:"isolated_key_prefix"`
	KeyCount          int                      `json:"key_count"`
	ValueSizeBytes    int                      `json:"value_size_bytes"`
	ReadRatioPercent  int                      `json:"read_ratio_percent"`
	WriteRatioPercent int                      `json:"write_ratio_percent"`
	Concurrency       int                      `json:"concurrency"`
	DurationSeconds   float64                  `json:"duration_seconds_per_phase"`
	MaxRequests       int                      `json:"max_requests_per_phase"`
	FlashEvery        int                      `json:"flash_key_moves_every_operations"`
	Mode              string                   `json:"mode"`
	StartedAt         string                   `json:"started_at"`
	FinishedAt        string                   `json:"finished_at"`
	RuntimeVersions   runtimeVersions          `json:"runtime_versions"`
	Scenarios         []workloadScenarioResult `json:"scenarios"`
	Notes             []string                 `json:"notes,omitempty"`
}

type workloadScenarioResult struct {
	Name                      string         `json:"name"`
	Origin                    benchmarkPhase `json:"origin"`
	Slizen                    benchmarkPhase `json:"slizen"`
	P50Milliseconds           float64        `json:"p50_ms"`
	P95Milliseconds           float64        `json:"p95_ms"`
	P99Milliseconds           float64        `json:"p99_ms"`
	OriginGETReductionPercent float64        `json:"origin_get_reduction_percent"`
	CacheHitRatioPercent      float64        `json:"cache_hit_ratio_percent"`
	EvidenceValid             bool           `json:"evidence_valid"`
	ProvedOriginGETReduction  bool           `json:"proved_origin_get_reduction"`
	Notes                     []string       `json:"notes,omitempty"`
}

type workloadWorkerResult struct {
	latencies          []time.Duration
	readLatencies      []time.Duration
	writeLatencies     []time.Duration
	readOrderingWaits  []time.Duration
	writeOrderingWaits []time.Duration
	reads              uint64
	writes             uint64
	failures           uint64
	valueMismatches    uint64
}

type workloadValues struct {
	size    int
	digests [][sha256.Size]byte
}

type workloadKeyState struct {
	mu      sync.RWMutex
	version uint64
}

type originGETCounterSnapshot struct {
	Calls uint64
	RunID string
	Err   error
}

func newWorkloadValues(keys []string, size int) workloadValues {
	digests := make([][sha256.Size]byte, len(keys))
	for i, key := range keys {
		digests[i] = sha256.Sum256([]byte(key))
	}
	return workloadValues{size: size, digests: digests}
}

func (v workloadValues) Fill(keyIndex int, version uint64, dst []byte) []byte {
	if cap(dst) < v.size {
		dst = make([]byte, v.size)
	} else {
		dst = dst[:v.size]
	}
	digest := v.versionDigest(keyIndex, version)
	for offset := 0; offset < len(dst); offset += len(digest) {
		copy(dst[offset:], digest[:])
	}
	binary.LittleEndian.PutUint64(dst[:8], version)
	copy(dst[8:16], v.digests[keyIndex][:8])
	return dst
}

func (v workloadValues) Matches(keyIndex int, version uint64, value []byte) bool {
	if len(value) != v.size {
		return false
	}
	if binary.LittleEndian.Uint64(value[:8]) != version || !bytes.Equal(value[8:16], v.digests[keyIndex][:8]) {
		return false
	}
	digest := v.versionDigest(keyIndex, version)
	for i, b := range value[16:] {
		position := i + 16
		if b != digest[position%len(digest)] {
			return false
		}
	}
	return true
}

func (v workloadValues) versionDigest(keyIndex int, version uint64) [sha256.Size]byte {
	var input [sha256.Size + 8]byte
	copy(input[:sha256.Size], v.digests[keyIndex][:])
	binary.LittleEndian.PutUint64(input[sha256.Size:], version)
	return sha256.Sum256(input[:])
}

type statusSnapshot struct {
	Version                 string `json:"version"`
	Commit                  string `json:"commit,omitempty"`
	Mode                    string `json:"mode"`
	KeyVisibility           string `json:"key_visibility"`
	Uptime                  string `json:"uptime"`
	RequestsTotal           uint64 `json:"requests_total"`
	CacheHitsTotal          uint64 `json:"cache_hits_total"`
	CacheMissesTotal        uint64 `json:"cache_misses_total"`
	CacheMissesPolicyBypass uint64 `json:"cache_misses_policy_bypass"`
	CacheMissesNotAdmitted  uint64 `json:"cache_misses_not_admitted"`
	CacheMissesNotPresent   uint64 `json:"cache_misses_not_present"`
	UpstreamRequestsTotal   uint64 `json:"upstream_requests_total"`
	UpstreamGETsTotal       uint64 `json:"upstream_gets_total"`
	CoalescedRequestsTotal  uint64 `json:"coalesced_requests_total"`
	InvalidationsTotal      uint64 `json:"invalidations_total"`
	PromotionsTotal         uint64 `json:"promotions_total"`
	DemotionsTotal          uint64 `json:"demotions_total"`
	CacheEntries            int    `json:"cache_entries"`
	CacheBytes              int64  `json:"cache_bytes"`
	HotKeys                 int    `json:"hot_keys"`
}

func benchmarkCmd(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: slizenctl benchmark hotkey|workload [flags]")
	}
	if args[0] == "workload" {
		return workloadBenchmarkCmd(args[1:], stdout, stderr)
	}
	if args[0] != "hotkey" {
		return errors.New("usage: slizenctl benchmark hotkey|workload [flags]")
	}
	fs := flag.NewFlagSet("benchmark hotkey", flag.ContinueOnError)
	fs.SetOutput(stderr)
	proxyAddr := fs.String("proxy", "127.0.0.1:6380", "Slizen RESP address")
	originAddr := fs.String("origin", "127.0.0.1:6379", "origin Redis or Valkey address")
	adminURL := fs.String("admin", defaultAdmin, "admin API URL")
	key := fs.String("key", "product:iphone_17", "benchmark key")
	value := fs.String("value", `{"name":"iPhone 17","price":999}`, "benchmark value")
	warmup := fs.Duration("warmup", 5*time.Second, "Slizen warmup window")
	duration := fs.Duration("duration", 15*time.Second, "per hot phase duration")
	concurrency := fs.Int("concurrency", 32, "parallel GET workers")
	requests := fs.Int("requests", 50000, "maximum GET requests per measured phase")
	output := fs.String("output", "text", "output format: text or json")
	jsonFile := fs.String("json-file", "", "optional path for JSON result")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if *concurrency <= 0 || *concurrency > 10000 {
		return errors.New("concurrency must be between 1 and 10000")
	}
	if *requests <= 0 {
		return errors.New("requests must be greater than zero")
	}
	if *duration <= 0 {
		return errors.New("duration must be greater than zero")
	}
	if *warmup < 0 {
		return errors.New("warmup cannot be negative")
	}
	if *output != "text" && *output != "json" {
		return errors.New("output must be text or json")
	}

	result, err := runHotKeyBenchmark(*originAddr, *proxyAddr, *adminURL, *key, *value, *warmup, *duration, *concurrency, *requests)
	if err != nil {
		return err
	}
	if *jsonFile != "" {
		data, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			return err
		}
		if err := os.WriteFile(*jsonFile, append(data, '\n'), 0o644); err != nil {
			return err
		}
	}
	if *output == "json" {
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(result)
	}
	printBenchmarkText(stdout, result)
	return nil
}

func workloadBenchmarkCmd(args []string, stdout, stderr io.Writer) error {
	cfg := workloadConfig{}
	fs := flag.NewFlagSet("benchmark workload", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&cfg.ProxyAddr, "proxy", "127.0.0.1:6380", "Slizen RESP address")
	fs.StringVar(&cfg.OriginAddr, "origin", "127.0.0.1:6379", "origin Redis or Valkey address")
	fs.StringVar(&cfg.AdminURL, "admin", defaultAdmin, "admin API URL")
	fs.StringVar(&cfg.Scenario, "scenario", workloadScenarioAll, "scenario: all, uniform, skew-80-20, skew-99-1, or moving-flash")
	fs.StringVar(&cfg.KeyPrefix, "key-prefix", "slizen:benchmark", "prefix for generated benchmark keys")
	fs.IntVar(&cfg.KeyCount, "keys", 1000, "number of keys per scenario")
	fs.IntVar(&cfg.ValueSize, "value-size", 1024, "value size in bytes")
	fs.IntVar(&cfg.ReadRatio, "read-ratio", 95, "percentage of operations that are GETs")
	fs.IntVar(&cfg.Concurrency, "concurrency", 32, "parallel workers")
	fs.IntVar(&cfg.MaxRequests, "requests", 100000, "maximum operations per measured phase")
	fs.DurationVar(&cfg.Duration, "duration", 10*time.Second, "duration per measured phase")
	fs.Int64Var(&cfg.Seed, "seed", 1, "deterministic workload seed")
	fs.IntVar(&cfg.FlashEvery, "flash-every", 5000, "operations before the moving flash key changes")
	fs.StringVar(&cfg.Output, "output", "text", "output format: text or json")
	fs.StringVar(&cfg.JSONFile, "json-file", "", "optional path for JSON result")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	if err := validateWorkloadConfig(cfg); err != nil {
		return err
	}

	result, err := runWorkloadBenchmark(cfg)
	if err != nil {
		return err
	}
	if cfg.JSONFile != "" {
		data, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			return err
		}
		if err := os.WriteFile(cfg.JSONFile, append(data, '\n'), 0o644); err != nil {
			return err
		}
	}
	if cfg.Output == "json" {
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(result)
	}
	printWorkloadBenchmarkText(stdout, result)
	return nil
}

func validateWorkloadConfig(cfg workloadConfig) error {
	if !isWorkloadScenario(cfg.Scenario) {
		return fmt.Errorf("scenario must be one of: %s", strings.Join(append([]string{workloadScenarioAll}, workloadScenarios()...), ", "))
	}
	if cfg.OriginAddr == "" || cfg.ProxyAddr == "" || cfg.AdminURL == "" {
		return errors.New("origin, proxy, and admin addresses must not be empty")
	}
	if cfg.KeyPrefix == "" {
		return errors.New("key-prefix must not be empty")
	}
	if len(cfg.KeyPrefix) > maxWorkloadKeyPrefix {
		return fmt.Errorf("key-prefix must not exceed %d bytes", maxWorkloadKeyPrefix)
	}
	if cfg.KeyCount < 2 || cfg.KeyCount > maxWorkloadKeys {
		return fmt.Errorf("keys must be between 2 and %d", maxWorkloadKeys)
	}
	if (cfg.Scenario == workloadScenarioAll || cfg.Scenario == workloadScenarioSkew99) && cfg.KeyCount < 100 {
		return errors.New("keys must be at least 100 for all and skew-99-1 scenarios")
	}
	if cfg.Scenario == workloadScenarioSkew80 && cfg.KeyCount < 5 {
		return errors.New("keys must be at least 5 for the skew-80-20 scenario")
	}
	if cfg.ValueSize < minWorkloadValueBytes || cfg.ValueSize > maxWorkloadValueBytes {
		return fmt.Errorf("value-size must be between %d and %d bytes", minWorkloadValueBytes, maxWorkloadValueBytes)
	}
	scenarioCount := len(selectedWorkloadScenarios(cfg.Scenario))
	if int64(cfg.KeyCount)*int64(cfg.ValueSize)*int64(scenarioCount) > maxWorkloadDataset {
		return fmt.Errorf("generated dataset must not exceed %d bytes", maxWorkloadDataset)
	}
	if cfg.ReadRatio < 1 || cfg.ReadRatio > 100 {
		return errors.New("read-ratio must be between 1 and 100 percent")
	}
	if cfg.Concurrency < 1 || cfg.Concurrency > maxWorkloadConcurrency {
		return fmt.Errorf("concurrency must be between 1 and %d", maxWorkloadConcurrency)
	}
	if cfg.MaxRequests < 1 || cfg.MaxRequests > maxWorkloadRequests {
		return fmt.Errorf("requests must be between 1 and %d", maxWorkloadRequests)
	}
	if cfg.Concurrency > cfg.MaxRequests {
		return errors.New("concurrency must not exceed requests")
	}
	if cfg.Duration <= 0 || cfg.Duration > maxWorkloadDuration {
		return fmt.Errorf("duration must be greater than zero and at most %s", maxWorkloadDuration)
	}
	if cfg.FlashEvery < 1 || cfg.FlashEvery > maxWorkloadRequests {
		return fmt.Errorf("flash-every must be between 1 and %d", maxWorkloadRequests)
	}
	if (cfg.Scenario == workloadScenarioAll || cfg.Scenario == workloadScenarioMovingFlash) && cfg.FlashEvery >= cfg.MaxRequests {
		return errors.New("flash-every must be less than requests for moving-flash workloads")
	}
	if cfg.Output != "text" && cfg.Output != "json" {
		return errors.New("output must be text or json")
	}
	return nil
}

func isWorkloadScenario(scenario string) bool {
	if scenario == workloadScenarioAll {
		return true
	}
	for _, candidate := range workloadScenarios() {
		if scenario == candidate {
			return true
		}
	}
	return false
}

func selectedWorkloadScenarios(selection string) []string {
	if selection == workloadScenarioAll {
		return workloadScenarios()
	}
	return []string{selection}
}

func workloadScenarios() []string {
	return []string{
		workloadScenarioUniform,
		workloadScenarioSkew80,
		workloadScenarioSkew99,
		workloadScenarioMovingFlash,
	}
}

func runWorkloadBenchmark(cfg workloadConfig) (workloadBenchmarkResult, error) {
	started := time.Now().UTC()
	isolatedKeyPrefix := workloadIsolatedKeyPrefix(cfg.KeyPrefix, started)
	cfg.AdminURL = strings.TrimRight(cfg.AdminURL, "/")
	status, err := readStatusSnapshot(cfg.AdminURL)
	if err != nil {
		return workloadBenchmarkResult{}, err
	}

	origin := redis.NewClient(&redis.Options{
		Addr:         cfg.OriginAddr,
		DialTimeout:  2 * time.Second,
		ReadTimeout:  2 * time.Second,
		WriteTimeout: 2 * time.Second,
		PoolSize:     cfg.Concurrency + 2,
	})
	defer origin.Close()
	versions, versionErr := collectRuntimeVersions(status, origin)
	runtimeEvidenceValid := versionErr == nil && knownOriginRuntimeVersion(versions.Origin)

	result := workloadBenchmarkResult{
		Name:              "Slizen Release Workload Benchmark",
		ScenarioSelection: cfg.Scenario,
		Seed:              cfg.Seed,
		KeyPrefix:         cfg.KeyPrefix,
		IsolatedKeyPrefix: isolatedKeyPrefix,
		KeyCount:          cfg.KeyCount,
		ValueSizeBytes:    cfg.ValueSize,
		ReadRatioPercent:  cfg.ReadRatio,
		WriteRatioPercent: 100 - cfg.ReadRatio,
		Concurrency:       cfg.Concurrency,
		DurationSeconds:   cfg.Duration.Seconds(),
		MaxRequests:       cfg.MaxRequests,
		FlashEvery:        cfg.FlashEvery,
		Mode:              status.Mode,
		StartedAt:         started.Format(time.RFC3339),
		RuntimeVersions:   versions,
		Notes: []string{
			"results describe this run and configuration only; they are not universal capacity or latency claims",
			"isolated_key_prefix is unique to this invocation so cache and adaptive hotness state start clean for every repeated run",
			"successful GETs are checked against deterministic key-and-write-version payloads; per-key ordering and final validation make stale-after-write responses invalidate the evidence",
			"aggregate p50_ms/p95_ms/p99_ms preserve end-to-end harness latency for successful measured reads, writes, and final-validation reads, including per-key ordering wait for generated operations",
			"read_latency and write_latency measure the Redis command after per-key ordering is acquired; read_ordering_wait_latency and write_ordering_wait_latency report that wait separately; final_validation_latency has no ordering wait",
			"read_latency combines cache hits and misses because process-global status counters cannot attribute an individual latency sample to a cache outcome",
			"termination_reason identifies whether measured operation issuance reached the request or duration limit; final validation runs afterward",
			"upstream_gets is the physical origin cmdstat_get calls delta from INFO commandstats; slizen_status_upstream_gets preserves the Slizen logical delta for isolation checks",
		},
	}
	if versionErr != nil {
		result.Notes = append(result.Notes, versionErr.Error())
	}
	if status.Mode != "cache" {
		result.Notes = append(result.Notes, "Slizen is not in cache mode; local cache hits and origin GET reduction are not expected")
	}

	for _, scenario := range selectedWorkloadScenarios(cfg.Scenario) {
		keys := buildWorkloadKeys(isolatedKeyPrefix, scenario, cfg.KeyCount)
		values := newWorkloadValues(keys, cfg.ValueSize)
		if err := seedWorkloadKeys(context.Background(), origin, keys, values); err != nil {
			return workloadBenchmarkResult{}, fmt.Errorf("seed %s workload: %w", scenario, err)
		}

		originGETsBefore := readOriginGETCounter(origin)
		originPhase, err := runRedisWorkload("origin direct", cfg.OriginAddr, keys, values, scenario, cfg)
		if err != nil {
			return workloadBenchmarkResult{}, fmt.Errorf("run %s origin phase: %w", scenario, err)
		}
		originGETsAfter := readOriginGETCounter(origin)
		originPhysicalEvidenceValid := applyOriginGETCounterDelta(&originPhase, originGETsBefore, originGETsAfter)
		if originPhysicalEvidenceValid {
			originPhysicalEvidenceValid = validatePhysicalOriginGETIsolation(&originPhase, originPhase.Reads, "direct phase reads")
		}
		if err := seedWorkloadKeys(context.Background(), origin, keys, values); err != nil {
			return workloadBenchmarkResult{}, fmt.Errorf("reset %s workload before Slizen phase: %w", scenario, err)
		}

		proxyClients, err := newWorkloadClients(cfg.ProxyAddr, cfg.Concurrency)
		if err != nil {
			return workloadBenchmarkResult{}, fmt.Errorf("initialize %s Slizen clients: %w", scenario, err)
		}
		if err := purgeAllBenchmarkCache(cfg.AdminURL); err != nil {
			closeErr := closeWorkloadClients(proxyClients)
			return workloadBenchmarkResult{}, errors.Join(fmt.Errorf("purge cache before %s workload: %w", scenario, err), closeErr)
		}
		before, err := readStatusSnapshot(cfg.AdminURL)
		if err != nil {
			closeErr := closeWorkloadClients(proxyClients)
			return workloadBenchmarkResult{}, errors.Join(err, closeErr)
		}
		slizenOriginGETsBefore := readOriginGETCounter(origin)
		slizenPhase, err := runRedisWorkloadWithClients("slizen", cfg.ProxyAddr, proxyClients, keys, values, scenario, cfg)
		slizenOriginGETsAfter := readOriginGETCounter(origin)
		closeErr := closeWorkloadClients(proxyClients)
		if err != nil {
			err = fmt.Errorf("run %s Slizen phase: %w", scenario, err)
		}
		if err = errors.Join(err, closeErr); err != nil {
			return workloadBenchmarkResult{}, err
		}
		after, err := readStatusSnapshot(cfg.AdminURL)
		if err != nil {
			return workloadBenchmarkResult{}, err
		}
		statusEvidenceValid := applyWorkloadStatusDelta(&slizenPhase, before, after)
		slizenPhysicalEvidenceValid := applyOriginGETCounterDelta(&slizenPhase, slizenOriginGETsBefore, slizenOriginGETsAfter)
		if slizenPhysicalEvidenceValid {
			slizenPhysicalEvidenceValid = validatePhysicalOriginGETIsolation(
				&slizenPhase,
				slizenPhase.SlizenStatusUpstreamGETs,
				"Slizen status upstream GET delta",
			)
		}
		crossPhaseOriginContinuityValid := validateSameOriginRunID(&originPhase, &slizenPhase)
		physicalEvidenceValid := originPhysicalEvidenceValid && slizenPhysicalEvidenceValid
		physicalEvidenceValid = physicalEvidenceValid && crossPhaseOriginContinuityValid
		physicalEvidenceValid = physicalEvidenceValid && runtimeEvidenceValid
		result.Scenarios = append(result.Scenarios, summarizeWorkloadScenario(
			scenario,
			originPhase,
			slizenPhase,
			statusEvidenceValid,
			physicalEvidenceValid,
		))
	}
	result.FinishedAt = time.Now().UTC().Format(time.RFC3339)
	return result, nil
}

func workloadIsolatedKeyPrefix(prefix string, started time.Time) string {
	return fmt.Sprintf("%s:run-%d-%d", prefix, started.UnixNano(), os.Getpid())
}

func buildWorkloadKeys(prefix, scenario string, count int) []string {
	keys := make([]string, count)
	for i := range keys {
		keys[i] = fmt.Sprintf("%s:%s:%06d", prefix, scenario, i)
	}
	return keys
}

func seedWorkloadKeys(ctx context.Context, client *redis.Client, keys []string, values workloadValues) error {
	batchSize := seedPipelineBytes / values.size
	if batchSize < 1 {
		batchSize = 1
	}
	if batchSize > 1000 {
		batchSize = 1000
	}
	for start := 0; start < len(keys); start += batchSize {
		end := minInt(start+batchSize, len(keys))
		pipe := client.Pipeline()
		for index := start; index < end; index++ {
			pipe.Set(ctx, keys[index], values.Fill(index, 0, nil), 0)
		}
		if _, err := pipe.Exec(ctx); err != nil {
			return err
		}
	}
	return nil
}

func runRedisWorkload(name, addr string, keys []string, values workloadValues, scenario string, cfg workloadConfig) (benchmarkPhase, error) {
	clients, err := newWorkloadClients(addr, cfg.Concurrency)
	if err != nil {
		return benchmarkPhase{}, err
	}
	phase, runErr := runRedisWorkloadWithClients(name, addr, clients, keys, values, scenario, cfg)
	return phase, errors.Join(runErr, closeWorkloadClients(clients))
}

func newWorkloadClients(addr string, count int) ([]*redis.Client, error) {
	if count < 1 || count > maxWorkloadConcurrency {
		return nil, fmt.Errorf("workload client count must be between 1 and %d", maxWorkloadConcurrency)
	}
	clients := make([]*redis.Client, count)
	initErrs := make([]error, count)
	var wg sync.WaitGroup
	wg.Add(count)
	for i := 0; i < count; i++ {
		clients[i] = redis.NewClient(&redis.Options{
			Addr:            addr,
			DialTimeout:     2 * time.Second,
			ReadTimeout:     2 * time.Second,
			WriteTimeout:    2 * time.Second,
			PoolSize:        1,
			Protocol:        2,
			DisableIdentity: true,
		})
		go func(index int) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			if err := clients[index].Ping(ctx).Err(); err != nil {
				initErrs[index] = fmt.Errorf("initialize workload connection %d: %w", index+1, err)
			}
		}(i)
	}
	wg.Wait()
	if err := errors.Join(initErrs...); err != nil {
		return nil, errors.Join(err, closeWorkloadClients(clients))
	}
	return clients, nil
}

func closeWorkloadClients(clients []*redis.Client) error {
	var errs []error
	for i, client := range clients {
		if err := client.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close workload connection %d: %w", i+1, err))
		}
	}
	return errors.Join(errs...)
}

func runRedisWorkloadWithClients(name, addr string, clients []*redis.Client, keys []string, values workloadValues, scenario string, cfg workloadConfig) (benchmarkPhase, error) {
	if len(clients) != cfg.Concurrency {
		return benchmarkPhase{}, fmt.Errorf("workload client count %d does not match concurrency %d", len(clients), cfg.Concurrency)
	}
	ctx, cancel := context.WithTimeout(context.Background(), cfg.Duration)
	defer cancel()
	var issued atomic.Uint64
	workers := make([]workloadWorkerResult, cfg.Concurrency)
	keyStates := make([]workloadKeyState, len(keys))
	start := time.Now()

	var wg sync.WaitGroup
	wg.Add(cfg.Concurrency)
	for workerIndex := 0; workerIndex < cfg.Concurrency; workerIndex++ {
		workerIndex := workerIndex
		go func() {
			defer wg.Done()
			client := clients[workerIndex]
			worker := &workers[workerIndex]
			valueBuffer := make([]byte, values.size)
			operationCtx := context.Background()
			for {
				if ctx.Err() != nil {
					return
				}
				operationIndex := issued.Add(1) - 1
				if operationIndex >= uint64(cfg.MaxRequests) {
					return
				}
				keyIndex, read := workloadRequestAt(scenario, cfg, operationIndex)
				requestStart := time.Now()
				var err error
				var commandElapsed time.Duration
				var orderingWait time.Duration
				valueMismatch := false
				state := &keyStates[keyIndex]
				if read {
					state.mu.RLock()
					orderingWait = time.Since(requestStart)
					expectedVersion := state.version
					var value []byte
					commandStart := time.Now()
					value, err = client.Get(operationCtx, keys[keyIndex]).Bytes()
					commandElapsed = time.Since(commandStart)
					valueMismatch = err == nil && !values.Matches(keyIndex, expectedVersion, value)
					state.mu.RUnlock()
				} else {
					state.mu.Lock()
					orderingWait = time.Since(requestStart)
					nextVersion := state.version + 1
					value := values.Fill(keyIndex, nextVersion, valueBuffer)
					commandStart := time.Now()
					err = client.Set(operationCtx, keys[keyIndex], value, 0).Err()
					commandElapsed = time.Since(commandStart)
					if err == nil {
						state.version = nextVersion
					}
					state.mu.Unlock()
				}
				elapsed := time.Since(requestStart)
				if err != nil || valueMismatch {
					worker.failures++
					if valueMismatch {
						worker.valueMismatches++
					}
					continue
				}
				worker.latencies = append(worker.latencies, elapsed)
				if read {
					worker.readLatencies = append(worker.readLatencies, commandElapsed)
					worker.readOrderingWaits = append(worker.readOrderingWaits, orderingWait)
					worker.reads++
				} else {
					worker.writeLatencies = append(worker.writeLatencies, commandElapsed)
					worker.writeOrderingWaits = append(worker.writeOrderingWaits, orderingWait)
					worker.writes++
				}
			}
		}()
	}
	wg.Wait()
	validation, validationReads, err := validateWrittenWorkloadValues(clients, keys, values, keyStates, cfg.Duration)
	if err != nil {
		return benchmarkPhase{}, err
	}
	elapsed := time.Since(start)

	var reads, writes, operationFailures, valueMismatches uint64
	for _, worker := range workers {
		reads += worker.reads
		writes += worker.writes
		operationFailures += worker.failures
		valueMismatches += worker.valueMismatches
	}
	latencies := make([]time.Duration, 0, reads+writes+validation.reads)
	readLatencies := make([]time.Duration, 0, reads)
	writeLatencies := make([]time.Duration, 0, writes)
	readOrderingWaits := make([]time.Duration, 0, reads)
	writeOrderingWaits := make([]time.Duration, 0, writes)
	for _, worker := range workers {
		latencies = append(latencies, worker.latencies...)
		readLatencies = append(readLatencies, worker.readLatencies...)
		writeLatencies = append(writeLatencies, worker.writeLatencies...)
		readOrderingWaits = append(readOrderingWaits, worker.readOrderingWaits...)
		writeOrderingWaits = append(writeOrderingWaits, worker.writeOrderingWaits...)
	}
	validationLatencies := validation.readLatencies
	latencies = append(latencies, validation.latencies...)
	operationAttempts := reads + writes + operationFailures
	terminationReason := workloadTerminationReason(operationAttempts, cfg.MaxRequests)
	failures := operationFailures
	reads += validation.reads
	failures += validation.failures
	valueMismatches += validation.valueMismatches
	requests := reads + writes
	aggregateLatency := latencyDistributionFor(latencies)
	phase := benchmarkPhase{
		Name:                     name,
		Address:                  addr,
		Requests:                 requests,
		OperationAttempts:        operationAttempts,
		TerminationReason:        terminationReason,
		Reads:                    reads,
		Writes:                   writes,
		Failures:                 failures,
		ValueMismatches:          valueMismatches,
		ValidationReads:          validationReads,
		ValidationFailures:       validation.failures,
		ValidationMismatches:     validation.valueMismatches,
		ElapsedSeconds:           elapsed.Seconds(),
		P50Milliseconds:          aggregateLatency.P50Milliseconds,
		P95Milliseconds:          aggregateLatency.P95Milliseconds,
		P99Milliseconds:          aggregateLatency.P99Milliseconds,
		ReadLatency:              latencyDistributionPointer(readLatencies),
		WriteLatency:             latencyDistributionPointer(writeLatencies),
		ReadOrderingWaitLatency:  latencyDistributionPointer(readOrderingWaits),
		WriteOrderingWaitLatency: latencyDistributionPointer(writeOrderingWaits),
		FinalValidationLatency:   latencyDistributionPointer(validationLatencies),
	}
	phase.Notes = append(phase.Notes, fmt.Sprintf("measured operation issuance stopped at the %s after %d operations", strings.ReplaceAll(terminationReason, "_", " "), operationAttempts))
	if validationReads > 0 {
		phase.Notes = append(phase.Notes, fmt.Sprintf("validated the final write generation for %d keys", validationReads))
	}
	if elapsed > 0 {
		phase.OpsPerSecond = float64(requests) / elapsed.Seconds()
	}
	if requests == 0 {
		return phase, errors.New("phase completed without a successful operation")
	}
	return phase, nil
}

func workloadTerminationReason(operationAttempts uint64, maxRequests int) string {
	if operationAttempts >= uint64(maxRequests) {
		return "request_limit"
	}
	return "duration_limit"
}

func validateWrittenWorkloadValues(clients []*redis.Client, keys []string, values workloadValues, states []workloadKeyState, duration time.Duration) (workloadWorkerResult, uint64, error) {
	type validationKey struct {
		index   int
		version uint64
	}
	changed := make([]validationKey, 0, len(states))
	for index := range states {
		if states[index].version > 0 {
			changed = append(changed, validationKey{index: index, version: states[index].version})
		}
	}
	if len(changed) == 0 {
		return workloadWorkerResult{}, 0, nil
	}

	validationTimeout := duration
	if validationTimeout < 10*time.Second {
		validationTimeout = 10 * time.Second
	}
	if validationTimeout > time.Minute {
		validationTimeout = time.Minute
	}
	ctx, cancel := context.WithTimeout(context.Background(), validationTimeout)
	defer cancel()

	workerCount := minInt(len(clients), len(changed))
	workers := make([]workloadWorkerResult, workerCount)
	var issued atomic.Uint64
	var attempted atomic.Uint64
	var wg sync.WaitGroup
	wg.Add(workerCount)
	for workerIndex := 0; workerIndex < workerCount; workerIndex++ {
		workerIndex := workerIndex
		go func() {
			defer wg.Done()
			worker := &workers[workerIndex]
			client := clients[workerIndex]
			for {
				position := issued.Add(1) - 1
				if position >= uint64(len(changed)) || ctx.Err() != nil {
					return
				}
				key := changed[position]
				attempted.Add(1)
				started := time.Now()
				value, err := client.Get(ctx, keys[key.index]).Bytes()
				elapsed := time.Since(started)
				mismatch := err == nil && !values.Matches(key.index, key.version, value)
				if err != nil || mismatch {
					worker.failures++
					if mismatch {
						worker.valueMismatches++
					}
					continue
				}
				worker.latencies = append(worker.latencies, elapsed)
				worker.readLatencies = append(worker.readLatencies, elapsed)
				worker.reads++
			}
		}()
	}
	wg.Wait()
	if attempted.Load() != uint64(len(changed)) {
		return workloadWorkerResult{}, attempted.Load(), fmt.Errorf("final write-generation validation timed out after checking %d of %d keys", attempted.Load(), len(changed))
	}

	result := workloadWorkerResult{}
	for _, worker := range workers {
		result.latencies = append(result.latencies, worker.latencies...)
		result.readLatencies = append(result.readLatencies, worker.readLatencies...)
		result.reads += worker.reads
		result.failures += worker.failures
		result.valueMismatches += worker.valueMismatches
	}
	return result, attempted.Load(), nil
}

func workloadRequestAt(scenario string, cfg workloadConfig, operationIndex uint64) (keyIndex int, read bool) {
	read = deterministicWorkloadValue(cfg.Seed, operationIndex, 1)%100 < uint64(cfg.ReadRatio)
	choice := deterministicWorkloadValue(cfg.Seed, operationIndex, 2) % 100
	keyValue := deterministicWorkloadValue(cfg.Seed, operationIndex, 3)

	switch scenario {
	case workloadScenarioSkew80:
		hotKeys := maxInt(1, cfg.KeyCount/5)
		if choice < 80 {
			return int(keyValue % uint64(hotKeys)), read
		}
		return hotKeys + int(keyValue%uint64(cfg.KeyCount-hotKeys)), read
	case workloadScenarioSkew99:
		hotKeys := maxInt(1, cfg.KeyCount/100)
		if choice < 99 {
			return int(keyValue % uint64(hotKeys)), read
		}
		return hotKeys + int(keyValue%uint64(cfg.KeyCount-hotKeys)), read
	case workloadScenarioMovingFlash:
		if choice < 99 {
			offset := deterministicWorkloadValue(cfg.Seed, 0, 4) % uint64(cfg.KeyCount)
			window := operationIndex / uint64(cfg.FlashEvery)
			return int((offset + window) % uint64(cfg.KeyCount)), read
		}
		return int(keyValue % uint64(cfg.KeyCount)), read
	default:
		return int(keyValue % uint64(cfg.KeyCount)), read
	}
}

func deterministicWorkloadValue(seed int64, operationIndex, stream uint64) uint64 {
	value := uint64(seed) + 0x9e3779b97f4a7c15*(operationIndex+1) + 0xd1b54a32d192ed03*stream
	value = (value ^ (value >> 30)) * 0xbf58476d1ce4e5b9
	value = (value ^ (value >> 27)) * 0x94d049bb133111eb
	return value ^ (value >> 31)
}

func summarizeWorkloadScenario(name string, origin, slizen benchmarkPhase, statusEvidenceValid, physicalEvidenceValid bool) workloadScenarioResult {
	evidenceValid := origin.Failures == 0 &&
		slizen.Failures == 0 &&
		origin.ValueMismatches == 0 &&
		slizen.ValueMismatches == 0 &&
		statusEvidenceValid &&
		physicalEvidenceValid
	reduction := 0.0
	cacheHitRatio := 0.0
	if evidenceValid {
		reduction = workloadOriginGETReduction(origin, slizen)
		cacheHitRatio = slizen.CacheHitRatio
	}
	result := workloadScenarioResult{
		Name:                      name,
		Origin:                    origin,
		Slizen:                    slizen,
		P50Milliseconds:           slizen.P50Milliseconds,
		P95Milliseconds:           slizen.P95Milliseconds,
		P99Milliseconds:           slizen.P99Milliseconds,
		OriginGETReductionPercent: reduction,
		CacheHitRatioPercent:      cacheHitRatio,
		EvidenceValid:             evidenceValid,
		ProvedOriginGETReduction: evidenceValid &&
			reduction > 0 &&
			slizen.CacheHits > 0 &&
			slizen.UpstreamGETs < slizen.Reads,
	}
	if origin.Failures > 0 || slizen.Failures > 0 {
		result.Notes = append(result.Notes, "origin GET reduction was suppressed because one or both phases recorded failed operations")
	}
	if origin.ValueMismatches > 0 || slizen.ValueMismatches > 0 {
		result.Notes = append(result.Notes, "origin GET reduction was suppressed because one or both phases returned an unexpected value")
	}
	if !statusEvidenceValid {
		result.Notes = append(result.Notes, "origin GET reduction was suppressed because Slizen logical process-global counters did not provide isolated, monotonic evidence")
	}
	if !physicalEvidenceValid {
		result.Notes = append(result.Notes, "origin GET reduction was suppressed because INFO commandstats did not provide isolated, monotonic physical GET evidence")
	}
	if !result.ProvedOriginGETReduction {
		result.Notes = append(result.Notes, "this run did not prove origin GET reduction for the scenario")
	}
	return result
}

func workloadOriginGETReduction(origin, slizen benchmarkPhase) float64 {
	if origin.Failures > 0 ||
		slizen.Failures > 0 ||
		origin.Reads == 0 ||
		slizen.Reads == 0 ||
		origin.UpstreamGETsSource != originGETsSourceCommandstats ||
		slizen.UpstreamGETsSource != originGETsSourceCommandstats {
		return 0
	}
	originRate := float64(origin.UpstreamGETs) / float64(origin.Reads)
	if originRate == 0 {
		return 0
	}
	slizenRate := float64(slizen.UpstreamGETs) / float64(slizen.Reads)
	return 100 * (1 - slizenRate/originRate)
}

func purgeAllBenchmarkCache(adminURL string) error {
	_, err := httpPost(strings.TrimRight(adminURL, "/")+"/v1/cache/purge", []byte(`{}`))
	return err
}

func readOriginGETCounter(origin *redis.Client) originGETCounterSnapshot {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	serverBefore, err := origin.Info(ctx, "server").Result()
	if err != nil {
		return originGETCounterSnapshot{Err: fmt.Errorf("INFO server before commandstats: %w", err)}
	}
	runIDBefore, err := parseOriginRunID(serverBefore)
	if err != nil {
		return originGETCounterSnapshot{Err: err}
	}
	info, err := origin.Info(ctx, "commandstats").Result()
	if err != nil {
		return originGETCounterSnapshot{Err: fmt.Errorf("INFO commandstats: %w", err)}
	}
	calls, err := parseOriginGETCalls(info)
	if err != nil {
		return originGETCounterSnapshot{Err: err}
	}
	serverAfter, err := origin.Info(ctx, "server").Result()
	if err != nil {
		return originGETCounterSnapshot{Err: fmt.Errorf("INFO server after commandstats: %w", err)}
	}
	runIDAfter, err := parseOriginRunID(serverAfter)
	if err != nil {
		return originGETCounterSnapshot{Err: err}
	}
	if runIDAfter != runIDBefore {
		return originGETCounterSnapshot{Err: errors.New("origin run_id changed while reading INFO commandstats")}
	}
	return originGETCounterSnapshot{Calls: calls, RunID: runIDBefore}
}

func parseOriginGETCalls(info string) (uint64, error) {
	sawCommandstats := false
	sawGET := false
	var calls uint64
	for _, rawLine := range strings.Split(info, "\n") {
		line := strings.TrimSpace(rawLine)
		if strings.HasPrefix(line, "#") {
			section := strings.TrimSpace(strings.TrimPrefix(line, "#"))
			if strings.EqualFold(section, "Commandstats") {
				sawCommandstats = true
			}
			continue
		}
		if !strings.HasPrefix(line, "cmdstat_get:") {
			continue
		}
		if sawGET {
			return 0, errors.New("origin INFO commandstats included duplicate cmdstat_get entries")
		}
		sawGET = true
		fields := strings.Split(strings.TrimPrefix(line, "cmdstat_get:"), ",")
		foundCalls := false
		for _, field := range fields {
			name, value, ok := strings.Cut(strings.TrimSpace(field), "=")
			if !ok || name != "calls" {
				continue
			}
			parsed, err := strconv.ParseUint(value, 10, 64)
			if err != nil {
				return 0, fmt.Errorf("parse origin cmdstat_get calls: %w", err)
			}
			calls = parsed
			foundCalls = true
			break
		}
		if !foundCalls {
			return 0, errors.New("origin INFO commandstats cmdstat_get entry did not include calls")
		}
	}
	if !sawCommandstats {
		return 0, errors.New("origin INFO response did not include a Commandstats section")
	}
	return calls, nil
}

func parseOriginRunID(info string) (string, error) {
	for _, rawLine := range strings.Split(info, "\n") {
		line := strings.TrimSpace(rawLine)
		name, value, ok := strings.Cut(line, ":")
		if ok && name == "run_id" {
			value = strings.TrimSpace(value)
			if value == "" {
				break
			}
			return value, nil
		}
	}
	return "", errors.New("origin INFO server did not include run_id")
}

func applyOriginGETCounterDelta(phase *benchmarkPhase, before, after originGETCounterSnapshot) bool {
	phase.UpstreamGETs = 0
	phase.UpstreamGETsSource = originGETsSourceCommandstats
	if before.Err != nil {
		phase.UpstreamGETsSource = originGETsSourceUnavailable
		phase.Notes = append(phase.Notes, fmt.Sprintf(
			"physical origin GET evidence is unavailable before the phase: %v",
			before.Err,
		))
		return false
	}
	if after.Err != nil {
		phase.UpstreamGETsSource = originGETsSourceUnavailable
		phase.Notes = append(phase.Notes, fmt.Sprintf(
			"physical origin GET evidence is unavailable after the phase: %v",
			after.Err,
		))
		return false
	}
	if before.RunID == "" || after.RunID == "" {
		phase.Notes = append(phase.Notes, "origin run_id was unavailable; restart continuity could not be verified")
		return false
	}
	if after.RunID != before.RunID {
		phase.Notes = append(phase.Notes, "origin run_id changed during the phase; restart invalidated physical evidence")
		return false
	}
	if after.Calls < before.Calls {
		phase.Notes = append(phase.Notes, fmt.Sprintf(
			"origin INFO commandstats cmdstat_get calls decreased from %d to %d; reset or restart invalidated physical evidence",
			before.Calls,
			after.Calls,
		))
		return false
	}
	phase.UpstreamGETs = after.Calls - before.Calls
	phase.OriginRunID = before.RunID
	return true
}

func validateSameOriginRunID(phases ...*benchmarkPhase) bool {
	if len(phases) < 2 {
		return true
	}
	expected := phases[0].OriginRunID
	if expected == "" {
		for _, phase := range phases {
			phase.Notes = append(phase.Notes, "origin run_id continuity across comparison phases could not be verified")
		}
		return false
	}
	for _, phase := range phases[1:] {
		if phase.OriginRunID == expected {
			continue
		}
		for _, compared := range phases {
			compared.Notes = append(compared.Notes, "origin run_id changed between comparison phases; restart invalidated reduction evidence")
		}
		return false
	}
	return true
}

func validatePhysicalOriginGETIsolation(phase *benchmarkPhase, expected uint64, expectation string) bool {
	if phase.UpstreamGETsSource != originGETsSourceCommandstats {
		return false
	}
	if phase.UpstreamGETs == expected {
		return true
	}
	phase.Notes = append(phase.Notes, fmt.Sprintf(
		"physical origin GET counter changed by %d while %s was %d; retries or unrelated origin traffic invalidated isolation",
		phase.UpstreamGETs,
		expectation,
		expected,
	))
	return false
}

func collectRuntimeVersions(status statusSnapshot, origin *redis.Client) (runtimeVersions, error) {
	versions := runtimeVersions{
		Slizenctl:   buildinfo.String(),
		Slizen:      status.Version,
		Commit:      status.Commit,
		Origin:      "unknown",
		Go:          runtime.Version(),
		OperatingOS: runtime.GOOS,
		Arch:        runtime.GOARCH,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	info, err := origin.Info(ctx, "server").Result()
	if err != nil {
		return versions, fmt.Errorf("origin runtime version unavailable: %w", err)
	}
	versions.Origin = parseOriginVersion(info)
	if versions.Origin == "unknown" {
		return versions, errors.New("origin runtime version unavailable: INFO server did not include a version")
	}
	return versions, nil
}

func knownOriginRuntimeVersion(version string) bool {
	version = strings.TrimSpace(version)
	return version != "" && !strings.EqualFold(version, "unknown")
}

func parseOriginVersion(info string) string {
	fields := make(map[string]string)
	for _, line := range strings.Split(info, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, value, ok := strings.Cut(line, ":")
		if ok {
			fields[name] = strings.TrimSpace(value)
		}
	}
	name := fields["server_name"]
	version := fields["redis_version"]
	if strings.EqualFold(name, "valkey") {
		version = fields["valkey_version"]
	} else if name == "" && fields["valkey_version"] != "" {
		name = "Valkey"
		version = fields["valkey_version"]
	}
	if name == "" && version != "" {
		name = "Redis-compatible"
	}
	if name == "" {
		return "unknown"
	}
	if version == "" {
		return name
	}
	return name + " " + version
}

func printWorkloadBenchmarkText(w io.Writer, result workloadBenchmarkResult) {
	fmt.Fprintln(w, "Slizen Release Workload Benchmark")
	fmt.Fprintln(w)
	fmt.Fprintf(w, "Scenario selection: %s\n", result.ScenarioSelection)
	fmt.Fprintf(w, "Seed: %d\n", result.Seed)
	fmt.Fprintf(w, "Isolated key prefix: %s\n", result.IsolatedKeyPrefix)
	fmt.Fprintf(w, "Keys: %d\n", result.KeyCount)
	fmt.Fprintf(w, "Value size: %d bytes\n", result.ValueSizeBytes)
	fmt.Fprintf(w, "Read/write: %d/%d\n", result.ReadRatioPercent, result.WriteRatioPercent)
	fmt.Fprintf(w, "Concurrency: %d\n", result.Concurrency)
	fmt.Fprintf(w, "Runtime: slizen=%s origin=%s go=%s %s/%s\n", result.RuntimeVersions.Slizen, result.RuntimeVersions.Origin, result.RuntimeVersions.Go, result.RuntimeVersions.OperatingOS, result.RuntimeVersions.Arch)
	fmt.Fprintln(w)
	fmt.Fprintf(w, "%-16s %-13s %10s %9s %9s %8s %8s %8s %13s %11s\n", "Scenario", "Phase", "Ops/sec", "Reads", "Writes", "mix p50", "mix p95", "mix p99", "Origin GETs", "Hit rate")
	for _, scenario := range result.Scenarios {
		for _, phase := range []benchmarkPhase{scenario.Origin, scenario.Slizen} {
			readLatency := latencyDistributionValue(phase.ReadLatency)
			writeLatency := latencyDistributionValue(phase.WriteLatency)
			readOrderingWait := latencyDistributionValue(phase.ReadOrderingWaitLatency)
			writeOrderingWait := latencyDistributionValue(phase.WriteOrderingWaitLatency)
			validationLatency := latencyDistributionValue(phase.FinalValidationLatency)
			fmt.Fprintf(w, "%-16s %-13s %10.0f %9d %9d %7.2fms %7.2fms %7.2fms %13d %10.1f%%\n",
				scenario.Name,
				phase.Name,
				phase.OpsPerSecond,
				phase.Reads,
				phase.Writes,
				phase.P50Milliseconds,
				phase.P95Milliseconds,
				phase.P99Milliseconds,
				phase.UpstreamGETs,
				phase.CacheHitRatio,
			)
			fmt.Fprintf(w, "    latency p50/p95/p99: read=%d@%.2f/%.2f/%.2fms write=%d@%.2f/%.2f/%.2fms final-validation=%d@%.2f/%.2f/%.2fms\n",
				readLatency.Samples,
				readLatency.P50Milliseconds,
				readLatency.P95Milliseconds,
				readLatency.P99Milliseconds,
				writeLatency.Samples,
				writeLatency.P50Milliseconds,
				writeLatency.P95Milliseconds,
				writeLatency.P99Milliseconds,
				validationLatency.Samples,
				validationLatency.P50Milliseconds,
				validationLatency.P95Milliseconds,
				validationLatency.P99Milliseconds,
			)
			fmt.Fprintf(w, "    per-key ordering wait p50/p95/p99: read=%d@%.2f/%.2f/%.2fms write=%d@%.2f/%.2f/%.2fms\n",
				readOrderingWait.Samples,
				readOrderingWait.P50Milliseconds,
				readOrderingWait.P95Milliseconds,
				readOrderingWait.P99Milliseconds,
				writeOrderingWait.Samples,
				writeOrderingWait.P50Milliseconds,
				writeOrderingWait.P95Milliseconds,
				writeOrderingWait.P99Milliseconds,
			)
			fmt.Fprintf(w, "    issuance: %s after %d operations\n", phase.TerminationReason, phase.OperationAttempts)
		}
		fmt.Fprintf(w, "  result: origin_get_reduction=%.1f%% cache_hit_ratio=%.1f%% value_mismatches=%d validation_reads=%d validation_failures=%d validation_mismatches=%d evidence_valid=%t proved=%t\n",
			scenario.OriginGETReductionPercent,
			scenario.CacheHitRatioPercent,
			scenario.Origin.ValueMismatches+scenario.Slizen.ValueMismatches,
			scenario.Origin.ValidationReads+scenario.Slizen.ValidationReads,
			scenario.Origin.ValidationFailures+scenario.Slizen.ValidationFailures,
			scenario.Origin.ValidationMismatches+scenario.Slizen.ValidationMismatches,
			scenario.EvidenceValid,
			scenario.ProvedOriginGETReduction,
		)
		for _, note := range scenario.Notes {
			fmt.Fprintf(w, "  note: %s\n", note)
		}
	}
	for _, note := range result.Notes {
		fmt.Fprintf(w, "note: %s\n", note)
	}
}

func runHotKeyBenchmark(originAddr, proxyAddr, adminURL, key, value string, warmup, duration time.Duration, concurrency, requests int) (benchmarkResult, error) {
	started := time.Now().UTC()
	adminURL = strings.TrimRight(adminURL, "/")
	before, err := readStatusSnapshot(adminURL)
	if err != nil {
		return benchmarkResult{}, err
	}
	origin := redis.NewClient(&redis.Options{Addr: originAddr, DialTimeout: 2 * time.Second, ReadTimeout: 2 * time.Second, WriteTimeout: 2 * time.Second, PoolSize: concurrency + 2})
	defer origin.Close()
	if err := origin.Set(context.Background(), key, value, 0).Err(); err != nil {
		return benchmarkResult{}, fmt.Errorf("seed origin: %w", err)
	}
	if err := purgeBenchmarkCache(adminURL, key); err != nil {
		return benchmarkResult{}, fmt.Errorf("purge benchmark key before origin phase: %w", err)
	}
	versions, versionErr := collectRuntimeVersions(before, origin)
	runtimeEvidenceValid := versionErr == nil && knownOriginRuntimeVersion(versions.Origin)

	result := benchmarkResult{
		Name:            "Slizen Hot Key Benchmark",
		Key:             key,
		Concurrency:     concurrency,
		DurationSeconds: duration.Seconds(),
		WarmupSeconds:   warmup.Seconds(),
		MaxRequests:     requests,
		Mode:            before.Mode,
		KeyVisibility:   before.KeyVisibility,
		StartedAt:       started.Format(time.RFC3339),
		StatusBefore:    before,
		RuntimeVersions: versions,
	}
	if versionErr != nil {
		result.Notes = append(result.Notes, versionErr.Error())
	}

	originGETsBefore := readOriginGETCounter(origin)
	originPhase, err := runRedisGetLoad("origin direct", originAddr, key, value, concurrency, requests, duration)
	if err != nil {
		return benchmarkResult{}, err
	}
	originGETsAfter := readOriginGETCounter(origin)
	originPhysicalEvidenceValid := applyOriginGETCounterDelta(&originPhase, originGETsBefore, originGETsAfter)
	if originPhysicalEvidenceValid {
		originPhysicalEvidenceValid = validatePhysicalOriginGETIsolation(&originPhase, originPhase.Requests, "direct phase reads")
	}
	result.Phases = append(result.Phases, originPhase)

	if err := purgeBenchmarkCache(adminURL, key); err != nil {
		return benchmarkResult{}, fmt.Errorf("purge benchmark key before cold phase: %w", err)
	}
	coldRequests := minInt(requests, maxInt(concurrency*2, 1))
	coldBefore, err := readStatusSnapshot(adminURL)
	if err != nil {
		return benchmarkResult{}, err
	}
	coldOriginGETsBefore := readOriginGETCounter(origin)
	coldPhase, err := runRedisGetLoad("slizen cold", proxyAddr, key, value, concurrency, coldRequests, minDuration(duration, maxDuration(500*time.Millisecond, warmup)))
	coldOriginGETsAfter := readOriginGETCounter(origin)
	if err != nil {
		return benchmarkResult{}, err
	}
	coldAfter, err := readStatusSnapshot(adminURL)
	if err != nil {
		return benchmarkResult{}, err
	}
	coldStatusEvidenceValid := applyHotKeyStatusDelta(&coldPhase, coldBefore, coldAfter)
	coldPhysicalEvidenceValid := applyOriginGETCounterDelta(&coldPhase, coldOriginGETsBefore, coldOriginGETsAfter)
	if coldPhysicalEvidenceValid {
		coldPhysicalEvidenceValid = validatePhysicalOriginGETIsolation(
			&coldPhase,
			coldPhase.SlizenStatusUpstreamGETs,
			"Slizen status upstream GET delta",
		)
	}
	coldPhase.Notes = append(coldPhase.Notes, "cold phase caps requests to avoid intentionally warming the key")
	if before.Mode == "observe" {
		coldPhase.Name = "slizen observe"
		coldPhase.Notes = append(coldPhase.Notes, "observe mode forwards reads and does not populate local cache")
	}
	result.Phases = append(result.Phases, coldPhase)

	var warmNotes []string
	if before.Mode == "cache" {
		if err := warmSlizenKey(proxyAddr, adminURL, key, concurrency, warmup); err != nil {
			warmNotes = append(warmNotes, err.Error())
		}
	} else {
		warmNotes = append(warmNotes, "Slizen is not in cache mode; hot phase cannot produce local cache hits")
	}
	hotBefore, err := readStatusSnapshot(adminURL)
	if err != nil {
		return benchmarkResult{}, err
	}
	hotOriginGETsBefore := readOriginGETCounter(origin)
	hotPhase, err := runRedisGetLoad("slizen hot", proxyAddr, key, value, concurrency, requests, duration)
	hotOriginGETsAfter := readOriginGETCounter(origin)
	if err != nil {
		return benchmarkResult{}, err
	}
	hotAfter, err := readStatusSnapshot(adminURL)
	if err != nil {
		return benchmarkResult{}, err
	}
	hotStatusEvidenceValid := applyHotKeyStatusDelta(&hotPhase, hotBefore, hotAfter)
	hotPhysicalEvidenceValid := applyOriginGETCounterDelta(&hotPhase, hotOriginGETsBefore, hotOriginGETsAfter)
	if hotPhysicalEvidenceValid {
		hotPhysicalEvidenceValid = validatePhysicalOriginGETIsolation(
			&hotPhase,
			hotPhase.SlizenStatusUpstreamGETs,
			"Slizen status upstream GET delta",
		)
	}
	hotPhase.Notes = append(hotPhase.Notes, warmNotes...)
	result.Phases = append(result.Phases, hotPhase)

	crossPhaseOriginContinuityValid := validateSameOriginRunID(&originPhase, &coldPhase, &hotPhase)
	result.EvidenceValid = runtimeEvidenceValid &&
		originPhysicalEvidenceValid &&
		coldStatusEvidenceValid &&
		coldPhysicalEvidenceValid &&
		hotStatusEvidenceValid &&
		hotPhysicalEvidenceValid &&
		crossPhaseOriginContinuityValid &&
		originPhase.Failures == 0 &&
		coldPhase.Failures == 0 &&
		hotPhase.Failures == 0 &&
		originPhase.ValueMismatches == 0 &&
		coldPhase.ValueMismatches == 0 &&
		hotPhase.ValueMismatches == 0
	if result.EvidenceValid {
		result.CacheHitRatio = hotPhase.CacheHitRatio
		result.UpstreamGetReduction = upstreamReduction(originPhase, hotPhase)
	}
	result.ProvedReduction = result.EvidenceValid &&
		result.UpstreamGetReduction > 0 &&
		result.CacheHitRatio > 0 &&
		hotPhase.UpstreamGETs < hotPhase.Requests
	if !result.EvidenceValid {
		result.Notes = append(result.Notes, "benchmark evidence is invalid because physical origin commandstats or Slizen logical isolation checks failed")
	}
	if !result.ProvedReduction {
		result.Notes = append(result.Notes, "benchmark did not prove upstream GET reduction for this run/configuration")
	}
	result.StatusAfter = hotAfter
	result.FinishedAt = time.Now().UTC().Format(time.RFC3339)
	return result, nil
}

func runRedisGetLoad(name, addr, key, expectedValue string, concurrency, maxRequests int, duration time.Duration) (benchmarkPhase, error) {
	ctx, cancel := context.WithTimeout(context.Background(), duration)
	defer cancel()
	expected := []byte(expectedValue)
	var issued atomic.Uint64
	var successes atomic.Uint64
	var failures atomic.Uint64
	var valueMismatches atomic.Uint64
	latencies := make(chan time.Duration, maxRequests)
	start := time.Now()

	var wg sync.WaitGroup
	wg.Add(concurrency)
	for i := 0; i < concurrency; i++ {
		go func() {
			defer wg.Done()
			client := redis.NewClient(&redis.Options{Addr: addr, DialTimeout: 2 * time.Second, ReadTimeout: 2 * time.Second, WriteTimeout: 2 * time.Second, PoolSize: 4})
			defer client.Close()
			for {
				select {
				case <-ctx.Done():
					return
				default:
				}
				next := issued.Add(1)
				if next > uint64(maxRequests) {
					return
				}
				reqStart := time.Now()
				value, err := client.Get(ctx, key).Bytes()
				elapsed := time.Since(reqStart)
				if err != nil {
					failures.Add(1)
					continue
				}
				if !bytes.Equal(value, expected) {
					failures.Add(1)
					valueMismatches.Add(1)
					continue
				}
				successes.Add(1)
				latencies <- elapsed
			}
		}()
	}
	wg.Wait()
	close(latencies)
	elapsed := time.Since(start)

	values := make([]time.Duration, 0, successes.Load())
	for latency := range latencies {
		values = append(values, latency)
	}
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	requests := successes.Load()
	ops := 0.0
	if elapsed > 0 {
		ops = float64(requests) / elapsed.Seconds()
	}
	return benchmarkPhase{
		Name:            name,
		Address:         addr,
		Requests:        requests,
		Failures:        failures.Load(),
		ValueMismatches: valueMismatches.Load(),
		ElapsedSeconds:  elapsed.Seconds(),
		OpsPerSecond:    ops,
		P50Milliseconds: percentileMillis(values, 50),
		P95Milliseconds: percentileMillis(values, 95),
		P99Milliseconds: percentileMillis(values, 99),
	}, nil
}

func readStatusSnapshot(adminURL string) (statusSnapshot, error) {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(strings.TrimRight(adminURL, "/") + "/v1/status")
	if err != nil {
		return statusSnapshot{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return statusSnapshot{}, fmt.Errorf("GET /v1/status returned %s", resp.Status)
	}
	return decodeStatusSnapshot(resp.Body)
}

func decodeStatusSnapshot(r io.Reader) (statusSnapshot, error) {
	data, err := io.ReadAll(io.LimitReader(r, maxStatusResponseBytes+1))
	if err != nil {
		return statusSnapshot{}, err
	}
	if len(data) > maxStatusResponseBytes {
		return statusSnapshot{}, fmt.Errorf("GET /v1/status response exceeds %d bytes", maxStatusResponseBytes)
	}
	var out statusSnapshot
	if err := json.Unmarshal(data, &out); err != nil {
		return statusSnapshot{}, err
	}
	return out, nil
}

func purgeBenchmarkCache(adminURL, key string) error {
	body, err := json.Marshal(map[string]string{"key": key})
	if err != nil {
		return err
	}
	_, err = httpPost(strings.TrimRight(adminURL, "/")+"/v1/cache/purge", body)
	return err
}

func warmSlizenKey(proxyAddr, adminURL, key string, concurrency int, warmup time.Duration) error {
	if warmup == 0 {
		return errors.New("warmup skipped; hot cache state was not verified")
	}
	deadline := time.Now().Add(warmup)
	client := redis.NewClient(&redis.Options{Addr: proxyAddr, DialTimeout: 2 * time.Second, ReadTimeout: 2 * time.Second, WriteTimeout: 2 * time.Second, PoolSize: concurrency + 2})
	defer client.Close()
	for time.Now().Before(deadline) {
		for i := 0; i < maxInt(concurrency, 1); i++ {
			if err := client.Get(context.Background(), key).Err(); err != nil {
				return fmt.Errorf("warmup GET failed: %w", err)
			}
		}
		status, err := readStatusSnapshot(adminURL)
		if err == nil && status.CacheEntries > 0 && status.HotKeys > 0 {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return errors.New("warmup ended before Slizen reported a local hot cache entry")
}

func applyStatusDelta(phase *benchmarkPhase, before, after statusSnapshot) {
	phase.SlizenStatusUpstreamGETs = subtractUint64(after.UpstreamGETsTotal, before.UpstreamGETsTotal)
	phase.CacheHits = subtractUint64(after.CacheHitsTotal, before.CacheHitsTotal)
	phase.CacheMisses = subtractUint64(after.CacheMissesTotal, before.CacheMissesTotal)
	phase.CacheMissesPolicyBypass = subtractUint64(after.CacheMissesPolicyBypass, before.CacheMissesPolicyBypass)
	phase.CacheMissesNotAdmitted = subtractUint64(after.CacheMissesNotAdmitted, before.CacheMissesNotAdmitted)
	phase.CacheMissesNotPresent = subtractUint64(after.CacheMissesNotPresent, before.CacheMissesNotPresent)
	denominator := phase.CacheHits + phase.CacheMisses
	if denominator > 0 {
		phase.CacheHitRatio = 100 * float64(phase.CacheHits) / float64(denominator)
	}
}

func applyHotKeyStatusDelta(phase *benchmarkPhase, before, after statusSnapshot) bool {
	if reason := invalidStatusDeltaReason(before, after); reason != "" {
		phase.SlizenStatusUpstreamGETs = 0
		phase.CacheHits = 0
		phase.CacheMisses = 0
		phase.CacheMissesPolicyBypass = 0
		phase.CacheMissesNotAdmitted = 0
		phase.CacheMissesNotPresent = 0
		phase.CacheHitRatio = 0
		phase.Notes = append(phase.Notes, reason)
		return false
	}

	applyStatusDelta(phase, before, after)
	expectedRequests := phase.Requests + phase.Failures
	observedRequests := after.RequestsTotal - before.RequestsTotal
	evidenceValid := true
	if observedRequests != expectedRequests {
		phase.Notes = append(phase.Notes, fmt.Sprintf(
			"Slizen process-global request counter changed by %d during %d benchmark operations; run Slizen quiescently and exclusively",
			observedRequests,
			expectedRequests,
		))
		evidenceValid = false
	}
	if phase.Failures == 0 && phase.CacheHits+phase.CacheMisses != phase.Requests {
		phase.Notes = append(phase.Notes, fmt.Sprintf(
			"Slizen cache counters recorded %d GET decisions during %d benchmark reads; process-global evidence is not isolated",
			phase.CacheHits+phase.CacheMisses,
			phase.Requests,
		))
		evidenceValid = false
	}
	if phase.Failures == 0 && phase.SlizenStatusUpstreamGETs > phase.Requests {
		phase.Notes = append(phase.Notes, fmt.Sprintf(
			"Slizen upstream GET counter changed by %d during %d benchmark reads; process-global evidence is not isolated",
			phase.SlizenStatusUpstreamGETs,
			phase.Requests,
		))
		evidenceValid = false
	}
	return evidenceValid
}

func applyWorkloadStatusDelta(phase *benchmarkPhase, before, after statusSnapshot) bool {
	if reason := invalidStatusDeltaReason(before, after); reason != "" {
		phase.SlizenStatusUpstreamGETs = 0
		phase.CacheHits = 0
		phase.CacheMisses = 0
		phase.CacheMissesPolicyBypass = 0
		phase.CacheMissesNotAdmitted = 0
		phase.CacheMissesNotPresent = 0
		phase.CacheHitRatio = 0
		phase.Notes = append(phase.Notes, reason)
		return false
	}

	applyStatusDelta(phase, before, after)
	observedRequests := after.RequestsTotal - before.RequestsTotal
	expectedRequests := phase.Requests + phase.Failures
	evidenceValid := true
	if observedRequests != expectedRequests {
		phase.Notes = append(phase.Notes, fmt.Sprintf(
			"Slizen process-global request counter changed by %d during %d benchmark operations; run Slizen quiescently and exclusively",
			observedRequests,
			expectedRequests,
		))
		evidenceValid = false
	}
	if phase.Failures == 0 && phase.CacheHits+phase.CacheMisses != phase.Reads {
		phase.Notes = append(phase.Notes, fmt.Sprintf(
			"Slizen cache counters recorded %d GET decisions during %d benchmark reads; process-global evidence is not isolated",
			phase.CacheHits+phase.CacheMisses,
			phase.Reads,
		))
		evidenceValid = false
	}
	if phase.Failures == 0 && phase.SlizenStatusUpstreamGETs > phase.Reads {
		phase.Notes = append(phase.Notes, fmt.Sprintf(
			"Slizen upstream GET counter changed by %d during %d benchmark reads; process-global evidence is not isolated",
			phase.SlizenStatusUpstreamGETs,
			phase.Reads,
		))
		evidenceValid = false
	}
	return evidenceValid
}

func invalidStatusDeltaReason(before, after statusSnapshot) string {
	if after.Version != before.Version || after.Commit != before.Commit || after.Mode != before.Mode || after.KeyVisibility != before.KeyVisibility {
		return "Slizen status identity or mode changed during the phase; daemon restart or reconfiguration invalidated process-global evidence"
	}
	beforeUptime, beforeErr := time.ParseDuration(before.Uptime)
	afterUptime, afterErr := time.ParseDuration(after.Uptime)
	if beforeErr != nil || afterErr != nil {
		return "Slizen uptime could not be parsed; daemon continuity could not be verified"
	}
	if afterUptime < beforeUptime {
		return "Slizen uptime decreased during the phase; daemon restart invalidated process-global evidence"
	}
	if after.RequestsTotal < before.RequestsTotal ||
		after.CacheHitsTotal < before.CacheHitsTotal ||
		after.CacheMissesTotal < before.CacheMissesTotal ||
		after.CacheMissesPolicyBypass < before.CacheMissesPolicyBypass ||
		after.CacheMissesNotAdmitted < before.CacheMissesNotAdmitted ||
		after.CacheMissesNotPresent < before.CacheMissesNotPresent ||
		after.UpstreamRequestsTotal < before.UpstreamRequestsTotal ||
		after.UpstreamGETsTotal < before.UpstreamGETsTotal ||
		after.CoalescedRequestsTotal < before.CoalescedRequestsTotal ||
		after.InvalidationsTotal < before.InvalidationsTotal ||
		after.PromotionsTotal < before.PromotionsTotal ||
		after.DemotionsTotal < before.DemotionsTotal {
		return "one or more Slizen status counters decreased during the phase; daemon restart or counter reset invalidated process-global evidence"
	}
	return ""
}

func upstreamReduction(origin, hot benchmarkPhase) float64 {
	if origin.Requests == 0 ||
		hot.Requests == 0 ||
		origin.Failures > 0 ||
		hot.Failures > 0 ||
		origin.ValueMismatches > 0 ||
		hot.ValueMismatches > 0 ||
		origin.UpstreamGETsSource != originGETsSourceCommandstats ||
		hot.UpstreamGETsSource != originGETsSourceCommandstats {
		return 0
	}
	originRate := float64(origin.UpstreamGETs) / float64(origin.Requests)
	hotRate := float64(hot.UpstreamGETs) / float64(hot.Requests)
	if originRate <= 0 {
		return 0
	}
	return math.Max(0, 100*(1-hotRate/originRate))
}

func printBenchmarkText(w io.Writer, result benchmarkResult) {
	fmt.Fprintln(w, "Slizen Hot Key Benchmark")
	fmt.Fprintln(w)
	fmt.Fprintf(w, "Key: %s\n", result.Key)
	fmt.Fprintf(w, "Mode: %s\n", result.Mode)
	fmt.Fprintf(w, "Concurrency: %d\n", result.Concurrency)
	fmt.Fprintf(w, "Duration: %.0fs\n", result.DurationSeconds)
	fmt.Fprintln(w)
	fmt.Fprintf(w, "%-20s %10s %8s %8s %8s %14s %12s\n", "Phase", "Ops/sec", "p50", "p95", "p99", "Upstream GETs", "Cache Hit %")
	for _, phase := range result.Phases {
		fmt.Fprintf(w, "%-20s %10.0f %7.2fms %7.2fms %7.2fms %14d %11.1f%%\n",
			phase.Name,
			phase.OpsPerSecond,
			phase.P50Milliseconds,
			phase.P95Milliseconds,
			phase.P99Milliseconds,
			phase.UpstreamGETs,
			phase.CacheHitRatio,
		)
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Result:")
	fmt.Fprintf(w, "upstream_get_reduction: %.1f%%\n", result.UpstreamGetReduction)
	fmt.Fprintf(w, "cache_hit_ratio: %.1f%%\n", result.CacheHitRatio)
	fmt.Fprintf(w, "evidence_valid: %t\n", result.EvidenceValid)
	fmt.Fprintf(w, "proved_reduction: %t\n", result.ProvedReduction)
	for _, note := range result.Notes {
		fmt.Fprintf(w, "note: %s\n", note)
	}
}

func percentileMillis(values []time.Duration, percentile int) float64 {
	if len(values) == 0 {
		return 0
	}
	if percentile <= 0 {
		return float64(values[0].Microseconds()) / 1000
	}
	if percentile >= 100 {
		return float64(values[len(values)-1].Microseconds()) / 1000
	}
	index := int(math.Ceil(float64(percentile)/100*float64(len(values)))) - 1
	if index < 0 {
		index = 0
	}
	if index >= len(values) {
		index = len(values) - 1
	}
	return float64(values[index].Microseconds()) / 1000
}

func latencyDistributionFor(values []time.Duration) latencyDistribution {
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	return latencyDistribution{
		Samples:         uint64(len(values)),
		P50Milliseconds: percentileMillis(values, 50),
		P95Milliseconds: percentileMillis(values, 95),
		P99Milliseconds: percentileMillis(values, 99),
	}
}

func latencyDistributionPointer(values []time.Duration) *latencyDistribution {
	if len(values) == 0 {
		return nil
	}
	distribution := latencyDistributionFor(values)
	return &distribution
}

func latencyDistributionValue(distribution *latencyDistribution) latencyDistribution {
	if distribution == nil {
		return latencyDistribution{}
	}
	return *distribution
}

func subtractUint64(after, before uint64) uint64 {
	if after < before {
		return 0
	}
	return after - before
}

func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}

func maxDuration(a, b time.Duration) time.Duration {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
