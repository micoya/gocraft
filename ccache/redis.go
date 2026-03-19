package ccache

import (
	"context"
	"errors"
	"fmt"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

const defaultRedisTTL = 24 * time.Hour

// RedisCache 是基于 go-redis 的缓存实现。
type RedisCache struct {
	client     *goredis.Client
	defaultTTL time.Duration
	keyPrefix  string
}

// RedisOption 配置 RedisCache 的可选项。
type RedisOption func(*RedisCache)

// WithRedisDefaultTTL 设置默认 TTL，调用 Set/GetOrSet 时未指定 TTL 则使用此值。默认 24h。
func WithRedisDefaultTTL(ttl time.Duration) RedisOption {
	return func(c *RedisCache) { c.defaultTTL = ttl }
}

// WithRedisKeyPrefix 设置 key 前缀，默认无前缀。
func WithRedisKeyPrefix(prefix string) RedisOption {
	return func(c *RedisCache) { c.keyPrefix = prefix }
}

// NewRedis 创建 Redis 缓存。client 由调用方管理生命周期，Close 不会关闭连接。
func NewRedis(client *goredis.Client, opts ...RedisOption) *RedisCache {
	c := &RedisCache{
		client:     client,
		defaultTTL: defaultRedisTTL,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

func (c *RedisCache) k(key string) string {
	if c.keyPrefix == "" {
		return key
	}
	return c.keyPrefix + key
}

func (c *RedisCache) Get(ctx context.Context, key string) (string, bool, error) {
	val, err := c.client.Get(ctx, c.k(key)).Result()
	if errors.Is(err, goredis.Nil) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("ccache/redis: get %s: %w", key, err)
	}
	return val, true, nil
}

func (c *RedisCache) Set(ctx context.Context, key string, val string, ttl ...DurationOpt) error {
	d := resolveTTL(ttl, c.defaultTTL)
	if err := c.client.Set(ctx, c.k(key), val, d).Err(); err != nil {
		return fmt.Errorf("ccache/redis: set %s: %w", key, err)
	}
	return nil
}

func (c *RedisCache) Del(ctx context.Context, keys ...string) error {
	rKeys := make([]string, len(keys))
	for i, k := range keys {
		rKeys[i] = c.k(k)
	}
	if err := c.client.Del(ctx, rKeys...).Err(); err != nil {
		return fmt.Errorf("ccache/redis: del: %w", err)
	}
	return nil
}

func (c *RedisCache) GetOrSet(ctx context.Context, key string, fn func(ctx context.Context) (string, error), ttl ...DurationOpt) (string, error) {
	val, found, err := c.Get(ctx, key)
	if err != nil {
		return "", err
	}
	if found {
		return val, nil
	}

	val, err = fn(ctx)
	if err != nil {
		return "", err
	}

	if setErr := c.Set(ctx, key, val, ttl...); setErr != nil {
		return val, setErr
	}
	return val, nil
}

func (c *RedisCache) Close() error { return nil }
