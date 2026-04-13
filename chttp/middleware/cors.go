package middleware

import (
	"net/url"
	"strings"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"

	"github.com/micoya/gocraft/config"
)

// CORS 根据 CORSConfig 返回跨域中间件。
func CORS(cfg *config.CORSConfig) gin.HandlerFunc {
	corsCfg := cors.Config{
		AllowAllOrigins:  cfg.AllowAllOrigins,
		AllowOrigins:     cfg.AllowOrigins,
		AllowMethods:     cfg.AllowMethods,
		AllowHeaders:     cfg.AllowHeaders,
		AllowCredentials: cfg.AllowCredentials,
		MaxAge:           cfg.MaxAge,
	}

	if len(cfg.AllowOriginDomains) > 0 {
		// AllowOriginFunc 启用后 gin-contrib/cors 会忽略 AllowOrigins，
		// 因此需要将精确列表也合并进 func。
		origins := make(map[string]bool, len(cfg.AllowOrigins))
		for _, o := range cfg.AllowOrigins {
			origins[o] = true
		}

		corsCfg.AllowOrigins = nil
		corsCfg.AllowOriginFunc = func(origin string) bool {
			if origins[origin] {
				return true
			}
			return matchOriginDomains(origin, cfg.AllowOriginDomains)
		}
	}

	return cors.New(corsCfg)
}

// matchOriginDomains 判断 origin 的 host 是否匹配 domains 中任一域名（含子域名）。
func matchOriginDomains(origin string, domains []string) bool {
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	host := u.Hostname()
	for _, d := range domains {
		if host == d || strings.HasSuffix(host, "."+d) {
			return true
		}
	}
	return false
}
