package service

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/slizendb/slizen/internal/cache"
	"github.com/slizendb/slizen/internal/cachepolicy"
	"github.com/slizendb/slizen/internal/config"
	"github.com/slizendb/slizen/internal/hotness"
	"github.com/slizendb/slizen/internal/metrics"
	"github.com/slizendb/slizen/internal/privacy"
	"github.com/slizendb/slizen/internal/upstream"
)

type Clock interface {
	Now() time.Time
}

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }

const cacheEpochStripeCount = 256

// cacheEpochStripeState makes an epoch check plus its local-cache mutation
// indivisible with respect to write-driven generation changes and invalidation.
type cacheEpochStripeState struct {
	mu    sync.Mutex
	epoch atomic.Uint64
}

type Options struct {
	Config   config.Config
	Upstream upstream.Client
	Metrics  *metrics.Recorder
	Logger   *slog.Logger
	Clock    Clock
	Version  string
	Commit   string
}

type Service struct {
	cfg      config.Config
	upstream upstream.Client
	cache    *cache.Cache
	tracker  *hotness.Tracker
	metrics  *metrics.Recorder
	logger   *slog.Logger
	clock    Clock
	policies *cachepolicy.Matcher
	started  time.Time
	version  string
	commit   string

	proxyActive    atomic.Bool
	closed         atomic.Bool
	lifetimeCtx    context.Context
	cancelLifetime context.CancelFunc
	group          singleflight.Group
	cacheEpochs    [cacheEpochStripeCount]cacheEpochStripeState
}

type Status struct {
	Version                string `json:"version"`
	Commit                 string `json:"commit,omitempty"`
	Mode                   string `json:"mode"`
	KeyVisibility          string `json:"key_visibility"`
	Uptime                 string `json:"uptime"`
	UpstreamStatus         string `json:"upstream_status"`
	CacheBytes             int64  `json:"cache_bytes"`
	CacheEntries           int    `json:"cache_entries"`
	TrackedKeys            int    `json:"tracked_keys"`
	HotKeys                int    `json:"hot_keys"`
	TotalRequests          uint64 `json:"total_requests"`
	CacheHits              uint64 `json:"cache_hits"`
	CacheMisses            uint64 `json:"cache_misses"`
	UpstreamRequests       uint64 `json:"upstream_requests"`
	RequestsTotal          uint64 `json:"requests_total"`
	CacheHitsTotal         uint64 `json:"cache_hits_total"`
	CacheMissesTotal       uint64 `json:"cache_misses_total"`
	UpstreamRequestsTotal  uint64 `json:"upstream_requests_total"`
	UpstreamGetsTotal      uint64 `json:"upstream_gets_total"`
	InvalidationsTotal     uint64 `json:"invalidations_total"`
	PromotionsTotal        uint64 `json:"promotions_total"`
	DemotionsTotal         uint64 `json:"demotions_total"`
	CoalescedRequestsTotal uint64 `json:"coalesced_requests_total"`
	Promotions             uint64 `json:"promotions"`
	Demotions              uint64 `json:"demotions"`
	CoalescedRequests      uint64 `json:"coalesced_requests"`
}

type HotKey struct {
	ID                string  `json:"id"`
	State             string  `json:"state"`
	Score             float64 `json:"score"`
	RequestRate       float64 `json:"request_rate"`
	LocallyCached     bool    `json:"locally_cached"`
	CacheAge          string  `json:"cache_age,omitempty"`
	RemainingLocalTTL string  `json:"remaining_local_ttl,omitempty"`
}

type CacheInfo struct {
	Entries   int    `json:"entries"`
	Bytes     int64  `json:"bytes"`
	MaxBytes  int64  `json:"max_bytes"`
	Evictions uint64 `json:"evictions"`
}

func New(opts Options) *Service {
	clock := opts.Clock
	if clock == nil {
		clock = systemClock{}
	}
	recorder := opts.Metrics
	if recorder == nil {
		recorder = metrics.New()
	}
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}

	cacheStore := cache.New(opts.Config.Cache.MaxBytes, opts.Config.Cache.MaxEntries, clock)
	tracker := hotness.New(hotness.Config{
		Window:             opts.Config.Hotness.Window,
		EWMAAlpha:          opts.Config.Hotness.EWMAAlpha,
		PromotionThreshold: opts.Config.Hotness.PromotionThreshold,
		DemotionThreshold:  opts.Config.Hotness.DemotionThreshold,
		MinimumHotWindows:  opts.Config.Hotness.MinimumHotWindows,
		Cooldown:           opts.Config.Hotness.Cooldown,
		MaxTrackedKeys:     opts.Config.Hotness.MaxTrackedKeys,
		Clock:              clock,
	})

	version := opts.Version
	if version == "" {
		version = "dev"
	}
	commit := opts.Commit
	if commit == "" {
		commit = "unknown"
	}
	lifetimeCtx, cancelLifetime := context.WithCancel(context.Background())

	return &Service{
		cfg:            opts.Config,
		upstream:       opts.Upstream,
		cache:          cacheStore,
		tracker:        tracker,
		metrics:        recorder,
		logger:         logger,
		clock:          clock,
		policies:       newPolicyMatcher(opts.Config),
		started:        clock.Now(),
		version:        version,
		commit:         commit,
		lifetimeCtx:    lifetimeCtx,
		cancelLifetime: cancelLifetime,
	}
}

func (s *Service) Metrics() *metrics.Recorder {
	return s.metrics
}

func (s *Service) SetProxyActive(active bool) {
	s.proxyActive.Store(active)
}

func (s *Service) Close() error {
	if !s.closed.CompareAndSwap(false, true) {
		return nil
	}
	s.cancelLifetime()
	s.cache.Close()
	if s.upstream != nil {
		return s.upstream.Close()
	}
	return nil
}

func (s *Service) Get(ctx context.Context, key string) (upstream.Value, error) {
	if key == "" {
		return upstream.Value{}, errors.New("key is required")
	}
	if err := ctx.Err(); err != nil {
		return upstream.Value{}, err
	}
	policy := s.policies.Match(key)
	var observedState hotness.State
	if policy.Mode != cachepolicy.ModeDeny {
		observation := s.tracker.ObserveWithState(key)
		s.handleObservation(observation)
		observedState = observation.State
	}

	if policy.Mode == cachepolicy.ModeCache {
		if item, ok := s.getFreshCachedInState(key, observedState); ok {
			s.metrics.CacheHit("GET")
			return upstream.Value{Exists: true, Data: item.Value, PTTL: item.TTL}, nil
		}
	}
	s.metrics.CacheMiss("GET")
	readCtx, cancel := context.WithTimeout(ctx, s.sharedReadTimeout())
	defer cancel()

	if policy.Mode != cachepolicy.ModeCache {
		value, err := s.fetchUpstreamGet(readCtx, key)
		s.observeCache()
		return value, err
	}

	epoch := s.cacheEpoch(key)
	resultCh := s.group.DoChan(key, func() (any, error) {
		// The shared upstream request has its own bounded timeout and must not
		// be canceled by whichever caller happened to become the flight leader.
		flightCtx, cancel := s.sharedGetContext()
		defer cancel()
		value, getErr := s.fetchUpstreamGet(flightCtx, key)
		if getErr != nil {
			return upstream.Value{}, getErr
		}
		if !value.Exists {
			s.reconcileLocalIfCurrent(key, value, epoch, policy)
			return value, nil
		}
		s.reconcileLocalIfCurrent(key, value, epoch, policy)
		return value, nil
	})
	var result singleflight.Result
	select {
	case <-readCtx.Done():
		return upstream.Value{}, readCtx.Err()
	case result = <-resultCh:
	}
	if result.Shared {
		s.metrics.Coalesced()
	}
	if result.Err != nil {
		if s.cfg.Cache.AllowStaleOnUpstreamError {
			if item, ok := s.cache.GetStale(key, s.cfg.Cache.StaleGrace); ok {
				return upstream.Value{Exists: true, Data: item.Value, PTTL: item.TTL}, nil
			}
		}
		return upstream.Value{}, result.Err
	}
	s.observeCache()
	return result.Val.(upstream.Value), nil
}

func (s *Service) fetchUpstreamGet(ctx context.Context, key string) (upstream.Value, error) {
	upCtx, cancel := s.upstreamContext(ctx)
	defer cancel()
	start := s.clock.Now()
	value, err := s.upstream.Get(upCtx, key)
	s.metrics.ObserveUpstream("GET", s.clock.Now().Sub(start), err)
	return value, err
}

func (s *Service) MGet(ctx context.Context, keys []string) ([]upstream.Value, error) {
	out := make([]upstream.Value, len(keys))
	if len(keys) == 0 {
		return out, nil
	}

	type missingRead struct {
		position int
		epoch    uint64
		policy   cachepolicy.Decision
	}
	missingKeys := make([]string, 0, len(keys))
	missingReads := make([]missingRead, 0, len(keys))
	for i, key := range keys {
		policy := s.policies.Match(key)
		var observedState hotness.State
		if policy.Mode != cachepolicy.ModeDeny {
			observation := s.tracker.ObserveWithState(key)
			s.handleObservation(observation)
			observedState = observation.State
		}
		if policy.Mode == cachepolicy.ModeCache {
			if item, ok := s.getFreshCachedInState(key, observedState); ok {
				s.metrics.CacheHit("MGET")
				out[i] = upstream.Value{Exists: true, Data: item.Value, PTTL: item.TTL}
				continue
			}
		}
		s.metrics.CacheMiss("MGET")
		missingKeys = append(missingKeys, key)
		read := missingRead{position: i, policy: policy}
		if policy.Mode == cachepolicy.ModeCache {
			read.epoch = s.cacheEpoch(key)
		}
		missingReads = append(missingReads, read)
	}

	if len(missingKeys) == 0 {
		return out, nil
	}

	upCtx, cancel := s.upstreamContext(ctx)
	defer cancel()
	start := s.clock.Now()
	values, err := s.upstream.MGet(upCtx, missingKeys)
	s.metrics.ObserveUpstream("MGET", s.clock.Now().Sub(start), err)
	if err != nil {
		if s.cfg.Cache.AllowStaleOnUpstreamError {
			staleCount := 0
			for i, key := range missingKeys {
				read := missingReads[i]
				if read.policy.Mode != cachepolicy.ModeCache {
					continue
				}
				if item, ok := s.cache.GetStale(key, s.cfg.Cache.StaleGrace); ok {
					out[read.position] = upstream.Value{Exists: true, Data: item.Value, PTTL: item.TTL}
					staleCount++
				}
			}
			if staleCount == len(missingKeys) {
				return out, nil
			}
		}
		return nil, err
	}
	for i, value := range values {
		key := missingKeys[i]
		read := missingReads[i]
		out[read.position] = value
		if read.policy.Mode == cachepolicy.ModeCache {
			s.reconcileLocalIfCurrent(key, value, read.epoch, read.policy)
		}
	}
	s.observeCache()
	return out, nil
}

func (s *Service) ExecuteWrite(ctx context.Context, command string, args []string, keys []string) (any, error) {
	s.advanceCacheEpochs(keys)
	start := s.clock.Now()
	result, err := s.upstream.Do(ctx, append([]string{command}, args...)...)
	s.metrics.ObserveUpstream(command, s.clock.Now().Sub(start), err)
	invalidated := s.invalidateCacheKeys(keys)
	for _, key := range keys {
		s.group.Forget(key)
	}
	for range invalidated {
		s.metrics.Invalidation("write")
	}
	s.observeCache()
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (s *Service) PassThrough(ctx context.Context, command string, args []string) (any, error) {
	start := s.clock.Now()
	result, err := s.upstream.Do(ctx, append([]string{command}, args...)...)
	s.metrics.ObserveUpstream(command, s.clock.Now().Sub(start), err)
	return result, err
}

func (s *Service) Ready(ctx context.Context) error {
	if !s.proxyActive.Load() {
		return errors.New("proxy listener is not active")
	}
	if s.upstream == nil {
		return errors.New("upstream is not configured")
	}
	return s.upstream.Ping(ctx)
}

func (s *Service) Status(ctx context.Context) Status {
	s.handleTransitions(s.tracker.Advance())
	cacheStats := s.cache.Stats()
	s.metrics.ObserveCache(cacheStats.Entries, cacheStats.Bytes, cacheStats.Evictions)
	tracked, hot := s.tracker.Stats()
	s.metrics.SetHotKeys(hot)
	snap := s.metrics.Snapshot()

	upstreamStatus := "ok"
	if err := s.upstream.Ping(ctx); err != nil {
		upstreamStatus = "error"
	}

	return Status{
		Version:                s.version,
		Commit:                 s.commit,
		Mode:                   s.cfg.Mode,
		KeyVisibility:          config.EffectiveKeyVisibility(s.cfg),
		Uptime:                 s.clock.Now().Sub(s.started).Round(time.Second).String(),
		UpstreamStatus:         upstreamStatus,
		CacheBytes:             cacheStats.Bytes,
		CacheEntries:           cacheStats.Entries,
		TrackedKeys:            tracked,
		HotKeys:                hot,
		TotalRequests:          snap.TotalRequests,
		CacheHits:              snap.CacheHits,
		CacheMisses:            snap.CacheMisses,
		UpstreamRequests:       snap.UpstreamRequests,
		RequestsTotal:          snap.TotalRequests,
		CacheHitsTotal:         snap.CacheHits,
		CacheMissesTotal:       snap.CacheMisses,
		UpstreamRequestsTotal:  snap.UpstreamRequests,
		UpstreamGetsTotal:      snap.UpstreamGets,
		InvalidationsTotal:     snap.Invalidations,
		PromotionsTotal:        snap.Promotions,
		DemotionsTotal:         snap.Demotions,
		CoalescedRequestsTotal: snap.CoalescedRequests,
		Promotions:             snap.Promotions,
		Demotions:              snap.Demotions,
		CoalescedRequests:      snap.CoalescedRequests,
	}
}

func (s *Service) HotKeys(limit int) []HotKey {
	view := s.tracker.AdvanceAndSnapshot(limit)
	s.handleTransitions(view.Transitions)
	visibility := config.EffectiveKeyVisibility(s.cfg)
	out := make([]HotKey, 0, len(view.Snapshots))
	for _, snapshot := range view.Snapshots {
		cacheSnapshot, cached := s.cache.Inspect(snapshot.Key)
		item := HotKey{
			ID:            privacy.KeyIdentifier(snapshot.Key, s.cfg.Privacy.KeyHashSecret, visibility),
			State:         string(snapshot.State),
			Score:         snapshot.Score,
			RequestRate:   snapshot.RequestRate,
			LocallyCached: cached,
		}
		if cached {
			item.CacheAge = cacheSnapshot.Age.Round(time.Millisecond).String()
			item.RemainingLocalTTL = cacheSnapshot.TTL.Round(time.Millisecond).String()
		}
		out = append(out, item)
	}
	return out
}

func (s *Service) CacheInfo() CacheInfo {
	stats := s.cache.Stats()
	s.metrics.ObserveCache(stats.Entries, stats.Bytes, stats.Evictions)
	return CacheInfo{
		Entries:   stats.Entries,
		Bytes:     stats.Bytes,
		MaxBytes:  stats.MaxBytes,
		Evictions: stats.Evictions,
	}
}

func (s *Service) PurgeCache(key string) bool {
	if key == "" {
		s.purgeAllCache()
		s.observeCache()
		return true
	}
	s.advanceCacheEpochs([]string{key})
	ok := s.invalidateCacheKeys([]string{key}) > 0
	s.group.Forget(key)
	if ok {
		s.metrics.Invalidation("admin_purge")
	}
	s.observeCache()
	return ok
}

func (s *Service) getFreshCachedInState(key string, state hotness.State) (cache.EntrySnapshot, bool) {
	if state != hotness.StateHot {
		s.cache.Delete(key)
		return cache.EntrySnapshot{}, false
	}
	if s.cfg.Cache.AllowStaleOnUpstreamError && s.cfg.Cache.StaleGrace > 0 {
		return s.cache.GetStale(key, 0)
	}
	return s.cache.Get(key)
}

func (s *Service) storeLocal(key string, value upstream.Value, policy cachepolicy.Decision) {
	if policy.MaxItemBytes <= 0 || cache.EstimateSize(key, value.Data) > policy.MaxItemBytes {
		s.cache.Delete(key)
		return
	}
	ttl := s.localTTL(value.PTTL, policy.MaxLocalTTL)
	if ttl <= 0 {
		s.cache.Delete(key)
		return
	}
	s.cache.Put(key, value.Data, ttl)
}

func (s *Service) reconcileLocalIfCurrent(key string, value upstream.Value, epoch uint64, policy cachepolicy.Decision) {
	s.withCurrentCacheEpoch(key, epoch, func() {
		if !value.Exists || !s.tracker.IsHot(key) {
			s.cache.Delete(key)
			return
		}
		s.storeLocal(key, value, policy)
	})
}

func (s *Service) cacheEpoch(key string) uint64 {
	return s.cacheEpochs[cacheEpochStripe(key)].epoch.Load()
}

func (s *Service) withCurrentCacheEpoch(key string, epoch uint64, mutate func()) bool {
	stripe := &s.cacheEpochs[cacheEpochStripe(key)]
	stripe.mu.Lock()
	defer stripe.mu.Unlock()
	if stripe.epoch.Load() != epoch {
		return false
	}
	mutate()
	return true
}

func (s *Service) advanceCacheEpochs(keys []string) {
	var advanced [cacheEpochStripeCount]bool
	for _, key := range keys {
		stripe := cacheEpochStripe(key)
		if advanced[stripe] {
			continue
		}
		state := &s.cacheEpochs[stripe]
		state.mu.Lock()
		state.epoch.Add(1)
		state.mu.Unlock()
		advanced[stripe] = true
	}
}

func (s *Service) invalidateCacheKeys(keys []string) int {
	var advanced [cacheEpochStripeCount]bool
	invalidated := 0
	for _, key := range keys {
		stripe := cacheEpochStripe(key)
		state := &s.cacheEpochs[stripe]
		state.mu.Lock()
		if !advanced[stripe] {
			state.epoch.Add(1)
			advanced[stripe] = true
		}
		if s.cache.Delete(key) {
			invalidated++
		}
		state.mu.Unlock()
	}
	return invalidated
}

func (s *Service) purgeAllCache() {
	for i := range s.cacheEpochs {
		s.cacheEpochs[i].mu.Lock()
	}
	defer func() {
		for i := len(s.cacheEpochs) - 1; i >= 0; i-- {
			s.cacheEpochs[i].mu.Unlock()
		}
	}()

	// Purge before advancing the epochs. A refill that captured an old epoch
	// either finishes before this critical section and is deleted, or waits for
	// its stripe and is rejected after the epoch change. A refill that observes
	// the new epoch is ordered after the purge and may populate normally.
	s.cache.Purge()
	for i := range s.cacheEpochs {
		s.cacheEpochs[i].epoch.Add(1)
	}
}

func cacheEpochStripe(key string) int {
	const (
		fnvOffset = uint32(2166136261)
		fnvPrime  = uint32(16777619)
	)
	hash := fnvOffset
	for i := 0; i < len(key); i++ {
		hash ^= uint32(key[i])
		hash *= fnvPrime
	}
	return int(hash % cacheEpochStripeCount)
}

func (s *Service) localTTL(upstreamTTL, policyMaxTTL time.Duration) time.Duration {
	maxTTL := s.cfg.Cache.MaxLocalTTL
	if policyMaxTTL <= 0 {
		return 0
	}
	if policyMaxTTL < maxTTL {
		maxTTL = policyMaxTTL
	}
	if upstreamTTL == 0 {
		return 0
	}
	if upstreamTTL > 0 && upstreamTTL < maxTTL {
		return upstreamTTL
	}
	return maxTTL
}

func (s *Service) handleTransitions(transitions []hotness.Transition) {
	s.applyTransitions(transitions)
	_, hot := s.tracker.Stats()
	s.metrics.SetHotKeys(hot)
	s.metrics.ObserveHotnessOversizedDrops(s.tracker.OversizedObservationsDropped())
}

func (s *Service) handleObservation(observation hotness.Observation) {
	s.applyTransitions(observation.Transitions)
	if len(observation.Transitions) > 0 {
		// Observations are applied after the tracker lock is released. Read the
		// current aggregate on the rare transition path so concurrent callers
		// cannot publish an older snapshot after a newer one.
		_, hot := s.tracker.Stats()
		s.metrics.SetHotKeys(hot)
	}
	if observation.OversizedObservationDropped {
		s.metrics.ObserveHotnessOversizedDrops(observation.OversizedObservationsDropped)
	}
}

func (s *Service) applyTransitions(transitions []hotness.Transition) {
	cacheChanged := false
	for _, transition := range transitions {
		keyID := privacy.HMACKeyIdentifier(transition.Key, s.cfg.Privacy.KeyHashSecret)
		if transition.To == hotness.StateHot && transition.From != hotness.StateHot {
			s.metrics.Promotion()
			s.logger.Info("hot key promoted", "key_id", keyID, "from", transition.From, "to", transition.To)
		}
		if transition.From == hotness.StateHot && transition.To != hotness.StateHot {
			if s.cache.Delete(transition.Key) {
				cacheChanged = true
			}
			s.metrics.Demotion()
			s.logger.Info("hot key demoted", "key_id", keyID, "from", transition.From, "to", transition.To)
		}
	}
	if cacheChanged {
		s.observeCache()
	}
}

func (s *Service) observeCache() {
	stats := s.cache.Stats()
	s.metrics.ObserveCache(stats.Entries, stats.Bytes, stats.Evictions)
}

func (s *Service) upstreamContext(parent context.Context) (context.Context, context.CancelFunc) {
	timeout := s.cfg.Upstream.ReadTimeout
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	return context.WithTimeout(parent, timeout)
}

func (s *Service) sharedGetContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(s.lifetimeCtx, s.sharedReadTimeout())
}

func (s *Service) sharedReadTimeout() time.Duration {
	upstreamTimeout := s.cfg.Upstream.ReadTimeout
	if upstreamTimeout <= 0 {
		upstreamTimeout = 2 * time.Second
	}
	proxyTimeout := s.cfg.Proxy.ReadTimeout
	if proxyTimeout > 0 && proxyTimeout < upstreamTimeout {
		return proxyTimeout
	}
	return upstreamTimeout
}
