package service

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/slizendb/slizen/internal/config"
	"github.com/slizendb/slizen/internal/testutil"
)

func BenchmarkMGetTrackedKeyCardinality(b *testing.B) {
	const batchSize = 100
	for _, tracked := range []int{1_000, 100_000} {
		b.Run(strconv.Itoa(tracked), func(b *testing.B) {
			clock := testutil.NewFakeClock(time.Unix(0, 0))
			up := testutil.NewFakeUpstream()
			svc := newTestServiceWithConfig(up, clock, func(cfg *config.Config) {
				cfg.Mode = "observe"
				cfg.Hotness.MaxTrackedKeys = tracked
			})
			keys := make([]string, batchSize)
			for i := 0; i < tracked; i++ {
				key := "key:" + strconv.Itoa(i)
				svc.tracker.Observe(key)
				if i < batchSize {
					keys[i] = key
					up.Put(key, []byte("value"), 0)
				}
			}

			ctx := context.Background()
			b.ReportAllocs()
			for b.Loop() {
				if _, err := svc.MGet(ctx, keys); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
