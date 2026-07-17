package cachepolicy

import (
	"sync"
	"testing"
	"time"
)

func TestMatcherUsesLongestLiteralBytePrefix(t *testing.T) {
	fallback := Decision{Mode: ModeCache, MaxItemBytes: 1024, MaxLocalTTL: time.Minute}
	rules := []Rule{
		{Prefix: "user:session:", Decision: Decision{Mode: ModeCache, MaxItemBytes: 256, MaxLocalTTL: time.Second}},
		{Prefix: "", Decision: Decision{Mode: ModeDeny}},
		{Prefix: "user:", Decision: Decision{Mode: ModeObserve}},
		{Prefix: "team:", Decision: Decision{Mode: ModeCache, MaxItemBytes: 128, MaxLocalTTL: 2 * time.Second}},
		{Prefix: "用户:", Decision: Decision{Mode: ModeObserve}},
	}
	matcher := New(fallback, rules)

	tests := []struct {
		key  string
		want Decision
	}{
		{key: "other", want: Decision{Mode: ModeDeny}},
		{key: "user:profile", want: Decision{Mode: ModeObserve}},
		{key: "user:session:", want: Decision{Mode: ModeCache, MaxItemBytes: 256, MaxLocalTTL: time.Second}},
		{key: "user:session:123", want: Decision{Mode: ModeCache, MaxItemBytes: 256, MaxLocalTTL: time.Second}},
		{key: "User:session:123", want: Decision{Mode: ModeDeny}},
		{key: "team:42", want: Decision{Mode: ModeCache, MaxItemBytes: 128, MaxLocalTTL: 2 * time.Second}},
		{key: "用户:42", want: Decision{Mode: ModeObserve}},
	}
	for _, tt := range tests {
		if got := matcher.Match(tt.key); got != tt.want {
			t.Fatalf("Match(%q) = %+v, want %+v", tt.key, got, tt.want)
		}
	}
}

func TestMatcherFallsBackAndIgnoresRuleOrder(t *testing.T) {
	fallback := Decision{Mode: ModeObserve}
	first := New(fallback, []Rule{
		{Prefix: "catalog:", Decision: Decision{Mode: ModeDeny}},
		{Prefix: "catalog:featured:", Decision: Decision{Mode: ModeCache, MaxItemBytes: 512, MaxLocalTTL: time.Second}},
	})
	second := New(fallback, []Rule{
		{Prefix: "catalog:featured:", Decision: Decision{Mode: ModeCache, MaxItemBytes: 512, MaxLocalTTL: time.Second}},
		{Prefix: "catalog:", Decision: Decision{Mode: ModeDeny}},
	})

	for _, key := range []string{"unmatched", "catalog:item", "catalog:featured:1"} {
		if got, want := first.Match(key), second.Match(key); got != want {
			t.Fatalf("rule order changed Match(%q): %+v != %+v", key, got, want)
		}
	}
	if got := first.Match("unmatched"); got != fallback {
		t.Fatalf("fallback = %+v, want %+v", got, fallback)
	}
}

func TestMatcherConcurrentReads(t *testing.T) {
	matcher := New(Decision{Mode: ModeDeny}, []Rule{
		{Prefix: "hot:", Decision: Decision{Mode: ModeCache, MaxItemBytes: 1024, MaxLocalTTL: time.Second}},
	})
	var wg sync.WaitGroup
	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 1000 {
				if got := matcher.Match("hot:key").Mode; got != ModeCache {
					t.Errorf("mode = %v, want cache", got)
					return
				}
			}
		}()
	}
	wg.Wait()
}

func TestMatcherMatchDoesNotAllocate(t *testing.T) {
	matcher := New(Decision{Mode: ModeDeny}, []Rule{
		{Prefix: "catalog:", Decision: Decision{Mode: ModeObserve}},
		{Prefix: "catalog:featured:", Decision: Decision{Mode: ModeCache, MaxItemBytes: 1024, MaxLocalTTL: time.Second}},
	})
	if allocations := testing.AllocsPerRun(1000, func() {
		_ = matcher.Match("catalog:featured:42")
	}); allocations != 0 {
		t.Fatalf("Match allocations = %f, want 0", allocations)
	}
}
