# 定时任务（ccron）

`ccron` 提供基于 `robfig/cron` 的定时任务调度器，支持 OTel 全链路追踪和分布式防重复执行。

## 特性

- 支持标准 5 位和扩展 6 位（含秒）cron 表达式
- 每次任务执行自动创建 OTel span，完整覆盖执行时长
- panic 自动恢复并记录堆栈，不影响调度器运行
- 分布式模式：多实例部署时同一时刻只有一个节点执行任务

## 快速开始

### 普通模式（单实例）

```go
import "github.com/micoya/gocraft/ccron"

scheduler := ccron.New(
    ccron.WithTimezone("Asia/Shanghai"),
    ccron.WithLogger(logger),
)

// 注册任务（6 位 cron：秒 分 时 日 月 周）
scheduler.Add("清理过期订单", "0 0 2 * * *", func(ctx context.Context) {
    // ctx 已注入 OTel span，直接透传给下游调用
    if err := orderSvc.CleanExpired(ctx); err != nil {
        slog.ErrorContext(ctx, "清理失败", "error", err)
    }
})

scheduler.Add("每5分钟同步汇率", "0 */5 * * * *", func(ctx context.Context) {
    rateSvc.Sync(ctx)
})

scheduler.Start()
defer scheduler.Stop(ctx) // 优雅停止，等待当前执行的任务完成
```

### 分布式模式（多实例防重复）

```go
import (
    "github.com/micoya/gocraft/ccron"
    "github.com/micoya/gocraft/clocker"
    "github.com/micoya/gocraft/cdao/redisx"
)

locker := clocker.New(redisx.Must(dao))

scheduler := ccron.New(
    ccron.WithTimezone("Asia/Shanghai"),
    ccron.WithLocker(locker, 5*time.Minute), // LockTTL 略大于任务预期最长执行时间
)

scheduler.Add("每日结算", "0 0 1 * * *", func(ctx context.Context) {
    settlementSvc.RunDaily(ctx)
})

scheduler.Start()
```

多实例部署时，每次任务触发时会竞争分布式锁，只有抢到锁的节点执行，其余节点静默跳过。

## 从 Config 读取配置

```go
// config.yaml
// cron:
//   timezone: "Asia/Shanghai"
//   distributed: true
//   lock_redis: "default"
//   lock_ttl: 5m

cfg, _ := config.Load[struct{}](ctx)

var opts []ccron.Option
opts = append(opts, ccron.WithTimezone(cfg.Cron.Timezone))

if cfg.Cron.Distributed {
    locker := clocker.New(redisx.Must(dao, cfg.Cron.LockRedis))
    opts = append(opts, ccron.WithLocker(locker, cfg.Cron.LockTTL))
}

scheduler := ccron.New(opts...)
```

## Cron 表达式参考（6 位含秒）

```
┌─────────── 秒 (0-59)
│ ┌─────────── 分 (0-59)
│ │ ┌─────────── 时 (0-23)
│ │ │ ┌─────────── 日 (1-31)
│ │ │ │ ┌─────────── 月 (1-12)
│ │ │ │ │ ┌─────────── 周 (0-6, 0=周日)
│ │ │ │ │ │
* * * * * *
```

| 表达式 | 含义 |
|---|---|
| `0 0 2 * * *` | 每天 02:00:00 |
| `0 30 9 * * 1-5` | 工作日 09:30:00 |
| `0 */5 * * * *` | 每 5 分钟（整分钟时执行）|
| `0 0 */6 * * *` | 每 6 小时整点执行 |
| `@hourly` | 每小时（等同于 `0 0 * * * *`）|

## 动态管理任务

```go
id, err := scheduler.Add("临时任务", "0 * * * * *", fn)

// 稍后移除
scheduler.Remove(id)
```
