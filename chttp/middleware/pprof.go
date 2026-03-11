package middleware

import (
	"net"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/micoya/gocraft/config"
)

// PprofGuard 返回 pprof 路由组的守卫中间件。
// 根据配置控制：仅允许本地访问（AllowExternal=false）和 Authorization token 鉴权。
func PprofGuard(cfg *config.PprofConfig) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !cfg.AllowExternal && !isLoopback(c.ClientIP()) {
			c.AbortWithStatus(http.StatusForbidden)
			return
		}

		if cfg.AuthorizationToken != "" {
			if c.GetHeader("Authorization") != cfg.AuthorizationToken {
				c.AbortWithStatus(http.StatusUnauthorized)
				return
			}
		}

		c.Next()
	}
}

func isLoopback(ip string) bool {
	parsed := net.ParseIP(ip)
	return parsed != nil && parsed.IsLoopback()
}
