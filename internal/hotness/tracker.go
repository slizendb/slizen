package hotness

import (
	"math"
	"sync"
	"time"
)

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
	mu     sync.Mutex
	cfg    Config
	clock  Clock
	window time.Time
	items  map[string]*entry
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
	now := t.clock.Now()

	t.mu.Lock()
	defer t.mu.Unlock()

	transitions := t.advanceLocked(now)
	if key == "" {
		return transitions
	}
	ent := t.getOrCreateLocked(key, now)
	ent.count++
	ent.lastSeen = now
	return transitions
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

	for _, ent := range t.items {
		if ent.state == StateHot {
			hot++
		}
	}
	return len(t.items), hot
}

func (t *Tracker) Snapshots(limit int) []Snapshot {
	now := t.clock.Now()

	t.mu.Lock()
	defer t.mu.Unlock()

	t.advanceLocked(now)
	out := make([]Snapshot, 0, len(t.items))
	for _, ent := range t.items {
		out = append(out, ent.snapshot(now))
	}
	sortSnapshots(out)
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

func (t *Tracker) getOrCreateLocked(key string, now time.Time) *entry {
	if ent, ok := t.items[key]; ok {
		return ent
	}
	if len(t.items) >= t.cfg.MaxTrackedKeys {
		t.evictOneLocked()
	}
	ent := &entry{
		key:       key,
		state:     StateCold,
		lastSeen:  now,
		createdAt: now,
	}
	t.items[key] = ent
	return ent
}

func (t *Tracker) advanceLocked(now time.Time) []Transition {
	if now.Before(t.window.Add(t.cfg.Window)) {
		return nil
	}
	elapsedWindows := int(now.Sub(t.window) / t.cfg.Window)
	if elapsedWindows < 1 {
		elapsedWindows = 1
	}
	transitions := make([]Transition, 0)
	for _, ent := range t.items {
		rate := float64(ent.count) / t.cfg.Window.Seconds()
		ent.lastRate = rate
		decayed := ent.score
		for i := 0; i < elapsedWindows; i++ {
			if i == 0 {
				decayed = t.cfg.EWMAAlpha*rate + (1-t.cfg.EWMAAlpha)*decayed
			} else {
				decayed = (1 - t.cfg.EWMAAlpha) * decayed
			}
		}
		ent.score = decayed
		ent.count = 0
		transitions = append(transitions, t.applyStateLocked(ent, now)...)
	}
	t.window = t.window.Add(time.Duration(elapsedWindows) * t.cfg.Window)
	return transitions
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

func (t *Tracker) evictOneLocked() {
	var victim *entry
	for _, ent := range t.items {
		if victim == nil {
			victim = ent
			continue
		}
		if ent.score < victim.score || (ent.score == victim.score && ent.lastSeen.Before(victim.lastSeen)) {
			victim = ent
		}
	}
	if victim != nil {
		delete(t.items, victim.key)
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
