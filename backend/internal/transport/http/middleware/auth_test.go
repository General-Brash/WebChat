package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/pkg/token"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/llm"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/shared/requestmeta"
	"github.com/gin-gonic/gin"
)

type authActorTestValidator struct{}

func (authActorTestValidator) ValidateAccessSession(context.Context, uint, string, time.Time, requestmeta.SessionAuditContext) error {
	return nil
}

func TestAuthMiddlewareCapturesVerifiedActorAsTrustedTriggerer(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const secret = "auth-test-secret"
	accessToken, err := token.GenerateWithClaims(token.GenerateClaimsInput{
		Secret:    secret,
		UserID:    11,
		Username:  "actor-a",
		Role:      "user",
		SessionID: "session-a",
		TokenType: "access",
		TTL:       time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	router.Use(AuthMiddleware(secret, authActorTestValidator{}))
	router.GET("/actor", func(c *gin.Context) {
		subject := llm.ExecutionSubjectFromContext(c.Request.Context())
		if subject.TriggererUserID != 11 {
			t.Fatalf("triggerer = %d, want 11", subject.TriggererUserID)
		}
		c.Status(http.StatusNoContent)
	})
	request := httptest.NewRequest(http.MethodGet, "/actor", nil)
	request.Header.Set("Authorization", "Bearer "+accessToken)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusNoContent)
	}
}

func TestAuthMiddlewareRejectsVerifiedActorAgainstStaleContextRoot(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const secret = "auth-test-secret"
	accessToken, err := token.GenerateWithClaims(token.GenerateClaimsInput{
		Secret:    secret,
		UserID:    12,
		Username:  "actor-b",
		Role:      "user",
		SessionID: "session-b",
		TokenType: "access",
		TTL:       time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	staleRoot, err := llm.WithAuthenticatedExecutionSubject(context.Background(), 11, "run-a")
	if err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	router.Use(AuthMiddleware(secret, authActorTestValidator{}))
	router.GET("/actor", func(c *gin.Context) {
		t.Fatal("stale actor context must be rejected before the handler")
	})
	request := httptest.NewRequest(http.MethodGet, "/actor", nil).WithContext(staleRoot)
	request.Header.Set("Authorization", "Bearer "+accessToken)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusUnauthorized)
	}
}
