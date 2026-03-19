package httpclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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

func initProvider(t *testing.T, cfg config.HTTPClientConfig) *provider {
	t.Helper()
	p := &provider{name: "test-service", cfg: cfg}
	if err := p.Init(context.Background()); err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(func() { _ = p.Close(context.Background()) })
	return p
}

// TestInit_DefaultValues 验证零值配置时使用默认超时和连接池参数。
func TestInit_DefaultValues(t *testing.T) {
	p := initProvider(t, config.HTTPClientConfig{})
	if p.client == nil {
		t.Fatal("expected non-nil http.Client after Init")
	}
	if p.client.Timeout != defaultTimeout {
		t.Errorf("Timeout = %v, want %v", p.client.Timeout, defaultTimeout)
	}
}

// TestInit_CustomTimeout 验证自定义超时被正确应用。
func TestInit_CustomTimeout(t *testing.T) {
	p := initProvider(t, config.HTTPClientConfig{Timeout: 5 * time.Second})
	if p.client.Timeout != 5*time.Second {
		t.Errorf("Timeout = %v, want 5s", p.client.Timeout)
	}
}

// TestInstance_ReturnsHTTPClient 验证 Instance() 返回 *http.Client。
func TestInstance_ReturnsHTTPClient(t *testing.T) {
	p := initProvider(t, config.HTTPClientConfig{})
	inst := p.Instance()
	if _, ok := inst.(*http.Client); !ok {
		t.Errorf("Instance() = %T, want *http.Client", inst)
	}
}

// TestClose_NilSafe 验证 Close 可安全重复调用。
func TestClose_NilSafe(t *testing.T) {
	p := &provider{name: "x", cfg: config.HTTPClientConfig{}}
	_ = p.Init(context.Background())
	_ = p.Close(context.Background())
	if err := p.Close(context.Background()); err != nil {
		t.Errorf("second Close() error: %v", err)
	}
}

// TestHealth_NoBaseURL 未配置 BaseURL 时 Health 应直接返回 nil。
func TestHealth_NoBaseURL(t *testing.T) {
	p := initProvider(t, config.HTTPClientConfig{})
	if err := p.Health(context.Background()); err != nil {
		t.Errorf("Health() without BaseURL: %v", err)
	}
}

// TestHealth_WithBaseURL 配置 BaseURL 时 Health 向该地址发送 HEAD 请求验证连通性。
func TestHealth_WithBaseURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodHead {
			t.Errorf("health check method = %q, want HEAD", r.Method)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p := initProvider(t, config.HTTPClientConfig{BaseURL: srv.URL})
	if err := p.Health(context.Background()); err != nil {
		t.Errorf("Health() error: %v", err)
	}
}

// TestHealth_5xxFails 上游返回 5xx 时 Health 应返回错误。
func TestHealth_5xxFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	p := initProvider(t, config.HTTPClientConfig{BaseURL: srv.URL})
	if err := p.Health(context.Background()); err == nil {
		t.Error("expected Health() error for 503, got nil")
	}
}

// TestOTel_SpanCreated 验证每次 HTTP 请求都产生一个 OTel client span。
func TestOTel_SpanCreated(t *testing.T) {
	exp := setupTestOTel(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p := initProvider(t, config.HTTPClientConfig{BaseURL: srv.URL})
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL+"/ping", nil)
	resp, err := p.client.Do(req)
	if err != nil {
		t.Fatalf("request error: %v", err)
	}
	// otelhttp 在 response body Close() 时才结束 span。
	// 必须先 Close body，再调用 exp.GetSpans()，否则 span 尚未 export。
	resp.Body.Close()

	spans := exp.GetSpans()
	if len(spans) == 0 {
		t.Fatal("expected at least one span, got none")
	}

	// otelhttp span 名格式为 "HTTP {METHOD}"（如 "HTTP GET"）
	var found bool
	for _, s := range spans {
		if strings.Contains(s.Name, "GET") {
			found = true
			break
		}
	}
	if !found {
		names := make([]string, len(spans))
		for i, s := range spans {
			names[i] = s.Name
		}
		t.Errorf("no HTTP GET span found; all spans: %v", names)
	}
}

// TestOTel_TracePropagation 验证 otelhttp 自动将 W3C traceparent 注入请求头，
// 使下游服务能接收到完整的 trace context（分布式链路核心特性）。
func TestOTel_TracePropagation(t *testing.T) {
	setupTestOTel(t)

	var receivedTraceparent string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedTraceparent = r.Header.Get("Traceparent")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p := initProvider(t, config.HTTPClientConfig{BaseURL: srv.URL})

	// 创建根 span，模拟请求来自某个业务入口
	rootCtx, rootSpan := otel.Tracer("test").Start(context.Background(), "root")
	defer rootSpan.End()

	req, _ := http.NewRequestWithContext(rootCtx, http.MethodGet, srv.URL+"/api", nil)
	resp, err := p.client.Do(req)
	if err != nil {
		t.Fatalf("request error: %v", err)
	}
	defer resp.Body.Close()

	if receivedTraceparent == "" {
		t.Fatal("downstream received empty Traceparent header; W3C propagation not working")
	}

	// traceparent 格式: 00-{traceID}-{spanID}-{flags}
	wantTraceID := rootSpan.SpanContext().TraceID().String()
	if !strings.Contains(receivedTraceparent, wantTraceID) {
		t.Errorf("traceparent %q does not contain root trace ID %q", receivedTraceparent, wantTraceID)
	}
}

// TestOTel_PeerServiceAttribute 验证 peer.service 属性等于 provider name（配置 key）。
func TestOTel_PeerServiceAttribute(t *testing.T) {
	exp := setupTestOTel(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p := &provider{name: "payment-service", cfg: config.HTTPClientConfig{BaseURL: srv.URL}}
	if err := p.Init(context.Background()); err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(func() { _ = p.Close(context.Background()) })

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, srv.URL+"/charge", nil)
	resp, err := p.client.Do(req)
	if err != nil {
		t.Fatalf("request error: %v", err)
	}
	resp.Body.Close()

	spans := exp.GetSpans()
	if len(spans) == 0 {
		t.Fatal("no spans")
	}

	attrMap := make(map[string]string)
	for _, a := range spans[0].Attributes {
		attrMap[string(a.Key)] = a.Value.AsString()
	}
	if attrMap["peer.service"] != "payment-service" {
		t.Errorf("peer.service = %q, want %q", attrMap["peer.service"], "payment-service")
	}
}

// TestOTel_ErrorSpanOn5xx 验证上游返回 5xx 时 span 状态被标记为 Error。
func TestOTel_ErrorSpanOn5xx(t *testing.T) {
	exp := setupTestOTel(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	p := initProvider(t, config.HTTPClientConfig{})
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL+"/broken", nil)
	resp, err := p.client.Do(req)
	if err != nil {
		t.Fatalf("unexpected transport error: %v", err)
	}
	resp.Body.Close()

	spans := exp.GetSpans()
	if len(spans) == 0 {
		t.Fatal("no spans")
	}
	if spans[0].Status.Code.String() != "Error" {
		t.Errorf("expected span status Error for 500, got %s", spans[0].Status.Code)
	}
}

// TestOTel_SpanCarriesTraceID 验证请求携带的根 span TraceID 与下游收到的一致。
func TestOTel_SpanCarriesTraceID(t *testing.T) {
	setupTestOTel(t)

	var downstreamTraceID string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 下游用 otel propagator 提取 trace context，读取 TraceID
		prop := otel.GetTextMapPropagator()
		ctx := prop.Extract(context.Background(), propagation.HeaderCarrier(r.Header))
		downstreamTraceID = trace.SpanFromContext(ctx).SpanContext().TraceID().String()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p := initProvider(t, config.HTTPClientConfig{})

	rootCtx, rootSpan := otel.Tracer("test").Start(context.Background(), "caller")
	defer rootSpan.End()
	wantTraceID := rootSpan.SpanContext().TraceID().String()

	req, _ := http.NewRequestWithContext(rootCtx, http.MethodGet, srv.URL+"/", nil)
	resp, err := p.client.Do(req)
	if err != nil {
		t.Fatalf("request error: %v", err)
	}
	defer resp.Body.Close()

	if downstreamTraceID != wantTraceID {
		t.Errorf("trace ID:\n  caller:     %s\n  downstream: %s", wantTraceID, downstreamTraceID)
	}
}
