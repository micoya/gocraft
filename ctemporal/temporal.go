// Package ctemporal 封装 Temporal Go SDK，提供配置驱动的连接管理和 Worker 生命周期管理。
//
// 核心设计原则：
//   - 只封装"胶水代码"（连接、TLS、slog、生命周期），不包装业务 API
//   - Workflow 和 Activity 定义完全使用 Temporal SDK 原生写法
//   - 通过 config.TemporalConfig 统一管理连接参数
//
// 快速上手：
//
//  1. 写 Workflow 和 Activity（原生 Temporal SDK 写法）：
//
//	func OrderWorkflow(ctx workflow.Context, input OrderInput) error {
//	    ao := workflow.ActivityOptions{StartToCloseTimeout: 30 * time.Second}
//	    ctx = workflow.WithActivityOptions(ctx, ao)
//	    return workflow.ExecuteActivity(ctx, (*OrderActivities).Charge, input).Get(ctx, nil)
//	}
//
//  2. 创建 App，注册 Worker，启动：
//
//	app, _ := ctemporal.New(cfg.Temporal, logger)
//
//	app.NewWorker("order-queue", func(w worker.Worker) {
//	    w.RegisterWorkflow(OrderWorkflow)
//	    w.RegisterActivity(NewOrderActivities(db, redis))
//	})
//
//	go app.Run(ctx) // 阻塞直到 ctx 取消
//
//  3. 触发 Workflow（从 API handler 中）：
//
//	run, _ := app.Client().ExecuteWorkflow(ctx,
//	    client.StartWorkflowOptions{ID: "order-"+orderID, TaskQueue: "order-queue"},
//	    OrderWorkflow, input,
//	)
package ctemporal

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log/slog"
	"os"
	"sync"

	temporalclient "go.temporal.io/sdk/client"
	temporallog "go.temporal.io/sdk/log"
	temporalworker "go.temporal.io/sdk/worker"

	"github.com/micoya/gocraft/config"
)

// App 是 Temporal 的生命周期管理器，持有一个 Client 和若干 Worker。
// 通常整个进程只创建一个 App 实例。
type App struct {
	client         temporalclient.Client
	workers        []temporalworker.Worker
	workerDefaults *config.TemporalWorkerConfig
	log            *slog.Logger
	mu             sync.Mutex
	started        bool
}

// WorkerOption 配置单个 Worker 的可选项，覆盖全局 TemporalWorkerConfig 默认值。
type WorkerOption func(*temporalworker.Options)

// WithMaxConcurrentActivities 设置该 Worker 的最大并发 Activity 执行数。
func WithMaxConcurrentActivities(n int) WorkerOption {
	return func(o *temporalworker.Options) { o.MaxConcurrentActivityExecutionSize = n }
}

// WithMaxConcurrentWorkflowTasks 设置该 Worker 的最大并发 Workflow Task 执行数。
func WithMaxConcurrentWorkflowTasks(n int) WorkerOption {
	return func(o *temporalworker.Options) { o.MaxConcurrentWorkflowTaskExecutionSize = n }
}

// WithMaxConcurrentLocalActivities 设置该 Worker 的最大并发 Local Activity 执行数。
func WithMaxConcurrentLocalActivities(n int) WorkerOption {
	return func(o *temporalworker.Options) { o.MaxConcurrentLocalActivityExecutionSize = n }
}

// WithWorkerOptions 直接传入完整的 worker.Options（高级用法，覆盖所有默认值）。
func WithWorkerOptions(opts temporalworker.Options) WorkerOption {
	return func(o *temporalworker.Options) { *o = opts }
}

// New 根据配置创建 Temporal App，建立到 Temporal Server 的 gRPC 连接。
//
// 连接场景：
//   - 本地开发：只需 host_port + namespace
//   - Temporal Cloud（API Key 模式）：host_port + namespace + api_key（自动启用 TLS）
//   - Temporal Cloud（mTLS 模式）：host_port + namespace + tls.cert_file + tls.key_file
//   - 自托管 TLS：host_port + namespace + tls.ca_file
func New(cfg *config.TemporalConfig, log *slog.Logger) (*App, error) {
	if cfg == nil {
		return nil, fmt.Errorf("ctemporal: config is required")
	}
	if log == nil {
		log = slog.Default()
	}

	hostPort := cfg.HostPort
	if hostPort == "" {
		hostPort = "localhost:7233"
	}
	namespace := cfg.Namespace
	if namespace == "" {
		namespace = "default"
	}

	opts := temporalclient.Options{
		HostPort:  hostPort,
		Namespace: namespace,
		Logger:    temporallog.NewStructuredLogger(log),
	}

	// Temporal Cloud API Key 认证（优先级高于 TLS 证书）
	if cfg.APIKey != "" {
		opts.Credentials = temporalclient.NewAPIKeyStaticCredentials(cfg.APIKey)
		// Temporal Cloud 强制要求 TLS
		opts.ConnectionOptions = temporalclient.ConnectionOptions{
			TLS: &tls.Config{MinVersion: tls.VersionTLS12},
		}
	}

	// TLS 配置（覆盖上面的默认 TLS）
	if cfg.TLS != nil {
		tlsCfg, err := buildTLSConfig(cfg.TLS)
		if err != nil {
			return nil, fmt.Errorf("ctemporal: build TLS config: %w", err)
		}
		opts.ConnectionOptions = temporalclient.ConnectionOptions{TLS: tlsCfg}
	}

	c, err := temporalclient.Dial(opts)
	if err != nil {
		return nil, fmt.Errorf("ctemporal: dial %s: %w", hostPort, err)
	}

	log.Info("ctemporal: connected", "host_port", hostPort, "namespace", namespace)

	return &App{
		client:         c,
		workerDefaults: cfg.Worker,
		log:            log,
	}, nil
}

// Client 返回底层 Temporal Client。
//
// 常用操作：
//
//	// 触发 Workflow
//	run, err := app.Client().ExecuteWorkflow(ctx,
//	    client.StartWorkflowOptions{ID: "my-id", TaskQueue: "my-queue"},
//	    MyWorkflow, input,
//	)
//
//	// 发送 Signal
//	app.Client().SignalWorkflow(ctx, workflowID, "", "my-signal", payload)
//
//	// 查询状态
//	resp, err := app.Client().QueryWorkflow(ctx, workflowID, "", "my-query")
func (a *App) Client() temporalclient.Client {
	return a.client
}

// NewWorker 创建一个 Worker，监听指定 taskQueue，通过 register 函数注册 Workflow 和 Activity。
//
// register 函数接收 Temporal SDK 原生的 worker.Worker，直接调用其 RegisterWorkflow / RegisterActivity：
//
//	app.NewWorker("order-queue", func(w worker.Worker) {
//	    w.RegisterWorkflow(workflow.OrderWorkflow)
//	    w.RegisterWorkflow(workflow.PaymentWorkflow)
//	    w.RegisterActivity(activities.NewOrderActivities(db, redis))
//	    w.RegisterActivity(activities.NewPaymentActivities(payClient))
//	})
//
// opts 可覆盖全局 TemporalWorkerConfig 中的并发设置。
func (a *App) NewWorker(taskQueue string, register func(temporalworker.Worker), opts ...WorkerOption) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if taskQueue == "" {
		return fmt.Errorf("ctemporal: taskQueue is required")
	}
	if a.started {
		return fmt.Errorf("ctemporal: cannot add worker after App has started")
	}

	wopts := a.buildWorkerOptions(opts)

	w := temporalworker.New(a.client, taskQueue, wopts)
	if register != nil {
		register(w)
	}
	a.workers = append(a.workers, w)
	a.log.Info("ctemporal: worker registered", "task_queue", taskQueue)
	return nil
}

// Run 启动所有 Worker 并阻塞，直到 ctx 取消后优雅停止所有 Worker 并关闭 Client。
//
// 推荐在主 goroutine 或应用入口中调用，与信号处理集成：
//
//	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
//	defer stop()
//
//	if err := app.Run(ctx); err != nil {
//	    log.Fatal(err)
//	}
func (a *App) Run(ctx context.Context) error {
	if err := a.start(); err != nil {
		return err
	}
	<-ctx.Done()
	a.stop()
	return nil
}

// start 启动所有已注册的 Worker（非阻塞）。
func (a *App) start() error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.started {
		return nil
	}

	for i, w := range a.workers {
		if err := w.Start(); err != nil {
			// 回滚：停止已成功启动的 Worker
			for j := i - 1; j >= 0; j-- {
				a.workers[j].Stop()
			}
			return fmt.Errorf("ctemporal: start worker %d: %w", i, err)
		}
	}
	a.started = true
	a.log.Info("ctemporal: all workers started", "count", len(a.workers))
	return nil
}

// stop 优雅停止所有 Worker 并关闭 Client。
func (a *App) stop() {
	a.mu.Lock()
	defer a.mu.Unlock()

	for _, w := range a.workers {
		w.Stop()
	}
	a.client.Close()
	a.log.Info("ctemporal: shutdown complete")
	a.started = false
}

// buildWorkerOptions 从全局配置构建 worker.Options，并应用 per-worker 覆盖。
func (a *App) buildWorkerOptions(opts []WorkerOption) temporalworker.Options {
	wopts := temporalworker.Options{}

	// 应用全局默认配置
	if d := a.workerDefaults; d != nil {
		if d.MaxConcurrentActivityExecutors > 0 {
			wopts.MaxConcurrentActivityExecutionSize = d.MaxConcurrentActivityExecutors
		}
		if d.MaxConcurrentWorkflowTaskExecutors > 0 {
			wopts.MaxConcurrentWorkflowTaskExecutionSize = d.MaxConcurrentWorkflowTaskExecutors
		}
		if d.MaxConcurrentLocalActivityExecutors > 0 {
			wopts.MaxConcurrentLocalActivityExecutionSize = d.MaxConcurrentLocalActivityExecutors
		}
	}

	// 应用 per-worker 覆盖
	for _, opt := range opts {
		opt(&wopts)
	}
	return wopts
}

// buildTLSConfig 根据文件路径构建 tls.Config。
func buildTLSConfig(cfg *config.TemporalTLSConfig) (*tls.Config, error) {
	tlsCfg := &tls.Config{
		MinVersion:         tls.VersionTLS12,
		ServerName:         cfg.ServerName,
		InsecureSkipVerify: cfg.InsecureSkipVerify, //nolint:gosec
	}

	// 客户端证书（mTLS）
	if cfg.CertFile != "" && cfg.KeyFile != "" {
		cert, err := tls.LoadX509KeyPair(cfg.CertFile, cfg.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("load client cert (%s, %s): %w", cfg.CertFile, cfg.KeyFile, err)
		}
		tlsCfg.Certificates = []tls.Certificate{cert}
	}

	// 自定义 CA（自托管 Temporal Server）
	if cfg.CAFile != "" {
		caPEM, err := os.ReadFile(cfg.CAFile)
		if err != nil {
			return nil, fmt.Errorf("read CA file %s: %w", cfg.CAFile, err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(caPEM) {
			return nil, fmt.Errorf("parse CA cert from %s", cfg.CAFile)
		}
		tlsCfg.RootCAs = pool
	}

	return tlsCfg, nil
}
