package service

import (
	"testing"
	"time"

	"github.com/slizendb/slizen/internal/cache"
	"github.com/slizendb/slizen/internal/testutil"
)

func TestSplitCacheTierLimitsPreservesGlobalBounds(t *testing.T) {
	tests := []struct {
		name       string
		bytes      int64
		entries    int
		wantTiered bool
	}{
		{name: "default", bytes: 64 << 20, entries: 100000, wantTiered: true},
		{name: "byte bounded only", bytes: 1024, entries: 0, wantTiered: true},
		{name: "single entry", bytes: 1024, entries: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			limits := splitCacheTierLimits(tt.bytes, tt.entries)
			if limits.probationaryEnabled != tt.wantTiered {
				t.Fatalf("probationary enabled = %t, want %t", limits.probationaryEnabled, tt.wantTiered)
			}
			if limits.protectedBytes+limits.probationaryBytes != tt.bytes {
				t.Fatalf("byte limits = %d + %d, want %d", limits.protectedBytes, limits.probationaryBytes, tt.bytes)
			}
			if tt.entries > 0 && limits.protectedEntries+limits.probationaryEntries != tt.entries {
				t.Fatalf("entry limits = %d + %d, want %d", limits.protectedEntries, limits.probationaryEntries, tt.entries)
			}
		})
	}
}

func TestCombinedCacheStatsReportsBothBoundedTiers(t *testing.T) {
	clock := testutil.NewFakeClock(time.Unix(0, 0))
	protected := cache.New(768, 6, clock)
	probationary := cache.New(256, 2, clock)
	if !protected.Put("hot", []byte("value"), time.Minute) {
		t.Fatal("protected put failed")
	}
	if !probationary.Put("candidate", []byte("value"), time.Minute) {
		t.Fatal("probationary put failed")
	}

	stats := combinedCacheStats(protected.Stats(), probationary)
	if stats.Entries != 2 || stats.MaxBytes != 1024 {
		t.Fatalf("combined stats = %+v", stats)
	}
}
