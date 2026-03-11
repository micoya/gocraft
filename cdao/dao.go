package cdao

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/micoya/gocraft/config"
)

type entry struct {
	provider Provider
	inited   bool
}

// DAO 是数据访问层的核心容器，管理所有资源的生命周期。
type DAO struct {
	mu      sync.RWMutex
	entries map[string]map[string]*entry // kind -> name -> entry
	inited  bool
}

// NewFromConfig 根据 DAOConfig 创建 DAO 实例（未初始化）。
// 配置中声明的每种资源必须有对应的 provider 已通过 Register 注册，否则返回错误。
func NewFromConfig(cfg *config.DAOConfig) (*DAO, error) {
	d := &DAO{entries: make(map[string]map[string]*entry)}

	for name, c := range cfg.Database {
		if err := d.add("database", name, c); err != nil {
			return nil, err
		}
	}
	for name, c := range cfg.Redis {
		if err := d.add("redis", name, c); err != nil {
			return nil, err
		}
	}
	for name, c := range cfg.OSS {
		if err := d.add("oss", name, c); err != nil {
			return nil, err
		}
	}
	for name, c := range cfg.OpenAI {
		if err := d.add("openai", name, c); err != nil {
			return nil, err
		}
	}

	return d, nil
}

func (d *DAO) add(kind, name string, rawCfg any) error {
	f, ok := factories[kind]
	if !ok {
		return fmt.Errorf(
			"dao: provider %q not registered; import _ %q",
			kind,
			"github.com/micoya/gocraft/cdao/provider/"+kind,
		)
	}

	p, err := f(name, rawCfg)
	if err != nil {
		return fmt.Errorf("dao: create %s/%s: %w", kind, name, err)
	}

	if d.entries[kind] == nil {
		d.entries[kind] = make(map[string]*entry)
	}
	d.entries[kind][name] = &entry{provider: p}
	return nil
}

// Init 初始化所有 provider。
//   - 已初始化的不重复执行（幂等）
//   - 任一资源初始化失败时回滚所有本次已成功初始化的资源
func (d *DAO) Init(ctx context.Context) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.inited {
		return nil
	}

	type ref struct{ kind, name string }
	var done []ref

	for kind, names := range d.entries {
		for name, e := range names {
			if e.inited {
				continue
			}
			if err := e.provider.Init(ctx); err != nil {
				for i := len(done) - 1; i >= 0; i-- {
					r := done[i]
					_ = d.entries[r.kind][r.name].provider.Close(ctx)
					d.entries[r.kind][r.name].inited = false
				}
				return fmt.Errorf("dao: init %s/%s: %w", kind, name, err)
			}
			e.inited = true
			done = append(done, ref{kind, name})
		}
	}

	d.inited = true
	return nil
}

// Close 关闭所有已初始化的 provider，返回遇到的第一个错误。
func (d *DAO) Close(ctx context.Context) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	var firstErr error
	for kind, names := range d.entries {
		for name, e := range names {
			if !e.inited {
				continue
			}
			if err := e.provider.Close(ctx); err != nil && firstErr == nil {
				firstErr = fmt.Errorf("dao: close %s/%s: %w", kind, name, err)
			}
			e.inited = false
		}
	}
	d.inited = false
	return firstErr
}

// HealthCheck 检查所有已初始化 provider 的健康状态。
func (d *DAO) HealthCheck(ctx context.Context) error {
	d.mu.RLock()
	defer d.mu.RUnlock()

	for kind, names := range d.entries {
		for name, e := range names {
			if !e.inited {
				continue
			}
			if err := e.provider.Health(ctx); err != nil {
				return fmt.Errorf("dao: health %s/%s: %w", kind, name, err)
			}
		}
	}
	return nil
}

// Get 返回指定资源的底层客户端实例。DAO 未初始化时返回错误。
func (d *DAO) Get(kind, name string) (any, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	if !d.inited {
		return nil, errors.New("dao: not initialized; call Init first")
	}

	names, ok := d.entries[kind]
	if !ok {
		return nil, fmt.Errorf("dao: kind %q not found", kind)
	}
	e, ok := names[name]
	if !ok {
		return nil, fmt.Errorf("dao: %s/%s not found", kind, name)
	}
	return e.provider.Instance(), nil
}

// Must 获取指定资源的类型安全实例。kind/name 不存在或类型不匹配时 panic。
func Must[T any](d *DAO, kind, name string) T {
	raw, err := d.Get(kind, name)
	if err != nil {
		panic(err)
	}
	v, ok := raw.(T)
	if !ok {
		panic(fmt.Sprintf("dao: %s/%s: want %T, got %T", kind, name, *new(T), raw))
	}
	return v
}
