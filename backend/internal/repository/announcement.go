package repository

import (
	"context"
	"time"

	domainannouncement "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/announcement"
)

// AnnouncementRepository 定义公告流程依赖的持久化能力。
type AnnouncementRepository interface {
	ListActiveAnnouncements(ctx context.Context, userID uint, now time.Time, includeDismissed bool) ([]domainannouncement.Announcement, error)
	ListAdminAnnouncements(ctx context.Context, filter AnnouncementListFilter, offset int, limit int) ([]domainannouncement.Announcement, int64, error)
	CreateAnnouncement(ctx context.Context, item *domainannouncement.Announcement) (*domainannouncement.Announcement, error)
	PatchAnnouncement(ctx context.Context, id uint, patch AnnouncementPatch) (*domainannouncement.Announcement, error)
	DeleteAnnouncement(ctx context.Context, id uint) error
	DismissAnnouncementToday(ctx context.Context, userID uint, announcementID uint, announcementUpdatedAt time.Time, now time.Time, dismissedUntil time.Time) error
	CloseAnnouncement(ctx context.Context, userID uint, announcementID uint, announcementUpdatedAt time.Time, now time.Time) error
}

// AnnouncementListFilter 描述管理员公告列表筛选条件。
type AnnouncementListFilter struct {
	Query  string
	Status string
	Type   string
	Pinned *bool
}

// AnnouncementPatch 描述可更新的公告字段。
type AnnouncementPatch struct {
	Title              *string
	ContentMarkdown    *string
	Status             *string
	Type               *string
	Pinned             *bool
	Priority           *int
	StartsAtSet        bool
	StartsAt           *time.Time
	ExpiresAtSet       bool
	ExpiresAt          *time.Time
	CreatedByUserIDSet bool
	CreatedByUserID    uint
}
