package admin

import (
	"context"
	"errors"
	"strings"

	userapp "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/application/user"
	domainchannel "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/channel"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/repository"
)

type permissionGroupRepo interface {
	ListPermissionGroups(ctx context.Context) ([]domainchannel.PermissionGroup, error)
	GetPermissionGroup(ctx context.Context, id uint) (*domainchannel.PermissionGroup, error)
	CreatePermissionGroup(ctx context.Context, item *domainchannel.PermissionGroup) error
	UpdatePermissionGroup(ctx context.Context, id uint, name string, description string, rateMultiplierPercent int) (*domainchannel.PermissionGroup, error)
	DeletePermissionGroup(ctx context.Context, id uint) error
	GetPermissionGroupDeleteSummary(ctx context.Context, id uint) (domainchannel.PermissionGroupDeleteSummary, error)
	ListGroupModelIDs(ctx context.Context, groupID uint) ([]uint, error)
	ListGroupModelRules(ctx context.Context, groupID uint) ([]domainchannel.PermissionGroupModelRule, error)
	SetGroupModelAccess(ctx context.Context, groupID uint, modelIDs []uint, rules []domainchannel.PermissionGroupModelRule) error
	ListModelManualGroupIDs(ctx context.Context, platformModelID uint) ([]uint, error)
	ListModelRuleGroupIDs(ctx context.Context, platformModelID uint) ([]uint, error)
	ListModelGroupIDs(ctx context.Context, platformModelID uint) ([]uint, error)
	SetModelManualGroups(ctx context.Context, platformModelID uint, groupIDs []uint) error
	ListGroupUserIDs(ctx context.Context, groupID uint) ([]uint, error)
	SetGroupUsers(ctx context.Context, groupID uint, userIDs []uint) error
}

type permissionGroupModelLookup interface {
	GetModelByID(ctx context.Context, modelID uint) (*domainchannel.PlatformModel, error)
}

type permissionGroupBillingPlanReferenceChecker interface {
	CountPlansWithPermissionGroup(ctx context.Context, groupID uint) (int64, error)
}

// SetPermissionGroupRepo 注入权限组仓储能力。
func (s *Service) SetPermissionGroupRepo(repo permissionGroupRepo) {
	s.permissionGroupRepo = repo
}

// SetPermissionGroupModelLookup 注入权限组模型目标校验能力。
func (s *Service) SetPermissionGroupModelLookup(lookup permissionGroupModelLookup) {
	s.permissionGroupModelLookup = lookup
}

// SetPermissionGroupBillingPlanReferenceChecker 注入权限组套餐引用校验能力。
func (s *Service) SetPermissionGroupBillingPlanReferenceChecker(checker permissionGroupBillingPlanReferenceChecker) {
	s.permissionGroupBillingPlanReferenceChecker = checker
}

// ListPermissionGroups 返回全部权限组。
func (s *Service) ListPermissionGroups(ctx context.Context) ([]domainchannel.PermissionGroup, error) {
	if s.permissionGroupRepo == nil {
		return nil, ErrPermissionGroupRepoUnavailable
	}
	return s.permissionGroupRepo.ListPermissionGroups(ctx)
}

// CreatePermissionGroup 创建权限组。
func (s *Service) CreatePermissionGroup(ctx context.Context, name, description string, rateMultiplierPercent int) (*domainchannel.PermissionGroup, error) {
	if s.permissionGroupRepo == nil {
		return nil, ErrPermissionGroupRepoUnavailable
	}
	trimmedName := strings.TrimSpace(name)
	if trimmedName == "" {
		return nil, ErrInvalidPermissionGroupName
	}
	normalizedPercent, err := normalizePermissionGroupRatePercent(rateMultiplierPercent)
	if err != nil {
		return nil, err
	}
	item := &domainchannel.PermissionGroup{
		Name:                  trimmedName,
		Description:           strings.TrimSpace(description),
		RateMultiplierPercent: normalizedPercent,
	}
	if err := s.permissionGroupRepo.CreatePermissionGroup(ctx, item); err != nil {
		return nil, err
	}
	return item, nil
}

// UpdatePermissionGroup 更新权限组名称、说明与计费倍率。
func (s *Service) UpdatePermissionGroup(ctx context.Context, id uint, name, description string, rateMultiplierPercent int) (*domainchannel.PermissionGroup, error) {
	if s.permissionGroupRepo == nil {
		return nil, ErrPermissionGroupRepoUnavailable
	}
	trimmedName := strings.TrimSpace(name)
	if trimmedName == "" {
		return nil, ErrInvalidPermissionGroupName
	}
	normalizedPercent, err := normalizePermissionGroupRatePercent(rateMultiplierPercent)
	if err != nil {
		return nil, err
	}
	item, err := s.permissionGroupRepo.UpdatePermissionGroup(ctx, id, trimmedName, strings.TrimSpace(description), normalizedPercent)
	if err != nil {
		return nil, mapPermissionGroupRepoError(err)
	}
	return item, nil
}

// normalizePermissionGroupRatePercent 校验计费倍率百分比：0 表示未填按 100 处理，范围 [1, 10000]。
func normalizePermissionGroupRatePercent(value int) (int, error) {
	if value == 0 {
		return 100, nil
	}
	if value < 1 || value > 10000 {
		return 0, ErrInvalidPermissionGroupRateMultiplier
	}
	return value, nil
}

// DeletePermissionGroup 删除权限组，默认组不可删除，被套餐引用时不可删除。
func (s *Service) DeletePermissionGroup(ctx context.Context, id uint) (*domainchannel.PermissionGroupDeleteSummary, error) {
	if s.permissionGroupRepo == nil {
		return nil, ErrPermissionGroupRepoUnavailable
	}
	group, err := s.permissionGroupRepo.GetPermissionGroup(ctx, id)
	if err != nil {
		return nil, mapPermissionGroupRepoError(err)
	}
	if group.IsDefault {
		return nil, ErrDefaultPermissionGroupDeleteNotAllowed
	}
	summary, err := s.permissionGroupRepo.GetPermissionGroupDeleteSummary(ctx, id)
	if err != nil {
		return nil, mapPermissionGroupRepoError(err)
	}
	if s.permissionGroupBillingPlanReferenceChecker == nil {
		return nil, ErrPermissionGroupRepoUnavailable
	}
	planCount, err := s.permissionGroupBillingPlanReferenceChecker.CountPlansWithPermissionGroup(ctx, id)
	if err != nil {
		return nil, err
	}
	summary.PlanCount = planCount
	if planCount > 0 {
		return nil, ErrPermissionGroupReferencedByPlan
	}
	if err := s.permissionGroupRepo.DeletePermissionGroup(ctx, id); err != nil {
		return nil, mapPermissionGroupRepoError(err)
	}
	return &summary, nil
}

// ListGroupModels 返回权限组授权的平台模型 ID。
func (s *Service) ListGroupModels(ctx context.Context, groupID uint) ([]uint, []domainchannel.PermissionGroupModelRule, error) {
	if s.permissionGroupRepo == nil {
		return nil, nil, ErrPermissionGroupRepoUnavailable
	}
	if _, err := s.permissionGroupRepo.GetPermissionGroup(ctx, groupID); err != nil {
		return nil, nil, mapPermissionGroupRepoError(err)
	}
	modelIDs, err := s.permissionGroupRepo.ListGroupModelIDs(ctx, groupID)
	if err != nil {
		return nil, nil, mapPermissionGroupRepoError(err)
	}
	rules, err := s.permissionGroupRepo.ListGroupModelRules(ctx, groupID)
	if err != nil {
		return nil, nil, mapPermissionGroupRepoError(err)
	}
	return modelIDs, rules, nil
}

// SetGroupModels 全量替换权限组授权的平台模型集合与动态规则。
func (s *Service) SetGroupModels(ctx context.Context, groupID uint, modelIDs []uint, rules []domainchannel.PermissionGroupModelRule) error {
	if s.permissionGroupRepo == nil {
		return ErrPermissionGroupRepoUnavailable
	}
	if _, err := s.permissionGroupRepo.GetPermissionGroup(ctx, groupID); err != nil {
		return mapPermissionGroupRepoError(err)
	}
	if err := s.validatePermissionGroupModelIDs(ctx, modelIDs); err != nil {
		return err
	}
	normalizedRules, err := normalizePermissionGroupModelRules(rules)
	if err != nil {
		return err
	}
	if err := s.permissionGroupRepo.SetGroupModelAccess(ctx, groupID, modelIDs, normalizedRules); err != nil {
		return mapPermissionGroupRepoError(err)
	}
	return nil
}

// ListModelPermissionGroups 返回某平台模型的手动权限组、自动命中权限组与有效权限组。
func (s *Service) ListModelPermissionGroups(ctx context.Context, modelID uint) ([]uint, []uint, []uint, bool, error) {
	if s.permissionGroupRepo == nil {
		return nil, nil, nil, false, ErrPermissionGroupRepoUnavailable
	}
	if err := s.validatePermissionGroupModelIDs(ctx, []uint{modelID}); err != nil {
		return nil, nil, nil, false, err
	}
	manualGroupIDs, err := s.permissionGroupRepo.ListModelManualGroupIDs(ctx, modelID)
	if err != nil {
		return nil, nil, nil, false, mapPermissionGroupRepoError(err)
	}
	matchedGroupIDs, err := s.permissionGroupRepo.ListModelRuleGroupIDs(ctx, modelID)
	if err != nil {
		return nil, nil, nil, false, mapPermissionGroupRepoError(err)
	}
	effectiveGroupIDs, err := s.permissionGroupRepo.ListModelGroupIDs(ctx, modelID)
	if err != nil {
		return nil, nil, nil, false, mapPermissionGroupRepoError(err)
	}
	return manualGroupIDs, matchedGroupIDs, effectiveGroupIDs, len(effectiveGroupIDs) == 0, nil
}

// SetModelPermissionGroups 全量替换某平台模型的手动权限组。
func (s *Service) SetModelPermissionGroups(ctx context.Context, modelID uint, groupIDs []uint) error {
	if s.permissionGroupRepo == nil {
		return ErrPermissionGroupRepoUnavailable
	}
	if err := s.validatePermissionGroupModelIDs(ctx, []uint{modelID}); err != nil {
		return err
	}
	normalizedGroupIDs, err := s.validatePermissionGroupIDs(ctx, groupIDs)
	if err != nil {
		return err
	}
	if err := s.permissionGroupRepo.SetModelManualGroups(ctx, modelID, normalizedGroupIDs); err != nil {
		return mapPermissionGroupRepoError(err)
	}
	return nil
}

// ListGroupUsers 返回权限组内的用户 ID。
func (s *Service) ListGroupUsers(ctx context.Context, groupID uint) ([]uint, error) {
	if s.permissionGroupRepo == nil {
		return nil, ErrPermissionGroupRepoUnavailable
	}
	group, err := s.permissionGroupRepo.GetPermissionGroup(ctx, groupID)
	if err != nil {
		return nil, mapPermissionGroupRepoError(err)
	}
	if group.IsDefault {
		return nil, ErrDefaultPermissionGroupUsersImmutable
	}
	userIDs, err := s.permissionGroupRepo.ListGroupUserIDs(ctx, groupID)
	if err != nil {
		return nil, mapPermissionGroupRepoError(err)
	}
	return userIDs, nil
}

// SetGroupUsers 全量替换权限组内的用户集合。
func (s *Service) SetGroupUsers(ctx context.Context, groupID uint, userIDs []uint) error {
	if s.permissionGroupRepo == nil {
		return ErrPermissionGroupRepoUnavailable
	}
	group, err := s.permissionGroupRepo.GetPermissionGroup(ctx, groupID)
	if err != nil {
		return mapPermissionGroupRepoError(err)
	}
	if group.IsDefault {
		return ErrDefaultPermissionGroupUsersImmutable
	}
	if err := s.validatePermissionGroupUserIDs(ctx, userIDs); err != nil {
		return err
	}
	if err := s.permissionGroupRepo.SetGroupUsers(ctx, groupID, userIDs); err != nil {
		return mapPermissionGroupRepoError(err)
	}
	return nil
}

func mapPermissionGroupRepoError(err error) error {
	if errors.Is(err, repository.ErrNotFound) {
		return ErrPermissionGroupNotFound
	}
	return err
}

func (s *Service) validatePermissionGroupModelIDs(ctx context.Context, modelIDs []uint) error {
	if len(modelIDs) > 0 && s.permissionGroupModelLookup == nil {
		return ErrPermissionGroupRepoUnavailable
	}
	seen := make(map[uint]struct{}, len(modelIDs))
	for _, id := range modelIDs {
		if id == 0 {
			return ErrInvalidPermissionGroupModels
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		if _, err := s.permissionGroupModelLookup.GetModelByID(ctx, id); err != nil {
			if errors.Is(err, repository.ErrNotFound) || errors.Is(err, repository.ErrModelNotFound) {
				return ErrInvalidPermissionGroupModels
			}
			return err
		}
	}
	return nil
}

func (s *Service) validatePermissionGroupIDs(ctx context.Context, groupIDs []uint) ([]uint, error) {
	seen := make(map[uint]struct{}, len(groupIDs))
	result := make([]uint, 0, len(groupIDs))
	for _, id := range groupIDs {
		if id == 0 {
			return nil, ErrInvalidPermissionGroupModels
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		if _, err := s.permissionGroupRepo.GetPermissionGroup(ctx, id); err != nil {
			if errors.Is(err, repository.ErrNotFound) {
				return nil, ErrInvalidPermissionGroupModels
			}
			return nil, err
		}
		result = append(result, id)
	}
	return result, nil
}

func (s *Service) validatePermissionGroupUserIDs(ctx context.Context, userIDs []uint) error {
	if len(userIDs) > 0 && s.userService == nil {
		return ErrPermissionGroupRepoUnavailable
	}
	seen := make(map[uint]struct{}, len(userIDs))
	for _, id := range userIDs {
		if id == 0 {
			return ErrInvalidPermissionGroupUsers
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		if _, err := s.userService.GetByID(ctx, id); err != nil {
			if errors.Is(err, userapp.ErrUserNotFound) {
				return ErrInvalidPermissionGroupUsers
			}
			return err
		}
	}
	return nil
}

func normalizePermissionGroupModelRules(rules []domainchannel.PermissionGroupModelRule) ([]domainchannel.PermissionGroupModelRule, error) {
	results := make([]domainchannel.PermissionGroupModelRule, 0, len(rules))
	seen := make(map[string]struct{}, len(rules))
	for _, rule := range rules {
		ruleType := strings.TrimSpace(rule.RuleType)
		value := strings.TrimSpace(rule.Value)
		switch ruleType {
		case domainchannel.PermissionGroupModelRuleAll:
			value = ""
		case domainchannel.PermissionGroupModelRuleVendor,
			domainchannel.PermissionGroupModelRuleProtocol,
			domainchannel.PermissionGroupModelRuleUpstream:
			if value == "" {
				return nil, ErrInvalidPermissionGroupModels
			}
		default:
			return nil, ErrInvalidPermissionGroupModels
		}
		key := ruleType + "\x00" + value
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		results = append(results, domainchannel.PermissionGroupModelRule{
			RuleType: ruleType,
			Value:    value,
		})
	}
	return results, nil
}
