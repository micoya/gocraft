// Package redis 提供基于 Redis Stream 的 PubSub 实现。
//
// 发布/订阅语义：
//   - topic → Stream KEY（加前缀，默认 "channel:<topic>"）
//   - group → Consumer Group 名
//   - consumer → Consumer 名
//
// 特性：
//   - At-least-once 语义：handler 成功返回后才 XACK
//   - 启动时先消费 pending 消息，处理完后再消费新消息
//   - 支持 deflate 压缩（WithCompress）
//   - 始终启用 W3C TraceContext 跨进程传播：Publish 注入 traceparent/tracestate 到消息字段，
//     Subscribe 提取后以 Link 关联 producer span（符合 OTel Messaging Semantic Conventions）。
//     未配置 TracerProvider 时全局默认为 noop，几乎零开销。
package redis

import (
	"bytes"
	"compress/flate"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	goredis "github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/micoya/gocraft/cpubsub"
)

const (
	defaultPrefix = "channel:"
	defaultTTL    = 7 * 24 * time.Hour

	fieldBody     = "b"
	fieldCompress = "c"
	compressFlag  = "1"

	tracerName = "cpubsub/redis"
)

// Option 配置 Redis Stream PubSub 的可选项。
type Option func(*options)

type options struct {
	prefix   string
	ttl      time.Duration
	compress bool
}

// WithPrefix 设置 Stream KEY 前缀，默认 "channel:"。
func WithPrefix(prefix string) Option {
	return func(o *options) { o.prefix = prefix }
}

// WithTTL 设置 Stream KEY 的过期时间，默认 7 天。0 表示不过期。
func WithTTL(ttl time.Duration) Option {
	return func(o *options) { o.ttl = ttl }
}

// WithCompress 启用 deflate 压缩。适用于消息体较大的场景。
func WithCompress(on bool) Option {
	return func(o *options) { o.compress = on }
}

// streamCarrier 将 Redis Stream XMessage values 适配为 OTel TextMapCarrier，
// 让 propagator 直接操作消息字段，无需手动映射 header 名。
type streamCarrier struct{ values map[string]any }

func (c streamCarrier) Get(key string) string {
	if v, ok := c.values[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}
func (c streamCarrier) Set(key, val string)  { c.values[key] = val }
func (c streamCarrier) Keys() []string {
	keys := make([]string, 0, len(c.values))
	for k := range c.values {
		keys = append(keys, k)
	}
	return keys
}

type pubsub struct {
	client *goredis.Client
	opts   options
}

// New 基于已有的 *redis.Client 创建 PubSub 实例。
// client 的生命周期由调用方管理，Close 不会关闭该连接。
func New(client *goredis.Client, opts ...Option) cpubsub.PubSub {
	o := options{
		prefix: defaultPrefix,
		ttl:    defaultTTL,
	}
	for _, opt := range opts {
		opt(&o)
	}
	return &pubsub{client: client, opts: o}
}

func (p *pubsub) key(topic string) string {
	return p.opts.prefix + topic
}

func (p *pubsub) Publish(ctx context.Context, topic string, body string) (msgID string, err error) {
	ctx, span := otel.Tracer(tracerName).Start(ctx, "publish "+topic,
		trace.WithSpanKind(trace.SpanKindProducer),
		trace.WithAttributes(
			attribute.String("messaging.system", "redis"),
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

	key := p.key(topic)
	values := map[string]any{fieldBody: body}

	if p.opts.compress {
		compressed, err := deflateCompress([]byte(body))
		if err != nil {
			return "", fmt.Errorf("cpubsub/redis: compress: %w", err)
		}
		values[fieldBody] = compressed
		values[fieldCompress] = compressFlag
	}

	// 始终注入 trace context，消费端凭此建立 link 串联 trace。
	// 未配置 TracerProvider 时 propagator 为 noop，写入为空操作。
	otel.GetTextMapPropagator().Inject(ctx, streamCarrier{values})

	pipe := p.client.Pipeline()
	xadd := pipe.XAdd(ctx, &goredis.XAddArgs{
		Stream: key,
		Values: values,
		ID:     "*",
	})
	if p.opts.ttl > 0 {
		pipe.Expire(ctx, key, p.opts.ttl)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return "", fmt.Errorf("cpubsub/redis: publish: %w", err)
	}
	return xadd.Val(), nil
}

func (p *pubsub) Subscribe(ctx context.Context, topic, group, consumer string, handler cpubsub.Handler) error {
	key := p.key(topic)

	if err := p.client.XGroupCreateMkStream(ctx, key, group, "0").Err(); err != nil {
		if !strings.Contains(err.Error(), "BUSYGROUP") {
			return fmt.Errorf("cpubsub/redis: xgroup create: %w", err)
		}
	}

	// 先消费该 consumer 的 pending 消息，全部处理完毕后切换到新消息
	startID := "0-0"

	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		streams, err := p.client.XReadGroup(ctx, &goredis.XReadGroupArgs{
			Group:    group,
			Consumer: consumer,
			Streams:  []string{key, startID},
			Count:    10,
			Block:    2 * time.Second,
		}).Result()
		if err != nil {
			if errors.Is(err, goredis.Nil) {
				if startID != ">" {
					startID = ">"
				}
				continue
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("cpubsub/redis: xreadgroup: %w", err)
		}

		for _, stream := range streams {
			if len(stream.Messages) == 0 {
				if startID != ">" {
					startID = ">"
				}
				continue
			}
			for _, raw := range stream.Messages {
				msg, err := p.decodeMessage(topic, raw)
				if err != nil {
					return fmt.Errorf("cpubsub/redis: decode: %w", err)
				}
				if err := p.dispatch(ctx, topic, group, key, raw, msg, handler); err != nil {
					return err
				}
			}
		}
	}
}

// dispatch 处理单条消息：提取 producer span context，通过 Link 关联后创建 consumer span。
func (p *pubsub) dispatch(ctx context.Context, topic, group, key string, raw goredis.XMessage, msg cpubsub.Message, handler cpubsub.Handler) (err error) {
	// 从消息字段提取 producer 的 span context，通过 Link 关联而非 parent-child。
	// OTel Messaging Semantic Conventions：异步 pub/sub 的 consumer span 应与
	// producer span 建立 Link，两者各自隶属独立的 trace，避免跨进程合并 trace tree。
	producerSpanCtx := trace.SpanContextFromContext(
		otel.GetTextMapPropagator().Extract(ctx, streamCarrier{raw.Values}),
	)

	spanOpts := []trace.SpanStartOption{
		trace.WithSpanKind(trace.SpanKindConsumer),
		trace.WithAttributes(
			attribute.String("messaging.system", "redis"),
			attribute.String("messaging.destination.name", topic),
			attribute.String("messaging.consumer.group.name", group),
			attribute.String("messaging.message.id", raw.ID),
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

	if err = handler(msgCtx, msg); err != nil {
		return err
	}
	if err = p.client.XAck(ctx, key, group, raw.ID).Err(); err != nil {
		return fmt.Errorf("cpubsub/redis: xack: %w", err)
	}
	return nil
}

func (p *pubsub) decodeMessage(topic string, raw goredis.XMessage) (cpubsub.Message, error) {
	body, _ := raw.Values[fieldBody].(string)

	if flag, _ := raw.Values[fieldCompress].(string); flag == compressFlag {
		decompressed, err := deflateDecompress([]byte(body))
		if err != nil {
			return cpubsub.Message{}, fmt.Errorf("decompress: %w", err)
		}
		body = string(decompressed)
	}

	return cpubsub.Message{
		ID:    raw.ID,
		Topic: topic,
		Body:  body,
	}, nil
}

func (p *pubsub) Close(_ context.Context) error {
	return nil
}

// --- compression ---

func deflateCompress(data []byte) ([]byte, error) {
	var buf bytes.Buffer
	w, err := flate.NewWriter(&buf, flate.BestSpeed)
	if err != nil {
		return nil, err
	}
	if _, err := w.Write(data); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func deflateDecompress(data []byte) ([]byte, error) {
	return io.ReadAll(flate.NewReader(bytes.NewReader(data)))
}
