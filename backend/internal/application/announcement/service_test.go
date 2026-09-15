package announcement

import (
	"context"
	"errors"
	"testing"
	"time"

	domainannouncement "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/announcement"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/repository"
)

func TestCreateAnnouncementValidation(t *testing.T) {
	service := NewService(&fakeRepo{})
	if _, err := service.Create(context.Background(), 1, WriteInput{Status: domainannouncement.StatusActive}); !errors.Is(err, ErrInvalidAnnouncement) {
		t.Fatalf("Create() error = %v, want ErrInvalidAnnouncement", err)
	}
	if _, err := service.Create(context.Background(), 0, WriteInput{Title: "A", ContentMarkdown: "Body"}); !errors.Is(err, repository.ErrInvalidInput) {
		t.Fatalf("Create() error = %v, want ErrInvalidInput", err)
	}
}

func TestCreateAnnouncementAcceptsValidWindow(t *testing.T) {
	start := time.Date(2026, 6, 1, 8, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	repo := &fakeRepo{}
	service := NewService(repo)

	item, err := service.Create(context.Background(), 7, WriteInput{
		Title:           "Notice",
		ContentMarkdown: "Hello",
		Status:          domainannouncement.StatusActive,
		Priority:        10,
		StartsAt:        &start,
		ExpiresAt:       &end,
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if item.CreatedByUserID != 7 || item.Priority != 10 || item.Status != domainannouncement.StatusActive {
		t.Fatalf("Create() item = %#v", item)
	}
	if item.Type != domainannouncement.TypeGeneral {
		t.Fatalf("Create() type = %q, want %q", item.Type, domainannouncement.TypeGeneral)
	}
}

func TestUpdateAnnouncementRejectsInvalidStatus(t *testing.T) {
	service := NewService(&fakeRepo{})
	status := "archived"
	if _, err := service.Update(context.Background(), 1, PatchInput{Status: &status}); !errors.Is(err, ErrInvalidAnnouncement) {
		t.Fatalf("Update() error = %v, want ErrInvalidAnnouncement", err)
	}
}

func TestDismissTodayRejectsInvalidInput(t *testing.T) {
	service := NewService(&fakeRepo{})
	now := time.Date(2026, 6, 1, 8, 0, 0, 0, time.UTC)
	if err := service.DismissToday(context.Background(), 0, 1, now, now, now.Add(time.Hour)); !errors.Is(err, repository.ErrInvalidInput) {
		t.Fatalf("DismissToday() error = %v, want ErrInvalidInput", err)
	}
	if err := service.DismissToday(context.Background(), 1, 1, time.Time{}, now, now.Add(time.Hour)); !errors.Is(err, repository.ErrInvalidInput) {
		t.Fatalf("DismissToday() error = %v, want ErrInvalidInput", err)
	}
	if err := service.DismissToday(context.Background(), 1, 1, now, now, now); !errors.Is(err, repository.ErrInvalidInput) {
		t.Fatalf("DismissToday() error = %v, want ErrInvalidInput", err)
	}
}

func TestListActivePassesIncludeDismissed(t *testing.T) {
	repo := &fakeRepo{}
	service := NewService(repo)
	if _, err := service.ListActive(context.Background(), 1, time.Now(), true); err != nil {
		t.Fatalf("ListActive() error = %v", err)
	}
	if !repo.includeDismissed {
		t.Fatal("ListActive() did not pass includeDismissed to repository")
	}
}

func TestCloseRejectsInvalidInput(t *testing.T) {
	service := NewService(&fakeRepo{})
	now := time.Now()
	if err := service.Close(context.Background(), 0, 1, now, now); !errors.Is(err, repository.ErrInvalidInput) {
		t.Fatalf("Close() error = %v, want ErrInvalidInput", err)
	}
	if err := service.Close(context.Background(), 1, 0, now, now); !errors.Is(err, repository.ErrInvalidInput) {
		t.Fatalf("Close() error = %v, want ErrInvalidInput", err)
	}
	if err := service.Close(context.Background(), 1, 1, time.Time{}, now); !errors.Is(err, repository.ErrInvalidInput) {
		t.Fatalf("Close() error = %v, want ErrInvalidInput", err)
	}
}

type fakeRepo struct {
	item             domainannouncement.Announcement
	includeDismissed bool
}

func (r *fakeRepo) ListActiveAnnouncements(_ context.Context, _ uint, _ time.Time, includeDismissed bool) ([]domainannouncement.Announcement, error) {
	r.includeDismissed = includeDismissed
	return []domainannouncement.Announcement{}, nil
}

func (r *fakeRepo) ListAdminAnnouncements(context.Context, repository.AnnouncementListFilter, int, int) ([]domainannouncement.Announcement, int64, error) {
	return []domainannouncement.Announcement{}, 0, nil
}

func (r *fakeRepo) CreateAnnouncement(_ context.Context, item *domainannouncement.Announcement) (*domainannouncement.Announcement, error) {
	item.ID = 1
	r.item = *item
	return item, nil
}

func (r *fakeRepo) PatchAnnouncement(_ context.Context, id uint, patch repository.AnnouncementPatch) (*domainannouncement.Announcement, error) {
	if id == 0 {
		return nil, repository.ErrInvalidInput
	}
	if patch.Status != nil {
		r.item.Status = *patch.Status
	}
	if patch.Type != nil {
		r.item.Type = *patch.Type
	}
	if patch.Pinned != nil {
		r.item.Pinned = *patch.Pinned
	}
	return &r.item, nil
}

func (r *fakeRepo) DeleteAnnouncement(context.Context, uint) error {
	return nil
}

func (r *fakeRepo) DismissAnnouncementToday(context.Context, uint, uint, time.Time, time.Time, time.Time) error {
	return nil
}

func (r *fakeRepo) CloseAnnouncement(context.Context, uint, uint, time.Time, time.Time) error {
	return nil
}
