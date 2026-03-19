package elasticsearch

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	es "github.com/elastic/go-elasticsearch/v8"
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

func TestFactory_MissingAddresses(t *testing.T) {
	_, err := factory("test", config.ElasticsearchConfig{})
	if err == nil {
		t.Fatal("expected error for missing addresses")
	}
}

func TestFactory_WrongType(t *testing.T) {
	_, err := factory("test", "not a config")
	if err == nil {
		t.Fatal("expected error for wrong config type")
	}
}

func TestFactory_ValidConfig(t *testing.T) {
	p, err := factory("test", config.ElasticsearchConfig{
		Addresses: []string{"http://localhost:9200"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p == nil {
		t.Fatal("expected non-nil provider")
	}
}

// ---- Init / Instance 测试 ----

func TestInit_CreatesClient(t *testing.T) {
	p := &provider{cfg: config.ElasticsearchConfig{
		Addresses: []string{"http://localhost:9200"},
	}}
	if err := p.Init(context.Background()); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if p.Instance() == nil {
		t.Fatal("Instance() should not be nil after Init")
	}
	if _, ok := p.Instance().(*es.Client); !ok {
		t.Errorf("Instance() = %T, want *es.Client", p.Instance())
	}
}

func TestClose_NilSafe(t *testing.T) {
	p := &provider{cfg: config.ElasticsearchConfig{Addresses: []string{"http://localhost:9200"}}}
	if err := p.Close(context.Background()); err != nil {
		t.Errorf("Close on uninitialized provider: %v", err)
	}
}

// ---- OTel transport 测试 ----

// esHandler 返回一个 http.HandlerFunc，模拟 Elasticsearch 节点响应。
// go-elasticsearch v8 client 会检验 X-Elastic-Product 头，需要正确设置。
func esHandler(statusCode int, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Elastic-Product", "Elasticsearch")
		w.WriteHeader(statusCode)
		_, _ = w.Write([]byte(body))
	}
}

func TestOtelTransport_SpanCreated(t *testing.T) {
	exp := setupTestOTel(t)

	srv := httptest.NewServer(esHandler(http.StatusOK, `{"name":"test"}`))
	defer srv.Close()

	transport := newOtelTransport(http.DefaultTransport)
	client, err := es.NewClient(es.Config{
		Addresses: []string{srv.URL},
		Transport: transport,
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	res, err := client.Info()
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	defer res.Body.Close()

	spans := exp.GetSpans()
	if len(spans) == 0 {
		t.Fatal("expected at least one span")
	}
}

func TestOtelTransport_SpanName(t *testing.T) {
	exp := setupTestOTel(t)

	srv := httptest.NewServer(esHandler(http.StatusOK, `{}`))
	defer srv.Close()

	transport := newOtelTransport(http.DefaultTransport)
	client, err := es.NewClient(es.Config{
		Addresses: []string{srv.URL},
		Transport: transport,
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	res, err := client.Info()
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	defer res.Body.Close()

	spans := exp.GetSpans()
	if len(spans) == 0 {
		t.Fatal("no spans")
	}
	if !strings.HasPrefix(spans[0].Name, "elasticsearch GET") {
		t.Errorf("span name = %q, want prefix %q", spans[0].Name, "elasticsearch GET")
	}
}

func TestOtelTransport_SpanKind(t *testing.T) {
	exp := setupTestOTel(t)

	srv := httptest.NewServer(esHandler(http.StatusOK, `{}`))
	defer srv.Close()

	transport := newOtelTransport(http.DefaultTransport)
	client, err := es.NewClient(es.Config{
		Addresses: []string{srv.URL},
		Transport: transport,
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	res, err := client.Info()
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	defer res.Body.Close()

	spans := exp.GetSpans()
	if len(spans) == 0 {
		t.Fatal("no spans")
	}
	if spans[0].SpanKind != trace.SpanKindClient {
		t.Errorf("span kind = %v, want Client", spans[0].SpanKind)
	}
}

func TestOtelTransport_AttributesPresent(t *testing.T) {
	exp := setupTestOTel(t)

	srv := httptest.NewServer(esHandler(http.StatusOK, `{}`))
	defer srv.Close()

	transport := newOtelTransport(http.DefaultTransport)
	client, err := es.NewClient(es.Config{
		Addresses: []string{srv.URL},
		Transport: transport,
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	res, err := client.Info()
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	defer res.Body.Close()

	spans := exp.GetSpans()
	if len(spans) == 0 {
		t.Fatal("no spans")
	}

	attrKeys := make(map[string]bool)
	for _, a := range spans[0].Attributes {
		attrKeys[string(a.Key)] = true
	}
	for _, want := range []string{"db.system", "http.request.method", "server.address", "http.response.status_code"} {
		if !attrKeys[want] {
			t.Errorf("span missing attribute %q; got: %v", want, attrKeys)
		}
	}
}

func TestOtelTransport_ErrorSpanOn4xx(t *testing.T) {
	exp := setupTestOTel(t)

	srv := httptest.NewServer(esHandler(http.StatusNotFound, `{"error":"not found"}`))
	defer srv.Close()

	transport := newOtelTransport(http.DefaultTransport)
	client, err := es.NewClient(es.Config{
		Addresses: []string{srv.URL},
		Transport: transport,
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	res, err := client.Info()
	if err != nil {
		t.Fatalf("unexpected transport error: %v", err)
	}
	defer res.Body.Close()

	spans := exp.GetSpans()
	if len(spans) == 0 {
		t.Fatal("no spans")
	}
	if spans[0].Status.Code.String() != "Error" {
		t.Errorf("expected span status Error for 404, got %s", spans[0].Status.Code)
	}
}

func TestOtelTransport_NilBase(t *testing.T) {
	tr := newOtelTransport(nil)
	if tr.base == nil {
		t.Error("expected fallback to http.DefaultTransport when base is nil")
	}
}
