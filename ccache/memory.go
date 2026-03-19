package ccache

import (
	"context"
	"fmt"
	"time"

	"github.com/dgraph-io/ristretto/v2"
)

const defaultMemoryTTL = time.Hour

// MemoryCache 是基于 ristretto 的本地内存缓存实现。
type MemoryCache struct {
	cache      *ristretto.Cache[string, string]
	defaultTTL time.Duration
}

// MemoryOption 配置 MemoryCache 的可选项。
type MemoryOption func(*MemoryCache)

// WithMemoryDefaultTTL 设置默认 TTL。默认 1h。
func WithMemoryDefaultTTL(ttl time.Duration) MemoryOption {
	return func(c *MemoryCache) { c.defaultTTL = ttl }
}

// NewMemory 基于已有的 ristretto.Cache 创建内存缓存。
// cache 的生命周期由调用方管理，Close 不会关闭底层缓存。
func NewMemory(cache *ristretto.Cache[string, string], opts ...MemoryOption) *MemoryCache {
	c := &MemoryCache{
		cache:      cache,
		defaultTTL: defaultMemoryTTL,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// NewMemoryFromConfig 根据参数创建 ristretto 缓存并包装为 MemoryCache。
// numCounters 建议为预期条目数的 10 倍，maxCost 为最大字节数（如 512<<20 即 512MB）。
func NewMemoryFromConfig(numCounters, maxCost int64, opts ...MemoryOption) (*MemoryCache, error) {
	cache, err := ristretto.NewCache(&ristretto.Config[string, string]{
		NumCounters: numCounters,
		MaxCost:     maxCost,
		BufferItems: 64,
	})
	if err != nil {
		return nil, fmt.Errorf("ccache/memory: create cache: %w", err)
	}
	return NewMemory(cache, opts...), nil
}

func (c *MemoryCache) Get(_ context.Context, key string) (string, bool, error) {
	val, found := c.cache.Get(key)
	return val, found, nil
}

func (c *MemoryCache) Set(_ context.Context, key string, val string, ttl ...DurationOpt) error {
	d := resolveTTL(ttl, c.defaultTTL)
	// ristretto 的 cost 按字节数计，允许缓存按实际大小计费
	c.cache.SetWithTTL(key, val, int64(len(val)), d)
	c.cache.Wait()
	return nil
}

func (c *MemoryCache) Del(_ context.Context, keys ...string) error {
	for _, k := range keys {
		c.cache.Del(k)
	}
	return nil
}

func (c *MemoryCache) GetOrSet(ctx context.Context, key string, fn func(ctx context.Context) (string, error), ttl ...DurationOpt) (string, error) {
	val, found, _ := c.Get(ctx, key)
	if found {
		return val, nil
	}

	val, err := fn(ctx)
	if err != nil {
		return "", err
	}

	_ = c.Set(ctx, key, val, ttl...)
	return val, nil
}

func (c *MemoryCache) Close() error { return nil }
