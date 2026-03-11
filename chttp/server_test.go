package chttp

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/micoya/gocraft/config"
	"github.com/micoya/gocraft/cotel"
)

func TestNew_defaults(t *testing.T) {
	s := New()
	if s.Engine() == nil {
		t.Fatal("engine should not be nil")
	}
	if s.cfg.Addr != ":8080" {
		t.Errorf("default addr = %q, want :8080", s.cfg.Addr)
	}
	if s.cfg.HealthPath != "/healthz" {
		t.Errorf("default health_path = %q, want /healthz", s.cfg.HealthPath)
	}
}

func TestNew_withOptions(t *testing.T) {
	cfg := &config.HTTPServerConfig{
		Addr:            ":9090",
		ShutdownTimeout: 10 * time.Second,
		ReadTimeout:     5 * time.Second,
		WriteTimeout:    5 * time.Second,
		IdleTimeout:     30 * time.Second,
	}
	log := slog.Default()
	s := New(WithServerConfig(cfg), WithLogger(log))

	if s.cfg.Addr != ":9090" {
		t.Errorf("addr = %q, want :9090", s.cfg.Addr)
	}
	if s.cfg.ShutdownTimeout != 10*time.Second {
		t.Errorf("shutdown_timeout = %v, want 10s", s.cfg.ShutdownTimeout)
	}
}

func TestNew_healthEndpoint(t *testing.T) {
	s := New()

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	s.Engine().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("health status = %d, want 200", w.Code)
	}
	if w.Body.String() != "ok" {
		t.Errorf("health body = %q, want ok", w.Body.String())
	}
}

func TestNew_corsMiddleware(t *testing.T) {
	cfg := &config.HTTPServerConfig{
		Addr: ":8080",
		CORS: &config.CORSConfig{
			AllowAllOrigins: true,
		},
		AccessLog: config.AccessLogConfig{Enabled: false},
	}
	s := New(WithServerConfig(cfg))

	s.Engine().GET("/test", func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Origin", "https://other.com")
	s.Engine().ServeHTTP(w, req)

	if w.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Errorf("CORS header = %q, want *", w.Header().Get("Access-Control-Allow-Origin"))
	}
}

func TestNew_pprofEnabled(t *testing.T) {
	s := New(WithServerConfig(&config.HTTPServerConfig{
		Addr:  ":8080",
		Pprof: config.PprofConfig{Enabled: true, AllowExternal: true},
	}))

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/debug/pprof/", nil)
	s.Engine().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("pprof status = %d, want 200", w.Code)
	}
}

func TestNew_pprofDisabled(t *testing.T) {
	s := New(WithServerConfig(&config.HTTPServerConfig{
		Addr:  ":8080",
		Pprof: config.PprofConfig{Enabled: false},
	}))

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/debug/pprof/", nil)
	s.Engine().ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("pprof status = %d, want 404 when disabled", w.Code)
	}
}

func TestNew_withOtelProvider_metricsEndpoint(t *testing.T) {
	otelCfg := &config.OtelConfig{
		Trace:  config.OtelTraceConfig{SampleRate: 1.0},
		Metric: config.OtelMetricConfig{},
	}
	p, err := cotel.New(context.Background(), otelCfg, "test-svc")
	if err != nil {
		t.Fatalf("cotel.New() error = %v", err)
	}
	defer p.Shutdown(context.Background())

	s := New(
		WithServerConfig(&config.HTTPServerConfig{
			Addr:        ":8080",
			MetricsPath: "/metrics",
		}),
		WithOtelProvider(p),
	)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	s.Engine().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("metrics status = %d, want 200", w.Code)
	}
}

func TestNew_withOtelProvider_traceIDHeader(t *testing.T) {
	otelCfg := &config.OtelConfig{
		Trace:  config.OtelTraceConfig{SampleRate: 1.0},
		Metric: config.OtelMetricConfig{},
	}
	p, err := cotel.New(context.Background(), otelCfg, "test-svc")
	if err != nil {
		t.Fatalf("cotel.New() error = %v", err)
	}
	defer p.Shutdown(context.Background())

	s := New(
		WithServerConfig(&config.HTTPServerConfig{
			Addr:        ":8080",
			MetricsPath: "/metrics",
		}),
		WithOtelProvider(p),
	)

	s.Engine().GET("/api/test", func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	s.Engine().ServeHTTP(w, req)

	traceID := w.Header().Get("X-Trace-ID")
	if traceID == "" {
		t.Error("expected X-Trace-ID response header")
	}
	if len(traceID) != 32 {
		t.Errorf("X-Trace-ID length = %d, want 32", len(traceID))
	}
}

func TestNew_withOtelProvider_prometheusContent(t *testing.T) {
	otelCfg := &config.OtelConfig{
		Trace:  config.OtelTraceConfig{SampleRate: 1.0},
		Metric: config.OtelMetricConfig{},
	}
	p, err := cotel.New(context.Background(), otelCfg, "test-svc")
	if err != nil {
		t.Fatalf("cotel.New() error = %v", err)
	}
	defer p.Shutdown(context.Background())

	s := New(
		WithServerConfig(&config.HTTPServerConfig{
			Addr:        ":8080",
			MetricsPath: "/metrics",
		}),
		WithOtelProvider(p),
	)

	s.Engine().GET("/api/hello", func(c *gin.Context) {
		c.String(http.StatusOK, "hello")
	})

	// Make a request to generate metrics
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/hello", nil)
	s.Engine().ServeHTTP(w, req)

	// Now check Prometheus metrics endpoint
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/metrics", nil)
	s.Engine().ServeHTTP(w, req)

	body := w.Body.String()
	if !strings.Contains(body, "http_server_request_duration") {
		t.Errorf("expected http_server_request_duration metric in prometheus output, got: %s", body)
	}
}

func TestNew_noOtelProvider_noMetricsEndpoint(t *testing.T) {
	s := New(WithServerConfig(&config.HTTPServerConfig{
		Addr:        ":8080",
		MetricsPath: "/metrics",
	}))

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	s.Engine().ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("metrics status = %d, want 404 when no otel provider", w.Code)
	}
}

func TestRun_gracefulShutdown(t *testing.T) {
	s := New(WithServerConfig(&config.HTTPServerConfig{
		Addr:            ":0",
		ShutdownTimeout: 5 * time.Second,
		AccessLog:       config.AccessLogConfig{Enabled: false},
	}))

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- s.Run(ctx)
	}()

	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Run() = %v, want nil", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timeout waiting for Run to return")
	}
}
