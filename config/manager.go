package config

import (
	"context"
	"fmt"
	"sync"

	"github.com/spf13/viper"
)

// ProviderBuilder 根据 DynProviderConfig 和原始 client 实例构造 DynamicProvider。
// 通常在 dynprovider 子包的 init() 中通过 RegisterProviderBuilder 注册。
type ProviderBuilder func(pcfg DynProviderConfig, client any) (DynamicProvider, error)

var (
	builderMu sync.RWMutex
	builders  = map[string]ProviderBuilder{}
)

// RegisterProviderBuilder 为指定 type 注册 ProviderBuilder。
// 重复注册同一 type 会 panic。通常在 dynprovider 子包的 init() 中调用。
func RegisterProviderBuilder(typ string, b ProviderBuilder) {
	builderMu.Lock()
	defer builderMu.Unlock()
	if _, dup := builders[typ]; dup {
		panic("config: RegisterProviderBuilder called twice for type " + typ)
	}
	builders[typ] = b
}

// ClientGetter 从 DAO 获取指定类型和名称的底层客户端实例。
// cdao.DAO 已实现此接口，可直接传入。
type ClientGetter interface {
	Get(kind, name string) (any, error)
}

// NewManagerFromDAO 根据 cfg.Dynamic.Provider 的声明，自动从 dao 获取客户端实例
// 并构建 DynamicProvider，然后创建 Manager。
//
// 需要提前 blank import 对应的 dynprovider 子包以注册 ProviderBuilder：
//
//	import _ "github.com/micoya/gocraft/config/dynprovider/redis"
//	import _ "github.com/micoya/gocraft/config/dynprovider/db"
//
// cfg.Dynamic 为 nil 或 Provider 为 nil 时，等价于直接调用 NewManager(ctx, cfg)。
func NewManagerFromDAO[T any](ctx context.Context, cfg *Config[T], dao ClientGetter) (*Manager[T], error) {
	if cfg.Dynamic == nil || cfg.Dynamic.Provider == nil {
		return NewManager(ctx, cfg)
	}

	pc := *cfg.Dynamic.Provider

	builderMu.RLock()
	b, ok := builders[pc.Type]
	builderMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf(
			"config: provider type %q not registered; import _ %q",
			pc.Type,
			"github.com/micoya/gocraft/config/dynprovider/"+pc.Type,
		)
	}

	name := pc.Name
	if name == "" {
		name = "default"
	}
	client, err := dao.Get(daoKind(pc.Type), name)
	if err != nil {
		return nil, fmt.Errorf("config: get dao client %s/%s: %w", pc.Type, name, err)
	}
	p, err := b(pc, client)
	if err != nil {
		return nil, fmt.Errorf("config: build provider %s/%s: %w", pc.Type, name, err)
	}
	return NewManager(ctx, cfg, p)
}

// daoKind 将 provider type 映射到 cdao 中的资源 kind。
func daoKind(providerType string) string {
	if providerType == "db" {
		return "database"
	}
	return providerType
}

// Manager 管理 app 配置块（Config.App）的动态热更新，并发安全。
//
// 设计约束：
//   - 动态更新范围仅限 app 块，基础设施配置（DAO/日志/HTTP 等）在启动时
//     一次性加载，不参与热更新，避免已建立连接池被意外覆盖。
//   - 多个 Provider 按注册顺序依次加载初始补丁，后注册的优先级更高。
//   - 运行时各 Provider 的 Watch 并发推送，每条补丁独立深度合并。
//
// 使用方式（手动指定 provider）：
//
//	cfg, _ := config.Load[AppCfg](ctx)
//	mgr, _ := config.NewManager(ctx, cfg, redisProvider, dbProvider)
//	defer mgr.Close()
//
// 使用方式（配置驱动，推荐）：
//
//	import _ "github.com/micoya/gocraft/config/dynprovider/redis"
//	import _ "github.com/micoya/gocraft/config/dynprovider/db"
//
//	cfg, _ := config.Load[AppCfg](ctx)
//	mgr, _ := config.NewManagerFromDAO(ctx, cfg, dao)
//	defer mgr.Close()
//
//	app := mgr.App()   // 任何时候都拿到最新值，无需自行加锁
type Manager[T any] struct {
	mu     sync.RWMutex
	v      *viper.Viper
	app    T
	subs   []func(T)
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewManager 创建 Manager：依序加载各 provider 的初始补丁，然后启动后台 Watch。
//
// providers 按传入顺序加载，后者优先级更高（后者的补丁覆盖前者）。
// cfg 须为 Load[T] 返回的实例。
func NewManager[T any](ctx context.Context, cfg *Config[T], providers ...DynamicProvider) (*Manager[T], error) {
	watchCtx, cancel := context.WithCancel(ctx)

	m := &Manager[T]{
		v:      cfg.v,
		app:    cfg.App,
		cancel: cancel,
	}

	for _, p := range providers {
		data, err := p.Load(ctx)
		if err != nil {
			cancel()
			return nil, fmt.Errorf("config: manager: load from %s: %w", p.Name(), err)
		}
		if len(data) > 0 {
			if err := m.applyPatch(data); err != nil {
				cancel()
				return nil, fmt.Errorf("config: manager: apply patch from %s: %w", p.Name(), err)
			}
		}
	}

	// patches 由所有 provider 共享写入，aggregator goroutine 负责消费
	patches := make(chan []byte, 16)

	var provWg sync.WaitGroup
	for _, p := range providers {
		p := p
		provWg.Add(1)
		go func() {
			defer provWg.Done()
			_ = p.Watch(watchCtx, patches)
		}()
	}

	// 所有 provider 退出后关闭 patches，aggregator 随之退出
	go func() {
		provWg.Wait()
		close(patches)
	}()

	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		for data := range patches {
			_ = m.applyPatch(data)
		}
	}()

	return m, nil
}

// App 返回当前 app 配置快照，并发安全，调用方无需加锁。
func (m *Manager[T]) App() T {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.app
}

// OnChange 注册 app 配置变更回调，每次热更新完成后在独立 goroutine 中触发。
// 支持链式调用。回调中不应执行耗时操作。
func (m *Manager[T]) OnChange(fn func(T)) *Manager[T] {
	m.mu.Lock()
	m.subs = append(m.subs, fn)
	m.mu.Unlock()
	return m
}

// Close 取消所有 provider 的监听并等待后台 goroutine 退出。
func (m *Manager[T]) Close() {
	m.cancel()
	m.wg.Wait()
}

// applyPatch 将 JSON 补丁深度合并进当前 app 配置，只覆盖补丁中出现的叶子字段。
func (m *Manager[T]) applyPatch(data []byte) error {
	leafs, err := parseJSONPatch(data)
	if err != nil {
		return fmt.Errorf("config: apply patch: %w", err)
	}

	m.mu.Lock()
	for k, val := range leafs {
		m.v.Set("app."+k, val)
	}
	var newApp T
	if err := m.v.UnmarshalKey("app", &newApp); err != nil {
		m.mu.Unlock()
		return fmt.Errorf("config: apply patch: unmarshal: %w", err)
	}
	m.app = newApp
	subs := make([]func(T), len(m.subs))
	copy(subs, m.subs)
	m.mu.Unlock()

	for _, fn := range subs {
		fn(newApp)
	}
	return nil
}
