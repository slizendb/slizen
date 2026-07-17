package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/slizendb/slizen/internal/service"
)

const maxPurgeBodyBytes int64 = 1024

func (s *Server) routes(mux *http.ServeMux) {
	mux.HandleFunc("/healthz", s.handleHealthz)
	mux.HandleFunc("/readyz", s.handleReadyz)
	mux.Handle("/metrics", s.svc.Metrics().Handler())
	mux.HandleFunc("/v1/status", s.handleStatus)
	mux.HandleFunc("/v1/hotkeys", s.handleHotKeys)
	mux.HandleFunc("/v1/audit", s.handleAudit)
	mux.HandleFunc("/v1/cache", s.handleCache)
	mux.HandleFunc("/v1/cache/purge", s.handleCachePurge)
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte("ok\n"))
}

func (s *Server) handleReadyz(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	ctx, cancel := withShortTimeout(r)
	defer cancel()
	if err := s.svc.Ready(ctx); err != nil {
		http.Error(w, "not ready", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte("ready\n"))
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	ctx, cancel := withShortTimeout(r)
	defer cancel()
	s.writeJSON(w, s.svc.Status(ctx))
}

func (s *Server) handleHotKeys(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	limit := 20
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 1000 {
			http.Error(w, "invalid limit", http.StatusBadRequest)
			return
		}
		limit = parsed
	}
	s.writeJSON(w, map[string]any{"hotkeys": s.svc.HotKeys(limit)})
}

func (s *Server) handleAudit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	limit := service.DefaultAuditLimit
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > service.MaxAuditLimit {
			http.Error(w, "invalid limit", http.StatusBadRequest)
			return
		}
		limit = parsed
	}
	s.writeJSON(w, s.svc.Audit(limit))
}

func (s *Server) handleCache(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	s.writeJSON(w, s.svc.CacheInfo())
}

func (s *Server) handleCachePurge(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxPurgeBodyBytes)

	var body struct {
		Key string `json:"key"`
	}
	if r.ContentLength != 0 {
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&body); err != nil {
			if errors.Is(err, io.EOF) {
				s.writeJSON(w, map[string]any{"purged": s.svc.PurgeCache("")})
				return
			}
			http.Error(w, "invalid purge request", http.StatusBadRequest)
			return
		}
		if len(body.Key) > 512 {
			http.Error(w, "key is too long", http.StatusBadRequest)
			return
		}
	}
	purged := s.svc.PurgeCache(body.Key)
	s.writeJSON(w, map[string]any{"purged": purged})
}

func withShortTimeout(r *http.Request) (context.Context, context.CancelFunc) {
	return context.WithTimeout(r.Context(), 2*time.Second)
}

func (s *Server) writeJSON(w http.ResponseWriter, value any) {
	if err := writeJSON(w, value); err != nil {
		s.logger.Error("admin JSON response failed", "error", err)
	}
}

func writeJSON(w http.ResponseWriter, value any) error {
	var body bytes.Buffer
	encoder := json.NewEncoder(&body)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		http.Error(w, "response encoding failed", http.StatusInternalServerError)
		return fmt.Errorf("encode JSON response: %w", err)
	}
	w.Header().Set("Content-Type", "application/json")
	if _, err := w.Write(body.Bytes()); err != nil {
		return fmt.Errorf("write JSON response: %w", err)
	}
	return nil
}

func methodNotAllowed(w http.ResponseWriter) {
	http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
}
