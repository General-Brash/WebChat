package sub2

import "encoding/json"

type GatewayRequest struct {
	Identity    IdentityRef `json:"identity"`
	Application string      `json:"application"`
	ExecutionID string      `json:"execution_id"`
	RunID       string      `json:"run_id"`
	// Model is the exact provider request model. RetailModel is the selected
	// Chat/platform model used for allowlist and pricing lookup.
	Model          string            `json:"model"`
	RetailModel    string            `json:"retail_model"`
	Protocol       string            `json:"protocol"`
	GroupIDs       []int64           `json:"group_ids"`
	GroupRevision  int64             `json:"group_revision"`
	PricingVersion int64             `json:"pricing_version"`
	PlayerVersion  int64             `json:"player_version"`
	PlayerGroupIDs []uint            `json:"player_group_ids"`
	Method         string            `json:"method"`
	Path           string            `json:"path"`
	Query          string            `json:"query,omitempty"`
	ContentType    string            `json:"content_type"`
	Headers        map[string]string `json:"headers,omitempty"`
	Body           []byte            `json:"body"`
}
type FinanceRequest struct {
	Identity       IdentityRef     `json:"identity"`
	Application    string          `json:"application"`
	ID             int64           `json:"id"`
	Query          string          `json:"query"`
	Command        json.RawMessage `json:"command"`
	IdempotencyKey string          `json:"idempotency_key"`
}
