package service

import "github.com/slizendb/slizen/internal/cache"

const probationaryCacheShare = 8

type cacheTierLimits struct {
	protectedBytes      int64
	protectedEntries    int
	probationaryBytes   int64
	probationaryEntries int
	probationaryEnabled bool
}

func splitCacheTierLimits(maxBytes int64, maxEntries int) cacheTierLimits {
	limits := cacheTierLimits{
		protectedBytes:   maxBytes,
		protectedEntries: maxEntries,
	}
	if maxBytes < 2 || maxEntries == 1 {
		return limits
	}

	probationaryBytes := maxBytes / probationaryCacheShare
	if probationaryBytes < 1 {
		probationaryBytes = 1
	}
	protectedBytes := maxBytes - probationaryBytes
	if protectedBytes < 1 {
		return limits
	}

	probationaryEntries := 0
	protectedEntries := maxEntries
	if maxEntries > 1 {
		probationaryEntries = maxEntries / probationaryCacheShare
		if probationaryEntries < 1 {
			probationaryEntries = 1
		}
		protectedEntries = maxEntries - probationaryEntries
	}

	return cacheTierLimits{
		protectedBytes:      protectedBytes,
		protectedEntries:    protectedEntries,
		probationaryBytes:   probationaryBytes,
		probationaryEntries: probationaryEntries,
		probationaryEnabled: true,
	}
}

func combinedCacheStats(protected cache.Stats, probationary *cache.Cache) cache.Stats {
	if probationary == nil {
		return protected
	}
	candidate := probationary.Stats()
	return cache.Stats{
		Entries:   protected.Entries + candidate.Entries,
		Bytes:     protected.Bytes + candidate.Bytes,
		MaxBytes:  protected.MaxBytes + candidate.MaxBytes,
		Evictions: protected.Evictions + candidate.Evictions,
	}
}
