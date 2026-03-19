# 分页工具（cpager）

`cpager` 提供 offset-based 分页的参数解析、归一化和泛型结果类型，深度集成 GORM。

## 核心类型

```go
type Page struct { ... }  // 分页请求参数（page 从 1 开始）

type Result[T any] struct {
    Items      []T   `json:"items"`
    Total      int64 `json:"total"`
    Page       int   `json:"page"`
    PageSize   int   `json:"page_size"`
    TotalPages int   `json:"total_pages"`
    HasNext    bool  `json:"has_next"`
    HasPrev    bool  `json:"has_prev"`
}
```

## 参数归一化规则

| 输入 | 归一化结果 |
|---|---|
| `page <= 0` | 自动设为 1 |
| `page_size <= 0` | 自动设为 20（默认值）|
| `page_size > 100` | 自动截断为 100（最大值）|
| 空字符串 / 非数字 | 使用默认值 |

## 基本用法

### 从 gin 请求中解析

```go
func ListUsers(c *gin.Context) {
    page := cpager.New(c.Query("page"), c.Query("page_size"))

    result, err := cpager.Paginate[UserVO](
        db.Model(&User{}).Where("status = ?", 1).Order("created_at DESC"),
        page,
    )
    if err != nil {
        // 处理错误
        return
    }

    c.JSON(200, result)
}
```

**响应示例：**

```json
{
  "items": [...],
  "total": 156,
  "page": 2,
  "page_size": 20,
  "total_pages": 8,
  "has_next": true,
  "has_prev": true
}
```

### 从整数创建

```go
page := cpager.Of(req.Page, req.PageSize)
```

### 手动使用 Scope（分步查询）

```go
page := cpager.Of(1, 20)

// 仅应用分页，不查 count
var items []User
db.Scopes(page.Scope).Find(&items)

// 单独查 count
var total int64
db.Model(&User{}).Where("status = ?", 1).Count(&total)
```

### 提前返回空结果

```go
if someCondition {
    return cpager.Empty[UserVO](page), nil
}
```

## 配合 Service 层封装

```go
type UserService struct {
    db *gorm.DB
}

func (s *UserService) ListActive(ctx context.Context, page cpager.Page) (*cpager.Result[UserVO], error) {
    return cpager.Paginate[UserVO](
        s.db.WithContext(ctx).
            Model(&User{}).
            Where("deleted_at IS NULL").
            Order("id DESC"),
        page,
    )
}
```

## 注意事项

- `Paginate` 使用 `Session(&gorm.Session{NewDB: true})` 执行 COUNT，避免影响原始查询的 SELECT 子句
- 返回的 `Items` 在无数据时为空切片 `[]T{}`（非 nil），JSON 序列化为 `[]` 而非 `null`
- `TotalPages` 最小值为 1（即使 total=0）
