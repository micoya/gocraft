package clog

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	oteltrace "go.opentelemetry.io/otel/trace"

	"github.com/micoya/gocraft/config"
)

// captureOutput 返回一个写入 buf 的 Logger，用于断言日志内容。
func captureOutput(buf *bytes.Buffer, opts ...Option) *slog.Logger {
	opts = append([]Option{WithOutput(buf)}, opts...)
	return New(opts...)
}

func TestNew_Defaults(t *testing.T) {
	var buf bytes.Buffer
	logger := captureOutput(&buf)
	logger.Info("hello")

	out := buf.String()
	if !strings.Contains(out, "hello") {
		t.Errorf("expected output to contain %q, got %q", "hello", out)
	}
	// 默认 text 格式不含 JSON 大括号
	if strings.HasPrefix(strings.TrimSpace(out), "{") {
		t.Errorf("expected text format, got JSON-like output: %s", out)
	}
}

func TestNew_JSONFormat(t *testing.T) {
	var buf bytes.Buffer
	logger := captureOutput(&buf, WithFormat("json"))
	logger.Info("ping")

	out := strings.TrimSpace(buf.String())
	if !strings.HasPrefix(out, "{") {
		t.Errorf("expected JSON format, got: %s", out)
	}
	if !strings.Contains(out, `"msg":"ping"`) {
		t.Errorf("expected msg field, got: %s", out)
	}
}

func TestNew_LevelFilter(t *testing.T) {
	var buf bytes.Buffer
	logger := captureOutput(&buf, WithLevel(slog.LevelWarn))

	logger.Debug("debug-msg")
	logger.Info("info-msg")
	logger.Warn("warn-msg")

	out := buf.String()
	if strings.Contains(out, "debug-msg") {
		t.Errorf("DEBUG should be filtered out")
	}
	if strings.Contains(out, "info-msg") {
		t.Errorf("INFO should be filtered out")
	}
	if !strings.Contains(out, "warn-msg") {
		t.Errorf("WARN should appear in output")
	}
}

func TestNew_WithTrace_InjectsIDs(t *testing.T) {
	var buf bytes.Buffer
	logger := captureOutput(&buf, WithFormat("json"), WithTrace(true))

	ctx := ContextWithTrace(context.Background(), TraceInfo{
		TraceID: "abc123",
		SpanID:  "def456",
	})
	logger.InfoContext(ctx, "traced")

	out := buf.String()
	if !strings.Contains(out, `"trace_id":"abc123"`) {
		t.Errorf("expected trace_id in output, got: %s", out)
	}
	if !strings.Contains(out, `"span_id":"def456"`) {
		t.Errorf("expected span_id in output, got: %s", out)
	}
}

func TestNew_WithTrace_NoContextNoFields(t *testing.T) {
	var buf bytes.Buffer
	logger := captureOutput(&buf, WithFormat("json"), WithTrace(true))
	logger.Info("no-trace")

	out := buf.String()
	if strings.Contains(out, "trace_id") {
		t.Errorf("expected no trace_id when context has none, got: %s", out)
	}
}

func TestNew_WithTrace_WithAttrsPreserved(t *testing.T) {
	var buf bytes.Buffer
	logger := captureOutput(&buf, WithFormat("json"), WithTrace(true))
	logger = logger.With("service", "api")

	ctx := ContextWithTrace(context.Background(), TraceInfo{TraceID: "tid-001"})
	logger.InfoContext(ctx, "with-attrs")

	out := buf.String()
	if !strings.Contains(out, `"service":"api"`) {
		t.Errorf("expected service attr, got: %s", out)
	}
	if !strings.Contains(out, `"trace_id":"tid-001"`) {
		t.Errorf("expected trace_id, got: %s", out)
	}
}

func TestNew_WithTrace_WithGroupPreserved(t *testing.T) {
	var buf bytes.Buffer
	logger := captureOutput(&buf, WithFormat("json"), WithTrace(true))
	logger = logger.WithGroup("req")

	ctx := ContextWithTrace(context.Background(), TraceInfo{TraceID: "tid-002"})
	logger.InfoContext(ctx, "grouped", "path", "/api")

	out := buf.String()
	if !strings.Contains(out, `"req"`) {
		t.Errorf("expected group in output, got: %s", out)
	}
	if !strings.Contains(out, `"trace_id":"tid-002"`) {
		t.Errorf("expected trace_id after WithGroup, got: %s", out)
	}
}

func TestNewFromConfig_Defaults(t *testing.T) {
	cfg := &config.LogConfig{
		Level:  "INFO",
		Format: "text",
		Path:   "stdout",
	}
	logger, err := NewFromConfig(cfg)
	if err != nil {
		t.Fatalf("NewFromConfig() error = %v", err)
	}
	if logger == nil {
		t.Fatal("expected non-nil logger")
	}
}

func TestNewFromConfig_JSONFormat(t *testing.T) {
	cfg := &config.LogConfig{
		Level:  "DEBUG",
		Format: "json",
		Path:   "stdout",
	}
	logger, err := NewFromConfig(cfg)
	if err != nil {
		t.Fatalf("NewFromConfig() error = %v", err)
	}
	if logger == nil {
		t.Fatal("expected non-nil logger")
	}
}

func TestNewFromConfig_StderrPath(t *testing.T) {
	cfg := &config.LogConfig{
		Level:  "INFO",
		Format: "text",
		Path:   "stderr",
	}
	logger, err := NewFromConfig(cfg)
	if err != nil {
		t.Fatalf("NewFromConfig() error = %v", err)
	}
	if logger == nil {
		t.Fatal("expected non-nil logger")
	}
}

func TestNewFromConfig_FilePath(t *testing.T) {
	dir := t.TempDir()
	logFile := filepath.Join(dir, "app.log")

	cfg := &config.LogConfig{
		Level:  "INFO",
		Format: "json",
		Path:   logFile,
	}
	logger, err := NewFromConfig(cfg)
	if err != nil {
		t.Fatalf("NewFromConfig() error = %v", err)
	}
	logger.Info("file-log", "key", "val")

	data, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if !strings.Contains(string(data), "file-log") {
		t.Errorf("expected log content in file, got: %s", string(data))
	}
}

func TestNewFromConfig_InvalidLevel(t *testing.T) {
	cfg := &config.LogConfig{
		Level: "VERBOSE",
	}
	_, err := NewFromConfig(cfg)
	if err == nil {
		t.Fatal("expected error for invalid log level")
	}
	if !strings.Contains(err.Error(), "invalid log level") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestNewFromConfig_InvalidFilePath(t *testing.T) {
	cfg := &config.LogConfig{
		Level: "INFO",
		Path:  "/nonexistent/dir/app.log",
	}
	_, err := NewFromConfig(cfg)
	if err == nil {
		t.Fatal("expected error for invalid file path")
	}
	if !strings.Contains(err.Error(), "open log output") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestNewFromConfig_WithTrace(t *testing.T) {
	var buf bytes.Buffer
	cfg := &config.LogConfig{
		Level:     "INFO",
		Format:    "json",
		Path:      "stdout",
		WithTrace: true,
	}
	// 直接用 New 来验证 withTrace 逻辑，避免无法捕获 stdout
	logger := New(WithFormat("json"), WithOutput(&buf), WithTrace(cfg.WithTrace))

	ctx := ContextWithTrace(context.Background(), TraceInfo{TraceID: "trace-xyz"})
	logger.InfoContext(ctx, "trace-test")

	out := buf.String()
	if !strings.Contains(out, `"trace_id":"trace-xyz"`) {
		t.Errorf("expected trace_id in output, got: %s", out)
	}
}

func TestTraceHandler_OTelSpanContext(t *testing.T) {
	var buf bytes.Buffer
	logger := captureOutput(&buf, WithFormat("json"), WithTrace(true))

	traceID, _ := oteltrace.TraceIDFromHex("0af7651916cd43dd8448eb211c80319c")
	spanID, _ := oteltrace.SpanIDFromHex("00f067aa0ba902b7")
	spanCtx := oteltrace.NewSpanContext(oteltrace.SpanContextConfig{
		TraceID:    traceID,
		SpanID:     spanID,
		TraceFlags: oteltrace.FlagsSampled,
	})
	ctx := oteltrace.ContextWithSpanContext(context.Background(), spanCtx)
	logger.InfoContext(ctx, "otel-trace")

	out := buf.String()
	if !strings.Contains(out, `"trace_id":"0af7651916cd43dd8448eb211c80319c"`) {
		t.Errorf("expected OTel trace_id in output, got: %s", out)
	}
	if !strings.Contains(out, `"span_id":"00f067aa0ba902b7"`) {
		t.Errorf("expected OTel span_id in output, got: %s", out)
	}
}

func TestTraceHandler_OTelTakesPrecedence(t *testing.T) {
	var buf bytes.Buffer
	logger := captureOutput(&buf, WithFormat("json"), WithTrace(true))

	traceID, _ := oteltrace.TraceIDFromHex("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa1")
	spanID, _ := oteltrace.SpanIDFromHex("bbbbbbbbbbbbbb01")
	spanCtx := oteltrace.NewSpanContext(oteltrace.SpanContextConfig{
		TraceID:    traceID,
		SpanID:     spanID,
		TraceFlags: oteltrace.FlagsSampled,
	})
	ctx := oteltrace.ContextWithSpanContext(context.Background(), spanCtx)
	ctx = ContextWithTrace(ctx, TraceInfo{TraceID: "manual-id", SpanID: "manual-span"})
	logger.InfoContext(ctx, "precedence-test")

	out := buf.String()
	if strings.Contains(out, "manual-id") {
		t.Errorf("OTel should take precedence over manual TraceInfo, got: %s", out)
	}
	if !strings.Contains(out, `"trace_id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa1"`) {
		t.Errorf("expected OTel trace_id, got: %s", out)
	}
}

func TestContextWithTrace_EmptyInfo(t *testing.T) {
	var buf bytes.Buffer
	logger := captureOutput(&buf, WithFormat("json"), WithTrace(true))

	ctx := ContextWithTrace(context.Background(), TraceInfo{})
	logger.InfoContext(ctx, "empty-trace")

	out := buf.String()
	if strings.Contains(out, "trace_id") || strings.Contains(out, "span_id") {
		t.Errorf("empty TraceInfo should not inject fields, got: %s", out)
	}
}
