package main

import (
	"context"
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
	maxWorkloadValueBytes  = 1 << 20
	maxWorkloadDataset     = 256 << 20
	maxWorkloadDuration    = time.Hour
	maxWorkloadKeyPrefix   = 128
	seedPipelineBytes      = 4 << 20
	maxStatusResponseBytes = 64 << 10
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
	ProvedReduction      bool             `json:"proved_reduction"`
	Notes                []string         `json:"notes,omitempty"`
	StatusBefore         statusSnapshot   `json:"status_before"`
	StatusAfter          statusSnapshot   `json:"status_after"`
	RuntimeVersions      runtimeVersions  `json:"runtime_versions"`
}

type benchmarkPhase struct {
	Name            string   `json:"name"`
	Address         string   `json:"address"`
	Requests        uint64   `json:"requests"`
	Reads           uint64   `json:"reads,omitempty"`
	Writes          uint64   `json:"writes,omitempty"`
	Failures        uint64   `json:"failures"`
	ElapsedSeconds  float64  `json:"elapsed_seconds"`
	OpsPerSecond    float64  `json:"ops_per_second"`
	P50Milliseconds float64  `json:"p50_ms"`
	P95Milliseconds float64  `json:"p95_ms"`
	P99Milliseconds float64  `json:"p99_ms"`
	UpstreamGETs    uint64   `json:"upstream_gets"`
	CacheHits       uint64   `json:"cache_hits"`
	CacheMisses     uint64   `json:"cache_misses"`
	CacheHitRatio   float64  `json:"cache_hit_ratio_percent"`
	Notes           []string `json:"notes,omitempty"`
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
	latencies []time.Duration
	reads     uint64
	writes    uint64
	failures  uint64
}

type statusSnapshot struct {
	Version                string `json:"version"`
	Commit                 string `json:"commit,omitempty"`
	Mode                   string `json:"mode"`
	KeyVisibility          string `json:"key_visibility"`
	Uptime                 string `json:"uptime"`
	RequestsTotal          uint64 `json:"requests_total"`
	CacheHitsTotal         uint64 `json:"cache_hits_total"`
	CacheMissesTotal       uint64 `json:"cache_misses_total"`
	UpstreamRequestsTotal  uint64 `json:"upstream_requests_total"`
	UpstreamGETsTotal      uint64 `json:"upstream_gets_total"`
	CoalescedRequestsTotal uint64 `json:"coalesced_requests_total"`
	InvalidationsTotal     uint64 `json:"invalidations_total"`
	PromotionsTotal        uint64 `json:"promotions_total"`
	DemotionsTotal         uint64 `json:"demotions_total"`
	CacheEntries           int    `json:"cache_entries"`
	CacheBytes             int64  `json:"cache_bytes"`
	HotKeys                int    `json:"hot_keys"`
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
	if cfg.ValueSize < 1 || cfg.ValueSize > maxWorkloadValueBytes {
		return fmt.Errorf("value-size must be between 1 and %d bytes", maxWorkloadValueBytes)
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
		},
	}
	if versionErr != nil {
		result.Notes = append(result.Notes, versionErr.Error())
	}
	if status.Mode != "cache" {
		result.Notes = append(result.Notes, "Slizen is not in cache mode; local cache hits and origin GET reduction are not expected")
	}

	value := make([]byte, cfg.ValueSize)
	for i := range value {
		value[i] = byte('a' + i%26)
	}
	for _, scenario := range selectedWorkloadScenarios(cfg.Scenario) {
		keys := buildWorkloadKeys(isolatedKeyPrefix, scenario, cfg.KeyCount)
		if err := seedWorkloadKeys(context.Background(), origin, keys, value); err != nil {
			return workloadBenchmarkResult{}, fmt.Errorf("seed %s workload: %w", scenario, err)
		}

		originPhase, err := runRedisWorkload("origin direct", cfg.OriginAddr, keys, value, scenario, cfg)
		if err != nil {
			return workloadBenchmarkResult{}, fmt.Errorf("run %s origin phase: %w", scenario, err)
		}
		originPhase.UpstreamGETs = originPhase.Reads

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
		slizenPhase, err := runRedisWorkloadWithClients("slizen", cfg.ProxyAddr, proxyClients, keys, value, scenario, cfg)
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
		result.Scenarios = append(result.Scenarios, summarizeWorkloadScenario(scenario, originPhase, slizenPhase, statusEvidenceValid))
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

func seedWorkloadKeys(ctx context.Context, client *redis.Client, keys []string, value []byte) error {
	batchSize := seedPipelineBytes / len(value)
	if batchSize < 1 {
		batchSize = 1
	}
	if batchSize > 1000 {
		batchSize = 1000
	}
	for start := 0; start < len(keys); start += batchSize {
		end := minInt(start+batchSize, len(keys))
		pipe := client.Pipeline()
		for _, key := range keys[start:end] {
			pipe.Set(ctx, key, value, 0)
		}
		if _, err := pipe.Exec(ctx); err != nil {
			return err
		}
	}
	return nil
}

func runRedisWorkload(name, addr string, keys []string, value []byte, scenario string, cfg workloadConfig) (benchmarkPhase, error) {
	clients, err := newWorkloadClients(addr, cfg.Concurrency)
	if err != nil {
		return benchmarkPhase{}, err
	}
	phase, runErr := runRedisWorkloadWithClients(name, addr, clients, keys, value, scenario, cfg)
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

func runRedisWorkloadWithClients(name, addr string, clients []*redis.Client, keys []string, value []byte, scenario string, cfg workloadConfig) (benchmarkPhase, error) {
	if len(clients) != cfg.Concurrency {
		return benchmarkPhase{}, fmt.Errorf("workload client count %d does not match concurrency %d", len(clients), cfg.Concurrency)
	}
	ctx, cancel := context.WithTimeout(context.Background(), cfg.Duration)
	defer cancel()
	var issued atomic.Uint64
	workers := make([]workloadWorkerResult, cfg.Concurrency)
	start := time.Now()

	var wg sync.WaitGroup
	wg.Add(cfg.Concurrency)
	for workerIndex := 0; workerIndex < cfg.Concurrency; workerIndex++ {
		workerIndex := workerIndex
		go func() {
			defer wg.Done()
			client := clients[workerIndex]
			worker := &workers[workerIndex]
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
				if read {
					err = client.Get(ctx, keys[keyIndex]).Err()
				} else {
					err = client.Set(ctx, keys[keyIndex], value, 0).Err()
				}
				elapsed := time.Since(requestStart)
				if err != nil {
					worker.failures++
					if ctx.Err() != nil {
						return
					}
					continue
				}
				worker.latencies = append(worker.latencies, elapsed)
				if read {
					worker.reads++
				} else {
					worker.writes++
				}
			}
		}()
	}
	wg.Wait()
	elapsed := time.Since(start)

	latencies := make([]time.Duration, 0, minInt(cfg.MaxRequests, int(issued.Load())))
	var reads, writes, failures uint64
	for _, worker := range workers {
		latencies = append(latencies, worker.latencies...)
		reads += worker.reads
		writes += worker.writes
		failures += worker.failures
	}
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	requests := reads + writes
	phase := benchmarkPhase{
		Name:            name,
		Address:         addr,
		Requests:        requests,
		Reads:           reads,
		Writes:          writes,
		Failures:        failures,
		ElapsedSeconds:  elapsed.Seconds(),
		P50Milliseconds: percentileMillis(latencies, 50),
		P95Milliseconds: percentileMillis(latencies, 95),
		P99Milliseconds: percentileMillis(latencies, 99),
	}
	if elapsed > 0 {
		phase.OpsPerSecond = float64(requests) / elapsed.Seconds()
	}
	if requests == 0 {
		return phase, errors.New("phase completed without a successful operation")
	}
	return phase, nil
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

func summarizeWorkloadScenario(name string, origin, slizen benchmarkPhase, statusEvidenceValid bool) workloadScenarioResult {
	evidenceValid := origin.Failures == 0 && slizen.Failures == 0 && statusEvidenceValid
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
		ProvedOriginGETReduction:  evidenceValid && reduction > 0 && slizen.CacheHits > 0 && slizen.UpstreamGETs < slizen.Reads,
	}
	if origin.Failures > 0 || slizen.Failures > 0 {
		result.Notes = append(result.Notes, "origin GET reduction was suppressed because one or both phases recorded failed operations")
	}
	if !statusEvidenceValid {
		result.Notes = append(result.Notes, "origin GET reduction was suppressed because Slizen process-global counters did not provide isolated, monotonic evidence")
	}
	if !result.ProvedOriginGETReduction {
		result.Notes = append(result.Notes, "this run did not prove origin GET reduction for the scenario")
	}
	return result
}

func workloadOriginGETReduction(origin, slizen benchmarkPhase) float64 {
	if origin.Failures > 0 || slizen.Failures > 0 || origin.Reads == 0 || slizen.Reads == 0 {
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
	fmt.Fprintf(w, "%-16s %-13s %10s %9s %9s %8s %8s %8s %13s %11s\n", "Scenario", "Phase", "Ops/sec", "Reads", "Writes", "p50", "p95", "p99", "Origin GETs", "Hit rate")
	for _, scenario := range result.Scenarios {
		for _, phase := range []benchmarkPhase{scenario.Origin, scenario.Slizen} {
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
		}
		fmt.Fprintf(w, "  result: origin_get_reduction=%.1f%% cache_hit_ratio=%.1f%% evidence_valid=%t proved=%t\n", scenario.OriginGETReductionPercent, scenario.CacheHitRatioPercent, scenario.EvidenceValid, scenario.ProvedOriginGETReduction)
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

	originPhase, err := runRedisGetLoad("origin direct", originAddr, key, concurrency, requests, duration)
	if err != nil {
		return benchmarkResult{}, err
	}
	originPhase.UpstreamGETs = originPhase.Requests
	result.Phases = append(result.Phases, originPhase)

	if err := purgeBenchmarkCache(adminURL, key); err != nil {
		return benchmarkResult{}, fmt.Errorf("purge benchmark key before cold phase: %w", err)
	}
	coldRequests := minInt(requests, maxInt(concurrency*2, 1))
	coldBefore, err := readStatusSnapshot(adminURL)
	if err != nil {
		return benchmarkResult{}, err
	}
	coldPhase, err := runRedisGetLoad("slizen cold", proxyAddr, key, concurrency, coldRequests, minDuration(duration, maxDuration(500*time.Millisecond, warmup)))
	if err != nil {
		return benchmarkResult{}, err
	}
	coldAfter, err := readStatusSnapshot(adminURL)
	if err != nil {
		return benchmarkResult{}, err
	}
	applyStatusDelta(&coldPhase, coldBefore, coldAfter)
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
	hotPhase, err := runRedisGetLoad("slizen hot", proxyAddr, key, concurrency, requests, duration)
	if err != nil {
		return benchmarkResult{}, err
	}
	hotAfter, err := readStatusSnapshot(adminURL)
	if err != nil {
		return benchmarkResult{}, err
	}
	applyStatusDelta(&hotPhase, hotBefore, hotAfter)
	hotPhase.Notes = append(hotPhase.Notes, warmNotes...)
	result.Phases = append(result.Phases, hotPhase)

	result.CacheHitRatio = hotPhase.CacheHitRatio
	result.UpstreamGetReduction = upstreamReduction(originPhase, hotPhase)
	result.ProvedReduction = result.UpstreamGetReduction > 0 && result.CacheHitRatio > 0 && hotPhase.UpstreamGETs < hotPhase.Requests
	if !result.ProvedReduction {
		result.Notes = append(result.Notes, "benchmark did not prove upstream GET reduction for this run/configuration")
	}
	result.StatusAfter = hotAfter
	result.FinishedAt = time.Now().UTC().Format(time.RFC3339)
	return result, nil
}

func runRedisGetLoad(name, addr, key string, concurrency, maxRequests int, duration time.Duration) (benchmarkPhase, error) {
	ctx, cancel := context.WithTimeout(context.Background(), duration)
	defer cancel()
	var issued atomic.Uint64
	var successes atomic.Uint64
	var failures atomic.Uint64
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
				err := client.Get(ctx, key).Err()
				elapsed := time.Since(reqStart)
				if err != nil {
					failures.Add(1)
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
	phase.UpstreamGETs = subtractUint64(after.UpstreamGETsTotal, before.UpstreamGETsTotal)
	phase.CacheHits = subtractUint64(after.CacheHitsTotal, before.CacheHitsTotal)
	phase.CacheMisses = subtractUint64(after.CacheMissesTotal, before.CacheMissesTotal)
	denominator := phase.CacheHits + phase.CacheMisses
	if denominator > 0 {
		phase.CacheHitRatio = 100 * float64(phase.CacheHits) / float64(denominator)
	}
}

func applyWorkloadStatusDelta(phase *benchmarkPhase, before, after statusSnapshot) bool {
	if reason := invalidStatusDeltaReason(before, after); reason != "" {
		phase.UpstreamGETs = 0
		phase.CacheHits = 0
		phase.CacheMisses = 0
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
	if phase.Failures == 0 && phase.UpstreamGETs > phase.Reads {
		phase.Notes = append(phase.Notes, fmt.Sprintf(
			"Slizen upstream GET counter changed by %d during %d benchmark reads; process-global evidence is not isolated",
			phase.UpstreamGETs,
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
	if origin.Requests == 0 || hot.Requests == 0 {
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
