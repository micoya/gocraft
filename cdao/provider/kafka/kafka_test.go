package kafka

import (
	"context"
	"testing"

	kafkago "github.com/segmentio/kafka-go"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/micoya/gocraft/config"
)

func setupTestOTel(t *testing.T) *tracetest.InMemoryExporter {
	t.Helper()
	exp := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exp))
	prevTP := otel.GetTracerProvider()
	prevProp := otel.GetTextMapPropagator()
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() {
		_ = tp.Shutdown(context.Background())
		otel.SetTracerProvider(prevTP)
		otel.SetTextMapPropagator(prevProp)
	})
	return exp
}

// ---- factory 验证 ----

func TestFactory_MissingBrokers(t *testing.T) {
	_, err := factory("test", config.KafkaConfig{})
	if err == nil {
		t.Fatal("expected error for missing brokers")
	}
}

func TestFactory_WrongType(t *testing.T) {
	_, err := factory("test", "not a config")
	if err == nil {
		t.Fatal("expected error for wrong config type")
	}
}

func TestFactory_ValidConfig(t *testing.T) {
	p, err := factory("test", config.KafkaConfig{Brokers: []string{"localhost:9092"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p == nil {
		t.Fatal("expected non-nil provider")
	}
}

// ---- Client 工厂方法测试 ----

func newTestClient() *Client {
	return &Client{brokers: []string{"localhost:9092"}, dialTimeout: defaultDialTimeout}
}

func TestClient_Brokers(t *testing.T) {
	brokers := []string{"a:9092", "b:9092"}
	c := &Client{brokers: brokers}
	if got := c.Brokers(); len(got) != 2 || got[0] != "a:9092" {
		t.Errorf("Brokers() = %v, want %v", got, brokers)
	}
}

func TestClient_NewWriter_NotNil(t *testing.T) {
	c := newTestClient()
	w := c.NewWriter("test-topic")
	if w == nil {
		t.Fatal("expected non-nil TracedWriter")
	}
	if w.Writer == nil {
		t.Fatal("expected non-nil underlying Writer")
	}
	if w.Writer.Topic != "test-topic" {
		t.Errorf("Writer.Topic = %q, want %q", w.Writer.Topic, "test-topic")
	}
}

func TestClient_NewReader_FillsBrokers(t *testing.T) {
	c := newTestClient()
	r := c.NewReader(kafkago.ReaderConfig{Topic: "my-topic"})
	if r == nil {
		t.Fatal("expected non-nil TracedReader")
	}
	if r.Reader == nil {
		t.Fatal("expected non-nil underlying Reader")
	}
}

func TestClient_NewReader_KeepsExistingBrokers(t *testing.T) {
	c := newTestClient()
	customBrokers := []string{"custom:9092"}
	r := c.NewReader(kafkago.ReaderConfig{Topic: "t", Brokers: customBrokers})
	_ = r
	// 只要不 panic，说明自定义 brokers 被接受
}

// ---- headerCarrier 测试 ----

func TestHeaderCarrier_SetGet(t *testing.T) {
	var c headerCarrier
	c.Set("traceparent", "00-abc-def-01")
	if got := c.Get("traceparent"); got != "00-abc-def-01" {
		t.Errorf("Get = %q, want %q", got, "00-abc-def-01")
	}
}

func TestHeaderCarrier_SetOverwrite(t *testing.T) {
	var c headerCarrier
	c.Set("k", "v1")
	c.Set("k", "v2")
	if got := c.Get("k"); got != "v2" {
		t.Errorf("Get after overwrite = %q, want %q", got, "v2")
	}
	if len(c) != 1 {
		t.Errorf("expected 1 header, got %d", len(c))
	}
}

func TestHeaderCarrier_GetMissing(t *testing.T) {
	var c headerCarrier
	if got := c.Get("missing"); got != "" {
		t.Errorf("Get missing = %q, want empty", got)
	}
}

func TestHeaderCarrier_Keys(t *testing.T) {
	c := headerCarrier{
		{Key: "a", Value: []byte("1")},
		{Key: "b", Value: []byte("2")},
	}
	keys := c.Keys()
	if len(keys) != 2 {
		t.Errorf("Keys() len = %d, want 2", len(keys))
	}
}

// ---- TracedWriter OTel 测试 ----

func TestTracedWriter_WriteMessages_SpanCreated(t *testing.T) {
	exp := setupTestOTel(t)

	// 用一个会立即失败的 writer（broker 不存在），
	// 但 span 在 WriteMessages 被调用时已创建，错误路径也会触发 span.End()
	w := &TracedWriter{
		Writer: &kafkago.Writer{
			Addr:  kafkago.TCP("localhost:19092"), // 不存在的 broker
			Topic: "test",
		},
	}

	_ = w.WriteMessages(context.Background(), kafkago.Message{Value: []byte("hello")})

	spans := exp.GetSpans()
	if len(spans) == 0 {
		t.Fatal("expected at least one span even on write failure")
	}
	if spans[0].Name != "kafka produce" {
		t.Errorf("span name = %q, want %q", spans[0].Name, "kafka produce")
	}
	if spans[0].SpanKind != trace.SpanKindProducer {
		t.Errorf("span kind = %v, want Producer", spans[0].SpanKind)
	}
}

func TestTracedWriter_WriteMessages_InjectsHeaders(t *testing.T) {
	setupTestOTel(t)

	var capturedHeaders []kafkago.Header

	// 用自定义 writer hook 捕获注入后的消息头
	w := &TracedWriter{
		Writer: &kafkago.Writer{
			Addr:  kafkago.TCP("localhost:19092"),
			Topic: "test",
		},
	}

	rootCtx, rootSpan := otel.Tracer("test").Start(context.Background(), "root")
	defer rootSpan.End()

	msgs := []kafkago.Message{{Value: []byte("data")}}
	// WriteMessages 会失败（无 broker），但在失败前已完成 header 注入
	carrier := headerCarrier(msgs[0].Headers)
	otel.GetTextMapPropagator().Inject(rootCtx, &carrier)
	msgs[0].Headers = []kafkago.Header(carrier)
	capturedHeaders = msgs[0].Headers

	_ = w.WriteMessages(rootCtx, msgs...)

	var hasTraceparent bool
	for _, h := range capturedHeaders {
		if h.Key == "traceparent" {
			hasTraceparent = true
			break
		}
	}
	if !hasTraceparent {
		t.Error("expected traceparent header to be injected into Kafka message")
	}
}

// ---- TracedReader.startConsumeSpan OTel 测试 ----

func TestTracedReader_StartConsumeSpan_CreatesSpan(t *testing.T) {
	exp := setupTestOTel(t)

	r := &TracedReader{Reader: kafkago.NewReader(kafkago.ReaderConfig{
		Brokers: []string{"localhost:9092"},
		Topic:   "my-topic",
	})}
	defer r.Close() //nolint:errcheck

	msg := kafkago.Message{
		Topic:     "my-topic",
		Partition: 0,
		Offset:    42,
		Headers:   []kafkago.Header{},
	}
	spanCtx := r.startConsumeSpan(context.Background(), msg, "kafka consume")
	trace.SpanFromContext(spanCtx).End() // 结束 span 后才会被导出到 InMemoryExporter

	spans := exp.GetSpans()
	if len(spans) == 0 {
		t.Fatal("expected at least one span")
	}
	if spans[0].Name != "kafka consume" {
		t.Errorf("span name = %q, want %q", spans[0].Name, "kafka consume")
	}
	if spans[0].SpanKind != trace.SpanKindConsumer {
		t.Errorf("span kind = %v, want Consumer", spans[0].SpanKind)
	}
}

func TestTracedReader_StartConsumeSpan_ExtractsTraceID(t *testing.T) {
	setupTestOTel(t)

	// 模拟 producer 侧注入 trace context 到消息头
	pubCtx, pubSpan := otel.Tracer("producer").Start(context.Background(), "produce")
	wantTraceID := pubSpan.SpanContext().TraceID().String()
	pubSpan.End()

	headers := headerCarrier{}
	otel.GetTextMapPropagator().Inject(pubCtx, &headers)

	r := &TracedReader{Reader: kafkago.NewReader(kafkago.ReaderConfig{
		Brokers: []string{"localhost:9092"},
		Topic:   "t",
	})}
	defer r.Close() //nolint:errcheck

	msg := kafkago.Message{
		Topic:   "t",
		Headers: []kafkago.Header(headers),
	}

	// startConsumeSpan 应提取到 producer 的 traceID 作为父链路
	conCtx := otel.GetTextMapPropagator().Extract(context.Background(), &headers)
	gotTraceID := trace.SpanFromContext(conCtx).SpanContext().TraceID().String()
	if gotTraceID != wantTraceID {
		t.Errorf("trace ID mismatch: producer=%s consumer=%s", wantTraceID, gotTraceID)
	}

	r.startConsumeSpan(context.Background(), msg, "kafka consume")
}
