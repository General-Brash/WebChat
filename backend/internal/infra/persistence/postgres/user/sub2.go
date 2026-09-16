package user

import (
	"context"
	"strings"
	"time"

	domainsub2 "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/sub2"
	domainuser "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/user"
	model "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/persistence/models"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/repository"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// GetSub2IdentityBinding looks up the immutable (issuer, subject) binding.
func (r *Repo) GetSub2IdentityBinding(ctx context.Context, issuer string, subject string) (*domainsub2.IdentityBinding, error) {
	var item model.Sub2IdentityBinding
	if err := r.db.WithContext(ctx).
		Where("issuer = ? AND subject = ?", strings.TrimSpace(issuer), strings.TrimSpace(subject)).
		First(&item).Error; err != nil {
		return nil, translateError(err)
	}
	return toDomainSub2IdentityBinding(item), nil
}

// GetSub2IdentityBindingByUserID returns the single Chat-side Sub2 binding for a user.
func (r *Repo) GetSub2IdentityBindingByUserID(ctx context.Context, userID uint) (*domainsub2.IdentityBinding, error) {
	var item model.Sub2IdentityBinding
	if err := r.db.WithContext(ctx).Where("user_id = ?", userID).First(&item).Error; err != nil {
		return nil, translateError(err)
	}
	return toDomainSub2IdentityBinding(item), nil
}

// CreateSub2IdentityBinding creates a binding without changing any user or balance data.
func (r *Repo) CreateSub2IdentityBinding(ctx context.Context, item *domainsub2.IdentityBinding) (*domainsub2.IdentityBinding, error) {
	if item == nil || item.UserID == 0 || !validSub2IdentityBinding(item) {
		return nil, repository.ErrInvalidInput
	}
	dbItem := toModelSub2IdentityBinding(item)
	if err := r.db.WithContext(ctx).Create(dbItem).Error; err != nil {
		return nil, translateError(err)
	}
	item.ID = dbItem.ID
	item.CreatedAt = dbItem.CreatedAt
	item.UpdatedAt = dbItem.UpdatedAt
	return toDomainSub2IdentityBinding(*dbItem), nil
}

// UpdateSub2IdentityBinding preserves the legacy repository method while
// routing all role/status/binding changes through the atomic synchronizer.
func (r *Repo) UpdateSub2IdentityBinding(ctx context.Context, item *domainsub2.IdentityBinding) error {
	if item == nil || item.ID == 0 || item.UserID == 0 || !validSub2IdentityBinding(item) {
		return repository.ErrInvalidInput
	}
	chatRole, ok := domainsub2.MapExternalRole(item.Role)
	if !ok {
		return repository.ErrInvalidInput
	}
	localStatus := domainuser.StatusSuspended
	if strings.TrimSpace(item.Status) == domainsub2.IdentityStatusActive {
		localStatus = domainuser.StatusActive
	}
	return r.SyncSub2UserAndIdentity(ctx, repository.SyncSub2UserAndIdentityInput{
		Identity:   item,
		UserRole:   chatRole,
		UserStatus: localStatus,
	})
}

// CreateSub2UserWithIdentity atomically creates the JIT user, disabled local
// credential and trusted Sub2 binding in Chat's own database.
func (r *Repo) CreateSub2UserWithIdentity(ctx context.Context, input repository.CreateSub2UserWithIdentityInput) error {
	if input.User == nil || input.Identity == nil || !validSub2IdentityBinding(input.Identity) {
		return repository.ErrInvalidInput
	}
	mappedRole, ok := domainsub2.MapExternalRole(input.Identity.Role)
	if !ok || mappedRole != strings.TrimSpace(input.User.Role) ||
		input.User.Status != domainuser.StatusActive ||
		input.Identity.Status != domainsub2.IdentityStatusActive ||
		input.Credential.PasswordEnabled {
		return repository.ErrInvalidInput
	}
	return translateError(r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := r.createWithCredentialTx(tx, input.CreateWithCredentialInput); err != nil {
			return err
		}
		input.Identity.UserID = input.User.ID
		dbIdentity := toModelSub2IdentityBinding(input.Identity)
		if err := tx.Create(dbIdentity).Error; err != nil {
			return translateError(err)
		}
		input.Identity.ID = dbIdentity.ID
		input.Identity.CreatedAt = dbIdentity.CreatedAt
		input.Identity.UpdatedAt = dbIdentity.UpdatedAt
		return nil
	}))
}

// SyncSub2UserAndIdentity atomically applies the authoritative role/status and
// binding snapshot. A lower permission version is ignored; a same-version
// contradictory role/epoch quarantines the local user and revokes sessions.
func (r *Repo) SyncSub2UserAndIdentity(ctx context.Context, input repository.SyncSub2UserAndIdentityInput) error {
	identity := input.Identity
	if identity == nil || identity.ID == 0 || identity.UserID == 0 || !validSub2IdentityBinding(identity) {
		return repository.ErrInvalidInput
	}
	mappedRole, ok := domainsub2.MapExternalRole(identity.Role)
	if !ok || mappedRole != strings.TrimSpace(input.UserRole) {
		return repository.ErrInvalidInput
	}
	if input.UserStatus != domainuser.StatusActive && input.UserStatus != domainuser.StatusSuspended {
		return repository.ErrInvalidInput
	}

	var postCommitErr error
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var currentBinding model.Sub2IdentityBinding
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ? AND user_id = ?", identity.ID, identity.UserID).
			First(&currentBinding).Error; err != nil {
			return translateError(err)
		}
		if currentBinding.Issuer != strings.TrimSpace(identity.Issuer) ||
			currentBinding.Subject != strings.TrimSpace(identity.Subject) {
			return repository.ErrConflict
		}

		var currentUser model.User
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ?", identity.UserID).
			First(&currentUser).Error; err != nil {
			return translateError(err)
		}

		// Security epoch is primary: a demoted administrator may legitimately
		// have permission_version=0. An older response must never revoke a
		// newer session or prevent authoritative demotion.
		if identity.IdentityEpoch < currentBinding.IdentityEpoch ||
			(identity.IdentityEpoch == currentBinding.IdentityEpoch && identity.PermissionVersion < currentBinding.PermissionVersion) {
			return nil
		}
		if identity.IdentityEpoch == currentBinding.IdentityEpoch &&
			(currentBinding.Role != strings.TrimSpace(identity.Role) || currentBinding.Status != strings.TrimSpace(identity.Status)) {
			if err := quarantineSub2UserTx(tx, identity.UserID, "sub2_identity_snapshot_conflict"); err != nil {
				return err
			}
			postCommitErr = repository.ErrConflict
			return nil
		}

		localRole := strings.TrimSpace(input.UserRole)
		localStatus := strings.TrimSpace(input.UserStatus)
		if strings.TrimSpace(identity.Status) != domainsub2.IdentityStatusActive {
			localRole = domainuser.RoleUser
			localStatus = domainuser.StatusSuspended
		}
		shouldRevoke := shouldRevokeSub2Sessions(
			currentUser.Role,
			currentUser.Status,
			localRole,
			localStatus,
			currentBinding.IdentityEpoch,
			identity.IdentityEpoch,
		)

		updates := sub2IdentityBindingUpdates(identity)
		result := tx.Model(&model.Sub2IdentityBinding{}).
			Where("id = ? AND user_id = ?", identity.ID, identity.UserID).
			Updates(updates)
		if result.Error != nil {
			return translateError(result.Error)
		}
		if result.RowsAffected == 0 {
			return repository.ErrConflict
		}

		userResult := tx.Model(&model.User{}).
			Where("id = ?", identity.UserID).
			Updates(map[string]any{
				"role":   localRole,
				"status": localStatus,
			})
		if userResult.Error != nil {
			return translateError(userResult.Error)
		}
		if userResult.RowsAffected == 0 {
			return repository.ErrNotFound
		}
		credentialResult := tx.Model(&model.UserCredential{}).
			Where("user_id = ?", identity.UserID).
			Update("password_enabled", false)
		if credentialResult.Error != nil {
			return translateError(credentialResult.Error)
		}
		if credentialResult.RowsAffected == 0 {
			return repository.ErrNotFound
		}
		if shouldRevoke {
			if err := revokeSub2SessionsTx(tx, identity.UserID, "sub2_identity_changed"); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	return postCommitErr
}

func shouldRevokeSub2Sessions(previousRole, previousStatus, nextRole, nextStatus string, previousEpoch, nextEpoch uint64) bool {
	return domainsub2.IsRoleDowngrade(previousRole, nextRole) ||
		(previousStatus == domainuser.StatusActive && nextStatus != domainuser.StatusActive) ||
		(previousEpoch != 0 && previousEpoch != nextEpoch)
}

func validSub2IdentityBinding(item *domainsub2.IdentityBinding) bool {
	if item == nil || strings.TrimSpace(item.Issuer) == "" || strings.TrimSpace(item.Subject) == "" {
		return false
	}
	if strings.TrimSpace(item.ExternalUserID) == "" || item.IdentityEpoch == 0 || item.PermissionVersion < 0 ||
		strings.TrimSpace(item.SubjectAssertionEncrypted) == "" || item.SubjectAssertionExpiresAt == nil ||
		!item.SubjectAssertionExpiresAt.After(time.Now()) {
		return false
	}
	_, ok := domainsub2.MapExternalRole(item.Role)
	return ok && strings.TrimSpace(item.Status) != ""
}

func sub2IdentityBindingUpdates(item *domainsub2.IdentityBinding) map[string]any {
	return map[string]any{
		"external_user_id":             strings.TrimSpace(item.ExternalUserID),
		"identity_epoch":               item.IdentityEpoch,
		"status":                       strings.TrimSpace(item.Status),
		"role":                         strings.TrimSpace(item.Role),
		"permission_version":           item.PermissionVersion,
		"subject_assertion_encrypted":  strings.TrimSpace(item.SubjectAssertionEncrypted),
		"subject_assertion_expires_at": item.SubjectAssertionExpiresAt,
		"email":                        strings.TrimSpace(item.Email),
		"email_verified":               item.EmailVerified,
		"last_checked_at":              item.LastCheckedAt,
		"last_login_at":                item.LastLoginAt,
	}
}

func quarantineSub2UserTx(tx *gorm.DB, userID uint, reason string) error {
	userResult := tx.Model(&model.User{}).
		Where("id = ?", userID).
		Updates(map[string]any{
			"role":   domainuser.RoleUser,
			"status": domainuser.StatusSuspended,
		})
	if userResult.Error != nil {
		return translateError(userResult.Error)
	}
	if userResult.RowsAffected == 0 {
		return repository.ErrNotFound
	}
	return revokeSub2SessionsTx(tx, userID, reason)
}

func revokeSub2SessionsTx(tx *gorm.DB, userID uint, reason string) error {
	now := time.Now()
	return translateError(tx.Model(&model.UserSession{}).
		Where("user_id = ? AND revoked_at IS NULL", userID).
		Updates(map[string]any{
			"revoked_at":    now,
			"revoke_reason": reason,
		}).Error)
}

func toDomainSub2IdentityBinding(item model.Sub2IdentityBinding) *domainsub2.IdentityBinding {
	return &domainsub2.IdentityBinding{
		ID:                        item.ID,
		UserID:                    item.UserID,
		Issuer:                    item.Issuer,
		Subject:                   item.Subject,
		ExternalUserID:            item.ExternalUserID,
		IdentityEpoch:             item.IdentityEpoch,
		Status:                    item.Status,
		Role:                      item.Role,
		PermissionVersion:         item.PermissionVersion,
		SubjectAssertionEncrypted: item.SubjectAssertionEncrypted,
		SubjectAssertionExpiresAt: item.SubjectAssertionExpiresAt,
		Email:                     item.Email,
		EmailVerified:             item.EmailVerified,
		LastCheckedAt:             item.LastCheckedAt,
		LastLoginAt:               item.LastLoginAt,
		CreatedAt:                 item.CreatedAt,
		UpdatedAt:                 item.UpdatedAt,
	}
}

func toModelSub2IdentityBinding(item *domainsub2.IdentityBinding) *model.Sub2IdentityBinding {
	if item == nil {
		return &model.Sub2IdentityBinding{}
	}
	return &model.Sub2IdentityBinding{
		BaseModel: model.BaseModel{
			ID:        item.ID,
			CreatedAt: item.CreatedAt,
			UpdatedAt: item.UpdatedAt,
		},
		UserID:                    item.UserID,
		Issuer:                    strings.TrimSpace(item.Issuer),
		Subject:                   strings.TrimSpace(item.Subject),
		ExternalUserID:            strings.TrimSpace(item.ExternalUserID),
		IdentityEpoch:             item.IdentityEpoch,
		Status:                    strings.TrimSpace(item.Status),
		Role:                      strings.TrimSpace(item.Role),
		PermissionVersion:         item.PermissionVersion,
		SubjectAssertionEncrypted: strings.TrimSpace(item.SubjectAssertionEncrypted),
		SubjectAssertionExpiresAt: item.SubjectAssertionExpiresAt,
		Email:                     strings.TrimSpace(item.Email),
		EmailVerified:             item.EmailVerified,
		LastCheckedAt:             item.LastCheckedAt,
		LastLoginAt:               item.LastLoginAt,
	}
}
