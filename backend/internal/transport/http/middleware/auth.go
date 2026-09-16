package middleware

import (
	"context"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/llm"
	"net/http"
	"strings"
	"time"

	domainuser "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/user"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/pkg/token"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/shared/requestmeta"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/shared/response"
	"github.com/gin-gonic/gin"
)

// SessionValidator 校验 access token 对应会话是否有效。
type SessionValidator interface {
	ValidateAccessSession(
		ctx context.Context,
		userID uint,
		sessionID string,
		accessIssuedAt time.Time,
		auditCtx requestmeta.SessionAuditContext,
	) error
}

// AuthoritativeRoleResolver lets the auth service replace a stale JWT role
// after the session has already passed local token/session validation. It is
// optional so existing validators and test doubles keep satisfying the
// original SessionValidator contract.
type AuthoritativeRoleResolver interface {
	ResolveAuthoritativeRole(ctx context.Context, userID uint) (string, error)
}

// AuthMiddleware 校验 JWT 并写入用户上下文。
func AuthMiddleware(jwtSecret string, validator SessionValidator) gin.HandlerFunc {
	return func(c *gin.Context) {
		authorization := c.GetHeader("Authorization")
		if authorization == "" {
			response.ErrorFrom(c, http.StatusUnauthorized, errAuthorizationHeaderRequired)
			c.Abort()
			return
		}

		if !strings.HasPrefix(authorization, "Bearer ") {
			response.ErrorFrom(c, http.StatusUnauthorized, errInvalidAuthorizationHeader)
			c.Abort()
			return
		}
		tokenText := strings.TrimSpace(strings.TrimPrefix(authorization, "Bearer "))
		if tokenText == "" {
			response.ErrorFrom(c, http.StatusUnauthorized, errInvalidAuthorizationHeader)
			c.Abort()
			return
		}

		claims, err := token.Parse(jwtSecret, tokenText)
		if err != nil {
			response.ErrorFrom(c, http.StatusUnauthorized, errInvalidToken)
			c.Abort()
			return
		}
		if claims.TokenType != "" && claims.TokenType != "access" {
			response.ErrorFrom(c, http.StatusUnauthorized, errInvalidTokenType)
			c.Abort()
			return
		}
		auditCtx := ResolveSessionAuditContext(c)
		if validator != nil {
			var issuedAt time.Time
			if claims.IssuedAt != nil {
				issuedAt = claims.IssuedAt.Time
			}
			if err = validator.ValidateAccessSession(c.Request.Context(), claims.UserID, claims.SessionID, issuedAt, auditCtx); err != nil {
				response.ErrorFrom(c, http.StatusUnauthorized, errSessionInvalid)
				c.Abort()
				return
			}
		}

		role := claims.Role
		if roleResolver, ok := validator.(AuthoritativeRoleResolver); ok {
			role, err = roleResolver.ResolveAuthoritativeRole(c.Request.Context(), claims.UserID)
			if err != nil {
				response.ErrorFrom(c, http.StatusUnauthorized, errSessionInvalid)
				c.Abort()
				return
			}
		}

		executionContext, subjectErr := llm.WithAuthenticatedExecutionSubject(c.Request.Context(), claims.UserID, "")
		if subjectErr != nil {
			response.ErrorFrom(c, http.StatusUnauthorized, errSessionInvalid)
			c.Abort()
			return
		}
		c.Set(ContextKeyUserID, claims.UserID)
		c.Request = c.Request.WithContext(executionContext)
		c.Set(ContextKeyUsername, claims.Username)
		c.Set(ContextKeyUserRole, role)
		c.Set(ContextKeySessionID, claims.SessionID)
		c.Next()
	}
}

// AdminOnly 限制管理员权限。
func AdminOnly() gin.HandlerFunc {
	return func(c *gin.Context) {
		role, ok := c.Get(ContextKeyUserRole)
		if !ok {
			response.ErrorFrom(c, http.StatusForbidden, errForbidden)
			c.Abort()
			return
		}

		roleStr, roleOK := role.(string)
		if !roleOK || !domainuser.IsAdminRole(roleStr) {
			response.ErrorFrom(c, http.StatusForbidden, errAdminPermissionRequired)
			c.Abort()
			return
		}

		c.Next()
	}
}
