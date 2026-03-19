package ccache

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	goredis "github.com/redis/go-redis/v9"
)

func newLayered(t *testing.T, l1TTL time.Duration) (*LayeredCache, *MemoryCache, *RedisCache, *miniredis.Miniredis) {
	t.Helper()
	mem := newMemoryCache(t)

	s := miniredis.RunT(t)
	client := goredis.NewClient(&goredis.Options{Addr: s.Addr()})
	t.Cleanup(func() { client.Close() })
	rc := NewRedis(client)

	opts := []LayeredOption{}
	if l1TTL > 0 {
		opts = append(opts, WithL1TTL(l1TTL))
	}
	lc := NewLayered(mem, rc, opts...)
	return lc, mem, rc, s
}

// --- 读穿透 & L1 回填 ---

func TestLayered_L2HitBackfillsL1(t *testing.T) {
	lc, l1, l2, _ := newLayered(t, 0)
	ctx := context.Background()

	// 直接写 L2
	l2.Set(ctx, "k", "redis-value") //nolint

	// layered.Get 应从 L2 读到，且回填 L1
	val, found, err := lc.Get(ctx, "k")
	if err != nil || !found || val != "redis-value" {
		t.Fatalf("Get = (%q, %v, %v), want (redis-value, true, nil)", val, found, err)
	}

	// L1 应已回填
	l1Val, l1Found, _ := l1.Get(ctx, "k")
	if !l1Found || l1Val != "redis-value" {
		t.Error("L1 should be backfilled after L2 hit")
	}
}

func TestLayered_L1HitSkipsL2(t *testing.T) {
	lc, l1, _, _ := newLayered(t, 0)
	ctx := context.Background()

	// 只写 L1
	l1.Set(ctx, "k", "memory-value") //nolint

	val, found, err := lc.Get(ctx, "k")
	if err != nil || !found || val != "memory-value" {
		t.Fatalf("Get = (%q, %v, %v), want (memory-value, true, nil)", val, found, err)
	}
}

// --- Set 同步写两级 ---

func TestLayered_SetWritesBothLevels(t *testing.T) {
	lc, l1, l2, _ := newLayered(t, 0)
	ctx := context.Background()

	lc.Set(ctx, "k", "v") //nolint

	l1Val, l1Found, _ := l1.Get(ctx, "k")
	if !l1Found || l1Val != "v" {
		t.Error("L1 should have value after Set")
	}

	l2Val, l2Found, _ := l2.Get(ctx, "k")
	if !l2Found || l2Val != "v" {
		t.Error("L2 should have value after Set")
	}
}

func TestLayered_SetRespectL1TTL(t *testing.T) {
	// 配置 L1 TTL 很短，L2 TTL 较长
	lc, l1, _, _ := newLayered(t, 80*time.Millisecond)
	ctx := context.Background()

	lc.Set(ctx, "k", "v", TTL(10*time.Second)) //nolint
	time.Sleep(200 * time.Millisecond)

	// L1 应已过期
	_, l1Found, _ := l1.Get(ctx, "k")
	if l1Found {
		t.Error("L1 value should have expired with short l1TTL")
	}
}

// --- Del 清两级 ---

func TestLayered_DelRemovesBothLevels(t *testing.T) {
	lc, l1, l2, _ := newLayered(t, 0)
	ctx := context.Background()

	lc.Set(ctx, "a", "1") //nolint
	lc.Set(ctx, "b", "2") //nolint

	lc.Del(ctx, "a", "b") //nolint

	for _, k := range []string{"a", "b"} {
		_, l1Found, _ := l1.Get(ctx, k)
		_, l2Found, _ := l2.Get(ctx, k)
		if l1Found || l2Found {
			t.Errorf("key %q should be deleted from both levels (l1=%v, l2=%v)", k, l1Found, l2Found)
		}
	}
}

// --- GetOrSet ---

func TestLayered_GetOrSet_FnCalledOnce(t *testing.T) {
	lc, _, _, _ := newLayered(t, 0)
	ctx := context.Background()

	var calls atomic.Int32
	val, err := lc.GetOrSet(ctx, "k", func(ctx context.Context) (string, error) {
		calls.Add(1)
		return "loaded", nil
	})

	if err != nil || val != "loaded" {
		t.Fatalf("GetOrSet = (%q, %v)", val, err)
	}

	// 第二次命中 L1
	val2, _ := lc.GetOrSet(ctx, "k", func(ctx context.Context) (string, error) {
		calls.Add(1)
		return "nope", nil
	})
	if val2 != "loaded" || calls.Load() != 1 {
		t.Errorf("second call: val=%q, fn_calls=%d; want loaded, 1", val2, calls.Load())
	}
}

// --- 全未命中 ---

func TestLayered_Miss(t *testing.T) {
	lc, _, _, _ := newLayered(t, 0)
	_, found, err := lc.Get(context.Background(), "missing")
	if err != nil || found {
		t.Errorf("Get missing = (_, %v, %v), want (_, false, nil)", found, err)
	}
}
