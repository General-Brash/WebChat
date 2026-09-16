package repository

import (
	"context"
	"time"
)

const (
	EmbeddingReindexStatusQueued         = "queued"
	EmbeddingReindexStatusRunning        = "running"
	EmbeddingReindexStatusCompleted      = "completed"
	EmbeddingReindexStatusFailed         = "failed"
	EmbeddingReindexStatusNeedsInitiator = "needs_initiator"
)

// EmbeddingReindexJob is the durable, single-purpose intent for one global
// embedding-space rebuild. The trigger/run/config fields are immutable after
// creation; the cursor and counters are recovery state.
type EmbeddingReindexJob struct {
	ID                    uint
	JobID                 string
	TriggererUserID       uint
	ResourceOwnerUserID   uint
	Purpose               string
	RunID                 string
	ExecutionID           string
	ParentExecutionID     string
	TriggerCreatedAt      time.Time
	EmbeddingSignature    string
	EmbeddingHost         string
	EmbeddingModel        string
	EmbeddingDimensions   int
	ConfigIdentity        string
	Status                string
	Cursor                uint
	TotalFiles            int64
	SubmittedFiles        int64
	CompletedFiles        int64
	FailedFiles           int64
	LastError             string
	LeaseOwner            string
	LeaseExpiresAt        *time.Time
	StartedAt             *time.Time
	CompletedAt           *time.Time
	CreatedAt             time.Time
	UpdatedAt             time.Time
}

// EmbeddingReindexRepository is intentionally scoped to the global embedding
// rebuild intent. It is not a generic background-job framework.
type EmbeddingReindexRepository interface {
	CreateOrGet(ctx context.Context, job *EmbeddingReindexJob) (*EmbeddingReindexJob, bool, error)
	GetByJobID(ctx context.Context, jobID string) (*EmbeddingReindexJob, error)
	GetActive(ctx context.Context) (*EmbeddingReindexJob, error)
	Claim(ctx context.Context, jobID, leaseOwner string, now, leaseUntil time.Time) (*EmbeddingReindexJob, bool, error)
	SaveProgress(ctx context.Context, job *EmbeddingReindexJob) error
	CountPendingFiles(ctx context.Context, signature string, before time.Time) (int64, error)
	CountFailedFiles(ctx context.Context, signature string, before time.Time) (int64, error)
}
