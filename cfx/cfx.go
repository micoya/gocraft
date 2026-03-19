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
//	    // 其余全部由业务项目自行 provide
//	).Run()
package cfx

import (
	"context"
	"log/slog"

	"go.uber.org/fx"
	"go.uber.org/fx/fxevent"

	"github.com/micoya/gocraft/cdao"
	"github.com/micoya/gocraft/clog"
	"github.com/micoya/gocraft/config"
	"github.com/micoya/gocraft/cotel"
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

// LoggerProvider 是供 fx.WithLogger 使用的事件日志适配函数。
// 将容器中的 *slog.Logger 接入 fx 内部事件输出。
//
// 用法：fx.WithLogger(cfx.LoggerProvider)
func LoggerProvider(log *slog.Logger) fxevent.Logger {
	return &fxevent.SlogLogger{Logger: log}
}
