package cpubsub

import "context"

// Message 表示一条 Pub/Sub 消息。
type Message struct {
	ID    string
	Topic string
	Body  string
}

// Handler 处理接收到的消息。返回 error 将终止订阅循环，
// 且该消息不会被确认（将在后续重新投递）。
type Handler func(ctx context.Context, msg Message) error

// PubSub 定义发布/订阅系统的核心抽象。
type PubSub interface {
	// Publish 向指定 topic 发送一条消息，返回由底层实现生成的消息 ID。
	Publish(ctx context.Context, topic string, body string) (string, error)
	// Subscribe 以消费组模式持续消费 topic 中的消息。
	// 方法阻塞直至 ctx 取消或 handler 返回 error。
	Subscribe(ctx context.Context, topic, group, consumer string, handler Handler) error
	// Close 释放实现持有的资源。不会关闭外部传入的连接。
	Close(ctx context.Context) error
}
