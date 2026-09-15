package user

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	model "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/persistence/models"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/repository"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestUpdateFieldsKeepsLastSuperAdminProtected(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("DEEIX_TEST_DATABASE_DSN"))
	if dsn == "" {
		t.Skip("set DEEIX_TEST_DATABASE_DSN to run PostgreSQL repository guard integration test")
	}

	db, cleanup := openUserRepositoryIntegrationDB(t, dsn)
	defer cleanup()

	userItem := model.User{
		PublicID: "superadmin_public_id",
		Username: "root",
		Role:     model.RoleSuperAdmin,
		Status:   model.UserStatusActive,
		Timezone: "Etc/UTC",
		Locale:   "en-US",
	}
	if err := db.Create(&userItem).Error; err != nil {
		t.Fatalf("create superadmin: %v", err)
	}

	nextRole := model.RoleAdmin
	_, err := NewRepo(db).UpdateFields(context.Background(), userItem.ID, repository.UpdateUserFieldsInput{
		Role: &nextRole,
	})
	if !errors.Is(err, repository.ErrLastSuperAdminRoleChange) {
		t.Fatalf("expected last superadmin guard, got %v", err)
	}

	var persisted model.User
	if err := db.First(&persisted, userItem.ID).Error; err != nil {
		t.Fatalf("reload user: %v", err)
	}
	if persisted.Role != model.RoleSuperAdmin {
		t.Fatalf("expected role to remain %q, got %q", model.RoleSuperAdmin, persisted.Role)
	}
}

func openUserRepositoryIntegrationDB(t *testing.T, dsn string) (*gorm.DB, func()) {
	t.Helper()

	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open postgres: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("resolve sql db: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)

	schemaName := fmt.Sprintf("deeix_test_superadmin_guard_%d", time.Now().UnixNano())
	if err := db.Exec(`CREATE SCHEMA ` + schemaName).Error; err != nil {
		_ = sqlDB.Close()
		t.Fatalf("create schema: %v", err)
	}
	cleanup := func() {
		_ = db.Exec(`DROP SCHEMA IF EXISTS ` + schemaName + ` CASCADE`).Error
		_ = sqlDB.Close()
	}
	if err := db.Exec(`SET search_path TO ` + schemaName).Error; err != nil {
		cleanup()
		t.Fatalf("set search_path: %v", err)
	}
	if err := db.AutoMigrate(&model.User{}); err != nil {
		cleanup()
		t.Fatalf("migrate user table: %v", err)
	}
	return db, cleanup
}
