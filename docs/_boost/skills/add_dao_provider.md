---
name: cdao-provider
description: Guide for adding new resource providers to the cdao package. Use when creating a new cdao provider (e.g. MongoDB, Kafka, Elasticsearch), adding typed helpers, or modifying DAO config structures.
---

# 添加 DAO Provider

## 架构概览

cdao 采用三层分离，新增 provider 必须严格遵循：

| 层 | 路径 | 第三方依赖 | 职责 |
|---|---|---|---|
| core | `cdao/` | 禁止 | 容器、生命周期、Registry |
| provider | `cdao/provider/<kind>/` | 允许 | 实现 `cdao.Provider` 接口 |
| typed helper | `cdao/<kind>x/` | 允许 | 封装 `Must(d, name)` 返回强类型 |

## 添加步骤

以添加 MongoDB 为例（替换为实际资源名称）：

### Step 1: config — 添加配置结构体

在 `config/config.go` 中添加配置类型，并在 `DAOConfig` 中新增字段：

```go
type MongoConfig struct {
    URI      string `mapstructure:"uri"`
    Database string `mapstructure:"database"`
}

type DAOConfig struct {
    Database map[string]DBConfig      `mapstructure:"database"`
    Redis    map[string]RedisConfig   `mapstructure:"redis"`
    Mongo    map[string]MongoConfig   `mapstructure:"mongo"`     // 新增
}
```

同步更新 `config/config.example.yaml` 中的注释示例。

### Step 2: cdao core — NewFromConfig 添加遍历

在 `cdao/dao.go` 的 `NewFromConfig` 中添加一段遍历（kind 名称必须与 provider 注册名一致）：

```go
for name, c := range cfg.Mongo {
    if err := d.add("mongo", name, c); err != nil {
        return nil, err
    }
}
```

**禁止在 cdao/ 下引入任何第三方 import**，core 只依赖标准库和 `config` 包。

### Step 3: provider — 实现 cdao.Provider 接口

创建 `cdao/provider/mongo/mongo.go`：

```go
package mongo

import (
    "github.com/micoya/gocraft/config"
    "github.com/micoya/gocraft/cdao"
)

func init() {
    cdao.Register("mongo", factory)
}

func factory(_ string, raw any) (cdao.Provider, error) {
    cfg, ok := raw.(config.MongoConfig)
    // ... 校验 + 返回 &provider{cfg: cfg}
}
```

Provider 必须实现 4 个方法：

| 方法 | 要求 |
|---|---|
| `Init(ctx)` | 建立连接 + Ping 验证连通性 |
| `Close(ctx)` | 释放连接，nil-safe |
| `Health(ctx)` | 执行 Ping |
| `Instance()` | 返回底层客户端（如 `*mongo.Client`） |

### Step 4: typed helper — 提供 Must 快捷方法

创建 `cdao/mongox/mongox.go`：

```go
package mongox

import (
    "go.mongodb.org/mongo-driver/mongo"
    "github.com/micoya/gocraft/cdao"
)

func Must(d *cdao.DAO, name string) *mongo.Client {
    return cdao.Must[*mongo.Client](d, "mongo", name)
}
```

### Step 5: go get + go mod tidy

添加第三方依赖后执行 `go get <pkg>` 和 `go mod tidy`。

### Step 6: 测试

- `config/config_test.go`：添加包含新资源的 yaml 解析测试
- `cdao/dao_test.go`：用 `registerMock(t, "<kind>", ...)` 测试 NewFromConfig 路径
- provider 无需集成测试（需要真实服务），确保 `go vet` 通过即可

## 约束清单

- [ ] `cdao/dao.go` 和 `cdao/provider.go` 不得 import 任何第三方包
- [ ] provider 通过 `init()` 调用 `cdao.Register` 自注册，kind 名称全局唯一
- [ ] factory 接收 `any` 后必须 type-assert 为对应的 config 类型，失败时返回清晰错误
- [ ] `Init` 中必须 Ping 验证连通性（fail-fast）
- [ ] `Close` 必须 nil-safe（未初始化时调用不 panic）
- [ ] typed helper 的 `Must` 隐藏 kind 参数，签名统一为 `Must(d *cdao.DAO, name string) *T`
- [ ] 使用侧通过 blank import 引入 provider：`import _ ".../cdao/provider/<kind>"`

## 现有 Provider 参考

| kind | provider 路径 | helper | 客户端类型 |
|---|---|---|---|
| `database` | `cdao/provider/database/` | `cdao/gormx/` | `*gorm.DB` |
| `redis` | `cdao/provider/redis/` | `cdao/redisx/` | `*redis.Client` |
