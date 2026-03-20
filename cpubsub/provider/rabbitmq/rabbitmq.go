// Package rabbitmq 提供基于 RabbitMQ fanout exchange 的 PubSub 实现。
//
// 发布/订阅语义：
//   - topic → fanout exchange 名（每个 topic 对应一个独立 exchange）
//   - group → 持久化队列名前缀，完整队列名为 "<topic>.<group>"
//   - consumer → AMQP consumer tag
//
// 特性：
//   - fanout exchange 保证所有不同 group（队列）各自收到完整消息副本
//   - At-least-once 语义：handler 成功返回后才 ack 消息
//   - 支持 deflate 消息体压缩（WithCompress）
//   - 始终启用 W3C TraceContext 跨进程传播：Publish 注入 traceparent/tracestate，
//     Subscribe 提取后以 Link 关联 producer span（符合 OTel Messaging Semantic Conventions）。
//     未配置 TracerProvider 时全局默认为 noop，几乎零开销。
package rabbitmq

import (
	"bytes"
	"compress/flate"
	"context"
	"fmt"
	"io"

	amqp "github.com/rabbitmq/amqp091-go"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/micoya/gocraft/cpubsub"
)

const (
	headerCompress = "x-compress"
	compressFlag   = "deflate"

	tracerName = "cpubsub/rabbitmq"
)

// Option 配置 RabbitMQ PubSub 的可选项。
type Option func(*options)

type options struct {
	compress bool
}

// WithCompress 启用 deflate 消息体压缩，适用于消息体较大的场景。
func WithCompress(on bool) Option {
	return func(o *options) { o.compress = on }
}

type pubsub struct {
	conn *amqp.Connection
	opts options
}

// New 基于已有的 *amqp.Connection 创建 PubSub 实例。
// conn 的生命周期由调用方管理，Close 不会关闭连接。
func New(conn *amqp.Connection, opts ...Option) cpubsub.PubSub {
	o := options{}
	for _, opt := range opts {
		opt(&o)
	}
	return &pubsub{conn: conn, opts: o}
}

func (p *pubsub) channel() (*amqp.Channel, error) {
	ch, err := p.conn.Channel()
	if err != nil {
		return nil, fmt.Errorf("cpubsub/rabbitmq: open channel: %w", err)
	}
	return ch, nil
}

// declareExchange 声明 fanout exchange（幂等，已存在不报错）。
func (p *pubsub) declareExchange(ch *amqp.Channel, topic string) error {
	return ch.ExchangeDeclare(
		topic,
		"fanout",
		true,  // durable
		false, // auto-delete
		false, // internal
		false, // no-wait
		nil,
	)
}

func (p *pubsub) Publish(ctx context.Context, topic string, body string) (msgID string, err error) {
	ctx, span := otel.Tracer(tracerName).Start(ctx, "publish "+topic,
		trace.WithSpanKind(trace.SpanKindProducer),
		trace.WithAttributes(
			attribute.String("messaging.system", "rabbitmq"),
			attribute.String("messaging.destination.name", topic),
			attribute.String("messaging.operation", "publish"),
		),
	)
	defer func() {
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		}
		span.End()
	}()

	ch, err := p.channel()
	if err != nil {
		return "", err
	}
	defer ch.Close()

	if err = p.declareExchange(ch, topic); err != nil {
		return "", fmt.Errorf("cpubsub/rabbitmq: declare exchange: %w", err)
	}

	headers := amqp.Table{}

	if p.opts.compress {
		compressed, cerr := deflateCompress([]byte(body))
		if cerr != nil {
			return "", fmt.Errorf("cpubsub/rabbitmq: compress: %w", cerr)
		}
		body = string(compressed)
		headers[headerCompress] = compressFlag
	}

	// 始终注入 trace context，消费端凭此建立 link 串联 trace。
	// 未配置 TracerProvider 时 propagator 为 noop，写入为空操作。
	otel.GetTextMapPropagator().Inject(ctx, &amqpCarrier{t: headers})

	publishing := amqp.Publishing{
		ContentType:  "text/plain",
		DeliveryMode: amqp.Persistent,
		Body:         []byte(body),
		Headers:      headers,
	}

	if err = ch.PublishWithContext(ctx, topic, "", false, false, publishing); err != nil {
		return "", fmt.Errorf("cpubsub/rabbitmq: publish: %w", err)
	}
	return "", nil
}

func (p *pubsub) Subscribe(ctx context.Context, topic, group, consumer string, handler cpubsub.Handler) error {
	ch, err := p.channel()
	if err != nil {
		return err
	}
	defer ch.Close()

	if err = p.declareExchange(ch, topic); err != nil {
		return fmt.Errorf("cpubsub/rabbitmq: declare exchange: %w", err)
	}

	queueName := topic + "." + group
	q, err := ch.QueueDeclare(
		queueName,
		true,  // durable
		false, // auto-delete
		false, // exclusive
		false, // no-wait
		nil,
	)
	if err != nil {
		return fmt.Errorf("cpubsub/rabbitmq: declare queue: %w", err)
	}

	if err = ch.QueueBind(q.Name, "", topic, false, nil); err != nil {
		return fmt.Errorf("cpubsub/rabbitmq: queue bind: %w", err)
	}

	deliveries, err := ch.ConsumeWithContext(ctx, q.Name, consumer, false, false, false, false, nil)
	if err != nil {
		return fmt.Errorf("cpubsub/rabbitmq: consume: %w", err)
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case d, ok := <-deliveries:
			if !ok {
				return fmt.Errorf("cpubsub/rabbitmq: delivery channel closed")
			}
			if err = p.dispatch(ctx, topic, group, d, handler); err != nil {
				return err
			}
		}
	}
}

func (p *pubsub) dispatch(ctx context.Context, topic, group string, d amqp.Delivery, handler cpubsub.Handler) (err error) {
	// 从消息头提取 producer 的 span context，通过 Link 关联而非 parent-child。
	// OTel Messaging Semantic Conventions：异步 pub/sub 的 consumer span 应与
	// producer span 建立 Link，两者各自隶属独立的 trace，避免跨进程合并 trace tree。
	producerSpanCtx := trace.SpanContextFromContext(
		otel.GetTextMapPropagator().Extract(ctx, &amqpCarrier{t: d.Headers}),
	)

	spanOpts := []trace.SpanStartOption{
		trace.WithSpanKind(trace.SpanKindConsumer),
		trace.WithAttributes(
			attribute.String("messaging.system", "rabbitmq"),
			attribute.String("messaging.destination.name", topic),
			attribute.String("messaging.consumer.group.name", group),
			attribute.String("messaging.message.id", d.MessageId),
			attribute.String("messaging.operation", "process"),
		),
	}
	if producerSpanCtx.IsValid() {
		spanOpts = append(spanOpts, trace.WithLinks(trace.Link{SpanContext: producerSpanCtx}))
	}

	msgCtx, span := otel.Tracer(tracerName).Start(ctx, "process "+topic, spanOpts...)
	defer func() {
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		}
		span.End()
	}()

	body, decErr := p.decodeBody(d)
	if decErr != nil {
		_ = d.Nack(false, false)
		return fmt.Errorf("cpubsub/rabbitmq: decode: %w", decErr)
	}

	msg := cpubsub.Message{
		ID:    d.MessageId,
		Topic: topic,
		Body:  body,
	}

	if err = handler(msgCtx, msg); err != nil {
		_ = d.Nack(false, true) // requeue
		return err
	}

	if ackErr := d.Ack(false); ackErr != nil {
		return fmt.Errorf("cpubsub/rabbitmq: ack: %w", ackErr)
	}
	return nil
}

func (p *pubsub) decodeBody(d amqp.Delivery) (string, error) {
	body := d.Body
	if v, ok := d.Headers[headerCompress]; ok && v == compressFlag {
		decompressed, err := deflateDecompress(body)
		if err != nil {
			return "", err
		}
		return string(decompressed), nil
	}
	return string(body), nil
}

func (p *pubsub) Close(_ context.Context) error {
	return nil
}

// amqpCarrier 将 amqp.Table 适配为 OTel TextMapCarrier。
type amqpCarrier struct {
	t amqp.Table
}

func (c *amqpCarrier) Get(key string) string {
	if v, ok := c.t[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func (c *amqpCarrier) Set(key, val string) {
	c.t[key] = val
}

func (c *amqpCarrier) Keys() []string {
	keys := make([]string, 0, len(c.t))
	for k := range c.t {
		keys = append(keys, k)
	}
	return keys
}

func deflateCompress(data []byte) ([]byte, error) {
	var buf bytes.Buffer
	w, err := flate.NewWriter(&buf, flate.BestSpeed)
	if err != nil {
		return nil, err
	}
	if _, err = w.Write(data); err != nil {
		return nil, err
	}
	if err = w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func deflateDecompress(data []byte) ([]byte, error) {
	return io.ReadAll(flate.NewReader(bytes.NewReader(data)))
}
