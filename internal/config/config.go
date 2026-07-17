package config

import (
	"errors"
	"fmt"
	"math"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"

	"github.com/slizendb/slizen/internal/cachepolicy"
)

const (
	maxCachePolicies               = 1024
	maxCachePolicyPrefixBytes      = 1024
	maxCachePolicyTotalPrefixBytes = 256 * 1024
	maxHotnessTrackedKeys          = 100000
)

type Config struct {
	Mode     string         `toml:"mode"`
	Proxy    ProxyConfig    `toml:"proxy"`
	Admin    AdminConfig    `toml:"admin"`
	Upstream UpstreamConfig `toml:"upstream"`
	Cache    CacheConfig    `toml:"cache"`
	Hotness  HotnessConfig  `toml:"hotness"`
	Privacy  PrivacyConfig  `toml:"privacy"`
	Logging  LoggingConfig  `toml:"logging"`
}

type ProxyConfig struct {
	Listen          string        `toml:"listen"`
	ReadTimeout     time.Duration `toml:"read_timeout"`
	WriteTimeout    time.Duration `toml:"write_timeout"`
	ShutdownTimeout time.Duration `toml:"shutdown_timeout"`
}

type AdminConfig struct {
	Listen        string `toml:"listen"`
	ExposeRawKeys bool   `toml:"expose_raw_keys"`
}

type UpstreamConfig struct {
	Address      string        `toml:"address"`
	Username     string        `toml:"username"`
	Password     string        `toml:"password"`
	Database     int           `toml:"database"`
	DialTimeout  time.Duration `toml:"dial_timeout"`
	ReadTimeout  time.Duration `toml:"read_timeout"`
	WriteTimeout time.Duration `toml:"write_timeout"`
}

type CacheConfig struct {
	MaxBytes                  int64               `toml:"max_bytes"`
	MaxEntries                int                 `toml:"max_entries"`
	MaxLocalTTL               time.Duration       `toml:"max_local_ttl"`
	AllowStaleOnUpstreamError bool                `toml:"allow_stale_on_upstream_error"`
	StaleGrace                time.Duration       `toml:"stale_grace"`
	NegativeTTL               time.Duration       `toml:"negative_ttl"`
	Policies                  []CachePolicyConfig `toml:"policies"`
}

type CachePolicyConfig struct {
	Prefix       string        `toml:"prefix"`
	Mode         string        `toml:"mode"`
	MaxItemBytes int64         `toml:"max_item_bytes"`
	MaxLocalTTL  time.Duration `toml:"max_local_ttl"`
}

type HotnessConfig struct {
	Window             time.Duration `toml:"window"`
	EWMAAlpha          float64       `toml:"ewma_alpha"`
	PromotionThreshold float64       `toml:"promotion_threshold"`
	DemotionThreshold  float64       `toml:"demotion_threshold"`
	MinimumHotWindows  int           `toml:"minimum_hot_windows"`
	Cooldown           time.Duration `toml:"cooldown"`
	MaxTrackedKeys     int           `toml:"max_tracked_keys"`
}

type PrivacyConfig struct {
	KeyVisibility string `toml:"key_visibility"`
	KeyHashSecret string `toml:"key_hash_secret"`
	KeyHashSalt   string `toml:"key_hash_salt"`
}

type LoggingConfig struct {
	Level  string `toml:"level"`
	Format string `toml:"format"`
}

// Default returns the documented defaults.
func Default() Config {
	return Config{
		Mode: "cache",
		Proxy: ProxyConfig{
			Listen:          "0.0.0.0:6380",
			ReadTimeout:     3 * time.Second,
			WriteTimeout:    3 * time.Second,
			ShutdownTimeout: 10 * time.Second,
		},
		Admin: AdminConfig{
			Listen:        "127.0.0.1:9090",
			ExposeRawKeys: false,
		},
		Upstream: UpstreamConfig{
			Address:      "127.0.0.1:6379",
			Database:     0,
			DialTimeout:  2 * time.Second,
			ReadTimeout:  2 * time.Second,
			WriteTimeout: 2 * time.Second,
		},
		Cache: CacheConfig{
			MaxBytes:                  64 * 1024 * 1024,
			MaxEntries:                100000,
			MaxLocalTTL:               30 * time.Second,
			AllowStaleOnUpstreamError: false,
			StaleGrace:                0,
			NegativeTTL:               0,
		},
		Hotness: HotnessConfig{
			Window:             time.Second,
			EWMAAlpha:          0.5,
			PromotionThreshold: 100,
			DemotionThreshold:  20,
			MinimumHotWindows:  2,
			Cooldown:           30 * time.Second,
			MaxTrackedKeys:     100000,
		},
		Privacy: PrivacyConfig{
			KeyVisibility: "hash",
			KeyHashSecret: "change-me",
			KeyHashSalt:   "change-me",
		},
		Logging: LoggingConfig{
			Level:  "info",
			Format: "json",
		},
	}
}

// Load reads a TOML configuration file, applies environment overrides, and
// validates the result. An empty path uses defaults plus environment overrides.
func Load(path string) (Config, error) {
	cfg := Default()
	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return Config{}, fmt.Errorf("read config: %w", err)
		}
		var raw rawConfig
		if err := toml.Unmarshal(data, &raw); err != nil {
			return Config{}, fmt.Errorf("parse config: %w", err)
		}
		if err := applyRaw(&cfg, raw); err != nil {
			return Config{}, err
		}
	}
	if err := applyEnv(&cfg); err != nil {
		return Config{}, err
	}
	if err := Validate(cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

type rawConfig struct {
	Mode     *string     `toml:"mode"`
	Proxy    rawProxy    `toml:"proxy"`
	Admin    rawAdmin    `toml:"admin"`
	Upstream rawUpstream `toml:"upstream"`
	Cache    rawCache    `toml:"cache"`
	Hotness  rawHotness  `toml:"hotness"`
	Privacy  rawPrivacy  `toml:"privacy"`
	Logging  rawLogging  `toml:"logging"`
}

type rawProxy struct {
	Listen          *string `toml:"listen"`
	ReadTimeout     *string `toml:"read_timeout"`
	WriteTimeout    *string `toml:"write_timeout"`
	ShutdownTimeout *string `toml:"shutdown_timeout"`
}

type rawAdmin struct {
	Listen        *string `toml:"listen"`
	ExposeRawKeys *bool   `toml:"expose_raw_keys"`
}

type rawUpstream struct {
	Address      *string `toml:"address"`
	Username     *string `toml:"username"`
	Password     *string `toml:"password"`
	Database     *int    `toml:"database"`
	DialTimeout  *string `toml:"dial_timeout"`
	ReadTimeout  *string `toml:"read_timeout"`
	WriteTimeout *string `toml:"write_timeout"`
}

type rawCache struct {
	MaxBytes                  *int64           `toml:"max_bytes"`
	MaxEntries                *int             `toml:"max_entries"`
	MaxLocalTTL               *string          `toml:"max_local_ttl"`
	AllowStaleOnUpstreamError *bool            `toml:"allow_stale_on_upstream_error"`
	StaleGrace                *string          `toml:"stale_grace"`
	NegativeTTL               *string          `toml:"negative_ttl"`
	Policies                  []rawCachePolicy `toml:"policies"`
}

type rawCachePolicy struct {
	Prefix       *string `toml:"prefix"`
	Mode         *string `toml:"mode"`
	MaxItemBytes *int64  `toml:"max_item_bytes"`
	MaxLocalTTL  *string `toml:"max_local_ttl"`
}

type rawHotness struct {
	Window             *string  `toml:"window"`
	EWMAAlpha          *float64 `toml:"ewma_alpha"`
	PromotionThreshold *float64 `toml:"promotion_threshold"`
	DemotionThreshold  *float64 `toml:"demotion_threshold"`
	MinimumHotWindows  *int     `toml:"minimum_hot_windows"`
	Cooldown           *string  `toml:"cooldown"`
	MaxTrackedKeys     *int     `toml:"max_tracked_keys"`
}

type rawPrivacy struct {
	KeyVisibility *string `toml:"key_visibility"`
	KeyHashSecret *string `toml:"key_hash_secret"`
	KeyHashSalt   *string `toml:"key_hash_salt"`
}

type rawLogging struct {
	Level  *string `toml:"level"`
	Format *string `toml:"format"`
}

func applyRaw(cfg *Config, raw rawConfig) error {
	if raw.Mode != nil {
		cfg.Mode = *raw.Mode
	}
	if raw.Proxy.Listen != nil {
		cfg.Proxy.Listen = *raw.Proxy.Listen
	}
	if err := setDuration(raw.Proxy.ReadTimeout, &cfg.Proxy.ReadTimeout, "proxy.read_timeout"); err != nil {
		return err
	}
	if err := setDuration(raw.Proxy.WriteTimeout, &cfg.Proxy.WriteTimeout, "proxy.write_timeout"); err != nil {
		return err
	}
	if err := setDuration(raw.Proxy.ShutdownTimeout, &cfg.Proxy.ShutdownTimeout, "proxy.shutdown_timeout"); err != nil {
		return err
	}
	if raw.Admin.Listen != nil {
		cfg.Admin.Listen = *raw.Admin.Listen
	}
	if raw.Admin.ExposeRawKeys != nil {
		cfg.Admin.ExposeRawKeys = *raw.Admin.ExposeRawKeys
	}
	if raw.Upstream.Address != nil {
		cfg.Upstream.Address = *raw.Upstream.Address
	}
	if raw.Upstream.Username != nil {
		cfg.Upstream.Username = *raw.Upstream.Username
	}
	if raw.Upstream.Password != nil {
		cfg.Upstream.Password = *raw.Upstream.Password
	}
	if raw.Upstream.Database != nil {
		cfg.Upstream.Database = *raw.Upstream.Database
	}
	if err := setDuration(raw.Upstream.DialTimeout, &cfg.Upstream.DialTimeout, "upstream.dial_timeout"); err != nil {
		return err
	}
	if err := setDuration(raw.Upstream.ReadTimeout, &cfg.Upstream.ReadTimeout, "upstream.read_timeout"); err != nil {
		return err
	}
	if err := setDuration(raw.Upstream.WriteTimeout, &cfg.Upstream.WriteTimeout, "upstream.write_timeout"); err != nil {
		return err
	}
	if raw.Cache.MaxBytes != nil {
		cfg.Cache.MaxBytes = *raw.Cache.MaxBytes
	}
	if raw.Cache.MaxEntries != nil {
		cfg.Cache.MaxEntries = *raw.Cache.MaxEntries
	}
	if raw.Cache.AllowStaleOnUpstreamError != nil {
		cfg.Cache.AllowStaleOnUpstreamError = *raw.Cache.AllowStaleOnUpstreamError
	}
	if err := setDuration(raw.Cache.MaxLocalTTL, &cfg.Cache.MaxLocalTTL, "cache.max_local_ttl"); err != nil {
		return err
	}
	if err := setDuration(raw.Cache.StaleGrace, &cfg.Cache.StaleGrace, "cache.stale_grace"); err != nil {
		return err
	}
	if err := setDuration(raw.Cache.NegativeTTL, &cfg.Cache.NegativeTTL, "cache.negative_ttl"); err != nil {
		return err
	}
	if raw.Cache.Policies != nil {
		if len(raw.Cache.Policies) > maxCachePolicies {
			return fmt.Errorf("cache.policies must contain at most %d entries", maxCachePolicies)
		}
		cfg.Cache.Policies = make([]CachePolicyConfig, len(raw.Cache.Policies))
		for i, rawPolicy := range raw.Cache.Policies {
			path := fmt.Sprintf("cache.policies[%d]", i)
			if rawPolicy.Prefix == nil {
				return fmt.Errorf("%s.prefix is required", path)
			}
			if rawPolicy.Mode == nil {
				return fmt.Errorf("%s.mode is required", path)
			}
			policy := CachePolicyConfig{Prefix: *rawPolicy.Prefix, Mode: *rawPolicy.Mode}
			if rawPolicy.MaxItemBytes != nil {
				policy.MaxItemBytes = *rawPolicy.MaxItemBytes
			}
			if err := setDuration(rawPolicy.MaxLocalTTL, &policy.MaxLocalTTL, path+".max_local_ttl"); err != nil {
				return err
			}
			cfg.Cache.Policies[i] = policy
		}
	}
	if err := setDuration(raw.Hotness.Window, &cfg.Hotness.Window, "hotness.window"); err != nil {
		return err
	}
	if raw.Hotness.EWMAAlpha != nil {
		cfg.Hotness.EWMAAlpha = *raw.Hotness.EWMAAlpha
	}
	if raw.Hotness.PromotionThreshold != nil {
		cfg.Hotness.PromotionThreshold = *raw.Hotness.PromotionThreshold
	}
	if raw.Hotness.DemotionThreshold != nil {
		cfg.Hotness.DemotionThreshold = *raw.Hotness.DemotionThreshold
	}
	if raw.Hotness.MinimumHotWindows != nil {
		cfg.Hotness.MinimumHotWindows = *raw.Hotness.MinimumHotWindows
	}
	if err := setDuration(raw.Hotness.Cooldown, &cfg.Hotness.Cooldown, "hotness.cooldown"); err != nil {
		return err
	}
	if raw.Hotness.MaxTrackedKeys != nil {
		cfg.Hotness.MaxTrackedKeys = *raw.Hotness.MaxTrackedKeys
	}
	if raw.Privacy.KeyVisibility != nil {
		cfg.Privacy.KeyVisibility = *raw.Privacy.KeyVisibility
	}
	if raw.Privacy.KeyHashSecret != nil {
		cfg.Privacy.KeyHashSecret = *raw.Privacy.KeyHashSecret
	}
	if raw.Privacy.KeyHashSalt != nil {
		if raw.Privacy.KeyHashSecret == nil {
			cfg.Privacy.KeyHashSecret = *raw.Privacy.KeyHashSalt
		}
		cfg.Privacy.KeyHashSalt = *raw.Privacy.KeyHashSalt
	}
	if raw.Logging.Level != nil {
		cfg.Logging.Level = *raw.Logging.Level
	}
	if raw.Logging.Format != nil {
		cfg.Logging.Format = *raw.Logging.Format
	}
	return nil
}

func setDuration(raw *string, target *time.Duration, name string) error {
	if raw == nil {
		return nil
	}
	parsed, err := time.ParseDuration(*raw)
	if err != nil {
		return fmt.Errorf("%s is invalid: %w", name, err)
	}
	*target = parsed
	return nil
}

func applyEnv(cfg *Config) error {
	setString := func(name string, target *string) {
		if value := strings.TrimSpace(os.Getenv(name)); value != "" {
			*target = value
		}
	}
	setBool := func(name string, target *bool) error {
		if value := strings.TrimSpace(os.Getenv(name)); value != "" {
			parsed, err := strconv.ParseBool(value)
			if err != nil {
				return fmt.Errorf("%s is invalid: %w", name, err)
			}
			*target = parsed
		}
		return nil
	}

	setString("SLIZEN_PROXY_LISTEN", &cfg.Proxy.Listen)
	setString("SLIZEN_MODE", &cfg.Mode)
	setString("SLIZEN_ADMIN_LISTEN", &cfg.Admin.Listen)
	if err := setBool("SLIZEN_ADMIN_EXPOSE_RAW_KEYS", &cfg.Admin.ExposeRawKeys); err != nil {
		return err
	}
	setString("SLIZEN_UPSTREAM_ADDRESS", &cfg.Upstream.Address)
	setString("SLIZEN_UPSTREAM_USERNAME", &cfg.Upstream.Username)
	setString("SLIZEN_UPSTREAM_PASSWORD", &cfg.Upstream.Password)
	setString("SLIZEN_KEY_VISIBILITY", &cfg.Privacy.KeyVisibility)
	setString("SLIZEN_KEY_HASH_SECRET", &cfg.Privacy.KeyHashSecret)
	setString("SLIZEN_LOG_LEVEL", &cfg.Logging.Level)
	return nil
}

func Validate(cfg Config) error {
	var errs []error

	checkAddress := func(name, value string) {
		if strings.TrimSpace(value) == "" {
			errs = append(errs, fmt.Errorf("%s address is required", name))
			return
		}
		host, port, err := net.SplitHostPort(value)
		if err != nil || host == "" || port == "" {
			errs = append(errs, fmt.Errorf("%s must be host:port", name))
		}
	}

	checkAddress("proxy.listen", cfg.Proxy.Listen)
	checkAddress("admin.listen", cfg.Admin.Listen)
	checkAddress("upstream.address", cfg.Upstream.Address)

	switch cfg.Mode {
	case "cache", "observe":
	default:
		errs = append(errs, errors.New("mode must be cache or observe"))
	}

	positiveDuration := func(name string, d time.Duration) {
		if d <= 0 {
			errs = append(errs, fmt.Errorf("%s must be greater than zero", name))
		}
	}
	nonNegativeDuration := func(name string, d time.Duration) {
		if d < 0 {
			errs = append(errs, fmt.Errorf("%s must be non-negative", name))
		}
	}

	positiveDuration("proxy.read_timeout", cfg.Proxy.ReadTimeout)
	positiveDuration("proxy.write_timeout", cfg.Proxy.WriteTimeout)
	positiveDuration("proxy.shutdown_timeout", cfg.Proxy.ShutdownTimeout)
	positiveDuration("upstream.dial_timeout", cfg.Upstream.DialTimeout)
	positiveDuration("upstream.read_timeout", cfg.Upstream.ReadTimeout)
	positiveDuration("upstream.write_timeout", cfg.Upstream.WriteTimeout)
	positiveDuration("cache.max_local_ttl", cfg.Cache.MaxLocalTTL)
	nonNegativeDuration("cache.stale_grace", cfg.Cache.StaleGrace)
	nonNegativeDuration("cache.negative_ttl", cfg.Cache.NegativeTTL)
	positiveDuration("hotness.window", cfg.Hotness.Window)
	positiveDuration("hotness.cooldown", cfg.Hotness.Cooldown)

	if cfg.Upstream.Database != 0 {
		errs = append(errs, errors.New("upstream.database must be 0"))
	}
	if cfg.Cache.MaxBytes <= 0 {
		errs = append(errs, errors.New("cache.max_bytes must be greater than zero"))
	}
	if cfg.Cache.MaxEntries < 0 {
		errs = append(errs, errors.New("cache.max_entries must be non-negative"))
	}
	policies := cfg.Cache.Policies
	if len(policies) > maxCachePolicies {
		errs = append(errs, fmt.Errorf("cache.policies must contain at most %d entries", maxCachePolicies))
		policies = policies[:maxCachePolicies]
	}
	seenPolicyPrefixes := make(map[string]int, len(policies))
	totalPrefixBytes := 0
	for i, policy := range policies {
		path := fmt.Sprintf("cache.policies[%d]", i)
		prefixBytes := len(policy.Prefix)
		if prefixBytes > maxCachePolicyPrefixBytes {
			errs = append(errs, fmt.Errorf("%s.prefix must contain at most %d bytes", path, maxCachePolicyPrefixBytes))
		}
		if totalPrefixBytes <= maxCachePolicyTotalPrefixBytes {
			remaining := maxCachePolicyTotalPrefixBytes - totalPrefixBytes
			if prefixBytes > remaining {
				totalPrefixBytes = maxCachePolicyTotalPrefixBytes + 1
			} else {
				totalPrefixBytes += prefixBytes
			}
		}
		if _, duplicate := seenPolicyPrefixes[policy.Prefix]; duplicate {
			errs = append(errs, fmt.Errorf("%s.prefix duplicates another policy", path))
		} else {
			seenPolicyPrefixes[policy.Prefix] = i
		}
		mode, validMode := cachepolicy.ParseMode(policy.Mode)
		if !validMode {
			errs = append(errs, fmt.Errorf("%s.mode must be deny, observe, or cache", path))
			continue
		}
		if mode == cachepolicy.ModeCache {
			if policy.MaxItemBytes <= 0 {
				errs = append(errs, fmt.Errorf("%s.max_item_bytes must be greater than zero in cache mode", path))
			} else if policy.MaxItemBytes > cfg.Cache.MaxBytes {
				errs = append(errs, fmt.Errorf("%s.max_item_bytes must not exceed cache.max_bytes", path))
			}
			if policy.MaxLocalTTL <= 0 {
				errs = append(errs, fmt.Errorf("%s.max_local_ttl must be greater than zero in cache mode", path))
			} else if policy.MaxLocalTTL > cfg.Cache.MaxLocalTTL {
				errs = append(errs, fmt.Errorf("%s.max_local_ttl must not exceed cache.max_local_ttl", path))
			}
			continue
		}
		if policy.MaxItemBytes != 0 || policy.MaxLocalTTL != 0 {
			errs = append(errs, fmt.Errorf("%s cache limits must be omitted outside cache mode", path))
		}
	}
	if totalPrefixBytes > maxCachePolicyTotalPrefixBytes {
		errs = append(errs, fmt.Errorf("cache.policies prefixes must contain at most %d bytes in total", maxCachePolicyTotalPrefixBytes))
	}
	if cfg.Hotness.MaxTrackedKeys <= 0 {
		errs = append(errs, errors.New("hotness.max_tracked_keys must be greater than zero"))
	} else if cfg.Hotness.MaxTrackedKeys > maxHotnessTrackedKeys {
		errs = append(errs, fmt.Errorf("hotness.max_tracked_keys must not exceed %d", maxHotnessTrackedKeys))
	}
	if !finiteFloat(cfg.Hotness.EWMAAlpha) || cfg.Hotness.EWMAAlpha <= 0 || cfg.Hotness.EWMAAlpha > 1 {
		errs = append(errs, errors.New("hotness.ewma_alpha must be greater than 0 and at most 1"))
	}
	promotionValid := finiteFloat(cfg.Hotness.PromotionThreshold) && cfg.Hotness.PromotionThreshold > 0
	if !promotionValid {
		errs = append(errs, errors.New("hotness.promotion_threshold must be finite and greater than zero"))
	}
	demotionValid := finiteFloat(cfg.Hotness.DemotionThreshold) && cfg.Hotness.DemotionThreshold > 0
	if !demotionValid {
		errs = append(errs, errors.New("hotness.demotion_threshold must be finite and greater than zero"))
	}
	if promotionValid && demotionValid && cfg.Hotness.PromotionThreshold <= cfg.Hotness.DemotionThreshold {
		errs = append(errs, errors.New("hotness.promotion_threshold must be greater than demotion_threshold"))
	}
	if cfg.Hotness.MinimumHotWindows <= 0 {
		errs = append(errs, errors.New("hotness.minimum_hot_windows must be greater than zero"))
	}
	switch cfg.Privacy.KeyVisibility {
	case "hash", "plain":
	default:
		errs = append(errs, errors.New("privacy.key_visibility must be hash or plain"))
	}
	if cfg.Privacy.KeyVisibility == "hash" && strings.TrimSpace(cfg.Privacy.KeyHashSecret) == "" {
		errs = append(errs, errors.New("privacy.key_hash_secret is required when key_visibility is hash"))
	}
	switch cfg.Logging.Level {
	case "debug", "info", "warn", "error":
	default:
		errs = append(errs, errors.New("logging.level must be debug, info, warn, or error"))
	}
	switch cfg.Logging.Format {
	case "json", "text":
	default:
		errs = append(errs, errors.New("logging.format must be json or text"))
	}

	return errors.Join(errs...)
}

func finiteFloat(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}

// RedactedSummary returns values safe for startup logs.
func RedactedSummary(cfg Config) map[string]any {
	return map[string]any{
		"mode":               cfg.Mode,
		"proxy_listen":       cfg.Proxy.Listen,
		"admin_listen":       cfg.Admin.Listen,
		"upstream_address":   cfg.Upstream.Address,
		"upstream_username":  redactPresence(cfg.Upstream.Username),
		"upstream_password":  redactPresence(cfg.Upstream.Password),
		"upstream_database":  cfg.Upstream.Database,
		"key_visibility":     EffectiveKeyVisibility(cfg),
		"key_hash_secret":    redactPresence(cfg.Privacy.KeyHashSecret),
		"cache_max_bytes":    cfg.Cache.MaxBytes,
		"cache_max_entries":  cfg.Cache.MaxEntries,
		"cache_max_ttl":      cfg.Cache.MaxLocalTTL.String(),
		"cache_policy_count": len(cfg.Cache.Policies),
		"hotness_window":     cfg.Hotness.Window.String(),
		"log_level":          cfg.Logging.Level,
	}
}

// EffectiveKeyVisibility returns the admin-visible key identity mode.
func EffectiveKeyVisibility(cfg Config) string {
	if cfg.Admin.ExposeRawKeys {
		return "plain"
	}
	return cfg.Privacy.KeyVisibility
}

func redactPresence(value string) string {
	if value == "" {
		return "unset"
	}
	return "set"
}
