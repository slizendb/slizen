package upstream

import (
	"context"
	"time"
)

type Value struct {
	Data   []byte
	Exists bool
	PTTL   time.Duration
}

type Client interface {
	Ping(ctx context.Context) error
	Get(ctx context.Context, key string) (Value, error)
	MGet(ctx context.Context, keys []string) ([]Value, error)
	Do(ctx context.Context, args ...string) (any, error)
	Close() error
}
