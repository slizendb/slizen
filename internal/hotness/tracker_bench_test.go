package hotness

import (
	"strconv"
	"testing"
	"time"
)

func BenchmarkHotnessObservation(b *testing.B) {
	tracker := New(Config{Window: time.Second, EWMAAlpha: 0.5, PromotionThreshold: 100, DemotionThreshold: 20, MinimumHotWindows: 2, Cooldown: time.Minute, MaxTrackedKeys: 100000})
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		tracker.Observe("key:" + strconv.Itoa(i%1000))
	}
}
