// Package db 提供基于关系型数据库（MySQL / PostgreSQL）的动态配置 Provider。
//
// 配置以 JSON 格式存储在 dynamic_configs 表中，Provider 在初始化时自动建表（AutoMigrate），
// 通过定时轮询 updated_at 字段检测变更。
//
// 建表后可直接通过 SQL 管理配置：
//
//	INSERT INTO dynamic_configs (key, value) VALUES ('app', '{"feature_flag": true}')
//	  ON DUPLICATE KEY UPDATE value = '{"feature_flag": true}';   -- MySQL
//	-- 或
//	INSERT INTO dynamic_configs (key, value) VALUES ('app', '{"feature_flag": true}')
//	  ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value;     -- PostgreSQL
//
// 使用示例：
//
//	db := gormx.Must(dao)
//	p, _ := dyndb.New(db)
//	mgr, _ := config.NewManager(ctx, cfg, p)
package db

import (
	"context"
	"fmt"
	"sync"
	"time"

	"gorm.io/gorm"

	"github.com/micoya/gocraft/config"
)

func init() {
	config.RegisterProviderBuilder("db", func(pcfg config.DynProviderConfig, client any) (config.DynamicProvider, error) {
		db, ok := client.(*gorm.DB)
		if !ok {
			return nil, fmt.Errorf("config/dynprovider/db: expected *gorm.DB, got %T", client)
		}
		key := pcfg.Key
		if key == "" {
			key = "app"
		}
		var opts []Option
		if pcfg.PollInterval > 0 {
			opts = append(opts, WithPollInterval(pcfg.PollInterval))
		}
		opts = append(opts, WithKey(key))
		return New(db, opts...)
	})
}

const defaultPollInterval = 30 * time.Second

// DynamicConfig 动态配置存储模型，对应 dynamic_configs 表。
type DynamicConfig struct {
	Key       string    `gorm:"primaryKey;size:255"`
	Value     string    `gorm:"type:text;not null"`
	UpdatedAt time.Time `gorm:"autoUpdateTime"`
}

// Option 配置 Provider 的可选项。
type Option func(*Provider)

// WithPollInterval 设置轮询间隔，默认 30s。
func WithPollInterval(d time.Duration) Option {
	return func(p *Provider) { p.pollInterval = d }
}

// WithKey 指定配置在表中的行 key，默认 "app"。
// 同一个数据库实例需要存储多个应用配置时可使用不同 key。
func WithKey(key string) Option {
	return func(p *Provider) { p.key = key }
}

// Provider 通过轮询数据库表提供动态配置。
// 支持 MySQL 和 PostgreSQL（通过 gorm dialector 自动适配）。
type Provider struct {
	db           *gorm.DB
	key          string
	pollInterval time.Duration

	mu          sync.Mutex
	lastUpdated time.Time // Load 返回时的 updated_at，Watch 从此处开始检测变更
}

// New 创建 DB 动态配置 Provider，并自动执行 AutoMigrate 建表。
// db 应为已初始化的 *gorm.DB（通常来自 cdao）。
func New(db *gorm.DB, opts ...Option) (*Provider, error) {
	p := &Provider{
		db:           db,
		key:          "app",
		pollInterval: defaultPollInterval,
	}
	for _, opt := range opts {
		opt(p)
	}

	if err := db.AutoMigrate(&DynamicConfig{}); err != nil {
		return nil, fmt.Errorf("config/dynprovider/db: auto-migrate: %w", err)
	}
	return p, nil
}

// Name 实现 config.DynamicProvider。
func (p *Provider) Name() string { return "db:dynamic_configs[" + p.key + "]" }

// Load 实现 config.DynamicProvider，读取行的当前值作为初始补丁，
// 并将 updated_at 记录为 Watch 的基准时间戳，避免 Watch 首次轮询重复推送相同内容。
// 行不存在时返回 nil（无初始覆盖）。
func (p *Provider) Load(ctx context.Context) ([]byte, error) {
	var record DynamicConfig
	err := p.db.WithContext(ctx).Where("key = ?", p.key).First(&record).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("config/dynprovider/db: load: %w", err)
	}
	p.mu.Lock()
	p.lastUpdated = record.UpdatedAt
	p.mu.Unlock()
	return []byte(record.Value), nil
}

// Watch 实现 config.DynamicProvider，轮询检测 updated_at 变更并推送补丁。
// Watch 以 Load 记录的 updated_at 为基准，只推送时间戳更新后的内容。
// ctx 取消后退出，不再写入 patches。
func (p *Provider) Watch(ctx context.Context, patches chan<- []byte) error {
	ticker := time.NewTicker(p.pollInterval)
	defer ticker.Stop()

	p.mu.Lock()
	lastUpdated := p.lastUpdated
	p.mu.Unlock()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			var record DynamicConfig
			err := p.db.WithContext(ctx).Where("key = ?", p.key).First(&record).Error
			if err != nil {
				continue
			}
			if !record.UpdatedAt.After(lastUpdated) {
				continue
			}
			lastUpdated = record.UpdatedAt
			select {
			case patches <- []byte(record.Value):
			case <-ctx.Done():
				return nil
			}
		}
	}
}

// Close 实现 config.DynamicProvider，gorm.DB 由外部管理，此处无需释放。
func (p *Provider) Close() error { return nil }
