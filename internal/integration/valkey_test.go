package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

type slizenEnv struct {
	proxyAddr string
	adminURL  string
	proxy     *redis.Client
	origin    *redis.Client
}

type integrationStatus struct {
	Mode                   string `json:"mode"`
	KeyVisibility          string `json:"key_visibility"`
	CacheEntries           int    `json:"cache_entries"`
	HotKeys                int    `json:"hot_keys"`
	CacheHitsTotal         uint64 `json:"cache_hits_total"`
	CacheMissesTotal       uint64 `json:"cache_misses_total"`
	UpstreamGETsTotal      uint64 `json:"upstream_gets_total"`
	InvalidationsTotal     uint64 `json:"invalidations_total"`
	CoalescedRequestsTotal uint64 `json:"coalesced_requests_total"`
}

func TestRealValkeyRESPBehavior(t *testing.T) {
	requireIntegration(t)
	env := startSlizend(t, "cache")
	ctx := context.Background()
	key := uniqueKey(t, "resp")

	if got, err := env.proxy.Ping(ctx).Result(); err != nil || got != "PONG" {
		t.Fatalf("PING = %q, %v", got, err)
	}
	if err := env.proxy.Set(ctx, key, "one", 0).Err(); err != nil {
		t.Fatal(err)
	}
	if got, err := env.proxy.Get(ctx, key).Result(); err != nil || got != "one" {
		t.Fatalf("GET = %q, %v", got, err)
	}
	if _, err := env.proxy.Get(ctx, key+":missing").Result(); !errors.Is(err, redis.Nil) {
		t.Fatalf("missing GET err = %v, want redis.Nil", err)
	}

	k1, k2, missing := key+":m1", key+":m2", key+":missing"
	if err := env.proxy.Set(ctx, k1, "v1", 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := env.proxy.Set(ctx, k2, "v2", 0).Err(); err != nil {
		t.Fatal(err)
	}
	values, err := env.proxy.MGet(ctx, k1, missing, k2).Result()
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 3 || values[0] != "v1" || values[1] != nil || values[2] != "v2" {
		t.Fatalf("MGET order/value mismatch: %#v", values)
	}

	for _, command := range [][]any{
		{"MULTI"},
		{"EXEC"},
		{"WATCH", key},
		{"SUBSCRIBE", "channel"},
		{"MONITOR"},
		{"BLPOP", key, "1"},
	} {
		err := env.proxy.Do(ctx, command...).Err()
		if err == nil || !strings.Contains(strings.ToLower(err.Error()), "stateful or unsafe") {
			t.Fatalf("%v returned %v, want stateful/unsafe RESP error", command, err)
		}
	}
}

func TestRealValkeyWriteCommandsInvalidateCache(t *testing.T) {
	requireIntegration(t)
	env := startSlizend(t, "cache")
	ctx := context.Background()

	key := uniqueKey(t, "set")
	if err := env.origin.Set(ctx, key, "old", 0).Err(); err != nil {
		t.Fatal(err)
	}
	warmUntilCached(t, env, key)
	before := getStatus(t, env.adminURL)
	if err := env.proxy.Set(ctx, key, "new", 0).Err(); err != nil {
		t.Fatal(err)
	}
	if got, err := env.proxy.Get(ctx, key).Result(); err != nil || got != "new" {
		t.Fatalf("SET invalidation GET = %q, %v", got, err)
	}
	requireInvalidation(t, before, getStatus(t, env.adminURL), "SET")

	key = uniqueKey(t, "del")
	if err := env.origin.Set(ctx, key, "old", 0).Err(); err != nil {
		t.Fatal(err)
	}
	warmUntilCached(t, env, key)
	before = getStatus(t, env.adminURL)
	if err := env.proxy.Del(ctx, key).Err(); err != nil {
		t.Fatal(err)
	}
	if _, err := env.proxy.Get(ctx, key).Result(); !errors.Is(err, redis.Nil) {
		t.Fatalf("DEL invalidation GET err = %v, want redis.Nil", err)
	}
	requireInvalidation(t, before, getStatus(t, env.adminURL), "DEL")

	key = uniqueKey(t, "expire")
	if err := env.origin.Set(ctx, key, "old", 0).Err(); err != nil {
		t.Fatal(err)
	}
	warmUntilCached(t, env, key)
	before = getStatus(t, env.adminURL)
	if err := env.proxy.Expire(ctx, key, time.Minute).Err(); err != nil {
		t.Fatal(err)
	}
	afterExpire := getStatus(t, env.adminURL)
	requireInvalidation(t, before, afterExpire, "EXPIRE")
	if _, err := env.proxy.Get(ctx, key).Result(); err != nil {
		t.Fatal(err)
	}
	if afterGet := getStatus(t, env.adminURL); afterGet.CacheMissesTotal <= afterExpire.CacheMissesTotal {
		t.Fatalf("EXPIRE should invalidate cache and force a miss: before=%+v after=%+v", afterExpire, afterGet)
	}

	key = uniqueKey(t, "pexpire")
	if err := env.origin.Set(ctx, key, "old", 0).Err(); err != nil {
		t.Fatal(err)
	}
	warmUntilCached(t, env, key)
	before = getStatus(t, env.adminURL)
	if err := env.proxy.PExpire(ctx, key, time.Minute).Err(); err != nil {
		t.Fatal(err)
	}
	afterPExpire := getStatus(t, env.adminURL)
	requireInvalidation(t, before, afterPExpire, "PEXPIRE")
}

func TestObserveModeDoesNotCreateCacheHits(t *testing.T) {
	requireIntegration(t)
	env := startSlizend(t, "observe")
	ctx := context.Background()
	key := uniqueKey(t, "observe")
	if err := env.origin.Set(ctx, key, "value", 0).Err(); err != nil {
		t.Fatal(err)
	}
	before := getStatus(t, env.adminURL)
	for i := 0; i < 20; i++ {
		if got, err := env.proxy.Get(ctx, key).Result(); err != nil || got != "value" {
			t.Fatalf("observe GET = %q, %v", got, err)
		}
	}
	after := getStatus(t, env.adminURL)
	if after.CacheHitsTotal != before.CacheHitsTotal {
		t.Fatalf("observe mode created cache hits: before=%+v after=%+v", before, after)
	}
	if after.CacheEntries != 0 {
		t.Fatalf("observe mode stored cache entries: %+v", after)
	}
	if delta := after.UpstreamGETsTotal - before.UpstreamGETsTotal; delta < 20 {
		t.Fatalf("observe mode should forward all GETs, upstream GET delta=%d", delta)
	}
}

func TestCacheModeCreatesHitsAndReducesUpstreamGETs(t *testing.T) {
	requireIntegration(t)
	env := startSlizend(t, "cache")
	ctx := context.Background()
	key := uniqueKey(t, "hot")
	if err := env.origin.Set(ctx, key, "value", 0).Err(); err != nil {
		t.Fatal(err)
	}
	warmUntilCached(t, env, key)

	before := getStatus(t, env.adminURL)
	const reads = 80
	for i := 0; i < reads; i++ {
		if got, err := env.proxy.Get(ctx, key).Result(); err != nil || got != "value" {
			t.Fatalf("hot GET = %q, %v", got, err)
		}
	}
	after := getStatus(t, env.adminURL)
	hits := after.CacheHitsTotal - before.CacheHitsTotal
	upstreamGETs := after.UpstreamGETsTotal - before.UpstreamGETsTotal
	if hits == 0 {
		t.Fatalf("cache mode did not create cache hits: before=%+v after=%+v", before, after)
	}
	if upstreamGETs >= reads {
		t.Fatalf("cache mode did not reduce upstream GETs: reads=%d upstream=%d", reads, upstreamGETs)
	}
}

func TestAdminAndMetricsDoNotLeakRawKeysOrSecrets(t *testing.T) {
	requireIntegration(t)
	env := startSlizend(t, "cache")
	ctx := context.Background()
	key := uniqueKey(t, "product:iphone_17")
	if err := env.origin.Set(ctx, key, "value", 0).Err(); err != nil {
		t.Fatal(err)
	}
	warmUntilCached(t, env, key)

	status := httpGetString(t, env.adminURL+"/v1/status")
	if strings.Contains(status, "integration-secret") {
		t.Fatalf("status leaked secret: %s", status)
	}
	hotkeys := httpGetString(t, env.adminURL+"/v1/hotkeys")
	if strings.Contains(hotkeys, "iphone_17") || strings.Contains(hotkeys, "product:iphone") {
		t.Fatalf("hotkeys leaked raw key: %s", hotkeys)
	}
	if !strings.Contains(hotkeys, "hmac-sha256:") {
		t.Fatalf("hotkeys did not expose HMAC id: %s", hotkeys)
	}
	metrics := httpGetString(t, env.adminURL+"/metrics")
	if strings.Contains(metrics, "iphone_17") || strings.Contains(metrics, "product:iphone") {
		t.Fatalf("metrics leaked raw key: %s", metrics)
	}
}

func requireIntegration(t *testing.T) {
	t.Helper()
	if os.Getenv("SLIZEN_INTEGRATION") != "1" {
		t.Skip("set SLIZEN_INTEGRATION=1 to run real Valkey/slizend integration tests")
	}
}

func startSlizend(t *testing.T, mode string) slizenEnv {
	t.Helper()
	root := repoRoot(t)
	originAddr := getenv("SLIZEN_INTEGRATION_ORIGIN", "127.0.0.1:6379")
	origin := redis.NewClient(&redis.Options{Addr: originAddr, DialTimeout: time.Second, ReadTimeout: time.Second, WriteTimeout: time.Second})
	t.Cleanup(func() { _ = origin.Close() })
	if err := origin.Ping(context.Background()).Err(); err != nil {
		t.Fatalf("integration origin %s is not reachable: %v", originAddr, err)
	}

	proxyAddr := freeAddr(t)
	adminAddr := freeAddr(t)
	cfg := fmt.Sprintf(`
mode = %q

[proxy]
listen = %q
read_timeout = "3s"
write_timeout = "3s"
shutdown_timeout = "5s"

[admin]
listen = %q
expose_raw_keys = false

[upstream]
address = %q
dial_timeout = "1s"
read_timeout = "1s"
write_timeout = "1s"

[cache]
max_bytes = 1048576
max_entries = 1000
max_local_ttl = "30s"
allow_stale_on_upstream_error = false
stale_grace = "0s"
negative_ttl = "0s"

[hotness]
window = "100ms"
ewma_alpha = 1.0
promotion_threshold = 2
demotion_threshold = 0.1
minimum_hot_windows = 1
cooldown = "1s"
max_tracked_keys = 1000

[privacy]
key_visibility = "hash"
key_hash_secret = "integration-secret"

[logging]
level = "warn"
format = "json"
`, mode, proxyAddr, adminAddr, originAddr)
	cfgPath := filepath.Join(t.TempDir(), "slizen.toml")
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}

	var logs bytes.Buffer
	cmd := exec.Command("go", "run", "./cmd/slizend", "--config", cfgPath)
	cmd.Dir = root
	cmd.Stdout = &logs
	cmd.Stderr = &logs
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = cmd.Process.Signal(syscall.SIGTERM)
		}
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			if cmd.Process != nil {
				_ = cmd.Process.Kill()
			}
			<-done
		}
		if t.Failed() {
			t.Logf("slizend logs:\n%s", logs.String())
		}
	})

	adminURL := "http://" + adminAddr
	waitUntil(t, 20*time.Second, func() bool {
		resp, err := http.Get(adminURL + "/readyz")
		if err != nil {
			return false
		}
		defer resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	})

	proxy := redis.NewClient(&redis.Options{Addr: proxyAddr, DialTimeout: time.Second, ReadTimeout: time.Second, WriteTimeout: time.Second, PoolSize: 16})
	t.Cleanup(func() {
		_ = proxy.Close()
	})

	return slizenEnv{proxyAddr: proxyAddr, adminURL: adminURL, proxy: proxy, origin: origin}
}

func warmUntilCached(t *testing.T, env slizenEnv, key string) {
	t.Helper()
	waitUntil(t, 5*time.Second, func() bool {
		if _, err := env.proxy.Get(context.Background(), key).Result(); err != nil {
			t.Fatalf("warm GET failed: %v", err)
		}
		status := getStatus(t, env.adminURL)
		return status.CacheEntries > 0 && status.HotKeys > 0
	})
}

func requireInvalidation(t *testing.T, before, after integrationStatus, command string) {
	t.Helper()
	if after.InvalidationsTotal <= before.InvalidationsTotal {
		t.Fatalf("%s did not increment invalidations: before=%+v after=%+v", command, before, after)
	}
}

func getStatus(t *testing.T, adminURL string) integrationStatus {
	t.Helper()
	resp, err := http.Get(adminURL + "/v1/status")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status returned %s", resp.Status)
	}
	var status integrationStatus
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		t.Fatal(err)
	}
	return status
}

func httpGetString(t *testing.T, url string) string {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("%s returned %s", url, resp.Status)
	}
	var out bytes.Buffer
	if _, err := out.ReadFrom(resp.Body); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

func waitUntil(t *testing.T, timeout time.Duration, ready func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if ready() {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("condition was not met within %s", timeout)
}

func freeAddr(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	return listener.Addr().String()
}

func uniqueKey(t *testing.T, suffix string) string {
	t.Helper()
	clean := strings.NewReplacer("/", "_", " ", "_", ":", "_").Replace(t.Name() + "_" + suffix)
	return fmt.Sprintf("slizen:integration:%s:%d", clean, time.Now().UnixNano())
}

func getenv(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found")
		}
		dir = parent
	}
}
