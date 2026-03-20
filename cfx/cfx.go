// Package cfx 提供基于 uber/fx 的 gocraft 原子依赖注入单元。
//
// 每个 Provide* 函数仅向容器注入一个依赖，按需组合使用：
//
//	fx.New(
//	    fx.WithLogger(cfx.LoggerProvider),
//	    cfx.ProvideConfig[AppConfig](),
//	    cfx.ProvideLogger[AppConfig](),
//	    cfx.ProvideOtel[AppConfig](),
//	    cfx.ProvideDAO[AppConfig](),
//	    cfx.ProvideHTTPServer[AppConfig](),
//	    cfx.ProvideLocker(),
//	    cfx.ProvideCron[AppConfig](),
//	    cfx.ProvideTemporal[AppConfig](),
//	    cfx.ProvideUIDUUID(),
//	    cfx.ProvideUIDSnowflakeStatic(1),
//	    // 或 cfx.ProvideUIDSnowflakeStaticFromConfig[AppConfig](),
//	    // cfx.ProvideUIDRedisSnowflake("default", cuid.WithRedisKeyPrefix("app:sf:")),
//	    // cfx.ProvideUIDRedisSnowflakeFromConfig[AppConfig](),
//	    cfx.ProvideUIDSonyflake(),
//	).Run()
package cfx

import (
	"context"
	"log/slog"
	"time"

	"go.uber.org/fx"
	"go.uber.org/fx/fxevent"

	"github.com/micoya/gocraft/ccron"
	"github.com/micoya/gocraft/cdao"
	"github.com/micoya/gocraft/cdao/redisx"
	"github.com/micoya/gocraft/chttp"
	"github.com/micoya/gocraft/clocker"
	"github.com/micoya/gocraft/clog"
	"github.com/micoya/gocraft/config"
	"github.com/micoya/gocraft/cotel"
	"github.com/micoya/gocraft/ctemporal"
)

// ProvideConfig 向容器注入 *config.Config[T]。
// T 为业务扩展配置类型（对应 config.yaml 中的 app 块），无扩展时传 struct{}。
func ProvideConfig[T any](opts ...config.Option) fx.Option {
	return fx.Provide(func() (*config.Config[T], error) {
		return config.Load[T](context.Background(), opts...)
	})
}

// ProvideLogger 向容器注入 *slog.Logger，依赖 *config.Config[T]。
func ProvideLogger[T any]() fx.Option {
	return fx.Provide(func(cfg *config.Config[T]) (*slog.Logger, error) {
		return clog.NewFromConfig(cfg.Log)
	})
}

// ProvideOtel 向容器注入 *cotel.Provider，依赖 *config.Config[T]。
// OTel 在 fx OnStop 时自动 Shutdown。
func ProvideOtel[T any]() fx.Option {
	return fx.Provide(func(lc fx.Lifecycle, cfg *config.Config[T]) (*cotel.Provider, error) {
		otelCfg := cfg.Otel
		if otelCfg == nil {
			otelCfg = &config.OtelConfig{}
		}
		name := cfg.Name
		if name == "" {
			name = "app"
		}
		p, err := cotel.New(context.Background(), otelCfg, name)
		if err != nil {
			return nil, err
		}
		lc.Append(fx.Hook{
			OnStop: func(ctx context.Context) error {
				return p.Shutdown(ctx)
			},
		})
		return p, nil
	})
}

// ProvideDAO 向容器注入 *cdao.DAO，依赖 *config.Config[T]。
//
// DAO 在 provider 执行阶段即完成 Init（早于 OnStart），确保所有依赖 *cdao.DAO
// 的 Service 构造函数执行时连接已就绪。fx OnStop 时自动 Close。
//
// 调用方须在 import 中引入所需的 cdao provider 包以注册驱动，例如：
//
//	import _ "github.com/micoya/gocraft/cdao/provider/database"
//	import _ "github.com/micoya/gocraft/cdao/provider/redis"
func ProvideDAO[T any]() fx.Option {
	return fx.Provide(func(lc fx.Lifecycle, cfg *config.Config[T]) (*cdao.DAO, error) {
		daoCfg := cfg.DAO
		if daoCfg == nil {
			daoCfg = &config.DAOConfig{}
		}
		dao, err := cdao.NewFromConfig(daoCfg)
		if err != nil {
			return nil, err
		}
		if err := dao.Init(context.Background()); err != nil {
			return nil, err
		}
		lc.Append(fx.Hook{
			OnStop: func(ctx context.Context) error {
				return dao.Close(ctx)
			},
		})
		return dao, nil
	})
}

// ProvideHTTPServer 向容器注入 *chttp.Server，依赖 *config.Config[T] 和 *slog.Logger。
// 若容器中存在 *cotel.Provider，将自动启用 OTel 中间件。
// Server 在 fx OnStart 时启动，OnStop 时优雅关闭。
func ProvideHTTPServer[T any]() fx.Option {
	type deps struct {
		fx.In
		Cfg  *config.Config[T]
		Log  *slog.Logger
		Otel *cotel.Provider `optional:"true"`
	}
	return fx.Provide(func(lc fx.Lifecycle, d deps) *chttp.Server {
		opts := []chttp.Option{
			chttp.WithLogger(d.Log),
		}
		if d.Cfg.HTTPServer != nil {
			opts = append(opts, chttp.WithServerConfig(d.Cfg.HTTPServer))
		}
		if d.Otel != nil {
			opts = append(opts, chttp.WithOtelProvider(d.Otel))
		}
		s := chttp.New(opts...)

		var cancel context.CancelFunc
		lc.Append(fx.Hook{
			OnStart: func(_ context.Context) error {
				var runCtx context.Context
				runCtx, cancel = context.WithCancel(context.Background())
				go func() {
					if err := s.Run(runCtx); err != nil {
						d.Log.Error("chttp: server stopped with error", "error", err)
					}
				}()
				return nil
			},
			OnStop: func(_ context.Context) error {
				if cancel != nil {
					cancel()
				}
				return nil
			},
		})
		return s
	})
}

// ProvideLocker 向容器注入 *clocker.Locker，依赖 *cdao.DAO（使用名为 "default" 的 Redis）。
// 需确保 DAO 已初始化且配置了 default Redis。
func ProvideLocker() fx.Option {
	return fx.Provide(func(dao *cdao.DAO) *clocker.Locker {
		return clocker.New(redisx.Must(dao))
	})
}

// clockerLockerAdapter 将 *clocker.Locker 适配为 ccron.Locker 接口。
// 两者的 TryLock 签名相同，仅返回的 Lock 接口类型名称不同。
type clockerLockerAdapter struct{ l *clocker.Locker }

func (a clockerLockerAdapter) TryLock(ctx context.Context, key string, ttl time.Duration) (ccron.Lock, error) {
	return a.l.TryLock(ctx, key, ttl)
}

// ProvideCron 向容器注入 *ccron.Scheduler，依赖 *config.Config[T] 和 *slog.Logger。
// 若容器中存在 *clocker.Locker 且 config.Cron.Distributed 为 true，将自动启用分布式防重复执行。
// Scheduler 在 fx OnStart 时启动，OnStop 时优雅停止。
func ProvideCron[T any]() fx.Option {
	type deps struct {
		fx.In
		Cfg    *config.Config[T]
		Log    *slog.Logger
		Locker *clocker.Locker `optional:"true"`
	}
	return fx.Provide(func(lc fx.Lifecycle, d deps) *ccron.Scheduler {
		cronCfg := d.Cfg.Cron

		opts := []ccron.Option{ccron.WithLogger(d.Log)}
		if cronCfg != nil && cronCfg.Timezone != "" {
			opts = append(opts, ccron.WithTimezone(cronCfg.Timezone))
		}
		if d.Locker != nil && cronCfg != nil && cronCfg.Distributed {
			lockTTL := 5 * time.Minute
			if cronCfg.LockTTL > 0 {
				lockTTL = cronCfg.LockTTL
			}
			opts = append(opts, ccron.WithLocker(clockerLockerAdapter{d.Locker}, lockTTL))
		}

		s := ccron.New(opts...)

		lc.Append(fx.Hook{
			OnStart: func(_ context.Context) error {
				s.Start()
				return nil
			},
			OnStop: func(ctx context.Context) error {
				s.Stop(ctx)
				return nil
			},
		})
		return s
	})
}

// ProvideTemporal 向容器注入 *ctemporal.App，依赖 *config.Config[T] 和 *slog.Logger。
// App 在 fx OnStop 时自动关闭所有 Worker 并断开连接。
func ProvideTemporal[T any]() fx.Option {
	return fx.Provide(func(lc fx.Lifecycle, cfg *config.Config[T], log *slog.Logger) (*ctemporal.App, error) {
		app, err := ctemporal.New(cfg.Temporal, log)
		if err != nil {
			return nil, err
		}
		var cancel context.CancelFunc
		lc.Append(fx.Hook{
			OnStart: func(_ context.Context) error {
				var runCtx context.Context
				runCtx, cancel = context.WithCancel(context.Background())
				go func() {
					_ = app.Run(runCtx)
				}()
				return nil
			},
			OnStop: func(_ context.Context) error {
				if cancel != nil {
					cancel()
				}
				return nil
			},
		})
		return app, nil
	})
}

// LoggerProvider 是供 fx.WithLogger 使用的事件日志适配函数。
// 将容器中的 *slog.Logger 接入 fx 内部事件输出。
//
// 用法：fx.WithLogger(cfx.LoggerProvider)
func LoggerProvider(log *slog.Logger) fxevent.Logger {
	return &fxevent.SlogLogger{Logger: log}
}
