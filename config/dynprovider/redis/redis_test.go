package redis_test

import (
	"context"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	goredis "github.com/redis/go-redis/v9"

	dynredis "github.com/micoya/gocraft/config/dynprovider/redis"
)

const testKey = "test:dynconfig"

func newTestClient(t *testing.T) (*goredis.Client, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	client := goredis.NewClient(&goredis.Options{Addr: mr.Addr()})
	return client, mr
}

func TestProvider_Load_Empty(t *testing.T) {
	client, _ := newTestClient(t)
	p := dynredis.New(client, testKey)

	data, err := p.Load(context.Background())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if data != nil {
		t.Errorf("Load on empty key: want nil, got %s", data)
	}
}

func TestProvider_Load_Value(t *testing.T) {
	client, mr := newTestClient(t)
	mr.Set(testKey, `{"a":1}`)

	p := dynredis.New(client, testKey)
	data, err := p.Load(context.Background())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if string(data) != `{"a":1}` {
		t.Errorf("Load: want {\"a\":1}, got %s", data)
	}
}

func TestProvider_Watch_DetectsChange(t *testing.T) {
	client, mr := newTestClient(t)
	mr.Set(testKey, `{"a":1}`)

	p := dynredis.New(client, testKey, dynredis.WithPollInterval(50*time.Millisecond))

	// 先 Load 建立基准，Watch 才能以此为起点检测变更
	if _, err := p.Load(context.Background()); err != nil {
		t.Fatalf("Load: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	patches := make(chan []byte, 4)
	go func() {
		_ = p.Watch(ctx, patches)
	}()

	// 等一个轮询周期后更新 key
	time.Sleep(80 * time.Millisecond)
	mr.Set(testKey, `{"a":99}`)

	select {
	case data := <-patches:
		if string(data) != `{"a":99}` {
			t.Errorf("Watch: want {\"a\":99}, got %s", data)
		}
	case <-time.After(500 * time.Millisecond):
		t.Error("Watch: no patch received within timeout")
	}
}

func TestProvider_Watch_SkipsDuplicate(t *testing.T) {
	client, mr := newTestClient(t)
	mr.Set(testKey, `{"a":1}`)

	p := dynredis.New(client, testKey, dynredis.WithPollInterval(40*time.Millisecond))

	// 先 Load 建立基准，之后值不变时 Watch 不应推送
	if _, err := p.Load(context.Background()); err != nil {
		t.Fatalf("Load: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	patches := make(chan []byte, 4)
	go func() {
		_ = p.Watch(ctx, patches)
	}()

	<-ctx.Done()

	if len(patches) > 0 {
		t.Errorf("Watch should not emit if value unchanged, got %d patches", len(patches))
	}
}

func TestProvider_Name(t *testing.T) {
	client, _ := newTestClient(t)
	p := dynredis.New(client, testKey)
	if p.Name() != "redis:"+testKey {
		t.Errorf("Name: want redis:%s, got %s", testKey, p.Name())
	}
}

func TestProvider_Close(t *testing.T) {
	client, _ := newTestClient(t)
	p := dynredis.New(client, testKey)
	if err := p.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}
