// Package clocker 提供基于 Redis 的分布式锁。
//
// 设计要点：
//   - SET NX EX 原子命令加锁，随机 token 标识持有者身份
//   - Lua 脚本保证解锁原子性（先验证 token，再删除），防止误删他人持有的锁
//   - Lock 阻塞等待模式使用指数退避重试，不做忙等
//   - Watchdog 自动续期：持有锁期间每 TTL/3 时间刷新过期时间，防止业务未执行完锁已过期
//
// 基本用法：
//
//	locker := clocker.New(redisClient)
//
//	// 非阻塞尝试加锁
//	lock, err := locker.TryLock(ctx, "my-key", 30*time.Second)
//	if errors.Is(err, clocker.ErrLockNotAcquired) {
//	    // 锁已被占用
//	}
//	defer lock.Unlock(ctx)
//
//	// 阻塞等待直到获取到锁或 ctx 取消
//	lock, err := locker.Lock(ctx, "my-key", 30*time.Second)
package clocker

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

// ErrLockNotAcquired 在 TryLock 未能获取到锁时返回。
var ErrLockNotAcquired = errors.New("clocker: lock not acquired")

// Locker 是分布式锁工厂，基于单个 Redis 实例。
type Locker struct {
	client *goredis.Client
	opts   lockerOptions
}

type lockerOptions struct {
	keyPrefix   string
	retryMin    time.Duration
	retryMax    time.Duration
	retryFactor float64
}

// Option 配置 Locker 的可选项。
type Option func(*lockerOptions)

// WithKeyPrefix 设置 Redis key 前缀，默认 "clocker:"。
func WithKeyPrefix(prefix string) Option {
	return func(o *lockerOptions) { o.keyPrefix = prefix }
}

// WithRetryInterval 设置 Lock 阻塞等待时的退避区间，默认 [50ms, 500ms]，指数增长因子 1.5。
func WithRetryInterval(min, max time.Duration) Option {
	return func(o *lockerOptions) {
		o.retryMin = min
		o.retryMax = max
	}
}

// New 创建 Locker 实例。
func New(client *goredis.Client, opts ...Option) *Locker {
	o := lockerOptions{
		keyPrefix:   "clocker:",
		retryMin:    50 * time.Millisecond,
		retryMax:    500 * time.Millisecond,
		retryFactor: 1.5,
	}
	for _, opt := range opts {
		opt(&o)
	}
	return &Locker{client: client, opts: o}
}

// TryLock 尝试获取锁，锁已被占用时立即返回 ErrLockNotAcquired（非阻塞）。
// ttl 为锁的自动过期时间，Watchdog 会在此时间内自动续期。
func (l *Locker) TryLock(ctx context.Context, key string, ttl time.Duration) (Lock, error) {
	token := newToken()
	rKey := l.opts.keyPrefix + key

	ok, err := l.client.SetNX(ctx, rKey, token, ttl).Result()
	if err != nil {
		return nil, fmt.Errorf("clocker: setnx %s: %w", key, err)
	}
	if !ok {
		return nil, ErrLockNotAcquired
	}

	lk := &lock{
		client: l.client,
		key:    rKey,
		token:  token,
		ttl:    ttl,
	}
	lk.startWatchdog()
	return lk, nil
}

// Lock 阻塞直到获取到锁或 ctx 取消。
// 内部使用指数退避重试，不做忙等。
func (l *Locker) Lock(ctx context.Context, key string, ttl time.Duration) (Lock, error) {
	wait := l.opts.retryMin
	for {
		lk, err := l.TryLock(ctx, key, ttl)
		if err == nil {
			return lk, nil
		}
		if !errors.Is(err, ErrLockNotAcquired) {
			return nil, err
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(wait):
		}

		wait = time.Duration(float64(wait) * l.opts.retryFactor)
		if wait > l.opts.retryMax {
			wait = l.opts.retryMax
		}
	}
}

// Lock 代表一个已持有的分布式锁。
type Lock interface {
	// Unlock 释放锁。幂等，锁已过期时静默返回。
	Unlock(ctx context.Context) error
	// Refresh 手动延长锁的过期时间（Watchdog 自动续期通常无需手动调用）。
	Refresh(ctx context.Context, ttl time.Duration) error
}

// Lua 脚本：先验证 token，再删除 key，保证原子性。
const unlockScript = `
if redis.call("GET", KEYS[1]) == ARGV[1] then
    return redis.call("DEL", KEYS[1])
else
    return 0
end`

// Lua 脚本：先验证 token，再设置新 TTL，保证原子性。
const refreshScript = `
if redis.call("GET", KEYS[1]) == ARGV[1] then
    return redis.call("PEXPIRE", KEYS[1], ARGV[2])
else
    return 0
end`

type lock struct {
	client       *goredis.Client
	key          string
	token        string
	ttl          time.Duration
	stopWatchdog context.CancelFunc
}

func (l *lock) Unlock(ctx context.Context) error {
	if l.stopWatchdog != nil {
		l.stopWatchdog()
		l.stopWatchdog = nil
	}
	res, err := l.client.Eval(ctx, unlockScript, []string{l.key}, l.token).Int()
	if err != nil {
		return fmt.Errorf("clocker: unlock: %w", err)
	}
	if res == 0 {
		// 锁已过期或被他人持有，视为已释放
		return nil
	}
	return nil
}

func (l *lock) Refresh(ctx context.Context, ttl time.Duration) error {
	ms := ttl.Milliseconds()
	res, err := l.client.Eval(ctx, refreshScript, []string{l.key}, l.token, ms).Int()
	if err != nil {
		return fmt.Errorf("clocker: refresh: %w", err)
	}
	if res == 0 {
		return fmt.Errorf("clocker: refresh: lock expired or owned by another holder")
	}
	return nil
}

// startWatchdog 启动自动续期 goroutine，每 ttl/3 刷新一次过期时间。
func (l *lock) startWatchdog() {
	ctx, cancel := context.WithCancel(context.Background())
	l.stopWatchdog = cancel

	interval := l.ttl / 3
	if interval < 100*time.Millisecond {
		interval = 100 * time.Millisecond
	}

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_ = l.Refresh(ctx, l.ttl)
			}
		}
	}()
}

func newToken() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
