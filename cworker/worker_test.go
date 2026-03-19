package cworker

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// --- 基本执行 ---

func TestGo_ExecutesTask(t *testing.T) {
	pool := New(WithConcurrency(4))
	done := make(chan struct{})

	_ = pool.Go(context.Background(), func(ctx context.Context) {
		close(done)
	})

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("task was not executed")
	}
	pool.Stop(context.Background()) //nolint
}

func TestGo_CtxCancelBeforeSlot(t *testing.T) {
	// 并发=1，先占满槽，再用已取消的 ctx 提交
	pool := New(WithConcurrency(1))

	blocker := make(chan struct{})
	_ = pool.Go(context.Background(), func(ctx context.Context) {
		<-blocker
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 已取消

	err := pool.Go(ctx, func(ctx context.Context) {})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Go() with cancelled ctx = %v, want context.Canceled", err)
	}

	close(blocker)
	pool.Stop(context.Background()) //nolint
}

// --- TryGo ---

func TestTryGo_ReturnsFalseWhenFull(t *testing.T) {
	pool := New(WithConcurrency(1))

	blocker := make(chan struct{})
	ok := pool.TryGo(context.Background(), func(ctx context.Context) {
		<-blocker
	})
	if !ok {
		t.Fatal("first TryGo should succeed")
	}

	// 此时并发槽已满，应立即返回 false
	ok2 := pool.TryGo(context.Background(), func(ctx context.Context) {})
	if ok2 {
		t.Error("TryGo should return false when pool is full")
	}

	close(blocker)
	pool.Stop(context.Background()) //nolint
}

// --- 并发限制 ---

func TestConcurrencyLimit(t *testing.T) {
	const limit = 5
	pool := New(WithConcurrency(limit))

	var current atomic.Int64
	var maxSeen atomic.Int64

	blocker := make(chan struct{})
	var wg sync.WaitGroup

	// 用单独 goroutine 提交，防止 pool.Go 阻塞主 goroutine
	for range limit * 3 {
		wg.Add(1)
		go func() {
			_ = pool.Go(context.Background(), func(ctx context.Context) {
				defer wg.Done()
				cur := current.Add(1)
				for {
					old := maxSeen.Load()
					if cur <= old || maxSeen.CompareAndSwap(old, cur) {
						break
					}
				}
				<-blocker
				current.Add(-1)
			})
		}()
	}

	// 等待 limit 个任务都已进入执行状态
	time.Sleep(100 * time.Millisecond)
	close(blocker)
	wg.Wait()
	pool.Stop(context.Background()) //nolint

	if maxSeen.Load() > limit {
		t.Errorf("max concurrency = %d, want <= %d", maxSeen.Load(), limit)
	}
}

// --- panic 恢复 ---

func TestGo_PanicRecovered(t *testing.T) {
	pool := New(WithConcurrency(2))
	done := make(chan struct{})

	// panic 不应崩溃进程
	_ = pool.Go(context.Background(), func(ctx context.Context) {
		defer close(done)
		panic("intentional panic for test")
	})

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("task did not complete after panic")
	}

	// pool 仍可正常使用
	afterPanic := make(chan struct{})
	_ = pool.Go(context.Background(), func(ctx context.Context) {
		close(afterPanic)
	})
	select {
	case <-afterPanic:
	case <-time.After(2 * time.Second):
		t.Fatal("pool should still work after panic recovery")
	}

	pool.Stop(context.Background()) //nolint
}

// --- 优雅关闭 ---

func TestStop_WaitsForInFlight(t *testing.T) {
	pool := New(WithConcurrency(4))

	var finished atomic.Bool
	started := make(chan struct{})

	_ = pool.Go(context.Background(), func(ctx context.Context) {
		close(started)
		time.Sleep(100 * time.Millisecond)
		finished.Store(true)
	})

	<-started // 确保任务已进入运行

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := pool.Stop(ctx); err != nil {
		t.Fatalf("Stop() error: %v", err)
	}

	if !finished.Load() {
		t.Error("Stop() returned before in-flight task finished")
	}
}

func TestStop_ReturnsCtxErrorOnTimeout(t *testing.T) {
	pool := New(WithConcurrency(2))

	blocker := make(chan struct{})
	_ = pool.Go(context.Background(), func(ctx context.Context) {
		<-blocker // 永不结束
	})

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := pool.Stop(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Stop() = %v, want DeadlineExceeded", err)
	}
	close(blocker) // 解锁，避免 goroutine 泄漏
}

// --- 关闭后拒绝新任务 ---

func TestGo_RejectedAfterStop(t *testing.T) {
	pool := New(WithConcurrency(4))
	pool.Stop(context.Background()) //nolint

	err := pool.Go(context.Background(), func(ctx context.Context) {})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Go() after Stop() = %v, want context.Canceled", err)
	}
}

func TestTryGo_ReturnsFalseAfterStop(t *testing.T) {
	pool := New(WithConcurrency(4))
	pool.Stop(context.Background()) //nolint

	if pool.TryGo(context.Background(), func(ctx context.Context) {}) {
		t.Error("TryGo() after Stop() should return false")
	}
}

// --- Concurrency() ---

func TestConcurrency(t *testing.T) {
	pool := New(WithConcurrency(7))
	if pool.Concurrency() != 7 {
		t.Errorf("Concurrency() = %d, want 7", pool.Concurrency())
	}
}
