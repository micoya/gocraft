// Package ccache 提供统一的缓存抽象接口与多种实现。
//
// 包含三种实现：
//   - Redis：基于 go-redis，TTL 精确，适合跨进程共享缓存
//   - Memory：基于 ristretto，本地内存，零延迟，适合热点数据
//   - Layered：L1(内存) + L2(Redis) 两级缓存，读取优先命中 L1，
//     L1 未命中时查 L2 并回填，写入同步更新两级
//
// 所有实现缓存值类型为 string，业务层自行 JSON 序列化。
//
// 使用示例：
//
//	// 单级 Redis 缓存
//	c := ccache.NewRedis(redisClient)
//
//	// 两级缓存
//	l1 := ccache.NewMemory(ristrettoCache)
//	l2 := ccache.NewRedis(redisClient)
//	c := ccache.NewLayered(l1, l2)
//
//	// 写入
//	c.Set(ctx, "user:1", `{"name":"alice"}`, time.Hour)
//
//	// 读取（GetOrSet 适合缓存穿透防护）
//	val, err := c.GetOrSet(ctx, "user:1", time.Hour, func(ctx context.Context) (string, error) {
//	    return fetchFromDB(ctx, 1)
//	})
package ccache

import "context"

// Cache 是统一的缓存操作抽象。
type Cache interface {
	// Get 获取缓存值。found=false 表示 key 不存在（未命中）。
	Get(ctx context.Context, key string) (val string, found bool, err error)
	// Set 写入缓存，ttl <= 0 表示永不过期（仅内存缓存支持）。
	Set(ctx context.Context, key string, val string, ttl ...DurationOpt) error
	// Del 删除一个或多个 key。
	Del(ctx context.Context, keys ...string) error
	// GetOrSet 先尝试获取缓存，未命中时调用 fn 生成并写入缓存，返回最终值。
	// fn 并发调用时由上层业务决定是否需要防击穿（本方法不做 singleflight）。
	GetOrSet(ctx context.Context, key string, fn func(ctx context.Context) (string, error), ttl ...DurationOpt) (string, error)
	// Close 释放实现持有的资源（不关闭外部传入的连接/缓存）。
	Close() error
}
