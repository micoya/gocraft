package middleware

import (
	"log/slog"
	"net/http"
	"runtime/debug"

	"github.com/gin-gonic/gin"
)

// Recovery 返回 panic 恢复中间件，捕获 handler 链中的 panic 并记录堆栈后返回 500。
func Recovery(log *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if err := recover(); err != nil {
				stack := debug.Stack()
				log.Error("http panic recovered",
					"error", err,
					"method", c.Request.Method,
					"path", c.Request.URL.Path,
					"stack", string(stack),
				)
				c.AbortWithStatus(http.StatusInternalServerError)
			}
		}()
		c.Next()
	}
}
