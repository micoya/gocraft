package cdao

import "context"

// Provider 是具体资源驱动必须实现的接口。
type Provider interface {
	// Init 建立连接。实现应自行保证幂等。
	Init(ctx context.Context) error
	// Close 关闭连接并释放资源。
	Close(ctx context.Context) error
	// Health 执行健康检查（如 Ping）。
	Health(ctx context.Context) error
	// Instance 返回底层客户端实例（如 *redis.Client、*gorm.DB）。
	Instance() any
}

// Factory 根据资源名称和原始配置创建未初始化的 Provider。
type Factory func(name string, raw any) (Provider, error)

var factories = make(map[string]Factory)

// Register 为指定资源类型注册 Factory。通常在 provider 包的 init() 中调用。
// 重复注册同一 kind 会 panic。
func Register(kind string, f Factory) {
	if _, dup := factories[kind]; dup {
		panic("dao: Register called twice for kind " + kind)
	}
	factories[kind] = f
}
