package hotness

import (
	"reflect"
	"strings"
	"sync"
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
	transitions = tracker.Observe("k")
	if len(transitions) != 1 || transitions[0].To != StateHot {
		t.Fatalf("expected guaranteed hot transition, got %+v", transitions)
	}
	if !tracker.IsHot("k") {
		t.Fatal("expected hot key")
	}
	clock.Advance(time.Second)
	if transitions := tracker.Advance(); len(transitions) != 0 {
		t.Fatalf("completed guaranteed window emitted duplicate transition: %+v", transitions)
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

func TestGuaranteedFinalHotWindowPromotesBeforeBoundary(t *testing.T) {
	clock := testutil.NewFakeClock(time.Unix(0, 0))
	tracker := New(Config{
		Window:             time.Second,
		EWMAAlpha:          0.5,
		PromotionThreshold: 100,
		DemotionThreshold:  20,
		MinimumHotWindows:  2,
		Cooldown:           time.Second,
		MaxTrackedKeys:     10,
		Clock:              clock,
	})

	// The first completed window produces an EWMA score of exactly 100 and
	// satisfies only the first of the two required hot windows.
	for range 200 {
		tracker.Observe("k")
	}
	if tracker.IsHot("k") {
		t.Fatal("current-window traffic bypassed the required completed hot window")
	}
	clock.Advance(time.Second)
	transitions := tracker.Advance()
	if len(transitions) != 1 || transitions[0] != (Transition{Key: "k", From: StateCold, To: StateWarm}) {
		t.Fatalf("first-window transitions = %+v, want COLD->WARM", transitions)
	}

	// With alpha 0.5 and a previous score of 100, 99 requests can produce at
	// most a next-window score of 99.5. Promotion must remain impossible.
	for i := 0; i < 99; i++ {
		observation := tracker.ObserveWithState("k")
		if observation.State != StateWarm || len(observation.Transitions) != 0 {
			t.Fatalf("observation %d = %+v, want unchanged WARM state", i+1, observation)
		}
	}

	// The 100th request guarantees a completed-window score of at least 100,
	// even if no later request arrives. The final required hot window can be
	// committed without waiting for its wall-clock boundary.
	promoted := tracker.ObserveWithState("k")
	if promoted.State != StateHot || promoted.Hot != 1 {
		t.Fatalf("guaranteed observation = %+v, want one HOT key", promoted)
	}
	if len(promoted.Transitions) != 1 || promoted.Transitions[0] != (Transition{Key: "k", From: StateWarm, To: StateHot}) {
		t.Fatalf("guaranteed transitions = %+v, want WARM->HOT", promoted.Transitions)
	}

	clock.Advance(time.Second)
	if transitions := tracker.Advance(); len(transitions) != 0 {
		t.Fatalf("completed guaranteed window emitted duplicate transition: %+v", transitions)
	}
	snapshots := tracker.Snapshots(1)
	if len(snapshots) != 1 || snapshots[0].State != StateHot || snapshots[0].Score != 100 || snapshots[0].RequestRate != 100 {
		t.Fatalf("completed guaranteed window snapshot = %+v, want HOT score/rate 100", snapshots)
	}
}

func TestGuaranteedPromotionHonorsThreeRequiredWindows(t *testing.T) {
	clock := testutil.NewFakeClock(time.Unix(0, 0))
	tracker := New(Config{
		Window:             time.Second,
		EWMAAlpha:          1,
		PromotionThreshold: 2,
		DemotionThreshold:  1,
		MinimumHotWindows:  3,
		Cooldown:           time.Second,
		MaxTrackedKeys:     10,
		Clock:              clock,
	})

	for range 2 {
		tracker.Observe("k")
	}
	clock.Advance(time.Second)
	if transitions := tracker.Advance(); len(transitions) != 1 || transitions[0].To != StateWarm {
		t.Fatalf("first-window transitions = %+v, want COLD->WARM", transitions)
	}

	for range 2 {
		if transitions := tracker.Observe("k"); len(transitions) != 0 {
			t.Fatalf("second-window traffic promoted before three required windows: %+v", transitions)
		}
	}
	clock.Advance(time.Second)
	if transitions := tracker.Advance(); len(transitions) != 0 || tracker.IsHot("k") {
		t.Fatalf("second-window boundary = %+v, want unchanged WARM", transitions)
	}

	tracker.Observe("k")
	transitions := tracker.Observe("k")
	if len(transitions) != 1 || transitions[0] != (Transition{Key: "k", From: StateWarm, To: StateHot}) {
		t.Fatalf("third-window transitions = %+v, want WARM->HOT", transitions)
	}
}

func TestGuaranteedPromotionDoesNotBridgeSkippedWindows(t *testing.T) {
	clock := testutil.NewFakeClock(time.Unix(0, 0))
	tracker := testTracker(clock)

	for range 2 {
		tracker.Observe("k")
	}
	clock.Advance(time.Second)
	if transitions := tracker.Advance(); len(transitions) != 1 || transitions[0].To != StateWarm {
		t.Fatalf("first-window transitions = %+v, want COLD->WARM", transitions)
	}

	// Empty windows break the qualifying streak before the new observations
	// are counted. One later guaranteed window must therefore only warm the key.
	clock.Advance(2 * time.Second)
	if transitions := tracker.Advance(); len(transitions) != 1 || transitions[0] != (Transition{Key: "k", From: StateWarm, To: StateCold}) {
		t.Fatalf("idle transitions = %+v, want WARM->COLD", transitions)
	}
	for range 2 {
		if transitions := tracker.Observe("k"); len(transitions) != 0 {
			t.Fatalf("new current-window traffic bridged the idle gap: %+v", transitions)
		}
	}
	if tracker.IsHot("k") {
		t.Fatal("one new qualifying window bypassed minimum_hot_windows after an idle gap")
	}

	clock.Advance(time.Second)
	if transitions := tracker.Advance(); len(transitions) != 1 || transitions[0].To != StateWarm {
		t.Fatalf("post-idle transitions = %+v, want COLD->WARM", transitions)
	}
}

func TestGuaranteedPromotionConcurrentExactlyOnce(t *testing.T) {
	clock := testutil.NewFakeClock(time.Unix(0, 0))
	tracker := New(Config{
		Window:             time.Second,
		EWMAAlpha:          1,
		PromotionThreshold: 64,
		DemotionThreshold:  1,
		MinimumHotWindows:  2,
		Cooldown:           time.Second,
		MaxTrackedKeys:     10,
		Clock:              clock,
	})

	for range 64 {
		tracker.Observe("k")
	}
	clock.Advance(time.Second)
	if transitions := tracker.Advance(); len(transitions) != 1 || transitions[0].To != StateWarm {
		t.Fatalf("first-window transitions = %+v, want COLD->WARM", transitions)
	}

	observations := make(chan Observation, 64)
	var wg sync.WaitGroup
	for range 64 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			observations <- tracker.ObserveWithState("k")
		}()
	}
	wg.Wait()
	close(observations)

	promotions := 0
	for observation := range observations {
		for _, transition := range observation.Transitions {
			if transition != (Transition{Key: "k", From: StateWarm, To: StateHot}) {
				t.Fatalf("unexpected concurrent transition: %+v", transition)
			}
			promotions++
		}
	}
	if promotions != 1 {
		t.Fatalf("concurrent promotions = %d, want exactly 1", promotions)
	}
	if tracked, hot := tracker.Stats(); tracked != 1 || hot != 1 {
		t.Fatalf("concurrent stats = tracked:%d hot:%d, want 1/1", tracked, hot)
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

func TestTrackingCapacityPinsHotVictimAndDropsUnseenObservation(t *testing.T) {
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

	dropped := tracker.ObserveWithState("new")
	if dropped.State != "" || dropped.Hot != 1 || !dropped.CapacityObservationDropped || dropped.CapacityDrops != 1 {
		t.Fatalf("capacity observation = %+v, want one dropped observation and retained HOT key", dropped)
	}
	if len(dropped.Transitions) != 0 {
		t.Fatalf("capacity drop emitted transitions: %+v", dropped.Transitions)
	}
	if !tracker.IsHot("hot") || tracker.IsTracked("new") {
		t.Fatal("capacity drop replaced the HOT victim")
	}
	if tracker.Evictions() != 0 || tracker.CapacityDrops() != 1 {
		t.Fatalf("capacity telemetry = evictions:%d drops:%d, want 0/1", tracker.Evictions(), tracker.CapacityDrops())
	}
}

func TestTrackingCapacityAdvancesPastHotVictimToReuseNonHotSlot(t *testing.T) {
	clock := testutil.NewFakeClock(time.Unix(0, 0))
	tracker := New(Config{
		Window: time.Second, EWMAAlpha: 1, PromotionThreshold: 10,
		DemotionThreshold: 1, MinimumHotWindows: 1,
		Cooldown: time.Second, MaxTrackedKeys: 2, Clock: clock,
	})

	tracker.Observe("hot")
	tracker.Observe("cold")
	tracker.Admit("hot")
	if dropped := tracker.ObserveWithState("first-new"); !dropped.CapacityObservationDropped {
		t.Fatalf("HOT victim observation = %+v, want capacity drop", dropped)
	}
	if admitted := tracker.ObserveWithState("second-new"); admitted.State != StateCold || admitted.CapacityObservationDropped {
		t.Fatalf("non-HOT victim observation = %+v, want admitted COLD key", admitted)
	}
	if !tracker.IsHot("hot") || tracker.IsTracked("cold") || !tracker.IsTracked("second-new") {
		t.Fatal("second-chance cursor did not preserve HOT and reuse the next non-HOT slot")
	}
	if tracker.Evictions() != 1 || tracker.CapacityDrops() != 1 {
		t.Fatalf("capacity telemetry = evictions:%d drops:%d, want 1/1", tracker.Evictions(), tracker.CapacityDrops())
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
	if replacement.State != "" || replacement.Hot != 1 || !replacement.CapacityObservationDropped || replacement.CapacityDrops != 1 {
		t.Fatalf("replacement observation = %+v, want capacity drop and retained hot key", replacement)
	}
	if len(replacement.Transitions) != 0 {
		t.Fatalf("replacement transitions = %+v, want none", replacement.Transitions)
	}

	oversized := tracker.ObserveWithState(strings.Repeat("k", MaxTrackedKeyBytes+1))
	if oversized.State != "" || oversized.Hot != 1 || oversized.CapacityDrops != 1 || !oversized.OversizedObservationDropped || oversized.OversizedObservationsDropped != 1 {
		t.Fatalf("oversized observation = %+v, want untracked state and one drop", oversized)
	}
}

func TestAdmitMissingKeyDoesNotTrackAndAdvancesWindows(t *testing.T) {
	clock := testutil.NewFakeClock(time.Unix(0, 0))
	tracker := New(Config{
		Window: time.Second, EWMAAlpha: 1, PromotionThreshold: 1,
		DemotionThreshold: 0.1, MinimumHotWindows: 1,
		Cooldown: time.Second, MaxTrackedKeys: 10, Clock: clock,
	})

	missing := tracker.Admit("missing")
	if missing.State != "" || missing.Hot != 0 || len(missing.Transitions) != 0 {
		t.Fatalf("missing admission = %+v, want empty no-op observation", missing)
	}
	if tracked, hot := tracker.Stats(); tracked != 0 || hot != 0 {
		t.Fatalf("missing admission stats = tracked:%d hot:%d, want 0/0", tracked, hot)
	}

	tracker.Observe(strings.Repeat("x", MaxTrackedKeyBytes+1))
	tracker.Observe("tracked")
	clock.Advance(time.Second)
	advanced := tracker.Admit("missing")
	if advanced.State != "" || advanced.Hot != 1 || advanced.OversizedObservationDropped || advanced.OversizedObservationsDropped != 1 {
		t.Fatalf("advanced missing admission = %+v, want no target state and current telemetry", advanced)
	}
	wantTransition := Transition{Key: "tracked", From: StateCold, To: StateHot}
	if len(advanced.Transitions) != 1 || advanced.Transitions[0] != wantTransition {
		t.Fatalf("advanced missing transitions = %+v, want %+v", advanced.Transitions, wantTransition)
	}
	if _, ok := tracker.items["missing"]; ok {
		t.Fatal("missing admission created a tracked key")
	}
	if tracked, hot := tracker.Stats(); tracked != 1 || hot != 1 {
		t.Fatalf("advanced missing admission stats = tracked:%d hot:%d, want 1/1", tracked, hot)
	}
	if transitions := tracker.Advance(); len(transitions) != 0 {
		t.Fatalf("admission did not consume elapsed window: %+v", transitions)
	}
}

func TestAdmitPromotesTrackedNonHotStatesWithoutChangingEvidence(t *testing.T) {
	assertAdmitted := func(t *testing.T, tracker *Tracker, from State) {
		t.Helper()
		ent := tracker.items["k"]
		if ent == nil || ent.state != from {
			t.Fatalf("setup state = %+v, want %s", ent, from)
		}
		beforeScore, beforeCount := ent.score, ent.count

		observation := tracker.Admit("k")
		wantTransition := Transition{Key: "k", From: from, To: StateHot}
		if observation.State != StateHot || observation.Hot != 1 {
			t.Fatalf("admission from %s = %+v, want one HOT key", from, observation)
		}
		if len(observation.Transitions) != 1 || observation.Transitions[0] != wantTransition {
			t.Fatalf("admission transitions from %s = %+v, want %+v", from, observation.Transitions, wantTransition)
		}
		if ent.score != beforeScore || ent.count != beforeCount {
			t.Fatalf("admission from %s changed evidence: score/count %v/%d -> %v/%d", from, beforeScore, beforeCount, ent.score, ent.count)
		}
		if ent.hotWindows != 0 {
			t.Fatalf("admission from %s retained EWMA streak %d", from, ent.hotWindows)
		}
		if !ent.coolingAt.IsZero() {
			t.Fatalf("admission from %s retained cooling timestamp %v", from, ent.coolingAt)
		}
	}

	t.Run("cold", func(t *testing.T) {
		clock := testutil.NewFakeClock(time.Unix(0, 0))
		tracker := testTracker(clock)
		tracker.Observe("k")
		assertAdmitted(t, tracker, StateCold)
	})

	t.Run("warm", func(t *testing.T) {
		clock := testutil.NewFakeClock(time.Unix(0, 0))
		tracker := testTracker(clock)
		tracker.Observe("k")
		tracker.Observe("k")
		clock.Advance(time.Second)
		if transitions := tracker.Advance(); len(transitions) != 1 || transitions[0].To != StateWarm {
			t.Fatalf("warm setup transitions = %+v", transitions)
		}
		tracker.Observe("k")
		assertAdmitted(t, tracker, StateWarm)
	})

	t.Run("cooling", func(t *testing.T) {
		clock := testutil.NewFakeClock(time.Unix(0, 0))
		tracker := testTracker(clock)
		tracker.Observe("k")
		tracker.Observe("k")
		clock.Advance(time.Second)
		tracker.Advance()
		tracker.Observe("k")
		tracker.Observe("k")
		clock.Advance(time.Second)
		tracker.Advance()
		clock.Advance(time.Second)
		if transitions := tracker.Advance(); len(transitions) != 1 || transitions[0].To != StateCooling {
			t.Fatalf("cooling setup transitions = %+v", transitions)
		}
		tracker.Observe("k")
		assertAdmitted(t, tracker, StateCooling)
	})
}

func TestAdmitHotIsIdempotent(t *testing.T) {
	clock := testutil.NewFakeClock(time.Unix(0, 0))
	tracker := testTracker(clock)
	tracker.Observe("k")
	if first := tracker.Admit("k"); len(first.Transitions) != 1 || first.Transitions[0] != (Transition{Key: "k", From: StateCold, To: StateHot}) {
		t.Fatalf("first admission = %+v, want COLD->HOT", first)
	}
	ent := tracker.items["k"]
	beforeScore, beforeCount := ent.score, ent.count

	second := tracker.Admit("k")
	if second.State != StateHot || second.Hot != 1 || len(second.Transitions) != 0 {
		t.Fatalf("repeated admission = %+v, want unchanged HOT state", second)
	}
	if ent.score != beforeScore || ent.count != beforeCount {
		t.Fatalf("repeated admission changed evidence: score/count %v/%d -> %v/%d", beforeScore, beforeCount, ent.score, ent.count)
	}
	if tracked, hot := tracker.Stats(); tracked != 1 || hot != 1 {
		t.Fatalf("repeated admission stats = tracked:%d hot:%d, want 1/1", tracked, hot)
	}
}

func TestAdmitConcurrentExactlyOnce(t *testing.T) {
	clock := testutil.NewFakeClock(time.Unix(0, 0))
	tracker := testTracker(clock)
	tracker.Observe("k")

	const callers = 64
	observations := make(chan Observation, callers)
	var wg sync.WaitGroup
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			observations <- tracker.Admit("k")
		}()
	}
	wg.Wait()
	close(observations)

	promotions := 0
	for observation := range observations {
		if observation.State != StateHot || observation.Hot != 1 {
			t.Fatalf("concurrent admission = %+v, want one HOT key", observation)
		}
		for _, transition := range observation.Transitions {
			if transition != (Transition{Key: "k", From: StateCold, To: StateHot}) {
				t.Fatalf("unexpected concurrent transition: %+v", transition)
			}
			promotions++
		}
	}
	if promotions != 1 {
		t.Fatalf("concurrent admissions emitted %d promotions, want exactly 1", promotions)
	}
	if ent := tracker.items["k"]; ent.score != 0 || ent.count != 1 {
		t.Fatalf("concurrent admissions changed evidence: score/count %v/%d, want 0/1", ent.score, ent.count)
	}
	if tracked, hot := tracker.Stats(); tracked != 1 || hot != 1 {
		t.Fatalf("concurrent admission stats = tracked:%d hot:%d, want 1/1", tracked, hot)
	}
}

func TestAdmitDoesNotPreventLaterLowRateDemotion(t *testing.T) {
	clock := testutil.NewFakeClock(time.Unix(0, 0))
	tracker := New(Config{
		Window: time.Second, EWMAAlpha: 1, PromotionThreshold: 10,
		DemotionThreshold: 2, MinimumHotWindows: 2,
		Cooldown: time.Second, MaxTrackedKeys: 10, Clock: clock,
	})
	tracker.Observe("k")
	tracker.Admit("k")

	clock.Advance(time.Second)
	transitions := tracker.Advance()
	if len(transitions) != 1 || transitions[0] != (Transition{Key: "k", From: StateHot, To: StateCooling}) {
		t.Fatalf("low-rate transitions = %+v, want HOT->COOLING", transitions)
	}
	snapshots := tracker.Snapshots(1)
	if len(snapshots) != 1 || snapshots[0].Score != 1 || snapshots[0].RequestRate != 1 {
		t.Fatalf("low-rate snapshot = %+v, want truthful score/rate 1", snapshots)
	}

	clock.Advance(time.Second)
	transitions = tracker.Advance()
	if len(transitions) != 1 || transitions[0] != (Transition{Key: "k", From: StateCooling, To: StateCold}) {
		t.Fatalf("cooldown transitions = %+v, want COOLING->COLD", transitions)
	}
	if tracked, hot := tracker.Stats(); tracked != 1 || hot != 0 {
		t.Fatalf("demoted admission stats = tracked:%d hot:%d, want 1/0", tracked, hot)
	}
}

func TestStatsHotCountTracksPromotionDemotionAndCapacityDrop(t *testing.T) {
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

	dropped := tracker.ObserveWithState("replacement")
	if !dropped.CapacityObservationDropped || dropped.CapacityDrops != 1 {
		t.Fatalf("replacement observation = %+v, want one capacity drop", dropped)
	}
	if tracked, hot := tracker.Stats(); tracked != 1 || hot != 1 {
		t.Fatalf("stats after capacity drop = tracked:%d hot:%d, want 1/1", tracked, hot)
	}

	clock.Advance(time.Second)
	view := tracker.AdvanceAndSnapshot(1)
	if view.Tracked != 1 || view.Hot != 0 || view.CapacityDrops != 1 {
		t.Fatalf("view after demotion = tracked:%d hot:%d drops:%d, want 1/0/1", view.Tracked, view.Hot, view.CapacityDrops)
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
