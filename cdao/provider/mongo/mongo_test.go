package mongo

import (
	"context"
	"testing"

	"go.mongodb.org/mongo-driver/event"
	"go.opentelemetry.io/otel"
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
	otel.SetTracerProvider(tp)
	t.Cleanup(func() {
		_ = tp.Shutdown(context.Background())
		otel.SetTracerProvider(prevTP)
	})
	return exp
}

// ---- factory 验证 ----

func TestFactory_MissingURI(t *testing.T) {
	_, err := factory("test", config.MongoConfig{})
	if err == nil {
		t.Fatal("expected error for missing URI")
	}
}

func TestFactory_WrongType(t *testing.T) {
	_, err := factory("test", "not a config")
	if err == nil {
		t.Fatal("expected error for wrong config type")
	}
}

func TestFactory_ValidConfig(t *testing.T) {
	p, err := factory("test", config.MongoConfig{URI: "mongodb://localhost:27017"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p == nil {
		t.Fatal("expected non-nil provider")
	}
}

// ---- OTel monitor 测试 ----

func TestNewMonitor_NotNil(t *testing.T) {
	m := newMonitor()
	if m == nil {
		t.Fatal("newMonitor() returned nil")
	}
	if m.Started == nil {
		t.Fatal("monitor.Started is nil")
	}
	if m.Succeeded == nil {
		t.Fatal("monitor.Succeeded is nil")
	}
	if m.Failed == nil {
		t.Fatal("monitor.Failed is nil")
	}
}

// TestMonitor_StartedSucceeded 通过直接调用 CommandMonitor 回调模拟 MongoDB 命令执行，
// 验证 otelmongo 能正确创建并结束 span，无需真实 MongoDB 连接。
func TestMonitor_StartedSucceeded_CreatesSpan(t *testing.T) {
	exp := setupTestOTel(t)
	m := newMonitor()

	startedEvt := &event.CommandStartedEvent{
		CommandName:  "find",
		DatabaseName: "testdb",
		RequestID:    1,
		ConnectionID: "localhost:27017[1]",
	}
	m.Started(context.Background(), startedEvt)

	succeededEvt := &event.CommandSucceededEvent{
		CommandFinishedEvent: event.CommandFinishedEvent{
			CommandName:  "find",
			RequestID:    1,
			ConnectionID: "localhost:27017[1]",
			Duration:     500,
		},
	}
	m.Succeeded(context.Background(), succeededEvt)

	spans := exp.GetSpans()
	if len(spans) == 0 {
		t.Fatal("expected at least one span after Started+Succeeded")
	}
}

func TestMonitor_StartedFailed_ErrorSpan(t *testing.T) {
	exp := setupTestOTel(t)
	m := newMonitor()

	startedEvt := &event.CommandStartedEvent{
		CommandName:  "insert",
		DatabaseName: "testdb",
		RequestID:    2,
		ConnectionID: "localhost:27017[1]",
	}
	m.Started(context.Background(), startedEvt)

	failedEvt := &event.CommandFailedEvent{
		CommandFinishedEvent: event.CommandFinishedEvent{
			CommandName:  "insert",
			RequestID:    2,
			ConnectionID: "localhost:27017[1]",
			Duration:     200,
		},
		Failure: "write concern error",
	}
	m.Failed(context.Background(), failedEvt)

	spans := exp.GetSpans()
	if len(spans) == 0 {
		t.Fatal("expected at least one span on failure path")
	}
	if spans[0].Status.Code.String() != "Error" {
		t.Errorf("expected span status Error on failed command, got %s", spans[0].Status.Code)
	}
}

func TestMonitor_SpanKind(t *testing.T) {
	exp := setupTestOTel(t)
	m := newMonitor()

	m.Started(context.Background(), &event.CommandStartedEvent{
		CommandName:  "find",
		DatabaseName: "db",
		RequestID:    3,
		ConnectionID: "localhost:27017[1]",
	})
	m.Succeeded(context.Background(), &event.CommandSucceededEvent{
		CommandFinishedEvent: event.CommandFinishedEvent{
			CommandName:  "find",
			RequestID:    3,
			ConnectionID: "localhost:27017[1]",
			Duration:     100,
		},
	})

	spans := exp.GetSpans()
	if len(spans) == 0 {
		t.Fatal("no spans")
	}
	if spans[0].SpanKind != trace.SpanKindClient {
		t.Errorf("span kind = %v, want Client", spans[0].SpanKind)
	}
}

func TestMonitor_MultipleCommands_MultipleSpans(t *testing.T) {
	exp := setupTestOTel(t)
	m := newMonitor()

	for i := int64(10); i < 13; i++ {
		m.Started(context.Background(), &event.CommandStartedEvent{
			CommandName:  "find",
			DatabaseName: "db",
			RequestID:    i,
			ConnectionID: "localhost:27017[1]",
		})
		m.Succeeded(context.Background(), &event.CommandSucceededEvent{
			CommandFinishedEvent: event.CommandFinishedEvent{
				CommandName:  "find",
				RequestID:    i,
				ConnectionID: "localhost:27017[1]",
				Duration:     100,
			},
		})
	}

	spans := exp.GetSpans()
	if len(spans) != 3 {
		t.Errorf("expected 3 spans, got %d", len(spans))
	}
}
