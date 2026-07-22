package upstream

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/slizendb/slizen/internal/config"
)

type RedisClient struct {
	client *redis.Client
}

const redisPTTLMissing = time.Duration(-2)

func NewRedisClient(cfg config.UpstreamConfig) *RedisClient {
	return &RedisClient{
		client: redis.NewClient(&redis.Options{
			Addr:         cfg.Address,
			Username:     cfg.Username,
			Password:     cfg.Password,
			DB:           cfg.Database,
			DialTimeout:  cfg.DialTimeout,
			ReadTimeout:  cfg.ReadTimeout,
			WriteTimeout: cfg.WriteTimeout,
		}),
	}
}

func (c *RedisClient) Ping(ctx context.Context) error {
	return c.client.Ping(ctx).Err()
}

func (c *RedisClient) Get(ctx context.Context, key string) (Value, error) {
	pipe := c.client.Pipeline()
	get := pipe.Get(ctx, key)
	pttl := pipe.PTTL(ctx, key)
	_, execErr := pipe.Exec(ctx)

	data, err := get.Bytes()
	if err == redis.Nil {
		var redisErr redis.Error
		// A Redis reply error is harmless after a confirmed miss; an interrupted pipeline is not.
		if execErr != nil && !errors.As(execErr, &redisErr) {
			return Value{}, execErr
		}
		return Value{Exists: false}, nil
	}
	if err != nil {
		return Value{}, err
	}
	ttl, err := pttl.Result()
	if err != nil {
		return Value{}, err
	}
	if execErr != nil {
		return Value{}, execErr
	}
	if ttl == redisPTTLMissing {
		return Value{Exists: false}, nil
	}
	return Value{Data: append([]byte(nil), data...), Exists: true, PTTL: ttl}, nil
}

func (c *RedisClient) MGet(ctx context.Context, keys []string) ([]Value, error) {
	if len(keys) == 0 {
		return nil, nil
	}
	pipe := c.client.Pipeline()
	mget := pipe.MGet(ctx, keys...)
	ttlCmds := make([]*redis.DurationCmd, len(keys))
	for i, key := range keys {
		ttlCmds[i] = pipe.PTTL(ctx, key)
	}
	_, err := pipe.Exec(ctx)
	if err != nil && err != redis.Nil {
		return nil, err
	}

	raw, err := mget.Result()
	if err != nil && err != redis.Nil {
		return nil, err
	}
	values := make([]Value, len(keys))
	for i, item := range raw {
		if item == nil {
			values[i] = Value{Exists: false}
			continue
		}
		var data []byte
		switch v := item.(type) {
		case string:
			data = []byte(v)
		case []byte:
			data = append([]byte(nil), v...)
		default:
			data = []byte(fmt.Sprint(v))
		}
		ttl := ttlCmds[i].Val()
		if ttl == redisPTTLMissing {
			values[i] = Value{Exists: false}
			continue
		}
		values[i] = Value{Data: data, Exists: true, PTTL: ttl}
	}
	return values, nil
}

func (c *RedisClient) Do(ctx context.Context, args ...string) (any, error) {
	redisArgs := make([]any, len(args))
	for i, arg := range args {
		redisArgs[i] = arg
	}
	result, err := c.client.Do(ctx, redisArgs...).Result()
	if err == redis.Nil {
		return nil, nil
	}
	return result, err
}

func (c *RedisClient) Close() error {
	return c.client.Close()
}
