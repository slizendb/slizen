package proxy

import (
	"context"
	"io"
	"log/slog"
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
}

func (c *benchmarkConn) WriteBulk(value []byte) {
	c.bulkBytes += int64(len(value))
}

func BenchmarkProxyGETCacheHit(b *testing.B) {
	const key = "key"
	value := []byte("value")
	cfg := config.Default()
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

	server := NewServer(cfg.Proxy, svc, logger)
	command := redcon.Command{Args: [][]byte{[]byte("GET"), []byte(key)}}
	conn := &benchmarkConn{}
	server.handle(conn, command)
	if conn.bulkBytes != int64(len(value)) {
		b.Fatalf("warm response bytes = %d, want %d", conn.bulkBytes, len(value))
	}
	if got := up.GetCallCount(key); got != 2 {
		b.Fatalf("warm request reached upstream: calls=%d", got)
	}
	conn.bulkBytes = 0

	b.ReportAllocs()
	for b.Loop() {
		server.handle(conn, command)
	}

	if got := up.GetCallCount(key); got != 2 {
		b.Fatalf("benchmark reached upstream: calls=%d", got)
	}
	if want := int64(b.N * len(value)); conn.bulkBytes != want {
		b.Fatalf("response bytes = %d, want %d", conn.bulkBytes, want)
	}
}
