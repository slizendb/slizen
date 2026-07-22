package proxy

import (
	"context"
	"io"
	"log/slog"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tidwall/redcon"

	"github.com/slizendb/slizen/internal/config"
	"github.com/slizendb/slizen/internal/service"
	"github.com/slizendb/slizen/internal/testutil"
)

type benchmarkConn struct {
	fakeConn
	bulkBytes int64
	netConn   benchmarkNetConn
}

func (c *benchmarkConn) WriteBulk(value []byte) {
	c.bulkBytes += int64(len(value))
}

func (c *benchmarkConn) NetConn() net.Conn {
	return &c.netConn
}

type benchmarkNetConn struct{}

func (*benchmarkNetConn) Read([]byte) (int, error)         { return 0, io.EOF }
func (*benchmarkNetConn) Write(value []byte) (int, error)  { return len(value), nil }
func (*benchmarkNetConn) Close() error                     { return nil }
func (*benchmarkNetConn) LocalAddr() net.Addr              { return nil }
func (*benchmarkNetConn) RemoteAddr() net.Addr             { return nil }
func (*benchmarkNetConn) SetDeadline(time.Time) error      { return nil }
func (*benchmarkNetConn) SetReadDeadline(time.Time) error  { return nil }
func (*benchmarkNetConn) SetWriteDeadline(time.Time) error { return nil }

type proxyGETCacheHitBenchmark struct {
	server            *Server
	command           redcon.Command
	upstream          *testutil.FakeUpstream
	responseBytes     int64
	setupUpstreamGETs int
}

func newProxyGETCacheHitBenchmark(b *testing.B) proxyGETCacheHitBenchmark {
	b.Helper()
	const key = "key"
	value := []byte("value")
	cfg := config.Default()
	cfg.Mode = "cache"
	cfg.Hotness.Window = time.Second
	cfg.Hotness.EWMAAlpha = 1
	cfg.Hotness.PromotionThreshold = 1
	cfg.Hotness.DemotionThreshold = 0.1
	cfg.Hotness.MinimumHotWindows = 1

	clock := testutil.NewFakeClock(time.Unix(0, 0))
	up := testutil.NewFakeUpstream()
	up.Put(key, value, 0)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := service.New(service.Options{
		Config:   cfg,
		Upstream: up,
		Logger:   logger,
		Clock:    clock,
	})
	b.Cleanup(func() {
		if err := svc.Close(); err != nil {
			b.Errorf("close service: %v", err)
		}
	})

	ctx := context.Background()
	if _, err := svc.Get(ctx, key); err != nil {
		b.Fatal(err)
	}
	clock.Advance(time.Second)
	if _, err := svc.Get(ctx, key); err != nil {
		b.Fatal(err)
	}
	if got := up.GetCallCount(key); got != 2 {
		b.Fatalf("setup upstream GETs = %d, want 2", got)
	}

	benchmark := proxyGETCacheHitBenchmark{
		server:            NewServer(cfg.Proxy, svc, logger),
		command:           redcon.Command{Args: [][]byte{[]byte("GET"), []byte(key)}},
		upstream:          up,
		responseBytes:     int64(len(value)),
		setupUpstreamGETs: 2,
	}
	conn := &benchmarkConn{}
	benchmark.server.handle(conn, benchmark.command)
	if conn.bulkBytes != int64(len(value)) {
		b.Fatalf("warm response bytes = %d, want %d", conn.bulkBytes, len(value))
	}
	if got := up.GetCallCount(key); got != 2 {
		b.Fatalf("warm request reached upstream: calls=%d", got)
	}
	return benchmark
}

func BenchmarkProxyGETCacheHit(b *testing.B) {
	benchmark := newProxyGETCacheHitBenchmark(b)
	conn := &benchmarkConn{}

	b.ReportAllocs()
	for b.Loop() {
		benchmark.server.handle(conn, benchmark.command)
	}

	if got := benchmark.upstream.GetCallCount("key"); got != benchmark.setupUpstreamGETs {
		b.Fatalf("benchmark reached upstream: calls=%d", got)
	}
	if want := int64(b.N) * benchmark.responseBytes; conn.bulkBytes != want {
		b.Fatalf("response bytes = %d, want %d", conn.bulkBytes, want)
	}
}

func BenchmarkProxyGETCacheHitParallel(b *testing.B) {
	benchmark := newProxyGETCacheHitBenchmark(b)
	var responseBytes atomic.Int64

	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		conn := &benchmarkConn{}
		for pb.Next() {
			benchmark.server.dispatch(conn, benchmark.command)
		}
		responseBytes.Add(conn.bulkBytes)
	})
	b.StopTimer()

	if got := benchmark.upstream.GetCallCount("key"); got != benchmark.setupUpstreamGETs {
		b.Fatalf("parallel benchmark reached upstream: calls=%d", got)
	}
	if want := int64(b.N) * benchmark.responseBytes; responseBytes.Load() != want {
		b.Fatalf("parallel response bytes = %d, want %d", responseBytes.Load(), want)
	}
}
