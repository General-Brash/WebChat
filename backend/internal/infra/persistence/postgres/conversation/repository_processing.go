package conversation

import (
	"context"
	"time"

	domainconversation "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/conversation"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/persistence/dberror"
	models "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/persistence/models"
	portllm "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/llm"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/repository"
	"gorm.io/gorm"
)

func (r *Repo) UpdateFileObjectProcessingState(ctx context.Context, item *domainconversation.FileObjectProcessing) error {
	if item == nil {
		return nil
	}
	query := r.db.WithContext(ctx).
		Model(&models.FileObject{}).
		Where("id = ? AND user_id = ?", item.FileObjectID, item.UserID)
	query = withFileObjectProcessingAttributionScope(query, fileObjectProcessingContextFromState(item))
	result := query.Updates(fileObjectProcessingStateUpdates(item))
	if result.Error != nil {
		return dberror.Translate(result.Error)
	}
	if result.RowsAffected == 0 {
		return repository.ErrNotFound
	}
	return nil
}

func (r *Repo) UpdateClaimedFileObjectProcessingState(
	ctx context.Context,
	item *domainconversation.FileObjectProcessing,
	attemptID string,
) (bool, error) {
	if item == nil || attemptID == "" {
		return false, nil
	}
	updates := fileObjectProcessingStateUpdates(item)
	if item.ProcessingStatus == "ready" || item.ProcessingStatus == "failed" {
		updates["processing_attempt_id"] = ""
	}
	query := r.db.WithContext(ctx).
		Model(&models.FileObject{}).
		Where("id = ? AND user_id = ? AND processing_attempt_id = ?", item.FileObjectID, item.UserID, attemptID)
	query = withFileObjectProcessingAttributionScope(query, fileObjectProcessingContextFromState(item))
	result := query.Updates(updates)
	if result.Error != nil {
		return false, dberror.Translate(result.Error)
	}
	return result.RowsAffected > 0, nil
}

func (r *Repo) GetFileObjectProcessingByObjectID(ctx context.Context, fileObjID uint) (*domainconversation.FileObjectProcessing, error) {
	var item models.FileObject
	if err := r.db.WithContext(ctx).
		Where("id = ?", fileObjID).
		First(&item).Error; err != nil {
		return nil, err
	}
	result := toFileObjectProcessingStateDomain(item)
	return &result, nil
}

func (r *Repo) CloneFileObjectProcessingState(ctx context.Context, sourceFileObjID uint, targetFileObjID uint, userID uint) error {
	if sourceFileObjID == 0 || targetFileObjID == 0 {
		return nil
	}
	source, err := r.GetFileObjectProcessingByObjectID(ctx, sourceFileObjID)
	if err != nil {
		return nil
	}
	now := time.Now()
	copyItem := *source
	copyItem.ID = 0
	copyItem.FileObjectID = targetFileObjID
	copyItem.UserID = userID
	copyItem.CreatedAt = now
	copyItem.UpdatedAt = now
	return r.UpdateFileObjectProcessingState(ctx, &copyItem)
}

func (r *Repo) TryClaimFileObjectProcessing(
	ctx context.Context,
	userID uint,
	fileID string,
	allowRecovery bool,
	extractorVersion string,
	attemptID string,
	trigger ...portllm.TrustedTriggerContext,
) (bool, error) {
	if attemptID == "" {
		return false, nil
	}
	claimableStatuses := []string{"queued"}
	if allowRecovery {
		claimableStatuses = append(claimableStatuses, "extracting", "embedding")
	}
	now := time.Now()
	query := r.db.WithContext(ctx).
		Model(&models.FileObject{}).
		Where("user_id = ? AND file_id = ? AND processing_status IN ?", userID, fileID, claimableStatuses)
	trusted := firstTrustedTriggerContext(trigger)
	query = withFileObjectProcessingAttributionScope(query, trusted)
	updates := map[string]any{
		"processing_status":        "extracting",
		"processing_ready":         false,
		"processing_error_code":    "",
		"processing_error_message": "",
		"extract_status":           "processing",
		"extractor_version":        extractorVersion,
		"processing_attempt_id":    attemptID,
		"processing_started_at":    now,
		"processing_completed_at":  nil,
		"updated_at":               now,
	}
	for key, value := range fileObjectProcessingMetadataUpdates(trusted) {
		updates[key] = value
	}
	result := query.Updates(updates)
	if result.Error != nil {
		return false, dberror.Translate(result.Error)
	}
	return result.RowsAffected > 0, nil
}

func firstTrustedTriggerContext(items []portllm.TrustedTriggerContext) portllm.TrustedTriggerContext {
	if len(items) == 0 {
		return portllm.TrustedTriggerContext{}
	}
	return repository.NormalizeTrustedTriggerContext(items[0])
}

func fileObjectProcessingContextFromState(item *domainconversation.FileObjectProcessing) portllm.TrustedTriggerContext {
	if item == nil {
		return portllm.TrustedTriggerContext{}
	}
	var createdAt time.Time
	if item.TriggerCreatedAt != nil {
		createdAt = *item.TriggerCreatedAt
	}
	return repository.NormalizeTrustedTriggerContext(portllm.TrustedTriggerContext{
		TriggererUserID:     item.TriggererUserID,
		ResourceOwnerUserID: item.ResourceOwnerUserID,
		Purpose:             item.Purpose,
		RunID:               item.RunID,
		ExecutionID:         item.ExecutionID,
		ParentExecutionID:   item.ParentExecutionID,
		CreatedAt:           createdAt,
	})
}

func fileObjectProcessingMetadataUpdates(trigger portllm.TrustedTriggerContext) map[string]any {
	trigger = repository.NormalizeTrustedTriggerContext(trigger)
	if !repository.HasTrustedTriggerMetadata(trigger) {
		return nil
	}
	updates := map[string]any{
		"processing_triggerer_user_id":      trigger.TriggererUserID,
		"processing_resource_owner_user_id": trigger.ResourceOwnerUserID,
		"processing_purpose":                trigger.Purpose,
		"processing_run_id":                 trigger.RunID,
		"processing_execution_id":           trigger.ExecutionID,
		"processing_parent_execution_id":    trigger.ParentExecutionID,
	}
	if !trigger.CreatedAt.IsZero() {
		updates["processing_trigger_created_at"] = trigger.CreatedAt
	}
	return updates
}

// withFileObjectProcessingAttributionScope makes a new task claim/update
// compare the immutable operation envelope. Legacy rows with no envelope may
// be adopted by the first explicit producer, but an existing actor/run cannot
// be silently replaced by a worker or a later browser context.
func withFileObjectProcessingAttributionScope(query *gorm.DB, trigger portllm.TrustedTriggerContext) *gorm.DB {
	trigger = repository.NormalizeTrustedTriggerContext(trigger)
	if !repository.HasTrustedTriggerMetadata(trigger) {
		return query
	}
	legacy := "(processing_triggerer_user_id = 0 AND processing_resource_owner_user_id = 0 AND processing_purpose = '' AND processing_run_id = '' AND processing_execution_id = '' AND processing_parent_execution_id = '' AND processing_trigger_created_at IS NULL)"
	current := "(processing_triggerer_user_id = ? AND processing_resource_owner_user_id = ? AND processing_purpose = ? AND processing_run_id = ? AND processing_execution_id = ? AND processing_parent_execution_id = ?"
	args := []any{
		trigger.TriggererUserID,
		trigger.ResourceOwnerUserID,
		trigger.Purpose,
		trigger.RunID,
		trigger.ExecutionID,
		trigger.ParentExecutionID,
	}
	if !trigger.CreatedAt.IsZero() {
		current += " AND (processing_trigger_created_at IS NULL OR processing_trigger_created_at = ?)"
		args = append(args, trigger.CreatedAt)
	}
	current += ")"
	return query.Where(legacy+" OR "+current, args...)
}

func (r *Repo) ResetFileObjectProcessingForRetry(
	ctx context.Context,
	userID uint,
	fileID string,
	attemptID string,
) (bool, error) {
	now := time.Now()
	result := r.db.WithContext(ctx).
		Model(&models.FileObject{}).
		Where(
			"user_id = ? AND file_id = ? AND processing_attempt_id = ? AND processing_status IN ?",
			userID,
			fileID,
			attemptID,
			[]string{"extracting", "embedding"},
		).
		Updates(map[string]any{
			"processing_status":       "queued",
			"processing_ready":        false,
			"extract_status":          "none",
			"processing_attempt_id":   "",
			"processing_completed_at": nil,
			"updated_at":              now,
		})
	if result.Error != nil {
		return false, dberror.Translate(result.Error)
	}
	return result.RowsAffected > 0, nil
}
