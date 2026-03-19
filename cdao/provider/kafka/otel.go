package kafka

import (
	"context"

	kafkago "github.com/segmentio/kafka-go"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

const tracerName = "cdao/kafka"

// headerCarrier 将 []kafkago.Header 适配为 OTel TextMapCarrier 接口，
// 用于在 Kafka 消息头中注入或提取 W3C trace context。
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

// TracedWriter 封装 *kafkago.Writer，为每批写入创建 producer span
// 并将 trace context 注入各消息的 Header 中。
type TracedWriter struct {
	Writer *kafkago.Writer
}

// WriteMessages 向 Kafka 写入消息，自动创建 producer span 并注入 trace context。
func (w *TracedWriter) WriteMessages(ctx context.Context, msgs ...kafkago.Message) error {
	ctx, span := otel.Tracer(tracerName).Start(ctx, "kafka produce",
		trace.WithSpanKind(trace.SpanKindProducer),
		trace.WithAttributes(
			attribute.String("messaging.system", "kafka"),
			attribute.String("messaging.operation", "publish"),
			attribute.String("messaging.destination", w.Writer.Topic),
			attribute.Int("messaging.batch.message_count", len(msgs)),
		),
	)
	defer span.End()

	for i := range msgs {
		carrier := headerCarrier(msgs[i].Headers)
		otel.GetTextMapPropagator().Inject(ctx, &carrier)
		msgs[i].Headers = []kafkago.Header(carrier)
	}

	if err := w.Writer.WriteMessages(ctx, msgs...); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return err
	}
	return nil
}

// Close 关闭底层 Writer。
func (w *TracedWriter) Close() error {
	return w.Writer.Close()
}

// TracedReader 封装 *kafkago.Reader，为每条消费记录创建 consumer span
// 并从消息头中提取 trace context 实现跨进程链路继承。
type TracedReader struct {
	Reader *kafkago.Reader
}

// ReadMessage 读取并自动提交偏移量，返回注入了 consumer span 的 context。
// 调用方应在消息处理完成后结束 span：
//
//	msgCtx, msg, err := reader.ReadMessage(ctx)
//	if err != nil { return err }
//	defer trace.SpanFromContext(msgCtx).End()
//	// 使用 msgCtx 处理消息
func (r *TracedReader) ReadMessage(ctx context.Context) (context.Context, kafkago.Message, error) {
	msg, err := r.Reader.ReadMessage(ctx)
	if err != nil {
		return ctx, msg, err
	}
	msgCtx := r.startConsumeSpan(ctx, msg, "kafka consume")
	return msgCtx, msg, nil
}

// FetchMessage 读取消息但不自动提交偏移量，返回注入了 consumer span 的 context。
// 需配合 CommitMessages 使用，span 应在消息处理完成（含 Commit）后结束：
//
//	msgCtx, msg, err := reader.FetchMessage(ctx)
//	if err != nil { return err }
//	defer trace.SpanFromContext(msgCtx).End()
//	// 使用 msgCtx 处理消息
//	reader.CommitMessages(msgCtx, msg)
func (r *TracedReader) FetchMessage(ctx context.Context) (context.Context, kafkago.Message, error) {
	msg, err := r.Reader.FetchMessage(ctx)
	if err != nil {
		return ctx, msg, err
	}
	msgCtx := r.startConsumeSpan(ctx, msg, "kafka fetch")
	return msgCtx, msg, nil
}

// startConsumeSpan 提取消息头中的 trace context 并创建 consumer span，
// 使消费侧 span 成为生产侧 span 的下游节点。返回注入了 span 的新 context，
// span 生命周期由调用方负责结束（调用 trace.SpanFromContext(ctx).End()）。
func (r *TracedReader) startConsumeSpan(ctx context.Context, msg kafkago.Message, opName string) context.Context {
	carrier := headerCarrier(msg.Headers)
	parentCtx := otel.GetTextMapPropagator().Extract(ctx, &carrier)
	msgCtx, _ := otel.Tracer(tracerName).Start(parentCtx, opName,
		trace.WithSpanKind(trace.SpanKindConsumer),
		trace.WithAttributes(
			attribute.String("messaging.system", "kafka"),
			attribute.String("messaging.operation", "receive"),
			attribute.String("messaging.source", msg.Topic),
			attribute.Int64("messaging.kafka.offset", msg.Offset),
			attribute.Int("messaging.kafka.partition", msg.Partition),
		),
	)
	return msgCtx
}

// CommitMessages 提交消息偏移量，委托给底层 Reader。
func (r *TracedReader) CommitMessages(ctx context.Context, msgs ...kafkago.Message) error {
	return r.Reader.CommitMessages(ctx, msgs...)
}

// Close 关闭底层 Reader。
func (r *TracedReader) Close() error {
	return r.Reader.Close()
}
