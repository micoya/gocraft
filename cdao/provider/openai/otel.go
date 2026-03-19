package openai

import (
	"net/http"
	"strings"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/openai/openai-go/option"
)

const openaiTracerName = "cdao/openai"

// otelMiddleware 为每次 OpenAI API 调用创建 OTel span。
// 从请求 URL 解析操作名（如 chat.completions、embeddings），
// 记录 HTTP 方法、状态码和响应错误。
func otelMiddleware(req *http.Request, next option.MiddlewareNext) (*http.Response, error) {
	op := operationFromPath(req.URL.Path)
	ctx, span := otel.Tracer(openaiTracerName).Start(
		req.Context(),
		"openai "+op,
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(
			attribute.String("gen_ai.system", "openai"),
			attribute.String("gen_ai.operation.name", op),
			attribute.String("http.request.method", req.Method),
			attribute.String("server.address", req.URL.Host),
		),
	)
	defer span.End()

	resp, err := next(req.WithContext(ctx))
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

// operationFromPath 从 URL 路径提取可读的操作名。
// 例：/v1/chat/completions → chat.completions，/v1/embeddings → embeddings。
func operationFromPath(path string) string {
	path = strings.TrimPrefix(path, "/v1/")
	path = strings.TrimPrefix(path, "/")
	// 取最多两段路径，用 "." 连接，使其更具可读性
	parts := strings.SplitN(path, "/", 3)
	if len(parts) >= 2 {
		return parts[0] + "." + parts[1]
	}
	if len(parts) == 1 && parts[0] != "" {
		return parts[0]
	}
	return "unknown"
}
