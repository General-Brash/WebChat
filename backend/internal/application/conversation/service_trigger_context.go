package conversation

import (
	"context"
	"strconv"
	"strings"
	"time"

	model "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/conversation"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/llm"
	"github.com/google/uuid"
)

const (
	trustedPurposeChatMain   = "chat.main"
	trustedPurposeMediaImage = "media.image"
	trustedPurposeMediaVideo = "media.video"
	trustedPurposeMCP        = "mcp.external"
)

// establishTrustedOperation binds the verified HTTP actor to one server-owned
// operation. UserID is deliberately supplied as the resource owner only; the
// payer always comes from the already-authenticated context subject.
func establishTrustedOperation(
	ctx context.Context,
	provided *llm.TrustedTriggerContext,
	resourceOwnerUserID uint,
	purpose string,
	runID string,
) (context.Context, llm.TrustedTriggerContext, error) {
	if ctx == nil {
		return ctx, llm.TrustedTriggerContext{}, llm.ErrTrustedTriggerRequired
	}
	subject := llm.ExecutionSubjectFromContext(ctx)
	if !subject.HasTriggerer() {
		return ctx, llm.TrustedTriggerContext{}, llm.ErrTrustedTriggerRequired
	}
	trigger := subject.TrustedTriggerContext
	if provided != nil {
		if provided.TriggererUserID != 0 && provided.TriggererUserID != subject.TriggererUserID {
			return ctx, llm.TrustedTriggerContext{}, llm.ErrTrustedTriggerMismatch
		}
		if subject.RunID != "" && strings.TrimSpace(provided.RunID) != "" && subject.RunID != strings.TrimSpace(provided.RunID) {
			return ctx, llm.TrustedTriggerContext{}, llm.ErrTrustedTriggerMismatch
		}
		trigger = *provided
	}
	trigger.TriggererUserID = subject.TriggererUserID
	if strings.TrimSpace(runID) == "" {
		runID = strings.TrimSpace(trigger.RunID)
	}
	if strings.TrimSpace(runID) == "" {
		runID = strings.TrimSpace(subject.RunID)
	}
	if subject.RunID != "" && runID != "" && subject.RunID != runID {
		return ctx, llm.TrustedTriggerContext{}, llm.ErrTrustedTriggerMismatch
	}
	if trigger.RunID == "" {
		trigger.RunID = runID
	}
	if strings.TrimSpace(purpose) != "" {
		if trigger.Purpose != "" && trigger.Purpose != purpose && subject.ExecutionID != "" {
			return ctx, llm.TrustedTriggerContext{}, llm.ErrTrustedTriggerMismatch
		}
		trigger.Purpose = purpose
	}
	if provided == nil {
		trigger.ResourceOwnerUserID = resourceOwnerUserID
	} else if trigger.ResourceOwnerUserID != resourceOwnerUserID {
		return ctx, llm.TrustedTriggerContext{}, llm.ErrTrustedTriggerMismatch
	}
	if subject.ExecutionID != "" && subject.ResourceOwnerUserID != trigger.ResourceOwnerUserID {
		return ctx, llm.TrustedTriggerContext{}, llm.ErrTrustedTriggerMismatch
	}
	if trigger.CreatedAt.IsZero() {
		trigger.CreatedAt = subject.CreatedAt
	}
	if trigger.CreatedAt.IsZero() {
		trigger.CreatedAt = time.Now().UTC()
	}
	bound, err := llm.WithTrustedTriggerContext(ctx, trigger)
	if err != nil {
		return ctx, llm.TrustedTriggerContext{}, err
	}
	return bound, llm.ExecutionSubjectFromContext(bound).TrustedTriggerContext, nil
}

// prepareTrustedProviderRoot allocates the one provider-root execution before
// any durable Run or generation-stream lease is created. The root is a
// server-owned provider identity; lease ownership has a separate token in the
// generation-stream registry and must never replace this value.
func prepareTrustedProviderRoot(
	ctx context.Context,
	trigger llm.TrustedTriggerContext,
	resourceOwnerUserID uint,
	purpose string,
	runID string,
) (context.Context, llm.TrustedTriggerContext, error) {
	if strings.TrimSpace(trigger.ExecutionID) == "" {
		trigger.ExecutionID = uuid.NewString()
	}
	input := llm.GenerateInput{
		RunID:         runID,
		ExecutionID:   trigger.ExecutionID,
		TriggerContext: &trigger,
	}
	return prepareTrustedProviderExecution(ctx, &input, resourceOwnerUserID, purpose, runID)
}

// prepareTrustedProviderExecution initializes the first execution on a trusted
// root and explicitly forks every later real model execution. A stored
// TriggerContext is restored only when the parent has no execution yet; a new
// child is never smuggled through the strict replay API.
func prepareTrustedProviderExecution(
	ctx context.Context,
	input *llm.GenerateInput,
	resourceOwnerUserID uint,
	purpose string,
	runID string,
) (context.Context, llm.TrustedTriggerContext, error) {
	if ctx == nil || input == nil {
		return ctx, llm.TrustedTriggerContext{}, llm.ErrTrustedTriggerRequired
	}
	subject := llm.ExecutionSubjectFromContext(ctx)
	if !subject.HasTriggerer() {
		return ctx, llm.TrustedTriggerContext{}, llm.ErrTrustedTriggerRequired
	}
	if input.TriggerContext != nil {
		if input.TriggerContext.TriggererUserID != 0 && input.TriggerContext.TriggererUserID != subject.TriggererUserID {
			return ctx, llm.TrustedTriggerContext{}, llm.ErrTrustedTriggerMismatch
		}
		if subject.RunID != "" && input.TriggerContext.RunID != "" && subject.RunID != input.TriggerContext.RunID {
			return ctx, llm.TrustedTriggerContext{}, llm.ErrTrustedTriggerMismatch
		}
		if subject.ExecutionID == "" {
			var err error
			ctx, err = llm.WithTrustedTriggerContext(ctx, *input.TriggerContext)
			if err != nil {
				return ctx, llm.TrustedTriggerContext{}, err
			}
			subject = llm.ExecutionSubjectFromContext(ctx)
		}
	}
	if strings.TrimSpace(runID) == "" {
		runID = strings.TrimSpace(subject.RunID)
	}
	requestedRunID := strings.TrimSpace(input.RunID)
	if requestedRunID != "" {
		if subject.RunID != "" && subject.RunID != requestedRunID {
			return ctx, llm.TrustedTriggerContext{}, llm.ErrTrustedTriggerMismatch
		}
		runID = requestedRunID
	}
	if subject.RunID != "" && runID != "" && subject.RunID != runID {
		return ctx, llm.TrustedTriggerContext{}, llm.ErrTrustedTriggerMismatch
	}
	if subject.ExecutionID != "" && subject.ResourceOwnerUserID != resourceOwnerUserID {
		return ctx, llm.TrustedTriggerContext{}, llm.ErrTrustedTriggerMismatch
	}
	if subject.ExecutionID != "" && strings.TrimSpace(purpose) != "" && subject.Purpose != "" && subject.Purpose != strings.TrimSpace(purpose) {
		return ctx, llm.TrustedTriggerContext{}, llm.ErrTrustedTriggerMismatch
	}
	executionID := strings.TrimSpace(input.ExecutionID)
	if executionID == "" {
		executionID = strings.TrimSpace(subject.ExecutionID)
	}
	if executionID == "" {
		executionID = uuid.NewString()
	}
	if subject.ExecutionID == "" {
		trigger := subject.TrustedTriggerContext
		trigger.TriggererUserID = subject.TriggererUserID
		trigger.ResourceOwnerUserID = resourceOwnerUserID
		trigger.Purpose = strings.TrimSpace(purpose)
		trigger.RunID = runID
		trigger.ExecutionID = executionID
		if trigger.CreatedAt.IsZero() {
			trigger.CreatedAt = subject.CreatedAt
		}
		if trigger.CreatedAt.IsZero() {
			trigger.CreatedAt = time.Now().UTC()
		}
		var err error
		ctx, err = llm.WithTrustedTriggerContext(ctx, trigger)
		if err != nil {
			return ctx, llm.TrustedTriggerContext{}, err
		}
	} else if subject.ExecutionID != executionID {
		var err error
		ctx, err = llm.WithTrustedExecutionChild(ctx, executionID)
		if err != nil {
			return ctx, llm.TrustedTriggerContext{}, err
		}
	}
	trusted := llm.ExecutionSubjectFromContext(ctx).TrustedTriggerContext
	input.RunID = trusted.RunID
	input.ExecutionID = trusted.ExecutionID
	copy := trusted
	input.TriggerContext = &copy
	return ctx, trusted, nil
}

// trustedMCPChildContext creates one stable, server-approved child for one
// logical tool call. Internal MCP retries keep the same child and therefore
// cannot create a second fee or switch the payer.
func trustedMCPChildContext(
	ctx context.Context,
	runID string,
	toolCallID string,
	serverID uint,
	toolName string,
	argumentsJSON string,
) (context.Context, llm.TrustedTriggerContext, error) {
	if ctx == nil {
		return ctx, llm.TrustedTriggerContext{}, llm.ErrTrustedTriggerRequired
	}
	subject := llm.ExecutionSubjectFromContext(ctx)
	if !subject.HasTriggerer() {
		return ctx, llm.TrustedTriggerContext{}, llm.ErrTrustedTriggerRequired
	}
	executionID := stableTrustedExecutionID(
		trustedPurposeMCP,
		runID,
		toolCallID,
		strconv.FormatUint(uint64(serverID), 10),
		toolName,
		canonicalToolArguments(argumentsJSON),
	)
	if subject.ExecutionID == "" {
		input := llm.GenerateInput{RunID: runID, ExecutionID: executionID}
		return prepareTrustedProviderExecution(ctx, &input, subject.ResourceOwnerUserID, trustedPurposeMCP, runID)
	}
	child, err := llm.WithTrustedExecutionChild(ctx, executionID)
	if err != nil {
		return ctx, llm.TrustedTriggerContext{}, err
	}
	return child, llm.ExecutionSubjectFromContext(child).TrustedTriggerContext, nil
}

func stableTrustedExecutionID(parts ...string) string {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(strings.Join(parts, "\x00"))).String()
}

func trustedTriggerContextPointer(ctx context.Context) *llm.TrustedTriggerContext {
	if ctx == nil {
		return nil
	}
	trigger := llm.ExecutionSubjectFromContext(ctx).TrustedTriggerContext
	if trigger.TriggererUserID == 0 {
		return nil
	}
	return &trigger
}

func canonicalTriggererUserID(ctx context.Context, provided *llm.TrustedTriggerContext) (uint, error) {
	if ctx == nil {
		return 0, llm.ErrTrustedTriggerRequired
	}
	subject := llm.ExecutionSubjectFromContext(ctx)
	if !subject.HasTriggerer() {
		return 0, llm.ErrTrustedTriggerRequired
	}
	if provided != nil && provided.TriggererUserID != 0 && provided.TriggererUserID != subject.TriggererUserID {
		return 0, llm.ErrTrustedTriggerMismatch
	}
	return subject.TriggererUserID, nil
}

func trustedTriggerCreatedAtPointer(trigger llm.TrustedTriggerContext) *time.Time {
	if trigger.CreatedAt.IsZero() {
		return nil
	}
	createdAt := trigger.CreatedAt
	return &createdAt
}

func applyTrustedTriggerToRun(run *model.Run, trigger llm.TrustedTriggerContext) {
	if run == nil {
		return
	}
	run.TriggererUserID = trigger.TriggererUserID
	run.ResourceOwnerUserID = trigger.ResourceOwnerUserID
	run.Purpose = strings.TrimSpace(trigger.Purpose)
	run.ExecutionID = strings.TrimSpace(trigger.ExecutionID)
	run.ParentExecutionID = strings.TrimSpace(trigger.ParentExecutionID)
	if !trigger.CreatedAt.IsZero() {
		createdAt := trigger.CreatedAt
		run.TriggerCreatedAt = &createdAt
	}
}
