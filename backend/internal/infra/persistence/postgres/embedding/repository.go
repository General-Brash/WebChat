package embedding

import (
	"context"
	"strings"
	"time"

	domainconversation "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/conversation"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/persistence/dberror"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/persistence/models"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/repository"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Repo persists only the global embedding reindex intent and its recovery
// counters. File ownership/status remains in the existing conversation tables.
type Repo struct {
	db *gorm.DB
}

func NewRepo(db *gorm.DB) *Repo { return &Repo{db: db} }

func (r *Repo) CreateOrGet(ctx context.Context, job *repository.EmbeddingReindexJob) (*repository.EmbeddingReindexJob, bool, error) {
	if r == nil || r.db == nil || job == nil || strings.TrimSpace(job.JobID) == "" || strings.TrimSpace(job.RunID) == "" {
		return nil, false, repository.ErrInvalidInput
	}
	var existing models.EmbeddingReindexJob
	query := r.db.WithContext(ctx).
		Where("status IN ?", []string{repository.EmbeddingReindexStatusQueued, repository.EmbeddingReindexStatusRunning}).
		Where("config_identity = ?", strings.TrimSpace(job.ConfigIdentity)).
		Order("id DESC")
	if err := query.First(&existing).Error; err == nil {
		return toDomain(existing), false, nil
	} else if err != nil && !errorsIsRecordNotFound(err) {
		return nil, false, dberror.Translate(err)
	}
	entity := toModel(*job)
	if err := r.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&entity).Error; err != nil {
		return nil, false, dberror.Translate(err)
	}
	if entity.ID == 0 {
		if err := r.db.WithContext(ctx).Where("job_id = ?", job.JobID).First(&entity).Error; err != nil {
			return nil, false, dberror.Translate(err)
		}
		return toDomain(entity), false, nil
	}
	return toDomain(entity), true, nil
}

func (r *Repo) GetByJobID(ctx context.Context, jobID string) (*repository.EmbeddingReindexJob, error) {
	var entity models.EmbeddingReindexJob
	if err := r.db.WithContext(ctx).Where("job_id = ?", strings.TrimSpace(jobID)).First(&entity).Error; err != nil {
		if errorsIsRecordNotFound(err) {
			return nil, nil
		}
		return nil, dberror.Translate(err)
	}
	return toDomain(entity), nil
}

func (r *Repo) GetActive(ctx context.Context) (*repository.EmbeddingReindexJob, error) {
	var entity models.EmbeddingReindexJob
	if err := r.db.WithContext(ctx).
		Where("status IN ?", []string{repository.EmbeddingReindexStatusQueued, repository.EmbeddingReindexStatusRunning}).
		Order("id DESC").First(&entity).Error; err != nil {
		if errorsIsRecordNotFound(err) {
			return nil, nil
		}
		return nil, dberror.Translate(err)
	}
	return toDomain(entity), nil
}

func (r *Repo) Claim(ctx context.Context, jobID, leaseOwner string, now, leaseUntil time.Time) (*repository.EmbeddingReindexJob, bool, error) {
	leaseOwner = strings.TrimSpace(leaseOwner)
	if leaseOwner == "" || strings.TrimSpace(jobID) == "" {
		return nil, false, repository.ErrInvalidInput
	}
	result := r.db.WithContext(ctx).Model(&models.EmbeddingReindexJob{}).
		Where("job_id = ? AND status IN ?", jobID, []string{repository.EmbeddingReindexStatusQueued, repository.EmbeddingReindexStatusRunning}).
		Where("lease_owner = '' OR lease_expires_at IS NULL OR lease_expires_at < ? OR lease_owner = ?", now, leaseOwner).
		Updates(map[string]any{
			"status":           repository.EmbeddingReindexStatusRunning,
			"lease_owner":      leaseOwner,
			"lease_expires_at": leaseUntil,
			"started_at":       gorm.Expr("COALESCE(started_at, ?)", now),
		})
	if result.Error != nil {
		return nil, false, dberror.Translate(result.Error)
	}
	job, err := r.GetByJobID(ctx, jobID)
	return job, result.RowsAffected > 0, err
}

func (r *Repo) SaveProgress(ctx context.Context, job *repository.EmbeddingReindexJob) error {
	if job == nil || strings.TrimSpace(job.JobID) == "" {
		return repository.ErrInvalidInput
	}
	updates := map[string]any{
		"status":             job.Status,
		"cursor":             job.Cursor,
		"total_files":       job.TotalFiles,
		"submitted_files":   job.SubmittedFiles,
		"completed_files":   job.CompletedFiles,
		"failed_files":      job.FailedFiles,
		"last_error":        job.LastError,
		"lease_owner":       job.LeaseOwner,
		"lease_expires_at":  job.LeaseExpiresAt,
		"started_at":        job.StartedAt,
		"completed_at":      job.CompletedAt,
	}
	result := r.db.WithContext(ctx).Model(&models.EmbeddingReindexJob{}).
		Where("job_id = ? AND run_id = ?", job.JobID, job.RunID).Updates(updates)
	if result.Error != nil {
		return dberror.Translate(result.Error)
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func (r *Repo) CountPendingFiles(ctx context.Context, signature string, before time.Time) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&models.FileObject{}).
		Where("status = ? AND created_at <= ? AND embed_status IN ?", "active", before, []string{"queued", "processing"}).
		Where("embed_signature = ?", strings.TrimSpace(signature)).Count(&count).Error
	return count, dberror.Translate(err)
}

func (r *Repo) CountFailedFiles(ctx context.Context, signature string, before time.Time) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&models.FileObject{}).
		Where("status = ? AND created_at <= ? AND embed_status = ? AND embed_signature = ?", "active", before, "failed", strings.TrimSpace(signature)).Count(&count).Error
	return count, dberror.Translate(err)
}

func errorsIsRecordNotFound(err error) bool { return err == gorm.ErrRecordNotFound }

func toDomain(item models.EmbeddingReindexJob) *repository.EmbeddingReindexJob {
	return &repository.EmbeddingReindexJob{
		ID: item.ID, JobID: item.JobID, TriggererUserID: item.TriggererUserID, ResourceOwnerUserID: item.ResourceOwnerUserID,
		Purpose: item.Purpose, RunID: item.RunID, ExecutionID: item.ExecutionID, ParentExecutionID: item.ParentExecutionID,
		TriggerCreatedAt: item.TriggerCreatedAt, EmbeddingSignature: item.EmbeddingSignature, EmbeddingHost: item.EmbeddingHost,
		EmbeddingModel: item.EmbeddingModel, EmbeddingDimensions: item.EmbeddingDimensions, ConfigIdentity: item.ConfigIdentity,
		Status: item.Status, Cursor: item.Cursor, TotalFiles: item.TotalFiles, SubmittedFiles: item.SubmittedFiles,
		CompletedFiles: item.CompletedFiles, FailedFiles: item.FailedFiles, LastError: item.LastError, LeaseOwner: item.LeaseOwner,
		LeaseExpiresAt: item.LeaseExpiresAt, StartedAt: item.StartedAt, CompletedAt: item.CompletedAt,
		CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt,
	}
}

func toModel(item repository.EmbeddingReindexJob) models.EmbeddingReindexJob {
	return models.EmbeddingReindexJob{
		BaseModel: models.BaseModel{ID: item.ID, CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt},
		JobID: item.JobID, TriggererUserID: item.TriggererUserID, ResourceOwnerUserID: item.ResourceOwnerUserID,
		Purpose: item.Purpose, RunID: item.RunID, ExecutionID: item.ExecutionID, ParentExecutionID: item.ParentExecutionID,
		TriggerCreatedAt: item.TriggerCreatedAt, EmbeddingSignature: item.EmbeddingSignature, EmbeddingHost: item.EmbeddingHost,
		EmbeddingModel: item.EmbeddingModel, EmbeddingDimensions: item.EmbeddingDimensions, ConfigIdentity: item.ConfigIdentity,
		Status: item.Status, Cursor: item.Cursor, TotalFiles: item.TotalFiles, SubmittedFiles: item.SubmittedFiles,
		CompletedFiles: item.CompletedFiles, FailedFiles: item.FailedFiles, LastError: item.LastError, LeaseOwner: item.LeaseOwner,
		LeaseExpiresAt: item.LeaseExpiresAt, StartedAt: item.StartedAt, CompletedAt: item.CompletedAt,
	}
}

