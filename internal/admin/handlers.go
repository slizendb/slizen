package admin

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const maxPurgeBodyBytes int64 = 1024

func (s *Server) routes(mux *http.ServeMux) {
	mux.HandleFunc("/healthz", s.handleHealthz)
	mux.HandleFunc("/readyz", s.handleReadyz)
	mux.Handle("/metrics", s.svc.Metrics().Handler())
	mux.HandleFunc("/v1/status", s.handleStatus)
	mux.HandleFunc("/v1/hotkeys", s.handleHotKeys)
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
	writeJSON(w, s.svc.Status(ctx))
}

func (s *Server) handleHotKeys(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	limit := 20
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 0 || parsed > 1000 {
			http.Error(w, "invalid limit", http.StatusBadRequest)
			return
		}
		limit = parsed
	}
	writeJSON(w, map[string]any{"hotkeys": s.svc.HotKeys(limit)})
}

func (s *Server) handleCache(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	writeJSON(w, s.svc.CacheInfo())
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
				writeJSON(w, map[string]any{"purged": s.svc.PurgeCache("")})
				return
			}
			http.Error(w, "invalid purge request", http.StatusBadRequest)
			return
		}
		body.Key = strings.TrimSpace(body.Key)
		if len(body.Key) > 512 {
			http.Error(w, "key is too long", http.StatusBadRequest)
			return
		}
	}
	purged := s.svc.PurgeCache(body.Key)
	writeJSON(w, map[string]any{"purged": purged})
}

func withShortTimeout(r *http.Request) (context.Context, context.CancelFunc) {
	return context.WithTimeout(r.Context(), 2*time.Second)
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	_ = encoder.Encode(value)
}

func methodNotAllowed(w http.ResponseWriter) {
	http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
}
