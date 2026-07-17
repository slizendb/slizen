package cachepolicy

import (
	"sort"
	"strings"
	"time"
)

type Mode uint8

const (
	ModeDeny Mode = iota
	ModeObserve
	ModeCache
)

func ParseMode(value string) (Mode, bool) {
	switch value {
	case "deny":
		return ModeDeny, true
	case "observe":
		return ModeObserve, true
	case "cache":
		return ModeCache, true
	default:
		return ModeDeny, false
	}
}

type Decision struct {
	Mode         Mode
	MaxItemBytes int64
	MaxLocalTTL  time.Duration
}

type Rule struct {
	Prefix   string
	Decision Decision
}

// Matcher is immutable after construction and safe for concurrent reads.
type Matcher struct {
	fallback Decision
	rules    map[string]Decision
	lengths  []int
}

func New(fallback Decision, rules []Rule) *Matcher {
	byPrefix := make(map[string]Decision, len(rules))
	seenLengths := make(map[int]struct{}, len(rules))
	lengths := make([]int, 0, len(rules))
	for _, rule := range rules {
		prefix := strings.Clone(rule.Prefix)
		byPrefix[prefix] = rule.Decision
		if _, seen := seenLengths[len(prefix)]; seen {
			continue
		}
		seenLengths[len(prefix)] = struct{}{}
		lengths = append(lengths, len(prefix))
	}
	sort.Sort(sort.Reverse(sort.IntSlice(lengths)))
	return &Matcher{fallback: fallback, rules: byPrefix, lengths: lengths}
}

func (m *Matcher) Match(key string) Decision {
	for _, length := range m.lengths {
		if length > len(key) {
			continue
		}
		if decision, ok := m.rules[key[:length]]; ok {
			return decision
		}
	}
	return m.fallback
}
