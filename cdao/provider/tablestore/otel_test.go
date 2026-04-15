package tablestore

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
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

func TestOtelTransport_SpanCreated(t *testing.T) {
	exp := setupTestTracer(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	transport := &otelTransport{base: http.DefaultTransport}
	client := &http.Client{Transport: transport}

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, srv.URL+"/", nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request error: %v", err)
	}
	defer resp.Body.Close()

	spans := exp.GetSpans()
	if len(spans) == 0 {
		t.Fatal("expected at least one span, got none")
	}

	if !strings.HasPrefix(spans[0].Name, "tablestore ") {
		t.Errorf("span name = %q, expected prefix 'tablestore '", spans[0].Name)
	}
}

func TestOtelTransport_SpanKind(t *testing.T) {
	exp := setupTestTracer(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	transport := &otelTransport{base: http.DefaultTransport}
	client := &http.Client{Transport: transport}

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, srv.URL+"/", nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request error: %v", err)
	}
	defer resp.Body.Close()

	spans := exp.GetSpans()
	if len(spans) == 0 {
		t.Fatal("no spans captured")
	}
	if spans[0].SpanKind != trace.SpanKindClient {
		t.Errorf("span kind = %v, want Client", spans[0].SpanKind)
	}
}

func TestOtelTransport_AttributesPresent(t *testing.T) {
	exp := setupTestTracer(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	transport := &otelTransport{base: http.DefaultTransport}
	client := &http.Client{Transport: transport}

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL+"/ListTable", nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request error: %v", err)
	}
	defer resp.Body.Close()

	spans := exp.GetSpans()
	if len(spans) == 0 {
		t.Fatal("no spans captured")
	}

	attrKeys := make(map[string]bool)
	for _, a := range spans[0].Attributes {
		attrKeys[string(a.Key)] = true
	}
	for _, want := range []string{"rpc.system", "http.request.method", "server.address", "http.response.status_code"} {
		if !attrKeys[want] {
			t.Errorf("span missing attribute %q; got keys: %v", want, attrKeys)
		}
	}
}

func TestOtelTransport_ErrorStatusOn4xx(t *testing.T) {
	exp := setupTestTracer(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	transport := &otelTransport{base: http.DefaultTransport}
	client := &http.Client{Transport: transport}

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, srv.URL+"/", nil)
	resp, _ := client.Do(req)
	if resp != nil {
		defer resp.Body.Close()
	}

	spans := exp.GetSpans()
	if len(spans) == 0 {
		t.Fatal("no spans captured")
	}
	if spans[0].Status.Code.String() != "Error" {
		t.Errorf("expected Error status for 403, got %s", spans[0].Status.Code)
	}
}
