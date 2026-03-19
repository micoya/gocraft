// Package kafka 提供基于 Kafka consumer group 的 PubSub 实现。
//
// 发布/订阅语义：
//   - topic → Kafka topic 名
//   - group → Kafka consumer group ID（不同 group 各自独立消费全量消息）
//   - consumer → Kafka reader 实例标识（informational）
//
// 特性：
//   - At-least-once 语义：handler 成功返回后才提交 offset
//   - 支持 deflate 压缩（WithCompress）
//   - 支持 W3C TraceContext 跨进程传播（WithTracing）
//   - Writer 按 topic 缓存，复用连接
package kafka

import (
	"bytes"
	"compress/flate"
	"context"
	"errors"
	"fmt"
	"io"
	"sync"

	kafkago "github.com/segmentio/kafka-go"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/micoya/gocraft/cpubsub"
)

const (
	headerTraceparent = "traceparent"
	headerTracestate  = "tracestate"
	headerCompress    = "x-compress"
	compressFlag      = "deflate"

	tracerName = "cpubsub/kafka"
)

// Option 配置 Kafka PubSub 的可选项。
type Option func(*options)

type options struct {
	compress bool
	tracing  bool
}

// WithCompress 启用 deflate 消息体压缩，适用于消息体较大的场景。
func WithCompress(on bool) Option {
	return func(o *options) { o.compress = on }
}

// WithTracing 启用 OTel 分布式追踪。
// 启用后 Publish 注入 W3C traceparent/tracestate 到消息 Header，
// Subscribe 自动提取并创建 consumer span，生命周期覆盖 handler 执行全程。
func WithTracing(on bool) Option {
	return func(o *options) { o.tracing = on }
}

type pubsub struct {
	brokers []string
	opts    options
	writers sync.Map // topic -> *kafkago.Writer
}

// New 基于 Kafka broker 地址列表创建 PubSub 实例。
// brokers 至少填一个地址，如 []string{"kafka:9092"}。
func New(brokers []string, opts ...Option) cpubsub.PubSub {
	o := options{}
	for _, opt := range opts {
		opt(&o)
	}
	return &pubsub{brokers: brokers, opts: o}
}

func (p *pubsub) writer(topic string) *kafkago.Writer {
	if w, ok := p.writers.Load(topic); ok {
		return w.(*kafkago.Writer)
	}
	w := &kafkago.Writer{
		Addr:     kafkago.TCP(p.brokers...),
		Topic:    topic,
		Balancer: &kafkago.LeastBytes{},
	}
	actual, loaded := p.writers.LoadOrStore(topic, w)
	if loaded {
		_ = w.Close()
	}
	return actual.(*kafkago.Writer)
}

func (p *pubsub) Publish(ctx context.Context, topic string, body string) (msgID string, err error) {
	if p.opts.tracing {
		var span trace.Span
		ctx, span = otel.Tracer(tracerName).Start(ctx, "publish "+topic,
			trace.WithSpanKind(trace.SpanKindProducer),
			trace.WithAttributes(
				attribute.String("messaging.system", "kafka"),
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
	}

	headers := make([]kafkago.Header, 0, 3)

	if p.opts.compress {
		compressed, cerr := deflateCompress([]byte(body))
		if cerr != nil {
			return "", fmt.Errorf("cpubsub/kafka: compress: %w", cerr)
		}
		body = string(compressed)
		headers = append(headers, kafkago.Header{Key: headerCompress, Value: []byte(compressFlag)})
	}

	if p.opts.tracing {
		carrier := make(headerCarrier, 0, 2)
		otel.GetTextMapPropagator().Inject(ctx, &carrier)
		for _, h := range carrier {
			headers = append(headers, h)
		}
	}

	msg := kafkago.Message{
		Value:   []byte(body),
		Headers: headers,
	}

	if err = p.writer(topic).WriteMessages(ctx, msg); err != nil {
		return "", fmt.Errorf("cpubsub/kafka: publish: %w", err)
	}
	return fmt.Sprintf("%s:?", topic), nil
}

func (p *pubsub) Subscribe(ctx context.Context, topic, group, consumer string, handler cpubsub.Handler) error {
	reader := kafkago.NewReader(kafkago.ReaderConfig{
		Brokers:  p.brokers,
		Topic:    topic,
		GroupID:  group,
		MinBytes: 1,
		MaxBytes: 10 << 20, // 10 MB
	})
	defer reader.Close()

	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		raw, err := reader.FetchMessage(ctx)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return ctx.Err()
			}
			return fmt.Errorf("cpubsub/kafka: fetch: %w", err)
		}

		if err = p.dispatch(ctx, topic, group, reader, raw, handler); err != nil {
			return err
		}
	}
}

func (p *pubsub) dispatch(ctx context.Context, topic, group string, reader *kafkago.Reader, raw kafkago.Message, handler cpubsub.Handler) (err error) {
	msgCtx := ctx

	if p.opts.tracing {
		carrier := make(headerCarrier, 0, len(raw.Headers))
		for _, h := range raw.Headers {
			carrier = append(carrier, h)
		}
		msgCtx = otel.GetTextMapPropagator().Extract(ctx, &carrier)

		var span trace.Span
		msgCtx, span = otel.Tracer(tracerName).Start(msgCtx, "process "+topic,
			trace.WithSpanKind(trace.SpanKindConsumer),
			trace.WithAttributes(
				attribute.String("messaging.system", "kafka"),
				attribute.String("messaging.destination.name", topic),
				attribute.String("messaging.consumer.group.name", group),
				attribute.Int64("messaging.kafka.offset", raw.Offset),
				attribute.Int("messaging.kafka.partition", raw.Partition),
				attribute.String("messaging.operation", "process"),
			),
		)
		defer func() {
			if err != nil {
				span.RecordError(err)
				span.SetStatus(codes.Error, err.Error())
			}
			span.End()
		}()
	}

	body, decErr := p.decodeBody(raw)
	if decErr != nil {
		return fmt.Errorf("cpubsub/kafka: decode: %w", decErr)
	}

	msg := cpubsub.Message{
		ID:    fmt.Sprintf("%s:%d:%d", raw.Topic, raw.Partition, raw.Offset),
		Topic: raw.Topic,
		Body:  body,
	}

	if err = handler(msgCtx, msg); err != nil {
		return err
	}

	if commitErr := reader.CommitMessages(ctx, raw); commitErr != nil {
		return fmt.Errorf("cpubsub/kafka: commit: %w", commitErr)
	}
	return nil
}

func (p *pubsub) decodeBody(raw kafkago.Message) (string, error) {
	body := string(raw.Value)
	for _, h := range raw.Headers {
		if h.Key == headerCompress && string(h.Value) == compressFlag {
			decompressed, err := deflateDecompress(raw.Value)
			if err != nil {
				return "", err
			}
			body = string(decompressed)
			break
		}
	}
	return body, nil
}

func (p *pubsub) Close(_ context.Context) error {
	var errs []error
	p.writers.Range(func(_, val any) bool {
		if err := val.(*kafkago.Writer).Close(); err != nil {
			errs = append(errs, err)
		}
		return true
	})
	return errors.Join(errs...)
}

// headerCarrier 将 []kafkago.Header 适配为 OTel TextMapCarrier。
type headerCarrier []kafkago.Header

func (c *headerCarrier) Get(key string) string {
	for _, h := range *c {
		if h.Key == key {
			return string(h.Value)
		}
	}
	return ""
}

func (c *headerCarrier) Set(key, val string) {
	for i, h := range *c {
		if h.Key == key {
			(*c)[i].Value = []byte(val)
			return
		}
	}
	*c = append(*c, kafkago.Header{Key: key, Value: []byte(val)})
}

func (c *headerCarrier) Keys() []string {
	keys := make([]string, len(*c))
	for i, h := range *c {
		keys[i] = h.Key
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
