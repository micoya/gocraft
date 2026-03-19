// Package cfx 提供基于 uber/fx 的 gocraft 核心模块。
//
// 使用方式：
//
//	import "github.com/micoya/gocraft/cfx"
//
//	func main() {
//	    fx.New(
//	        fx.WithLogger(cfx.LoggerProvider),
//	        app.Module(),
//	        fx.Invoke(cfx.RunHTTPServer),
//	    ).Run()
//	}
//
//	func Module() fx.Option {
//	    return fx.Options(
//	        cfx.CoreModule[AppConfig](),  // 提供 config / logger / otel / http server
//	        cfx.DAOModule[AppConfig](),   // 可选：提供 *cdao.DAO
//	        fx.Provide(service.NewXxxService),
//	        fx.Provide(api.NewXxxHandler),
//	        fx.Invoke(RegisterRoutes),
//	    )
//	}
package cfx

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"go.uber.org/fx"
	"go.uber.org/fx/fxevent"

	"github.com/micoya/gocraft/cdao"
	"github.com/micoya/gocraft/chttp"
	"github.com/micoya/gocraft/clog"
	"github.com/micoya/gocraft/config"
	"github.com/micoya/gocraft/cotel"
)

// CoreModule 提供核心基础设施依赖，是所有 gocraft 应用的基础模块。
//
// 向 fx 容器提供：
//   - *config.Config[T]  — 应用配置（文件 + 环境变量）
//   - *slog.Logger       — 结构化日志
//   - *cotel.Provider    — OTel 可观测性（trace + metrics），随 fx 生命周期自动 Shutdown
//   - *chttp.Server      — HTTP 服务器实例（路由尚未绑定）
//
// T 为业务扩展配置类型（对应 config.yaml 中的 app 块）；无扩展配置时传 struct{}。
// cfgOpts 透传给 config.Load，可用于指定配置目录等。
func CoreModule[T any](cfgOpts ...config.Option) fx.Option {
	return fx.Options(
		fx.Provide(func() (*config.Config[T], error) {
			return config.Load[T](context.Background(), cfgOpts...)
		}),
		fx.Provide(func(cfg *config.Config[T]) (*slog.Logger, error) {
			return clog.NewFromConfig(cfg.Log)
		}),
		fx.Provide(func(lc fx.Lifecycle, cfg *config.Config[T]) (*cotel.Provider, error) {
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
		}),
		fx.Provide(func(cfg *config.Config[T], log *slog.Logger, otel *cotel.Provider) *chttp.Server {
			return chttp.New(
				chttp.WithServerConfig(cfg.HTTPServer),
				chttp.WithLogger(log),
				chttp.WithOtelProvider(otel),
			)
		}),
	)
}

// DAOModule 提供 *cdao.DAO 依赖，并通过 fx 生命周期自动管理 Init / Close。
//
// 需要在 CoreModule 之后声明（依赖 *config.Config[T]）。
// 调用方须在 import 中引入所需的 cdao provider 包以注册驱动工厂，例如：
//
//	import _ "github.com/micoya/gocraft/cdao/provider/database"
//	import _ "github.com/micoya/gocraft/cdao/provider/redis"
func DAOModule[T any]() fx.Option {
	return fx.Options(
		fx.Provide(func(cfg *config.Config[T]) (*cdao.DAO, error) {
			daoCfg := cfg.DAO
			if daoCfg == nil {
				daoCfg = &config.DAOConfig{}
			}
			return cdao.NewFromConfig(daoCfg)
		}),
		fx.Invoke(func(lc fx.Lifecycle, dao *cdao.DAO) {
			lc.Append(fx.Hook{
				OnStart: func(ctx context.Context) error {
					return dao.Init(ctx)
				},
				OnStop: func(ctx context.Context) error {
					return dao.Close(ctx)
				},
			})
		}),
	)
}

// RunHTTPServer 管理 HTTP Server 的生命周期，供 fx.Invoke 使用。
//
// OnStart：后台启动服务，打印监听地址。
// OnStop：通知 server 执行优雅关闭。
//
// 用法：fx.Invoke(cfx.RunHTTPServer)
func RunHTTPServer(lc fx.Lifecycle, server *chttp.Server, log *slog.Logger) {
	ctx, cancel := context.WithCancel(context.Background())
	lc.Append(fx.Hook{
		OnStart: func(_ context.Context) error {
			go func() {
				if err := server.Run(ctx); err != nil {
					log.Error("http server stopped with error", slog.Any("error", err))
				}
			}()
			addr := server.Addr()
			host := strings.Replace(addr, ":", "localhost:", 1)
			fmt.Printf("\n\033[32m🚀 服务已启动 → http://%s\033[0m\n\n", host)
			return nil
		},
		OnStop: func(_ context.Context) error {
			cancel()
			return nil
		},
	})
}

// LoggerProvider 是供 fx.WithLogger 使用的事件日志提供函数。
// 使用 *slog.Logger（由 CoreModule 提供）驱动 fx 的内部事件日志输出。
//
// 用法：fx.WithLogger(cfx.LoggerProvider)
func LoggerProvider(log *slog.Logger) fxevent.Logger {
	return &fxevent.SlogLogger{Logger: log}
}
