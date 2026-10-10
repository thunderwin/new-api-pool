package middleware

import (
	"net/http"
	"os"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
)

// trustedCredentialOrigins 是允许「带 Cookie 的跨域请求」的 Origin 白名单。
//
// 来源：
//   - FRONTEND_BASE_URL：分域部署时前端所在的地址（例：https://admin.tokwork.com）
//   - TRUSTED_ORIGINS ：额外白名单，逗号分隔（一个后端对多个前端时用）
//
// 只有命中白名单的 Origin 才会被回显成 Access-Control-Allow-Origin 并附带
// Access-Control-Allow-Credentials: true；其余 Origin 一律拿 `*`、不带凭证头。
//
// 为什么必须区分：本项目的登录态是 **session cookie**（见 controller/user.go
// setupLogin + middleware/auth.go authHelper）。浏览器的 CORS 规则里
// 「回显任意 Origin + Allow-Credentials」等于允许任意网站拿受害者的登录 Cookie
// 读接口响应——攻击者开一个页面就能调 /api/token/ 把密钥读走。
// 但中转站的 /v1/* 又要让第三方网页客户端（自带 API Key、不带 Cookie）能直连，
// 所以其它 Origin 保留 `*`。
func trustedCredentialOrigins() map[string]bool {
	set := make(map[string]bool)
	for _, item := range strings.Split(os.Getenv("FRONTEND_BASE_URL")+","+os.Getenv("TRUSTED_ORIGINS"), ",") {
		item = strings.TrimSuffix(strings.TrimSpace(item), "/")
		if item != "" {
			set[item] = true
		}
	}
	return set
}

func CORS() gin.HandlerFunc {
	return func(c *gin.Context) {
		origin := strings.TrimSuffix(strings.TrimSpace(c.GetHeader("Origin")), "/")
		if origin == "" {
			// 同源请求或服务端到服务端调用，不涉及 CORS
			c.Next()
			return
		}

		if trustedCredentialOrigins()[origin] {
			// 凭证模式下允许的 Origin 必须是具体值，不能用 `*`
			c.Header("Access-Control-Allow-Origin", origin)
			c.Header("Access-Control-Allow-Credentials", "true")
			c.Header("Vary", "Origin")
			// ⚠️ 凭证模式下 `Access-Control-Allow-Headers: *` 不再是通配符，
			// 会被当成字面量头名，导致预检报
			//   "Request header field new-api-user is not allowed by
			//    Access-Control-Allow-Headers in preflight response"
			// 所以这里要回显浏览器实际请求的头（axios 会带 New-API-User 等自定义头）。
			if reqHeaders := c.GetHeader("Access-Control-Request-Headers"); reqHeaders != "" {
				c.Header("Access-Control-Allow-Headers", reqHeaders)
			} else {
				c.Header("Access-Control-Allow-Headers", "Authorization,Content-Type,New-API-User,Cache-Control")
			}
		} else {
			c.Header("Access-Control-Allow-Origin", "*")
			c.Header("Access-Control-Allow-Headers", "*")
		}
		c.Header("Access-Control-Allow-Methods", "GET,POST,PUT,DELETE,OPTIONS")
		c.Header("Access-Control-Max-Age", "43200")

		// 只拦真正的预检（带 Access-Control-Request-Method），
		// 普通 OPTIONS 放行给路由自己处理
		if c.Request.Method == http.MethodOptions && c.GetHeader("Access-Control-Request-Method") != "" {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}

func PoweredBy() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("X-New-Api-Version", common.Version)
		c.Next()
	}
}
