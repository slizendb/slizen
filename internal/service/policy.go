package service

import (
	"github.com/slizendb/slizen/internal/cachepolicy"
	"github.com/slizendb/slizen/internal/config"
)

func newPolicyMatcher(cfg config.Config) *cachepolicy.Matcher {
	fallbackMode, ok := cachepolicy.ParseMode(cfg.Mode)
	if !ok {
		fallbackMode = cachepolicy.ModeDeny
	}
	fallback := cachepolicy.Decision{Mode: fallbackMode}
	if fallbackMode == cachepolicy.ModeCache {
		fallback.MaxItemBytes = cfg.Cache.MaxBytes
		fallback.MaxLocalTTL = cfg.Cache.MaxLocalTTL
	}

	rules := make([]cachepolicy.Rule, len(cfg.Cache.Policies))
	for i, policy := range cfg.Cache.Policies {
		mode, valid := cachepolicy.ParseMode(policy.Mode)
		if !valid {
			mode = cachepolicy.ModeDeny
		}
		if cfg.Mode == "observe" && mode == cachepolicy.ModeCache {
			mode = cachepolicy.ModeObserve
		}
		decision := cachepolicy.Decision{Mode: mode}
		if mode == cachepolicy.ModeCache {
			decision.MaxItemBytes = policy.MaxItemBytes
			decision.MaxLocalTTL = policy.MaxLocalTTL
		}
		rules[i] = cachepolicy.Rule{Prefix: policy.Prefix, Decision: decision}
	}
	return cachepolicy.New(fallback, rules)
}
