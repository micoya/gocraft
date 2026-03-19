package ccache

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func newMemoryCache(t *testing.T, opts ...MemoryOption) *MemoryCache {
	t.Helper()
	c, err := NewMemoryFromConfig(1_000, 64<<20, opts...)
	if err != nil {
		t.Fatalf("NewMemoryFromConfig: %v", err)
	}
	t.Cleanup(func() { c.cache.Clear() })
	return c
}

func TestMemoryCache_SetGet(t *testing.T) {
	c := newMemoryCache(t)
	ctx := context.Background()

	if err := c.Set(ctx, "k", "hello"); err != nil {
		t.Fatalf("Set: %v", err)
	}

	val, found, err := c.Get(ctx, "k")
	if err != nil || !found || val != "hello" {
		t.Errorf("Get = (%q, %v, %v), want (hello, true, nil)", val, found, err)
	}
}

func TestMemoryCache_Miss(t *testing.T) {
	c := newMemoryCache(t)
	_, found, err := c.Get(context.Background(), "missing")
	if err != nil || found {
		t.Errorf("Get missing = (_, %v, %v), want (_, false, nil)", found, err)
	}
}

func TestMemoryCache_Del(t *testing.T) {
	c := newMemoryCache(t)
	ctx := context.Background()
	c.Set(ctx, "a", "1") //nolint
	c.Set(ctx, "b", "2") //nolint

	c.Del(ctx, "a", "b") //nolint

	for _, k := range []string{"a", "b"} {
		_, found, _ := c.Get(ctx, k)
		if found {
			t.Errorf("key %q should be deleted", k)
		}
	}
}

func TestMemoryCache_TTLExpiry(t *testing.T) {
	c := newMemoryCache(t)
	ctx := context.Background()

	c.Set(ctx, "expiring", "v", TTL(50*time.Millisecond)) //nolint
	time.Sleep(150 * time.Millisecond)

	_, found, _ := c.Get(ctx, "expiring")
	if found {
		t.Error("key should have expired")
	}
}

func TestMemoryCache_GetOrSet_Miss_CallsFn(t *testing.T) {
	c := newMemoryCache(t)
	ctx := context.Background()

	var calls atomic.Int32
	val, err := c.GetOrSet(ctx, "k", func(ctx context.Context) (string, error) {
		calls.Add(1)
		return "computed", nil
	})

	if err != nil || val != "computed" {
		t.Fatalf("GetOrSet = (%q, %v)", val, err)
	}
	if calls.Load() != 1 {
		t.Errorf("fn calls = %d, want 1", calls.Load())
	}

	// 第二次命中，fn 不应再被调用
	_, _ = c.GetOrSet(ctx, "k", func(ctx context.Context) (string, error) {
		calls.Add(1)
		return "nope", nil
	})
	if calls.Load() != 1 {
		t.Errorf("fn should not be called again, calls = %d", calls.Load())
	}
}

func TestMemoryCache_GetOrSet_FnError(t *testing.T) {
	c := newMemoryCache(t)
	ctx := context.Background()

	wantErr := errors.New("load failed")
	_, err := c.GetOrSet(ctx, "k", func(ctx context.Context) (string, error) {
		return "", wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Errorf("GetOrSet fn error = %v, want %v", err, wantErr)
	}
}
