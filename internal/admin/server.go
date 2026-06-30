package admin

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/slizendb/slizen/internal/config"
	"github.com/slizendb/slizen/internal/service"
)

type Server struct {
	cfg    config.AdminConfig
	svc    *service.Service
	logger *slog.Logger
	server *http.Server
}

func NewServer(cfg config.AdminConfig, svc *service.Service, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	return &Server{cfg: cfg, svc: svc, logger: logger}
}

func (s *Server) Start(ctx context.Context) error {
	mux := http.NewServeMux()
	s.routes(mux)
	s.server = &http.Server{
		Addr:              s.cfg.Listen,
		Handler:           mux,
		ReadHeaderTimeout: 2 * time.Second,
		ReadTimeout:       5 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	ln, err := net.Listen("tcp", s.cfg.Listen)
	if err != nil {
		return err
	}

	done := make(chan error, 1)
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		done <- s.server.Shutdown(shutdownCtx)
	}()

	s.logger.Info("admin listener started", "listen", s.cfg.Listen)
	err = s.server.Serve(ln)
	if errors.Is(err, http.ErrServerClosed) {
		err = <-done
	}
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}

func (s *Server) Close(ctx context.Context) error {
	if s.server == nil {
		return nil
	}
	return s.server.Shutdown(ctx)
}
