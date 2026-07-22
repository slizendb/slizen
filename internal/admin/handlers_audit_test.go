package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/slizendb/slizen/internal/config"
	"github.com/slizendb/slizen/internal/service"
	"github.com/slizendb/slizen/internal/testutil"
)

func TestAuditHandlerDefaultsToBoundedPrivateReport(t *testing.T) {
	svc, handler := newAuditTestHandler(t)
	for i := 0; i < service.DefaultAuditLimit+1; i++ {
		key := fmt.Sprintf("private-policy:key-%03d", i)
		if _, err := svc.Get(context.Background(), key); err != nil {
			t.Fatal(err)
		}
	}

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/v1/audit", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if got := recorder.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("content type = %q", got)
	}
	var report service.AuditReport
	if err := json.Unmarshal(recorder.Body.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.SchemaVersion != service.AuditSchemaVersion {
		t.Fatalf("schema version = %q", report.SchemaVersion)
	}
	if len(report.Entries) != service.DefaultAuditLimit {
		t.Fatalf("entries = %d, want %d", len(report.Entries), service.DefaultAuditLimit)
	}
	if report.TrackedKeys != service.DefaultAuditLimit+1 || report.ReturnedEntries != service.DefaultAuditLimit || !report.Truncated {
		t.Fatalf("report bounds = %+v", report)
	}
	if strings.Contains(recorder.Body.String(), "private-policy:") {
		t.Fatalf("audit endpoint leaked a raw key or policy prefix: %s", recorder.Body.String())
	}
}

func TestAuditHandlerValidatesMethodAndLimit(t *testing.T) {
	_, handler := newAuditTestHandler(t)
	tests := []struct {
		name   string
		method string
		target string
		want   int
	}{
		{name: "post", method: http.MethodPost, target: "/v1/audit", want: http.StatusMethodNotAllowed},
		{name: "not a number", method: http.MethodGet, target: "/v1/audit?limit=many", want: http.StatusBadRequest},
		{name: "zero", method: http.MethodGet, target: "/v1/audit?limit=0", want: http.StatusBadRequest},
		{name: "negative", method: http.MethodGet, target: "/v1/audit?limit=-1", want: http.StatusBadRequest},
		{name: "above maximum", method: http.MethodGet, target: "/v1/audit?limit=1001", want: http.StatusBadRequest},
		{name: "maximum", method: http.MethodGet, target: "/v1/audit?limit=1000", want: http.StatusOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, httptest.NewRequest(tt.method, tt.target, nil))
			if recorder.Code != tt.want {
				t.Fatalf("status = %d, want %d; body = %s", recorder.Code, tt.want, recorder.Body.String())
			}
		})
	}
}

func TestHotKeysHandlerRejectsUnboundedZeroLimit(t *testing.T) {
	_, handler := newAuditTestHandler(t)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/v1/hotkeys?limit=0", nil))
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body = %s", recorder.Code, http.StatusBadRequest, recorder.Body.String())
	}
}

func TestWriteJSONRejectsNonFiniteNumbersBeforeWriting(t *testing.T) {
	recorder := httptest.NewRecorder()
	err := writeJSON(recorder, map[string]float64{"invalid": math.NaN()})
	if err == nil {
		t.Fatal("expected JSON encoding error")
	}
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusInternalServerError)
	}
	if strings.Contains(recorder.Body.String(), "NaN") {
		t.Fatalf("invalid JSON escaped into response: %s", recorder.Body.String())
	}
}

func TestCachePurgePreservesWhitespaceKeyAndNeverExpandsItToPurgeAll(t *testing.T) {
	cfg := config.Default()
	cfg.Mode = "cache"
	cfg.Hotness.Window = time.Second
	cfg.Hotness.EWMAAlpha = 1
	cfg.Hotness.PromotionThreshold = 1
	cfg.Hotness.DemotionThreshold = 0.1
	cfg.Hotness.MinimumHotWindows = 1
	clock := testutil.NewFakeClock(time.Unix(0, 0))
	up := testutil.NewFakeUpstream()
	for _, key := range []string{"first", "second"} {
		up.Put(key, []byte(key), 0)
	}
	svc := service.New(service.Options{
		Config: cfg, Upstream: up, Clock: clock,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	t.Cleanup(func() { _ = svc.Close() })
	for _, key := range []string{"first", "second"} {
		if _, err := svc.Get(context.Background(), key); err != nil {
			t.Fatal(err)
		}
	}
	clock.Advance(time.Second)
	for _, key := range []string{"first", "second"} {
		if _, err := svc.Get(context.Background(), key); err != nil {
			t.Fatal(err)
		}
	}

	server := NewServer(cfg.Admin, svc, nil)
	mux := http.NewServeMux()
	server.routes(mux)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/cache/purge", strings.NewReader(`{"key":"  "}`))
	mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"purged": false`) {
		t.Fatalf("purge response = %d %s", recorder.Code, recorder.Body.String())
	}

	for _, key := range []string{"first", "second"} {
		if _, err := svc.Get(context.Background(), key); err != nil {
			t.Fatal(err)
		}
		if calls := up.GetCallCount(key); calls != 2 {
			t.Fatalf("whitespace purge flushed %q: upstream GETs = %d, want 2", key, calls)
		}
	}
}

func newAuditTestHandler(t *testing.T) (*service.Service, http.Handler) {
	t.Helper()
	cfg := config.Default()
	cfg.Mode = "observe"
	cfg.Privacy.KeyVisibility = "hash"
	cfg.Privacy.KeyHashSecret = "handler-audit-secret"
	cfg.Cache.Policies = []config.CachePolicyConfig{{Prefix: "private-policy:", Mode: "observe"}}
	cfg.Hotness.MaxTrackedKeys = service.DefaultAuditLimit + 1
	clock := testutil.NewFakeClock(time.Unix(100, 0))
	svc := service.New(service.Options{
		Config:   cfg,
		Upstream: testutil.NewFakeUpstream(),
		Clock:    clock,
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		Version:  "test",
	})
	t.Cleanup(func() { _ = svc.Close() })
	server := NewServer(cfg.Admin, svc, nil)
	mux := http.NewServeMux()
	server.routes(mux)
	return svc, mux
}
