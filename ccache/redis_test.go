package ccache

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	goredis "github.com/redis/go-redis/v9"
)

func newRedisCache(t *testing.T, opts ...RedisOption) (*RedisCache, *miniredis.Miniredis) {
	t.Helper()
	s := miniredis.RunT(t)
	client := goredis.NewClient(&goredis.Options{Addr: s.Addr()})
	t.Cleanup(func() { client.Close() })
	return NewRedis(client, opts...), s
}

// --- 基础 Get/Set/Del ---

func TestRedisCache_Miss(t *testing.T) {
	c, _ := newRedisCache(t)
	_, found, err := c.Get(context.Background(), "missing")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if found {
		t.Error("Get on missing key should return found=false")
	}
}

func TestRedisCache_SetGet(t *testing.T) {
	c, _ := newRedisCache(t)
	ctx := context.Background()

	if err := c.Set(ctx, "k", "hello"); err != nil {
		t.Fatalf("Set: %v", err)
	}

	val, found, err := c.Get(ctx, "k")
	if err != nil || !found || val != "hello" {
		t.Errorf("Get = (%q, %v, %v), want (hello, true, nil)", val, found, err)
	}
}

func TestRedisCache_Del(t *testing.T) {
	c, _ := newRedisCache(t)
	ctx := context.Background()
	c.Set(ctx, "a", "1")  //nolint
	c.Set(ctx, "b", "2")  //nolint

	if err := c.Del(ctx, "a", "b"); err != nil {
		t.Fatalf("Del: %v", err)
	}

	for _, k := range []string{"a", "b"} {
		_, found, _ := c.Get(ctx, k)
		if found {
			t.Errorf("key %q should have been deleted", k)
		}
	}
}

// --- TTL ---

func TestRedisCache_TTLApplied(t *testing.T) {
	c, mr := newRedisCache(t)
	ctx := context.Background()

	c.Set(ctx, "ttl-key", "v", TTL(5*time.Second)) //nolint

	ttl := mr.TTL("ttl-key")
	if ttl <= 0 || ttl > 5*time.Second {
		t.Errorf("TTL = %v, want (0, 5s]", ttl)
	}
}

// --- KeyPrefix ---

func TestRedisCache_KeyPrefix(t *testing.T) {
	c, mr := newRedisCache(t, WithRedisKeyPrefix("app:"))
	ctx := context.Background()

	c.Set(ctx, "user", "alice") //nolint

	if !mr.Exists("app:user") {
		t.Error("key should be stored with prefix 'app:user'")
	}
	// 不带前缀的原始 key 不应存在
	if mr.Exists("user") {
		t.Error("key should NOT exist without prefix")
	}
}

// --- GetOrSet ---

func TestRedisCache_GetOrSet_Miss_CallsFn(t *testing.T) {
	c, _ := newRedisCache(t)
	ctx := context.Background()

	var calls atomic.Int32
	val, err := c.GetOrSet(ctx, "k", func(ctx context.Context) (string, error) {
		calls.Add(1)
		return "computed", nil
	})

	if err != nil {
		t.Fatalf("GetOrSet: %v", err)
	}
	if val != "computed" {
		t.Errorf("val = %q, want computed", val)
	}
	if calls.Load() != 1 {
		t.Errorf("fn calls = %d, want 1", calls.Load())
	}

	// 二次调用不再触发 fn
	val2, _ := c.GetOrSet(ctx, "k", func(ctx context.Context) (string, error) {
		calls.Add(1)
		return "should-not-be-called", nil
	})
	if val2 != "computed" {
		t.Errorf("second GetOrSet = %q, want computed", val2)
	}
	if calls.Load() != 1 {
		t.Errorf("fn should not be called again, calls = %d", calls.Load())
	}
}

func TestRedisCache_GetOrSet_FnError(t *testing.T) {
	c, _ := newRedisCache(t)
	ctx := context.Background()

	wantErr := errors.New("db error")
	_, err := c.GetOrSet(ctx, "k", func(ctx context.Context) (string, error) {
		return "", wantErr
	})

	if !errors.Is(err, wantErr) {
		t.Errorf("GetOrSet fn error = %v, want %v", err, wantErr)
	}

	// key 不应被写入
	_, found, _ := c.Get(ctx, "k")
	if found {
		t.Error("key should not be cached when fn returns error")
	}
}
