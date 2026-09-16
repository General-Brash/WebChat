package repository

import (
	"context"
	"time"

	domainsub2 "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/sub2"
)

// CreateSub2UserWithIdentityInput describes the atomic JIT creation of an
// ordinary, password-disabled Chat user and its immutable Sub2 binding.
type CreateSub2UserWithIdentityInput struct {
	CreateWithCredentialInput
	Identity *domainsub2.IdentityBinding
}

// SyncSub2UserAndIdentityInput is the authoritative role/status snapshot used
// to update a bound Chat user and its Sub2 binding atomically.
type SyncSub2UserAndIdentityInput struct {
	Identity   *domainsub2.IdentityBinding
	UserRole   string
	UserStatus string
}

// Sub2IdentityRepository stores the Chat-side trusted (issuer, subject) binding.
type Sub2IdentityRepository interface {
	GetSub2IdentityBinding(ctx context.Context, issuer string, subject string) (*domainsub2.IdentityBinding, error)
	GetSub2IdentityBindingByUserID(ctx context.Context, userID uint) (*domainsub2.IdentityBinding, error)
	CreateSub2IdentityBinding(ctx context.Context, item *domainsub2.IdentityBinding) (*domainsub2.IdentityBinding, error)
	UpdateSub2IdentityBinding(ctx context.Context, item *domainsub2.IdentityBinding) error
	CreateSub2UserWithIdentity(ctx context.Context, input CreateSub2UserWithIdentityInput) error
	SyncSub2UserAndIdentity(ctx context.Context, input SyncSub2UserAndIdentityInput) error
}

// ExternalExecutionRepository persists the Chat-side idempotency and
// reconciliation state without owning Sub2 funds.
type ExternalExecutionRepository interface {
	CreateExternalExecution(ctx context.Context, item *domainsub2.ExternalExecution) error
	GetExternalExecution(ctx context.Context, userID uint, executionID string) (*domainsub2.ExternalExecution, error)
	GetExternalExecutionByProviderResponseID(ctx context.Context, userID uint, application string, responseID string) (*domainsub2.ExternalExecution, error)
	ListPendingExternalExecutions(ctx context.Context, before time.Time, limit int) ([]*domainsub2.ExternalExecution, error)
	UpdateExternalExecution(ctx context.Context, item *domainsub2.ExternalExecution) error
	MarkExternalExecutionQueried(ctx context.Context, userID uint, executionID string, queriedAt time.Time) error
}
