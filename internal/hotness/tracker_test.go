package hotness

import (
	"testing"
	"time"

	"github.com/slizendb/slizen/internal/testutil"
)

func testTracker(clock *testutil.FakeClock) *Tracker {
	return New(Config{
		Window:             time.Second,
		EWMAAlpha:          1,
		PromotionThreshold: 2,
		DemotionThreshold:  1,
		MinimumHotWindows:  2,
		Cooldown:           2 * time.Second,
		MaxTrackedKeys:     10,
		Clock:              clock,
	})
}

func TestHotnessPromotionHysteresisAndCooldown(t *testing.T) {
	clock := testutil.NewFakeClock(time.Unix(0, 0))
	tracker := testTracker(clock)

	tracker.Observe("k")
	tracker.Observe("k")
	clock.Advance(time.Second)
	transitions := tracker.Advance()
	if len(transitions) != 1 || transitions[0].To != StateWarm {
		t.Fatalf("expected warm transition, got %+v", transitions)
	}
	if tracker.IsHot("k") {
		t.Fatal("minimum hot windows should prevent immediate promotion")
	}

	tracker.Observe("k")
	tracker.Observe("k")
	clock.Advance(time.Second)
	transitions = tracker.Advance()
	if len(transitions) != 1 || transitions[0].To != StateHot {
		t.Fatalf("expected hot transition, got %+v", transitions)
	}
	if !tracker.IsHot("k") {
		t.Fatal("expected hot key")
	}

	clock.Advance(time.Second)
	transitions = tracker.Advance()
	if len(transitions) != 1 || transitions[0].To != StateCooling {
		t.Fatalf("expected cooling transition, got %+v", transitions)
	}
	clock.Advance(2 * time.Second)
	transitions = tracker.Advance()
	if len(transitions) != 1 || transitions[0].To != StateCold {
		t.Fatalf("expected cold transition, got %+v", transitions)
	}
}

func TestHotnessBoundedTracking(t *testing.T) {
	clock := testutil.NewFakeClock(time.Unix(0, 0))
	tracker := New(Config{Window: time.Second, EWMAAlpha: 1, PromotionThreshold: 10, DemotionThreshold: 1, MinimumHotWindows: 1, Cooldown: time.Second, MaxTrackedKeys: 2, Clock: clock})
	tracker.Observe("a")
	tracker.Observe("b")
	tracker.Observe("c")
	tracked, _ := tracker.Stats()
	if tracked != 2 {
		t.Fatalf("tracked keys = %d", tracked)
	}
}
