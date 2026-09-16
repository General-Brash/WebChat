package channel

import "time"

// PermissionGroup 定义模型访问权限组。
type PermissionGroup struct {
	ID                    uint
	Name                  string
	Description           string
	IsDefault             bool
	RateMultiplierPercent int
	ModelCount            int64
	ManualModelCount      int64
	RuleModelCount        int64
	UserCount             int64
	ManualUserCount       int64
	SubscriptionUserCount int64
	CreatedAt             time.Time
	UpdatedAt             time.Time
}

// PermissionGroupModelAccess 关联权限组与平台模型。
type PermissionGroupModelAccess struct {
	GroupID         uint
	PlatformModelID uint
}

const (
	PermissionGroupModelRuleAll      = "all"
	PermissionGroupModelRuleVendor   = "vendor"
	PermissionGroupModelRuleProtocol = "protocol"
	PermissionGroupModelRuleUpstream = "upstream"
)

// PermissionGroupModelRule 定义权限组的动态模型访问规则。
type PermissionGroupModelRule struct {
	GroupID  uint
	RuleType string
	Value    string
}

// PermissionGroupUserAccess 关联权限组与用户。
type PermissionGroupUserAccess struct {
	GroupID uint
	UserID  uint
}

// PermissionGroupDeleteSummary 描述删除权限组前清理的关联规模。
type PermissionGroupDeleteSummary struct {
	ManualModelCount int64
	RuleCount        int64
	ManualUserCount  int64
	PlanCount        int64
}
