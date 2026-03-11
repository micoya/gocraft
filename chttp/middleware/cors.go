package middleware

import (
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"

	"github.com/micoya/gocraft/config"
)

// CORS 根据 CORSConfig 返回跨域中间件。
func CORS(cfg *config.CORSConfig) gin.HandlerFunc {
	return cors.New(cors.Config{
		AllowAllOrigins:  cfg.AllowAllOrigins,
		AllowOrigins:     cfg.AllowOrigins,
		AllowMethods:     cfg.AllowMethods,
		AllowHeaders:     cfg.AllowHeaders,
		AllowCredentials: cfg.AllowCredentials,
		MaxAge:           cfg.MaxAge,
	})
}
