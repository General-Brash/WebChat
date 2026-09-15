package repository

import (
	"context"

	domainpromptpreset "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/promptpreset"
)

// PromptPresetRepository 定义预制提示词持久化能力。
type PromptPresetRepository interface {
	ListPromptPresets(ctx context.Context, filter PromptPresetListFilter, offset int, limit int) ([]domainpromptpreset.PromptPreset, int64, error)
	GetPromptPreset(ctx context.Context, id uint) (*domainpromptpreset.PromptPreset, error)
	CreatePromptPreset(ctx context.Context, item *domainpromptpreset.PromptPreset) (*domainpromptpreset.PromptPreset, error)
	PatchPromptPreset(ctx context.Context, id uint, patch PromptPresetPatch) (*domainpromptpreset.PromptPreset, error)
	DeletePromptPreset(ctx context.Context, id uint) error
}

// PromptPresetListFilter 描述预制提示词列表筛选条件。
type PromptPresetListFilter struct {
	Query         string
	Scope         string
	OwnerUserID   *uint
	Enabled       *bool
	VisibleUserID *uint
}

// PromptPresetPatch 描述可更新的预制提示词字段。
type PromptPresetPatch struct {
	Title              *string
	Trigger            *string
	Description        *string
	Content            *string
	Enabled            *bool
	SortOrder          *int
	UpdatedByUserIDSet bool
	UpdatedByUserID    uint
}
