package middleware

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	otelmetric "go.opentelemetry.io/otel/sdk/metric"
	tracesdk "go.opentelemetry.io/otel/sdk/trace"

	"github.com/micoya/gocraft/config"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func TestRecovery(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))

	r := gin.New()
	r.Use(Recovery(log))
	r.GET("/panic", func(c *gin.Context) {
		panic("test panic")
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/panic", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", w.Code)
	}
	if !strings.Contains(buf.String(), "test panic") {
		t.Errorf("log should contain panic message, got: %s", buf.String())
	}
}

func TestRecovery_noPanic(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))

	r := gin.New()
	r.Use(Recovery(log))
	r.GET("/ok", func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ok", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	if buf.Len() != 0 {
		t.Errorf("expected no log output, got: %s", buf.String())
	}
}

func TestAccessLog(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))

	r := gin.New()
	r.Use(AccessLog(log))
	r.GET("/hello", func(c *gin.Context) {
		c.String(http.StatusOK, "hello")
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/hello?foo=bar", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}

	logStr := buf.String()
	for _, want := range []string{"GET", "/hello", "foo=bar", "access"} {
		if !strings.Contains(logStr, want) {
			t.Errorf("log should contain %q, got: %s", want, logStr)
		}
	}
}

func TestAccessLog_noQuery(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))

	r := gin.New()
	r.Use(AccessLog(log))
	r.GET("/ping", func(c *gin.Context) {
		c.String(http.StatusOK, "pong")
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ping", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	if strings.Contains(buf.String(), "query") {
		t.Errorf("log should not contain query key when no query string, got: %s", buf.String())
	}
}

func TestPprofGuard_allowLocalhost(t *testing.T) {
	cfg := &config.PprofConfig{Enabled: true}

	r := gin.New()
	r.Use(PprofGuard(cfg))
	r.GET("/debug/pprof/", func(c *gin.Context) {
		c.String(http.StatusOK, "pprof")
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/debug/pprof/", nil)
	req.RemoteAddr = "127.0.0.1:12345"
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 for localhost", w.Code)
	}
}

func TestPprofGuard_blockExternal(t *testing.T) {
	cfg := &config.PprofConfig{Enabled: true}

	r := gin.New()
	r.Use(PprofGuard(cfg))
	r.GET("/debug/pprof/", func(c *gin.Context) {
		c.String(http.StatusOK, "pprof")
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/debug/pprof/", nil)
	req.RemoteAddr = "10.0.0.1:12345"
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403 for external IP", w.Code)
	}
}

func TestPprofGuard_allowExternal(t *testing.T) {
	cfg := &config.PprofConfig{Enabled: true, AllowExternal: true}

	r := gin.New()
	r.Use(PprofGuard(cfg))
	r.GET("/debug/pprof/", func(c *gin.Context) {
		c.String(http.StatusOK, "pprof")
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/debug/pprof/", nil)
	req.RemoteAddr = "10.0.0.1:12345"
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 when AllowExternal=true", w.Code)
	}
}

func TestPprofGuard_authToken_valid(t *testing.T) {
	cfg := &config.PprofConfig{Enabled: true, AllowExternal: true, AuthorizationToken: "Bearer secret"}

	r := gin.New()
	r.Use(PprofGuard(cfg))
	r.GET("/debug/pprof/", func(c *gin.Context) {
		c.String(http.StatusOK, "pprof")
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/debug/pprof/", nil)
	req.Header.Set("Authorization", "Bearer secret")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 with valid token", w.Code)
	}
}

func TestPprofGuard_authToken_missing(t *testing.T) {
	cfg := &config.PprofConfig{Enabled: true, AllowExternal: true, AuthorizationToken: "Bearer secret"}

	r := gin.New()
	r.Use(PprofGuard(cfg))
	r.GET("/debug/pprof/", func(c *gin.Context) {
		c.String(http.StatusOK, "pprof")
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/debug/pprof/", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 without token", w.Code)
	}
}

func TestPprofGuard_authToken_invalid(t *testing.T) {
	cfg := &config.PprofConfig{Enabled: true, AllowExternal: true, AuthorizationToken: "Bearer secret"}

	r := gin.New()
	r.Use(PprofGuard(cfg))
	r.GET("/debug/pprof/", func(c *gin.Context) {
		c.String(http.StatusOK, "pprof")
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/debug/pprof/", nil)
	req.Header.Set("Authorization", "Bearer wrong")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 with wrong token", w.Code)
	}
}

func TestTraceID_withSpan(t *testing.T) {
	r := gin.New()
	r.Use(func(c *gin.Context) {
		ctx, span := tracesdk.NewTracerProvider().Tracer("test").Start(c.Request.Context(), "op")
		c.Request = c.Request.WithContext(ctx)
		defer span.End()
		c.Next()
	})
	r.Use(TraceID())
	r.GET("/api", func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api", nil)
	r.ServeHTTP(w, req)

	traceID := w.Header().Get("X-Trace-ID")
	if traceID == "" {
		t.Error("expected X-Trace-ID header to be set")
	}
	if len(traceID) != 32 {
		t.Errorf("trace ID length = %d, want 32 hex chars", len(traceID))
	}
}

func TestTraceID_noSpan(t *testing.T) {
	r := gin.New()
	r.Use(TraceID())
	r.GET("/api", func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api", nil)
	r.ServeHTTP(w, req)

	if w.Header().Get("X-Trace-ID") != "" {
		t.Error("expected no X-Trace-ID header when no span")
	}
}

func TestHTTPMetrics_noPanic(t *testing.T) {
	meter := otelmetric.NewMeterProvider().Meter("test")

	r := gin.New()
	r.Use(HTTPMetrics(meter))
	r.GET("/api", func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestCORS(t *testing.T) {
	cfg := &config.CORSConfig{
		AllowOrigins: []string{"https://example.com"},
		AllowMethods: []string{"GET", "POST"},
		AllowHeaders: []string{"Authorization"},
		MaxAge:       12 * time.Hour,
	}

	r := gin.New()
	r.Use(CORS(cfg))
	r.GET("/api", func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api", nil)
	req.Header.Set("Origin", "https://example.com")
	req.Host = "other.com"
	r.ServeHTTP(w, req)

	origin := w.Header().Get("Access-Control-Allow-Origin")
	if origin != "https://example.com" {
		t.Errorf("CORS Allow-Origin = %q, want https://example.com", origin)
	}
}

func TestCORS_AllowOriginDomains(t *testing.T) {
	cfg := &config.CORSConfig{
		AllowOriginDomains: []string{"cli.im"},
		AllowMethods:       []string{"GET"},
	}

	r := gin.New()
	r.Use(CORS(cfg))
	r.GET("/api", func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})

	tests := []struct {
		name   string
		origin string
		want   string
	}{
		{"root http", "http://cli.im", "http://cli.im"},
		{"root https", "https://cli.im", "https://cli.im"},
		{"subdomain", "https://aaa.cli.im", "https://aaa.cli.im"},
		{"subdomain with port", "https://aaa.cli.im:8988", "https://aaa.cli.im:8988"},
		{"deep subdomain", "https://a.b.cli.im", "https://a.b.cli.im"},
		{"root with port", "http://cli.im:3000", "http://cli.im:3000"},
		{"not matching suffix", "https://evilcli.im", ""},
		{"completely different", "https://other.com", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/api", nil)
			req.Header.Set("Origin", tt.origin)
			r.ServeHTTP(w, req)

			got := w.Header().Get("Access-Control-Allow-Origin")
			if got != tt.want {
				t.Errorf("origin %q: Allow-Origin = %q, want %q", tt.origin, got, tt.want)
			}
		})
	}
}

func TestCORS_AllowOriginDomainsWithAllowOrigins(t *testing.T) {
	cfg := &config.CORSConfig{
		AllowOrigins:       []string{"https://special.other.com"},
		AllowOriginDomains: []string{"cli.im"},
		AllowMethods:       []string{"GET"},
	}

	r := gin.New()
	r.Use(CORS(cfg))
	r.GET("/api", func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})

	tests := []struct {
		name   string
		origin string
		want   string
	}{
		{"domain match", "https://app.cli.im", "https://app.cli.im"},
		{"exact origin match", "https://special.other.com", "https://special.other.com"},
		{"no match", "https://random.com", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/api", nil)
			req.Header.Set("Origin", tt.origin)
			r.ServeHTTP(w, req)

			got := w.Header().Get("Access-Control-Allow-Origin")
			if got != tt.want {
				t.Errorf("origin %q: Allow-Origin = %q, want %q", tt.origin, got, tt.want)
			}
		})
	}
}
