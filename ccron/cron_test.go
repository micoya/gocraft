package ccron

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// --- mock Locker 用于测试分布式锁逻辑 ---

type mockLock struct {
	locker *mockLocker
}

func (l *mockLock) Unlock(_ context.Context) error {
	l.locker.release()
	return nil
}

// mockLocker 可控制是否允许 TryLock 成功
type mockLocker struct {
	mu      sync.Mutex
	held    bool // 模拟锁已被持有
	lastKey string
}

var errLockNotAcquired = errors.New("lock not acquired")

func (l *mockLocker) TryLock(_ context.Context, key string, _ time.Duration) (Lock, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lastKey = key
	if l.held {
		return nil, errLockNotAcquired
	}
	l.held = true
	return &mockLock{locker: l}, nil
}

func (l *mockLocker) release() {
	l.mu.Lock()
	l.held = false
	l.mu.Unlock()
}

// --- Add / Start / Stop ---

func TestAdd_InvalidSpec(t *testing.T) {
	s := New()
	_, err := s.Add("bad-job", "not-a-cron-spec", func(_ context.Context) {})
	if err == nil {
		t.Error("Add() with invalid spec should return error")
	}
}

func TestAdd_ValidSpec(t *testing.T) {
	s := New()
	id, err := s.Add("test-job", "* * * * * *", func(_ context.Context) {})
	if err != nil {
		t.Fatalf("Add() unexpected error: %v", err)
	}
	if id == 0 {
		t.Error("Add() should return non-zero EntryID")
	}
}

// --- 任务执行 ---

func TestJob_Executes(t *testing.T) {
	s := New()
	done := make(chan struct{})

	_, err := s.Add("exec-test", "* * * * * *", func(_ context.Context) {
		select {
		case done <- struct{}{}:
		default:
		}
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	s.Start()
	defer s.Stop(context.Background())

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("job did not execute within 3 seconds")
	}
}

// --- panic 恢复 ---

func TestJob_PanicRecovered(t *testing.T) {
	s := New()
	afterPanic := make(chan struct{}, 1)

	var count atomic.Int32
	_, err := s.Add("panic-job", "* * * * * *", func(_ context.Context) {
		if count.Add(1) == 1 {
			panic("intentional panic")
		}
		afterPanic <- struct{}{}
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	s.Start()
	defer s.Stop(context.Background())

	// 等待第二次执行（证明 panic 后调度器仍在运行）
	select {
	case <-afterPanic:
	case <-time.After(5 * time.Second):
		t.Fatal("scheduler should continue running after job panic")
	}
}

// --- 分布式锁：锁已被占用时跳过 ---

func TestJob_DistributedSkipWhenLocked(t *testing.T) {
	locker := &mockLocker{held: true} // 锁已被其他实例持有
	s := New(WithLocker(locker, time.Minute))

	var execCount atomic.Int32
	_, err := s.Add("locked-job", "* * * * * *", func(_ context.Context) {
		execCount.Add(1)
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	s.Start()

	time.Sleep(2500 * time.Millisecond)
	s.Stop(context.Background())

	if execCount.Load() > 0 {
		t.Errorf("job should have been skipped, but executed %d times", execCount.Load())
	}
}

// --- 分布式锁：抢到锁后执行，并释放锁 ---

func TestJob_DistributedRunsAndReleasesLock(t *testing.T) {
	locker := &mockLocker{held: false}
	s := New(WithLocker(locker, time.Minute))

	done := make(chan struct{}, 1)
	_, err := s.Add("unlocked-job", "* * * * * *", func(_ context.Context) {
		// 执行结束后 wrap 会调用 lk.Unlock，释放 locker.held
		done <- struct{}{}
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	s.Start()
	defer s.Stop(context.Background())

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("job did not execute")
	}

	// 执行完毕后锁应被释放（held = false），才能让下一次执行也能获取锁
	time.Sleep(100 * time.Millisecond)
	locker.mu.Lock()
	held := locker.held
	locker.mu.Unlock()
	if held {
		t.Error("lock should be released after job finishes")
	}
}

// --- 分布式锁 key 格式验证 ---

func TestJob_DistributedLockKeyFormat(t *testing.T) {
	locker := &mockLocker{held: false}
	s := New(WithLocker(locker, time.Minute))

	_, err := s.Add("report-generator", "* * * * * *", func(_ context.Context) {})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	s.Start()
	time.Sleep(1500 * time.Millisecond)
	s.Stop(context.Background())

	locker.mu.Lock()
	key := locker.lastKey
	locker.mu.Unlock()

	want := "ccron:report-generator"
	if key != want {
		t.Errorf("TryLock key = %q, want %q", key, want)
	}
}

// --- Remove ---

func TestRemove_StopsJobExecution(t *testing.T) {
	s := New()
	var count atomic.Int32

	id, _ := s.Add("removable-job", "* * * * * *", func(_ context.Context) {
		count.Add(1)
	})
	s.Start()
	time.Sleep(1500 * time.Millisecond)
	s.Remove(id)

	// 记录 remove 后的计数
	countAfterRemove := count.Load()
	time.Sleep(1500 * time.Millisecond)

	if count.Load() > countAfterRemove {
		t.Error("job should not execute after Remove()")
	}
	s.Stop(context.Background())
}
