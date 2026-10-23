package router

import (
	"embed"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/QuantumNous/new-api/controller"
	"github.com/QuantumNous/new-api/middleware"

	"github.com/gin-gonic/gin"
)

func SetRouter(router *gin.Engine, buildFS embed.FS, indexPage []byte) {
	SetApiRouter(router)
	SetDashboardRouter(router)
	SetRelayRouter(router)
	SetCapabilityRouter(router)
	SetVideoRouter(router)
	SetPoolRouter(router)

	// FRONTEND_BASE_URL：把「网页」交给外部前端（例如 Cloudflare Pages）托管，
	// 本服务只保留 /api/* 和 /v1/*。
	//
	// 上游原本在 master 节点上强制忽略这个变量（清空并打日志），后果是：
	// 前端部署在 CDN、后端只做 API 的分域部署，在单节点（也是 master）下无法生效，
	// 后端会一直兜底服务嵌入版前端。本 fork 去掉这个特例 —— 设了就生效，
	// 与节点角色无关。
	frontendBaseUrl := os.Getenv("FRONTEND_BASE_URL")
	if frontendBaseUrl == "" {
		SetWebRouter(router, buildFS, indexPage)
	} else {
		frontendBaseUrl = strings.TrimSuffix(frontendBaseUrl, "/")
		router.NoRoute(func(c *gin.Context) {
			c.Set(middleware.RouteTagKey, "web")
			// 接口路径不跳转：跳成 HTML 站点会把 API 客户端带偏。
			// 未注册的 /api/*、/v1/* 仍然返回 JSON 404。
			uri := c.Request.RequestURI
			if strings.HasPrefix(uri, "/v1") || strings.HasPrefix(uri, "/api") {
				controller.RelayNotFound(c)
				return
			}
			c.Redirect(http.StatusMovedPermanently, fmt.Sprintf("%s%s", frontendBaseUrl, c.Request.RequestURI))
		})
	}
}
