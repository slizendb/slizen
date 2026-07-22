package hotness

import (
	"strconv"
	"testing"
	"time"

	"github.com/slizendb/slizen/internal/testutil"
)

func BenchmarkHotnessObservation(b *testing.B) {
	const keyCount = 1000
	clock := testutil.NewFakeClock(time.Unix(0, 0))
	tracker := New(Config{
		Window:             time.Second,
		EWMAAlpha:          0.5,
		PromotionThreshold: 100,
		DemotionThreshold:  20,
		MinimumHotWindows:  2,
		Cooldown:           time.Minute,
		MaxTrackedKeys:     keyCount,
		Clock:              clock,
	})
	keys := make([]string, keyCount)
	for i := range keys {
		keys[i] = "key:" + strconv.Itoa(i)
		tracker.Observe(keys[i])
	}

	next := 0
	b.ReportAllocs()
	for b.Loop() {
		tracker.Observe(keys[next])
		next++
		if next == len(keys) {
			next = 0
		}
	}
}

func BenchmarkHotnessStats(b *testing.B) {
	for _, entries := range []int{1, 1_000, 100_000} {
		b.Run(strconv.Itoa(entries), func(b *testing.B) {
			clock := testutil.NewFakeClock(time.Unix(0, 0))
			tracker := New(Config{
				Window:             time.Second,
				EWMAAlpha:          0.5,
				PromotionThreshold: 100,
				DemotionThreshold:  20,
				MinimumHotWindows:  2,
				Cooldown:           time.Minute,
				MaxTrackedKeys:     entries,
				Clock:              clock,
			})
			for i := 0; i < entries; i++ {
				tracker.Observe("key:" + strconv.Itoa(i))
			}

			b.ReportAllocs()
			for b.Loop() {
				_, _ = tracker.Stats()
			}
		})
	}
}

func BenchmarkHotnessObservationAtCapacityChurn(b *testing.B) {
	for _, entries := range []int{1_000, 100_000} {
		b.Run(strconv.Itoa(entries), func(b *testing.B) {
			clock := testutil.NewFakeClock(time.Unix(0, 0))
			tracker := New(Config{
				Window:             time.Second,
				EWMAAlpha:          0.5,
				PromotionThreshold: 100,
				DemotionThreshold:  20,
				MinimumHotWindows:  2,
				Cooldown:           time.Minute,
				MaxTrackedKeys:     entries,
				Clock:              clock,
			})
			for i := 0; i < entries; i++ {
				tracker.Observe("seed:" + strconv.Itoa(i))
			}
			// Cycling through capacity+1 distinct keys guarantees that every
			// observation misses the full tracker and exercises eviction.
			keys := make([]string, entries+1)
			for i := range keys {
				keys[i] = "churn:" + strconv.Itoa(i)
			}

			next := 0
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				tracker.Observe(keys[next])
				next++
				if next == len(keys) {
					next = 0
				}
			}
		})
	}
}
