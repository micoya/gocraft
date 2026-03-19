// Package cworker 提供带并发控制和 panic 恢复的后台任务 Worker 池。
//
// 核心能力：
//   - 信号量限制并发数，避免无限制创建 goroutine
//   - 每个 goroutine 自动 recover panic，并通过 slog 记录堆栈
//   - 支持优雅关闭：Stop 等待所有进行中的任务结束后返回
//
// 基本用法：
//
//	pool := cworker.New(cworker.WithConcurrency(20))
//
//	pool.Go(ctx, func(ctx context.Context) {
//	    doWork(ctx)
//	})
//
//	// 优雅关闭（等待在途任务）
//	pool.Stop(shutdownCtx)
package cworker

import (
	"context"
	"log/slog"
	"runtime"
	"runtime/debug"
	"sync"
	"sync/atomic"
)

// Pool 是并发受控的后台任务池。
type Pool struct {
	sem    chan struct{}
	wg     sync.WaitGroup
	log    *slog.Logger
	closed atomic.Bool
}

// Option 配置 Pool 的可选项。
type Option func(*Pool)

// WithConcurrency 设置最大并发 goroutine 数，默认为 CPU 核数。
// n <= 0 时使用 CPU 核数。
func WithConcurrency(n int) Option {
	return func(p *Pool) {
		if n <= 0 {
			n = runtime.NumCPU()
		}
		p.sem = make(chan struct{}, n)
	}
}

// WithLogger 设置日志实例，用于记录 panic 信息。
func WithLogger(log *slog.Logger) Option {
	return func(p *Pool) {
		if log != nil {
			p.log = log
		}
	}
}

// New 创建 Pool 实例。默认并发数为 CPU 核数。
func New(opts ...Option) *Pool {
	p := &Pool{
		sem: make(chan struct{}, runtime.NumCPU()),
		log: slog.Default(),
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// Go 提交一个异步任务。若已达并发上限，阻塞等待直到有空槽或 ctx 取消。
// Pool 已关闭时返回 context.Canceled。
// fn 内部的 panic 会被捕获并记录到日志，不会导致进程崩溃。
func (p *Pool) Go(ctx context.Context, fn func(ctx context.Context)) error {
	if p.closed.Load() {
		return context.Canceled
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	case p.sem <- struct{}{}:
	}

	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		defer func() { <-p.sem }()
		defer func() {
			if r := recover(); r != nil {
				p.log.Error("cworker: panic recovered",
					"panic", r,
					"stack", string(debug.Stack()),
				)
			}
		}()
		fn(ctx)
	}()
	return nil
}

// TryGo 提交一个异步任务，若当前并发已达上限则立即返回 false（非阻塞）。
// Pool 已关闭时同样返回 false。
func (p *Pool) TryGo(ctx context.Context, fn func(ctx context.Context)) bool {
	if p.closed.Load() {
		return false
	}

	select {
	case p.sem <- struct{}{}:
	default:
		return false
	}

	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		defer func() { <-p.sem }()
		defer func() {
			if r := recover(); r != nil {
				p.log.Error("cworker: panic recovered",
					"panic", r,
					"stack", string(debug.Stack()),
				)
			}
		}()
		fn(ctx)
	}()
	return true
}

// Stop 停止接受新任务并等待所有进行中的任务完成。
// ctx 取消时立即返回 ctx.Err()，但已在运行的 goroutine 不会被强制终止。
// Stop 之后再调用 Go / TryGo 均返回 context.Canceled / false。
func (p *Pool) Stop(ctx context.Context) error {
	p.closed.Store(true)

	done := make(chan struct{})
	go func() {
		p.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Concurrency 返回当前允许的最大并发数。
func (p *Pool) Concurrency() int {
	return cap(p.sem)
}
