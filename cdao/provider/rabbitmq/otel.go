package rabbitmq

import (
	"context"

	amqp "github.com/rabbitmq/amqp091-go"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

const tracerName = "cdao/rabbitmq"

// tableCarrier 将 amqp.Table 适配为 OTel TextMapCarrier 接口，
// 用于在 AMQP 消息头中注入或提取 W3C trace context（traceparent / tracestate）。
type tableCarrier amqp.Table

func (c tableCarrier) Get(key string) string {
	v, ok := c[key]
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return s
}

func (c tableCarrier) Set(key, val string) {
	c[key] = val
}

func (c tableCarrier) Keys() []string {
	keys := make([]string, 0, len(c))
	for k := range c {
		keys = append(keys, k)
	}
	return keys
}

// InjectHeaders 将 ctx 中的 trace context 注入到 AMQP 消息头 table 中。
// 应在调用 Channel.Publish 前调用，以实现跨进程链路传递。
func InjectHeaders(ctx context.Context, headers amqp.Table) {
	if headers == nil {
		return
	}
	otel.GetTextMapPropagator().Inject(ctx, tableCarrier(headers))
}

// ExtractHeaders 从 AMQP Delivery 的消息头中提取 trace context，
// 返回携带 parent span context 的新 ctx，应在消费者 handler 入口处调用。
func ExtractHeaders(ctx context.Context, headers amqp.Table) context.Context {
	return otel.GetTextMapPropagator().Extract(ctx, tableCarrier(headers))
}

// StartPublishSpan 为 AMQP Publish 操作创建 producer span。
// 调用方负责在操作完成后调用 span.End()。
func StartPublishSpan(ctx context.Context, exchange, routingKey string) (context.Context, trace.Span) {
	return otel.Tracer(tracerName).Start(ctx, "rabbitmq publish",
		trace.WithSpanKind(trace.SpanKindProducer),
		trace.WithAttributes(
			attribute.String("messaging.system", "rabbitmq"),
			attribute.String("messaging.operation", "publish"),
			attribute.String("messaging.destination", exchange),
			attribute.String("messaging.rabbitmq.routing_key", routingKey),
		),
	)
}

// StartConsumeSpan 为处理 AMQP Delivery 创建 consumer span。
// 调用方负责在 handler 完成后调用 span.End()。
func StartConsumeSpan(ctx context.Context, queue string) (context.Context, trace.Span) {
	return otel.Tracer(tracerName).Start(ctx, "rabbitmq consume",
		trace.WithSpanKind(trace.SpanKindConsumer),
		trace.WithAttributes(
			attribute.String("messaging.system", "rabbitmq"),
			attribute.String("messaging.operation", "receive"),
			attribute.String("messaging.source", queue),
		),
	)
}

// RecordError 将 err 记录到 span 并将其状态设置为 Error。
func RecordError(span trace.Span, err error) {
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
}
