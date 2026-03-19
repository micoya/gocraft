package openai

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/openai/openai-go/option"
)

func setupTestTracer(t *testing.T) *tracetest.InMemoryExporter {
	t.Helper()
	exp := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exp))
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	t.Cleanup(func() {
		_ = tp.Shutdown(context.Background())
		otel.SetTracerProvider(prev)
	})
	return exp
}

// ---- operationFromPath 单元测试 ----

func TestOperationFromPath(t *testing.T) {
	cases := []struct {
		path string
		want string
	}{
		{"/v1/chat/completions", "chat.completions"},
		{"/v1/embeddings", "embeddings"},
		{"/v1/models", "models"},
		{"/v1/images/generations", "images.generations"},
		{"/v1/audio/transcriptions", "audio.transcriptions"},
		{"/v1/fine_tuning/jobs", "fine_tuning.jobs"},
		{"", "unknown"},
		{"/", "unknown"},
	}
	for _, c := range cases {
		if got := operationFromPath(c.path); got != c.want {
			t.Errorf("operationFromPath(%q) = %q, want %q", c.path, got, c.want)
		}
	}
}

// ---- otelMiddleware 集成测试 ----

// TestOtelMiddleware_SpanCreated 通过 httptest.Server 模拟 OpenAI 端点，
// 验证 otelMiddleware 为每次请求创建了正确命名的 span。
func TestOtelMiddleware_SpanCreated(t *testing.T) {
	exp := setupTestTracer(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"test"}`))
	}))
	defer srv.Close()

	// 直接调用 middleware，模拟 openai-go SDK 的调用链
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost,
		srv.URL+"/v1/chat/completions", nil)

	var called bool
	next := option.MiddlewareNext(func(r *http.Request) (*http.Response, error) {
		called = true
		return http.DefaultClient.Do(r)
	})

	resp, err := otelMiddleware(req, next)
	if err != nil {
		t.Fatalf("otelMiddleware error: %v", err)
	}
	defer resp.Body.Close()

	if !called {
		t.Error("next was not called")
	}

	spans := exp.GetSpans()
	if len(spans) == 0 {
		t.Fatal("expected at least one span, got none")
	}

	var target *tracetest.SpanStub
	for i := range spans {
		if strings.Contains(spans[i].Name, "chat.completions") {
			target = &spans[i]
			break
		}
	}
	if target == nil {
		names := make([]string, len(spans))
		for i, s := range spans {
			names[i] = s.Name
		}
		t.Errorf("no 'openai chat.completions' span; got: %v", names)
	}
}

func TestOtelMiddleware_SpanAttributes(t *testing.T) {
	exp := setupTestTracer(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost,
		srv.URL+"/v1/embeddings", nil)
	resp, _ := otelMiddleware(req, func(r *http.Request) (*http.Response, error) {
		return http.DefaultClient.Do(r)
	})
	if resp != nil {
		defer resp.Body.Close()
	}

	spans := exp.GetSpans()
	if len(spans) == 0 {
		t.Fatal("no spans captured")
	}

	attrMap := make(map[string]bool)
	for _, a := range spans[0].Attributes {
		attrMap[string(a.Key)] = true
	}
	for _, want := range []string{"gen_ai.system", "gen_ai.operation.name", "http.request.method", "http.response.status_code"} {
		if !attrMap[want] {
			t.Errorf("span missing attribute %q; got: %v", want, attrMap)
		}
	}
}

func TestOtelMiddleware_ErrorStatusOn4xx(t *testing.T) {
	exp := setupTestTracer(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid_api_key"}`))
	}))
	defer srv.Close()

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost,
		srv.URL+"/v1/chat/completions", nil)
	resp, _ := otelMiddleware(req, func(r *http.Request) (*http.Response, error) {
		return http.DefaultClient.Do(r)
	})
	if resp != nil {
		defer resp.Body.Close()
	}

	spans := exp.GetSpans()
	if len(spans) == 0 {
		t.Fatal("no spans captured")
	}

	if spans[0].Status.Code.String() != "Error" {
		t.Errorf("expected span status Error for 401, got %s", spans[0].Status.Code)
	}
}
