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
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"
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
}

type benchmarkPhase struct {
	Name            string   `json:"name"`
	Address         string   `json:"address"`
	Requests        uint64   `json:"requests"`
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
	if len(args) == 0 || args[0] != "hotkey" {
		return errors.New("usage: slizenctl benchmark hotkey [flags]")
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
	_ = purgeBenchmarkCache(adminURL, key)

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
	}

	originPhase, err := runRedisGetLoad("origin direct", originAddr, key, concurrency, requests, duration)
	if err != nil {
		return benchmarkResult{}, err
	}
	originPhase.UpstreamGETs = originPhase.Requests
	result.Phases = append(result.Phases, originPhase)

	_ = purgeBenchmarkCache(adminURL, key)
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
	client := &httpClient
	resp, err := client.Get(strings.TrimRight(adminURL, "/") + "/v1/status")
	if err != nil {
		return statusSnapshot{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return statusSnapshot{}, fmt.Errorf("GET /v1/status returned %s", resp.Status)
	}
	var out statusSnapshot
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return statusSnapshot{}, err
	}
	return out, nil
}

var httpClient = httpClientType{Timeout: 5 * time.Second}

type httpClientType struct {
	Timeout time.Duration
}

func (c *httpClientType) Get(url string) (*http.Response, error) {
	client := &http.Client{Timeout: c.Timeout}
	return client.Get(url)
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
	if !result.ProvedReduction {
		fmt.Fprintln(w, "proved_reduction: false")
		for _, note := range result.Notes {
			fmt.Fprintf(w, "note: %s\n", note)
		}
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
