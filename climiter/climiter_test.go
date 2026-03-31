package climiter

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	goredis "github.com/redis/go-redis/v9"

	"github.com/micoya/gocraft/config"
)

func newTestClient(t *testing.T) (*goredis.Client, *miniredis.Miniredis) {
	t.Helper()
	s := miniredis.RunT(t)
	client := goredis.NewClient(&goredis.Options{Addr: s.Addr()})
	t.Cleanup(func() { client.Close() })
	return client, s
}

// ─────────────────────────────────────────────────────────────────────────────
// SlidingWindow Redis
// ─────────────────────────────────────────────────────────────────────────────

func TestSlidingWindowRedis_AllowsUpToRate(t *testing.T) {
	client, _ := newTestClient(t)
	ctx := context.Background()
	limiter := NewSlidingWindowRedis(client, 3, time.Minute)

	for i := range 3 {
		if _, err := limiter.Limit(ctx, "key"); err != nil {
			t.Fatalf("request %d: unexpected error %v", i+1, err)
		}
	}
}

func TestSlidingWindowRedis_BlocksOverRate(t *testing.T) {
	client, _ := newTestClient(t)
	ctx := context.Background()
	limiter := NewSlidingWindowRedis(client, 3, time.Minute)

	for range 3 {
		limiter.Limit(ctx, "key") //nolint
	}

	_, err := limiter.Limit(ctx, "key")
	if !errors.Is(err, ErrRateLimited) {
		t.Errorf("4th request = %v, want ErrRateLimited", err)
	}
}

func TestSlidingWindowRedis_RetryAfterIsPositive(t *testing.T) {
	client, _ := newTestClient(t)
	ctx := context.Background()
	limiter := NewSlidingWindowRedis(client, 1, time.Minute)

	limiter.Limit(ctx, "key") //nolint
	retryAfter, err := limiter.Limit(ctx, "key")
	if !errors.Is(err, ErrRateLimited) {
		t.Fatalf("want ErrRateLimited, got %v", err)
	}
	if retryAfter <= 0 {
		t.Errorf("retryAfter = %v, want > 0", retryAfter)
	}
}

func TestSlidingWindowRedis_DifferentKeysAreIndependent(t *testing.T) {
	client, _ := newTestClient(t)
	ctx := context.Background()
	limiter := NewSlidingWindowRedis(client, 1, time.Minute)

	limiter.Limit(ctx, "userA") //nolint

	if _, err := limiter.Limit(ctx, "userB"); err != nil {
		t.Errorf("different key should not be rate limited, got %v", err)
	}
}

func TestSlidingWindowRedis_KeyPrefix(t *testing.T) {
	client, mr := newTestClient(t)
	ctx := context.Background()
	limiter := NewSlidingWindowRedis(client, 5, time.Minute, WithKeyPrefix("myapp:"))

	limiter.Limit(ctx, "user1") //nolint

	// limiters 内部用 {prefix}timestamp 格式存 key，检查前缀是否生效
	keys := mr.Keys()
	found := false
	for _, k := range keys {
		// key 形如 "{myapp:user1}1774927140000000000"
		if len(k) > 7 && k[1:8] == "myapp:u" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected a Redis key containing 'myapp:u', keys = %v", keys)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// FixedWindow Redis
// ─────────────────────────────────────────────────────────────────────────────

func TestFixedWindowRedis_AllowsUpToRate(t *testing.T) {
	client, _ := newTestClient(t)
	ctx := context.Background()
	limiter := NewFixedWindowRedis(client, 3, time.Minute)

	for i := range 3 {
		if _, err := limiter.Limit(ctx, "key"); err != nil {
			t.Fatalf("request %d: unexpected error %v", i+1, err)
		}
	}
}

func TestFixedWindowRedis_BlocksOverRate(t *testing.T) {
	client, _ := newTestClient(t)
	ctx := context.Background()
	limiter := NewFixedWindowRedis(client, 3, time.Minute)

	for range 3 {
		limiter.Limit(ctx, "key") //nolint
	}

	_, err := limiter.Limit(ctx, "key")
	if !errors.Is(err, ErrRateLimited) {
		t.Errorf("4th request = %v, want ErrRateLimited", err)
	}
}

func TestFixedWindowRedis_DifferentKeysAreIndependent(t *testing.T) {
	client, _ := newTestClient(t)
	ctx := context.Background()
	limiter := NewFixedWindowRedis(client, 1, time.Minute)

	limiter.Limit(ctx, "keyA") //nolint

	if _, err := limiter.Limit(ctx, "keyB"); err != nil {
		t.Errorf("different key should not be rate limited, got %v", err)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// TokenBucket Redis
// ─────────────────────────────────────────────────────────────────────────────

func TestTokenBucketRedis_AllowsUpToBurst(t *testing.T) {
	client, _ := newTestClient(t)
	ctx := context.Background()
	// burst=3，每分钟补充 1 个令牌（rate=1, window=1min）
	limiter := NewTokenBucketRedis(client, 1, time.Minute, WithBurst(3))

	for i := range 3 {
		if _, err := limiter.Limit(ctx, "key"); err != nil {
			t.Fatalf("request %d: unexpected error %v", i+1, err)
		}
	}
}

func TestTokenBucketRedis_BlocksWhenBucketEmpty(t *testing.T) {
	client, _ := newTestClient(t)
	ctx := context.Background()
	limiter := NewTokenBucketRedis(client, 1, time.Minute, WithBurst(2))

	for range 2 {
		limiter.Limit(ctx, "key") //nolint
	}

	_, err := limiter.Limit(ctx, "key")
	if !errors.Is(err, ErrRateLimited) {
		t.Errorf("3rd request = %v, want ErrRateLimited", err)
	}
}

func TestTokenBucketRedis_DifferentKeysAreIndependent(t *testing.T) {
	client, _ := newTestClient(t)
	ctx := context.Background()
	limiter := NewTokenBucketRedis(client, 1, time.Minute)

	limiter.Limit(ctx, "keyA") //nolint

	if _, err := limiter.Limit(ctx, "keyB"); err != nil {
		t.Errorf("different key should not be rate limited, got %v", err)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// LocalSlidingWindow
// ─────────────────────────────────────────────────────────────────────────────

func TestLocalSlidingWindow_AllowsUpToRate(t *testing.T) {
	ctx := context.Background()
	limiter := NewLocalSlidingWindow(3, time.Minute)

	for i := range 3 {
		if _, err := limiter.Limit(ctx, "key"); err != nil {
			t.Fatalf("request %d: unexpected error %v", i+1, err)
		}
	}
}

func TestLocalSlidingWindow_BlocksOverRate(t *testing.T) {
	ctx := context.Background()
	limiter := NewLocalSlidingWindow(3, time.Minute)

	for range 3 {
		limiter.Limit(ctx, "key") //nolint
	}

	_, err := limiter.Limit(ctx, "key")
	if !errors.Is(err, ErrRateLimited) {
		t.Errorf("4th request = %v, want ErrRateLimited", err)
	}
}

func TestLocalSlidingWindow_DifferentKeysAreIndependent(t *testing.T) {
	ctx := context.Background()
	limiter := NewLocalSlidingWindow(1, time.Minute)

	limiter.Limit(ctx, "keyA") //nolint

	if _, err := limiter.Limit(ctx, "keyB"); err != nil {
		t.Errorf("different key should not be rate limited, got %v", err)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Registry / NewFromConfig
// ─────────────────────────────────────────────────────────────────────────────

func TestNewFromConfig_SlidingWindow(t *testing.T) {
	client, _ := newTestClient(t)
	ctx := context.Background()

	cfg := map[string]config.LimiterItemConfig{
		"default": {Algo: "sliding_window", Rate: 3, Window: time.Minute},
	}
	reg, err := NewFromConfig(cfg, func(_ string) (*goredis.Client, error) { return client, nil })
	if err != nil {
		t.Fatalf("NewFromConfig: %v", err)
	}

	lim := reg.Must("default")
	for range 3 {
		lim.Limit(ctx, "key") //nolint
	}
	_, err = lim.Limit(ctx, "key")
	if !errors.Is(err, ErrRateLimited) {
		t.Errorf("4th request = %v, want ErrRateLimited", err)
	}
}

func TestNewFromConfig_Local(t *testing.T) {
	ctx := context.Background()

	cfg := map[string]config.LimiterItemConfig{
		"mem": {Algo: "local", Rate: 2, Window: time.Minute},
	}
	reg, err := NewFromConfig(cfg, nil)
	if err != nil {
		t.Fatalf("NewFromConfig: %v", err)
	}

	lim := reg.Must("mem")
	for range 2 {
		lim.Limit(ctx, "key") //nolint
	}
	_, err = lim.Limit(ctx, "key")
	if !errors.Is(err, ErrRateLimited) {
		t.Errorf("3rd request = %v, want ErrRateLimited", err)
	}
}

func TestNewFromConfig_TokenBucketWithBurst(t *testing.T) {
	client, _ := newTestClient(t)
	ctx := context.Background()

	cfg := map[string]config.LimiterItemConfig{
		"burst3": {Algo: "token_bucket", Rate: 1, Window: time.Minute, Burst: 3},
	}
	reg, err := NewFromConfig(cfg, func(_ string) (*goredis.Client, error) { return client, nil })
	if err != nil {
		t.Fatalf("NewFromConfig: %v", err)
	}

	lim := reg.Must("burst3")
	for range 3 {
		lim.Limit(ctx, "key") //nolint
	}
	_, err = lim.Limit(ctx, "key")
	if !errors.Is(err, ErrRateLimited) {
		t.Errorf("4th request = %v, want ErrRateLimited", err)
	}
}

func TestNewFromConfig_MultipleInstances(t *testing.T) {
	client, _ := newTestClient(t)
	ctx := context.Background()

	cfg := map[string]config.LimiterItemConfig{
		"lax":    {Algo: "sliding_window", Rate: 10, Window: time.Minute},
		"strict": {Algo: "sliding_window", Rate: 1, Window: time.Minute},
	}
	getter := func(_ string) (*goredis.Client, error) { return client, nil }
	reg, err := NewFromConfig(cfg, getter)
	if err != nil {
		t.Fatalf("NewFromConfig: %v", err)
	}

	// lax 第一次应通过
	if _, err := reg.Must("lax").Limit(ctx, "k"); err != nil {
		t.Errorf("lax first request: %v", err)
	}
	// strict 第一次通过，第二次被限流
	reg.Must("strict").Limit(ctx, "k") //nolint
	_, err = reg.Must("strict").Limit(ctx, "k")
	if !errors.Is(err, ErrRateLimited) {
		t.Errorf("strict 2nd request = %v, want ErrRateLimited", err)
	}
}

func TestNewFromConfig_Get_NotFound(t *testing.T) {
	reg, _ := NewFromConfig(map[string]config.LimiterItemConfig{}, nil)
	_, ok := reg.Get("nonexistent")
	if ok {
		t.Error("Get nonexistent should return false")
	}
}

func TestNewFromConfig_Must_Panics(t *testing.T) {
	reg, _ := NewFromConfig(map[string]config.LimiterItemConfig{}, nil)
	defer func() {
		if r := recover(); r == nil {
			t.Error("Must nonexistent should panic")
		}
	}()
	reg.Must("nonexistent")
}

func TestNewFromConfig_InvalidRate(t *testing.T) {
	cfg := map[string]config.LimiterItemConfig{
		"bad": {Algo: "sliding_window", Rate: 0, Window: time.Minute},
	}
	_, err := NewFromConfig(cfg, nil)
	if err == nil {
		t.Error("should return error for rate=0")
	}
}

func TestNewFromConfig_UnknownAlgo(t *testing.T) {
	client, _ := newTestClient(t)
	cfg := map[string]config.LimiterItemConfig{
		"x": {Algo: "unknown", Rate: 1, Window: time.Second},
	}
	_, err := NewFromConfig(cfg, func(_ string) (*goredis.Client, error) { return client, nil })
	if err == nil {
		t.Error("should return error for unknown algo")
	}
}

func TestNewFromConfig_DefaultAlgoIsSlidingWindow(t *testing.T) {
	client, _ := newTestClient(t)
	ctx := context.Background()

	// Algo 为空时默认 sliding_window
	cfg := map[string]config.LimiterItemConfig{
		"implicit": {Rate: 2, Window: time.Minute},
	}
	reg, err := NewFromConfig(cfg, func(_ string) (*goredis.Client, error) { return client, nil })
	if err != nil {
		t.Fatalf("NewFromConfig: %v", err)
	}

	for range 2 {
		reg.Must("implicit").Limit(ctx, "k") //nolint
	}
	_, err = reg.Must("implicit").Limit(ctx, "k")
	if !errors.Is(err, ErrRateLimited) {
		t.Errorf("3rd request = %v, want ErrRateLimited", err)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// 通用行为：WithBurst 对非令牌桶工厂不生效但不报错
// ─────────────────────────────────────────────────────────────────────────────

func TestSlidingWindowRedis_WithBurstIgnored(t *testing.T) {
	client, _ := newTestClient(t)
	ctx := context.Background()
	// WithBurst 对 SlidingWindow 无语义意义，但不应 panic 或报错
	limiter := NewSlidingWindowRedis(client, 2, time.Minute, WithBurst(99))

	for range 2 {
		limiter.Limit(ctx, "key") //nolint
	}
	_, err := limiter.Limit(ctx, "key")
	if !errors.Is(err, ErrRateLimited) {
		t.Errorf("3rd request = %v, want ErrRateLimited", err)
	}
}
