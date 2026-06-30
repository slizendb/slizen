package testutil

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/slizendb/slizen/internal/upstream"
)

type FakeUpstream struct {
	mu       sync.Mutex
	values   map[string]storedValue
	fail     bool
	getCalls map[string]int
	doCalls  int
	doNil    bool
	delay    time.Duration
}

type storedValue struct {
	data      []byte
	expiresAt time.Time
}

func NewFakeUpstream() *FakeUpstream {
	return &FakeUpstream{
		values:   make(map[string]storedValue),
		getCalls: make(map[string]int),
	}
}

func (f *FakeUpstream) SetFailure(fail bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fail = fail
}

func (f *FakeUpstream) SetDelay(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.delay = d
}

func (f *FakeUpstream) SetDoNil(doNil bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.doNil = doNil
}

func (f *FakeUpstream) GetCallCount(key string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.getCalls[key]
}

func (f *FakeUpstream) Put(key string, value []byte, ttl time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var expiresAt time.Time
	if ttl > 0 {
		expiresAt = time.Now().Add(ttl)
	}
	f.values[key] = storedValue{data: append([]byte(nil), value...), expiresAt: expiresAt}
}

func (f *FakeUpstream) Ping(ctx context.Context) error {
	f.mu.Lock()
	fail := f.fail
	f.mu.Unlock()
	if fail {
		return errors.New("upstream unavailable")
	}
	return nil
}

func (f *FakeUpstream) Get(ctx context.Context, key string) (upstream.Value, error) {
	delay, err := f.beforeCall(ctx, key)
	if err != nil {
		return upstream.Value{}, err
	}
	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return upstream.Value{}, ctx.Err()
		}
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	value, ok := f.values[key]
	if !ok || expired(value) {
		delete(f.values, key)
		return upstream.Value{Exists: false}, nil
	}
	return upstream.Value{Exists: true, Data: append([]byte(nil), value.data...), PTTL: ttl(value)}, nil
}

func (f *FakeUpstream) MGet(ctx context.Context, keys []string) ([]upstream.Value, error) {
	out := make([]upstream.Value, len(keys))
	for i, key := range keys {
		value, err := f.Get(ctx, key)
		if err != nil {
			return nil, err
		}
		out[i] = value
	}
	return out, nil
}

func (f *FakeUpstream) Do(ctx context.Context, args ...string) (any, error) {
	if len(args) == 0 {
		return nil, errors.New("empty command")
	}
	if _, err := f.beforeCall(ctx, ""); err != nil {
		return nil, err
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	f.doCalls++
	if f.doNil {
		return nil, nil
	}

	cmd := strings.ToUpper(args[0])
	switch cmd {
	case "SET":
		if len(args) < 3 {
			return nil, errors.New("wrong number of arguments")
		}
		f.values[args[1]] = storedValue{data: []byte(args[2])}
		return "OK", nil
	case "SETEX":
		if len(args) != 4 {
			return nil, errors.New("wrong number of arguments")
		}
		seconds, err := strconv.Atoi(args[2])
		if err != nil {
			return nil, err
		}
		f.values[args[1]] = storedValue{data: []byte(args[3]), expiresAt: time.Now().Add(time.Duration(seconds) * time.Second)}
		return "OK", nil
	case "PSETEX":
		if len(args) != 4 {
			return nil, errors.New("wrong number of arguments")
		}
		millis, err := strconv.Atoi(args[2])
		if err != nil {
			return nil, err
		}
		f.values[args[1]] = storedValue{data: []byte(args[3]), expiresAt: time.Now().Add(time.Duration(millis) * time.Millisecond)}
		return "OK", nil
	case "DEL", "UNLINK":
		deleted := 0
		for _, key := range args[1:] {
			if _, ok := f.values[key]; ok {
				deleted++
				delete(f.values, key)
			}
		}
		return int64(deleted), nil
	case "EXPIRE", "PEXPIRE":
		if len(args) != 3 {
			return nil, errors.New("wrong number of arguments")
		}
		value, ok := f.values[args[1]]
		if !ok {
			return int64(0), nil
		}
		n, err := strconv.Atoi(args[2])
		if err != nil {
			return nil, err
		}
		if cmd == "EXPIRE" {
			value.expiresAt = time.Now().Add(time.Duration(n) * time.Second)
		} else {
			value.expiresAt = time.Now().Add(time.Duration(n) * time.Millisecond)
		}
		f.values[args[1]] = value
		return int64(1), nil
	case "PERSIST":
		value, ok := f.values[args[1]]
		if !ok {
			return int64(0), nil
		}
		value.expiresAt = time.Time{}
		f.values[args[1]] = value
		return int64(1), nil
	case "TTL", "PTTL":
		value, ok := f.values[args[1]]
		if !ok || expired(value) {
			return int64(-2), nil
		}
		if value.expiresAt.IsZero() {
			return int64(-1), nil
		}
		left := time.Until(value.expiresAt)
		if cmd == "TTL" {
			return int64(left / time.Second), nil
		}
		return int64(left / time.Millisecond), nil
	case "EXISTS":
		var count int64
		for _, key := range args[1:] {
			if value, ok := f.values[key]; ok && !expired(value) {
				count++
			}
		}
		return count, nil
	default:
		return nil, errors.New("unsupported fake command")
	}
}

func (f *FakeUpstream) Close() error { return nil }

func (f *FakeUpstream) beforeCall(ctx context.Context, key string) (time.Duration, error) {
	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	default:
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail {
		return 0, errors.New("upstream unavailable")
	}
	if key != "" {
		f.getCalls[key]++
	}
	return f.delay, nil
}

func expired(value storedValue) bool {
	return !value.expiresAt.IsZero() && time.Now().After(value.expiresAt)
}

func ttl(value storedValue) time.Duration {
	if value.expiresAt.IsZero() {
		return -1
	}
	return time.Until(value.expiresAt)
}
