package hotness

import (
	"reflect"
	"strings"
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
	if evictions := tracker.Evictions(); evictions != 1 {
		t.Fatalf("tracking evictions = %d, want 1", evictions)
	}
}

func TestTrackingEvictionUsesDeterministicAdmissionFIFO(t *testing.T) {
	clock := testutil.NewFakeClock(time.Unix(0, 0))
	tracker := New(Config{Window: time.Second, EWMAAlpha: 1, PromotionThreshold: 10, DemotionThreshold: 1, MinimumHotWindows: 1, Cooldown: time.Second, MaxTrackedKeys: 2, Clock: clock})

	tracker.Observe("a")
	tracker.Observe("b")
	tracker.Observe("a") // Re-observation does not change admission order.
	tracker.Observe("c")
	if _, ok := tracker.items["a"]; ok {
		t.Fatal("oldest admission a was not evicted")
	}
	if _, ok := tracker.items["b"]; !ok {
		t.Fatal("newer admission b was evicted before a")
	}

	tracker.Observe("d")
	if _, ok := tracker.items["b"]; ok {
		t.Fatal("second-oldest admission b was not evicted next")
	}
	if _, ok := tracker.items["c"]; !ok {
		t.Fatal("replacement c was evicted before b")
	}
	if _, ok := tracker.items["d"]; !ok {
		t.Fatal("latest admission d is not tracked")
	}
	if tracked, hot := tracker.Stats(); tracked != 2 || hot != 0 {
		t.Fatalf("stats after FIFO churn = tracked:%d hot:%d, want 2/0", tracked, hot)
	}
	if evictions := tracker.Evictions(); evictions != 2 {
		t.Fatalf("tracking evictions = %d, want 2", evictions)
	}
}

func TestTrackingEvictionReturnsHotDemotion(t *testing.T) {
	clock := testutil.NewFakeClock(time.Unix(0, 0))
	tracker := New(Config{
		Window: time.Second, EWMAAlpha: 1, PromotionThreshold: 1,
		DemotionThreshold: 0.1, MinimumHotWindows: 1,
		Cooldown: time.Second, MaxTrackedKeys: 1, Clock: clock,
	})
	tracker.Observe("hot")
	clock.Advance(time.Second)
	if transitions := tracker.Advance(); len(transitions) != 1 || transitions[0].To != StateHot {
		t.Fatalf("promotion transitions = %+v", transitions)
	}

	transitions := tracker.Observe("new")
	if len(transitions) != 1 || transitions[0] != (Transition{Key: "hot", From: StateHot, To: StateCold}) {
		t.Fatalf("eviction transitions = %+v", transitions)
	}
}

func TestObserveWithStateReturnsCurrentStateAndTelemetry(t *testing.T) {
	clock := testutil.NewFakeClock(time.Unix(0, 0))
	tracker := New(Config{
		Window: time.Second, EWMAAlpha: 1, PromotionThreshold: 1,
		DemotionThreshold: 0.1, MinimumHotWindows: 1,
		Cooldown: time.Second, MaxTrackedKeys: 1, Clock: clock,
	})

	first := tracker.ObserveWithState("first")
	if first.State != StateCold || first.Hot != 0 || first.OversizedObservationsDropped != 0 {
		t.Fatalf("first observation = %+v, want cold state and zero telemetry", first)
	}
	clock.Advance(time.Second)
	promoted := tracker.ObserveWithState("first")
	if promoted.State != StateHot || promoted.Hot != 1 {
		t.Fatalf("promoted observation = %+v, want hot state and one hot key", promoted)
	}
	if len(promoted.Transitions) != 1 || promoted.Transitions[0].To != StateHot {
		t.Fatalf("promotion transitions = %+v", promoted.Transitions)
	}

	replacement := tracker.ObserveWithState("replacement")
	if replacement.State != StateCold || replacement.Hot != 0 {
		t.Fatalf("replacement observation = %+v, want cold state and zero hot keys", replacement)
	}
	if len(replacement.Transitions) != 1 || replacement.Transitions[0] != (Transition{Key: "first", From: StateHot, To: StateCold}) {
		t.Fatalf("replacement transitions = %+v", replacement.Transitions)
	}

	oversized := tracker.ObserveWithState(strings.Repeat("k", MaxTrackedKeyBytes+1))
	if oversized.State != "" || oversized.Hot != 0 || !oversized.OversizedObservationDropped || oversized.OversizedObservationsDropped != 1 {
		t.Fatalf("oversized observation = %+v, want untracked state and one drop", oversized)
	}
}

func TestStatsHotCountTracksPromotionDemotionAndEviction(t *testing.T) {
	clock := testutil.NewFakeClock(time.Unix(0, 0))
	tracker := New(Config{
		Window: time.Second, EWMAAlpha: 1, PromotionThreshold: 1,
		DemotionThreshold: 0.1, MinimumHotWindows: 1,
		Cooldown: time.Second, MaxTrackedKeys: 1, Clock: clock,
	})

	tracker.Observe("first")
	clock.Advance(time.Second)
	tracker.Advance()
	if tracked, hot := tracker.Stats(); tracked != 1 || hot != 1 {
		t.Fatalf("stats after promotion = tracked:%d hot:%d, want 1/1", tracked, hot)
	}

	tracker.Observe("replacement")
	if tracked, hot := tracker.Stats(); tracked != 1 || hot != 0 {
		t.Fatalf("stats after hot eviction = tracked:%d hot:%d, want 1/0", tracked, hot)
	}

	clock.Advance(time.Second)
	view := tracker.AdvanceAndSnapshot(1)
	if view.Tracked != 1 || view.Hot != 1 {
		t.Fatalf("view after replacement promotion = tracked:%d hot:%d, want 1/1", view.Tracked, view.Hot)
	}

	clock.Advance(time.Second)
	tracker.Advance()
	if tracked, hot := tracker.Stats(); tracked != 1 || hot != 0 {
		t.Fatalf("stats after demotion = tracked:%d hot:%d, want 1/0", tracked, hot)
	}
}

func TestAdvanceAndSnapshotReturnsBoundaryTransitionAndStateTogether(t *testing.T) {
	clock := testutil.NewFakeClock(time.Unix(0, 0))
	tracker := New(Config{
		Window: time.Second, EWMAAlpha: 1, PromotionThreshold: 1,
		DemotionThreshold: 0.1, MinimumHotWindows: 1,
		Cooldown: time.Second, MaxTrackedKeys: 10, Clock: clock,
	})
	tracker.Observe("k")
	clock.Advance(time.Second)

	view := tracker.AdvanceAndSnapshot(1)
	if len(view.Transitions) != 1 || view.Transitions[0].To != StateHot {
		t.Fatalf("view transitions = %+v", view.Transitions)
	}
	if len(view.Snapshots) != 1 || view.Snapshots[0].State != StateHot {
		t.Fatalf("view snapshots = %+v", view.Snapshots)
	}
	if again := tracker.AdvanceAndSnapshot(1); len(again.Transitions) != 0 {
		t.Fatalf("transition was returned more than once: %+v", again.Transitions)
	}
}

func TestSnapshotRateRepresentsLatestCompletedWindow(t *testing.T) {
	clock := testutil.NewFakeClock(time.Unix(0, 0))
	tracker := testTracker(clock)
	tracker.Observe("k")
	tracker.Observe("k")

	clock.Advance(time.Second)
	tracker.Advance()
	snapshots := tracker.Snapshots(1)
	if len(snapshots) != 1 || snapshots[0].RequestRate != 2 {
		t.Fatalf("first completed-window rate = %+v, want 2", snapshots)
	}

	clock.Advance(3 * time.Second)
	tracker.Advance()
	snapshots = tracker.Snapshots(1)
	if len(snapshots) != 1 || snapshots[0].RequestRate != 0 {
		t.Fatalf("idle latest-window rate = %+v, want 0", snapshots)
	}
}

func TestAdvanceAcrossHugeWindowGapUsesClosedFormDecay(t *testing.T) {
	clock := testutil.NewFakeClock(time.Unix(0, 0))
	tracker := New(Config{
		Window: time.Nanosecond, EWMAAlpha: 0.5, PromotionThreshold: 10,
		DemotionThreshold: 1, MinimumHotWindows: 1,
		Cooldown: time.Second, MaxTrackedKeys: 1, Clock: clock,
	})
	tracker.Observe("k")
	clock.Advance(24 * time.Hour)
	tracker.Advance()

	view := tracker.AdvanceAndSnapshot(1)
	if len(view.Snapshots) != 1 {
		t.Fatalf("snapshots = %d, want 1", len(view.Snapshots))
	}
	if view.Snapshots[0].Score != 0 || view.Snapshots[0].RequestRate != 0 {
		t.Fatalf("large-gap snapshot = %+v, want fully decayed", view.Snapshots[0])
	}
}

func TestLongIdleGapCompletesCooldownAndRequiresRepromotion(t *testing.T) {
	clock := testutil.NewFakeClock(time.Unix(0, 0))
	tracker := New(Config{
		Window: time.Second, EWMAAlpha: 1, PromotionThreshold: 2,
		DemotionThreshold: 1, MinimumHotWindows: 2,
		Cooldown: 2 * time.Second, MaxTrackedKeys: 1, Clock: clock,
	})
	for range 2 {
		tracker.Observe("k")
	}
	clock.Advance(time.Second)
	tracker.Advance()
	for range 2 {
		tracker.Observe("k")
	}
	clock.Advance(time.Second)
	tracker.Advance()
	if !tracker.IsHot("k") {
		t.Fatal("test setup did not promote key")
	}

	clock.Advance(10 * time.Second)
	transitions := tracker.Advance()
	if tracker.IsHot("k") {
		t.Fatal("long idle gap left key hot")
	}
	if _, hot := tracker.Stats(); hot != 0 {
		t.Fatalf("stats retained %d hot keys after closed-form cooldown", hot)
	}
	if len(transitions) != 2 || transitions[0].To != StateCooling || transitions[1].To != StateCold {
		t.Fatalf("idle transitions = %+v, want HOT->COOLING->COLD", transitions)
	}

	for range 2 {
		tracker.Observe("k")
	}
	clock.Advance(time.Second)
	transitions = tracker.Advance()
	if tracker.IsHot("k") {
		t.Fatal("one new hot window bypassed minimum_hot_windows")
	}
	if len(transitions) != 1 || transitions[0].To != StateWarm {
		t.Fatalf("first repromotion transitions = %+v, want COLD->WARM", transitions)
	}
}

func TestSnapshotsLimitMatchesFullOrdering(t *testing.T) {
	now := time.Unix(100, 0)
	items := map[string]*entry{}
	states := []State{StateCold, StateWarm, StateHot, StateCooling}
	scores := []float64{1, 7, 7, 3, 12}
	for i := 0; i < 40; i++ {
		key := string(rune('a'+i%10)) + string(rune('A'+i/10))
		items[key] = &entry{
			key:       key,
			state:     states[i%len(states)],
			score:     scores[i%len(scores)],
			lastSeen:  now.Add(-time.Duration(i) * time.Second),
			createdAt: now.Add(-time.Minute),
		}
	}

	all := topSnapshots(items, now, 0)
	for _, limit := range []int{1, 2, 3, 7, 19, len(all) - 1} {
		got := topSnapshots(items, now, limit)
		if !reflect.DeepEqual(got, all[:limit]) {
			t.Fatalf("top %d snapshots differ from full ordering:\ngot  %+v\nwant %+v", limit, got, all[:limit])
		}
	}
}

func TestOversizedKeyIsNotTracked(t *testing.T) {
	clock := testutil.NewFakeClock(time.Unix(0, 0))
	tracker := testTracker(clock)
	tracker.Observe(strings.Repeat("k", MaxTrackedKeyBytes+1))
	if tracked, hot := tracker.Stats(); tracked != 0 || hot != 0 {
		t.Fatalf("oversized key affected tracker: tracked=%d hot=%d", tracked, hot)
	}
	if dropped := tracker.OversizedObservationsDropped(); dropped != 1 {
		t.Fatalf("oversized observations dropped = %d, want 1", dropped)
	}
}
