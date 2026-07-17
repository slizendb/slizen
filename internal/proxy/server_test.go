package proxy

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/tidwall/redcon"

	"github.com/slizendb/slizen/internal/config"
	"github.com/slizendb/slizen/internal/service"
	"github.com/slizendb/slizen/internal/testutil"
)

func TestSupportedWriteHandlersInvalidateAffectedKeys(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		keys       []string
		wantValue  string
		wantExists bool
	}{
		{name: "SET", args: []string{"SET", "k", "new"}, keys: []string{"k"}, wantValue: "new", wantExists: true},
		{name: "SETEX", args: []string{"SETEX", "k", "60", "new"}, keys: []string{"k"}, wantValue: "new", wantExists: true},
		{name: "PSETEX", args: []string{"PSETEX", "k", "60000", "new"}, keys: []string{"k"}, wantValue: "new", wantExists: true},
		{name: "DEL multiple keys", args: []string{"DEL", "k1", "k2"}, keys: []string{"k1", "k2"}},
		{name: "UNLINK multiple keys", args: []string{"UNLINK", "k1", "k2"}, keys: []string{"k1", "k2"}},
		{name: "EXPIRE", args: []string{"EXPIRE", "k", "60"}, keys: []string{"k"}, wantValue: "old", wantExists: true},
		{name: "PEXPIRE", args: []string{"PEXPIRE", "k", "60000"}, keys: []string{"k"}, wantValue: "old", wantExists: true},
		{name: "PERSIST", args: []string{"PERSIST", "k"}, keys: []string{"k"}, wantValue: "old", wantExists: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.Default()
			cfg.Cache.MaxBytes = 1 << 20
			cfg.Cache.MaxEntries = 100
			cfg.Cache.MaxLocalTTL = time.Minute
			cfg.Hotness.Window = time.Second
			cfg.Hotness.EWMAAlpha = 1
			cfg.Hotness.PromotionThreshold = 1
			cfg.Hotness.DemotionThreshold = 0.1
			cfg.Hotness.MinimumHotWindows = 1
			clock := testutil.NewFakeClock(time.Unix(0, 0))
			up := testutil.NewFakeUpstream()
			for _, key := range tt.keys {
				up.Put(key, []byte("old"), 0)
			}
			svc := service.New(service.Options{Config: cfg, Upstream: up, Clock: clock})
			t.Cleanup(func() { _ = svc.Close() })
			for _, key := range tt.keys {
				warmHandlerCache(t, svc, clock, key)
			}

			commandArgs := make([][]byte, len(tt.args))
			for i, arg := range tt.args {
				commandArgs[i] = []byte(arg)
			}
			conn := &fakeConn{}
			NewServer(cfg.Proxy, svc, nil).handle(conn, redcon.Command{Args: commandArgs})
			if len(conn.writes) == 0 || strings.HasPrefix(conn.writes[0], "-") {
				t.Fatalf("write response = %#v", conn.writes)
			}

			for _, key := range tt.keys {
				before := up.GetCallCount(key)
				value, err := svc.Get(context.Background(), key)
				if err != nil {
					t.Fatal(err)
				}
				if after := up.GetCallCount(key); after != before+1 {
					t.Fatalf("%s did not invalidate %q: upstream GETs before=%d after=%d", tt.args[0], key, before, after)
				}
				if value.Exists != tt.wantExists || string(value.Data) != tt.wantValue {
					t.Fatalf("GET %q = {exists:%t value:%q}, want {exists:%t value:%q}", key, value.Exists, value.Data, tt.wantExists, tt.wantValue)
				}
			}
		})
	}
}

func warmHandlerCache(t *testing.T, svc *service.Service, clock *testutil.FakeClock, key string) {
	t.Helper()
	if _, err := svc.Get(context.Background(), key); err != nil {
		t.Fatal(err)
	}
	clock.Advance(time.Second)
	if _, err := svc.Get(context.Background(), key); err != nil {
		t.Fatal(err)
	}
}

func TestRejectedMutationsDoNotReachUpstream(t *testing.T) {
	cfg := config.Default()
	svc := service.New(service.Options{Config: cfg, Upstream: testutil.NewFakeUpstream()})
	t.Cleanup(func() { _ = svc.Close() })
	server := NewServer(cfg.Proxy, svc, nil)

	for _, command := range []string{"MSET", "RENAME", "HSET", "HDEL", "LPUSH", "RPUSH", "LPOP", "RPOP", "SADD", "SREM"} {
		t.Run(command, func(t *testing.T) {
			before := svc.Metrics().Snapshot().UpstreamRequests
			conn := &fakeConn{}
			server.handle(conn, redcon.Command{Args: [][]byte{[]byte(command), []byte("key"), []byte("value")}})

			if after := svc.Metrics().Snapshot().UpstreamRequests; after != before {
				t.Fatalf("%s reached upstream", command)
			}
			if len(conn.writes) != 1 || !strings.Contains(conn.writes[0], "mutating command") {
				t.Fatalf("%s response = %#v", command, conn.writes)
			}
		})
	}
}
