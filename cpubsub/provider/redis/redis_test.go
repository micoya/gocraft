package redis

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	goredis "github.com/redis/go-redis/v9"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/micoya/gocraft/cpubsub"
)

// setupTestOTel 注册同步内存 exporter 并配置 W3C TraceContext 传播器。
// 对 cpubsub 跨进程传播测试至关重要：若未设置 TextMapPropagator，
// Inject/Extract 将成为 no-op，traceparent 字段不会被写入/读出。
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

func setup(t *testing.T, opts ...Option) (cpubsub.PubSub, *miniredis.Miniredis) {
	t.Helper()
	s := miniredis.RunT(t)
	client := goredis.NewClient(&goredis.Options{Addr: s.Addr()})
	t.Cleanup(func() { client.Close() })
	return New(client, opts...), s
}

func TestPublishSubscribe(t *testing.T) {
	ps, _ := setup(t, WithPrefix("test:"), WithTTL(time.Hour))
	ctx := context.Background()

	id, err := ps.Publish(ctx, "orders", `{"id":1}`)
	if err != nil {
		t.Fatalf("Publish() error: %v", err)
	}
	if id == "" {
		t.Fatal("expected non-empty message ID")
	}

	var got cpubsub.Message
	subCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	err = ps.Subscribe(subCtx, "orders", "grp", "w1", func(_ context.Context, msg cpubsub.Message) error {
		got = msg
		cancel()
		return nil
	})
	if err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("Subscribe() error: %v", err)
	}

	if got.Body != `{"id":1}` {
		t.Errorf("body = %q, want %q", got.Body, `{"id":1}`)
	}
	if got.Topic != "orders" {
		t.Errorf("topic = %q, want %q", got.Topic, "orders")
	}
}

func TestPublishSubscribeWithCompression(t *testing.T) {
	ps, _ := setup(t, WithCompress(true))
	ctx := context.Background()

	body := strings.Repeat("hello world ", 100)
	if _, err := ps.Publish(ctx, "big", body); err != nil {
		t.Fatalf("Publish() error: %v", err)
	}

	var got string
	subCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	_ = ps.Subscribe(subCtx, "big", "grp", "w1", func(_ context.Context, msg cpubsub.Message) error {
		got = msg.Body
		cancel()
		return nil
	})

	if got != body {
		t.Errorf("decompressed body mismatch: got len %d, want len %d", len(got), len(body))
	}
}

func TestMultipleMessages(t *testing.T) {
	ps, _ := setup(t)
	ctx := context.Background()

	const n = 5
	for i := range n {
		if _, err := ps.Publish(ctx, "multi", fmt.Sprintf("msg-%d", i)); err != nil {
			t.Fatalf("Publish(%d) error: %v", i, err)
		}
	}

	var bodies []string
	subCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	_ = ps.Subscribe(subCtx, "multi", "grp", "w1", func(_ context.Context, msg cpubsub.Message) error {
		bodies = append(bodies, msg.Body)
		if len(bodies) == n {
			cancel()
		}
		return nil
	})

	if len(bodies) != n {
		t.Fatalf("got %d messages, want %d", len(bodies), n)
	}
	for i, b := range bodies {
		if want := fmt.Sprintf("msg-%d", i); b != want {
			t.Errorf("bodies[%d] = %q, want %q", i, b, want)
		}
	}
}

func TestHandlerErrorStopsSubscription(t *testing.T) {
	ps, _ := setup(t)
	ctx := context.Background()

	if _, err := ps.Publish(ctx, "err-test", "data"); err != nil {
		t.Fatal(err)
	}

	wantErr := errors.New("handler boom")
	subCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	err := ps.Subscribe(subCtx, "err-test", "grp", "w1", func(_ context.Context, _ cpubsub.Message) error {
		return wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Errorf("Subscribe() error = %v, want %v", err, wantErr)
	}
}

func TestDefaultPrefix(t *testing.T) {
	p := &pubsub{opts: options{prefix: defaultPrefix}}
	if got := p.key("orders"); got != "channel:orders" {
		t.Errorf("key = %q, want %q", got, "channel:orders")
	}
}

func TestCustomPrefix(t *testing.T) {
	p := &pubsub{opts: options{prefix: "app:"}}
	if got := p.key("events"); got != "app:events" {
		t.Errorf("key = %q, want %q", got, "app:events")
	}
}

func TestTTLApplied(t *testing.T) {
	ps, s := setup(t, WithTTL(10*time.Minute))
	ctx := context.Background()

	if _, err := ps.Publish(ctx, "ttl-test", "data"); err != nil {
		t.Fatal(err)
	}

	ttl := s.TTL("channel:ttl-test")
	if ttl <= 0 {
		t.Error("expected positive TTL on stream key")
	}
}

func TestCompressRoundTrip(t *testing.T) {
	original := "the quick brown fox jumps over the lazy dog"
	compressed, err := deflateCompress([]byte(original))
	if err != nil {
		t.Fatal(err)
	}
	if len(compressed) == 0 {
		t.Fatal("compressed data is empty")
	}

	decompressed, err := deflateDecompress(compressed)
	if err != nil {
		t.Fatal(err)
	}
	if string(decompressed) != original {
		t.Errorf("round-trip failed: got %q", decompressed)
	}
}

// ---- WithTracing 测试 ----

func TestWithTracing_PublishCreatesProducerSpan(t *testing.T) {
	exp := setupTestOTel(t)
	ps, _ := setup(t, WithTracing(true))

	_, err := ps.Publish(context.Background(), "events", `{"type":"order"}`)
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}

	spans := exp.GetSpans()
	var found bool
	for _, s := range spans {
		if strings.Contains(s.Name, "publish") {
			found = true
			if s.SpanKind.String() != "producer" {
				t.Errorf("publish span kind = %q, want producer", s.SpanKind)
			}
			break
		}
	}
	if !found {
		names := make([]string, len(spans))
		for i, s := range spans {
			names[i] = s.Name
		}
		t.Errorf("no publish span found; all spans: %v", names)
	}
}

func TestWithTracing_SubscribeCreatesConsumerSpan(t *testing.T) {
	exp := setupTestOTel(t)
	ps, _ := setup(t, WithTracing(true))
	ctx := context.Background()

	if _, err := ps.Publish(ctx, "events", `{"type":"payment"}`); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	exp.Reset() // 只关注消费侧 span

	subCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_ = ps.Subscribe(subCtx, "events", "grp", "w1", func(_ context.Context, _ cpubsub.Message) error {
		cancel()
		return nil
	})

	spans := exp.GetSpans()
	var found bool
	for _, s := range spans {
		if strings.Contains(s.Name, "process") {
			found = true
			if s.SpanKind.String() != "consumer" {
				t.Errorf("process span kind = %q, want consumer", s.SpanKind)
			}
			break
		}
	}
	if !found {
		names := make([]string, len(spans))
		for i, s := range spans {
			names[i] = s.Name
		}
		t.Errorf("no process span found; all spans: %v", names)
	}
}

// TestWithTracing_TracePropagation 是最关键的端到端测试：
// 验证 publisher 注入的 trace context 能被 consumer 正确提取，
// 使两端 span 共享同一个 TraceID，形成完整的分布式链路。
func TestWithTracing_TracePropagation(t *testing.T) {
	setupTestOTel(t)
	ps, _ := setup(t, WithTracing(true))
	ctx := context.Background()

	// 用一个根 span 模拟 publisher 侧的请求上下文
	rootCtx, rootSpan := otel.Tracer("test").Start(ctx, "publisher-handler")
	wantTraceID := rootSpan.SpanContext().TraceID()

	if _, err := ps.Publish(rootCtx, "orders", `{"id":42}`); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	rootSpan.End()

	var gotTraceID trace.TraceID
	subCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	_ = ps.Subscribe(subCtx, "orders", "grp", "w1", func(msgCtx context.Context, _ cpubsub.Message) error {
		// msgCtx 应包含从消息中还原的 span context，TraceID 与 publisher 一致
		gotTraceID = trace.SpanFromContext(msgCtx).SpanContext().TraceID()
		cancel()
		return nil
	})

	if gotTraceID == (trace.TraceID{}) {
		t.Fatal("consumer received zero TraceID; trace context was not propagated")
	}
	if gotTraceID != wantTraceID {
		t.Errorf("TraceID mismatch:\n  publisher: %s\n  consumer:  %s", wantTraceID, gotTraceID)
	}
}

func TestWithTracing_NoTracingByDefault(t *testing.T) {
	exp := setupTestOTel(t)
	// 不传 WithTracing，默认不追踪
	ps, _ := setup(t)

	if _, err := ps.Publish(context.Background(), "plain", "hello"); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	// 不应有任何 cpubsub span
	for _, s := range exp.GetSpans() {
		if strings.Contains(s.Name, "publish") || strings.Contains(s.Name, "process") {
			t.Errorf("unexpected span %q when tracing is disabled", s.Name)
		}
	}
}
