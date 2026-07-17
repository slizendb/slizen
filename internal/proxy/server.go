package proxy

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/tidwall/redcon"

	"github.com/slizendb/slizen/internal/config"
	"github.com/slizendb/slizen/internal/service"
)

type Server struct {
	cfg    config.ProxyConfig
	svc    *service.Service
	logger *slog.Logger
	server *redcon.Server
	drain  *drainTracker

	lifecycleMu    sync.Mutex
	started        bool
	closing        bool
	listenerReady  bool
	listenerClosed bool
	finished       bool
	listening      chan struct{}
	forceClose     chan struct{}
	forceOnce      sync.Once
	requestCtx     context.Context
	cancelRequests context.CancelFunc
}

func NewServer(cfg config.ProxyConfig, svc *service.Service, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	requestCtx, cancelRequests := context.WithCancel(context.Background())
	return &Server{
		cfg:            cfg,
		svc:            svc,
		logger:         logger,
		drain:          newDrainTracker(),
		listening:      make(chan struct{}),
		forceClose:     make(chan struct{}),
		requestCtx:     requestCtx,
		cancelRequests: cancelRequests,
	}
}

func (s *Server) Start(ctx context.Context) error {
	server := redcon.NewServer(s.cfg.Listen, s.dispatch, s.acceptConnection, s.connectionClosed)
	s.lifecycleMu.Lock()
	if s.started {
		s.lifecycleMu.Unlock()
		return errors.New("proxy server already started")
	}
	if s.closing {
		s.lifecycleMu.Unlock()
		return errors.New("proxy server is closed")
	}
	s.started = true
	s.server = server
	s.lifecycleMu.Unlock()
	defer func() {
		s.cancelRequests()
		s.svc.SetProxyActive(false)
		s.lifecycleMu.Lock()
		s.listenerReady = false
		s.finished = true
		s.lifecycleMu.Unlock()
	}()

	listenSignal := make(chan error, 1)
	done := make(chan error, 1)
	go func() {
		done <- server.ListenServeAndSignal(listenSignal)
	}()

	if err := <-listenSignal; err != nil {
		return err
	}
	s.lifecycleMu.Lock()
	s.listenerReady = true
	closing := s.closing
	if !closing {
		s.svc.SetProxyActive(true)
	}
	s.lifecycleMu.Unlock()
	close(s.listening)
	if closing {
		if err := s.closeListener(); err != nil {
			return err
		}
		return s.pollServeResult(done)
	}
	s.logger.Info("proxy listener started", "listen", s.cfg.Listen)

	select {
	case err := <-done:
		return s.waitAfterListenerExit(err)
	case <-ctx.Done():
		return s.drainAndClose(done)
	case <-s.forceClose:
		s.cancelRequests()
		s.drain.stopAdmission()
		closeErr := s.closeListener()
		if closeErr != nil {
			return closeErr
		}
		return s.pollServeResult(done)
	}
}

func (s *Server) Close() error {
	s.lifecycleMu.Lock()
	s.closing = true
	s.svc.SetProxyActive(false)
	s.drain.stopAdmission()
	s.lifecycleMu.Unlock()
	s.cancelRequests()
	s.forceOnce.Do(func() { close(s.forceClose) })
	return s.closeListener()
}

func (s *Server) acceptConnection(conn redcon.Conn) bool {
	accepted, err := s.drain.accept(conn.NetConn(), s.cfg.ReadTimeout)
	if err != nil {
		s.logger.Debug("proxy connection deadline setup failed", "remote", conn.RemoteAddr(), "error", err)
	}
	return accepted
}

func (s *Server) connectionClosed(conn redcon.Conn, err error) {
	s.drain.connectionClosed(conn.NetConn())
	if err != nil {
		s.logger.Debug("proxy connection closed", "remote", conn.RemoteAddr(), "error", err)
	}
}

func (s *Server) dispatch(conn redcon.Conn, cmd redcon.Command) {
	if !s.drain.beginHandler() {
		conn.WriteError("TRYAGAIN Slizen is shutting down")
		if err := conn.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			s.logger.Debug("proxy shutdown connection close failed", "remote", conn.RemoteAddr(), "error", err)
		}
		return
	}
	defer s.finishHandler(conn)
	s.handle(conn, cmd)
}

func (s *Server) finishHandler(conn redcon.Conn) {
	draining, deadlineErr := s.drain.prepareHandlerDone(conn.NetConn(), s.cfg.ReadTimeout, s.cfg.WriteTimeout)
	if deadlineErr != nil {
		s.logger.Debug("proxy connection deadline setup failed", "remote", conn.RemoteAddr(), "error", deadlineErr)
	}
	if !draining {
		return
	}
	defer s.drain.completeDrainingHandler()
	if err := conn.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
		s.logger.Debug("proxy drained connection close failed", "remote", conn.RemoteAddr(), "error", err)
	}
}

func (s *Server) drainAndClose(done <-chan error) error {
	s.svc.SetProxyActive(false)
	deadline := time.Now().Add(s.cfg.ShutdownTimeout)
	drained, active, connections, deadlineFailures := s.drain.beginDrain(deadline)
	if deadlineFailures > 0 {
		s.logger.Warn("proxy drain could not wake all connections", "failures", deadlineFailures)
	}
	s.logger.Info("proxy drain started", "active_handlers", active, "connections", connections, "timeout", s.cfg.ShutdownTimeout)

	outcome := waitForDrain(drained, s.forceClose, deadline)
	if outcome != drainCompleted {
		s.cancelRequests()
	}
	if outcome == drainTimedOut {
		active, connections = s.drain.snapshot()
		s.logger.Warn("proxy drain deadline reached", "active_handlers", active, "connections", connections)
	} else if outcome == drainForced {
		s.logger.Warn("proxy drain forced closed")
	} else {
		s.logger.Info("proxy drain completed")
	}
	closeErr := s.closeListener()
	if closeErr != nil {
		return closeErr
	}
	if outcome == drainCompleted {
		return <-done
	}
	return s.pollServeResult(done)
}

func (s *Server) waitAfterListenerExit(serveErr error) error {
	s.svc.SetProxyActive(false)
	s.cancelRequests()
	deadline := time.Now().Add(s.cfg.ShutdownTimeout)
	drained, _, _, deadlineFailures := s.drain.beginDrain(deadline)
	if deadlineFailures > 0 {
		s.logger.Warn("proxy shutdown could not wake all connections", "failures", deadlineFailures)
	}
	if waitForDrain(drained, s.forceClose, deadline) == drainTimedOut {
		active, connections := s.drain.snapshot()
		s.logger.Warn("proxy handler cleanup deadline reached", "active_handlers", active, "connections", connections)
	}
	return serveErr
}

type drainOutcome uint8

const (
	drainCompleted drainOutcome = iota
	drainTimedOut
	drainForced
)

func waitForDrain(drained, force <-chan struct{}, deadline time.Time) drainOutcome {
	select {
	case <-force:
		return drainForced
	default:
	}
	select {
	case <-drained:
		return drainCompleted
	default:
	}
	remaining := time.Until(deadline)
	if remaining <= 0 {
		return drainTimedOut
	}
	timer := time.NewTimer(remaining)
	defer timer.Stop()
	select {
	case <-force:
		return drainForced
	case <-drained:
		return drainCompleted
	case <-timer.C:
		return drainTimedOut
	}
}

func (s *Server) closeListener() error {
	s.lifecycleMu.Lock()
	if s.listenerClosed || !s.listenerReady || s.finished || s.server == nil {
		s.lifecycleMu.Unlock()
		return nil
	}
	s.listenerClosed = true
	server := s.server
	s.lifecycleMu.Unlock()
	if err := server.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
		return fmt.Errorf("close proxy listener: %w", err)
	}
	return nil
}

func (s *Server) pollServeResult(done <-chan error) error {
	select {
	case err := <-done:
		return err
	default:
		go func() {
			if err := <-done; err != nil {
				s.logger.Warn("proxy listener cleanup failed after forced shutdown", "error", err)
			}
		}()
		return nil
	}
}

func (s *Server) handle(conn redcon.Conn, cmd redcon.Command) {
	start := time.Now()
	result := "ok"
	command := "UNKNOWN"
	defer func() {
		s.svc.Metrics().ObserveRequest(command, result, time.Since(start))
	}()

	parsed, err := ParseCommand(cmd)
	if err != nil {
		result = "error"
		conn.WriteError("ERR empty command")
		return
	}
	command = parsed.Name
	args := parsed.Args

	if isUnsafeCommand(command) {
		result = "error"
		conn.WriteError(rejectedUnsafe(command))
		return
	}
	if isRejectedMutation(command) {
		result = "error"
		conn.WriteError(rejectedMutation(command))
		return
	}

	ctx, cancel := context.WithTimeout(s.requestCtx, s.cfg.ReadTimeout)
	defer cancel()

	switch command {
	case "PING":
		if len(args) == 1 {
			conn.WriteString("PONG")
			return
		}
		if len(args) == 2 {
			conn.WriteBulkString(args[1])
			return
		}
		result = "error"
		conn.WriteError(wrongArity(command))
	case "QUIT":
		conn.WriteString("OK")
		_ = conn.Close()
	case "SELECT":
		if len(args) != 2 {
			result = "error"
			conn.WriteError(wrongArity(command))
			return
		}
		if args[1] != "0" {
			result = "error"
			conn.WriteError("ERR SELECT is supported only for database 0 in Slizen")
			return
		}
		conn.WriteString("OK")
	case "GET":
		if len(args) != 2 {
			result = "error"
			conn.WriteError(wrongArity(command))
			return
		}
		value, err := s.svc.Get(ctx, args[1])
		if err != nil {
			result = "error"
			writeUpstreamError(conn)
			return
		}
		if !value.Exists {
			conn.WriteNull()
			return
		}
		conn.WriteBulk(value.Data)
	case "MGET":
		if len(args) < 2 {
			result = "error"
			conn.WriteError(wrongArity(command))
			return
		}
		values, err := s.svc.MGet(ctx, args[1:])
		if err != nil {
			result = "error"
			writeUpstreamError(conn)
			return
		}
		conn.WriteArray(len(values))
		for _, value := range values {
			if !value.Exists {
				conn.WriteNull()
			} else {
				conn.WriteBulk(value.Data)
			}
		}
	case "SET":
		if len(args) < 3 {
			result = "error"
			conn.WriteError(wrongArity(command))
			return
		}
		if setUsesGetOption(args[3:]) {
			result = "error"
			conn.WriteError(rejectedSetGet())
			return
		}
		value, err := s.svc.ExecuteWrite(ctx, command, args[1:], []string{args[1]})
		if err != nil {
			result = "error"
			writeUpstreamError(conn)
			return
		}
		writeAny(conn, value)
	case "SETEX", "PSETEX":
		if len(args) != 4 {
			result = "error"
			conn.WriteError(wrongArity(command))
			return
		}
		value, err := s.svc.ExecuteWrite(ctx, command, args[1:], []string{args[1]})
		if err != nil {
			result = "error"
			writeUpstreamError(conn)
			return
		}
		writeAny(conn, value)
	case "DEL", "UNLINK":
		if len(args) < 2 {
			result = "error"
			conn.WriteError(wrongArity(command))
			return
		}
		value, err := s.svc.ExecuteWrite(ctx, command, args[1:], args[1:])
		if err != nil {
			result = "error"
			writeUpstreamError(conn)
			return
		}
		writeAny(conn, value)
	case "EXPIRE", "PEXPIRE":
		if len(args) != 3 {
			result = "error"
			conn.WriteError(wrongArity(command))
			return
		}
		value, err := s.svc.ExecuteWrite(ctx, command, args[1:], []string{args[1]})
		if err != nil {
			result = "error"
			writeUpstreamError(conn)
			return
		}
		writeAny(conn, value)
	case "PERSIST":
		if len(args) != 2 {
			result = "error"
			conn.WriteError(wrongArity(command))
			return
		}
		value, err := s.svc.ExecuteWrite(ctx, command, args[1:], []string{args[1]})
		if err != nil {
			result = "error"
			writeUpstreamError(conn)
			return
		}
		writeAny(conn, value)
	case "TTL", "PTTL":
		if len(args) != 2 {
			result = "error"
			conn.WriteError(wrongArity(command))
			return
		}
		value, err := s.svc.PassThrough(ctx, command, args[1:])
		if err != nil {
			result = "error"
			writeUpstreamError(conn)
			return
		}
		writeAny(conn, value)
	case "EXISTS":
		if len(args) < 2 {
			result = "error"
			conn.WriteError(wrongArity(command))
			return
		}
		value, err := s.svc.PassThrough(ctx, command, args[1:])
		if err != nil {
			result = "error"
			writeUpstreamError(conn)
			return
		}
		writeAny(conn, value)
	default:
		result = "error"
		conn.WriteError(unsupported(strings.ToUpper(command)))
	}
}
