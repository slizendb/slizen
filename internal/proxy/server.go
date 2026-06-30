package proxy

import (
	"context"
	"log/slog"
	"strings"
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
}

func NewServer(cfg config.ProxyConfig, svc *service.Service, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	return &Server{cfg: cfg, svc: svc, logger: logger}
}

func (s *Server) Start(ctx context.Context) error {
	s.server = redcon.NewServer(s.cfg.Listen, s.handle, func(conn redcon.Conn) bool {
		return true
	}, func(conn redcon.Conn, err error) {
		if err != nil {
			s.logger.Debug("proxy connection closed", "remote", conn.RemoteAddr(), "error", err)
		}
	})
	s.server.SetIdleClose(s.cfg.ReadTimeout)

	listenSignal := make(chan error, 1)
	done := make(chan error, 1)
	go func() {
		done <- s.server.ListenServeAndSignal(listenSignal)
	}()

	if err := <-listenSignal; err != nil {
		return err
	}
	s.svc.SetProxyActive(true)
	s.logger.Info("proxy listener started", "listen", s.cfg.Listen)

	select {
	case err := <-done:
		s.svc.SetProxyActive(false)
		return err
	case <-ctx.Done():
		s.svc.SetProxyActive(false)
		_ = s.server.Close()
		<-done
		return nil
	}
}

func (s *Server) Close() error {
	s.svc.SetProxyActive(false)
	if s.server == nil {
		return nil
	}
	return s.server.Close()
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

	ctx, cancel := context.WithTimeout(context.Background(), s.cfg.ReadTimeout)
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
			conn.WriteError("ERR SELECT is supported only for database 0 in Slizen v0.1")
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
