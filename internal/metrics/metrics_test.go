package metrics

import (
	"sync"
	"sync/atomic"
	"testing"
)

func TestCommandLabelBoundsUserInput(t *testing.T) {
	tests := map[string]string{
		"GET":       "GET",
		"get":       "GET",
		"MULTI":     "unsafe",
		"BLPOP":     "unsafe",
		"RANDOM123": "unsupported",
		"":          "invalid",
		"UNKNOWN":   "invalid",
	}

	for command, want := range tests {
		if got := commandLabel(command); got != want {
			t.Fatalf("commandLabel(%q) = %q, want %q", command, got, want)
		}
	}
}

func TestAdvanceHighWaterNeverRegressesOrDoubleCounts(t *testing.T) {
	var mark atomic.Uint64
	if delta := advanceHighWater(&mark, 2); delta != 2 {
		t.Fatalf("first delta = %d, want 2", delta)
	}
	if delta := advanceHighWater(&mark, 1); delta != 0 {
		t.Fatalf("out-of-order delta = %d, want 0", delta)
	}
	if delta := advanceHighWater(&mark, 3); delta != 1 {
		t.Fatalf("next delta = %d, want 1", delta)
	}

	mark.Store(0)
	var sum atomic.Uint64
	var wg sync.WaitGroup
	for total := uint64(1); total <= 1000; total++ {
		wg.Add(1)
		go func(value uint64) {
			defer wg.Done()
			sum.Add(advanceHighWater(&mark, value))
		}(total)
	}
	wg.Wait()
	if got := mark.Load(); got != 1000 {
		t.Fatalf("high-water mark = %d, want 1000", got)
	}
	if got := sum.Load(); got != 1000 {
		t.Fatalf("accepted deltas sum = %d, want 1000", got)
	}
}
