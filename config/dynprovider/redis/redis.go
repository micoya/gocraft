// Package redis 提供基于 Redis 的动态配置 Provider。
//
// 配置以 JSON 格式存储在指定 key 中，Provider 通过定时轮询检测变更。
// 变更检测通过对比值内容实现，内容不变时不触发热更新。
//
// 使用示例：
//
//	rdb := redis.NewClient(...)
//	p := dynredis.New(rdb, "myapp:dynconfig")
//	mgr, _ := config.NewManager(ctx, cfg, p)
package redis

import (
	"bytes"
	"context"
	"fmt"
	"sync"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"github.com/micoya/gocraft/config"
)

func init() {
	config.RegisterProviderBuilder("redis", func(pcfg config.DynProviderConfig, client any) (config.DynamicProvider, error) {
		rdb, ok := client.(*goredis.Client)
		if !ok {
			return nil, fmt.Errorf("config/dynprovider/redis: expected *redis.Client, got %T", client)
		}
		key := pcfg.Key
		if key == "" {
			key = "dynconfig"
		}
		var opts []Option
		if pcfg.PollInterval > 0 {
			opts = append(opts, WithPollInterval(pcfg.PollInterval))
		}
		return New(rdb, key, opts...), nil
	})
}

const defaultPollInterval = 30 * time.Second

// Option 配置 Provider 的可选项。
type Option func(*Provider)

// WithPollInterval 设置轮询间隔，默认 30s。
func WithPollInterval(d time.Duration) Option {
	return func(p *Provider) { p.pollInterval = d }
}

// Provider 通过轮询 Redis key 提供动态配置。
// key 对应的值应为合法 JSON 对象（app 块的部分或全量字段）。
type Provider struct {
	client       *goredis.Client
	key          string
	pollInterval time.Duration

	mu      sync.Mutex
	lastVal []byte // Load 返回的基准值，Watch 从此处开始检测变更
}

// New 创建 Redis 动态配置 Provider。
// client 应为已初始化的 Redis 客户端（通常来自 cdao）。
// key 为存储配置 JSON 的 Redis key。
func New(client *goredis.Client, key string, opts ...Option) *Provider {
	p := &Provider{
		client:       client,
		key:          key,
		pollInterval: defaultPollInterval,
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// Name 实现 config.DynamicProvider。
func (p *Provider) Name() string { return "redis:" + p.key }

// Load 实现 config.DynamicProvider，读取 key 的当前值作为初始补丁，
// 并将其记录为 Watch 的基准值，避免 Watch 首次轮询重复推送相同内容。
// key 不存在时返回 nil（无初始覆盖）。
func (p *Provider) Load(ctx context.Context) ([]byte, error) {
	val, err := p.client.Get(ctx, p.key).Bytes()
	if err == goredis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	p.mu.Lock()
	p.lastVal = val
	p.mu.Unlock()
	return val, nil
}

// Watch 实现 config.DynamicProvider，轮询检测变更并推送补丁。
// Watch 以 Load 返回的值为基准，只推送值发生变化的轮询结果。
// ctx 取消后退出，不再写入 patches。
func (p *Provider) Watch(ctx context.Context, patches chan<- []byte) error {
	ticker := time.NewTicker(p.pollInterval)
	defer ticker.Stop()

	p.mu.Lock()
	lastVal := p.lastVal
	p.mu.Unlock()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			val, err := p.client.Get(ctx, p.key).Bytes()
			if err != nil {
				continue
			}
			if bytes.Equal(val, lastVal) {
				continue
			}
			lastVal = val
			select {
			case patches <- val:
			case <-ctx.Done():
				return nil
			}
		}
	}
}

// Close 实现 config.DynamicProvider，Redis 客户端由外部管理，此处无需释放。
func (p *Provider) Close() error { return nil }
