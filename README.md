# gocraft

面向业务落地的 **Go 脚手架与可复用库集合**，模块路径：`github.com/micoya/gocraft`。适合希望统一技术栈、从单体配置到观测与中间件都「开箱即用」的团队。

**设计取向**：涉及 I/O 的 API 使用 `context`；包之间可组合、可单独引用；行为以单元测试约束；避免为简单逻辑强行抽象。

**环境要求**：Go **1.25+**（以仓库根目录 `go.mod` 为准）。

---

## 命令行工具

`cmd/gocraft` 提供基于 Cobra 的 CLI，用于项目创建等脚手架能力。安装方式：`go install github.com/micoya/gocraft/cmd/gocraft@latest`，子命令与参数可通过 `gocraft -h` 查看。

---

## 模块总览

下列子目录均为同一 module 下的包，可按需 `import`，不必整仓引入。

| 包 | 能力摘要 |
| --- | --- |
| **config** | 基于 Viper：YAML、环境变量、可选 `.env`；支持业务自定义配置块与分层键名（如 `__` 映射嵌套）。 |
| **clog** | 基于标准库 `log/slog` 的结构化日志；可选从 context / OpenTelemetry span 注入 trace 相关字段。 |
| **cotel** | OpenTelemetry 初始化：OTLP gRPC 导出 trace/metric、Prometheus 指标；配置缺失时可退化为 no-op，便于与其他包安全组合。 |
| **chttp** | 基于 Gin 的 HTTP 服务封装：优雅关闭、健康检查与 Prometheus 路由、常用中间件（恢复、访问日志、CORS、OTel、pprof 等可配置）。 |
| **cdao** | 数据访问层**容器**：统一 `Init` / `Close` / `HealthCheck`，按配置拉起多实例资源；通过 **provider 插件**扩展类型。 |
| **cfx** | 将上述能力拆成 **uber/fx** 的 `Provide*` 原子单元，按需组装应用生命周期（含 OTel、DAO、HTTP、定时任务、Temporal、分布式 ID 等注入）。 |
| **cerror** | 业务错误类型与错误码，语义与 HTTP 状态码对齐；支持错误链与映射到 HTTP 状态，便于 API 层统一响应。 |
| **cpager** | 基于页码/页大小的分页：参数归一化、与 GORM 集成的查询封装、泛型结果结构。 |
| **ccache** | 缓存抽象：**Redis**、基于 Ristretto 的**内存**、**L1+L2 分层**（内存 + Redis）；值类型为 string，序列化由业务决定；支持 `GetOrSet` 等模式。 |
| **clocker** | 基于 Redis 的分布式锁：`TryLock` / 阻塞 `Lock`、Lua 安全解锁、可选 **watchdog 自动续期**。 |
| **ccron** | 基于 `robfig/cron` 的调度：任务级 OTel span、panic 恢复、可选**分布式锁**避免多副本重复跑、时区可配置。 |
| **cpubsub** | 发布/订阅抽象（发布、消费组式订阅）；实现位于子包 **Redis / Kafka / RabbitMQ**。 |
| **ctemporal** | Temporal 客户端与 Worker 的**胶水封装**：配置驱动连接、TLS、slog、多 Worker 注册与统一 `Run`；业务 Workflow/Activity 仍用官方 SDK 原生写法。 |
| **cworker** | 带并发上限的后台任务池：信号量控并发、panic 恢复、`Stop` 等待在途任务结束。 |
| **climiter** | 限流器：**滑动窗口**（推荐）、**固定窗口**、**令牌桶**（Redis 分布式）、**本地内存**；支持配置驱动多实例 Registry、per-key 限流、OTel span 自动埋点。 |
| **cuid** | 分布式 ID：**UUID v4**、**Snowflake**（静态节点或基于 Redis 租约的动态节点）、**Sonyflake**（默认结合本机私网 IPv4 派生机器号）。 |

---

## cdao 资源类型与辅助包

在配置中声明资源后，需**空白导入**对应 `cdao/provider/...` 完成注册。当前内置种类包括：

- **database**（GORM，MySQL / Postgres）— 辅助取句柄：`cdao/gormx`
- **redis** — `cdao/redisx`
- **mongo** — `cdao/mongox`，可选 OTel 插桩子包
- **elasticsearch** — `cdao/elasticsearchx`
- **kafka** — `cdao/kafkax`
- **rabbitmq** — `cdao/rabbitmqx`
- **httpclient**（可观测 HTTP 客户端）— `cdao/httpclientx`
- **oss**（阿里云 OSS）— `cdao/ossx`
- **openai**（官方 Go SDK 客户端）— `cdao/openaix`
- **mcache**（Ristretto 本地缓存）— `cdao/mcachex`
- **mns**（阿里云消息服务 MNS）— `cdao/mnsx`
- **tablestore**（阿里云表格存储 OTS）— `cdao/tablestorex`，内置 OTel 插桩

多数与外部服务相关的 provider 提供 **OpenTelemetry 插桩**子包，便于与 `cotel` / `chttp` 串联。

---

## 推荐阅读顺序

1. **config** → **clog** → **cotel**：配置、日志与观测打底。  
2. **cdao** + 需要的 **provider** + 对应 `*x` 辅助包：接好数据与中间件。  
3. **chttp** 或 **cfx**：手写组装用前者，依赖注入与生命周期用后者。  
4. 按业务需要叠加 **ccache**、**clocker**、**ccron**、**cpubsub**、**ctemporal**、**cworker**、**cuid**、**climiter**、**cpager**、**cerror**。

各包顶部的 **package 注释**与 `_test.go` 即最贴近代码的用法说明；细节以源码为准。

---

## 仓库与协作

本仓库为**多包 monorepo**，根目录 `go.mod` 统一管理依赖版本。若你扩展新的 `cdao` provider 或公共库，请保持对外 API 稳定、为 I/O 方法传入 `context`，并补充单元测试。
