// Package ccron 提供基于 robfig/cron 的定时任务调度器，支持 OTel 链路追踪和分布式防重复执行。
//
// 特性：
//   - 每次任务执行自动创建 OTel span，覆盖任务执行全程
//   - 分布式模式：执行前尝试抢占分布式锁，未抢到则跳过本次执行（适合多实例部署）
//   - 支持自定义时区（默认 Asia/Shanghai）
//   - 任务内部 panic 被 recover 并记录到日志，不影响调度器运行
//
// 基本用法：
//
//	// 普通模式
//	s := ccron.New(ccron.WithTimezone("Asia/Shanghai"))
//	s.Add("清理过期订单", "0 2 * * *", func(ctx context.Context) {
//	    cleanup(ctx)
//	})
//	s.Start()
//	defer s.Stop(ctx)
//
//	// 分布式防重复执行
//	locker := clocker.New(redisClient)
//	s := ccron.New(
//	    ccron.WithTimezone("Asia/Shanghai"),
//	    ccron.WithLocker(locker, 5*time.Minute),
//	)
package ccron

import (
	"context"
	"fmt"
	"log/slog"
	"runtime/debug"
	"time"

	"github.com/robfig/cron/v3"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

const tracerName = "ccron"

// Locker 是分布式锁接口，由 clocker.Locker 或其他实现满足。
type Locker interface {
	TryLock(ctx context.Context, key string, ttl time.Duration) (Lock, error)
}

// Lock 是分布式锁持有句柄。
type Lock interface {
	Unlock(ctx context.Context) error
}

// Scheduler 是定时任务调度器。
type Scheduler struct {
	c      *cron.Cron
	log    *slog.Logger
	locker Locker
	lockTTL time.Duration
}

// Option 配置 Scheduler 的可选项。
type Option func(*Scheduler)

// WithTimezone 设置调度器时区，默认 "Asia/Shanghai"。
// 传入空字符串时使用本地时区。
func WithTimezone(tz string) Option {
	return func(s *Scheduler) {
		if tz == "" {
			s.c = cron.New(cron.WithSeconds())
			return
		}
		loc, err := time.LoadLocation(tz)
		if err != nil {
			s.log.Warn("ccron: invalid timezone, fallback to local", "timezone", tz, "error", err)
			return
		}
		s.c = cron.New(cron.WithLocation(loc), cron.WithSeconds())
	}
}

// WithLogger 设置日志实例。
func WithLogger(log *slog.Logger) Option {
	return func(s *Scheduler) {
		if log != nil {
			s.log = log
		}
	}
}

// WithLocker 启用分布式防重复执行。
// 每次任务触发前会尝试获取 name 对应的分布式锁，未获取到则跳过本次执行。
// lockTTL 应略大于任务预期最长执行时间（Watchdog 会自动续期）。
func WithLocker(locker Locker, lockTTL time.Duration) Option {
	return func(s *Scheduler) {
		s.locker = locker
		s.lockTTL = lockTTL
	}
}

// New 创建 Scheduler 实例。默认时区为 Asia/Shanghai。
func New(opts ...Option) *Scheduler {
	loc, _ := time.LoadLocation("Asia/Shanghai")
	s := &Scheduler{
		c:       cron.New(cron.WithLocation(loc), cron.WithSeconds()),
		log:     slog.Default(),
		lockTTL: 5 * time.Minute,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Add 注册一个定时任务。
//   - name：任务名，用于日志和分布式锁 key
//   - spec：cron 表达式（支持 6 位含秒，如 "0 30 9 * * *" 表示每天 09:30:00）
//   - fn：任务函数，接收带 OTel span 的 context
//
// 返回任务 ID 和可能的语法错误。
func (s *Scheduler) Add(name, spec string, fn func(ctx context.Context)) (cron.EntryID, error) {
	id, err := s.c.AddFunc(spec, s.wrap(name, fn))
	if err != nil {
		return 0, fmt.Errorf("ccron: add %q: %w", name, err)
	}
	s.log.Info("ccron: job registered", "name", name, "spec", spec)
	return id, nil
}

// Remove 移除已注册的任务。
func (s *Scheduler) Remove(id cron.EntryID) {
	s.c.Remove(id)
}

// Start 启动调度器（非阻塞）。
func (s *Scheduler) Start() {
	s.c.Start()
	s.log.Info("ccron: scheduler started")
}

// Stop 优雅停止调度器，等待正在执行的任务完成后返回。
func (s *Scheduler) Stop(_ context.Context) {
	ctx := s.c.Stop()
	<-ctx.Done()
	s.log.Info("ccron: scheduler stopped")
}

// wrap 包装任务函数，注入 OTel span、panic 恢复和分布式锁。
func (s *Scheduler) wrap(name string, fn func(ctx context.Context)) func() {
	return func() {
		ctx := context.Background()

		// 分布式锁：抢到才执行
		if s.locker != nil {
			lk, err := s.locker.TryLock(ctx, "ccron:"+name, s.lockTTL)
			if err != nil {
				// 未抢到锁，跳过本次
				s.log.Debug("ccron: job skipped (lock not acquired)", "name", name)
				return
			}
			defer func() { _ = lk.Unlock(ctx) }()
		}

		ctx, span := otel.Tracer(tracerName).Start(ctx, "cron: "+name,
			trace.WithSpanKind(trace.SpanKindInternal),
			trace.WithAttributes(attribute.String("cron.job.name", name)),
		)
		defer span.End()

		s.log.Info("ccron: job started", "name", name)

		defer func() {
			if r := recover(); r != nil {
				err := fmt.Errorf("panic: %v", r)
				span.RecordError(err)
				span.SetStatus(codes.Error, err.Error())
				s.log.Error("ccron: job panic recovered",
					"name", name,
					"panic", r,
					"stack", string(debug.Stack()),
				)
			}
		}()

		fn(ctx)

		s.log.Info("ccron: job finished", "name", name)
	}
}
