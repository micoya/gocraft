package chttp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-contrib/pprof"
	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/contrib/instrumentation/github.com/gin-gonic/gin/otelgin"
	"go.opentelemetry.io/otel"

	"github.com/micoya/gocraft/chttp/middleware"
	"github.com/micoya/gocraft/config"
	"github.com/micoya/gocraft/cotel"
)

// Server 基于 gin 的 HTTP 服务器，支持优雅关闭。
type Server struct {
	engine  *gin.Engine
	httpSrv *http.Server
	cfg     config.HTTPServerConfig
	log     *slog.Logger
	otel    *cotel.Provider
}

// Option 创建 Server 时的可选配置项。
type Option func(*Server)

// WithServerConfig 设置 HTTP 服务配置。
func WithServerConfig(cfg *config.HTTPServerConfig) Option {
	return func(s *Server) {
		if cfg != nil {
			s.cfg = *cfg
		}
	}
}

// WithLogger 设置 Server 使用的日志实例。
func WithLogger(log *slog.Logger) Option {
	return func(s *Server) {
		if log != nil {
			s.log = log
		}
	}
}

// WithOtelProvider 注入 OTel Provider，启用 trace/metrics 中间件和 Prometheus 端点。
func WithOtelProvider(p *cotel.Provider) Option {
	return func(s *Server) {
		s.otel = p
	}
}

// New 创建 Server 实例。默认使用 gin.ReleaseMode，自动注册 Recovery 中间件，
// 根据配置启用 OTel、AccessLog、CORS 等中间件。
func New(opts ...Option) *Server {
	s := &Server{
		cfg: config.HTTPServerConfig{
			Addr:            ":8080",
			ShutdownTimeout: 30 * time.Second,
			ReadTimeout:     30 * time.Second,
			WriteTimeout:    30 * time.Second,
			IdleTimeout:     60 * time.Second,
			HealthPath:      "/healthz",
			AccessLog:       config.AccessLogConfig{Enabled: true},
			Pprof:           config.PprofConfig{Enabled: true},
		},
		log: slog.Default(),
	}

	for _, opt := range opts {
		opt(s)
	}

	gin.SetMode(gin.ReleaseMode)
	engine := gin.New()

	// 中间件顺序: Recovery → otelgin → TraceID → HTTPMetrics → AccessLog → CORS
	engine.Use(middleware.Recovery(s.log))

	if s.otel != nil {
		engine.Use(otelgin.Middleware("", otelgin.WithTracerProvider(otel.GetTracerProvider())))
		engine.Use(middleware.TraceID())
		engine.Use(middleware.HTTPMetrics(otel.Meter("chttp")))
	}

	if s.cfg.AccessLog.Enabled {
		engine.Use(middleware.AccessLog(s.log))
	}

	if s.cfg.CORS != nil {
		engine.Use(middleware.CORS(s.cfg.CORS))
	}

	if s.cfg.HealthPath != "" {
		engine.GET(s.cfg.HealthPath, func(c *gin.Context) {
			c.String(http.StatusOK, "ok")
		})
	}

	if s.otel != nil && s.otel.PrometheusHandler() != nil && s.cfg.MetricsPath != "" {
		engine.GET(s.cfg.MetricsPath, gin.WrapH(s.otel.PrometheusHandler()))
	}

	if s.cfg.Pprof.Enabled {
		pprofGroup := engine.Group("/debug/pprof", middleware.PprofGuard(&s.cfg.Pprof))
		pprof.RouteRegister(pprofGroup, "")
	}

	s.engine = engine
	s.httpSrv = &http.Server{
		Addr:         s.cfg.Addr,
		Handler:      engine,
		ReadTimeout:  s.cfg.ReadTimeout,
		WriteTimeout: s.cfg.WriteTimeout,
		IdleTimeout:  s.cfg.IdleTimeout,
	}

	return s
}

// Engine 返回底层 gin.Engine，用于注册路由和中间件。
func (s *Server) Engine() *gin.Engine {
	return s.engine
}

// Run 启动 HTTP 服务并阻塞，直到 ctx 取消后执行优雅关闭。
func (s *Server) Run(ctx context.Context) error {
	errCh := make(chan error, 1)
	go func() {
		errCh <- s.httpSrv.ListenAndServe()
	}()

	s.log.Info("http server started", "addr", s.cfg.Addr)

	select {
	case <-ctx.Done():
		shutdownTimeout := s.cfg.ShutdownTimeout
		if shutdownTimeout <= 0 {
			shutdownTimeout = 30 * time.Second
		}
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()

		s.log.Info("http server shutting down")
		if err := s.httpSrv.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("chttp: shutdown: %w", err)
		}
		s.log.Info("http server stopped")
		return nil

	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("chttp: listen: %w", err)
	}
}
