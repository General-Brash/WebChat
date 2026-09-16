package model

import "time"

// Sub2IdentityBinding stores the immutable (issuer, subject) identity key used
// by the Chat-side Sub2 integration. Email fields are profile snapshots only.
type Sub2IdentityBinding struct {
	BaseModel
	UserID                    uint       `gorm:"not null;default:0;uniqueIndex:uk_sub2_identity_user_id;comment:Chat用户ID"`
	Issuer                    string     `gorm:"size:512;not null;default:'';uniqueIndex:uk_sub2_identity_issuer_subject,priority:1;comment:Sub2 OIDC issuer"`
	Subject                   string     `gorm:"size:255;not null;default:'';uniqueIndex:uk_sub2_identity_issuer_subject,priority:2;comment:Sub2 OIDC subject"`
	ExternalUserID            string     `gorm:"size:128;not null;default:'';comment:Sub2稳定用户ID"`
	IdentityEpoch             uint64     `gorm:"not null;default:0;comment:Sub2身份版本，不代表权限版本"`
	Status                    string     `gorm:"size:32;not null;default:'active';index:idx_sub2_identity_status;comment:Sub2身份状态"`
	Role                      string     `gorm:"size:32;not null;default:'user';index:idx_sub2_identity_role;comment:Sub2权威角色"`
	PermissionVersion         int64      `gorm:"not null;default:0;index:idx_sub2_identity_permission_version;comment:Sub2权限版本"`
	SubjectAssertionEncrypted string     `gorm:"type:text;not null;default:'';comment:加密的Sub2绑定证明，不存原文"`
	SubjectAssertionExpiresAt *time.Time `gorm:"index:idx_sub2_identity_assertion_expires_at;comment:Sub2绑定证明过期时间"`
	Email                     string     `gorm:"size:128;not null;default:'';comment:身份资料邮箱快照，不用于自动关联"`
	EmailVerified             bool       `gorm:"not null;default:false;comment:身份资料邮箱验证快照"`
	LastCheckedAt             *time.Time `gorm:"index:idx_sub2_identity_last_checked_at;comment:最近一次Sub2状态核验时间"`
	LastLoginAt               *time.Time `gorm:"index:idx_sub2_identity_last_login_at;comment:最近一次SSO登录时间"`
}

func (Sub2IdentityBinding) TableName() string {
	return "integration_sub2_identity_bindings"
}

// Sub2ExternalExecution persists the Chat-side execution state machine and
// authority references. It intentionally has no credential or token fields.
type Sub2ExternalExecution struct {
	BaseModel
	ExecutionID string `gorm:"size:64;not null;uniqueIndex:uk_sub2_external_executions_execution_id;comment:Chat外部执行ID"`
	// UserID is the legacy authority-scope column. Existing rows are not
	// reinterpreted; new paid rows use the triggerer for compatibility.
	UserID                 uint       `gorm:"not null;default:0;index:idx_sub2_external_executions_user_id;comment:Legacy authority scope user ID"`
	TriggererUserID        uint       `gorm:"not null;default:0;index:idx_sub2_external_executions_triggerer_user_id;comment:Trusted authenticated triggerer user ID"`
	ResourceOwnerUserID    uint       `gorm:"not null;default:0;index:idx_sub2_external_executions_resource_owner_user_id;comment:Resource owner user ID, zero for platform"`
	Purpose                string     `gorm:"size:64;not null;default:'';comment:Trusted operation purpose"`
	ParentExecutionID      string     `gorm:"size:64;not null;default:'';index:idx_sub2_external_executions_parent_execution_id;comment:Trusted parent execution reference"`
	Issuer                 string     `gorm:"size:512;not null;default:'';comment:身份源issuer快照"`
	Subject                string     `gorm:"size:255;not null;default:'';comment:身份subject快照"`
	ExternalUserID         string     `gorm:"size:128;not null;default:'';comment:Sub2用户ID快照"`
	IdentityEpoch          uint64     `gorm:"not null;default:0;comment:执行时身份版本"`
	Application            string     `gorm:"size:64;not null;default:'';index:idx_sub2_external_executions_application;comment:来源应用"`
	ChatRunID              string     `gorm:"size:64;not null;default:'';index:idx_sub2_external_executions_chat_run_id;comment:Chat运行ID"`
	ConversationID         uint       `gorm:"not null;default:0;index:idx_sub2_external_executions_conversation_id;comment:Chat会话ID"`
	TaskType               string     `gorm:"size:32;not null;default:'';comment:任务类型"`
	ModelName              string     `gorm:"size:255;not null;default:'';comment:模型标识快照"`
	RequestHash            string     `gorm:"size:64;not null;default:'';comment:请求摘要"`
	IdempotencyKey         string     `gorm:"size:128;not null;default:'';index:idx_sub2_external_executions_idempotency_key;comment:幂等键"`
	State                  string     `gorm:"size:32;not null;default:'CREATED';index:idx_sub2_external_executions_state;comment:外部执行状态"`
	TerminalStatus         string     `gorm:"size:32;not null;default:'';comment:终态快照"`
	AuthorityExecutionID   string     `gorm:"size:128;not null;default:'';index:idx_sub2_external_executions_authority_execution_id;comment:Sub2执行引用"`
	AuthorityUsageID       string     `gorm:"size:128;not null;default:'';comment:Sub2用量引用"`
	AuthorityTransactionID string     `gorm:"size:128;not null;default:'';comment:Sub2账务引用"`
	ErrorCode              string     `gorm:"size:64;not null;default:'';comment:权威错误码"`
	ErrorMessage           string     `gorm:"size:255;not null;default:'';comment:脱敏错误摘要"`
	MetadataJSON           string     `gorm:"type:text;not null;default:'';comment:非敏感执行元数据JSON"`
	AuthorizedAt           *time.Time `gorm:"index:idx_sub2_external_executions_authorized_at;comment:授权时间"`
	DispatchedAt           *time.Time `gorm:"index:idx_sub2_external_executions_dispatched_at;comment:派发时间"`
	UnknownAt              *time.Time `gorm:"index:idx_sub2_external_executions_unknown_at;comment:进入UNKNOWN时间"`
	ReconciliationAt       *time.Time `gorm:"index:idx_sub2_external_executions_reconciliation_at;comment:进入RECONCILING时间"`
	TerminalAt             *time.Time `gorm:"index:idx_sub2_external_executions_terminal_at;comment:终态时间"`
	CancelRequestedAt      *time.Time `gorm:"comment:取消请求时间"`
	LastQueriedAt          *time.Time `gorm:"index:idx_sub2_external_executions_last_queried_at;comment:最近一次权威查询时间"`
}

func (Sub2ExternalExecution) TableName() string {
	return "integration_sub2_external_executions"
}

// Sub2ApplicationPublication stores confirmed external configuration references,
// never funds. Draft price edits cannot silently replace these frozen versions.
type Sub2ApplicationPublication struct {
	Application    string `gorm:"primaryKey;size:128"`
	GroupRevision  int64
	PricingVersion int64
	PlayerVersion  int64
	// PricingHash stores the deterministic publication fingerprint. It includes
	// the retail strategy hash plus group/model/protocol/sync revisions, so the
	// field name remains backward-compatible while readiness covers the whole
	// published allowlist.
	PricingHash string `gorm:"size:64"`
	UpdatedAt   time.Time
}

func (Sub2ApplicationPublication) TableName() string { return "sub2_application_publications" }
