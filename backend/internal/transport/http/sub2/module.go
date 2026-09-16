package sub2

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/shared/response"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/transport/http/middleware"
	"github.com/gin-gonic/gin"
)

type ActionRequest struct {
	ID                 int64           `json:"id,omitempty"`
	TargetUserID       uint            `json:"targetUserID,omitempty"`
	Query              string          `json:"query,omitempty"`
	Command            json.RawMessage `json:"command,omitempty" swaggertype:"object"`
	IdempotencyKey     string          `json:"idempotencyKey,omitempty"`
	ExecutionID        string          `json:"executionID,omitempty"`
	RunID              string          `json:"runID,omitempty"`
	RequestKey         string          `json:"requestKey,omitempty"`
	PayloadFingerprint string          `json:"payloadFingerprint,omitempty"`
}
type Authority interface {
	Action(context.Context, uint, string, ActionRequest) (any, error)
}
type Module struct{ authority Authority }

func NewModule(authority Authority) *Module { return &Module{authority: authority} }
func (m *Module) RegisterRoutes(r *gin.RouterGroup) {
	r.GET("/sub2/status", func(c *gin.Context) { m.call(c, "status", false) })
	for _, operation := range []string{"wallet", "execution", "services-query", "cancel", "finance-config", "finance-plans", "finance-quote", "finance-purchase", "finance-checkout", "finance-orders", "finance-ledger", "finance-order", "finance-cancel-order", "finance-refund-request", "finance-redeem", "finance-redemptions", "finance-subscriptions", "finance-subscription-summary"} {
		op := operation
		r.POST("/sub2/"+op, func(c *gin.Context) { m.call(c, op, true) })
	}
}
func (m *Module) RegisterAdminRoutes(r *gin.RouterGroup) {
	for _, operation := range []string{"groups", "publish-groups", "publish", "revoke-policy", "catalog", "finance-admin-orders", "finance-admin-ledger", "finance-admin-order", "finance-admin-balance", "finance-admin-temporary-credit", "finance-admin-credit-audit", "finance-admin-redeem-create", "finance-admin-redeem-apply", "finance-admin-subscriptions", "finance-admin-subscription-assign", "finance-admin-subscription-extend", "finance-admin-subscription-revoke", "finance-admin-subscription-restore", "finance-admin-refund", "finance-admin-refund-query", "finance-admin-plan-create", "finance-admin-plan-update", "finance-admin-plan-delete"} {
		op := operation
		r.POST("/sub2/"+op, func(c *gin.Context) { m.call(c, op, true) })
	}
}
func (m *Module) call(c *gin.Context, operation string, body bool) {
	var req ActionRequest
	if body && c.Request.ContentLength != 0 {
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"code": "invalid_request", "message": "Invalid Sub2 command"})
			return
		}
	}
	if m == nil || m.authority == nil {
		response.Success(c, map[string]any{"enabled": false})
		return
	}
	result, err := m.authority.Action(c.Request.Context(), middleware.MustUserID(c), operation, req)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"code": "sub2_authority_unavailable", "message": err.Error()})
		return
	}
	response.Success(c, result)
}

// AuthorityResponseDoc documents the Sub2-backed response envelope. Financial
// payloads use Sub2's product and ledger schema, never a Chat money command.
type AuthorityResponseDoc struct {
	Data map[string]interface{} `json:"data"`
}

// AuthorityStatusDoc documents the non-secret local readiness projection.
type AuthorityStatusDoc struct {
	Data AuthorityStatusData `json:"data"`
}
type AuthorityStatusData struct {
	Enabled        bool   `json:"enabled"`
	PaidReady      bool   `json:"paidReady,omitempty"`
	Issuer         string `json:"issuer,omitempty"`
	GroupRevision  int64  `json:"groupRevision,omitempty"`
	PricingVersion int64  `json:"pricingVersion,omitempty"`
	PlayerVersion  int64  `json:"playerVersion,omitempty"`
}

// StatusAPI godoc
// @Summary Read Sub2 authority status
// @Tags sub2
// @Security BearerAuth
// @Produce json
// @Success 200 {object} AuthorityStatusDoc
// @Router /sub2/status [get]
func (m *Module) StatusAPI(c *gin.Context) { m.call(c, "status", false) }

// UserActionAPI godoc
// @Summary Execute a scoped Sub2 user operation
// @Description Operations are explicitly allowlisted; identity is derived server-side. Unknown money results are not replayed.
// @Tags sub2
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param operation path string true "Operation" Enums(wallet,execution,services-query,cancel,finance-config,finance-plans,finance-quote,finance-purchase,finance-checkout,finance-orders,finance-order,finance-ledger,finance-redeem,finance-redemptions,finance-refund-request,finance-subscriptions)
// @Param request body ActionRequest true "Command; never contains a subject assertion or provider key"
// @Success 200 {object} AuthorityResponseDoc
// @Router /sub2/{operation} [post]
func (m *Module) UserActionAPI(c *gin.Context) { m.call(c, c.Param("operation"), true) }

// AdminActionAPI godoc
// @Summary Execute an authorized Sub2 application or financial command
// @Tags sub2
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param operation path string true "Allowlisted administrative operation"
// @Param request body ActionRequest true "Operator command"
// @Success 200 {object} AuthorityResponseDoc
// @Router /admin/sub2/{operation} [post]
func (m *Module) AdminActionAPI(c *gin.Context) { m.call(c, c.Param("operation"), true) }
