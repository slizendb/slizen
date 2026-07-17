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
