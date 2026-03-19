# 统一错误处理（cerror）

`cerror` 提供业务错误码体系，解决 Go 原生 `error` 接口缺乏结构化信息、HTTP 层无法自动映射状态码的问题。

## 核心概念

| 概念 | 说明 |
|---|---|
| `cerror.Code` | 业务错误码，与 HTTP 状态码语义对齐（400/401/403/404/409/429/500/503）|
| `*cerror.Error` | 携带 `code + message + cause` 的结构化错误，支持 `errors.Is/As` 链式操作 |

## 预定义错误码

```go
cerror.CodeBadRequest     // 400 - 参数错误
cerror.CodeUnauthorized   // 401 - 未登录
cerror.CodeForbidden      // 403 - 无权限
cerror.CodeNotFound       // 404 - 资源不存在
cerror.CodeConflict       // 409 - 资源冲突
cerror.CodeTooManyRequests // 429 - 频率限制
cerror.CodeInternal       // 500 - 内部错误
cerror.CodeUnavailable    // 503 - 服务不可用
```

## 基本用法

### 创建错误

```go
// 使用预定义变量（最常见）
return cerror.ErrNotFound

// 自定义消息
return cerror.New(cerror.CodeNotFound, "用户不存在")

// 格式化消息
return cerror.Newf(cerror.CodeNotFound, "用户 %d 不存在", userID)

// 包装底层错误（保留原始错误链）
return cerror.Wrap(cerror.CodeInternal, "查询用户失败", err)
return cerror.Wrapf(cerror.CodeInternal, err, "查询用户 %d 失败", userID)
```

### 错误判断

```go
// 检查是否为指定 code
if cerror.IsCode(err, cerror.CodeNotFound) {
    // ...
}

// 提取 *cerror.Error（用于获取 code 和 message）
if ce, ok := cerror.FromError(err); ok {
    log.Printf("code=%d, message=%s", ce.Code(), ce.Message())
}

// 标准 errors.As 兼容
var ce *cerror.Error
if errors.As(err, &ce) {
    httpStatus := cerror.HTTPStatus(ce.Code())
}
```

### 在 gin handler 中统一响应

推荐在项目内封装统一的响应中间件：

```go
// 在你的 handler 包中定义响应辅助函数
func Respond(c *gin.Context, data any, err error) {
    if err == nil {
        c.JSON(200, gin.H{"code": 0, "data": data})
        return
    }
    ce, ok := cerror.FromError(err)
    if !ok {
        ce = cerror.Wrap(cerror.CodeInternal, "internal server error", err)
    }
    c.JSON(cerror.HTTPStatus(ce.Code()), gin.H{
        "code":    ce.Code(),
        "message": ce.Message(),
    })
}

// handler 中直接返回 cerror
func GetUser(c *gin.Context) {
    user, err := userSvc.Find(c.Request.Context(), id)
    if err != nil {
        Respond(c, nil, err)
        return
    }
    Respond(c, user, nil)
}
```

### 自定义业务错误码

推荐在业务包内统一定义：

```go
// internal/errors/errors.go
const (
    CodeOrderNotPaid   cerror.Code = 10001
    CodeStockInsufficient cerror.Code = 10002
)

var (
    ErrOrderNotPaid      = cerror.New(CodeOrderNotPaid, "订单未支付")
    ErrStockInsufficient = cerror.New(CodeStockInsufficient, "库存不足")
)
```

## 错误链传播

`cerror.Error` 实现了 `Unwrap()`，与标准库完全兼容：

```go
// service 层
err := repo.FindUser(ctx, id)
if errors.Is(err, sql.ErrNoRows) {
    return cerror.Wrap(cerror.CodeNotFound, "用户不存在", err)
}

// handler 层
if cerror.IsCode(err, cerror.CodeNotFound) {
    // err 链中任意位置含 CodeNotFound 均可检测到
}
```
