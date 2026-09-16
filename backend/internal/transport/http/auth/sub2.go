package auth

import (
	"errors"
	"net/http"
	"time"

	appauth "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/application/auth"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/shared/response"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/transport/http/middleware"
	"github.com/gin-gonic/gin"
)

// Sub2LoginStartResponse contains only the authorization URL and transaction expiry.
type Sub2LoginStartResponse struct {
	AuthorizationURL string    `json:"authorizationURL"`
	ExpiresAt        time.Time `json:"expiresAt"`
}

// Sub2LoginCallbackRequest is the JSON form of the OIDC callback for API clients.
type Sub2LoginCallbackRequest struct {
	Code                string `json:"code,omitempty"`
	State               string `json:"state" binding:"required"`
	ProviderError       string `json:"error,omitempty"`
	BrowserBindingToken string `json:"browser_binding_token,omitempty"`
}

func (h *Handler) StartSub2Login(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	result, err := h.service.StartSub2Login(c.Request.Context(), c.Query("next"))
	if err != nil {
		writeSub2Error(c, err)
		return
	}
	h.writeSub2LoginBindingCookie(c, result.BrowserBindingToken, result.ExpiresAt)
	c.Redirect(http.StatusFound, result.AuthorizationURL)
}

func (h *Handler) Sub2Callback(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	browserBindingToken, _ := c.Cookie(sub2LoginBindingCookieName)
	result, err := h.service.CompleteSub2Login(c.Request.Context(), appauth.Sub2LoginCallbackInput{
		Code:                c.Query("code"),
		State:               c.Query("state"),
		ProviderError:       c.Query("error"),
		BrowserBindingToken: browserBindingToken,
		RequestID:           middleware.MustRequestID(c),
		AuditContext:        middleware.ResolveSessionAuditContext(c),
	})
	if err != nil {
		if shouldClearSub2LoginBindingCookie(err) {
			h.clearSub2LoginBindingCookie(c)
		}
		writeSub2Error(c, err)
		return
	}
	h.clearSub2LoginBindingCookie(c)
	h.writeRefreshTokenCookie(c, result)
	c.Redirect(http.StatusSeeOther, h.service.Sub2FrontendRedirect(result.RedirectPath))
}

func (h *Handler) CompleteSub2Login(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	var req Sub2LoginCallbackRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.InvalidRequestBody(c, err)
		return
	}
	browserBindingToken := req.BrowserBindingToken
	if browserBindingToken == "" {
		browserBindingToken, _ = c.Cookie(sub2LoginBindingCookieName)
	}
	result, err := h.service.CompleteSub2Login(c.Request.Context(), appauth.Sub2LoginCallbackInput{
		Code:                req.Code,
		State:               req.State,
		ProviderError:       req.ProviderError,
		BrowserBindingToken: browserBindingToken,
		RequestID:           middleware.MustRequestID(c),
		AuditContext:        middleware.ResolveSessionAuditContext(c),
	})
	if err != nil {
		if shouldClearSub2LoginBindingCookie(err) {
			h.clearSub2LoginBindingCookie(c)
		}
		writeSub2Error(c, err)
		return
	}
	h.clearSub2LoginBindingCookie(c)
	h.writeRefreshTokenCookie(c, result)
	response.Success(c, toLoginResponse(result))
}

const sub2LoginBindingCookieName = "deeix_chat_sub2_login_binding"

func shouldClearSub2LoginBindingCookie(err error) bool {
	return err == nil || appauth.IsSub2AuthorizationTransactionConsumed(err)
}

func (h *Handler) writeSub2LoginBindingCookie(c *gin.Context, token string, expiresAt time.Time) {
	if token == "" || expiresAt.IsZero() {
		return
	}
	maxAge := int(time.Until(expiresAt).Seconds())
	if maxAge < 0 {
		maxAge = 0
	}
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     sub2LoginBindingCookieName,
		Value:    token,
		Path:     "/api/v1/auth/sub2",
		Expires:  expiresAt,
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   h.shouldUseSecureCookie(c),
		SameSite: http.SameSiteLaxMode,
	})
}

func (h *Handler) clearSub2LoginBindingCookie(c *gin.Context) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     sub2LoginBindingCookieName,
		Value:    "",
		Path:     "/api/v1/auth/sub2",
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   h.shouldUseSecureCookie(c),
		SameSite: http.SameSiteLaxMode,
	})
}

func writeSub2Error(c *gin.Context, err error) {
	switch {
	case errors.Is(err, appauth.ErrSub2IntegrationDisabled):
		response.ErrorFrom(c, http.StatusNotFound, err)
	case errors.Is(err, appauth.ErrSub2IntegrationNotReady), errors.Is(err, appauth.ErrSub2IdentityStatusUnavailable):
		response.ErrorFrom(c, http.StatusServiceUnavailable, err)
	case errors.Is(err, appauth.ErrSub2IdentityRevoked), errors.Is(err, appauth.ErrSub2IdentityEpochMismatch):
		response.ErrorFrom(c, http.StatusUnauthorized, err)
	default:
		response.ErrorFrom(c, http.StatusBadRequest, err)
	}
}
