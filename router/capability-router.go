package router

import (
	"github.com/QuantumNous/new-api/controller"
	"github.com/QuantumNous/new-api/middleware"

	"github.com/gin-gonic/gin"
)

// SetCapabilityRouter exposes non-LLM third-party APIs through the same sk-
// token auth and billing surface as the relay routes.
func SetCapabilityRouter(router *gin.Engine) {
	g := router.Group("/v1")
	g.Use(middleware.RouteTag("relay"))
	g.Use(middleware.SystemPerformanceCheck())
	g.Use(middleware.TokenAuth())
	{
		g.GET("/capabilities", controller.ListCapabilities)
		g.POST("/capabilities/:capability", controller.ExecuteCapability)
		g.POST("/tasks", controller.SubmitCapabilityTask)
		g.GET("/tasks/:id", controller.GetCapabilityTask)
		g.GET("/tasks/:id/content", controller.GetCapabilityTaskContent)
	}
}
