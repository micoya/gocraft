# Temporal 工作流引擎（ctemporal）

`ctemporal` 封装了 [Temporal Go SDK](https://github.com/temporalio/sdk-go)，提供配置驱动的连接管理和 Worker 生命周期管理。

## 什么是 Temporal？

先用一个对比来理解：

| 传统方案 | Temporal 方案 |
|---|---|
| 写一个"下单流程"：调支付 → 扣库存 → 发短信，任何一步失败就要自己写重试、补偿、状态记录 | 写一个 Workflow 函数，Temporal 自动处理重试、崩溃恢复、状态持久化，**代码就是流程图** |
| 定时任务跑到一半服务崩了，不知道执行到哪了 | Temporal 记录每一步的状态，重启后从断点继续 |
| 需要等待用户确认的流程（审批、支付回调）要靠数据库轮询 | Workflow 可以睡眠等待 Signal，零轮询 |

**核心概念：**
- **Workflow**：业务流程的"剧本"，用普通 Go 函数写，但不能有 IO（必须通过 Activity）
- **Activity**：实际干活的函数，调数据库、发 HTTP 请求都在这里，自动重试
- **Worker**：一个进程，轮询任务队列，执行 Workflow 和 Activity
- **Client**：用来触发 Workflow、发送 Signal、查询状态

## 安装和配置

### 本地开发（最简单）

```bash
# 安装 Temporal CLI 并启动本地 Server
brew install temporal
temporal server start-dev
# Server 启动在 localhost:7233，Web UI 在 http://localhost:8233
```

```yaml
# config.yaml
temporal:
  host_port: "localhost:7233"
  namespace: "default"
```

### Temporal Cloud（生产环境推荐）

```yaml
temporal:
  host_port: "your-namespace.your-account.tmprl.cloud:7233"
  namespace: "your-namespace.your-account"
  api_key: "your-temporal-cloud-api-key"
```

### 自托管（有 TLS 证书）

```yaml
temporal:
  host_port: "temporal.internal:7233"
  namespace: "production"
  tls:
    cert_file: "/certs/client.pem"
    key_file:  "/certs/client.key"
    ca_file:   "/certs/ca.pem"
```

## 第一个完整示例：订单处理流程

### 1. 定义 Activity（实际干活的函数）

```go
// internal/workflow/activities.go
package workflow

import (
    "context"
    "time"
    "go.temporal.io/sdk/activity"
)

// OrderActivities 持有 Activity 需要的依赖（数据库、外部服务等）
type OrderActivities struct {
    db      *gorm.DB
    payment PaymentClient
    notify  NotifyClient
}

func NewOrderActivities(db *gorm.DB, payment PaymentClient, notify NotifyClient) *OrderActivities {
    return &OrderActivities{db: db, payment: payment, notify: notify}
}

// ChargePayment 扣款（可以失败，Temporal 会自动重试）
func (a *OrderActivities) ChargePayment(ctx context.Context, orderID string, amount float64) error {
    activity.RecordHeartbeat(ctx, "charging") // 长任务时汇报进度
    return a.payment.Charge(ctx, orderID, amount)
}

// DeductInventory 扣库存
func (a *OrderActivities) DeductInventory(ctx context.Context, orderID string, items []Item) error {
    return a.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
        for _, item := range items {
            if err := deductStock(tx, item); err != nil {
                return err
            }
        }
        return nil
    })
}

// SendConfirmation 发送确认短信
func (a *OrderActivities) SendConfirmation(ctx context.Context, orderID, phone string) error {
    return a.notify.SendSMS(ctx, phone, "您的订单 "+orderID+" 已确认")
}
```

### 2. 定义 Workflow（流程编排）

```go
// internal/workflow/order_workflow.go
package workflow

import (
    "time"
    "go.temporal.io/sdk/workflow"
)

type OrderInput struct {
    OrderID string
    UserID  int64
    Amount  float64
    Items   []Item
    Phone   string
}

// OrderWorkflow 是一个不会丢失状态的订单处理流程
// ⚠️ Workflow 里不能直接调 DB/HTTP，必须通过 ExecuteActivity
func OrderWorkflow(ctx workflow.Context, input OrderInput) error {
    // Activity 默认超时配置
    ao := workflow.ActivityOptions{
        StartToCloseTimeout: 30 * time.Second,
        RetryPolicy: &temporal.RetryPolicy{
            MaximumAttempts: 3,
        },
    }
    ctx = workflow.WithActivityOptions(ctx, ao)

    // 用 nil 指针引用 Activity 方法（框架会自动路由，不会真正调用）
    var act *OrderActivities

    // 步骤 1：扣款
    if err := workflow.ExecuteActivity(ctx, act.ChargePayment, input.OrderID, input.Amount).Get(ctx, nil); err != nil {
        return fmt.Errorf("扣款失败: %w", err)
    }

    // 步骤 2：扣库存
    if err := workflow.ExecuteActivity(ctx, act.DeductInventory, input.OrderID, input.Items).Get(ctx, nil); err != nil {
        // 扣库存失败 → 退款（补偿事务，Saga 模式）
        workflow.ExecuteActivity(ctx, act.RefundPayment, input.OrderID).Get(ctx, nil)
        return fmt.Errorf("扣库存失败: %w", err)
    }

    // 步骤 3：发送确认通知（失败不影响主流程）
    notifyCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
        StartToCloseTimeout: 10 * time.Second,
        RetryPolicy: &temporal.RetryPolicy{MaximumAttempts: 2},
    })
    workflow.ExecuteActivity(notifyCtx, act.SendConfirmation, input.OrderID, input.Phone)

    return nil
}
```

### 3. 启动 Worker

```go
// cmd/worker/main.go
package main

import (
    "context"
    "os"
    "os/signal"

    "go.temporal.io/sdk/worker"
    "github.com/micoya/gocraft/ctemporal"
    "github.com/micoya/gocraft/config"
    appworkflow "myapp/internal/workflow"
)

func main() {
    cfg, _ := config.Load[struct{}](context.Background())

    app, err := ctemporal.New(cfg.Temporal, slog.Default())
    if err != nil {
        slog.Error("connect temporal failed", "error", err)
        os.Exit(1)
    }

    // 注入依赖（db, redis 等从 DAO 取）
    activities := appworkflow.NewOrderActivities(
        gormx.Must(dao),
        paymentClient,
        notifyClient,
    )

    // 注册 Workflow 和 Activity
    app.NewWorker("order-queue", func(w worker.Worker) {
        w.RegisterWorkflow(appworkflow.OrderWorkflow)
        w.RegisterActivity(activities)
    })

    // 阻塞直到 Ctrl+C
    ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
    defer stop()

    app.Run(ctx)
}
```

### 4. 触发 Workflow（从 HTTP Handler 中）

```go
// internal/handler/order.go
package handler

import (
    "github.com/gin-gonic/gin"
    temporalclient "go.temporal.io/sdk/client"
    appworkflow "myapp/internal/workflow"
)

type OrderHandler struct {
    temporal *ctemporal.App
}

func (h *OrderHandler) CreateOrder(c *gin.Context) {
    var req CreateOrderRequest
    c.ShouldBindJSON(&req)

    input := appworkflow.OrderInput{
        OrderID: generateOrderID(),
        UserID:  req.UserID,
        Amount:  req.Amount,
        Items:   req.Items,
        Phone:   req.Phone,
    }

    // 触发 Workflow（立即返回，不等待执行完成）
    run, err := h.temporal.Client().ExecuteWorkflow(c.Request.Context(),
        temporalclient.StartWorkflowOptions{
            ID:        "order-" + input.OrderID, // 同一 ID 不会重复触发（幂等）
            TaskQueue: "order-queue",
        },
        appworkflow.OrderWorkflow,
        input,
    )
    if err != nil {
        c.JSON(500, gin.H{"error": "触发订单流程失败"})
        return
    }

    c.JSON(200, gin.H{
        "order_id":    input.OrderID,
        "workflow_id": run.GetID(),
        "run_id":      run.GetRunID(),
    })
}
```

## 等待外部事件（Signal）

适合需要等待用户确认、支付回调的场景：

```go
func PaymentWorkflow(ctx workflow.Context, orderID string) error {
    // 注册 Signal Channel，等待外部发信号
    signalCh := workflow.GetSignalChannel(ctx, "payment-callback")

    var result PaymentResult
    // 等待最多 30 分钟
    selector := workflow.NewSelector(ctx)
    selector.AddReceive(signalCh, func(ch workflow.ReceiveChannel, more bool) {
        ch.Receive(ctx, &result)
    })
    selector.AddFuture(workflow.NewTimer(ctx, 30*time.Minute), func(f workflow.Future) {
        result.Status = "timeout"
    })
    selector.Select(ctx)

    if result.Status != "success" {
        // 触发退款 Activity
        return workflow.ExecuteActivity(ctx, refundActivity, orderID).Get(ctx, nil)
    }
    return nil
}

// 在支付回调 Handler 中发送 Signal
func (h *Handler) PaymentCallback(c *gin.Context) {
    h.temporal.Client().SignalWorkflow(c.Request.Context(),
        "order-"+orderID, "",
        "payment-callback",
        PaymentResult{Status: "success", TxID: txID},
    )
}
```

## WorkerOption：覆盖并发配置

```go
app.NewWorker("heavy-tasks",
    func(w worker.Worker) { /* ... */ },
    ctemporal.WithMaxConcurrentActivities(20),      // 这个队列限制 20 并发
    ctemporal.WithMaxConcurrentWorkflowTasks(50),
)
```

## 混合部署：同进程 HTTP + Worker

```go
func main() {
    cfg, _ := config.Load[struct{}](context.Background())

    // 初始化 Temporal
    temporalApp, _ := ctemporal.New(cfg.Temporal, logger)
    temporalApp.NewWorker("default", func(w worker.Worker) {
        w.RegisterWorkflow(...)
        w.RegisterActivity(...)
    })

    // 初始化 HTTP Server
    httpServer := chttp.New(
        chttp.WithServerConfig(cfg.HTTPServer),
        chttp.WithLogger(logger),
    )
    httpServer.Engine().POST("/orders", orderHandler.CreateOrder)

    ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
    defer stop()

    // 并发运行两个服务
    eg, ctx := errgroup.WithContext(ctx)
    eg.Go(func() error { return httpServer.Run(ctx) })
    eg.Go(func() error { return temporalApp.Run(ctx) })
    eg.Wait()
}
```

## Temporal 关键约束

| 约束 | 说明 |
|---|---|
| Workflow 必须确定性 | 相同输入必须产生相同行为，不能用 `rand`、`time.Now()`（用 `workflow.Now()`）|
| Workflow 不能有 IO | 不能直接调 DB、HTTP、文件系统，必须通过 Activity |
| Activity 可以做任何事 | 有 IO、有副作用、可以失败（Temporal 自动重试）|
| 参数必须可序列化 | Workflow/Activity 参数和返回值会被序列化存储 |

## 查看执行状态

访问 Temporal Web UI（本地：http://localhost:8233）可以看到：
- 所有 Workflow 的执行历史
- 每个 Activity 的调用记录和重试次数
- 失败原因和堆栈追踪
- 当前等待中的 Workflow
