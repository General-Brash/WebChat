package model

import "time"

// EmbeddingReindexJob stores one administrator-attributed global embedding
// rebuild and its resumable cursor. It is deliberately narrower than a generic
// job table: it only represents file embedding reindex orchestration.
type EmbeddingReindexJob struct {
	BaseModel
	JobID               string     `gorm:"size:64;not null;uniqueIndex:uk_chat_embedding_reindex_jobs_job_id;comment:重建任务公开ID"`
	TriggererUserID     uint       `gorm:"not null;index:idx_chat_embedding_reindex_jobs_triggerer;comment:可信管理员付款人ID"`
	ResourceOwnerUserID uint       `gorm:"not null;default:0;comment:全局意图资源所有者，0表示跨文件"`
	Purpose             string     `gorm:"size:64;not null;comment:可信操作用途"`
	RunID               string     `gorm:"size:64;not null;uniqueIndex:uk_chat_embedding_reindex_jobs_run_id;comment:稳定逻辑操作ID"`
	ExecutionID         string     `gorm:"size:64;not null;comment:稳定根执行ID"`
	ParentExecutionID   string     `gorm:"size:64;not null;default:'';comment:可信父执行ID"`
	TriggerCreatedAt    time.Time  `gorm:"not null;comment:管理员触发时间"`
	EmbeddingSignature  string     `gorm:"size:128;not null;index:idx_chat_embedding_reindex_jobs_signature;comment:目标向量空间签名"`
	EmbeddingHost       string     `gorm:"size:512;not null;comment:目标服务端点快照"`
	EmbeddingModel      string     `gorm:"size:255;not null;comment:目标模型快照"`
	EmbeddingDimensions int        `gorm:"not null;comment:目标向量维度快照"`
	ConfigIdentity      string     `gorm:"size:128;not null;comment:目标配置身份"`
	Status              string     `gorm:"size:32;not null;index:idx_chat_embedding_reindex_jobs_status;comment:queued/running/completed/failed/needs_initiator"`
	Cursor              uint       `gorm:"not null;default:0;comment:已持久化扫描游标"`
	TotalFiles          int64      `gorm:"not null;default:0;comment:本次目标文件数"`
	SubmittedFiles      int64      `gorm:"not null;default:0;comment:已投递Q2任务数"`
	CompletedFiles      int64      `gorm:"not null;default:0;comment:已完成文件数"`
	FailedFiles         int64      `gorm:"not null;default:0;comment:失败文件数"`
	LastError           string     `gorm:"type:text;not null;default:'';comment:最近错误"`
	LeaseOwner          string     `gorm:"size:128;not null;default:'';comment:当前编排进程租约持有者"`
	LeaseExpiresAt      *time.Time `gorm:"index:idx_chat_embedding_reindex_jobs_lease_expires_at;comment:编排租约截止时间"`
	StartedAt           *time.Time `gorm:"comment:开始执行时间"`
	CompletedAt         *time.Time `gorm:"comment:完成时间"`
}

func (EmbeddingReindexJob) TableName() string {
	return "chat_embedding_reindex_jobs"
}
