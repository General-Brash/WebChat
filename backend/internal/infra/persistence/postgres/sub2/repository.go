package sub2

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	domainsub2 "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/sub2"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/persistence/dberror"
	model "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/persistence/models"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/repository"
	"gorm.io/gorm"
)

// Repo persists only Chat-side Sub2 identity/execution projections. It never
// writes Sub2 wallet or ledger tables.
type Repo struct {
	db *gorm.DB
}

// NewRepo creates the Chat-owned Sub2 projection repository.
func NewRepo(db *gorm.DB) *Repo {
	return &Repo{db: db}
}

// CreateExternalExecution persists the pre-dispatch execution record.
func (r *Repo) CreateExternalExecution(ctx context.Context, item *domainsub2.ExternalExecution) error {
	// New paid executions must carry an authenticated triggerer. UserID is
	// retained as the legacy authority scope and is set to that triggerer by
	// the gateway so old reconciliation queries remain scoped.
	if r == nil || r.db == nil || item == nil || strings.TrimSpace(item.ExecutionID) == "" || item.UserID == 0 || item.TriggererUserID == 0 || item.UserID != item.TriggererUserID {
		return repository.ErrInvalidInput
	}
	dbItem := toModelExternalExecution(item)
	if err := r.db.WithContext(ctx).Create(dbItem).Error; err != nil {
		return translateError(err)
	}
	item.ID = dbItem.ID
	item.CreatedAt = dbItem.CreatedAt
	item.UpdatedAt = dbItem.UpdatedAt
	return nil
}

// GetExternalExecution retrieves an execution scoped to its Chat user.
func (r *Repo) GetExternalExecution(ctx context.Context, userID uint, executionID string) (*domainsub2.ExternalExecution, error) {
	if r == nil || r.db == nil || userID == 0 || strings.TrimSpace(executionID) == "" {
		return nil, repository.ErrInvalidInput
	}
	var item model.Sub2ExternalExecution
	if err := r.db.WithContext(ctx).
		Where("user_id = ? AND execution_id = ?", userID, strings.TrimSpace(executionID)).
		First(&item).Error; err != nil {
		return nil, translateError(err)
	}
	return toDomainExternalExecution(item), nil
}

// GetExternalExecutionByProviderResponseID resolves a provider response ID within
// the authenticated Chat user and application boundary. Metadata remains a
// legacy text JSON column, so the query deliberately uses portable LIKE
// matching plus indexed owner/application predicates and a bounded candidate
// set; JSON is parsed in Go for exact equality instead of using a
// dialect-specific JSON operator or an unbounded text scan.
func (r *Repo) GetExternalExecutionByProviderResponseID(ctx context.Context, userID uint, application string, responseID string) (*domainsub2.ExternalExecution, error) {
	if r == nil || r.db == nil || userID == 0 || strings.TrimSpace(application) == "" || strings.TrimSpace(responseID) == "" {
		return nil, repository.ErrInvalidInput
	}
	responseID = strings.TrimSpace(responseID)
	application = strings.TrimSpace(application)
	pattern := "%" + escapeLikePattern(responseID) + "%"
	var items []model.Sub2ExternalExecution
	if err := r.db.WithContext(ctx).
		Where("user_id = ? AND application = ? AND metadata_json LIKE ? ESCAPE '\\'", userID, application, pattern).
		Order("id DESC").
		Limit(32).
		Find(&items).Error; err != nil {
		return nil, translateError(err)
	}
	for _, item := range items {
		var metadata struct {
			ProviderResponseID string `json:"provider_response_id"`
		}
		if json.Unmarshal([]byte(item.MetadataJSON), &metadata) != nil {
			continue
		}
		if strings.TrimSpace(metadata.ProviderResponseID) == responseID {
			return toDomainExternalExecution(item), nil
		}
	}
	return nil, repository.ErrNotFound
}

func escapeLikePattern(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `%`, `\%`)
	return strings.ReplaceAll(value, `_`, `\_`)
}

// UpdateExternalExecution updates the state machine and authority references.
// ListPendingExternalExecutions returns non-terminal executions that are due for an
// authority query. Querying is deliberately separate from dispatch: recovery never
// sends the model request again.
func (r *Repo) ListPendingExternalExecutions(ctx context.Context, before time.Time, limit int) ([]*domainsub2.ExternalExecution, error) {
	if r == nil || r.db == nil || before.IsZero() {
		return nil, repository.ErrInvalidInput
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	var items []model.Sub2ExternalExecution
	err := r.db.WithContext(ctx).
		Where("state IN ?", []string{
			domainsub2.ExecutionStateCreated,
			domainsub2.ExecutionStateAuthorized,
			domainsub2.ExecutionStateDispatched,
			domainsub2.ExecutionStateUnknown,
			domainsub2.ExecutionStateReconciling,
		}).
		Where("last_queried_at IS NULL OR last_queried_at < ?", before).
		Order("id ASC").
		Limit(limit).
		Find(&items).Error
	if err != nil {
		return nil, translateError(err)
	}
	results := make([]*domainsub2.ExternalExecution, 0, len(items))
	for _, item := range items {
		results = append(results, toDomainExternalExecution(item))
	}
	return results, nil
}

func (r *Repo) UpdateExternalExecution(ctx context.Context, item *domainsub2.ExternalExecution) error {
	if r == nil || r.db == nil || item == nil || item.ID == 0 || item.UserID == 0 {
		return repository.ErrInvalidInput
	}
	var current model.Sub2ExternalExecution
	if err := r.db.WithContext(ctx).Where("id = ?", item.ID).First(&current).Error; err != nil {
		return translateError(err)
	}
	if current.TriggererUserID == 0 && item.TriggererUserID != 0 {
		return domainsub2.ErrExecutionAttributionMismatch
	}
	if current.TriggererUserID > 0 {
		if current.TriggererUserID != item.TriggererUserID ||
			current.ResourceOwnerUserID != item.ResourceOwnerUserID ||
			strings.TrimSpace(current.Purpose) != strings.TrimSpace(item.Purpose) ||
			strings.TrimSpace(current.ParentExecutionID) != strings.TrimSpace(item.ParentExecutionID) ||
			strings.TrimSpace(current.Issuer) != strings.TrimSpace(item.Issuer) ||
			strings.TrimSpace(current.Subject) != strings.TrimSpace(item.Subject) ||
			strings.TrimSpace(current.ExternalUserID) != strings.TrimSpace(item.ExternalUserID) ||
			current.IdentityEpoch != item.IdentityEpoch ||
			strings.TrimSpace(current.Application) != strings.TrimSpace(item.Application) ||
			strings.TrimSpace(current.ChatRunID) != strings.TrimSpace(item.ChatRunID) {
			return domainsub2.ErrExecutionAttributionMismatch
		}
	}
	result := r.db.WithContext(ctx).
		Model(&model.Sub2ExternalExecution{}).
		Where("id = ? AND user_id = ? AND state NOT IN ?", item.ID, item.UserID, []string{
			domainsub2.ExecutionStateSucceeded,
			domainsub2.ExecutionStateSettled,
			domainsub2.ExecutionStateFailed,
			domainsub2.ExecutionStateCanceled,
		}).
		Updates(map[string]any{
			"state":                    item.State,
			"terminal_status":          item.TerminalStatus,
			"authority_execution_id":   item.AuthorityExecutionID,
			"authority_usage_id":       item.AuthorityUsageID,
			"authority_transaction_id": item.AuthorityTransactionID,
			"error_code":               item.ErrorCode,
			"error_message":            item.ErrorMessage,
			"metadata_json":            item.MetadataJSON,
			"authorized_at":            item.AuthorizedAt,
			"dispatched_at":            item.DispatchedAt,
			"unknown_at":               item.UnknownAt,
			"reconciliation_at":        item.ReconciliationAt,
			"terminal_at":              item.TerminalAt,
			"cancel_requested_at":      item.CancelRequestedAt,
			"last_queried_at":          item.LastQueriedAt,
		})
	if result.Error != nil {
		return translateError(result.Error)
	}
	if result.RowsAffected == 0 {
		return repository.ErrNotFound
	}
	return nil
}

// MarkExternalExecutionQueried records the latest authority query without
// changing the authoritative state itself.
func (r *Repo) MarkExternalExecutionQueried(ctx context.Context, userID uint, executionID string, queriedAt time.Time) error {
	if r == nil || r.db == nil || userID == 0 || strings.TrimSpace(executionID) == "" || queriedAt.IsZero() {
		return repository.ErrInvalidInput
	}
	result := r.db.WithContext(ctx).
		Model(&model.Sub2ExternalExecution{}).
		Where("user_id = ? AND execution_id = ?", userID, strings.TrimSpace(executionID)).
		Update("last_queried_at", queriedAt)
	if result.Error != nil {
		return translateError(result.Error)
	}
	if result.RowsAffected == 0 {
		return repository.ErrNotFound
	}
	return nil
}

func translateError(err error) error {
	return dberror.Translate(err)
}

func toDomainExternalExecution(item model.Sub2ExternalExecution) *domainsub2.ExternalExecution {
	return &domainsub2.ExternalExecution{
		ID:                     item.ID,
		ExecutionID:            item.ExecutionID,
		UserID:                 item.UserID,
		TriggererUserID:        item.TriggererUserID,
		ResourceOwnerUserID:    item.ResourceOwnerUserID,
		Purpose:                item.Purpose,
		ParentExecutionID:      item.ParentExecutionID,
		Issuer:                 item.Issuer,
		Subject:                item.Subject,
		ExternalUserID:         item.ExternalUserID,
		IdentityEpoch:          item.IdentityEpoch,
		Application:            item.Application,
		ChatRunID:              item.ChatRunID,
		ConversationID:         item.ConversationID,
		TaskType:               item.TaskType,
		ModelName:              item.ModelName,
		RequestHash:            item.RequestHash,
		IdempotencyKey:         item.IdempotencyKey,
		State:                  item.State,
		TerminalStatus:         item.TerminalStatus,
		AuthorityExecutionID:   item.AuthorityExecutionID,
		AuthorityUsageID:       item.AuthorityUsageID,
		AuthorityTransactionID: item.AuthorityTransactionID,
		ErrorCode:              item.ErrorCode,
		ErrorMessage:           item.ErrorMessage,
		MetadataJSON:           item.MetadataJSON,
		CreatedAt:              item.CreatedAt,
		UpdatedAt:              item.UpdatedAt,
		AuthorizedAt:           item.AuthorizedAt,
		DispatchedAt:           item.DispatchedAt,
		UnknownAt:              item.UnknownAt,
		ReconciliationAt:       item.ReconciliationAt,
		TerminalAt:             item.TerminalAt,
		CancelRequestedAt:      item.CancelRequestedAt,
		LastQueriedAt:          item.LastQueriedAt,
	}
}

func toModelExternalExecution(item *domainsub2.ExternalExecution) *model.Sub2ExternalExecution {
	if item == nil {
		return &model.Sub2ExternalExecution{}
	}
	return &model.Sub2ExternalExecution{
		BaseModel:              model.BaseModel{ID: item.ID, CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt},
		ExecutionID:            strings.TrimSpace(item.ExecutionID),
		UserID:                 item.UserID,
		TriggererUserID:        item.TriggererUserID,
		ResourceOwnerUserID:    item.ResourceOwnerUserID,
		Purpose:                strings.TrimSpace(item.Purpose),
		ParentExecutionID:      strings.TrimSpace(item.ParentExecutionID),
		Issuer:                 strings.TrimSpace(item.Issuer),
		Subject:                strings.TrimSpace(item.Subject),
		ExternalUserID:         strings.TrimSpace(item.ExternalUserID),
		IdentityEpoch:          item.IdentityEpoch,
		Application:            strings.TrimSpace(item.Application),
		ChatRunID:              strings.TrimSpace(item.ChatRunID),
		ConversationID:         item.ConversationID,
		TaskType:               strings.TrimSpace(item.TaskType),
		ModelName:              strings.TrimSpace(item.ModelName),
		RequestHash:            strings.TrimSpace(item.RequestHash),
		IdempotencyKey:         strings.TrimSpace(item.IdempotencyKey),
		State:                  strings.TrimSpace(item.State),
		TerminalStatus:         strings.TrimSpace(item.TerminalStatus),
		AuthorityExecutionID:   strings.TrimSpace(item.AuthorityExecutionID),
		AuthorityUsageID:       strings.TrimSpace(item.AuthorityUsageID),
		AuthorityTransactionID: strings.TrimSpace(item.AuthorityTransactionID),
		ErrorCode:              strings.TrimSpace(item.ErrorCode),
		ErrorMessage:           strings.TrimSpace(item.ErrorMessage),
		MetadataJSON:           item.MetadataJSON,
		AuthorizedAt:           item.AuthorizedAt,
		DispatchedAt:           item.DispatchedAt,
		UnknownAt:              item.UnknownAt,
		ReconciliationAt:       item.ReconciliationAt,
		TerminalAt:             item.TerminalAt,
		CancelRequestedAt:      item.CancelRequestedAt,
		LastQueriedAt:          item.LastQueriedAt,
	}
}

var _ repository.ExternalExecutionRepository = (*Repo)(nil)
