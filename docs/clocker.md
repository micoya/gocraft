# 分布式锁（clocker）

`clocker` 提供基于 Redis 的分布式锁，适用于多实例部署时需要互斥执行的场景（如库存扣减、分布式幂等、防重复任务）。

## 核心机制

| 机制 | 实现 |
|---|---|
| 加锁 | `SET key token NX PX ttl`（原子操作） |
| 解锁 | Lua 脚本：验证 token 后才删除，防止误删他人锁 |
| 阻塞等待 | 指数退避重试，默认区间 [50ms, 500ms] |
| 自动续期 | Watchdog goroutine 每 TTL/3 刷新过期时间 |

## 安装

```go
import (
    "github.com/micoya/gocraft/clocker"
    "github.com/micoya/gocraft/cdao/redisx"
)

locker := clocker.New(redisx.Must(dao))
```

## 基本用法

### TryLock（非阻塞）

```go
lock, err := locker.TryLock(ctx, "order:pay:123", 30*time.Second)
if errors.Is(err, clocker.ErrLockNotAcquired) {
    // 锁已被其他实例持有，直接返回（幂等处理）
    return nil
}
if err != nil {
    return fmt.Errorf("获取锁失败: %w", err)
}
defer lock.Unlock(ctx) // 确保释放

// 执行临界区逻辑
return processPayment(ctx, orderID)
```

### Lock（阻塞等待）

```go
// 最多等待 10 秒
lockCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
defer cancel()

lock, err := locker.Lock(lockCtx, "inventory:sku:456", 30*time.Second)
if err != nil {
    return err // 超时或 ctx 取消
}
defer lock.Unlock(ctx)

return deductInventory(ctx, skuID, quantity)
```

## 配置选项

```go
locker := clocker.New(
    redisClient,
    clocker.WithKeyPrefix("myapp:lock:"),          // Redis key 前缀，默认 "clocker:"
    clocker.WithRetryInterval(50*time.Millisecond, 1*time.Second), // 重试退避区间
)
```

## 通过配置文件使用

在 `config.yaml` 中配置专用 Redis 实例（推荐与业务数据隔离）：

```yaml
dao:
  redis:
    default:
      addr: "127.0.0.1:6379"
    lock:
      addr: "127.0.0.1:6379"
      db: 1
```

```go
locker := clocker.New(redisx.Must(dao, "lock"))
```

## 与 ccron 配合实现分布式定时任务

```go
locker := clocker.New(redisx.Must(dao))
scheduler := ccron.New(
    ccron.WithLocker(locker, 5*time.Minute),
)
scheduler.Add("每日对账", "0 0 2 * * *", func(ctx context.Context) {
    reconcile(ctx)
})
```

## 注意事项

- `ttl` 应略大于业务执行预期最长时间（Watchdog 会自动续期，一般不需要太精确）
- 单 Redis 节点存在单点故障风险；生产高可用场景建议使用 Redis Cluster 或 Redlock 算法
- `Unlock` 是幂等的，锁已过期或不存在时静默返回
