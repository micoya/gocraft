# 后台任务 Worker 池（cworker）

`cworker` 提供并发受控的后台任务池，解决 Go 中无限制创建 goroutine 导致资源耗尽的问题。

## 特性

- 信号量限制最大并发 goroutine 数
- 每个 goroutine 自动 recover panic，记录堆栈到 slog，不崩溃进程
- 支持优雅关闭：`Stop()` 等待所有在途任务完成后返回
- `TryGo()` 非阻塞提交，立即返回是否成功入队

## 配置

| 选项 | 说明 | 默认值 |
|---|---|---|
| `WithConcurrency(n)` | 最大并发 goroutine 数 | CPU 核数 |
| `WithLogger(log)` | panic 日志记录器 | `slog.Default()` |

## 基本用法

```go
// 创建 Worker 池（默认并发数 = CPU 核数）
pool := cworker.New(
    cworker.WithConcurrency(50),
    cworker.WithLogger(logger),
)

// 提交异步任务（阻塞直到有空闲槽位）
err := pool.Go(ctx, func(ctx context.Context) {
    if err := sendEmail(ctx, email); err != nil {
        slog.ErrorContext(ctx, "发送邮件失败", "error", err)
    }
})
if errors.Is(err, context.Canceled) {
    // pool 已关闭或 ctx 已取消
}

// 非阻塞提交（立即返回）
if !pool.TryGo(ctx, func(ctx context.Context) {
    doWork(ctx)
}) {
    // 达到并发上限，可选择降级处理
    log.Warn("worker pool full, task dropped")
}

// 优雅关闭（等待在途任务完成）
shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
defer cancel()
if err := pool.Stop(shutdownCtx); err != nil {
    log.Error("worker pool shutdown timeout", "error", err)
}
```

## 典型场景：处理 HTTP 请求中的异步副作用

```go
func CreateOrder(c *gin.Context) {
    order, err := orderSvc.Create(c.Request.Context(), req)
    if err != nil {
        // 处理错误
        return
    }

    // 异步发送确认邮件，不阻塞响应
    _ = workerPool.Go(c.Request.Context(), func(ctx context.Context) {
        notifySvc.SendOrderConfirm(ctx, order)
    })

    c.JSON(200, order)
}
```

## 在应用启动/关闭生命周期中集成

```go
func main() {
    pool := cworker.New(cworker.WithConcurrency(100))

    // 注入到服务层
    svc := service.New(pool)

    // 应用关闭时优雅停止
    ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
    defer stop()

    <-ctx.Done()
    shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
    defer cancel()
    pool.Stop(shutdownCtx)
}
```
