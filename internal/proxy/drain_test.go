package proxy

import (
	"errors"
	"net"
	"testing"
	"time"
)

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
