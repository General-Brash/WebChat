package sub2

import (
	"testing"

	"github.com/gin-gonic/gin"
)

func TestRegisterRoutesIncludesServiceSettlementQueryOperation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	NewModule(nil).RegisterRoutes(r.Group("/api/v1"))
	for _, route := range r.Routes() {
		if route.Method == "POST" && route.Path == "/api/v1/sub2/services-query" {
			return
		}
	}
	t.Fatal("services-query runtime operation was not registered")
}
