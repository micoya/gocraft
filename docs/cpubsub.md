# 发布订阅（cpubsub）

`cpubsub` 提供统一的发布/订阅抽象接口，目前有三种实现：Redis Streams、Kafka、RabbitMQ。

## 接口说明

```go
type PubSub interface {
    Publish(ctx context.Context, topic string, body string) (msgID string, error)
    Subscribe(ctx context.Context, topic, group, consumer string, handler Handler) error
    Close(ctx context.Context) error
}

type Handler func(ctx context.Context, msg Message) error
```

所有实现均为 **At-least-once** 语义：handler 成功返回后才确认消息，失败时消息会被重新投递。

## 各实现对比

| 特性 | Redis Streams | Kafka | RabbitMQ |
|---|---|---|---|
| 消息持久化 | 有（TTL 过期） | 有（按 retention 策略）| 有（持久化队列）|
| 不同 group 独立消费 | ✅ | ✅ | ✅ |
| 消息顺序保证 | ✅（单分区）| ✅（单分区）| ✅（单队列）|
| 多分区/并行消费 | ❌ | ✅ | ✅（多消费者）|

## Redis Streams 实现

```go
import (
    "github.com/micoya/gocraft/cpubsub/provider/redis"
    "github.com/micoya/gocraft/cdao/redisx"
)

ps := redis.New(
    redisx.Must(dao),
    redis.WithCompress(true),
    redis.WithTracing(true),
    redis.WithTTL(7*24*time.Hour),
    redis.WithPrefix("events:"),
)
```

## Kafka 实现

```go
import "github.com/micoya/gocraft/cpubsub/provider/kafka"

// brokers 从 DAO 的 kafka 配置中取（或直接硬编码）
ps := kafka.New(
    []string{"kafka:9092"},
    kafka.WithCompress(true),
    kafka.WithTracing(true),
)
```

## RabbitMQ 实现

```go
import (
    "github.com/micoya/gocraft/cpubsub/provider/rabbitmq"
    "github.com/micoya/gocraft/cdao/rabbitmqx"
)

ps := rabbitmq.New(
    rabbitmqx.Must(dao),
    rabbitmq.WithCompress(true),
    rabbitmq.WithTracing(true),
)
```

## 发布消息

```go
msgID, err := ps.Publish(ctx, "order.created", `{"order_id":123,"amount":99.9}`)
```

## 订阅消息

```go
// 通常在独立 goroutine 中运行，Subscribe 会阻塞
go func() {
    err := ps.Subscribe(ctx, "order.created", "payment-service", "payment-1",
        func(ctx context.Context, msg cpubsub.Message) error {
            var event OrderCreatedEvent
            if err := json.Unmarshal([]byte(msg.Body), &event); err != nil {
                return err // 返回 error 将重新投递
            }
            return paymentSvc.Process(ctx, event)
        },
    )
    if err != nil && !errors.Is(err, context.Canceled) {
        slog.Error("订阅失败", "error", err)
    }
}()
```

## 多 group 独立消费同一 topic

```go
// 支付服务订阅
ps.Subscribe(ctx, "order.created", "payment-service", "payment-1", payHandler)

// 通知服务订阅同一 topic
ps.Subscribe(ctx, "order.created", "notify-service", "notify-1", notifyHandler)
```

两个 group 各自独立消费，互不影响，每个消息两个 group 均会收到。

## 消息体建议

推荐使用 JSON 格式，便于后续扩展：

```go
type OrderCreatedEvent struct {
    OrderID   int64   `json:"order_id"`
    UserID    int64   `json:"user_id"`
    Amount    float64 `json:"amount"`
    CreatedAt int64   `json:"created_at"`
}

body, _ := json.Marshal(event)
ps.Publish(ctx, "order.created", string(body))
```
