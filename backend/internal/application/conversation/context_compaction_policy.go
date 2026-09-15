package conversation

import (
	"context"

	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/config"
)

type contextCompactionPolicy struct {
	AdminEnabled bool
	UserEnabled  bool
}

func (p contextCompactionPolicy) EffectiveEnabled() bool {
	return p.AdminEnabled && p.UserEnabled
}

func (s *Service) resolveContextCompactionPolicy(ctx context.Context, cfg config.Config, userID uint) contextCompactionPolicy {
	policy := contextCompactionPolicy{
		AdminEnabled: cfg.ContextCompactEnabled,
		UserEnabled:  true,
	}
	if val, valErr := s.getUserSettingCached(ctx, userID, "chat.context_compact_auto"); valErr == nil && val == "false" {
		policy.UserEnabled = false
	}
	return policy
}
