package clog

import (
	"io"
	"log/slog"
	"os"
)

type options struct {
	level     slog.Level
	format    string // "text" | "json"
	output    io.Writer
	addSource bool
	withTrace bool
}

func defaultOptions() *options {
	return &options{
		level:  slog.LevelInfo,
		format: "text",
		output: os.Stdout,
	}
}

// Option 创建 Logger 时的可选配置项
type Option func(*options)

// WithLevel 设置日志级别，默认 INFO
func WithLevel(level slog.Level) Option {
	return func(o *options) {
		o.level = level
	}
}

// WithFormat 设置日志格式："text"（默认）或 "json"
func WithFormat(format string) Option {
	return func(o *options) {
		o.format = format
	}
}

// WithOutput 设置日志输出目标，默认 os.Stdout
func WithOutput(w io.Writer) Option {
	return func(o *options) {
		o.output = w
	}
}

// WithAddSource 是否在日志条目中记录调用源码位置，默认 false
func WithAddSource(v bool) Option {
	return func(o *options) {
		o.addSource = v
	}
}

// WithTrace 是否从 context 中提取 trace/span ID 注入日志条目，默认 false
func WithTrace(v bool) Option {
	return func(o *options) {
		o.withTrace = v
	}
}
