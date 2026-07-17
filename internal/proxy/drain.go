package proxy

import (
	"errors"
	"net"
	"sync"
	"time"
)

// drainTracker closes admission and accounts for both command handlers and
// accepted connections. The mutex makes the draining check and handler add
// atomic, avoiding the Add-versus-Wait race of a bare sync.WaitGroup.
type drainTracker struct {
	mu          sync.Mutex
	draining    bool
	active      int
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

func (d *drainTracker) accept(conn net.Conn, readTimeout time.Duration) (bool, error) {
	if conn == nil {
		return false, nil
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.draining {
		return false, nil
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
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.draining {
		return false
	}
	d.active++
	return true
}

// prepareHandlerDone sets a fresh write deadline immediately before redcon
// flushes the response and resets the idle read deadline. During a drain it
// preserves the shutdown deadlines and leaves the handler accounted for until
// its goroutine closes the connection.
func (d *drainTracker) prepareHandlerDone(conn net.Conn, readTimeout, writeTimeout time.Duration) (draining bool, deadlineErr error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.draining {
		return true, nil
	}
	if conn != nil {
		now := time.Now()
		deadlineErr = errors.Join(
			conn.SetReadDeadline(now.Add(readTimeout)),
			conn.SetWriteDeadline(now.Add(writeTimeout)),
		)
	}
	d.active--
	return false, deadlineErr
}

func (d *drainTracker) completeDrainingHandler() {
	d.mu.Lock()
	d.active--
	d.signalDrainedLocked()
	d.mu.Unlock()
}

func (d *drainTracker) stopAdmission() {
	d.mu.Lock()
	if !d.draining {
		d.draining = true
		close(d.started)
	}
	d.signalDrainedLocked()
	d.mu.Unlock()
}

func (d *drainTracker) beginDrain(deadline time.Time) (drained <-chan struct{}, active, connections, deadlineFailures int) {
	d.mu.Lock()
	if !d.draining {
		d.draining = true
		close(d.started)
	}
	active = d.active
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
	return d.active, len(d.connections)
}

func (d *drainTracker) signalDrainedLocked() {
	if d.draining && d.active == 0 && len(d.connections) == 0 {
		d.drainedOnce.Do(func() { close(d.drained) })
	}
}
