package rabbitmq

import (
	"context"
	"testing"

	amqp "github.com/rabbitmq/amqp091-go"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
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

func TestFactory_MissingURL(t *testing.T) {
	_, err := factory("test", config.RabbitMQConfig{})
	if err == nil {
		t.Fatal("expected error for missing URL")
	}
}

func TestFactory_WrongType(t *testing.T) {
	_, err := factory("test", "not a config")
	if err == nil {
		t.Fatal("expected error for wrong config type")
	}
}

func TestFactory_ValidConfig(t *testing.T) {
	p, err := factory("test", config.RabbitMQConfig{URL: "amqp://localhost"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p == nil {
		t.Fatal("expected non-nil provider")
	}
}

// ---- OTel span 测试 ----

func TestStartPublishSpan_CreatesSpan(t *testing.T) {
	exp := setupTestOTel(t)

	ctx, span := StartPublishSpan(context.Background(), "orders", "order.created")
	span.End()
	_ = ctx

	spans := exp.GetSpans()
	if len(spans) == 0 {
		t.Fatal("expected at least one span")
	}
	if spans[0].Name != "rabbitmq publish" {
		t.Errorf("span name = %q, want %q", spans[0].Name, "rabbitmq publish")
	}
	if spans[0].SpanKind != trace.SpanKindProducer {
		t.Errorf("span kind = %v, want Producer", spans[0].SpanKind)
	}
}

func TestStartPublishSpan_Attributes(t *testing.T) {
	exp := setupTestOTel(t)

	_, span := StartPublishSpan(context.Background(), "events", "user.signup")
	span.End()

	spans := exp.GetSpans()
	if len(spans) == 0 {
		t.Fatal("no spans")
	}
	attrs := make(map[string]string)
	for _, a := range spans[0].Attributes {
		attrs[string(a.Key)] = a.Value.AsString()
	}
	if attrs["messaging.system"] != "rabbitmq" {
		t.Errorf("messaging.system = %q, want %q", attrs["messaging.system"], "rabbitmq")
	}
	if attrs["messaging.destination"] != "events" {
		t.Errorf("messaging.destination = %q, want %q", attrs["messaging.destination"], "events")
	}
	if attrs["messaging.rabbitmq.routing_key"] != "user.signup" {
		t.Errorf("routing_key = %q, want %q", attrs["messaging.rabbitmq.routing_key"], "user.signup")
	}
}

func TestStartConsumeSpan_CreatesSpan(t *testing.T) {
	exp := setupTestOTel(t)

	ctx, span := StartConsumeSpan(context.Background(), "order-queue")
	span.End()
	_ = ctx

	spans := exp.GetSpans()
	if len(spans) == 0 {
		t.Fatal("expected at least one span")
	}
	if spans[0].Name != "rabbitmq consume" {
		t.Errorf("span name = %q, want %q", spans[0].Name, "rabbitmq consume")
	}
	if spans[0].SpanKind != trace.SpanKindConsumer {
		t.Errorf("span kind = %v, want Consumer", spans[0].SpanKind)
	}
}

// ---- OTel 传播测试 ----

func TestInjectExtractHeaders_RoundTrip(t *testing.T) {
	setupTestOTel(t)

	// 模拟 publisher：创建 span 并注入消息头
	pubCtx, pubSpan := StartPublishSpan(context.Background(), "x", "k")
	headers := amqp.Table{}
	InjectHeaders(pubCtx, headers)
	pubSpan.End()

	if _, ok := headers["traceparent"]; !ok {
		t.Fatal("expected traceparent in headers after InjectHeaders")
	}

	// 模拟 consumer：从消息头提取 span context
	conCtx := ExtractHeaders(context.Background(), headers)
	extracted := trace.SpanFromContext(conCtx)
	if !extracted.SpanContext().IsValid() {
		t.Error("expected valid span context after ExtractHeaders")
	}
}

func TestInjectHeaders_NilTable(t *testing.T) {
	setupTestOTel(t)
	// nil table 不应 panic
	InjectHeaders(context.Background(), nil)
}

func TestInjectHeaders_SameTraceID(t *testing.T) {
	setupTestOTel(t)

	rootCtx, rootSpan := otel.Tracer("test").Start(context.Background(), "root")
	wantTraceID := rootSpan.SpanContext().TraceID().String()
	defer rootSpan.End()

	headers := amqp.Table{}
	InjectHeaders(rootCtx, headers)

	conCtx := ExtractHeaders(context.Background(), headers)
	gotTraceID := trace.SpanFromContext(conCtx).SpanContext().TraceID().String()
	if gotTraceID != wantTraceID {
		t.Errorf("trace ID mismatch: injected %s, extracted %s", wantTraceID, gotTraceID)
	}
}

// ---- tableCarrier 测试 ----

func TestTableCarrier_SetGet(t *testing.T) {
	table := amqp.Table{}
	c := tableCarrier(table)
	c.Set("key1", "val1")
	if got := c.Get("key1"); got != "val1" {
		t.Errorf("Get = %q, want %q", got, "val1")
	}
	if got := c.Get("missing"); got != "" {
		t.Errorf("Get missing key = %q, want empty", got)
	}
}

func TestTableCarrier_Keys(t *testing.T) {
	table := amqp.Table{"a": "1", "b": "2"}
	c := tableCarrier(table)
	keys := c.Keys()
	if len(keys) != 2 {
		t.Errorf("Keys() len = %d, want 2", len(keys))
	}
}

func TestTableCarrier_GetNonString(t *testing.T) {
	table := amqp.Table{"num": 42}
	c := tableCarrier(table)
	if got := c.Get("num"); got != "" {
		t.Errorf("Get non-string value = %q, want empty string", got)
	}
}

// ---- RecordError 测试 ----

func TestRecordError_NilError(t *testing.T) {
	setupTestOTel(t)
	_, span := otel.Tracer("test").Start(context.Background(), "op")
	RecordError(span, nil)
	span.End()
	// 不应 panic，状态应保持 Unset
}

func TestRecordError_WithError(t *testing.T) {
	exp := setupTestOTel(t)
	_, span := otel.Tracer("test").Start(context.Background(), "op")
	RecordError(span, context.DeadlineExceeded)
	span.End()

	spans := exp.GetSpans()
	if len(spans) == 0 {
		t.Fatal("no spans")
	}
	if spans[0].Status.Code != codes.Error {
		t.Errorf("span status = %v, want Error", spans[0].Status.Code)
	}
}
