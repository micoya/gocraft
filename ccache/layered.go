package ccache

import (
	"context"
	"time"
)

// LayeredCache 实现 L1(内存) + L2(Redis) 两级缓存策略。
//
// 读取策略：L1 命中 → 直接返回；L1 未命中 → 查 L2，命中后回填 L1。
// 写入策略：同步写入 L1 和 L2。
// 删除策略：同时从 L1 和 L2 删除。
type LayeredCache struct {
	l1         Cache
	l2         Cache
	l1TTL      time.Duration // L1 的回填 TTL，避免内存层 TTL 超过 L2
}

// LayeredOption 配置 LayeredCache 的可选项。
type LayeredOption func(*LayeredCache)

// WithL1TTL 设置回填 L1 时使用的 TTL，默认与写入时一致。
// 建议设置为比 L2 TTL 更短的值，如 L2=1h 时设置 L1=5min。
func WithL1TTL(ttl time.Duration) LayeredOption {
	return func(c *LayeredCache) { c.l1TTL = ttl }
}

// NewLayered 创建两级缓存。l1 为快速内存缓存，l2 为持久远端缓存（如 Redis）。
func NewLayered(l1, l2 Cache, opts ...LayeredOption) *LayeredCache {
	c := &LayeredCache{l1: l1, l2: l2}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

func (c *LayeredCache) Get(ctx context.Context, key string) (string, bool, error) {
	// 先查 L1
	if val, found, err := c.l1.Get(ctx, key); err == nil && found {
		return val, true, nil
	}
	// 再查 L2
	val, found, err := c.l2.Get(ctx, key)
	if err != nil || !found {
		return val, found, err
	}
	// L2 命中，回填 L1
	if c.l1TTL > 0 {
		_ = c.l1.Set(ctx, key, val, c.l1TTL)
	} else {
		_ = c.l1.Set(ctx, key, val)
	}
	return val, true, nil
}

func (c *LayeredCache) Set(ctx context.Context, key string, val string, ttl ...DurationOpt) error {
	// 同步写入两级，其中一级失败不影响另一级
	l1TTL := ttl
	if c.l1TTL > 0 {
		l1TTL = []DurationOpt{c.l1TTL}
	}
	_ = c.l1.Set(ctx, key, val, l1TTL...)
	return c.l2.Set(ctx, key, val, ttl...)
}

func (c *LayeredCache) Del(ctx context.Context, keys ...string) error {
	_ = c.l1.Del(ctx, keys...)
	return c.l2.Del(ctx, keys...)
}

func (c *LayeredCache) GetOrSet(ctx context.Context, key string, fn func(ctx context.Context) (string, error), ttl ...DurationOpt) (string, error) {
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

	_ = c.Set(ctx, key, val, ttl...)
	return val, nil
}

func (c *LayeredCache) Close() error {
	_ = c.l1.Close()
	return c.l2.Close()
}
