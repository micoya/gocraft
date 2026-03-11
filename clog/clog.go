package clog

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"

	oteltrace "go.opentelemetry.io/otel/trace"

	"github.com/micoya/gocraft/config"
)

type traceContextKey struct{}

// TraceInfo 用于日志注入的链路追踪信息。
type TraceInfo struct {
	TraceID string
	SpanID  string
}

// ContextWithTrace 将 TraceInfo 写入 ctx，后续通过该 ctx 打日志时会自动注入 trace_id/span_id。
func ContextWithTrace(ctx context.Context, info TraceInfo) context.Context {
	return context.WithValue(ctx, traceContextKey{}, info)
}

// New 使用 Option 创建 *slog.Logger。
func New(opts ...Option) *slog.Logger {
	o := defaultOptions()
	for _, opt := range opts {
		opt(o)
	}
	return build(o)
}

// NewFromConfig 从 config.LogConfig 创建 *slog.Logger。
// Path 支持 "stdout"（默认）、"stderr" 或具体文件路径（以追加模式打开）。
func NewFromConfig(cfg *config.LogConfig) (*slog.Logger, error) {
	o := defaultOptions()

	if cfg.Level != "" {
		var level slog.Level
		if err := level.UnmarshalText([]byte(cfg.Level)); err != nil {
			return nil, fmt.Errorf("clog: invalid log level %q: %w", cfg.Level, err)
		}
		o.level = level
	}

	if cfg.Format != "" {
		o.format = cfg.Format
	}

	o.withTrace = cfg.WithTrace
	o.addSource = cfg.AddSource

	w, err := openWriter(cfg.Path)
	if err != nil {
		return nil, fmt.Errorf("clog: open log output: %w", err)
	}
	o.output = w

	return build(o), nil
}

func build(o *options) *slog.Logger {
	ho := &slog.HandlerOptions{
		Level:     o.level,
		AddSource: o.addSource,
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			if a.Key == slog.SourceKey {
				if src, ok := a.Value.Any().(*slog.Source); ok {
					src.File = filepath.Base(src.File)
				}
			}
			return a
		},
	}
	var h slog.Handler
	if o.format == "json" {
		h = slog.NewJSONHandler(o.output, ho)
	} else {
		h = slog.NewTextHandler(o.output, ho)
	}
	if o.withTrace {
		h = &traceHandler{Handler: h}
	}
	return slog.New(h)
}

func openWriter(path string) (io.Writer, error) {
	switch path {
	case "", "stdout":
		return os.Stdout, nil
	case "stderr":
		return os.Stderr, nil
	default:
		f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			return nil, err
		}
		return f, nil
	}
}

// traceHandler 是 slog.Handler 的包装，负责从 context 提取 TraceInfo 并注入日志属性。
type traceHandler struct {
	slog.Handler
}

func (h *traceHandler) Handle(ctx context.Context, r slog.Record) error {
	spanCtx := oteltrace.SpanContextFromContext(ctx)
	if spanCtx.IsValid() {
		r.AddAttrs(
			slog.String("trace_id", spanCtx.TraceID().String()),
			slog.String("span_id", spanCtx.SpanID().String()),
		)
	} else if info, ok := ctx.Value(traceContextKey{}).(TraceInfo); ok {
		if info.TraceID != "" {
			r.AddAttrs(slog.String("trace_id", info.TraceID))
		}
		if info.SpanID != "" {
			r.AddAttrs(slog.String("span_id", info.SpanID))
		}
	}
	return h.Handler.Handle(ctx, r)
}

func (h *traceHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &traceHandler{Handler: h.Handler.WithAttrs(attrs)}
}

func (h *traceHandler) WithGroup(name string) slog.Handler {
	return &traceHandler{Handler: h.Handler.WithGroup(name)}
}
