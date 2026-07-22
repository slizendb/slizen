package hotness

import (
	"math"
	"sync"
	"time"
)

const MaxTrackedKeyBytes = 1024

type Clock interface {
	Now() time.Time
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

type Config struct {
	Window             time.Duration
	EWMAAlpha          float64
	PromotionThreshold float64
	DemotionThreshold  float64
	MinimumHotWindows  int
	Cooldown           time.Duration
	MaxTrackedKeys     int
	Clock              Clock
}

type Snapshot struct {
	Key         string        `json:"-"`
	State       State         `json:"state"`
	Score       float64       `json:"score"`
	RequestRate float64       `json:"request_rate"`
	LastSeen    time.Time     `json:"last_seen"`
	Age         time.Duration `json:"age"`
}

type entry struct {
	key        string
	state      State
	score      float64
	count      int
	lastRate   float64
	lastSeen   time.Time
	createdAt  time.Time
	hotWindows int
	coolingAt  time.Time
}

type Tracker struct {
	mu                           sync.Mutex
	cfg                          Config
	clock                        Clock
	window                       time.Time
	items                        map[string]*entry
	admissionRing                []*entry
	nextVictim                   int
	hot                          int
	evictions                    uint64
	oversizedObservationsDropped uint64
}

// View is an atomic point-in-time view of the tracker. Transitions and
// snapshots are derived from the same clock reading while the tracker is
// locked, so callers cannot accidentally discard a state change at a window
// boundary.
type View struct {
	Transitions                  []Transition
	Snapshots                    []Snapshot
	Tracked                      int
	Hot                          int
	Evictions                    uint64
	OversizedObservationsDropped uint64
}

// Observation is the result of observing one key. The key state and aggregate
// telemetry are captured under the same lock as the observation.
type Observation struct {
	Transitions                  []Transition
	State                        State
	Hot                          int
	OversizedObservationDropped  bool
	OversizedObservationsDropped uint64
}

func New(cfg Config) *Tracker {
	if cfg.Clock == nil {
		cfg.Clock = realClock{}
	}
	if cfg.Window <= 0 {
		cfg.Window = time.Second
	}
	if cfg.EWMAAlpha <= 0 || cfg.EWMAAlpha > 1 {
		cfg.EWMAAlpha = 0.5
	}
	if cfg.MinimumHotWindows <= 0 {
		cfg.MinimumHotWindows = 2
	}
	if cfg.MaxTrackedKeys <= 0 {
		cfg.MaxTrackedKeys = 1
	}
	now := cfg.Clock.Now()
	return &Tracker{
		cfg:    cfg,
		clock:  cfg.Clock,
		window: now,
		items:  make(map[string]*entry),
	}
}

func (t *Tracker) Observe(key string) []Transition {
	return t.ObserveWithState(key).Transitions
}

func (t *Tracker) ObserveWithState(key string) Observation {
	now := t.clock.Now()

	t.mu.Lock()
	defer t.mu.Unlock()

	transitions := t.advanceLocked(now)
	if key == "" || len(key) > MaxTrackedKeyBytes {
		if len(key) > MaxTrackedKeyBytes {
			t.oversizedObservationsDropped++
		}
		return Observation{
			Transitions:                  transitions,
			Hot:                          t.hot,
			OversizedObservationDropped:  len(key) > MaxTrackedKeyBytes,
			OversizedObservationsDropped: t.oversizedObservationsDropped,
		}
	}
	ent, evictionTransition := t.getOrCreateLocked(key, now)
	if evictionTransition != nil {
		transitions = append(transitions, *evictionTransition)
	}
	ent.count++
	ent.lastSeen = now
	return Observation{
		Transitions:                  transitions,
		State:                        ent.state,
		Hot:                          t.hot,
		OversizedObservationsDropped: t.oversizedObservationsDropped,
	}
}

func (t *Tracker) Advance() []Transition {
	now := t.clock.Now()

	t.mu.Lock()
	defer t.mu.Unlock()

	return t.advanceLocked(now)
}

func (t *Tracker) IsHot(key string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	ent, ok := t.items[key]
	return ok && ent.state == StateHot
}

func (t *Tracker) Stats() (tracked int, hot int) {
	t.mu.Lock()
	defer t.mu.Unlock()

	return len(t.items), t.hot
}

func (t *Tracker) Evictions() uint64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.evictions
}

func (t *Tracker) OversizedObservationsDropped() uint64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.oversizedObservationsDropped
}

// AdvanceAndSnapshot advances the scoring window and returns all resulting
// transitions together with a bounded snapshot from the same instant.
func (t *Tracker) AdvanceAndSnapshot(limit int) View {
	now := t.clock.Now()

	t.mu.Lock()
	defer t.mu.Unlock()

	view := View{
		Transitions:                  t.advanceLocked(now),
		Tracked:                      len(t.items),
		Hot:                          t.hot,
		Evictions:                    t.evictions,
		OversizedObservationsDropped: t.oversizedObservationsDropped,
	}
	view.Snapshots = topSnapshots(t.items, now, limit)
	return view
}

// Snapshots returns current state without advancing the scoring window. Code
// that needs up-to-date transitions must use AdvanceAndSnapshot so transitions
// cannot be silently discarded.
func (t *Tracker) Snapshots(limit int) []Snapshot {
	now := t.clock.Now()

	t.mu.Lock()
	defer t.mu.Unlock()

	return topSnapshots(t.items, now, limit)
}

func (t *Tracker) getOrCreateLocked(key string, now time.Time) (*entry, *Transition) {
	if ent, ok := t.items[key]; ok {
		return ent, nil
	}

	if len(t.admissionRing) < t.cfg.MaxTrackedKeys {
		ent := &entry{
			key:       key,
			state:     StateCold,
			lastSeen:  now,
			createdAt: now,
		}
		t.admissionRing = append(t.admissionRing, ent)
		t.items[key] = ent
		return ent, nil
	}

	// Reuse the oldest admission slot. This keeps eviction deterministic and
	// constant-time even when tracking is at its configured cardinality cap.
	victim := t.admissionRing[t.nextVictim]
	victimKey := victim.key
	victimState := victim.state
	delete(t.items, victimKey)
	t.adjustHotCountLocked(victimState, StateCold)
	t.evictions++
	t.nextVictim++
	if t.nextVictim == len(t.admissionRing) {
		t.nextVictim = 0
	}

	*victim = entry{
		key:       key,
		state:     StateCold,
		lastSeen:  now,
		createdAt: now,
	}
	t.items[key] = victim
	if victimState == StateCold {
		return victim, nil
	}
	return victim, &Transition{Key: victimKey, From: victimState, To: StateCold}
}

func (t *Tracker) advanceLocked(now time.Time) []Transition {
	if now.Before(t.window.Add(t.cfg.Window)) {
		return nil
	}
	elapsedWindows := int64(now.Sub(t.window) / t.cfg.Window)
	if elapsedWindows < 1 {
		elapsedWindows = 1
	}
	transitions := make([]Transition, 0)
	firstBoundary := t.window.Add(t.cfg.Window)
	for _, ent := range t.items {
		rate := float64(ent.count) / t.cfg.Window.Seconds()
		before := ent.state
		transitions = append(transitions, t.advanceEntryLocked(ent, rate, elapsedWindows, firstBoundary)...)
		t.adjustHotCountLocked(before, ent.state)
		ent.count = 0
	}
	t.window = t.window.Add(time.Duration(elapsedWindows) * t.cfg.Window)
	return transitions
}

func (t *Tracker) advanceEntryLocked(ent *entry, rate float64, elapsedWindows int64, firstBoundary time.Time) []Transition {
	beta := 1 - t.cfg.EWMAAlpha
	firstScore := t.cfg.EWMAAlpha*rate + beta*ent.score
	ent.score = firstScore
	ent.lastRate = rate
	transitions := t.applyStateLocked(ent, firstBoundary)
	if elapsedWindows == 1 {
		return transitions
	}

	// The count belongs to the oldest completed window. Every later elapsed
	// window was empty, so both score decay and state transitions can be caught
	// up by threshold segments instead of iterating once per skipped window.
	remaining := elapsedWindows - 1
	ent.lastRate = 0
	highWindows := decayWindowsAtLeast(firstScore, beta, t.cfg.PromotionThreshold, remaining)
	atLeastDemotion := decayWindowsAtLeast(firstScore, beta, t.cfg.DemotionThreshold, remaining)
	midWindows := atLeastDemotion - highWindows
	lowWindows := remaining - atLeastDemotion

	if highWindows > 0 {
		before := ent.state
		if ent.state == StateCooling {
			ent.state = StateHot
			ent.coolingAt = time.Time{}
		}
		needed := int64(t.cfg.MinimumHotWindows - ent.hotWindows)
		if needed < 0 {
			needed = 0
		}
		if ent.state != StateHot && highWindows >= needed {
			ent.state = StateHot
		}
		if highWindows >= int64(t.cfg.MinimumHotWindows) || int64(ent.hotWindows)+highWindows >= int64(t.cfg.MinimumHotWindows) {
			ent.hotWindows = t.cfg.MinimumHotWindows
		} else {
			ent.hotWindows += int(highWindows)
		}
		if before != ent.state {
			transitions = append(transitions, Transition{Key: ent.key, From: before, To: ent.state})
		}
	}

	if midWindows > 0 {
		before := ent.state
		ent.hotWindows = 0
		if ent.state == StateCold {
			ent.state = StateWarm
		}
		if before != ent.state {
			transitions = append(transitions, Transition{Key: ent.key, From: before, To: ent.state})
		}
	}

	finalScore := firstScore * math.Pow(beta, float64(remaining))
	if lowWindows > 0 {
		ent.hotWindows = 0
		lowStartOffset := highWindows + midWindows + 1
		lowStart := firstBoundary.Add(time.Duration(lowStartOffset) * t.cfg.Window)
		if ent.state == StateHot {
			ent.state = StateCooling
			ent.coolingAt = lowStart
			transitions = append(transitions, Transition{Key: ent.key, From: StateHot, To: StateCooling})
		}
		finalBoundary := firstBoundary.Add(time.Duration(remaining) * t.cfg.Window)
		if ent.state == StateCooling && finalBoundary.Sub(ent.coolingAt) >= t.cfg.Cooldown {
			ent.state = StateCold
			ent.coolingAt = time.Time{}
			transitions = append(transitions, Transition{Key: ent.key, From: StateCooling, To: StateCold})
		} else if ent.state == StateWarm && finalScore == 0 {
			ent.state = StateCold
			transitions = append(transitions, Transition{Key: ent.key, From: StateWarm, To: StateCold})
		}
	}
	ent.score = finalScore
	return transitions
}

// decayWindowsAtLeast returns how many of the next maxWindows empty-window
// scores (initial*decay^1, initial*decay^2, ...) are at least threshold.
func decayWindowsAtLeast(initial, decay, threshold float64, maxWindows int64) int64 {
	if maxWindows <= 0 || initial <= 0 || threshold <= 0 || decay <= 0 || initial*decay < threshold {
		return 0
	}
	if decay >= 1 || initial*math.Pow(decay, float64(maxWindows)) >= threshold {
		return maxWindows
	}
	estimate := int64(math.Floor(math.Log(threshold/initial) / math.Log(decay)))
	if estimate < 0 {
		estimate = 0
	}
	if estimate > maxWindows {
		estimate = maxWindows
	}
	for estimate > 0 && initial*math.Pow(decay, float64(estimate)) < threshold {
		estimate--
	}
	for estimate < maxWindows && initial*math.Pow(decay, float64(estimate+1)) >= threshold {
		estimate++
	}
	return estimate
}

func (t *Tracker) applyStateLocked(ent *entry, now time.Time) []Transition {
	before := ent.state
	score := ent.score

	switch {
	case score >= t.cfg.PromotionThreshold:
		ent.hotWindows++
		if ent.state == StateCooling {
			ent.state = StateHot
			ent.coolingAt = time.Time{}
		} else if ent.hotWindows >= t.cfg.MinimumHotWindows {
			ent.state = StateHot
		} else if ent.state == StateCold {
			ent.state = StateWarm
		}
	case score >= t.cfg.DemotionThreshold:
		ent.hotWindows = 0
		if ent.state == StateCold {
			ent.state = StateWarm
		}
	case score < t.cfg.DemotionThreshold:
		ent.hotWindows = 0
		if ent.state == StateHot {
			ent.state = StateCooling
			ent.coolingAt = now
		} else if ent.state == StateCooling && now.Sub(ent.coolingAt) >= t.cfg.Cooldown {
			ent.state = StateCold
			ent.coolingAt = time.Time{}
		} else if ent.state == StateWarm && score == 0 {
			ent.state = StateCold
		}
	}

	if before == ent.state {
		return nil
	}
	return []Transition{{Key: ent.key, From: before, To: ent.state}}
}

func (t *Tracker) adjustHotCountLocked(before, after State) {
	if before == StateHot && after != StateHot {
		t.hot--
	}
	if before != StateHot && after == StateHot {
		t.hot++
	}
}

func (e *entry) snapshot(now time.Time) Snapshot {
	return Snapshot{
		Key:         e.key,
		State:       e.state,
		Score:       math.Round(e.score*100) / 100,
		RequestRate: math.Round(e.lastRate*100) / 100,
		LastSeen:    e.lastSeen,
		Age:         now.Sub(e.createdAt),
	}
}
