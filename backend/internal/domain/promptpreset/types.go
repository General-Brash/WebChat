package promptpreset

import "time"

const (
	// ScopeBuiltin 表示管理员维护的全局内置提示词。
	ScopeBuiltin = "builtin"
	// ScopeUser 表示用户维护的个人自定义提示词。
	ScopeUser = "user"
)

// PromptPreset 表示可通过 slash 命令触发的预制提示词。
type PromptPreset struct {
	ID              uint
	Scope           string
	OwnerUserID     uint
	Title           string
	Trigger         string
	Description     string
	Content         string
	Enabled         bool
	SortOrder       int
	CreatedByUserID uint
	UpdatedByUserID uint
	CreatedAt       time.Time
	UpdatedAt       time.Time
}
