package sub2

import (
	"context"
	"errors"
	"testing"

	domainsub2 "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/sub2"
	model "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/persistence/models"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/repository"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestGetExternalExecutionByProviderResponseIDScopesOwnerAndApplication(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:sub2_response_lookup?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&model.Sub2ExternalExecution{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	repo := NewRepo(db)
	items := []*domainsub2.ExternalExecution{
		{ExecutionID: "exec-wrong-user", UserID: 8, TriggererUserID: 8, ResourceOwnerUserID: 8, Purpose: "chat.main", Application: "chat", MetadataJSON: `{"provider_response_id":"resp_1"}`},
		{ExecutionID: "exec-wrong-app", UserID: 7, TriggererUserID: 7, ResourceOwnerUserID: 7, Purpose: "chat.main", Application: "other", MetadataJSON: `{"provider_response_id":"resp_1"}`},
		{ExecutionID: "exec-compact", UserID: 7, TriggererUserID: 7, ResourceOwnerUserID: 7, Purpose: "chat.main", Application: "chat", MetadataJSON: `{"provider_response_id":"resp_1","request_model":"model"}`},
		{ExecutionID: "exec-spaced", UserID: 7, TriggererUserID: 7, ResourceOwnerUserID: 7, Purpose: "chat.main", Application: "chat", MetadataJSON: `{"provider_response_id": "resp_2"}`},
	}
	for _, item := range items {
		if err := repo.CreateExternalExecution(context.Background(), item); err != nil {
			t.Fatalf("create %s: %v", item.ExecutionID, err)
		}
	}

	got, err := repo.GetExternalExecutionByProviderResponseID(context.Background(), 7, "chat", "resp_1")
	if err != nil {
		t.Fatalf("lookup compact metadata: %v", err)
	}
	if got.ExecutionID != "exec-compact" {
		t.Fatalf("lookup returned %q, want exec-compact", got.ExecutionID)
	}
	got, err = repo.GetExternalExecutionByProviderResponseID(context.Background(), 7, "chat", "resp_2")
	if err != nil || got.ExecutionID != "exec-spaced" {
		t.Fatalf("lookup spaced metadata = %#v, %v", got, err)
	}
	if _, err := repo.GetExternalExecutionByProviderResponseID(context.Background(), 8, "chat", "resp_2"); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("wrong owner lookup error = %v, want not found", err)
	}
}

func TestExternalExecutionRoundTripAndImmutableAttribution(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:sub2_attribution_roundtrip?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&model.Sub2ExternalExecution{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	repo := NewRepo(db)
	item := &domainsub2.ExternalExecution{
		ExecutionID:         "exec-attribution",
		UserID:              11,
		TriggererUserID:     11,
		ResourceOwnerUserID: 0,
		Purpose:             "file.extract",
		ParentExecutionID:   "parent-a",
		Application:         "chat",
		ChatRunID:           "run-a",
		State:               domainsub2.ExecutionStateDispatched,
	}
	if err := repo.CreateExternalExecution(context.Background(), item); err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := repo.GetExternalExecution(context.Background(), 11, item.ExecutionID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.TriggererUserID != 11 || got.ResourceOwnerUserID != 0 || got.Purpose != "file.extract" || got.ParentExecutionID != "parent-a" {
		t.Fatalf("attribution roundtrip = %#v", got)
	}
	got.TriggererUserID = 12
	if err := repo.UpdateExternalExecution(context.Background(), got); !errors.Is(err, domainsub2.ErrExecutionAttributionMismatch) {
		t.Fatalf("mismatched trigger update error = %v", err)
	}
}

func TestLegacyExternalExecutionCannotBeBackfilledWithTriggerer(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:sub2_legacy_backfill?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&model.Sub2ExternalExecution{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	legacy := &model.Sub2ExternalExecution{ExecutionID: "legacy-exec", UserID: 11, State: domainsub2.ExecutionStateSettled}
	if err := db.Create(legacy).Error; err != nil {
		t.Fatalf("seed legacy row: %v", err)
	}
	item := toDomainExternalExecution(*legacy)
	item.TriggererUserID = item.UserID
	if err := NewRepo(db).UpdateExternalExecution(context.Background(), item); !errors.Is(err, domainsub2.ErrExecutionAttributionMismatch) {
		t.Fatalf("legacy backfill error = %v, want attribution mismatch", err)
	}
}
