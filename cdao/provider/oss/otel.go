package oss

import (
	"net/http"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

const ossTracerName = "cdao/oss"

// otelTransport 包裹 http.RoundTripper，为每次 OSS API 调用创建 OTel span。
// OSS REST API 通过 HTTP Method + 路径参数标识操作，
// span 名为 "oss {METHOD}"，并记录 bucket、object、状态码等属性。
type otelTransport struct {
	base http.RoundTripper
}

func (t *otelTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	op := ossOperationName(req)
	ctx, span := otel.Tracer(ossTracerName).Start(
		req.Context(),
		"oss "+op,
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(
			attribute.String("rpc.system", "alioss"),
			attribute.String("http.request.method", req.Method),
			attribute.String("server.address", req.URL.Host),
		),
	)
	defer span.End()

	// 从路径拆分 bucket / object（OSS 路径格式：/{bucket}/{object...}）
	if bucket, object := parseBucketObject(req.URL.Path); bucket != "" {
		span.SetAttributes(attribute.String("oss.bucket", bucket))
		if object != "" {
			span.SetAttributes(attribute.String("oss.object", object))
		}
	}

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

// ossOperationName 根据 HTTP 方法和查询参数推断 OSS 操作名。
// OSS 通过 query 参数区分对同一资源的不同操作，例如 ?acl、?tagging 等。
func ossOperationName(req *http.Request) string {
	q := req.URL.RawQuery
	switch req.Method {
	case http.MethodPut:
		switch {
		case containsParam(q, "acl"):
			return "PutObjectACL"
		case containsParam(q, "tagging"):
			return "PutObjectTagging"
		case containsParam(q, "symlink"):
			return "PutSymlink"
		default:
			_, obj := parseBucketObject(req.URL.Path)
			if obj == "" {
				return "PutBucket"
			}
			return "PutObject"
		}
	case http.MethodGet:
		switch {
		case containsParam(q, "acl"):
			return "GetObjectACL"
		case containsParam(q, "tagging"):
			return "GetObjectTagging"
		case containsParam(q, "list-type"):
			return "ListObjectsV2"
		default:
			_, obj := parseBucketObject(req.URL.Path)
			if obj == "" {
				return "ListObjects"
			}
			return "GetObject"
		}
	case http.MethodDelete:
		_, obj := parseBucketObject(req.URL.Path)
		if obj == "" {
			return "DeleteBucket"
		}
		return "DeleteObject"
	case http.MethodHead:
		return "HeadObject"
	case http.MethodPost:
		if containsParam(q, "delete") {
			return "DeleteMultipleObjects"
		}
		if containsParam(q, "uploads") {
			return "InitiateMultipartUpload"
		}
		return "PostObject"
	default:
		return req.Method
	}
}

func containsParam(rawQuery, key string) bool {
	for _, kv := range splitQuery(rawQuery) {
		if kv == key || len(kv) > len(key) && kv[:len(key)+1] == key+"=" {
			return true
		}
	}
	return false
}

func splitQuery(rawQuery string) []string {
	if rawQuery == "" {
		return nil
	}
	var parts []string
	start := 0
	for i := 0; i <= len(rawQuery); i++ {
		if i == len(rawQuery) || rawQuery[i] == '&' {
			parts = append(parts, rawQuery[start:i])
			start = i + 1
		}
	}
	return parts
}

// parseBucketObject 从 OSS 路径 /{bucket}/{object...} 中提取 bucket 和 object 名。
func parseBucketObject(path string) (bucket, object string) {
	if len(path) == 0 || path == "/" {
		return "", ""
	}
	path = path[1:] // 去掉前缀 /
	idx := indexOf(path, '/')
	if idx < 0 {
		return path, ""
	}
	return path[:idx], path[idx+1:]
}

func indexOf(s string, b byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return i
		}
	}
	return -1
}
