package clocker

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	goredis "github.com/redis/go-redis/v9"
)

func newTestLocker(t *testing.T, opts ...Option) (*Locker, *miniredis.Miniredis) {
	t.Helper()
	s := miniredis.RunT(t)
	client := goredis.NewClient(&goredis.Options{Addr: s.Addr()})
	t.Cleanup(func() { client.Close() })
	return New(client, opts...), s
}

// --- TryLock ---

func TestTryLock_Success(t *testing.T) {
	locker, _ := newTestLocker(t)
	ctx := context.Background()

	lk, err := locker.TryLock(ctx, "resource", 5*time.Second)
	if err != nil {
		t.Fatalf("TryLock() unexpected error: %v", err)
	}
	defer lk.Unlock(ctx) //nolint

	if lk == nil {
		t.Fatal("TryLock() returned nil lock")
	}
}

func TestTryLock_ErrLockNotAcquired(t *testing.T) {
	locker, _ := newTestLocker(t)
	ctx := context.Background()

	lk1, err := locker.TryLock(ctx, "resource", 5*time.Second)
	if err != nil {
		t.Fatalf("first TryLock: %v", err)
	}
	defer lk1.Unlock(ctx) //nolint

	_, err = locker.TryLock(ctx, "resource", 5*time.Second)
	if !errors.Is(err, ErrLockNotAcquired) {
		t.Errorf("second TryLock = %v, want ErrLockNotAcquired", err)
	}
}

func TestTryLock_KeyPrefix(t *testing.T) {
	locker, mr := newTestLocker(t, WithKeyPrefix("myapp:lock:"))
	ctx := context.Background()

	lk, err := locker.TryLock(ctx, "job", 5*time.Second)
	if err != nil {
		t.Fatalf("TryLock: %v", err)
	}
	defer lk.Unlock(ctx) //nolint

	if !mr.Exists("myapp:lock:job") {
		t.Error("key should exist with custom prefix")
	}
}

// --- Unlock ---

func TestUnlock_ReleasesLock(t *testing.T) {
	locker, _ := newTestLocker(t)
	ctx := context.Background()

	lk, _ := locker.TryLock(ctx, "resource", 5*time.Second)
	if err := lk.Unlock(ctx); err != nil {
		t.Fatalf("Unlock: %v", err)
	}

	// 解锁后应能再次获取
	lk2, err := locker.TryLock(ctx, "resource", 5*time.Second)
	if err != nil {
		t.Fatalf("TryLock after Unlock: %v", err)
	}
	lk2.Unlock(ctx) //nolint
}

func TestUnlock_Idempotent(t *testing.T) {
	locker, _ := newTestLocker(t)
	ctx := context.Background()

	lk, _ := locker.TryLock(ctx, "resource", 5*time.Second)
	_ = lk.Unlock(ctx)

	// 重复解锁不应报错
	if err := lk.Unlock(ctx); err != nil {
		t.Errorf("second Unlock() = %v, want nil", err)
	}
}

func TestUnlock_CannotStealAnotherHoldersLock(t *testing.T) {
	locker, _ := newTestLocker(t)
	ctx := context.Background()

	// holder1 持有锁
	lk1, _ := locker.TryLock(ctx, "resource", 5*time.Second)
	// holder2 模拟：直接在同一 locker 实例创建一个指向相同 key 但 token 不同的 lock
	fakeLock := &lock{
		client: lk1.(*lock).client,
		key:    lk1.(*lock).key,
		token:  "fake-token-different-from-real",
		// stopWatchdog 为 nil
	}

	// 假锁解锁应静默成功（Lua 脚本保证不误删）
	if err := fakeLock.Unlock(ctx); err != nil {
		t.Fatalf("fakeLock.Unlock error: %v", err)
	}

	// 真正的持有者依然持有锁
	_, err := locker.TryLock(ctx, "resource", 5*time.Second)
	if !errors.Is(err, ErrLockNotAcquired) {
		t.Errorf("lock should still be held, got: %v", err)
	}

	lk1.Unlock(ctx) //nolint
}

// --- Lock（阻塞） ---

func TestLock_BlocksUntilAvailable(t *testing.T) {
	locker, _ := newTestLocker(t, WithRetryInterval(20*time.Millisecond, 100*time.Millisecond))
	ctx := context.Background()

	lk1, _ := locker.TryLock(ctx, "resource", 5*time.Second)

	// 后台解锁
	go func() {
		time.Sleep(100 * time.Millisecond)
		lk1.Unlock(ctx) //nolint
	}()

	lk2, err := locker.Lock(ctx, "resource", 5*time.Second)
	if err != nil {
		t.Fatalf("Lock() failed: %v", err)
	}
	lk2.Unlock(ctx) //nolint
}

func TestLock_CtxCancel(t *testing.T) {
	locker, _ := newTestLocker(t, WithRetryInterval(20*time.Millisecond, 100*time.Millisecond))
	ctx := context.Background()

	lk1, _ := locker.TryLock(ctx, "resource", 5*time.Second)
	defer lk1.Unlock(ctx) //nolint

	ctxCancel, cancel := context.WithTimeout(ctx, 80*time.Millisecond)
	defer cancel()

	_, err := locker.Lock(ctxCancel, "resource", 5*time.Second)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Lock() with timeout = %v, want DeadlineExceeded", err)
	}
}

// --- Refresh ---

func TestRefresh_ExtendsExpiry(t *testing.T) {
	locker, mr := newTestLocker(t)
	ctx := context.Background()

	lk, _ := locker.TryLock(ctx, "resource", 2*time.Second)
	defer lk.Unlock(ctx) //nolint

	// 续期为 10s
	if err := lk.Refresh(ctx, 10*time.Second); err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	ttl := mr.TTL("clocker:resource")
	if ttl < 8*time.Second {
		t.Errorf("TTL after refresh = %v, want > 8s", ttl)
	}
}

func TestRefresh_FailsForWrongToken(t *testing.T) {
	locker, _ := newTestLocker(t)
	ctx := context.Background()

	lk, _ := locker.TryLock(ctx, "resource", 5*time.Second)
	defer lk.Unlock(ctx) //nolint

	fakeLock := &lock{
		client: lk.(*lock).client,
		key:    lk.(*lock).key,
		token:  "wrong-token",
	}

	if err := fakeLock.Refresh(ctx, 10*time.Second); err == nil {
		t.Error("Refresh with wrong token should return error")
	}
}
