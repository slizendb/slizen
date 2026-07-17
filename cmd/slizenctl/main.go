package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/slizendb/slizen/internal/buildinfo"
)

const (
	defaultAdmin              = "http://127.0.0.1:9090"
	maxAdminJSONResponseBytes = 4 << 20
)

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		usage(stderr)
		return errors.New("missing command")
	}
	switch args[0] {
	case "version":
		_, err := fmt.Fprintln(stdout, buildinfo.String())
		return err
	case "healthz":
		return textEndpointCmd(args[1:], stdout, stderr, "/healthz")
	case "readyz":
		return textEndpointCmd(args[1:], stdout, stderr, "/readyz")
	case "status":
		return statusCmd(args[1:], stdout, stderr)
	case "hotkeys":
		return hotkeysCmd(args[1:], stdout, stderr)
	case "audit":
		return auditCmd(args[1:], stdout, stderr)
	case "cache":
		return cacheCmd(args[1:], stdout, stderr)
	case "benchmark":
		return benchmarkCmd(args[1:], stdout, stderr)
	case "demo":
		return demoCmd(args[1:], stdout, stderr)
	default:
		usage(stderr)
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func textEndpointCmd(args []string, stdout, stderr io.Writer, endpoint string) error {
	fs := flag.NewFlagSet(strings.TrimPrefix(endpoint, "/"), flag.ContinueOnError)
	fs.SetOutput(stderr)
	adminURL := fs.String("admin", defaultAdmin, "admin API URL")
	if err := fs.Parse(args); err != nil {
		return err
	}
	body, err := httpGetText(strings.TrimRight(*adminURL, "/") + endpoint)
	if err != nil {
		return err
	}
	_, err = fmt.Fprint(stdout, body)
	return err
}

func statusCmd(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	fs.SetOutput(stderr)
	adminURL := fs.String("admin", defaultAdmin, "admin API URL")
	if err := fs.Parse(args); err != nil {
		return err
	}
	value, err := httpGet(strings.TrimRight(*adminURL, "/") + "/v1/status")
	return printJSON(stdout, value, err)
}

func hotkeysCmd(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("hotkeys", flag.ContinueOnError)
	fs.SetOutput(stderr)
	adminURL := fs.String("admin", defaultAdmin, "admin API URL")
	limit := fs.Int("limit", 20, "maximum hot keys")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *limit < 1 || *limit > 1000 {
		return errors.New("limit must be between 1 and 1000")
	}
	value, err := httpGet(strings.TrimRight(*adminURL, "/") + "/v1/hotkeys?limit=" + strconv.Itoa(*limit))
	return printJSON(stdout, value, err)
}

func cacheCmd(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 || args[0] != "purge" {
		return errors.New("usage: slizenctl cache purge [--key key] [--admin url]")
	}
	fs := flag.NewFlagSet("cache purge", flag.ContinueOnError)
	fs.SetOutput(stderr)
	adminURL := fs.String("admin", defaultAdmin, "admin API URL")
	key := fs.String("key", "", "single key to purge")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	body := []byte(`{}`)
	if *key != "" {
		encoded, err := json.Marshal(map[string]string{"key": *key})
		if err != nil {
			return err
		}
		body = encoded
	}
	value, err := httpPost(strings.TrimRight(*adminURL, "/")+"/v1/cache/purge", body)
	return printJSON(stdout, value, err)
}

func demoCmd(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 || args[0] != "black-friday" {
		return errors.New("usage: slizenctl demo black-friday [flags]")
	}
	fs := flag.NewFlagSet("demo black-friday", flag.ContinueOnError)
	fs.SetOutput(stderr)
	redisAddr := fs.String("redis", "127.0.0.1:6380", "Slizen RESP address")
	adminURL := fs.String("admin", defaultAdmin, "admin API URL")
	key := fs.String("key", "product:iphone_17", "demo key")
	workers := fs.Int("workers", 100, "reader workers")
	duration := fs.Duration("duration", 20*time.Second, "demo duration")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if *workers <= 0 || *workers > 10000 {
		return errors.New("workers must be between 1 and 10000")
	}
	if *duration <= 0 {
		return errors.New("duration must be greater than zero")
	}
	return runBlackFridayDemo(stdout, *redisAddr, *adminURL, *key, *workers, *duration)
}

func runBlackFridayDemo(stdout io.Writer, redisAddr, adminURL, key string, workers int, duration time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), duration)
	defer cancel()

	client := redis.NewClient(&redis.Options{Addr: redisAddr, DialTimeout: 2 * time.Second, ReadTimeout: 2 * time.Second, WriteTimeout: 2 * time.Second})
	defer client.Close()

	if err := client.Set(context.Background(), key, `{"name":"iPhone 17","price":999}`, 0).Err(); err != nil {
		return fmt.Errorf("seed key through Slizen: %w", err)
	}

	fmt.Fprintf(stdout, "black friday demo\nkey: %s\nworkers: %d\nduration: %s\n\n", key, workers, duration)
	fmt.Fprintln(stdout, "second rps state cached hits misses upstream coalesced")

	var total atomic.Uint64
	var failures atomic.Uint64
	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				default:
				}
				if err := client.Get(ctx, key).Err(); err != nil {
					failures.Add(1)
					continue
				}
				total.Add(1)
			}
		}()
	}

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	var previous uint64
	start := time.Now()
	for {
		select {
		case <-ctx.Done():
			wg.Wait()
			status, _ := fetchStatus(adminURL)
			fmt.Fprintf(stdout, "\nsummary total=%d failures=%d elapsed=%s hit_ratio=%.2f\n", total.Load(), failures.Load(), time.Since(start).Round(time.Millisecond), hitRatio(status))
			return nil
		case <-ticker.C:
			current := total.Load()
			rps := current - previous
			previous = current
			status, _ := fetchStatus(adminURL)
			state, cached := fetchKeyState(adminURL)
			fmt.Fprintf(stdout, "%6.0f %3d %5s %6t %4.0f %6.0f %8.0f %9.0f\n",
				time.Since(start).Seconds(),
				rps,
				state,
				cached,
				float64Number(status["cache_hits"]),
				float64Number(status["cache_misses"]),
				float64Number(status["upstream_requests"]),
				float64Number(status["coalesced_requests"]),
			)
		}
	}
}

func httpGetText(url string) (string, error) {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("GET %s returned %s", url, resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1024))
	if err != nil {
		return "", err
	}
	return string(body), nil
}

func httpGet(url string) (any, error) {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("GET %s returned %s", url, resp.Status)
	}
	return decodeBoundedAdminJSON(resp.Body)
}

func httpPost(url string, body []byte) (any, error) {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("POST %s returned %s", url, resp.Status)
	}
	return decodeBoundedAdminJSON(resp.Body)
}

func decodeBoundedAdminJSON(reader io.Reader) (any, error) {
	body, err := io.ReadAll(io.LimitReader(reader, maxAdminJSONResponseBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxAdminJSONResponseBytes {
		return nil, fmt.Errorf("admin JSON response exceeds %d bytes", maxAdminJSONResponseBytes)
	}
	var out any
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func printJSON(stdout io.Writer, value any, err error) error {
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func fetchStatus(adminURL string) (map[string]any, error) {
	value, err := httpGet(strings.TrimRight(adminURL, "/") + "/v1/status")
	if err != nil {
		return map[string]any{}, err
	}
	if m, ok := value.(map[string]any); ok {
		return m, nil
	}
	return map[string]any{}, nil
}

func fetchKeyState(adminURL string) (string, bool) {
	value, err := httpGet(strings.TrimRight(adminURL, "/") + "/v1/hotkeys?limit=1")
	if err != nil {
		return "unknown", false
	}
	root, ok := value.(map[string]any)
	if !ok {
		return "unknown", false
	}
	rawKeys, ok := root["hotkeys"].([]any)
	if !ok || len(rawKeys) == 0 {
		return "COLD", false
	}
	first, ok := rawKeys[0].(map[string]any)
	if !ok {
		return "unknown", false
	}
	state, _ := first["state"].(string)
	cached, _ := first["locally_cached"].(bool)
	if state == "" {
		state = "unknown"
	}
	return state, cached
}

func float64Number(value any) float64 {
	switch v := value.(type) {
	case float64:
		return v
	case int:
		return float64(v)
	case uint64:
		return float64(v)
	default:
		return 0
	}
}

func hitRatio(status map[string]any) float64 {
	hits := float64Number(status["cache_hits"])
	misses := float64Number(status["cache_misses"])
	if hits+misses == 0 {
		return 0
	}
	return hits / (hits + misses)
}

func usage(w io.Writer) {
	fmt.Fprintln(w, "usage: slizenctl version|healthz|readyz|status|hotkeys|audit|cache|benchmark|demo")
}
