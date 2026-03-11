# gocraft

Go 开发脚手架包集合。模块路径：`github.com/micoya/gocraft`

---

## 包概览

| 包 | 核心依赖 | 功能 |
|---|---|---|
| `config` | viper | YAML + 环境变量配置加载 |
| `clog` | log/slog | 结构化日志，支持 trace 注入 |
| `cdao` | gorm, go-redis/v9, ... | 数据访问层容器（DB/Redis 生命周期管理） |
| `chttp` | gin | HTTP 服务器，优雅关闭 |
| `cotel` | opentelemetry-go | Trace + Metrics（OTLP gRPC + Prometheus） |

---

## config

**核心依赖**：`github.com/spf13/viper`、`github.com/joho/godotenv`

配置优先级：默认值 < `config.yaml` < 环境变量（`.env` 文件 < 系统 ENV）。

环境变量层级分隔符用 `__`，例如 `http_server.addr` → `HTTP_SERVER__ADDR`。

```go
// T 为业务自定义扩展配置（对应 yaml 中 app 块），不需要时传 struct{}
cfg, err := config.Load[AppConfig](ctx,
    config.WithConfigPath("./configs"), // 默认 "."
    config.WithEnvFile(".env"),         // 默认 ".env"，传 "" 跳过
)
// cfg.Name, cfg.Env, cfg.Log, cfg.HTTPServer, cfg.Otel, cfg.DAO, cfg.App
```

**config.yaml 示例**：
```yaml
name: my-app
env: production

log:
  level: INFO          # DEBUG/INFO/WARN/ERROR
  format: json         # text | json
  path: stdout         # stdout | stderr | /path/to/file
  with_trace: true     # 自动从 context 注入 trace_id/span_id
  add_source: true

http_server:
  addr: ":8080"
  shutdown_timeout: 30s
  health_path: /healthz
  metrics_path: /metrics
  cors:
    allow_all_origins: true
  access_log:
    enabled: true
  pprof:
    enabled: true
    allow_external: false
    authorization_token: ""

otel:
  trace:
    endpoint: "otel-collector:4317"
    insecure: true
    sample_rate: 1.0
  metric:
    endpoint: "otel-collector:4317"
    insecure: true

dao:
  database:
    default:
      driver: mysql   # mysql | postgres
      dsn: "user:pass@tcp(127.0.0.1:3306)/db?parseTime=True"
    replica:
      driver: mysql
      dsn: "..."
  redis:
    default:
      addr: "127.0.0.1:6379"
      password: ""
      db: 0
      read_timeout: 3s
      write_timeout: 3s
```

---

## clog

**核心依赖**：标准库 `log/slog`

返回 `*slog.Logger`，支持 text/json 格式，可从 context 自动提取 OTel span 注入 `trace_id`/`span_id`。

```go
// 方式一：从 config 创建
logger, err := clog.NewFromConfig(cfg.Log)

// 方式二：Option 创建
logger := clog.New(
    clog.WithLevel(slog.LevelDebug),
    clog.WithFormat("json"),
    clog.WithTrace(true),
)

// 使用（标准 slog API）
logger.InfoContext(ctx, "request received", "path", "/api/v1/users")

// 手动注入 trace（不使用 OTel 时）
ctx = clog.ContextWithTrace(ctx, clog.TraceInfo{TraceID: "abc", SpanID: "def"})
```

---

## cdao

**核心依赖**：`gorm.io/gorm`（mysql/postgres）、`github.com/redis/go-redis/v9`

DAO 是资源容器，通过 provider 插件管理连接生命周期（Init/Close/Health）。

**使用 provider 前必须用空白导入注册**：

```go
import (
    _ "github.com/micoya/gocraft/cdao/provider/database" // 注册 database provider
    _ "github.com/micoya/gocraft/cdao/provider/redis"    // 注册 redis provider
)

// 创建并初始化
dao, err := cdao.NewFromConfig(cfg.DAO)
if err != nil { ... }
if err := dao.Init(ctx); err != nil { ... }
defer dao.Close(ctx)

// 获取 *gorm.DB（类型安全，失败 panic）
import "github.com/micoya/gocraft/cdao/gormx"
db := gormx.Must(dao, "default")   // → *gorm.DB

// 获取 *redis.Client
import "github.com/micoya/gocraft/cdao/redisx"
rdb := redisx.Must(dao, "default") // → *redis.Client

// 通用方式（需自行类型断言）
raw, err := dao.Get("database", "default")

// 健康检查（所有资源 Ping）
err = dao.HealthCheck(ctx)
```

**自定义 Provider**（扩展其他资源类型）：

```go
// 实现 cdao.Provider 接口并在 init() 中注册
cdao.Register("kafka", myKafkaFactory)
```

---

## chttp

**核心依赖**：`github.com/gin-gonic/gin`

封装了 gin，自动注册 Recovery、AccessLog、CORS、OTel、pprof 中间件，支持优雅关闭。

内置路由：`GET /healthz`（健康检查）、`GET /metrics`（Prometheus，需注入 OTel）、`/debug/pprof/*`。

```go
// 创建服务器
srv := chttp.New(
    chttp.WithServerConfig(cfg.HTTPServer),
    chttp.WithLogger(logger),
    chttp.WithOtelProvider(otelProvider), // 可选，注入后启用 trace/metrics
)

// 注册路由
r := srv.Engine() // *gin.Engine
r.GET("/api/v1/users", handler)

// 启动（阻塞，ctx 取消后优雅关闭）
if err := srv.Run(ctx); err != nil { ... }
```

中间件顺序：`Recovery → otelgin → TraceID → HTTPMetrics → AccessLog → CORS`

---

## cotel

**核心依赖**：`go.opentelemetry.io/otel`，导出器：OTLP gRPC（trace + metric）+ Prometheus（metric）

```go
// 初始化，设置全局 TracerProvider 和 MeterProvider
otelProvider, err := cotel.New(ctx, cfg.Otel, cfg.Name)
if err != nil { ... }
defer otelProvider.Shutdown(ctx)

// cfg.Otel 为 nil 时返回 no-op Provider，其他包可安全使用

// Prometheus handler（挂到 /metrics）
handler := otelProvider.PrometheusHandler() // 可能为 nil
```

trace 采集后通过 `chttp.WithOtelProvider` 注入 HTTP 服务，chttp 会自动启用 `otelgin` 中间件传播 span context。

---

## 典型启动流程

```go
func main() {
    ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
    defer stop()

    cfg, _ := config.Load[AppConfig](ctx)
    logger, _ := clog.NewFromConfig(cfg.Log)
    otelProvider, _ := cotel.New(ctx, cfg.Otel, cfg.Name)
    defer otelProvider.Shutdown(ctx)

    dao, _ := cdao.NewFromConfig(cfg.DAO)
    _ = dao.Init(ctx)
    defer dao.Close(ctx)

    srv := chttp.New(
        chttp.WithServerConfig(cfg.HTTPServer),
        chttp.WithLogger(logger),
        chttp.WithOtelProvider(otelProvider),
    )
    srv.Engine().GET("/api/v1/hello", helloHandler)
    _ = srv.Run(ctx)
}
```
