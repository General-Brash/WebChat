package conversation

import (
	"context"
	"strings"
	"time"

	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/llm"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/shared/background"
	"go.uber.org/zap"
)

type openAIResponsesBackgroundRecoveryState struct {
	Enabled        bool
	ResponseID     string
	ObservedUsage  llm.Usage
	TriggerContext llm.TrustedTriggerContext
}

func trustedResponsesRecoveryContext(parent context.Context, trigger llm.TrustedTriggerContext) (context.Context, bool) {
	if parent == nil {
		parent = context.Background()
	}
	subject := llm.ExecutionSubjectFromContext(parent)
	if subject.HasTriggerer() {
		if trigger.TriggererUserID == 0 || subject.TriggererUserID != trigger.TriggererUserID {
			return parent, false
		}
		if trigger.RunID != "" && subject.RunID != "" && trigger.RunID != subject.RunID {
			return parent, false
		}
		if trigger.ExecutionID != "" && subject.ExecutionID != "" && trigger.ExecutionID != subject.ExecutionID {
			return parent, false
		}
		return parent, true
	}
	if !trigger.HasTriggerer() {
		return parent, false
	}
	bound, err := llm.WithTrustedTriggerContext(parent, trigger)
	if err != nil {
		return parent, false
	}
	return bound, true
}

func (s *Service) recoverOpenAIResponsesBackgroundUsage(parent context.Context, route llm.RouteConfig, state openAIResponsesBackgroundRecoveryState) (llm.Usage, bool) {
	responseID := strings.TrimSpace(state.ResponseID)
	if s == nil || s.llmClient == nil || !state.Enabled || responseID == "" {
		return llm.Usage{}, false
	}

	recoveryCtx, ok := trustedResponsesRecoveryContext(parent, state.TriggerContext)
	if !ok {
		return llm.Usage{}, false
	}
	ctx, cancel := background.WithTimeout(recoveryCtx, 8*time.Second)
	defer cancel()

	var cancelErr error
	if output, err := s.llmClient.CancelOpenAIResponse(ctx, route, responseID); err == nil {
		if output != nil && output.Usage != (llm.Usage{}) {
			return output.Usage, true
		}
	} else {
		cancelErr = err
	}

	var retrieveErr error
	for attempt := 0; attempt < 4; attempt++ {
		select {
		case <-ctx.Done():
			return llm.Usage{}, false
		case <-time.After(600 * time.Millisecond):
		}
		output, err := s.llmClient.RetrieveOpenAIResponse(ctx, route, responseID)
		if err != nil {
			retrieveErr = err
			continue
		}
		if output != nil && output.Usage != (llm.Usage{}) {
			return output.Usage, true
		}
	}
	if s.logger != nil {
		s.logger.Warn("openai_responses_background_usage_recovery_failed",
			zap.String("response_id", responseID),
			zap.String("protocol", route.Protocol),
			zap.NamedError("cancel_error", cancelErr),
			zap.NamedError("retrieve_error", retrieveErr),
		)
	}

	return llm.Usage{}, false
}
