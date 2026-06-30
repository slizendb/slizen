package service

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/slizendb/slizen/internal/cache"
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
	started  time.Time
	version  string
	commit   string

	proxyActive atomic.Bool
	closed      atomic.Bool
	group       singleflight.Group
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

	return &Service{
		cfg:      opts.Config,
		upstream: opts.Upstream,
		cache:    cacheStore,
		tracker:  tracker,
		metrics:  recorder,
		logger:   logger,
		clock:    clock,
		started:  clock.Now(),
		version:  version,
		commit:   commit,
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
	s.handleTransitions(s.tracker.Observe(key))

	if s.cacheEnabled() {
		if item, ok := s.getFreshCached(key); ok {
			s.metrics.CacheHit("GET")
			s.observeCache()
			return upstream.Value{Exists: true, Data: item.Value, PTTL: item.TTL}, nil
		}
	}
	s.metrics.CacheMiss("GET")

	if !s.cacheEnabled() {
		value, err := s.fetchUpstreamGet(ctx, key)
		s.observeCache()
		return value, err
	}

	valueAny, err, shared := s.group.Do(key, func() (any, error) {
		value, getErr := s.fetchUpstreamGet(ctx, key)
		if getErr != nil {
			return upstream.Value{}, getErr
		}
		if !value.Exists {
			if s.cacheEnabled() {
				s.cache.Delete(key)
			}
			return value, nil
		}
		if s.cacheEnabled() && s.tracker.IsHot(key) {
			s.storeLocal(key, value)
		}
		return value, nil
	})
	if shared {
		s.metrics.Coalesced()
	}
	if err != nil {
		if s.cacheEnabled() && s.cfg.Cache.AllowStaleOnUpstreamError {
			if item, ok := s.cache.GetStale(key, s.cfg.Cache.StaleGrace); ok {
				return upstream.Value{Exists: true, Data: item.Value, PTTL: item.TTL}, nil
			}
		}
		return upstream.Value{}, err
	}
	s.observeCache()
	return valueAny.(upstream.Value), nil
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

	missingKeys := make([]string, 0, len(keys))
	missingPositions := make([]int, 0, len(keys))
	for i, key := range keys {
		s.handleTransitions(s.tracker.Observe(key))
		if s.cacheEnabled() {
			if item, ok := s.getFreshCached(key); ok {
				s.metrics.CacheHit("MGET")
				out[i] = upstream.Value{Exists: true, Data: item.Value, PTTL: item.TTL}
				continue
			}
		}
		s.metrics.CacheMiss("MGET")
		missingKeys = append(missingKeys, key)
		missingPositions = append(missingPositions, i)
	}

	if len(missingKeys) == 0 {
		s.observeCache()
		return out, nil
	}

	upCtx, cancel := s.upstreamContext(ctx)
	defer cancel()
	start := s.clock.Now()
	values, err := s.upstream.MGet(upCtx, missingKeys)
	s.metrics.ObserveUpstream("MGET", s.clock.Now().Sub(start), err)
	if err != nil {
		if s.cacheEnabled() && s.cfg.Cache.AllowStaleOnUpstreamError {
			staleCount := 0
			for i, key := range missingKeys {
				pos := missingPositions[i]
				if item, ok := s.cache.GetStale(key, s.cfg.Cache.StaleGrace); ok {
					out[pos] = upstream.Value{Exists: true, Data: item.Value, PTTL: item.TTL}
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
		pos := missingPositions[i]
		out[pos] = value
		if !value.Exists {
			if s.cacheEnabled() {
				s.cache.Delete(key)
			}
			continue
		}
		if s.cacheEnabled() && s.tracker.IsHot(key) {
			s.storeLocal(key, value)
		}
	}
	s.observeCache()
	return out, nil
}

func (s *Service) ExecuteWrite(ctx context.Context, command string, args []string, keys []string) (any, error) {
	start := s.clock.Now()
	result, err := s.upstream.Do(ctx, append([]string{command}, args...)...)
	s.metrics.ObserveUpstream(command, s.clock.Now().Sub(start), err)
	if err != nil {
		return nil, err
	}
	for _, key := range keys {
		if s.cache.Delete(key) {
			s.metrics.Invalidation("write")
		}
	}
	s.observeCache()
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
	s.handleTransitions(s.tracker.Advance())
	snapshots := s.tracker.Snapshots(limit)
	visibility := config.EffectiveKeyVisibility(s.cfg)
	out := make([]HotKey, 0, len(snapshots))
	for _, snapshot := range snapshots {
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
		s.cache.Purge()
		s.observeCache()
		return true
	}
	ok := s.cache.Delete(key)
	if ok {
		s.metrics.Invalidation("admin_purge")
	}
	s.observeCache()
	return ok
}

func (s *Service) getFreshCached(key string) (cache.EntrySnapshot, bool) {
	if s.cfg.Cache.AllowStaleOnUpstreamError && s.cfg.Cache.StaleGrace > 0 {
		return s.cache.GetStale(key, 0)
	}
	return s.cache.Get(key)
}

func (s *Service) cacheEnabled() bool {
	return s.cfg.Mode == "cache"
}

func (s *Service) storeLocal(key string, value upstream.Value) {
	ttl := s.localTTL(value.PTTL)
	if ttl <= 0 {
		return
	}
	s.cache.Put(key, value.Data, ttl)
}

func (s *Service) localTTL(upstreamTTL time.Duration) time.Duration {
	maxTTL := s.cfg.Cache.MaxLocalTTL
	if upstreamTTL > 0 && upstreamTTL < maxTTL {
		return upstreamTTL
	}
	return maxTTL
}

func (s *Service) handleTransitions(transitions []hotness.Transition) {
	for _, transition := range transitions {
		keyID := privacy.HMACKeyIdentifier(transition.Key, s.cfg.Privacy.KeyHashSecret)
		if transition.To == hotness.StateHot && transition.From != hotness.StateHot {
			s.metrics.Promotion()
			s.logger.Info("hot key promoted", "key_id", keyID, "from", transition.From, "to", transition.To)
		}
		if transition.From == hotness.StateHot && transition.To != hotness.StateHot {
			s.cache.Delete(transition.Key)
			s.metrics.Demotion()
			s.logger.Info("hot key demoted", "key_id", keyID, "from", transition.From, "to", transition.To)
		}
	}
	_, hot := s.tracker.Stats()
	s.metrics.SetHotKeys(hot)
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
