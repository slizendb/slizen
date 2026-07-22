package metrics

import (
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// CacheMissReason is a bounded cache miss classification. Its metric label is
// selected internally so callers cannot introduce arbitrary label values.
type CacheMissReason uint8

const (
	CacheMissReasonUnclassified CacheMissReason = iota
	CacheMissReasonPolicyBypass
	CacheMissReasonNotAdmitted
	CacheMissReasonNotPresent
	cacheMissReasonCount
)

type Snapshot struct {
	TotalRequests           uint64 `json:"total_requests"`
	CacheHits               uint64 `json:"cache_hits"`
	CacheMisses             uint64 `json:"cache_misses"`
	CacheMissesUnclassified uint64 `json:"cache_misses_unclassified"`
	CacheMissesPolicyBypass uint64 `json:"cache_misses_policy_bypass"`
	CacheMissesNotAdmitted  uint64 `json:"cache_misses_not_admitted"`
	CacheMissesNotPresent   uint64 `json:"cache_misses_not_present"`
	UpstreamRequests        uint64 `json:"upstream_requests"`
	UpstreamGets            uint64 `json:"upstream_gets"`
	UpstreamErrors          uint64 `json:"upstream_errors"`
	Promotions              uint64 `json:"promotions"`
	Demotions               uint64 `json:"demotions"`
	Invalidations           uint64 `json:"invalidations"`
	CoalescedRequests       uint64 `json:"coalesced_requests"`
}

type Recorder struct {
	registry *prometheus.Registry

	requests              *prometheus.CounterVec
	requestLatency        *prometheus.HistogramVec
	cacheHits             *prometheus.CounterVec
	cacheMisses           *prometheus.CounterVec
	cacheMissReasons      *prometheus.CounterVec
	cacheMissByReason     [cacheMissReasonCount]prometheus.Counter
	getRequestOK          prometheus.Counter
	getRequestOKLatency   prometheus.Observer
	getCacheHits          prometheus.Counter
	cacheEntries          prometheus.Gauge
	cacheBytes            prometheus.Gauge
	cacheEvictions        prometheus.Counter
	upstreamReqs          *prometheus.CounterVec
	upstreamErrors        *prometheus.CounterVec
	upstreamLatency       *prometheus.HistogramVec
	hotKeys               prometheus.Gauge
	promotions            prometheus.Counter
	demotions             prometheus.Counter
	invalidations         *prometheus.CounterVec
	coalesced             prometheus.Counter
	hotnessCapacityDrops  prometheus.Counter
	hotnessOversizedDrops prometheus.Counter

	totalRequests            atomic.Uint64
	totalCacheHits           atomic.Uint64
	totalCacheMisses         atomic.Uint64
	totalCacheMissesByReason [cacheMissReasonCount]atomic.Uint64
	totalUpstreamReqs        atomic.Uint64
	totalUpstreamGets        atomic.Uint64
	totalUpstreamErrs        atomic.Uint64
	totalPromotions          atomic.Uint64
	totalDemotions           atomic.Uint64
	totalInvalidates         atomic.Uint64
	totalCoalesced           atomic.Uint64
	evictionSeen             atomic.Uint64
	hotnessCapacityDropSeen  atomic.Uint64
	hotnessOversizedDropSeen atomic.Uint64
}

func New() *Recorder {
	r := &Recorder{registry: prometheus.NewRegistry()}
	r.requests = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "slizen_requests_total", Help: "Total Slizen proxy requests."}, []string{"command", "result"})
	r.requestLatency = prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "slizen_request_duration_seconds", Help: "Slizen proxy request duration.", Buckets: prometheus.DefBuckets}, []string{"command", "result"})
	r.cacheHits = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "slizen_cache_hits_total", Help: "Total local cache hits."}, []string{"command"})
	r.cacheMisses = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "slizen_cache_misses_total", Help: "Total local cache misses."}, []string{"command"})
	r.cacheMissReasons = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "slizen_cache_miss_reasons_total", Help: "Total local cache misses by bounded reason."}, []string{"reason"})
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
	r.hotnessCapacityDrops = prometheus.NewCounter(prometheus.CounterOpts{Name: "slizen_hotness_capacity_observations_dropped_total", Help: "Total unseen-key observations skipped to avoid evicting HOT tracker entries at the configured capacity."})
	r.hotnessOversizedDrops = prometheus.NewCounter(prometheus.CounterOpts{Name: "slizen_hotness_oversized_observations_dropped_total", Help: "Total observations skipped because the Redis key exceeded the hotness tracking byte limit."})
	r.getRequestOK = r.requests.WithLabelValues("GET", "ok")
	r.getRequestOKLatency = r.requestLatency.WithLabelValues("GET", "ok")
	r.getCacheHits = r.cacheHits.WithLabelValues("GET")
	for reason := CacheMissReasonUnclassified; reason < cacheMissReasonCount; reason++ {
		r.cacheMissByReason[reason] = r.cacheMissReasons.WithLabelValues(cacheMissReasonLabel(reason))
	}

	r.registry.MustRegister(
		r.requests,
		r.requestLatency,
		r.cacheHits,
		r.cacheMisses,
		r.cacheMissReasons,
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
		r.hotnessCapacityDrops,
		r.hotnessOversizedDrops,
	)
	return r
}

func (r *Recorder) Handler() http.Handler {
	return promhttp.HandlerFor(r.registry, promhttp.HandlerOpts{})
}

func (r *Recorder) ObserveRequest(command, result string, d time.Duration) {
	command = commandLabel(command)
	r.totalRequests.Add(1)
	if command == "GET" && result == "ok" {
		r.getRequestOK.Inc()
		r.getRequestOKLatency.Observe(d.Seconds())
		return
	}
	r.requests.WithLabelValues(command, result).Inc()
	r.requestLatency.WithLabelValues(command, result).Observe(d.Seconds())
}

func (r *Recorder) CacheHit(command string) {
	command = commandLabel(command)
	r.totalCacheHits.Add(1)
	if command == "GET" {
		r.getCacheHits.Inc()
		return
	}
	r.cacheHits.WithLabelValues(command).Inc()
}

func (r *Recorder) CacheMiss(command string) {
	r.CacheMissWithReason(command, CacheMissReasonUnclassified)
}

func (r *Recorder) CacheMissWithReason(command string, reason CacheMissReason) {
	command = commandLabel(command)
	reason = normalizeCacheMissReason(reason)
	r.totalCacheMisses.Add(1)
	r.totalCacheMissesByReason[reason].Add(1)
	r.cacheMisses.WithLabelValues(command).Inc()
	r.cacheMissByReason[reason].Inc()
}

func normalizeCacheMissReason(reason CacheMissReason) CacheMissReason {
	if reason >= cacheMissReasonCount {
		return CacheMissReasonUnclassified
	}
	return reason
}

func cacheMissReasonLabel(reason CacheMissReason) string {
	switch normalizeCacheMissReason(reason) {
	case CacheMissReasonPolicyBypass:
		return "policy_bypass"
	case CacheMissReasonNotAdmitted:
		return "not_admitted"
	case CacheMissReasonNotPresent:
		return "not_present"
	default:
		return "unclassified"
	}
}

func (r *Recorder) ObserveCache(entries int, bytes int64, evictions uint64) {
	r.cacheEntries.Set(float64(entries))
	r.cacheBytes.Set(float64(bytes))
	if delta := advanceHighWater(&r.evictionSeen, evictions); delta > 0 {
		r.cacheEvictions.Add(float64(delta))
	}
}

func (r *Recorder) ObserveUpstream(command string, d time.Duration, err error) {
	command = commandLabel(command)
	r.totalUpstreamReqs.Add(1)
	if command == "GET" {
		r.totalUpstreamGets.Add(1)
	}
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

func (r *Recorder) ObserveHotnessOversizedDrops(total uint64) {
	if delta := advanceHighWater(&r.hotnessOversizedDropSeen, total); delta > 0 {
		r.hotnessOversizedDrops.Add(float64(delta))
	}
}

func (r *Recorder) ObserveHotnessCapacityDrops(total uint64) {
	if delta := advanceHighWater(&r.hotnessCapacityDropSeen, total); delta > 0 {
		r.hotnessCapacityDrops.Add(float64(delta))
	}
}

func advanceHighWater(mark *atomic.Uint64, total uint64) uint64 {
	for {
		previous := mark.Load()
		if total <= previous {
			return 0
		}
		if mark.CompareAndSwap(previous, total) {
			return total - previous
		}
	}
}

func (r *Recorder) Snapshot() Snapshot {
	return Snapshot{
		TotalRequests:           r.totalRequests.Load(),
		CacheHits:               r.totalCacheHits.Load(),
		CacheMisses:             r.totalCacheMisses.Load(),
		CacheMissesUnclassified: r.totalCacheMissesByReason[CacheMissReasonUnclassified].Load(),
		CacheMissesPolicyBypass: r.totalCacheMissesByReason[CacheMissReasonPolicyBypass].Load(),
		CacheMissesNotAdmitted:  r.totalCacheMissesByReason[CacheMissReasonNotAdmitted].Load(),
		CacheMissesNotPresent:   r.totalCacheMissesByReason[CacheMissReasonNotPresent].Load(),
		UpstreamRequests:        r.totalUpstreamReqs.Load(),
		UpstreamGets:            r.totalUpstreamGets.Load(),
		UpstreamErrors:          r.totalUpstreamErrs.Load(),
		Promotions:              r.totalPromotions.Load(),
		Demotions:               r.totalDemotions.Load(),
		Invalidations:           r.totalInvalidates.Load(),
		CoalescedRequests:       r.totalCoalesced.Load(),
	}
}
