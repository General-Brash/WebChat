package sub2

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	sub2port "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/sub2"
)

// DispatchGateway preserves the provider response protocol, while all request
// identity, policy, payload and idempotency fields are covered by one signature.
func (c *Client) DispatchGateway(ctx context.Context, req sub2port.GatewayRequest) (*http.Response, error) {
	if err := validateBoundIdentityRequest(req.Identity); err != nil {
		return nil, err
	}
	target, err := c.buildURL("/api/v1/integration/gateway")
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.GetBody = nil // net/http must not replay a paid request automatically.
	request.Header.Set("Content-Type", "application/json")
	if err = c.sign(request, body); err != nil {
		return nil, err
	}
	streamClient := *c.httpClient
	streamClient.Timeout = 0
	return streamClient.Do(request)
}
func (c *Client) CallApplication(ctx context.Context, identity sub2port.IdentityRef, method, operation string, payload map[string]any, result any) error {
	if err := validateBoundIdentityRequest(identity); err != nil {
		return err
	}
	allowed := map[string]bool{"group-catalog": true, "group-policy": true, "group-policy/publish": true, "group-policy/confirm": true, "group-policy/revoke": true, "retail-policy": true, "retail-policy/publish": true, "retail-policy/confirm": true, "retail-policy/revoke": true, "models/sync": true, "models/publish": true, "managed-keys/restore": true}
	if !allowed[operation] || (method != "GET" && method != "POST") {
		return errors.New("unsupported application operation")
	}
	body := map[string]any{}
	for k, v := range payload {
		body[k] = v
	}
	body["identity"] = identity
	body["application"] = c.application
	body["application_id"] = c.application
	target, err := c.buildURL("/api/v1/chat/applications/" + c.application + "/" + operation)
	if err != nil {
		return err
	}
	var raw json.RawMessage
	if err := c.doJSON(ctx, method, target, body, &raw); err != nil {
		return err
	}
	if result == nil {
		return nil
	}
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if json.Unmarshal(raw, &envelope) == nil && len(envelope.Data) > 0 {
		raw = envelope.Data
	}
	return json.Unmarshal(raw, result)
}
func (c *Client) CallFinance(ctx context.Context, operation string, req sub2port.FinanceRequest) (json.RawMessage, error) {
	if strings.ContainsAny(operation, "/?.\\") || operation == "" {
		return nil, errors.New("invalid finance operation")
	}
	if err := validateBoundIdentityRequest(req.Identity); err != nil {
		return nil, err
	}
	target, err := c.buildURL("/api/v1/integration/finance/" + operation)
	if err != nil {
		return nil, err
	}
	req.Application = c.application
	var result json.RawMessage
	err = c.doJSON(ctx, http.MethodPost, target, req, &result)
	return result, err
}
func (c *Client) DiscoverModels(ctx context.Context, identity sub2port.IdentityRef, groups []int64) (json.RawMessage, error) {
	if err := validateBoundIdentityRequest(identity); err != nil {
		return nil, err
	}
	target, err := c.buildURL(modelCatalogPath)
	if err != nil {
		return nil, err
	}
	var result json.RawMessage
	err = c.doJSON(ctx, "POST", target, map[string]any{"identity": identity, "application": c.application, "group_ids": groups, "discover": true}, &result)
	return result, err
}

type ApplicationState struct {
	GroupRevision  int64 `json:"group_revision"`
	PricingVersion int64 `json:"pricing_version"`
	SyncRevision   int64 `json:"sync_revision"`
}

func (c *Client) GetApplicationState(ctx context.Context, identity sub2port.IdentityRef) (ApplicationState, error) {
	var state ApplicationState
	target, err := c.buildURL("/api/v1/integration/application-state")
	if err != nil {
		return state, err
	}
	err = c.doJSON(ctx, "POST", target, map[string]any{"identity": identity, "application": c.application}, &state)
	return state, err
}

// ApplicationGroupPolicy is the authoritative confirmed group-policy projection.
// It is intentionally separate from model catalog/request metadata so execution
// readiness can fail closed when an administrator revokes the policy remotely.
type ApplicationGroupPolicy struct {
	Revision int64  `json:"revision"`
	Status   string `json:"status"`
}

// ApplicationRetailPolicy is the authoritative retail-policy projection used by
// Chat to distinguish a locally published reference from a remotely confirmed,
// still-effective policy.
type ApplicationRetailPolicy struct {
	Version     int64      `json:"version"`
	Status      string     `json:"status"`
	EffectiveAt *time.Time `json:"effective_at,omitempty"`
	Strategy    struct {
		PlayerPolicyVersion int64 `json:"player_policy_version"`
	} `json:"strategy"`
}

func (c *Client) GetApplicationGroupPolicy(ctx context.Context, identity sub2port.IdentityRef) (*ApplicationGroupPolicy, error) {
	var result ApplicationGroupPolicy
	if err := c.CallApplication(ctx, identity, http.MethodGet, "group-policy", nil, &result); err != nil {
		return nil, err
	}
	if result.Revision <= 0 || strings.TrimSpace(result.Status) == "" {
		return nil, fmt.Errorf("%w: group policy response is incomplete", ErrInvalidResponse)
	}
	return &result, nil
}

func (c *Client) GetApplicationRetailPolicy(ctx context.Context, identity sub2port.IdentityRef) (*ApplicationRetailPolicy, error) {
	var result ApplicationRetailPolicy
	if err := c.CallApplication(ctx, identity, http.MethodGet, "retail-policy", nil, &result); err != nil {
		return nil, err
	}
	if result.Version <= 0 || strings.TrimSpace(result.Status) == "" {
		return nil, fmt.Errorf("%w: retail policy response is incomplete", ErrInvalidResponse)
	}
	return &result, nil
}

const (
	serviceSettlementQueryPath = "/api/v1/integration/services/query"
	serviceSettlementPath      = "/api/v1/integration/services/settle"
)

func validateServiceSettlementIdentity(c *Client, identity sub2port.IdentityRef, application, runID, requestKey, payloadFingerprint string) error {
	if err := validateBoundIdentityRequest(identity); err != nil {
		return err
	}
	application = strings.TrimSpace(application)
	runID = strings.TrimSpace(runID)
	requestKey = strings.TrimSpace(requestKey)
	payloadFingerprint = strings.TrimSpace(payloadFingerprint)
	if c == nil || application == "" || application != strings.TrimSpace(c.application) {
		return fmt.Errorf("%w: service settlement application is invalid", ErrInvalidResponse)
	}
	if runID == "" || strings.ContainsAny(runID, "/\\") {
		return fmt.Errorf("%w: service settlement run_id is invalid", ErrInvalidResponse)
	}
	if len(payloadFingerprint) != 64 {
		return fmt.Errorf("%w: service settlement payload fingerprint is invalid", ErrInvalidResponse)
	}
	if _, err := hex.DecodeString(payloadFingerprint); err != nil {
		return fmt.Errorf("%w: service settlement payload fingerprint is invalid", ErrInvalidResponse)
	}
	if requestKey != sub2port.ServiceSettlementRequestKey(application, runID, payloadFingerprint) {
		return fmt.Errorf("%w: service settlement request key does not bind payload", ErrInvalidResponse)
	}
	return nil
}

func validateServiceSettlementResponse(response *sub2port.ServiceSettlementResponse, request sub2port.ServiceSettlementQueryRequest, allowLegacy bool) error {
	if response == nil {
		return fmt.Errorf("%w: service settlement response is empty", ErrInvalidResponse)
	}
	if strings.TrimSpace(response.Application) != strings.TrimSpace(request.Application) || strings.TrimSpace(response.RunID) != strings.TrimSpace(request.RunID) {
		return fmt.Errorf("%w: service settlement response identity mismatch", ErrInvalidResponse)
	}
	if strings.TrimSpace(response.State) == "" || response.IdentityEpoch == 0 {
		return fmt.Errorf("%w: service settlement response is incomplete", ErrInvalidResponse)
	}
	if strings.TrimSpace(response.RequestKey) != strings.TrimSpace(request.RequestKey) {
		if !(allowLegacy && response.LegacyRequest && strings.TrimSpace(response.RequestKey) != "") {
			return fmt.Errorf("%w: service settlement response request key mismatch", ErrInvalidResponse)
		}
	} else if response.LegacyRequest {
		return fmt.Errorf("%w: legacy flag on current service settlement key", ErrInvalidResponse)
	}
	if response.PayloadFingerprint != "" && strings.TrimSpace(response.PayloadFingerprint) != strings.TrimSpace(request.PayloadFingerprint) {
		return fmt.Errorf("%w: service settlement response payload mismatch", ErrInvalidResponse)
	}
	return nil
}

// QueryServiceSettlement reads the durable services outcome. It never creates
// a billing claim and its NOT_SUBMITTED result is scoped to the exact key.
func (c *Client) QueryServiceSettlement(ctx context.Context, req sub2port.ServiceSettlementQueryRequest) (*sub2port.ServiceSettlementResponse, error) {
	if err := validateServiceSettlementIdentity(c, req.Identity, req.Application, req.RunID, req.RequestKey, req.PayloadFingerprint); err != nil {
		return nil, err
	}
	target, err := c.buildURL(serviceSettlementQueryPath)
	if err != nil {
		return nil, err
	}
	var result sub2port.ServiceSettlementResponse
	if err := c.doJSON(ctx, http.MethodPost, target, req, &result); err != nil {
		return nil, err
	}
	if err := validateServiceSettlementResponse(&result, req, true); err != nil {
		return nil, err
	}
	return &result, nil
}

// SettleServices submits one already fingerprinted service charge. The
// authority may reject it as parent_settlement_pending; callers must query
// again and must not reinterpret an ambiguous error as permission to retry.
func (c *Client) SettleServices(ctx context.Context, req sub2port.ServiceSettlementRequest) (*sub2port.ServiceSettlementResponse, error) {
	if err := validateServiceSettlementIdentity(c, req.Identity, req.Application, req.RunID, req.RequestKey, req.PayloadFingerprint); err != nil {
		return nil, err
	}
	if req.PricingVersion <= 0 || len(req.Items) == 0 {
		return nil, fmt.Errorf("%w: service settlement pricing and items are required", ErrInvalidResponse)
	}
	for _, item := range req.Items {
		if strings.TrimSpace(item.Code) == "" || item.Count <= 0 || item.ExpectedUnitNanousd <= 0 {
			return nil, fmt.Errorf("%w: service settlement item is invalid", ErrInvalidResponse)
		}
	}
	target, err := c.buildURL(serviceSettlementPath)
	if err != nil {
		return nil, err
	}
	var result sub2port.ServiceSettlementResponse
	if err := c.doJSON(ctx, http.MethodPost, target, req, &result); err != nil {
		return nil, err
	}
	query := sub2port.ServiceSettlementQueryRequest{Identity: req.Identity, Application: req.Application, RunID: req.RunID, RequestKey: req.RequestKey, PayloadFingerprint: req.PayloadFingerprint}
	if err := validateServiceSettlementResponse(&result, query, false); err != nil {
		return nil, err
	}
	return &result, nil
}

var _ sub2port.ServiceSettlementClient = (*Client)(nil)

