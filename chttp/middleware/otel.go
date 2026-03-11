package middleware

import (
	"time"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
)

// TraceID 从 OTel span context 读取 trace ID 并写入 X-Trace-ID 响应头。
// 需注册在 otelgin 中间件之后，使 span 已创建。
func TraceID() gin.HandlerFunc {
	return func(c *gin.Context) {
		spanCtx := trace.SpanContextFromContext(c.Request.Context())
		if spanCtx.IsValid() {
			c.Header("X-Trace-ID", spanCtx.TraceID().String())
		}
		c.Next()
	}
}

// HTTPMetrics 使用 OTel Meter API 记录 HTTP 请求指标，遵循 OpenTelemetry HTTP 语义约定。
// 记录 http.server.request.duration（histogram）和 http.server.active_requests（gauge）。
func HTTPMetrics(meter metric.Meter) gin.HandlerFunc {
	duration, _ := meter.Float64Histogram("http.server.request.duration",
		metric.WithUnit("s"),
		metric.WithDescription("Duration of HTTP server requests"),
	)
	active, _ := meter.Int64UpDownCounter("http.server.active_requests",
		metric.WithDescription("Number of active HTTP server requests"),
	)

	return func(c *gin.Context) {
		methodAttr := semconv.HTTPRequestMethodKey.String(c.Request.Method)
		active.Add(c.Request.Context(), 1, metric.WithAttributes(methodAttr))

		start := time.Now()
		c.Next()
		elapsed := time.Since(start).Seconds()

		attrs := []attribute.KeyValue{
			methodAttr,
			semconv.HTTPResponseStatusCode(c.Writer.Status()),
		}
		if route := c.FullPath(); route != "" {
			attrs = append(attrs, semconv.HTTPRoute(route))
		}

		active.Add(c.Request.Context(), -1, metric.WithAttributes(methodAttr))
		duration.Record(c.Request.Context(), elapsed, metric.WithAttributes(attrs...))
	}
}
