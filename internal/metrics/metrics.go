package metrics

import (
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type Snapshot struct {
	TotalRequests     uint64 `json:"total_requests"`
	CacheHits         uint64 `json:"cache_hits"`
	CacheMisses       uint64 `json:"cache_misses"`
	UpstreamRequests  uint64 `json:"upstream_requests"`
	UpstreamErrors    uint64 `json:"upstream_errors"`
	Promotions        uint64 `json:"promotions"`
	Demotions         uint64 `json:"demotions"`
	Invalidations     uint64 `json:"invalidations"`
	CoalescedRequests uint64 `json:"coalesced_requests"`
}

type Recorder struct {
	registry *prometheus.Registry

	requests        *prometheus.CounterVec
	requestLatency  *prometheus.HistogramVec
	cacheHits       *prometheus.CounterVec
	cacheMisses     *prometheus.CounterVec
	cacheEntries    prometheus.Gauge
	cacheBytes      prometheus.Gauge
	cacheEvictions  prometheus.Counter
	upstreamReqs    *prometheus.CounterVec
	upstreamErrors  *prometheus.CounterVec
	upstreamLatency *prometheus.HistogramVec
	hotKeys         prometheus.Gauge
	promotions      prometheus.Counter
	demotions       prometheus.Counter
	invalidations   *prometheus.CounterVec
	coalesced       prometheus.Counter

	totalRequests     atomic.Uint64
	totalCacheHits    atomic.Uint64
	totalCacheMisses  atomic.Uint64
	totalUpstreamReqs atomic.Uint64
	totalUpstreamErrs atomic.Uint64
	totalPromotions   atomic.Uint64
	totalDemotions    atomic.Uint64
	totalInvalidates  atomic.Uint64
	totalCoalesced    atomic.Uint64
	evictionSeen      atomic.Uint64
}

func New() *Recorder {
	r := &Recorder{registry: prometheus.NewRegistry()}
	r.requests = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "slizen_requests_total", Help: "Total Slizen proxy requests."}, []string{"command", "result"})
	r.requestLatency = prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "slizen_request_duration_seconds", Help: "Slizen proxy request duration.", Buckets: prometheus.DefBuckets}, []string{"command", "result"})
	r.cacheHits = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "slizen_cache_hits_total", Help: "Total local cache hits."}, []string{"command"})
	r.cacheMisses = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "slizen_cache_misses_total", Help: "Total local cache misses."}, []string{"command"})
	r.cacheEntries = prometheus.NewGauge(prometheus.GaugeOpts{Name: "slizen_cache_entries", Help: "Current local cache entries."})
	r.cacheBytes = prometheus.NewGauge(prometheus.GaugeOpts{Name: "slizen_cache_bytes", Help: "Approximate local cache bytes."})
	r.cacheEvictions = prometheus.NewCounter(prometheus.CounterOpts{Name: "slizen_cache_evictions_total", Help: "Total local cache evictions."})
	r.upstreamReqs = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "slizen_upstream_requests_total", Help: "Total upstream Redis or Valkey requests."}, []string{"command"})
	r.upstreamErrors = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "slizen_upstream_errors_total", Help: "Total upstream Redis or Valkey errors."}, []string{"command"})
	r.upstreamLatency = prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "slizen_upstream_duration_seconds", Help: "Upstream request duration.", Buckets: prometheus.DefBuckets}, []string{"command", "result"})
	r.hotKeys = prometheus.NewGauge(prometheus.GaugeOpts{Name: "slizen_hot_keys", Help: "Current hot key count."})
	r.promotions = prometheus.NewCounter(prometheus.CounterOpts{Name: "slizen_promotions_total", Help: "Total hot-key promotions."})
	r.demotions = prometheus.NewCounter(prometheus.CounterOpts{Name: "slizen_demotions_total", Help: "Total hot-key demotions."})
	r.invalidations = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "slizen_invalidations_total", Help: "Total write-driven cache invalidations."}, []string{"reason"})
	r.coalesced = prometheus.NewCounter(prometheus.CounterOpts{Name: "slizen_coalesced_requests_total", Help: "Total cache-miss requests served by singleflight coalescing."})

	r.registry.MustRegister(
		r.requests,
		r.requestLatency,
		r.cacheHits,
		r.cacheMisses,
		r.cacheEntries,
		r.cacheBytes,
		r.cacheEvictions,
		r.upstreamReqs,
		r.upstreamErrors,
		r.upstreamLatency,
		r.hotKeys,
		r.promotions,
		r.demotions,
		r.invalidations,
		r.coalesced,
	)
	return r
}

func (r *Recorder) Handler() http.Handler {
	return promhttp.HandlerFor(r.registry, promhttp.HandlerOpts{})
}

func (r *Recorder) ObserveRequest(command, result string, d time.Duration) {
	command = commandLabel(command)
	r.totalRequests.Add(1)
	r.requests.WithLabelValues(command, result).Inc()
	r.requestLatency.WithLabelValues(command, result).Observe(d.Seconds())
}

func (r *Recorder) CacheHit(command string) {
	command = commandLabel(command)
	r.totalCacheHits.Add(1)
	r.cacheHits.WithLabelValues(command).Inc()
}

func (r *Recorder) CacheMiss(command string) {
	command = commandLabel(command)
	r.totalCacheMisses.Add(1)
	r.cacheMisses.WithLabelValues(command).Inc()
}

func (r *Recorder) ObserveCache(entries int, bytes int64, evictions uint64) {
	r.cacheEntries.Set(float64(entries))
	r.cacheBytes.Set(float64(bytes))
	previous := r.evictionSeen.Swap(evictions)
	if evictions > previous {
		r.cacheEvictions.Add(float64(evictions - previous))
	}
}

func (r *Recorder) ObserveUpstream(command string, d time.Duration, err error) {
	command = commandLabel(command)
	r.totalUpstreamReqs.Add(1)
	r.upstreamReqs.WithLabelValues(command).Inc()
	result := "ok"
	if err != nil {
		result = "error"
		r.totalUpstreamErrs.Add(1)
		r.upstreamErrors.WithLabelValues(command).Inc()
	}
	r.upstreamLatency.WithLabelValues(command, result).Observe(d.Seconds())
}

func commandLabel(command string) string {
	switch strings.ToUpper(command) {
	case "PING", "QUIT", "SELECT", "GET", "MGET", "SET", "SETEX", "PSETEX", "DEL", "UNLINK", "EXPIRE", "PEXPIRE", "PERSIST", "TTL", "PTTL", "EXISTS":
		return strings.ToUpper(command)
	case "MULTI", "EXEC", "WATCH", "UNWATCH", "SUBSCRIBE", "PSUBSCRIBE", "SSUBSCRIBE", "MONITOR", "BLPOP", "BRPOP", "BRPOPLPUSH", "BLMOVE", "BZPOPMIN", "BZPOPMAX", "XREAD", "XREADGROUP":
		return "unsafe"
	case "", "UNKNOWN":
		return "invalid"
	default:
		return "unsupported"
	}
}

func (r *Recorder) Promotion() {
	r.totalPromotions.Add(1)
	r.promotions.Inc()
}

func (r *Recorder) Demotion() {
	r.totalDemotions.Add(1)
	r.demotions.Inc()
}

func (r *Recorder) Invalidation(reason string) {
	r.totalInvalidates.Add(1)
	r.invalidations.WithLabelValues(reason).Inc()
}

func (r *Recorder) Coalesced() {
	r.totalCoalesced.Add(1)
	r.coalesced.Inc()
}

func (r *Recorder) SetHotKeys(count int) {
	r.hotKeys.Set(float64(count))
}

func (r *Recorder) Snapshot() Snapshot {
	return Snapshot{
		TotalRequests:     r.totalRequests.Load(),
		CacheHits:         r.totalCacheHits.Load(),
		CacheMisses:       r.totalCacheMisses.Load(),
		UpstreamRequests:  r.totalUpstreamReqs.Load(),
		UpstreamErrors:    r.totalUpstreamErrs.Load(),
		Promotions:        r.totalPromotions.Load(),
		Demotions:         r.totalDemotions.Load(),
		Invalidations:     r.totalInvalidates.Load(),
		CoalescedRequests: r.totalCoalesced.Load(),
	}
}
