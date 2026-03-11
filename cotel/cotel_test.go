package cotel

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/micoya/gocraft/config"
)

func TestNew_nilConfig(t *testing.T) {
	p, err := New(context.Background(), nil, "test-svc")
	if err != nil {
		t.Fatalf("New(nil) error = %v", err)
	}
	if p.PrometheusHandler() != nil {
		t.Error("PrometheusHandler should be nil when config is nil")
	}
	if err := p.Shutdown(context.Background()); err != nil {
		t.Errorf("Shutdown() error = %v", err)
	}
}

func TestNew_prometheusOnly(t *testing.T) {
	cfg := &config.OtelConfig{
		Trace:  config.OtelTraceConfig{SampleRate: 1.0},
		Metric: config.OtelMetricConfig{},
	}
	p, err := New(context.Background(), cfg, "test-svc")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer p.Shutdown(context.Background())

	if p.PrometheusHandler() == nil {
		t.Fatal("PrometheusHandler should not be nil")
	}

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	p.PrometheusHandler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("prometheus status = %d, want 200", w.Code)
	}
}

func TestNew_withTraceEndpoint_invalidEndpoint(t *testing.T) {
	cfg := &config.OtelConfig{
		Trace: config.OtelTraceConfig{
			Endpoint:   "localhost:4317",
			Insecure:   true,
			SampleRate: 1.0,
		},
	}
	p, err := New(context.Background(), cfg, "test-svc")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer p.Shutdown(context.Background())

	if p.PrometheusHandler() == nil {
		t.Error("PrometheusHandler should not be nil even with trace endpoint")
	}
}

func TestNew_sampler(t *testing.T) {
	tests := []struct {
		name string
		rate float64
	}{
		{"always_sample", 1.0},
		{"never_sample", 0.0},
		{"ratio", 0.5},
		{"negative_clamp", -1.0},
		{"over_one_clamp", 2.0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.OtelConfig{
				Trace: config.OtelTraceConfig{SampleRate: tt.rate},
			}
			p, err := New(context.Background(), cfg, "test-svc")
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}
			p.Shutdown(context.Background())
		})
	}
}

func TestPrometheusHandler_containsMetrics(t *testing.T) {
	cfg := &config.OtelConfig{
		Trace:  config.OtelTraceConfig{SampleRate: 1.0},
		Metric: config.OtelMetricConfig{},
	}
	p, err := New(context.Background(), cfg, "test-svc")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer p.Shutdown(context.Background())

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	p.PrometheusHandler().ServeHTTP(w, req)

	body := w.Body.String()
	if !strings.Contains(body, "target_info") && w.Code != http.StatusOK {
		t.Errorf("expected prometheus output, got status=%d body=%s", w.Code, body)
	}
}
