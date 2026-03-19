package oss

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
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

// ---- parseBucketObject 单元测试 ----

func TestParseBucketObject(t *testing.T) {
	cases := []struct {
		path   string
		bucket string
		object string
	}{
		{"/my-bucket/path/to/file.txt", "my-bucket", "path/to/file.txt"},
		{"/my-bucket/image.jpg", "my-bucket", "image.jpg"},
		{"/my-bucket/", "my-bucket", ""},
		{"/my-bucket", "my-bucket", ""},
		{"/", "", ""},
		{"", "", ""},
	}
	for _, c := range cases {
		b, o := parseBucketObject(c.path)
		if b != c.bucket || o != c.object {
			t.Errorf("parseBucketObject(%q) = (%q, %q), want (%q, %q)",
				c.path, b, o, c.bucket, c.object)
		}
	}
}

// ---- ossOperationName 单元测试 ----

func TestOssOperationName(t *testing.T) {
	cases := []struct {
		method string
		path   string
		query  string
		want   string
	}{
		{http.MethodPut, "/bucket/object.txt", "", "PutObject"},
		{http.MethodPut, "/bucket", "", "PutBucket"},
		{http.MethodPut, "/bucket/object.txt", "acl", "PutObjectACL"},
		{http.MethodPut, "/bucket/object.txt", "tagging", "PutObjectTagging"},
		{http.MethodGet, "/bucket/object.txt", "", "GetObject"},
		{http.MethodGet, "/bucket", "", "ListObjects"},
		{http.MethodGet, "/bucket", "list-type=2", "ListObjectsV2"},
		{http.MethodGet, "/bucket/object.txt", "acl", "GetObjectACL"},
		{http.MethodDelete, "/bucket/object.txt", "", "DeleteObject"},
		{http.MethodDelete, "/bucket", "", "DeleteBucket"},
		{http.MethodHead, "/bucket/object.txt", "", "HeadObject"},
		{http.MethodPost, "/bucket", "delete", "DeleteMultipleObjects"},
		{http.MethodPost, "/bucket/object.txt", "uploads", "InitiateMultipartUpload"},
	}
	for _, c := range cases {
		url := "http://oss.example.com" + c.path
		if c.query != "" {
			url += "?" + c.query
		}
		req, _ := http.NewRequest(c.method, url, nil)
		got := ossOperationName(req)
		if got != c.want {
			t.Errorf("ossOperationName(%s %s?%s) = %q, want %q", c.method, c.path, c.query, got, c.want)
		}
	}
}

// ---- otelTransport 集成测试 ----

// TestOtelTransport_SpanCreated 用 httptest.Server 模拟 OSS 端点，
// 验证每次请求都创建了 span，并且 span 名包含 OSS 操作名。
func TestOtelTransport_SpanCreated(t *testing.T) {
	exp := setupTestTracer(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	transport := &otelTransport{base: http.DefaultTransport}
	client := &http.Client{Transport: transport}

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet,
		srv.URL+"/my-bucket/photo.jpg", nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request error: %v", err)
	}
	defer resp.Body.Close()

	spans := exp.GetSpans()
	if len(spans) == 0 {
		t.Fatal("expected at least one span, got none")
	}

	if !strings.HasPrefix(spans[0].Name, "oss ") {
		t.Errorf("span name = %q, expected prefix 'oss '", spans[0].Name)
	}
}

func TestOtelTransport_BucketObjectAttributes(t *testing.T) {
	exp := setupTestTracer(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	transport := &otelTransport{base: http.DefaultTransport}
	client := &http.Client{Transport: transport}

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPut,
		srv.URL+"/prod-bucket/images/avatar.png", nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request error: %v", err)
	}
	defer resp.Body.Close()

	spans := exp.GetSpans()
	if len(spans) == 0 {
		t.Fatal("no spans captured")
	}

	attrMap := make(map[string]string)
	for _, a := range spans[0].Attributes {
		attrMap[string(a.Key)] = a.Value.AsString()
	}

	if attrMap["oss.bucket"] != "prod-bucket" {
		t.Errorf("oss.bucket = %q, want %q", attrMap["oss.bucket"], "prod-bucket")
	}
	if attrMap["oss.object"] != "images/avatar.png" {
		t.Errorf("oss.object = %q, want %q", attrMap["oss.object"], "images/avatar.png")
	}
	if attrMap["http.request.method"] != http.MethodPut {
		t.Errorf("http.request.method = %q, want %q", attrMap["http.request.method"], http.MethodPut)
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

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet,
		srv.URL+"/bucket/file.txt", nil)
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
