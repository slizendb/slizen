package proxy

import (
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

type connectionLimitError struct{}

func (connectionLimitError) Error() string { return "proxy connection limit reached" }

// drainTracker closes admission and accounts for both command handlers and
// accepted connections. Handler admission uses a double-checked reservation;
// completion takes the connection mutex once to linearize with drain startup.
// This removes steady-path admission locking while still ensuring that every
// successfully admitted handler is included in drain accounting.
type drainTracker struct {
	mu          sync.Mutex
	draining    atomic.Bool
	active      atomic.Int64
	connections map[net.Conn]*trackedConnection
	pendingHead *trackedConnection
	started     chan struct{}
	drained     chan struct{}
	drainedOnce sync.Once
}

type trackedConnection struct {
	conn    net.Conn
	prev    *trackedConnection
	next    *trackedConnection
	pending bool
}

func newDrainTracker() *drainTracker {
	return &drainTracker{
		connections: make(map[net.Conn]*trackedConnection),
		started:     make(chan struct{}),
		drained:     make(chan struct{}),
	}
}

func (d *drainTracker) accept(conn net.Conn, readTimeout time.Duration, maxConnections int) (bool, error) {
	if conn == nil {
		return false, nil
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.draining.Load() {
		return false, nil
	}
	if len(d.connections) >= maxConnections {
		return false, connectionLimitError{}
	}
	if err := conn.SetReadDeadline(time.Now().Add(readTimeout)); err != nil {
		return false, err
	}
	tracked := &trackedConnection{conn: conn, next: d.pendingHead, pending: true}
	if d.pendingHead != nil {
		d.pendingHead.prev = tracked
	}
	d.pendingHead = tracked
	d.connections[conn] = tracked
	return true, nil
}

func (d *drainTracker) connectionClosed(conn net.Conn) {
	if conn == nil {
		return
	}
	d.mu.Lock()
	if tracked := d.connections[conn]; tracked != nil {
		d.removePendingLocked(tracked)
	}
	delete(d.connections, conn)
	d.signalDrainedLocked()
	d.mu.Unlock()
}

func (d *drainTracker) beginHandler() bool {
	if d.draining.Load() {
		return false
	}
	if active := d.active.Add(1); active <= 0 {
		d.active.Add(-1)
		panic("proxy active handler count overflow")
	}
	if !d.draining.Load() {
		return true
	}

	// Drain won after the first check. This reservation never becomes an
	// admitted handler, so roll it back and repair a drain signal that may have
	// observed the transient count before rollback.
	d.decrementActiveHandler()
	d.mu.Lock()
	d.signalDrainedLocked()
	d.mu.Unlock()
	return false
}

// prepareHandlerDone sets a fresh write deadline immediately before redcon
// flushes the response and resets the idle read deadline. During a drain it
// preserves the shutdown deadlines and leaves the handler accounted for until
// its goroutine closes the connection.
func (d *drainTracker) prepareHandlerDone(conn net.Conn, readTimeout, writeTimeout time.Duration) (draining bool, deadlineErr error) {
	if d.draining.Load() {
		return true, nil
	}

	if conn != nil {
		now := time.Now()
		deadlineErr = errors.Join(
			conn.SetReadDeadline(now.Add(readTimeout)),
			conn.SetWriteDeadline(now.Add(writeTimeout)),
		)
	}

	// A drain may have started while the connection deadlines were being set.
	// Keep the handler accounted for in that case; finishHandler will close the
	// connection and call completeDrainingHandler. Closing the connection also
	// makes any normal deadline that raced with the drain harmless.
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.draining.Load() {
		return true, deadlineErr
	}
	d.decrementActiveHandler()
	return false, deadlineErr
}

func (d *drainTracker) completeDrainingHandler() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.draining.Load() {
		panic("proxy handler completed through the drain path before drain startup")
	}
	d.decrementActiveHandler()
	d.signalDrainedLocked()
}

func (d *drainTracker) stopAdmission() {
	d.mu.Lock()
	d.stopAdmissionLocked()
	d.signalDrainedLocked()
	d.mu.Unlock()
}

func (d *drainTracker) beginDrain(deadline time.Time) (drained <-chan struct{}, active, connections, deadlineFailures int) {
	d.mu.Lock()
	d.stopAdmissionLocked()
	active = int(d.active.Load())
	connections = len(d.connections)
	d.signalDrainedLocked()
	d.mu.Unlock()

	wakeAt := time.Now()
	for time.Now().Before(deadline) {
		select {
		case <-d.drained:
			return d.drained, active, connections, deadlineFailures
		default:
		}

		var batch [128]net.Conn
		count := 0
		d.mu.Lock()
		for count < len(batch) && d.pendingHead != nil {
			tracked := d.pendingHead
			d.removePendingLocked(tracked)
			batch[count] = tracked.conn
			count++
		}
		d.mu.Unlock()
		if count == 0 {
			break
		}
		for _, conn := range batch[:count] {
			if err := conn.SetReadDeadline(wakeAt); err != nil {
				deadlineFailures++
			}
			if err := conn.SetWriteDeadline(deadline); err != nil {
				deadlineFailures++
			}
		}
	}
	return d.drained, active, connections, deadlineFailures
}

func (d *drainTracker) removePendingLocked(tracked *trackedConnection) {
	if !tracked.pending {
		return
	}
	if tracked.prev == nil {
		d.pendingHead = tracked.next
	} else {
		tracked.prev.next = tracked.next
	}
	if tracked.next != nil {
		tracked.next.prev = tracked.prev
	}
	tracked.prev = nil
	tracked.next = nil
	tracked.pending = false
}

func (d *drainTracker) snapshot() (active, connections int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return int(d.active.Load()), len(d.connections)
}

func (d *drainTracker) signalDrainedLocked() {
	if d.draining.Load() && d.active.Load() == 0 && len(d.connections) == 0 {
		d.drainedOnce.Do(func() { close(d.drained) })
	}
}

func (d *drainTracker) stopAdmissionLocked() {
	if !d.draining.Swap(true) {
		close(d.started)
	}
}

func (d *drainTracker) decrementActiveHandler() {
	if remaining := d.active.Add(-1); remaining < 0 {
		d.active.Add(1)
		panic("proxy active handler count underflow")
	}
}
