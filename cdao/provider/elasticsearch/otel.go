package elasticsearch

import (
	"fmt"
	"net/http"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

const tracerName = "cdao/elasticsearch"

// otelTransport 包装 http.RoundTripper，为每次 Elasticsearch HTTP 请求
// 创建 OTel client span，记录方法、URL、状态码等属性。
type otelTransport struct {
	base http.RoundTripper
}

func newOtelTransport(base http.RoundTripper) *otelTransport {
	if base == nil {
		base = http.DefaultTransport
	}
	return &otelTransport{base: base}
}

func (t *otelTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	ctx, span := otel.Tracer(tracerName).Start(
		req.Context(),
		fmt.Sprintf("elasticsearch %s %s", req.Method, req.URL.Path),
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(
			attribute.String("db.system", "elasticsearch"),
			attribute.String("http.request.method", req.Method),
			attribute.String("url.full", req.URL.String()),
			attribute.String("server.address", req.URL.Host),
		),
	)
	defer span.End()

	resp, err := t.base.RoundTrip(req.WithContext(ctx))
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}

	span.SetAttributes(attribute.Int("http.response.status_code", resp.StatusCode))
	if resp.StatusCode >= 400 {
		span.SetStatus(codes.Error, resp.Status)
	}
	return resp, nil
}
