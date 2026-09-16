package llm

import (
	"context"
	"errors"
	"strings"
	"time"
)

var (
	// ErrTrustedTriggerRequired is returned before a paid provider dispatch when
	// no authenticated or explicitly restored triggerer is present.
	ErrTrustedTriggerRequired = errors.New("trusted triggerer is required for paid provider execution")
	// ErrTrustedTriggerMismatch prevents a child operation from changing the
	// payer of an already established logical execution.
	ErrTrustedTriggerMismatch = errors.New("trusted trigger context is immutable")
	// ErrTrustedExecutionChildInvalid is returned when a producer attempts to
	// fork without a genuinely new server-approved execution reference.
	ErrTrustedExecutionChildInvalid = errors.New("trusted execution child requires a new execution id")
)

type executionSubjectKey struct{}

// TrustedTriggerContext is the server-owned attribution envelope for one
// logical operation. TriggererUserID is the immutable payer selector; it is
// never inferred from resource ownership, route data, request JSON, tool
// arguments, or MCP _meta. ResourceOwnerUserID is an access/storage owner and
// may legitimately be zero for a platform resource.
//
// The envelope intentionally contains stable references only. It has no token,
// assertion, API key, or provider credential field.
type TrustedTriggerContext struct {
	TriggererUserID     uint      `json:"triggerer_user_id"`
	ResourceOwnerUserID uint      `json:"resource_owner_user_id"`
	Purpose             string    `json:"purpose"`
	RunID               string    `json:"run_id"`
	ExecutionID         string    `json:"execution_id"`
	ParentExecutionID   string    `json:"parent_execution_id,omitempty"`
	CreatedAt           time.Time `json:"created_at"`
}

// HasTriggerer reports whether this envelope can authorize paid provider work.
func (t TrustedTriggerContext) HasTriggerer() bool {
	return t.TriggererUserID > 0
}

// ExecutionSubject carries the legacy access/resource owner alongside the one
// trusted trigger dimension. UserID is retained for compatibility with old
// callers and rows; it means resource owner/access user here, never payer.
// New paid Sub2 records use TriggererUserID as their authority scope and keep
// ResourceOwnerUserID separately.
type ExecutionSubject struct {
	UserID uint `json:"user_id"`
	TrustedTriggerContext
}

// WithAuthenticatedExecutionSubject captures an actor only after the HTTP
// authentication middleware has verified the access claims and session. A
// verified root must not silently inherit a different actor already present in
// the request context. An established actor is never replaced by a later child
// context.
func WithAuthenticatedExecutionSubject(ctx context.Context, actorID uint, runID string) (context.Context, error) {
	subject := ExecutionSubjectFromContext(ctx)
	if actorID == 0 {
		return ctx, ErrTrustedTriggerRequired
	}
	if subject.TriggererUserID > 0 && subject.TriggererUserID != actorID {
		return ctx, ErrTrustedTriggerMismatch
	}
	if subject.RunID != "" && strings.TrimSpace(runID) != "" && subject.RunID != strings.TrimSpace(runID) {
		return ctx, ErrTrustedTriggerMismatch
	}
	if subject.TriggererUserID == 0 {
		subject.TriggererUserID = actorID
	}
	if subject.UserID == 0 && subject.ResourceOwnerUserID == 0 {
		subject.UserID = actorID
		subject.ResourceOwnerUserID = actorID
	}
	if subject.RunID == "" && strings.TrimSpace(runID) != "" {
		subject.RunID = strings.TrimSpace(runID)
	}
	if subject.CreatedAt.IsZero() {
		subject.CreatedAt = time.Now().UTC()
	}
	return context.WithValue(ctx, executionSubjectKey{}, subject), nil
}

// WithTrustedTriggerContext restores server-owned operation metadata, such as
// a durable queue/recovery envelope. It accepts an explicit resource owner of
// zero for platform work, but rejects actor or logical-run changes once one is
// established in the parent context.
func WithTrustedTriggerContext(ctx context.Context, trigger TrustedTriggerContext) (context.Context, error) {
	subject := ExecutionSubjectFromContext(ctx)
	if subject.TriggererUserID > 0 && trigger.TriggererUserID > 0 && subject.TriggererUserID != trigger.TriggererUserID {
		return ctx, ErrTrustedTriggerMismatch
	}
	if subject.RunID != "" && trigger.RunID != "" && subject.RunID != trigger.RunID {
		return ctx, ErrTrustedTriggerMismatch
	}
	if subject.ExecutionID != "" && trigger.ExecutionID != "" && subject.ExecutionID != trigger.ExecutionID {
		return ctx, ErrTrustedTriggerMismatch
	}
	if trigger == (TrustedTriggerContext{}) {
		return ctx, nil
	}
	if trigger.TriggererUserID > 0 {
		subject.TriggererUserID = trigger.TriggererUserID
	}
	subject.ResourceOwnerUserID = trigger.ResourceOwnerUserID
	subject.UserID = trigger.ResourceOwnerUserID
	if trigger.Purpose != "" {
		subject.Purpose = trigger.Purpose
	}
	if trigger.RunID != "" {
		subject.RunID = trigger.RunID
	}
	if trigger.ExecutionID != "" {
		subject.ExecutionID = trigger.ExecutionID
	}
	if trigger.ParentExecutionID != "" {
		subject.ParentExecutionID = trigger.ParentExecutionID
	}
	if !trigger.CreatedAt.IsZero() {
		subject.CreatedAt = trigger.CreatedAt
	} else if subject.CreatedAt.IsZero() && subject.TriggererUserID > 0 {
		subject.CreatedAt = time.Now().UTC()
	}
	return context.WithValue(ctx, executionSubjectKey{}, subject), nil
}

// WithTrustedExecutionChild creates one explicitly approved child execution
// without changing the authenticated actor, resource owner, purpose, or
// logical run. The current execution becomes the trusted parent. Producers
// must obtain executionID from their server-side execution allocator and call
// this with the existing context; this API never accepts actor, owner, run, or
// browser metadata and never uses context.Background.
//
// Restoring a durable/replayed child remains the job of
// WithTrustedTriggerContext, which intentionally rejects a different
// ExecutionID. This fork API is only for a new forward execution.
func WithTrustedExecutionChild(ctx context.Context, executionID string) (context.Context, error) {
	subject := ExecutionSubjectFromContext(ctx)
	if !subject.HasTriggerer() {
		return ctx, ErrTrustedTriggerRequired
	}
	parentExecutionID := strings.TrimSpace(subject.ExecutionID)
	childExecutionID := strings.TrimSpace(executionID)
	if parentExecutionID == "" || childExecutionID == "" || childExecutionID == parentExecutionID {
		return ctx, ErrTrustedExecutionChildInvalid
	}
	child := subject
	child.ExecutionID = childExecutionID
	child.ParentExecutionID = parentExecutionID
	return context.WithValue(ctx, executionSubjectKey{}, child), nil
}

// WithExecutionSubject is the legacy owner/run setter. It deliberately cannot
// establish or replace a payer. Existing producers may use it to set the
// resource owner and first logical run; producer-specific trigger capture must
// use WithAuthenticatedExecutionSubject or restore trusted server metadata.
func WithExecutionSubject(ctx context.Context, userID uint, runID string) context.Context {
	subject := ExecutionSubjectFromContext(ctx)
	subject.UserID = userID
	subject.ResourceOwnerUserID = userID
	if subject.RunID == "" && runID != "" {
		subject.RunID = runID
	}
	if subject.CreatedAt.IsZero() && subject.TriggererUserID > 0 {
		subject.CreatedAt = time.Now().UTC()
	}
	return context.WithValue(ctx, executionSubjectKey{}, subject)
}

// ExecutionSubjectFromContext returns a value copy so callers cannot mutate
// the trusted context stored in a parent context.
func ExecutionSubjectFromContext(ctx context.Context) ExecutionSubject {
	if ctx == nil {
		return ExecutionSubject{}
	}
	subject, _ := ctx.Value(executionSubjectKey{}).(ExecutionSubject)
	return subject
}
