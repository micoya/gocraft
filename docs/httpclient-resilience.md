# HTTP 客户端韧性（重试 + 熔断）

`cdao/provider/httpclient` 已内置重试和熔断能力，通过配置文件按需开启，无需修改业务代码。

## 传输链结构

```
业务代码 → http.Client → 熔断器 → 重试 → OTel Trace → base Transport
```

- **熔断器**（最外层）：连续失败达阈值时拒绝请求，保护下游服务
- **重试**（中间层）：对幂等请求和网络错误自动重试，指数退避 + 抖动
- **OTel**（最内层）：每次实际发出的 HTTP 请求均被 trace，包含重试的每次尝试

## 配置

在 `config.yaml` 中为对应 HTTP 客户端添加 `retry` 和/或 `circuit_breaker` 块：

```yaml
dao:
  http_client:
    payment:
      base_url: "https://payment.internal"
      timeout: 10s

      # 重试配置（不填则不重试）
      retry:
        max_attempts: 3      # 最多重试 3 次（不含首次），共 4 次尝试
        wait_min: 100ms      # 首次重试前最小等待
        wait_max: 2s         # 退避上限

      # 熔断器配置（不填则不启用）
      circuit_breaker:
        max_requests: 1      # 半开状态探测请求数
        interval: 60s        # 统计窗口
        timeout: 30s         # 熔断持续时间
        threshold: 5         # 连续失败次数触发熔断
```

## 重试策略

| 情况 | 幂等方法（GET/HEAD/PUT/DELETE/OPTIONS）| 非幂等方法（POST/PATCH）|
|---|---|---|
| 网络错误（连接失败、超时）| ✅ 重试 | ✅ 重试（未到达服务端）|
| 5xx 响应 | ✅ 重试 | ❌ 不重试（可能已执行）|
| 4xx 响应 | ❌ 不重试 | ❌ 不重试 |

退避时间使用指数增长 + ±20% 随机抖动，防止多实例同时重试形成惊群：

```
第 1 次重试等待: ~100ms
第 2 次重试等待: ~200ms
第 3 次重试等待: ~400ms（上限为 wait_max）
```

## 熔断器状态机

```
Closed（正常）→ 连续失败 >= threshold → Open（熔断，拒绝所有请求）
Open → 经过 timeout 时间 → HalfOpen（探测）
HalfOpen → 探测成功 → Closed
HalfOpen → 探测失败 → Open
```

## 业务代码使用

业务代码无感知，直接使用 `*http.Client` 即可：

```go
client := httpclientx.Must(dao, "payment")

resp, err := client.Do(req)
if err != nil {
    // 包含熔断错误: "httpclient: circuit breaker: circuit breaker is open"
    return cerror.Wrap(cerror.CodeUnavailable, "支付服务不可用", err)
}
```

## 最佳实践

1. **幂等接口**（查询、状态确认）：开启重试，max_attempts=3
2. **非幂等但有幂等设计**（带唯一 requestID 的下单）：可开启重试，服务端通过 requestID 去重
3. **纯非幂等操作**（扣款）：不建议配置重试，依赖熔断保护
4. **内部服务调用**：推荐同时启用重试和熔断
5. **第三方 API**：根据对方 SLA 决定是否重试，避免超出 rate limit
