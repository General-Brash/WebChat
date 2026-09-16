package sub2

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"
)

const (
	ServiceSettlementStateNotSubmitted       = "NOT_SUBMITTED"
	ServiceSettlementStateParentPending      = "PARENT_SETTLEMENT_PENDING"
	ServiceSettlementStateSettled            = "SETTLED"
	ServiceSettlementStateUnknown            = "UNKNOWN"
	ServiceSettlementStateConflict           = "CONFLICT"
	serviceSettlementRequestKeyPrefix        = "s2svc2"
	serviceSettlementRequestKeyHashHexLength = 58
)

// ServiceSettlementItem is a server-validated, independent service charge.
// It is intentionally separate from model pricing so MCP charges cannot inherit
// the player's model discount.
type ServiceSettlementItem struct {
	Code                string `json:"code"`
	Count               int64  `json:"count"`
	ExpectedUnitNanousd int64  `json:"expected_unit_nanousd"`
}

// ServiceSettlementRequest is the signed Chat-to-Sub2 settlement command. The
// request key and payload fingerprint are derived from the application, run and
// canonical item list; callers must not invent either value.
type ServiceSettlementRequest struct {
	Identity           IdentityRef             `json:"identity"`
	Application        string                  `json:"application"`
	RunID              string                  `json:"run_id"`
	RequestKey         string                  `json:"request_key"`
	PayloadFingerprint string                  `json:"payload_fingerprint"`
	PricingVersion     int64                   `json:"pricing_version"`
	Items              []ServiceSettlementItem `json:"items"`
}

// ServiceSettlementQueryRequest asks the authority for the durable outcome of
// one service settlement request. A NOT_SUBMITTED response is authoritative
// only for this exact request key; UNKNOWN is never a permission to replay.
type ServiceSettlementQueryRequest struct {
	Identity           IdentityRef `json:"identity"`
	Application        string      `json:"application"`
	RunID              string      `json:"run_id"`
	RequestKey         string      `json:"request_key"`
	PayloadFingerprint string      `json:"payload_fingerprint"`
}

// ServiceSettlementResponse is the query/settlement/reconciliation contract.
type ServiceSettlementResponse struct {
	Application            string    `json:"application"`
	RunID                  string    `json:"run_id"`
	RequestKey             string    `json:"request_key"`
	PayloadFingerprint     string    `json:"payload_fingerprint"`
	State                  string    `json:"state"`
	ParentState            string    `json:"parent_state,omitempty"`
	LegacyRequest          bool      `json:"legacy_request,omitempty"`
	ExecutionID            string    `json:"execution_id,omitempty"`
	AuthorityUsageID       string    `json:"authority_usage_id,omitempty"`
	AuthorityTransactionID string    `json:"authority_transaction_id,omitempty"`
	BilledNanousd          int64     `json:"billed_nanousd,omitempty"`
	IdentityEpoch          uint64    `json:"identity_epoch"`
	ErrorCode              string    `json:"error_code,omitempty"`
	CheckedAt              time.Time `json:"checked_at"`
}

// ServiceSettlementClient is the optional service-settlement portion of the
// Sub2 integration. It is separate from the model-execution Client interface so
// existing identity/auth test doubles do not silently gain money operations.
type ServiceSettlementClient interface {
	QueryServiceSettlement(context.Context, ServiceSettlementQueryRequest) (*ServiceSettlementResponse, error)
	SettleServices(context.Context, ServiceSettlementRequest) (*ServiceSettlementResponse, error)
}

type serviceSettlementFingerprintPayload struct {
	Application string                  `json:"application"`
	RunID       string                  `json:"run_id"`
	Items       []ServiceSettlementItem `json:"items"`
}

// NormalizeServiceSettlementItems returns a deterministic copy without
// changing the caller's slice. Duplicates are preserved because two entries
// with the same code are additive billing facts, not a player discount.
func NormalizeServiceSettlementItems(items []ServiceSettlementItem) []ServiceSettlementItem {
	normalized := make([]ServiceSettlementItem, 0, len(items))
	for _, item := range items {
		normalized = append(normalized, ServiceSettlementItem{
			Code:                strings.TrimSpace(item.Code),
			Count:               item.Count,
			ExpectedUnitNanousd: item.ExpectedUnitNanousd,
		})
	}
	sort.SliceStable(normalized, func(i, j int) bool {
		if normalized[i].Code != normalized[j].Code {
			return normalized[i].Code < normalized[j].Code
		}
		if normalized[i].ExpectedUnitNanousd != normalized[j].ExpectedUnitNanousd {
			return normalized[i].ExpectedUnitNanousd < normalized[j].ExpectedUnitNanousd
		}
		return normalized[i].Count < normalized[j].Count
	})
	return normalized
}

// ServiceSettlementPayloadFingerprint hashes the complete trusted service
// payload, including application and run identity, not only the item bytes.
func ServiceSettlementPayloadFingerprint(application, runID string, items []ServiceSettlementItem) (string, error) {
	application = strings.TrimSpace(application)
	runID = strings.TrimSpace(runID)
	if application == "" || runID == "" {
		return "", errors.New("service settlement application and run_id are required")
	}
	normalized := NormalizeServiceSettlementItems(items)
	for _, item := range normalized {
		if item.Code == "" || item.Count <= 0 || item.ExpectedUnitNanousd <= 0 {
			return "", errors.New("service settlement item is invalid")
		}
	}
	raw, err := json.Marshal(serviceSettlementFingerprintPayload{Application: application, RunID: runID, Items: normalized})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

// ServiceSettlementRequestKey returns the bounded request key used by both
// usage_billing_dedup and chat_execution_settlements. It is exactly 64 bytes so
// it remains compatible with the historical usage_logs.request_id column.
func ServiceSettlementRequestKey(application, runID, payloadFingerprint string) string {
	raw := strings.Join([]string{"chat-services:v2", strings.TrimSpace(application), strings.TrimSpace(runID), strings.TrimSpace(payloadFingerprint)}, "\n")
	sum := sha256.Sum256([]byte(raw))
	return serviceSettlementRequestKeyPrefix + hex.EncodeToString(sum[:])[:serviceSettlementRequestKeyHashHexLength]
}

// LegacyServiceSettlementRequestKey identifies the pre-contract key so the
// authority can recognize an already committed legacy service settlement
// without replaying it under a new key.
func LegacyServiceSettlementRequestKey(application, runID string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(runID)))
	return "chat-services:" + strings.TrimSpace(application) + ":" + hex.EncodeToString(sum[:])
}

func IsCurrentServiceSettlementRequestKey(value string) bool {
	value = strings.TrimSpace(value)
	return len(value) == len(serviceSettlementRequestKeyPrefix)+serviceSettlementRequestKeyHashHexLength && strings.HasPrefix(value, serviceSettlementRequestKeyPrefix)
}
