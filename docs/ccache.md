# 多级缓存（ccache）

`ccache` 提供统一的缓存抽象接口与三种实现：Redis、内存（ristretto）和两级联合缓存（L1 内存 + L2 Redis）。

## 实现对比

| 实现 | 特点 | 适用场景 |
|---|---|---|
| `RedisCache` | 持久化、跨进程共享、TTL 精确 | 分布式缓存、会话、共享热点数据 |
| `MemoryCache` | 零延迟、进程内、基于 ristretto | 高频读取、本地计算结果缓存 |
| `LayeredCache` | L1 内存 miss 时查 L2 Redis 并回填 | 两全其美，降低 Redis 压力 |

所有实现缓存值类型为 `string`，业务层自行 JSON 序列化/反序列化。

## 快速开始

### 单级 Redis 缓存

```go
import (
    "github.com/micoya/gocraft/ccache"
    "github.com/micoya/gocraft/cdao/redisx"
)

cache := ccache.NewRedis(
    redisx.Must(dao),
    ccache.WithRedisDefaultTTL(time.Hour),
    ccache.WithRedisKeyPrefix("myapp:"),
)
```

### 单级内存缓存

```go
// 方式一：从 DAO 中取已有的 ristretto 实例
ristrettoCache := cdao.Must[*ristretto.Cache[string, any]](dao, "mcache", "default")
// 注意：ccache.MemoryCache 使用 Cache[string, string]，需要单独创建

// 方式二：直接创建
cache, err := ccache.NewMemoryFromConfig(
    10_000_000,   // numCounters（预期条目数的 10 倍）
    512<<20,      // maxCost（512MB）
    ccache.WithMemoryDefaultTTL(5*time.Minute),
)
```

### 两级缓存（推荐生产环境）

```go
l1, _ := ccache.NewMemoryFromConfig(10_000_000, 512<<20,
    ccache.WithMemoryDefaultTTL(5*time.Minute),
)
l2 := ccache.NewRedis(redisx.Must(dao),
    ccache.WithRedisDefaultTTL(time.Hour),
)
cache := ccache.NewLayered(l1, l2,
    ccache.WithL1TTL(5*time.Minute), // L1 回填时使用较短 TTL，避免内存数据过旧
)
```

## 接口说明

```go
// 写入（ttl 可选，不传使用默认值）
cache.Set(ctx, "user:1", `{"name":"alice"}`, ccache.TTL(time.Hour))

// 读取（found=false 表示 key 不存在）
val, found, err := cache.Get(ctx, "user:1")

// 删除
cache.Del(ctx, "user:1", "user:2")

// 读取或生成（最常用，防缓存穿透）
val, err := cache.GetOrSet(ctx, "user:1",
    func(ctx context.Context) (string, error) {
        user, err := userRepo.Find(ctx, 1)
        if err != nil {
            return "", err
        }
        b, _ := json.Marshal(user)
        return string(b), nil
    },
    ccache.TTL(time.Hour),
)
```

## 完整业务示例

```go
type UserService struct {
    cache ccache.Cache
    repo  UserRepository
}

func (s *UserService) GetUser(ctx context.Context, id int64) (*User, error) {
    key := fmt.Sprintf("user:%d", id)

    val, err := s.cache.GetOrSet(ctx, key, func(ctx context.Context) (string, error) {
        user, err := s.repo.Find(ctx, id)
        if err != nil {
            return "", err
        }
        b, _ := json.Marshal(user)
        return string(b), nil
    }, ccache.TTL(time.Hour))

    if err != nil {
        return nil, err
    }

    var user User
    json.Unmarshal([]byte(val), &user)
    return &user, nil
}

func (s *UserService) UpdateUser(ctx context.Context, user *User) error {
    if err := s.repo.Update(ctx, user); err != nil {
        return err
    }
    // 更新后删除缓存，下次读取时重新加载
    return s.cache.Del(ctx, fmt.Sprintf("user:%d", user.ID))
}
```
