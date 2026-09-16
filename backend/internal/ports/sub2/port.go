// Package sub2 defines the versioned, server-to-server Sub2 integration port.
package sub2

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

// IdentityRef is the only trusted subject context accepted by Sub2 calls.
// Callers must obtain it from a verified ID token and the persisted binding;
// browser-supplied user IDs, roles, groups and prices are intentionally absent.
type IdentityRef struct {
	Issuer         string `json:"issuer"`
	Subject        string `json:"subject"`
	ExternalUserID string `json:"external_user_id,omitempty"`
	IdentityEpoch  uint64 `json:"identity_epoch,omitempty"`
	// SubjectAssertion is an opaque, server-only binding proof. It must be
	// loaded from encrypted Chat storage and is never accepted from a browser.
	SubjectAssertion string `json:"subject_assertion,omitempty"`
}

// IdentityStatusRequest asks Sub2 to revalidate an external identity.
type IdentityStatusRequest struct {
	Identity              IdentityRef `json:"identity"`
	Application           string      `json:"application"`
	ExpectedIdentityEpoch uint64      `json:"expected_identity_epoch,omitempty"`
}

// IdentityStatusResponse is the authoritative status/epoch response.
type IdentityStatusResponse struct {
	AssertionExpiresAt       time.Time   `json:"assertion_expires_at"`
	Identity                 IdentityRef `json:"identity"`
	Active                   bool        `json:"active"`
	Status                   string      `json:"status"`
	IdentityEpoch            uint64      `json:"identity_epoch"`
	Role                     string      `json:"role"`
	PermissionVersion        int64       `json:"permission_version"`
	PermissionVersionPresent bool        `json:"-"`
	AllowedScopes            []string    `json:"allowed_scopes,omitempty"`
	CheckedAt                time.Time   `json:"checked_at"`
}

// SubjectExchangeRequest exchanges a verified OIDC subject for a short-lived,
// least-privilege service assertion. The assertion must never be sent to a browser.
type SubjectExchangeRequest struct {
	Identity        IdentityRef `json:"identity"`
	SubjectToken    string      `json:"subject_token"`
	Audience        string      `json:"audience"`
	Application     string      `json:"application"`
	Purpose         string      `json:"purpose"`
	Nonce           string      `json:"nonce,omitempty"`
	RequestedScopes []string    `json:"requested_scopes,omitempty"`
}

// SubjectExchangeResponse contains the bounded service assertion and identity projection.
type SubjectExchangeResponse struct {
	Identity                 IdentityRef `json:"identity"`
	Assertion                string      `json:"assertion"`
	ExpiresAt                time.Time   `json:"expires_at"`
	Role                     string      `json:"role"`
	PermissionVersion        int64       `json:"permission_version"`
	PermissionVersionPresent bool        `json:"-"`
	Scopes                   []string    `json:"scopes,omitempty"`
}

// WalletQueryRequest asks for a display/authorization snapshot. The response
// is not a local balance and must not be used as an independent debit ledger.
type WalletQueryRequest struct {
	Identity    IdentityRef `json:"identity"`
	Application string      `json:"application"`
}

// WalletResponse describes Sub2 permanent and temporary available amounts.
type WalletResponse struct {
	Currency                  string     `json:"currency"`
	PermanentAvailableNanousd int64      `json:"permanent_available_nanousd"`
	TemporaryAvailableNanousd int64      `json:"temporary_available_nanousd"`
	TemporaryExpiresAt        *time.Time `json:"temporary_expires_at,omitempty"`
	Revision                  string     `json:"revision"`
	CheckedAt                 time.Time  `json:"checked_at"`
}

// ModelCatalogRequest asks for the models visible to a trusted subject.
type ModelCatalogRequest struct {
	Identity    IdentityRef `json:"identity"`
	Application string      `json:"application"`
}

// ModelDescriptor is a server-authoritative model capability projection.
type ModelDescriptor struct {
	Model          string   `json:"model"`
	DisplayName    string   `json:"display_name,omitempty"`
	Protocols      []string `json:"protocols,omitempty"`
	SourceGroupIDs []int64  `json:"source_group_ids,omitempty"`
	Enabled        bool     `json:"enabled"`
	PermissionSet  string   `json:"permission_set,omitempty"`
}

// ModelCatalogResponse contains an authoritative model snapshot.
type ModelCatalogResponse struct {
	Revision  string            `json:"revision"`
	Models    []ModelDescriptor `json:"models"`
	CheckedAt time.Time         `json:"checked_at"`
}

// ModelAdmissionRequest asks Sub2 to authorize one concrete model execution.
type ModelAdmissionRequest struct {
	Identity        IdentityRef `json:"identity"`
	Application     string      `json:"application"`
	Model           string      `json:"model"`
	RequestHash     string      `json:"request_hash"`
	PricingRevision string      `json:"pricing_revision,omitempty"`
}

// ModelQuote is informational until bound to the authority admission.
type ModelQuote struct {
	Currency        string `json:"currency"`
	AmountNanousd   int64  `json:"amount_nanousd"`
	PricingRevision string `json:"pricing_revision"`
}

// ModelAdmissionResponse is the authoritative allow/deny and quote result.
type ModelAdmissionResponse struct {
	Allowed       bool        `json:"allowed"`
	ReasonCode    string      `json:"reason_code,omitempty"`
	AdmissionID   string      `json:"admission_id,omitempty"`
	IdentityEpoch uint64      `json:"identity_epoch"`
	Quote         *ModelQuote `json:"quote,omitempty"`
	ExpiresAt     time.Time   `json:"expires_at"`
}

// ExecutionQueryRequest asks for the authoritative result of one execution.
type ExecutionQueryRequest struct {
	Identity      IdentityRef `json:"identity"`
	Application   string      `json:"application"`
	ExecutionID   string      `json:"execution_id"`
	ExpectedEpoch uint64      `json:"expected_identity_epoch,omitempty"`
}

// UsageOutboxStatus is advisory metadata returned beside the authoritative
// usage snapshot. It never makes a legacy amount-only response authoritative.
type UsageOutboxStatus struct {
	State        string `json:"state"`
	AttemptCount int64  `json:"attempt_count,omitempty"`
	LastError    string `json:"last_error,omitempty"`
}

// ExecutionQueryResponse is used for recovery and UNKNOWN reconciliation.
type ExecutionQueryResponse struct {
	ResponseStatus         int                `json:"response_status,omitempty"`
	ResponseContentType    string             `json:"response_content_type,omitempty"`
	ResponseBody           []byte             `json:"response_body,omitempty"`
	BilledNanousd          int64              `json:"billed_nanousd"`
	ExecutionID            string             `json:"execution_id"`
	State                  string             `json:"state"`
	Terminal               bool               `json:"terminal"`
	AuthorityExecutionID   string             `json:"authority_execution_id,omitempty"`
	AuthorityUsageID       string             `json:"authority_usage_id,omitempty"`
	AuthorityTransactionID string             `json:"authority_transaction_id,omitempty"`
	IdentityEpoch          uint64             `json:"identity_epoch"`
	ErrorCode              string             `json:"error_code,omitempty"`
	ErrorMessage           string             `json:"error_message,omitempty"`
	CreatedAt              time.Time          `json:"created_at"`
	UpdatedAt              time.Time          `json:"updated_at"`
	TerminalAt             *time.Time         `json:"terminal_at,omitempty"`
	UsageOutbox            *UsageOutboxStatus `json:"usage_outbox,omitempty"`
	// AuthoritativeUsage is optional for backward compatibility; legacy amount-only fields are not full usage authority.
	AuthoritativeUsage *AuthoritativeUsageSnapshot `json:"authoritative_usage,omitempty"`
}

// ExecutionCancelRequest requests idempotent cancellation of an execution.
type ExecutionCancelRequest struct {
	Identity       IdentityRef `json:"identity"`
	Application    string      `json:"application"`
	ExecutionID    string      `json:"execution_id"`
	IdempotencyKey string      `json:"idempotency_key"`
	Reason         string      `json:"reason,omitempty"`
}

// ExecutionCancelResponse describes the authority's cancellation outcome.
type ExecutionCancelResponse struct {
	ExecutionID          string    `json:"execution_id"`
	State                string    `json:"state"`
	Accepted             bool      `json:"accepted"`
	AlreadyTerminal      bool      `json:"already_terminal"`
	AuthorityExecutionID string    `json:"authority_execution_id,omitempty"`
	IdentityEpoch        uint64    `json:"identity_epoch"`
	UpdatedAt            time.Time `json:"updated_at"`
}

// TokenSet is the OIDC authorization-code exchange response projection.
type TokenSet struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	IDToken     string `json:"id_token"`
	ExpiresIn   int    `json:"expires_in"`
}

// IDTokenClaims contains only validated OIDC claims. Raw JWT text is not retained.
type IDTokenClaims struct {
	Issuer            string
	Subject           string
	Audience          []string
	AuthorizedParty   string
	Nonce             string
	Email             string
	EmailVerified     bool
	Name              string
	PreferredUsername string
	Picture           string
	IssuedAt          time.Time
	ExpiresAt         time.Time
	NotBefore         *time.Time
	AuthTime          *time.Time
	Claims            map[string]any
}

// UnmarshalJSON records whether permission_version was actually present.
// Sub2 legitimately uses version 0 for ordinary users; a missing field must
// not be silently interpreted as that valid value.
func (r *IdentityStatusResponse) UnmarshalJSON(data []byte) error {
	type alias IdentityStatusResponse
	var decoded alias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	rawVersion, present := fields["permission_version"]
	decoded.PermissionVersionPresent = present && string(rawVersion) != "null"
	*r = IdentityStatusResponse(decoded)
	return nil
}

// UnmarshalJSON records whether permission_version was actually present.
func (r *SubjectExchangeResponse) UnmarshalJSON(data []byte) error {
	type alias SubjectExchangeResponse
	var decoded alias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	rawVersion, present := fields["permission_version"]
	decoded.PermissionVersionPresent = present && string(rawVersion) != "null"
	*r = SubjectExchangeResponse(decoded)
	return nil
}

// ErrExecutionDispatchUnavailable is returned while the Chat-side execution
// dispatcher has not yet replaced the local provider path.
var ErrExecutionDispatchUnavailable = errors.New("Sub2 execution dispatch is not configured")

// Client is the authenticated Sub2 server-to-server contract.
type Client interface {
	GetIdentityStatus(ctx context.Context, req IdentityStatusRequest) (*IdentityStatusResponse, error)
	ExchangeSubject(ctx context.Context, req SubjectExchangeRequest) (*SubjectExchangeResponse, error)
	GetWallet(ctx context.Context, req WalletQueryRequest) (*WalletResponse, error)
	ListModels(ctx context.Context, req ModelCatalogRequest) (*ModelCatalogResponse, error)
	AdmitModel(ctx context.Context, req ModelAdmissionRequest) (*ModelAdmissionResponse, error)
	QueryExecution(ctx context.Context, req ExecutionQueryRequest) (*ExecutionQueryResponse, error)
	CancelExecution(ctx context.Context, req ExecutionCancelRequest) (*ExecutionCancelResponse, error)
}

// OIDCProvider is the Sub2 authorization-code + PKCE S256 boundary.
type OIDCProvider interface {
	AuthorizationURL(state string, nonce string, codeChallenge string) (string, error)
	ExchangeAuthorizationCode(ctx context.Context, code string, redirectURI string, codeVerifier string) (*TokenSet, error)
	ValidateIDToken(ctx context.Context, rawIDToken string, expectedNonce string) (*IDTokenClaims, error)
}

// AuthorityClient combines the signed service API and OIDC provider portions.
type AuthorityClient interface {
	Client
	ServiceSettlementClient
	OIDCProvider
}
