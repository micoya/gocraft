package tablestore

import (
	"net/http"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

const tracerName = "cdao/tablestore"

// otelTransport 包裹 http.RoundTripper，为每次 TableStore API 调用创建 OTel span。
type otelTransport struct {
	base http.RoundTripper
}

func (t *otelTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	ctx, span := otel.Tracer(tracerName).Start(
		req.Context(),
		"tablestore "+req.Method,
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(
			attribute.String("rpc.system", "ali_tablestore"),
			attribute.String("http.request.method", req.Method),
			attribute.String("server.address", req.URL.Host),
		),
	)
	defer span.End()

	resp, err := t.base.RoundTrip(req.WithContext(ctx))
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return resp, err
	}

	span.SetAttributes(attribute.Int("http.response.status_code", resp.StatusCode))
	if resp.StatusCode >= 400 {
		span.SetStatus(codes.Error, http.StatusText(resp.StatusCode))
	}
	return resp, nil
}
