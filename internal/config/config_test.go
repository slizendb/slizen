package config

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadConfigAndValidate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "slizen.toml")
	data := []byte(`
[proxy]
listen = "127.0.0.1:6380"
read_timeout = "4s"
write_timeout = "5s"
shutdown_timeout = "6s"

[admin]
listen = "127.0.0.1:9090"
expose_raw_keys = true

[upstream]
address = "127.0.0.1:6379"
database = 0
dial_timeout = "1s"
read_timeout = "2s"
write_timeout = "3s"

[cache]
max_bytes = 1024
max_entries = 10
max_local_ttl = "15s"
allow_stale_on_upstream_error = true
stale_grace = "1s"
negative_ttl = "0s"

[hotness]
window = "1s"
ewma_alpha = 1.0
promotion_threshold = 10
demotion_threshold = 2
minimum_hot_windows = 1
cooldown = "5s"
max_tracked_keys = 100

[privacy]
key_visibility = "hash"
key_hash_secret = "test"

[logging]
level = "debug"
format = "text"
`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Proxy.ReadTimeout != 4*time.Second {
		t.Fatalf("read timeout = %s", cfg.Proxy.ReadTimeout)
	}
	if !cfg.Admin.ExposeRawKeys {
		t.Fatal("expected raw key exposure from config")
	}
	if cfg.Cache.MaxBytes != 1024 {
		t.Fatalf("cache max bytes = %d", cfg.Cache.MaxBytes)
	}
}

func TestEnvironmentOverrides(t *testing.T) {
	t.Setenv("SLIZEN_MODE", "observe")
	t.Setenv("SLIZEN_UPSTREAM_ADDRESS", "redis.internal:6379")
	t.Setenv("SLIZEN_UPSTREAM_USERNAME", "user")
	t.Setenv("SLIZEN_UPSTREAM_PASSWORD", "secret")
	t.Setenv("SLIZEN_KEY_VISIBILITY", "plain")
	t.Setenv("SLIZEN_KEY_HASH_SECRET", "hash-secret")
	t.Setenv("SLIZEN_LOG_LEVEL", "warn")

	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Upstream.Address != "redis.internal:6379" {
		t.Fatalf("address override not applied: %s", cfg.Upstream.Address)
	}
	if cfg.Mode != "observe" {
		t.Fatalf("mode override not applied: %s", cfg.Mode)
	}
	if cfg.Upstream.Username != "user" || cfg.Upstream.Password != "secret" {
		t.Fatal("credential override not applied")
	}
	if cfg.Privacy.KeyVisibility != "plain" || cfg.Privacy.KeyHashSecret != "hash-secret" {
		t.Fatalf("privacy overrides not applied: %+v", cfg.Privacy)
	}
	if cfg.Logging.Level != "warn" {
		t.Fatalf("log level override not applied: %s", cfg.Logging.Level)
	}
}

func TestValidationRejectsBadMode(t *testing.T) {
	cfg := Default()
	cfg.Mode = "mirror"
	if err := Validate(cfg); err == nil {
		t.Fatal("expected validation error")
	}
}

func TestValidationRejectsBadKeyVisibility(t *testing.T) {
	cfg := Default()
	cfg.Privacy.KeyVisibility = "rawish"
	if err := Validate(cfg); err == nil {
		t.Fatal("expected validation error")
	}
}

func TestValidationRejectsMissingHashSecret(t *testing.T) {
	cfg := Default()
	cfg.Privacy.KeyHashSecret = ""
	if err := Validate(cfg); err == nil {
		t.Fatal("expected validation error")
	}
}

func TestEnvironmentOverrideRejectsInvalidBool(t *testing.T) {
	t.Setenv("SLIZEN_ADMIN_EXPOSE_RAW_KEYS", "definitely")

	if _, err := Load(""); err == nil {
		t.Fatal("expected invalid bool env override to fail")
	}
}

func TestValidationRejectsBadThresholds(t *testing.T) {
	cfg := Default()
	cfg.Hotness.PromotionThreshold = 1
	cfg.Hotness.DemotionThreshold = 1
	if err := Validate(cfg); err == nil {
		t.Fatal("expected validation error")
	}
}

func TestLoadPerPrefixCachePolicies(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "slizen.toml")
	data := []byte(`
[[cache.policies]]
prefix = ""
mode = "deny"

[[cache.policies]]
prefix = "catalog:"
mode = "observe"

[[cache.policies]]
prefix = "catalog:featured:"
mode = "cache"
max_item_bytes = 1048576
max_local_ttl = "5s"
`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Cache.Policies) != 3 {
		t.Fatalf("policy count = %d, want 3", len(cfg.Cache.Policies))
	}
	policy := cfg.Cache.Policies[2]
	if policy.Prefix != "catalog:featured:" || policy.Mode != "cache" || policy.MaxItemBytes != 1048576 || policy.MaxLocalTTL != 5*time.Second {
		t.Fatalf("cache policy = %+v", policy)
	}
}

func TestValidationRejectsInvalidPerPrefixPolicies(t *testing.T) {
	tests := []struct {
		name     string
		policies []CachePolicyConfig
	}{
		{name: "unknown mode", policies: []CachePolicyConfig{{Prefix: "x:", Mode: "mirror"}}},
		{name: "duplicate prefix", policies: []CachePolicyConfig{{Prefix: "x:", Mode: "deny"}, {Prefix: "x:", Mode: "observe"}}},
		{name: "cache missing item limit", policies: []CachePolicyConfig{{Prefix: "x:", Mode: "cache", MaxLocalTTL: time.Second}}},
		{name: "cache missing ttl limit", policies: []CachePolicyConfig{{Prefix: "x:", Mode: "cache", MaxItemBytes: 1024}}},
		{name: "item limit above global", policies: []CachePolicyConfig{{Prefix: "x:", Mode: "cache", MaxItemBytes: 65 * 1024 * 1024, MaxLocalTTL: time.Second}}},
		{name: "ttl above global", policies: []CachePolicyConfig{{Prefix: "x:", Mode: "cache", MaxItemBytes: 1024, MaxLocalTTL: time.Minute}}},
		{name: "deny with limits", policies: []CachePolicyConfig{{Prefix: "x:", Mode: "deny", MaxItemBytes: 1024, MaxLocalTTL: time.Second}}},
		{name: "observe with limits", policies: []CachePolicyConfig{{Prefix: "x:", Mode: "observe", MaxItemBytes: 1024, MaxLocalTTL: time.Second}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Default()
			cfg.Cache.Policies = tt.policies
			if err := Validate(cfg); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestValidationBoundsPerPrefixPolicyConfig(t *testing.T) {
	t.Run("policy count", func(t *testing.T) {
		cfg := Default()
		cfg.Cache.Policies = make([]CachePolicyConfig, maxCachePolicies+1)
		for i := range cfg.Cache.Policies {
			cfg.Cache.Policies[i] = CachePolicyConfig{Prefix: fmt.Sprintf("policy:%d:", i), Mode: "deny"}
		}
		err := Validate(cfg)
		if err == nil || !strings.Contains(err.Error(), "cache.policies must contain at most") {
			t.Fatalf("validation error = %v, want policy count limit", err)
		}
	})

	t.Run("individual prefix bytes", func(t *testing.T) {
		cfg := Default()
		cfg.Cache.Policies = []CachePolicyConfig{{Prefix: strings.Repeat("x", maxCachePolicyPrefixBytes+1), Mode: "deny"}}
		err := Validate(cfg)
		if err == nil || !strings.Contains(err.Error(), "prefix must contain at most") {
			t.Fatalf("validation error = %v, want prefix byte limit", err)
		}
	})

	t.Run("total prefix bytes", func(t *testing.T) {
		cfg := Default()
		policyCount := maxCachePolicyTotalPrefixBytes/maxCachePolicyPrefixBytes + 1
		cfg.Cache.Policies = make([]CachePolicyConfig, policyCount)
		for i := range cfg.Cache.Policies {
			marker := fmt.Sprintf("%04d:", i)
			prefix := marker + strings.Repeat("x", maxCachePolicyPrefixBytes-len(marker))
			cfg.Cache.Policies[i] = CachePolicyConfig{Prefix: prefix, Mode: "deny"}
		}
		err := Validate(cfg)
		if err == nil || !strings.Contains(err.Error(), "prefixes must contain at most") {
			t.Fatalf("validation error = %v, want total prefix byte limit", err)
		}
	})
}

func TestLoadPerPrefixPolicyRequiresPrefixAndMode(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "prefix", body: "mode = \"deny\"", want: "cache.policies[0].prefix is required"},
		{name: "mode", body: "prefix = \"catalog:\"", want: "cache.policies[0].mode is required"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "slizen.toml")
			data := []byte("[[cache.policies]]\n" + tt.body + "\n")
			if err := os.WriteFile(path, data, 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := Load(path)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Load error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestGlobalObserveModeAllowsCachePoliciesAsSafetyCeiling(t *testing.T) {
	cfg := Default()
	cfg.Mode = "observe"
	cfg.Cache.Policies = []CachePolicyConfig{{
		Prefix:       "catalog:",
		Mode:         "cache",
		MaxItemBytes: 1024,
		MaxLocalTTL:  time.Second,
	}}
	if err := Validate(cfg); err != nil {
		t.Fatalf("global observe should safely override cache policy: %v", err)
	}
}

func TestPolicyPrefixesAreRedactedFromSummaryAndValidationErrors(t *testing.T) {
	const sensitivePrefix = "customer:secret-tenant:"
	cfg := Default()
	cfg.Cache.Policies = []CachePolicyConfig{
		{Prefix: sensitivePrefix, Mode: "deny"},
		{Prefix: sensitivePrefix, Mode: "observe"},
	}
	err := Validate(cfg)
	if err == nil {
		t.Fatal("expected duplicate policy error")
	}
	if strings.Contains(err.Error(), sensitivePrefix) {
		t.Fatalf("validation error leaked policy prefix: %v", err)
	}
	summary := RedactedSummary(cfg)
	if got := summary["cache_policy_count"]; got != 2 {
		t.Fatalf("cache_policy_count = %#v, want 2", got)
	}
	if strings.Contains(fmt.Sprint(summary), sensitivePrefix) {
		t.Fatal("redacted summary leaked policy prefix")
	}
}

func TestValidateBoundsHotnessTracking(t *testing.T) {
	cfg := Default()
	cfg.Hotness.MaxTrackedKeys = maxHotnessTrackedKeys + 1
	err := Validate(cfg)
	if err == nil || !strings.Contains(err.Error(), "hotness.max_tracked_keys must not exceed") {
		t.Fatalf("validation error = %v, want max tracked key bound", err)
	}
}

func TestValidationRejectsNonFiniteAndNegativeHotnessValues(t *testing.T) {
	tests := []struct {
		name string
		edit func(*Config)
	}{
		{name: "ewma nan", edit: func(cfg *Config) { cfg.Hotness.EWMAAlpha = math.NaN() }},
		{name: "ewma positive infinity", edit: func(cfg *Config) { cfg.Hotness.EWMAAlpha = math.Inf(1) }},
		{name: "ewma negative infinity", edit: func(cfg *Config) { cfg.Hotness.EWMAAlpha = math.Inf(-1) }},
		{name: "promotion nan", edit: func(cfg *Config) { cfg.Hotness.PromotionThreshold = math.NaN() }},
		{name: "promotion positive infinity", edit: func(cfg *Config) { cfg.Hotness.PromotionThreshold = math.Inf(1) }},
		{name: "promotion negative infinity", edit: func(cfg *Config) { cfg.Hotness.PromotionThreshold = math.Inf(-1) }},
		{name: "promotion negative", edit: func(cfg *Config) { cfg.Hotness.PromotionThreshold = -1 }},
		{name: "demotion nan", edit: func(cfg *Config) { cfg.Hotness.DemotionThreshold = math.NaN() }},
		{name: "demotion positive infinity", edit: func(cfg *Config) { cfg.Hotness.DemotionThreshold = math.Inf(1) }},
		{name: "demotion negative infinity", edit: func(cfg *Config) { cfg.Hotness.DemotionThreshold = math.Inf(-1) }},
		{name: "demotion negative", edit: func(cfg *Config) { cfg.Hotness.DemotionThreshold = -1 }},
		{name: "demotion zero", edit: func(cfg *Config) { cfg.Hotness.DemotionThreshold = 0 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Default()
			tt.edit(&cfg)
			if err := Validate(cfg); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestLoadRejectsNonFiniteHotnessValue(t *testing.T) {
	path := filepath.Join(t.TempDir(), "slizen.toml")
	if err := os.WriteFile(path, []byte("[hotness]\newma_alpha = nan\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "hotness.ewma_alpha") {
		t.Fatalf("Load error = %v, want finite EWMA validation error", err)
	}
}
