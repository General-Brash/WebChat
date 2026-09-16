// Package sub2 defines the Chat-side domain vocabulary for the Sub2 integration.
package sub2

import (
	"errors"
	"strings"
	"time"

	domainuser "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/user"
)

var ErrExecutionAttributionMismatch = errors.New("Sub2 execution trusted attribution mismatch")

const (
	IdentityStatusActive = "active"

	ExternalRoleUser       = "user"
	ExternalRoleAdmin      = "admin"
	ExternalRoleSuperAdmin = "super_admin"

	ExecutionStateCreated     = "CREATED"
	ExecutionStateAuthorized  = "AUTHORIZED"
	ExecutionStateDispatched  = "DISPATCHED"
	ExecutionStateUnknown     = "UNKNOWN"
	ExecutionStateReconciling = "RECONCILING"
	ExecutionStateSucceeded   = "SUCCEEDED"
	ExecutionStateFailed      = "FAILED"
	ExecutionStateCanceled    = "CANCELED"
	ExecutionStateSettled     = "SETTLED"
)

// MapExternalRole maps only the explicitly trusted Sub2 role vocabulary.
// Unknown or missing roles are intentionally rejected rather than downgraded.
func MapExternalRole(role string) (string, bool) {
	switch strings.TrimSpace(role) {
	case ExternalRoleUser:
		return domainuser.RoleUser, true
	case ExternalRoleAdmin:
		return domainuser.RoleAdmin, true
	case ExternalRoleSuperAdmin:
		return domainuser.RoleSuperAdmin, true
	default:
		return "", false
	}
}

// IsRoleDowngrade reports whether a local role transition removes authority.
func IsRoleDowngrade(previous string, next string) bool {
	return localRoleRank(previous) > localRoleRank(next)
}

// IsPermissionVersionValid accepts zero for the ordinary Sub2 role. Presence
// is checked at the wire boundary because Go's zero value cannot distinguish
// a missing JSON field from an explicit 0.
func IsPermissionVersionValid(version int64) bool {
	return version >= 0
}

func localRoleRank(role string) int {
	switch strings.TrimSpace(role) {
	case domainuser.RoleSuperAdmin:
		return 3
	case domainuser.RoleAdmin:
		return 2
	case domainuser.RoleUser:
		return 1
	default:
		return 0
	}
}

// NormalizeAuthorityExecutionState maps the authority's terminal result into
// the Chat-side state machine. A non-terminal authority response is always a
// reconciliation state; it is never treated as a failed request that can be
// dispatched again.
func NormalizeAuthorityExecutionState(state string, terminal bool) string {
	if !terminal {
		return ExecutionStateReconciling
	}
	state = strings.ToUpper(strings.TrimSpace(state))
	switch {
	case strings.Contains(state, "CANCEL"):
		return ExecutionStateCanceled
	case strings.Contains(state, "FAIL"), strings.Contains(state, "ERROR"):
		return ExecutionStateFailed
	case strings.Contains(state, "SETTLE"):
		return ExecutionStateSettled
	case strings.Contains(state, "SUCCEED"), strings.Contains(state, "COMPLETE"), strings.Contains(state, "DONE"), strings.Contains(state, "FINISH"):
		return ExecutionStateSucceeded
	default:
		return ExecutionStateUnknown
	}
}

// IsTerminalExecutionState reports whether the state is terminal and must not
// be dispatched again.
func IsTerminalExecutionState(state string) bool {
	switch state {
	case ExecutionStateSucceeded, ExecutionStateFailed, ExecutionStateCanceled, ExecutionStateSettled:
		return true
	default:
		return false
	}
}

// IdentityBinding is the Chat-side projection of a trusted Sub2 identity.
// Issuer and Subject are the immutable identity key; email is display data only.
type IdentityBinding struct {
	ID                        uint
	UserID                    uint
	Issuer                    string
	Subject                   string
	ExternalUserID            string
	IdentityEpoch             uint64
	Status                    string
	Role                      string
	PermissionVersion         int64
	SubjectAssertionEncrypted string
	SubjectAssertionExpiresAt *time.Time
	Email                     string
	EmailVerified             bool
	LastCheckedAt             *time.Time
	LastLoginAt               *time.Time
	CreatedAt                 time.Time
	UpdatedAt                 time.Time
}

// ExternalExecution persists the idempotency and reconciliation boundary for
// an execution authorized by Sub2. RequestHash and IdempotencyKey are stored
// for same-key/different-input detection; credentials and tokens are never
// stored here.
type ExternalExecution struct {
	ID          uint
	ExecutionID string
	// UserID is the legacy authority-scope column. Historical rows remain
	// ambiguous and are not backfilled; new paid rows set it to the trusted
	// triggerer for compatibility with existing reconciliation queries.
	UserID uint
	// TriggererUserID is the immutable authenticated human who triggered the
	// paid execution. ResourceOwnerUserID is independent and may be zero.
	TriggererUserID        uint
	ResourceOwnerUserID    uint
	Purpose                string
	ParentExecutionID      string
	Issuer                 string
	Subject                string
	ExternalUserID         string
	IdentityEpoch          uint64
	Application            string
	ChatRunID              string
	ConversationID         uint
	TaskType               string
	ModelName              string
	RequestHash            string
	IdempotencyKey         string
	State                  string
	TerminalStatus         string
	AuthorityExecutionID   string
	AuthorityUsageID       string
	AuthorityTransactionID string
	ErrorCode              string
	ErrorMessage           string
	MetadataJSON           string
	CreatedAt              time.Time
	UpdatedAt              time.Time
	AuthorizedAt           *time.Time
	DispatchedAt           *time.Time
	UnknownAt              *time.Time
	ReconciliationAt       *time.Time
	TerminalAt             *time.Time
	CancelRequestedAt      *time.Time
	LastQueriedAt          *time.Time
}
