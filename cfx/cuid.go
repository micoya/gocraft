package cfx

import (
	"context"
	"errors"
	"fmt"

	"github.com/bwmarrin/snowflake"
	"github.com/google/uuid"
	"github.com/sony/sonyflake/v2"
	"go.uber.org/fx"

	"github.com/micoya/gocraft/cdao"
	"github.com/micoya/gocraft/config"
	"github.com/micoya/gocraft/cuid"
)

// UIDUUIDGen 无状态 UUID v4 生成抽象，便于测试时替换实现。
type UIDUUIDGen interface {
	NewV4() uuid.UUID
	NewV4String() string
	NewV4NoDash() string
}

type uidUUIDGen struct{}

func (uidUUIDGen) NewV4() uuid.UUID      { return cuid.UUIDV4() }
func (uidUUIDGen) NewV4String() string   { return cuid.UUIDV4String() }
func (uidUUIDGen) NewV4NoDash() string   { return cuid.UUIDV4NoDash() }

// ProvideUIDUUID 向容器注入 UIDUUIDGen（默认委托 cuid 包函数）。
func ProvideUIDUUID() fx.Option {
	return fx.Provide(func() UIDUUIDGen {
		return uidUUIDGen{}
	})
}

// ProvideUIDSnowflakeStatic 向容器注入 *snowflake.Node，nodeID 与配置文件中的静态节点一致。
func ProvideUIDSnowflakeStatic(nodeID int64) fx.Option {
	return fx.Provide(func() (*snowflake.Node, error) {
		return cuid.NewSnowflakeNode(nodeID)
	})
}

// ProvideUIDSnowflakeStaticFromConfig 从 config.Config[T].UID.SnowflakeStatic 读取 node_id。
func ProvideUIDSnowflakeStaticFromConfig[T any]() fx.Option {
	return fx.Provide(func(cfg *config.Config[T]) (*snowflake.Node, error) {
		if cfg.UID == nil || cfg.UID.SnowflakeStatic == nil {
			return nil, errors.New("cfx: uid: config.uid.snowflake_static is required")
		}
		return cuid.NewSnowflakeNode(cfg.UID.SnowflakeStatic.NodeID)
	})
}

func uidRedisSnowflakeOptsFromConfig(c *config.UIDRedisSnowflakeConfig) []cuid.RedisSnowflakeOption {
	if c == nil {
		return nil
	}
	var opts []cuid.RedisSnowflakeOption
	if c.KeyPrefix != "" {
		opts = append(opts, cuid.WithRedisKeyPrefix(c.KeyPrefix))
	}
	if c.HeartbeatEvery > 0 && c.LeaseTTL > 0 {
		opts = append(opts, cuid.WithHeartbeat(c.HeartbeatEvery, c.LeaseTTL))
	}
	if c.MaxNodeExclusive > 0 {
		opts = append(opts, cuid.WithMaxNode(c.MaxNodeExclusive))
	}
	return opts
}

// ProvideUIDRedisSnowflake 向容器注入 *cuid.RedisSnowflake，依赖已初始化的 *cdao.DAO。
// redisName 为空时使用与 redisx.Must 相同的 "default" 实例。
// fx OnStop 时自动 Close 释放租约。
func ProvideUIDRedisSnowflake(redisName string, opts ...cuid.RedisSnowflakeOption) fx.Option {
	return fx.Provide(func(lc fx.Lifecycle, dao *cdao.DAO) (*cuid.RedisSnowflake, error) {
		ctx := context.Background()
		rs, err := cuid.NewRedisSnowflakeDAO(ctx, dao, redisName, opts...)
		if err != nil {
			return nil, fmt.Errorf("cfx: uid redis snowflake: %w", err)
		}
		lc.Append(fx.Hook{
			OnStop: func(stopCtx context.Context) error {
				return rs.Close(stopCtx)
			},
		})
		return rs, nil
	})
}

// ProvideUIDRedisSnowflakeFromConfig 从 config.Config[T].UID.RedisSnowflake 读取参数。
func ProvideUIDRedisSnowflakeFromConfig[T any]() fx.Option {
	return fx.Provide(func(lc fx.Lifecycle, cfg *config.Config[T], dao *cdao.DAO) (*cuid.RedisSnowflake, error) {
		if cfg.UID == nil || cfg.UID.RedisSnowflake == nil {
			return nil, errors.New("cfx: uid: config.uid.redis_snowflake is required")
		}
		rc := cfg.UID.RedisSnowflake
		opts := uidRedisSnowflakeOptsFromConfig(rc)
		ctx := context.Background()
		rs, err := cuid.NewRedisSnowflakeDAO(ctx, dao, rc.Redis, opts...)
		if err != nil {
			return nil, fmt.Errorf("cfx: uid redis snowflake: %w", err)
		}
		lc.Append(fx.Hook{
			OnStop: func(stopCtx context.Context) error {
				return rs.Close(stopCtx)
			},
		})
		return rs, nil
	})
}

// ProvideUIDSonyflake 向容器注入 *sonyflake.Sonyflake（机器号为私网 IPv4 后 16 位，见 cuid.NewSonyflake）。
func ProvideUIDSonyflake() fx.Option {
	return fx.Provide(func() (*sonyflake.Sonyflake, error) {
		return cuid.NewSonyflake()
	})
}
