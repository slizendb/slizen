package proxy

import (
	"errors"
	"io"
	"net"
	"sync"
	"testing"
	"time"
)

type blockingDeadlineConn struct {
	readStarted chan struct{}
	releaseRead chan struct{}
	readOnce    sync.Once
}

func (c *blockingDeadlineConn) Read([]byte) (int, error)         { return 0, io.EOF }
func (c *blockingDeadlineConn) Write(p []byte) (int, error)      { return len(p), nil }
func (c *blockingDeadlineConn) Close() error                     { return nil }
func (c *blockingDeadlineConn) LocalAddr() net.Addr              { return nil }
func (c *blockingDeadlineConn) RemoteAddr() net.Addr             { return nil }
func (c *blockingDeadlineConn) SetDeadline(time.Time) error      { return nil }
func (c *blockingDeadlineConn) SetWriteDeadline(time.Time) error { return nil }

func (c *blockingDeadlineConn) SetReadDeadline(time.Time) error {
	c.readOnce.Do(func() {
		close(c.readStarted)
		<-c.releaseRead
	})
	return nil
}

func TestDrainWaitsForAcceptThatPassedAdmissionCheck(t *testing.T) {
	tracker := newDrainTracker()
	conn := &blockingDeadlineConn{
		readStarted: make(chan struct{}),
		releaseRead: make(chan struct{}),
	}
	t.Cleanup(func() { _ = conn.Close() })

	type acceptResult struct {
		accepted bool
		err      error
	}
	accepted := make(chan acceptResult, 1)
	go func() {
		ok, err := tracker.accept(conn, time.Hour, 1)
		accepted <- acceptResult{accepted: ok, err: err}
	}()
	<-conn.readStarted

	type drainResult struct {
		drained     <-chan struct{}
		active      int
		connections int
	}
	drainCalling := make(chan struct{})
	drainStarted := make(chan drainResult, 1)
	go func() {
		close(drainCalling)
		drained, active, connections, _ := tracker.beginDrain(time.Now().Add(time.Second))
		drainStarted <- drainResult{drained: drained, active: active, connections: connections}
	}()
	<-drainCalling
	select {
	case <-tracker.started:
		t.Fatal("drain passed an accept still inside the connection critical section")
	case <-time.After(25 * time.Millisecond):
	}

	close(conn.releaseRead)
	if result := <-accepted; !result.accepted || result.err != nil {
		t.Fatalf("accept = %t, %v", result.accepted, result.err)
	}
	result := <-drainStarted
	if result.active != 0 || result.connections != 1 {
		t.Fatalf("drain snapshot = active:%d connections:%d, want 0/1", result.active, result.connections)
	}
	select {
	case <-result.drained:
		t.Fatal("drain completed before the accepted connection closed")
	default:
	}
	tracker.connectionClosed(conn)
	waitClosed(t, result.drained, "accepted connection to drain")
}

func TestDrainClosesAfterLastHandlerAndConnectionInEitherOrder(t *testing.T) {
	for _, handlerFirst := range []bool{true, false} {
		name := "connection_first"
		if handlerFirst {
			name = "handler_first"
		}
		t.Run(name, func(t *testing.T) {
			tracker := newDrainTracker()
			conn := &blockingDeadlineConn{
				readStarted: make(chan struct{}),
				releaseRead: make(chan struct{}),
			}
			close(conn.releaseRead)
			accepted, err := tracker.accept(conn, time.Hour, 1)
			if err != nil || !accepted {
				t.Fatalf("accept = %t, %v", accepted, err)
			}
			if !tracker.beginHandler() {
				t.Fatal("handler admission unexpectedly closed")
			}
			drained, active, connections, _ := tracker.beginDrain(time.Now().Add(time.Second))
			if active != 1 || connections != 1 {
				t.Fatalf("drain snapshot = active:%d connections:%d, want 1/1", active, connections)
			}

			completeHandler := func() {
				draining, err := tracker.prepareHandlerDone(nil, time.Second, time.Second)
				if err != nil || !draining {
					t.Fatalf("handler completion = draining:%t error:%v", draining, err)
				}
				tracker.completeDrainingHandler()
			}
			if handlerFirst {
				completeHandler()
			} else {
				tracker.connectionClosed(conn)
			}
			select {
			case <-drained:
				t.Fatal("drain completed after only one tracked half finished")
			default:
			}
			if handlerFirst {
				tracker.connectionClosed(conn)
			} else {
				completeHandler()
			}
			waitClosed(t, drained, "last handler and connection")
		})
	}
}

func TestBeginHandlerAndDrainAreLinearizable(t *testing.T) {
	for range 1_000 {
		tracker := newDrainTracker()
		start := make(chan struct{})
		admitted := make(chan bool, 1)
		drainDone := make(chan struct{})
		go func() {
			<-start
			admitted <- tracker.beginHandler()
		}()
		go func() {
			<-start
			tracker.stopAdmission()
			close(drainDone)
		}()
		close(start)
		wasAdmitted := <-admitted
		<-drainDone

		active, connections := tracker.snapshot()
		wantActive := 0
		if wasAdmitted {
			wantActive = 1
		}
		if active != wantActive || connections != 0 {
			t.Fatalf("post-race snapshot = active:%d connections:%d, want %d/0", active, connections, wantActive)
		}
		if wasAdmitted {
			if draining, err := tracker.prepareHandlerDone(nil, 0, 0); !draining || err != nil {
				t.Fatalf("admitted handler completion = draining:%t error:%v", draining, err)
			}
			tracker.completeDrainingHandler()
		}
		waitClosed(t, tracker.drained, "admission/drain race")
		if tracker.beginHandler() {
			t.Fatal("handler admission reopened after drain")
		}
	}
}

func TestConcurrentDrainInitiatorsCloseStartedOnce(t *testing.T) {
	tracker := newDrainTracker()
	if !tracker.beginHandler() {
		t.Fatal("handler admission unexpectedly closed")
	}

	var wg sync.WaitGroup
	for i := range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if i%2 == 0 {
				tracker.stopAdmission()
				return
			}
			tracker.beginDrain(time.Now())
		}()
	}
	wg.Wait()
	waitClosed(t, tracker.started, "concurrent drain initiation")
	if active, connections := tracker.snapshot(); active != 1 || connections != 0 {
		t.Fatalf("concurrent drain snapshot = active:%d connections:%d, want 1/0", active, connections)
	}
	if draining, err := tracker.prepareHandlerDone(nil, 0, 0); !draining || err != nil {
		t.Fatalf("handler completion = draining:%t error:%v", draining, err)
	}
	tracker.completeDrainingHandler()
	waitClosed(t, tracker.drained, "concurrent drain initiators")
}

func TestDrainCounterGuardsRestoreState(t *testing.T) {
	t.Run("overflow", func(t *testing.T) {
		tracker := newDrainTracker()
		tracker.active.Store(1<<63 - 1)
		requireDrainPanic(t, func() { tracker.beginHandler() })
		if active := tracker.active.Load(); active != 1<<63-1 {
			t.Fatalf("active after overflow = %d, want %d", active, int64(1<<63-1))
		}
	})

	t.Run("underflow", func(t *testing.T) {
		tracker := newDrainTracker()
		tracker.stopAdmission()
		requireDrainPanic(t, tracker.completeDrainingHandler)
		if active := tracker.active.Load(); active != 0 {
			t.Fatalf("active after underflow = %d, want 0", active)
		}

		snapshot := make(chan struct{})
		go func() {
			tracker.snapshot()
			close(snapshot)
		}()
		waitClosed(t, snapshot, "drain mutex after recovered counter panic")
	})
}

func requireDrainPanic(t *testing.T, fn func()) {
	t.Helper()
	didPanic := false
	func() {
		defer func() {
			didPanic = recover() != nil
		}()
		fn()
	}()
	if !didPanic {
		t.Fatal("operation did not panic")
	}
}

func TestDrainDeadlineUnblocksBlockedConnectionWrite(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	t.Cleanup(func() {
		_ = serverConn.Close()
		_ = clientConn.Close()
	})
	tracker := newDrainTracker()
	accepted, err := tracker.accept(serverConn, time.Hour, 1)
	if err != nil || !accepted {
		t.Fatalf("accept = %t, %v", accepted, err)
	}

	writeStarted := make(chan struct{})
	writeDone := make(chan error, 1)
	go func() {
		close(writeStarted)
		_, err := serverConn.Write([]byte("blocked"))
		writeDone <- err
	}()
	<-writeStarted
	drained, _, _, deadlineFailures := tracker.beginDrain(time.Now().Add(25 * time.Millisecond))
	if deadlineFailures != 0 {
		t.Fatalf("deadline failures = %d", deadlineFailures)
	}

	select {
	case err := <-writeDone:
		if err == nil {
			t.Fatal("blocked write unexpectedly succeeded without a reader")
		}
	case <-time.After(time.Second):
		t.Fatal("drain deadline did not unblock the blocked write")
	}
	tracker.connectionClosed(serverConn)
	waitClosed(t, drained, "connection tracker to drain")
}

func TestDrainTracksEveryActiveHandler(t *testing.T) {
	tracker := newDrainTracker()
	if !tracker.beginHandler() || !tracker.beginHandler() {
		t.Fatal("handler admission unexpectedly closed")
	}
	drained, active, connections, _ := tracker.beginDrain(time.Now().Add(time.Second))
	if active != 2 || connections != 0 {
		t.Fatalf("drain snapshot = active %d, connections %d; want 2, 0", active, connections)
	}
	if tracker.beginHandler() {
		t.Fatal("handler admitted after drain began")
	}

	if draining, err := tracker.prepareHandlerDone(nil, time.Second, time.Second); !draining || err != nil {
		t.Fatalf("first handler completion = draining %t, error %v", draining, err)
	}
	tracker.completeDrainingHandler()
	select {
	case <-drained:
		t.Fatal("drain completed with one active handler remaining")
	default:
	}
	if draining, err := tracker.prepareHandlerDone(nil, time.Second, time.Second); !draining || err != nil {
		t.Fatalf("second handler completion = draining %t, error %v", draining, err)
	}
	tracker.completeDrainingHandler()
	waitClosed(t, drained, "all active handlers to drain")
}

func TestNormalResponseWriteDeadlineUnblocksNonReadingClient(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	t.Cleanup(func() {
		_ = serverConn.Close()
		_ = clientConn.Close()
	})
	tracker := newDrainTracker()
	accepted, err := tracker.accept(serverConn, time.Hour, 1)
	if err != nil || !accepted {
		t.Fatalf("accept = %t, %v", accepted, err)
	}
	if !tracker.beginHandler() {
		t.Fatal("handler admission unexpectedly closed")
	}
	if draining, err := tracker.prepareHandlerDone(serverConn, time.Hour, 25*time.Millisecond); draining || err != nil {
		t.Fatalf("handler completion = draining %t, error %v", draining, err)
	}

	writeDone := make(chan error, 1)
	go func() {
		_, err := serverConn.Write([]byte("response with no reader"))
		writeDone <- err
	}()
	select {
	case err := <-writeDone:
		if err == nil {
			t.Fatal("blocked response write unexpectedly succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("proxy.write_timeout did not unblock the response write")
	}
	tracker.connectionClosed(serverConn)
}

func TestPrepareHandlerDoneDoesNotBlockDrainWhileSettingDeadlines(t *testing.T) {
	tracker := newDrainTracker()
	if !tracker.beginHandler() {
		t.Fatal("handler admission unexpectedly closed")
	}
	conn := &blockingDeadlineConn{
		readStarted: make(chan struct{}),
		releaseRead: make(chan struct{}),
	}
	type completion struct {
		draining bool
		err      error
	}
	completed := make(chan completion, 1)
	go func() {
		draining, err := tracker.prepareHandlerDone(conn, time.Hour, time.Hour)
		completed <- completion{draining: draining, err: err}
	}()
	<-conn.readStarted

	drainStarted := make(chan struct{})
	var drained <-chan struct{}
	go func() {
		var active, connections int
		drained, active, connections, _ = tracker.beginDrain(time.Now().Add(time.Second))
		if active != 1 || connections != 0 {
			t.Errorf("drain snapshot = active %d, connections %d; want 1, 0", active, connections)
		}
		close(drainStarted)
	}()
	select {
	case <-drainStarted:
	case <-time.After(time.Second):
		t.Fatal("drain waited for a connection deadline call under the global mutex")
	}

	close(conn.releaseRead)
	result := <-completed
	if !result.draining || result.err != nil {
		t.Fatalf("handler completion = draining %t, error %v; want draining without error", result.draining, result.err)
	}
	tracker.completeDrainingHandler()
	waitClosed(t, drained, "handler that raced with drain")
}

func TestDrainRejectsConnectionsAtConfiguredLimit(t *testing.T) {
	firstServer, firstClient := net.Pipe()
	secondServer, secondClient := net.Pipe()
	t.Cleanup(func() {
		_ = firstServer.Close()
		_ = firstClient.Close()
		_ = secondServer.Close()
		_ = secondClient.Close()
	})

	tracker := newDrainTracker()
	accepted, err := tracker.accept(firstServer, time.Hour, 1)
	if err != nil || !accepted {
		t.Fatalf("first accept = %t, %v", accepted, err)
	}
	accepted, err = tracker.accept(secondServer, time.Hour, 1)
	var limitErr connectionLimitError
	if accepted || !errors.As(err, &limitErr) {
		t.Fatalf("second accept = %t, %v; want connection limit", accepted, err)
	}

	tracker.connectionClosed(firstServer)
	accepted, err = tracker.accept(secondServer, time.Hour, 1)
	if err != nil || !accepted {
		t.Fatalf("accept after close = %t, %v", accepted, err)
	}
	tracker.connectionClosed(secondServer)
}
