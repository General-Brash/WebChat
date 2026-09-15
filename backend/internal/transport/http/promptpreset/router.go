package promptpreset

import "github.com/gin-gonic/gin"

// RegisterRoutes 注册预制提示词用户侧路由。
func (m *Module) RegisterRoutes(authRequired *gin.RouterGroup) {
	authRequired.GET("/prompt-presets", m.Handler.ListVisiblePromptPresets)
	authRequired.GET("/prompt-presets/mine", m.Handler.ListMyPromptPresets)
	authRequired.POST("/prompt-presets/mine", m.Handler.CreateMyPromptPreset)
	authRequired.PATCH("/prompt-presets/mine/:id", m.Handler.PatchMyPromptPreset)
	authRequired.DELETE("/prompt-presets/mine/:id", m.Handler.DeleteMyPromptPreset)
}

// RegisterAdminRoutes 注册预制提示词管理路由。
func (m *Module) RegisterAdminRoutes(adminGroup *gin.RouterGroup) {
	adminGroup.GET("/prompt-presets", m.Handler.ListAdminPromptPresets)
	adminGroup.POST("/prompt-presets", m.Handler.CreateAdminPromptPreset)
	adminGroup.PATCH("/prompt-presets/:id", m.Handler.PatchAdminPromptPreset)
	adminGroup.DELETE("/prompt-presets/:id", m.Handler.DeleteAdminPromptPreset)
}
