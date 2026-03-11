package middleware

import (
	"log/slog"
	"time"

	"github.com/gin-gonic/gin"
)

// AccessLog 返回访问日志中间件，记录每个请求的基本信息。
func AccessLog(log *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path
		query := c.Request.URL.RawQuery

		c.Next()

		attrs := []slog.Attr{
			slog.String("method", c.Request.Method),
			slog.String("path", path),
			slog.Int("status", c.Writer.Status()),
			slog.Duration("latency", time.Since(start)),
			slog.String("client_ip", c.ClientIP()),
			slog.Int("body_size", c.Writer.Size()),
		}
		if query != "" {
			attrs = append(attrs, slog.String("query", query))
		}

		log.LogAttrs(c.Request.Context(), slog.LevelInfo, "access", attrs...)
	}
}
