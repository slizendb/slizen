package config

import (
	"os"
	"path/filepath"
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
key_hash_salt = "test"

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
