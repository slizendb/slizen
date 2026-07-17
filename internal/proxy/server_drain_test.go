package proxy

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/slizendb/slizen/internal/config"
	"github.com/slizendb/slizen/internal/service"
	"github.com/slizendb/slizen/internal/testutil"
	"github.com/slizendb/slizen/internal/upstream"
)

func TestShutdownWaitsForActiveHandlerAndFlushesResponse(t *testing.T) {
	cfg := drainTestConfig()
	up := newBlockingGetUpstream(false)
	server, cancel, startDone := startDrainTestServer(t, cfg, up)
	conn := dialDrainTestServer(t, server)

	if _, err := io.WriteString(conn, "*2\r\n$3\r\nGET\r\n$3\r\nkey\r\n*3\r\n$3\r\nSET\r\n$3\r\nkey\r\n$3\r\nnew\r\n"); err != nil {
		t.Fatal(err)
	}
	waitClosed(t, up.entered, "GET to enter upstream")

	cancel()
	waitClosed(t, server.drain.started, "proxy drain to start")
	active, connections := server.drain.snapshot()
	if active != 1 || connections != 1 {
		t.Fatalf("drain snapshot = active %d, connections %d; want 1, 1", active, connections)
	}
	select {
	case err := <-startDone:
		t.Fatalf("Start returned before active handler completed: %v", err)
	default:
	}
	requireNewConnectionRejected(t, server)

	up.unblock()
	if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	response := make([]byte, len("$5\r\nvalue\r\n"))
	if _, err := io.ReadFull(conn, response); err != nil {
		t.Fatalf("read completed GET response: %v", err)
	}
	if got, want := string(response), "$5\r\nvalue\r\n"; got != want {
		t.Fatalf("response = %q, want %q", got, want)
	}
	if err := waitStartDone(t, startDone); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	if calls := up.doCalls.Load(); calls != 0 {
		t.Fatalf("pipelined SET reached upstream after drain started: %d calls", calls)
	}
}

func TestShutdownDeadlineDoesNotWaitForStuckHandler(t *testing.T) {
	cfg := drainTestConfig()
	cfg.Proxy.ShutdownTimeout = 25 * time.Millisecond
	up := newBlockingGetUpstream(true)
	server, cancel, startDone := startDrainTestServer(t, cfg, up)
	conn := dialDrainTestServer(t, server)

	if _, err := io.WriteString(conn, "*2\r\n$3\r\nGET\r\n$3\r\nkey\r\n"); err != nil {
		t.Fatal(err)
	}
	waitClosed(t, up.entered, "GET to enter upstream")
	cancel()
	waitClosed(t, server.drain.started, "proxy drain to start")
	if err := waitStartDone(t, startDone); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}

	select {
	case <-up.finished:
		t.Fatal("stuck handler unexpectedly completed before release")
	default:
	}
	select {
	case <-server.drain.drained:
		t.Fatal("drain completed while the stuck handler was still active")
	default:
	}
	up.unblock()
	waitClosed(t, up.finished, "stuck handler release")
	waitClosed(t, server.drain.drained, "forced handler cleanup")
}

func TestShutdownClosesPartialRequestClientWithoutIdleTimeout(t *testing.T) {
	cfg := drainTestConfig()
	cfg.Proxy.ReadTimeout = 30 * time.Second
	cfg.Proxy.ShutdownTimeout = 5 * time.Second
	server, cancel, startDone := startDrainTestServer(t, cfg, testutil.NewFakeUpstream())
	conn := dialDrainTestServer(t, server)
	reader := bufio.NewReader(conn)

	if _, err := io.WriteString(conn, "*1\r\n$4\r\nPING\r\n"); err != nil {
		t.Fatal(err)
	}
	if response, err := reader.ReadString('\n'); err != nil || response != "+PONG\r\n" {
		t.Fatalf("PING response = %q, %v", response, err)
	}
	if _, err := io.WriteString(conn, "*2\r\n$3\r\nGET\r\n$100\r\npartial"); err != nil {
		t.Fatal(err)
	}

	cancel()
	waitClosed(t, server.drain.started, "proxy drain to start")
	if err := waitStartDone(t, startDone); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.ReadByte(); err == nil {
		t.Fatal("partial-request client remained open after shutdown")
	} else {
		var netErr net.Error
		if errors.As(err, &netErr) && netErr.Timeout() {
			t.Fatal("partial-request client timed out instead of being closed")
		}
	}
}

func TestServerCloseIsIdempotent(t *testing.T) {
	cfg := drainTestConfig()
	server, _, startDone := startDrainTestServer(t, cfg, testutil.NewFakeUpstream())
	errs := make(chan error, 2)
	go func() { errs <- server.Close() }()
	go func() { errs <- server.Close() }()

	for range 2 {
		select {
		case err := <-errs:
			if err != nil {
				t.Fatalf("Close returned error: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("concurrent Close call did not return")
		}
	}
	if err := waitStartDone(t, startDone); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
}

func TestCloseStopsHandlerAdmissionSynchronously(t *testing.T) {
	cfg := drainTestConfig()
	up := newBlockingGetUpstream(true)
	server, _, startDone := startDrainTestServer(t, cfg, up)
	conn := dialDrainTestServer(t, server)
	if _, err := io.WriteString(conn, "*2\r\n$3\r\nGET\r\n$3\r\nkey\r\n*3\r\n$3\r\nSET\r\n$3\r\nkey\r\n$3\r\nnew\r\n"); err != nil {
		t.Fatal(err)
	}
	waitClosed(t, up.entered, "GET to enter upstream")

	if err := server.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
	select {
	case <-server.drain.started:
	default:
		t.Fatal("Close returned before handler admission was stopped")
	}
	up.unblock()
	waitClosed(t, server.drain.drained, "forced handler cleanup")
	if calls := up.doCalls.Load(); calls != 0 {
		t.Fatalf("pipelined SET reached upstream after Close: %d calls", calls)
	}
	if err := waitStartDone(t, startDone); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
}

func TestCloseBeforeStartIsTerminal(t *testing.T) {
	cfg := drainTestConfig()
	svc := service.New(service.Options{Config: cfg, Upstream: testutil.NewFakeUpstream()})
	t.Cleanup(func() { _ = svc.Close() })
	server := NewServer(cfg.Proxy, svc, nil)
	if err := server.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
	if err := server.Start(context.Background()); err == nil {
		t.Fatal("Start unexpectedly succeeded after Close")
	}
}

func TestServerRejectsSecondStart(t *testing.T) {
	cfg := drainTestConfig()
	server, cancel, startDone := startDrainTestServer(t, cfg, testutil.NewFakeUpstream())
	secondCtx, cancelSecond := context.WithCancel(context.Background())
	cancelSecond()
	if err := server.Start(secondCtx); err == nil {
		t.Fatal("second Start unexpectedly succeeded")
	}
	cancel()
	if err := waitStartDone(t, startDone); err != nil {
		t.Fatalf("first Start returned error: %v", err)
	}
}

type blockingGetUpstream struct {
	*testutil.FakeUpstream
	entered       chan struct{}
	release       chan struct{}
	finished      chan struct{}
	ignoreContext bool
	enterOnce     sync.Once
	releaseOnce   sync.Once
	finishOnce    sync.Once
	doCalls       atomic.Int64
}

var _ upstream.Client = (*blockingGetUpstream)(nil)

func newBlockingGetUpstream(ignoreContext bool) *blockingGetUpstream {
	return &blockingGetUpstream{
		FakeUpstream:  testutil.NewFakeUpstream(),
		entered:       make(chan struct{}),
		release:       make(chan struct{}),
		finished:      make(chan struct{}),
		ignoreContext: ignoreContext,
	}
}

func (u *blockingGetUpstream) Get(ctx context.Context, key string) (upstream.Value, error) {
	u.enterOnce.Do(func() { close(u.entered) })
	defer u.finishOnce.Do(func() { close(u.finished) })
	if u.ignoreContext {
		<-u.release
	} else {
		select {
		case <-u.release:
		case <-ctx.Done():
			return upstream.Value{}, ctx.Err()
		}
	}
	return upstream.Value{Data: []byte("value"), Exists: true}, nil
}

func (u *blockingGetUpstream) Do(ctx context.Context, args ...string) (any, error) {
	u.doCalls.Add(1)
	return u.FakeUpstream.Do(ctx, args...)
}

func (u *blockingGetUpstream) unblock() {
	u.releaseOnce.Do(func() { close(u.release) })
}

func drainTestConfig() config.Config {
	cfg := config.Default()
	cfg.Proxy.Listen = "127.0.0.1:0"
	cfg.Proxy.ReadTimeout = 5 * time.Second
	cfg.Proxy.WriteTimeout = time.Second
	cfg.Proxy.ShutdownTimeout = 2 * time.Second
	return cfg
}

func startDrainTestServer(t *testing.T, cfg config.Config, up upstream.Client) (*Server, context.CancelFunc, <-chan error) {
	t.Helper()
	svc := service.New(service.Options{Config: cfg, Upstream: up})
	server := NewServer(cfg.Proxy, svc, nil)
	ctx, cancel := context.WithCancel(context.Background())
	startDone := make(chan error, 1)
	startExited := make(chan struct{})
	t.Cleanup(func() {
		cancel()
		_ = server.Close()
		if blocking, ok := up.(*blockingGetUpstream); ok {
			blocking.unblock()
		}
		select {
		case <-startExited:
		case <-time.After(2 * time.Second):
			t.Error("proxy Start did not exit during cleanup")
		}
		select {
		case <-server.drain.started:
			select {
			case <-server.drain.drained:
			case <-time.After(2 * time.Second):
				t.Error("proxy handlers did not drain during cleanup")
			}
		default:
		}
		_ = svc.Close()
	})
	go func() {
		startDone <- server.Start(ctx)
		close(startExited)
	}()
	waitClosed(t, server.listening, "proxy listener to start")
	return server, cancel, startDone
}

func dialDrainTestServer(t *testing.T, server *Server) net.Conn {
	t.Helper()
	conn, err := net.Dial("tcp", server.server.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func requireNewConnectionRejected(t *testing.T, server *Server) {
	t.Helper()
	conn, err := net.DialTimeout("tcp", server.server.Addr().String(), time.Second)
	if err != nil {
		var netErr net.Error
		if errors.As(err, &netErr) && netErr.Timeout() {
			t.Fatal("new connection dial timed out during drain")
		}
		return
	}
	defer func() { _ = conn.Close() }()
	if err := conn.SetDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(conn, "*1\r\n$4\r\nPING\r\n"); err != nil {
		var netErr net.Error
		if errors.As(err, &netErr) && netErr.Timeout() {
			t.Fatal("new connection write timed out during drain")
		}
		return
	}
	response := make([]byte, len("+PONG\r\n"))
	_, err = io.ReadFull(conn, response)
	if err == nil {
		t.Fatalf("new connection was admitted during drain: response %q", response)
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		t.Fatal("new connection remained open during drain")
	}
}

func waitClosed(t *testing.T, ch <-chan struct{}, description string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for %s", description)
	}
}

func waitStartDone(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for proxy Start to return")
		return nil
	}
}
