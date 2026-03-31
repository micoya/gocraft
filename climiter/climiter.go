// Package climiter 提供配置简单、开箱即用的限流器，底层基于 github.com/mennanov/limiters。
//
// # 工厂函数（手动构造）
//
//   - NewSlidingWindowRedis  - 分布式滑动窗口（推荐首选）
//   - NewFixedWindowRedis    - 分布式固定窗口（最轻量）
//   - NewTokenBucketRedis    - 分布式令牌桶（支持突发）
//   - NewLocalSlidingWindow  - 单机内存滑动窗口（无需 Redis）
//
// # Registry（配置驱动多实例）
//
// 通过 config.LimiterItemConfig 声明一组命名限流器，由 NewFromConfig 批量创建，
// 之后通过 Registry.Must("name") 获取单个实例：
//
//	// config.yaml
//	//   limiter:
//	//     default:
//	//       algo: sliding_window
//	//       rate: 100
//	//       window: 1s
//	//       redis: default
//	//     strict:
//	//       algo: token_bucket
//	//       rate: 10
//	//       window: 1m
//	//       burst: 5
//	//       redis: default
//
//	reg, err := climiter.NewFromConfig(cfg.Limiter, func(name string) (*goredis.Client, error) {
//	    return cdao.Must[*goredis.Client](dao, "redis", name), nil
//	})
//
//	retryAfter, err := reg.Must("default").Limit(ctx, "user:123")
//	if errors.Is(err, climiter.ErrRateLimited) {
//	    // 被限流，建议等待 retryAfter 后重试
//	}
package climiter

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	redsyncgoredis "github.com/go-redsync/redsync/v4/redis/goredis/v9"
	"github.com/mennanov/limiters"
	goredis "github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/micoya/gocraft/config"
)

const tracerName = "climiter"

// 支持的算法名称常量。
const (
	AlgoSlidingWindow = "sliding_window" // 分布式滑动窗口（推荐）
	AlgoFixedWindow   = "fixed_window"   // 分布式固定窗口
	AlgoTokenBucket   = "token_bucket"   // 分布式令牌桶
	AlgoLocal         = "local"          // 单机内存滑动窗口
)

// ErrRateLimited 请求被限流时返回。
var ErrRateLimited = errors.New("climiter: rate limited")

// Limiter 是支持 per-key 限流的核心接口。
type Limiter interface {
	// Limit 检查 key 是否被限流。
	// 允许通过时返回 (0, nil)。
	// 被限流时返回 (retryAfter, ErrRateLimited)，retryAfter 为建议的最短等待时间。
	Limit(ctx context.Context, key string) (retryAfter time.Duration, err error)
}

// Option 是 Limiter 的可选配置项。
type Option func(*options)

type options struct {
	keyPrefix    string
	burst        int64
	instanceName string // 由 Registry 内部设置，用于 OTel span attribute
}

// WithKeyPrefix 设置 Redis key 前缀，默认 "climiter:"。
func WithKeyPrefix(prefix string) Option {
	return func(o *options) { o.keyPrefix = prefix }
}

// WithBurst 设置令牌桶的突发容量，仅对 NewTokenBucketRedis 生效，默认为 1（严格按速率限流，无突发）。
// 例如 WithBurst(10) 表示令牌桶最多可积累 10 个令牌，允许短时间内连续发出 10 个请求。
func WithBurst(n int64) Option {
	return func(o *options) { o.burst = n }
}

// withInstanceName 仅供包内使用，将 Registry 实例名透传给 perKeyLimiter
// 以便在 OTel span 中记录 ratelimit.name attribute。
func withInstanceName(name string) Option {
	return func(o *options) { o.instanceName = name }
}

func applyOptions(opts []Option) options {
	o := options{
		keyPrefix: "climiter:",
		burst:     1,
	}
	for _, opt := range opts {
		opt(&o)
	}
	return o
}

// ─────────────────────────────────────────────────────────────────────────────
// 内部实现
// ─────────────────────────────────────────────────────────────────────────────

// internalLimiter 是 mennanov/limiters 各算法的公共接口。
type internalLimiter interface {
	Limit(ctx context.Context) (time.Duration, error)
}

// perKeyLimiter 按 key 懒创建并缓存 internalLimiter 实例。
// 每个 key 对应一个独立的底层限流器，Redis-backend 下多实例部署共享 Redis 状态。
type perKeyLimiter struct {
	mu      sync.Mutex
	entries map[string]internalLimiter
	factory func(key string) internalLimiter
	algo    string // 算法名，用于 OTel span attribute
	name    string // Registry 实例名（可为空），用于 OTel span attribute
}

func newPerKeyLimiter(algo, name string, factory func(key string) internalLimiter) *perKeyLimiter {
	return &perKeyLimiter{
		algo:    algo,
		name:    name,
		entries: make(map[string]internalLimiter),
		factory: factory,
	}
}

// Limit 检查 key 是否被限流，并自动创建 OTel span 记录结果。
// 未配置 TracerProvider 时全局默认为 noop tracer，几乎零开销。
//
// Span attributes：
//   - ratelimit.key             — 被检查的 key
//   - ratelimit.algo            — 限流算法
//   - ratelimit.name            — Registry 实例名（仅通过 NewFromConfig 创建时存在）
//   - ratelimit.allowed         — 是否放行（true/false）
//   - ratelimit.retry_after_ms  — 被限流时的建议等待毫秒数
func (l *perKeyLimiter) Limit(ctx context.Context, key string) (time.Duration, error) {
	attrs := []attribute.KeyValue{
		attribute.String("ratelimit.key", key),
		attribute.String("ratelimit.algo", l.algo),
	}
	if l.name != "" {
		attrs = append(attrs, attribute.String("ratelimit.name", l.name))
	}

	ctx, span := otel.Tracer(tracerName).Start(ctx, "climiter.limit",
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(attrs...),
	)
	defer span.End()

	l.mu.Lock()
	b, ok := l.entries[key]
	if !ok {
		b = l.factory(key)
		l.entries[key] = b
	}
	l.mu.Unlock()

	w, err := b.Limit(ctx)
	if errors.Is(err, limiters.ErrLimitExhausted) {
		span.SetAttributes(
			attribute.Bool("ratelimit.allowed", false),
			attribute.Int64("ratelimit.retry_after_ms", w.Milliseconds()),
		)
		return w, ErrRateLimited
	}
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return w, err
	}
	span.SetAttributes(attribute.Bool("ratelimit.allowed", true))
	return w, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// 工厂函数
// ─────────────────────────────────────────────────────────────────────────────

// NewSlidingWindowRedis 创建基于 Redis 的分布式滑动窗口限流器。
//
// rate 为每 window 时间内允许的最大请求数。
//
// 推荐首选：边界平滑（不会出现固定窗口边界处 2x 突发的问题），无需分布式锁，
// Redis 操作仅用 pipeline，性能好。
func NewSlidingWindowRedis(client *goredis.Client, rate int64, window time.Duration, opts ...Option) Limiter {
	o := applyOptions(opts)
	return newPerKeyLimiter(AlgoSlidingWindow, o.instanceName, func(key string) internalLimiter {
		rKey := o.keyPrefix + key
		return limiters.NewSlidingWindow(
			rate, window,
			limiters.NewSlidingWindowRedis(client, rKey),
			limiters.NewSystemClock(),
			1e-9,
		)
	})
}

// NewFixedWindowRedis 创建基于 Redis 的分布式固定窗口限流器。
//
// rate 为每 window 时间内允许的最大请求数。
//
// 优点：资源消耗最低，实现最简单。
// 缺点：窗口边界处可能在短时间内允许约 2x 的请求（两个相邻窗口各打满）。
func NewFixedWindowRedis(client *goredis.Client, rate int64, window time.Duration, opts ...Option) Limiter {
	o := applyOptions(opts)
	return newPerKeyLimiter(AlgoFixedWindow, o.instanceName, func(key string) internalLimiter {
		rKey := o.keyPrefix + key
		return limiters.NewFixedWindow(
			rate, window,
			limiters.NewFixedWindowRedis(client, rKey),
			limiters.NewSystemClock(),
		)
	})
}

// NewTokenBucketRedis 创建基于 Redis 的分布式令牌桶限流器。
//
// rate 为每 window 时间内的稳定速率（即每 window/rate 补充 1 个令牌）。
// 通过 WithBurst(n) 可设置最大突发容量（默认 1，等价于严格限速）。
//
// 适合需要允许短时集中请求的场景，例如向外部 API 做平滑发送、上游回调积压消费等。
// 注意：令牌桶需要分布式锁保证精确性，内部使用 Redis 锁（基于 redsync），
// 每个 key 对应独立的锁，key 数量较多时请注意 Redis 锁资源。
func NewTokenBucketRedis(client *goredis.Client, rate int64, window time.Duration, opts ...Option) Limiter {
	o := applyOptions(opts)
	pool := redsyncgoredis.NewPool(client)
	refillRate := window / time.Duration(rate)

	return newPerKeyLimiter(AlgoTokenBucket, o.instanceName, func(key string) internalLimiter {
		rKey := o.keyPrefix + key
		lockKey := o.keyPrefix + key + ":lock"

		// 存储 TTL：令牌桶填满所需时间的 10 倍，最少 1 分钟，确保状态不会提前过期。
		ttl := time.Duration(o.burst) * refillRate * 10
		if ttl < time.Minute {
			ttl = time.Minute
		}

		return limiters.NewTokenBucket(
			o.burst, refillRate,
			limiters.NewLockRedis(pool, lockKey),
			limiters.NewTokenBucketRedis(client, rKey, ttl, false),
			limiters.NewSystemClock(),
			limiters.NewStdLogger(),
		)
	})
}

// NewLocalSlidingWindow 创建基于内存的单机滑动窗口限流器，无需 Redis。
//
// rate 为每 window 时间内允许的最大请求数。
//
// 适合单实例服务或开发 / 测试环境。
// 注意：多副本部署时各实例独立计数，不共享状态，实际总放量 = rate × 副本数。
func NewLocalSlidingWindow(rate int64, window time.Duration, opts ...Option) Limiter {
	o := applyOptions(opts)
	return newPerKeyLimiter(AlgoLocal, o.instanceName, func(_ string) internalLimiter {
		return limiters.NewSlidingWindow(
			rate, window,
			limiters.NewSlidingWindowInMemory(),
			limiters.NewSystemClock(),
			1e-9,
		)
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// Registry：配置驱动的多实例管理
// ─────────────────────────────────────────────────────────────────────────────

// RedisGetter 按名称获取 Redis 客户端，供 NewFromConfig 构建 Redis-backed 限流器时使用。
// 通常封装 cdao.Must[*goredis.Client](dao, "redis", name)。
type RedisGetter func(name string) (*goredis.Client, error)

// Registry 持有一组命名的 Limiter 实例。
type Registry struct {
	limiters map[string]Limiter
}

// NewFromConfig 根据配置批量创建限流器并返回 Registry。
//
// cfg 为 config.Config[T].Limiter 字段（map[string]LimiterItemConfig）。
// getRedis 用于按名称获取 Redis 客户端；algo 为 "local" 时可传 nil。
//
// 配置示例（config.yaml）：
//
//	limiter:
//	  default:
//	    algo: sliding_window
//	    rate: 100
//	    window: 1s
//	    redis: default
//	  strict:
//	    algo: token_bucket
//	    rate: 10
//	    window: 1m
//	    burst: 5
//	    redis: default
func NewFromConfig(cfg map[string]config.LimiterItemConfig, getRedis RedisGetter) (*Registry, error) {
	r := &Registry{limiters: make(map[string]Limiter, len(cfg))}

	for name, item := range cfg {
		lim, err := buildFromItem(name, item, getRedis)
		if err != nil {
			return nil, err
		}
		r.limiters[name] = lim
	}

	return r, nil
}

func buildFromItem(name string, item config.LimiterItemConfig, getRedis RedisGetter) (Limiter, error) {
	if item.Rate <= 0 {
		return nil, fmt.Errorf("climiter: %q: rate must be > 0", name)
	}
	if item.Window <= 0 {
		return nil, fmt.Errorf("climiter: %q: window must be > 0", name)
	}

	algo := item.Algo
	if algo == "" {
		algo = AlgoSlidingWindow
	}

	if algo == AlgoLocal {
		return NewLocalSlidingWindow(item.Rate, item.Window, withInstanceName(name)), nil
	}

	redisName := item.Redis
	if redisName == "" {
		redisName = "default"
	}
	if getRedis == nil {
		return nil, fmt.Errorf("climiter: %q: getRedis is required for algo %q", name, algo)
	}
	client, err := getRedis(redisName)
	if err != nil {
		return nil, fmt.Errorf("climiter: %q: get redis %q: %w", name, redisName, err)
	}

	opts := []Option{withInstanceName(name)}
	if item.KeyPrefix != "" {
		opts = append(opts, WithKeyPrefix(item.KeyPrefix))
	}

	switch algo {
	case AlgoSlidingWindow:
		return NewSlidingWindowRedis(client, item.Rate, item.Window, opts...), nil
	case AlgoFixedWindow:
		return NewFixedWindowRedis(client, item.Rate, item.Window, opts...), nil
	case AlgoTokenBucket:
		if item.Burst > 0 {
			opts = append(opts, WithBurst(item.Burst))
		}
		return NewTokenBucketRedis(client, item.Rate, item.Window, opts...), nil
	default:
		return nil, fmt.Errorf("climiter: %q: unknown algo %q (valid: sliding_window, fixed_window, token_bucket, local)", name, algo)
	}
}

// Get 按名称获取 Limiter。不存在时返回 (nil, false)。
func (r *Registry) Get(name string) (Limiter, bool) {
	lim, ok := r.limiters[name]
	return lim, ok
}

// Must 按名称获取 Limiter。不存在时 panic。
func (r *Registry) Must(name string) Limiter {
	lim, ok := r.limiters[name]
	if !ok {
		panic(fmt.Sprintf("climiter: limiter %q not found in registry", name))
	}
	return lim
}
