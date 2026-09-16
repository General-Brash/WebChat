// Package filetrigger contains the small server-side attribution helpers used
// by the file upload, extraction, processing, and embedding producers.
package filetrigger

import (
	"context"
	"errors"
	"strings"
	"time"

	domainconversation "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/conversation"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/llm"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/repository"
	"github.com/google/uuid"
)

const (
	// PurposeFileExtract identifies the logical operation that owns file
	// extraction/OCR work. The resource owner is carried separately.
	PurposeFileExtract = "file.extract"
	// PurposeFileEmbedding identifies an explicitly submitted file embedding
	// operation. Its per-file children retain the same purpose.
	PurposeFileEmbedding = "file.embedding"
)

var ErrInvalidPurpose = errors.New("invalid trusted file operation purpose")

// Begin starts a new authenticated file operation. actorID is only a
// cross-check against the already verified context subject; it is never a
// payer source by itself. A new root receives stable server-created logical
// and execution references before any file queue or provider fork.
func Begin(ctx context.Context, actorID uint, resourceOwnerUserID uint, purpose string) (context.Context, llm.TrustedTriggerContext, error) {
	if ctx == nil {
		return ctx, llm.TrustedTriggerContext{}, llm.ErrTrustedTriggerRequired
	}
	purpose = normalizePurpose(purpose)
	if !validPurpose(purpose) {
		return ctx, llm.TrustedTriggerContext{}, ErrInvalidPurpose
	}

	subject := llm.ExecutionSubjectFromContext(ctx)
	if actorID == 0 {
		actorID = subject.TriggererUserID
	}
	var err error
	ctx, err = llm.WithAuthenticatedExecutionSubject(ctx, actorID, "")
	if err != nil {
		return ctx, llm.TrustedTriggerContext{}, err
	}
	subject = llm.ExecutionSubjectFromContext(ctx)
	if !subject.HasTriggerer() {
		return ctx, llm.TrustedTriggerContext{}, llm.ErrTrustedTriggerRequired
	}
	// A context with an execution already belongs to an existing operation.
	// Starting a second root on it would either overwrite attribution or make
	// an accepted provider execution look like a new task.
	if strings.TrimSpace(subject.ExecutionID) != "" {
		return ctx, llm.TrustedTriggerContext{}, llm.ErrTrustedTriggerMismatch
	}

	now := time.Now().UTC()
	trigger := subject.TrustedTriggerContext
	trigger.TriggererUserID = subject.TriggererUserID
	trigger.ResourceOwnerUserID = resourceOwnerUserID
	trigger.Purpose = purpose
	trigger.RunID = uuid.NewString()
	trigger.ExecutionID = uuid.NewString()
	trigger.ParentExecutionID = ""
	trigger.CreatedAt = now
	bound, err := llm.WithTrustedTriggerContext(ctx, trigger)
	if err != nil {
		return ctx, llm.TrustedTriggerContext{}, err
	}
	return bound, llm.ExecutionSubjectFromContext(bound).TrustedTriggerContext, nil
}

// Ensure returns an existing trusted root or starts one for an authenticated
// server context. It never derives an actor from an owner or request input.
func Ensure(ctx context.Context, resourceOwnerUserID uint, purpose string) (context.Context, llm.TrustedTriggerContext, error) {
	subject := llm.ExecutionSubjectFromContext(ctx)
	if subject.ExecutionID != "" {
		trigger := repository.NormalizeTrustedTriggerContext(subject.TrustedTriggerContext)
		if trigger.ResourceOwnerUserID != resourceOwnerUserID || trigger.Purpose != normalizePurpose(purpose) {
			return ctx, llm.TrustedTriggerContext{}, llm.ErrTrustedTriggerMismatch
		}
		return ctx, trigger, nil
	}
	return Begin(ctx, subject.TriggererUserID, resourceOwnerUserID, purpose)
}

// Restore binds a durable operation envelope without creating a new actor,
// run, or execution reference. A missing envelope remains missing so callers
// can keep deterministic local work available while blocking paid work.
func Restore(ctx context.Context, trigger llm.TrustedTriggerContext) (context.Context, llm.TrustedTriggerContext, error) {
	if ctx == nil {
		return ctx, trigger, llm.ErrTrustedTriggerRequired
	}
	trigger = repository.NormalizeTrustedTriggerContext(trigger)
	if !repository.HasTrustedTriggerMetadata(trigger) {
		return ctx, trigger, nil
	}
	bound, err := llm.WithTrustedTriggerContext(ctx, trigger)
	if err != nil {
		return ctx, llm.TrustedTriggerContext{}, err
	}
	return bound, trigger, nil
}

// RestoreFileObject restores the operation that created the current durable
// file-processing state. hasStored distinguishes a legacy empty envelope from
// a current operation and prevents a browser/worker actor from backfilling it.
func RestoreFileObject(ctx context.Context, file domainconversation.FileObject) (context.Context, llm.TrustedTriggerContext, bool, error) {
	trigger := ContextFromFileObject(file)
	if !repository.HasTrustedTriggerMetadata(trigger) {
		return ctx, trigger, false, nil
	}
	bound, restored, err := Restore(ctx, trigger)
	return bound, restored, true, err
}

// ContextFromFileObject maps Q2's current processing metadata into the
// canonical trusted envelope without treating FileObject.UserID as a payer.
func ContextFromFileObject(file domainconversation.FileObject) llm.TrustedTriggerContext {
	var createdAt time.Time
	if file.ProcessingTriggerCreatedAt != nil {
		createdAt = *file.ProcessingTriggerCreatedAt
	}
	return repository.NormalizeTrustedTriggerContext(llm.TrustedTriggerContext{
		TriggererUserID:     file.ProcessingTriggererUserID,
		ResourceOwnerUserID: file.ProcessingResourceOwnerUserID,
		Purpose:             file.ProcessingPurpose,
		RunID:               file.ProcessingRunID,
		ExecutionID:         file.ProcessingExecutionID,
		ParentExecutionID:   file.ProcessingParentExecutionID,
		CreatedAt:           createdAt,
	})
}

// ApplyToFileObject writes only explicit trusted metadata. Legacy/local state
// updates therefore remain unable to erase an already persisted envelope.
func ApplyToFileObject(file *domainconversation.FileObject, trigger llm.TrustedTriggerContext) {
	if file == nil {
		return
	}
	trigger = repository.NormalizeTrustedTriggerContext(trigger)
	if !repository.HasTrustedTriggerMetadata(trigger) {
		return
	}
	file.ProcessingTriggererUserID = trigger.TriggererUserID
	file.ProcessingResourceOwnerUserID = trigger.ResourceOwnerUserID
	file.ProcessingPurpose = trigger.Purpose
	file.ProcessingRunID = trigger.RunID
	file.ProcessingExecutionID = trigger.ExecutionID
	file.ProcessingParentExecutionID = trigger.ParentExecutionID
	if trigger.CreatedAt.IsZero() {
		file.ProcessingTriggerCreatedAt = nil
	} else {
		createdAt := trigger.CreatedAt
		file.ProcessingTriggerCreatedAt = &createdAt
	}
}

// ApplyToProcessingState adds the complete immutable envelope to a state DTO.
func ApplyToProcessingState(state *domainconversation.FileObjectProcessing, trigger llm.TrustedTriggerContext) *domainconversation.FileObjectProcessing {
	if state == nil {
		return nil
	}
	trigger = repository.NormalizeTrustedTriggerContext(trigger)
	if !repository.HasTrustedTriggerMetadata(trigger) {
		return state
	}
	state.TriggererUserID = trigger.TriggererUserID
	state.ResourceOwnerUserID = trigger.ResourceOwnerUserID
	state.Purpose = trigger.Purpose
	state.RunID = trigger.RunID
	state.ExecutionID = trigger.ExecutionID
	state.ParentExecutionID = trigger.ParentExecutionID
	if trigger.CreatedAt.IsZero() {
		state.TriggerCreatedAt = nil
	} else {
		createdAt := trigger.CreatedAt
		state.TriggerCreatedAt = &createdAt
	}
	return state
}

// Pointer makes a server-owned copy for an application input. A zero envelope
// is intentionally representable: extraction/embedding can quarantine paid
// provider work while still allowing their deterministic local stages.
func Pointer(trigger llm.TrustedTriggerContext) *llm.TrustedTriggerContext {
	trigger = repository.NormalizeTrustedTriggerContext(trigger)
	return &trigger
}

// ChildForFile creates a stable per-file child execution. Queue retries reuse
// the stored child; a delivery never receives a fresh random provider ID.
func ChildForFile(ctx context.Context, fileID string, embeddingSignature string) (context.Context, llm.TrustedTriggerContext, error) {
	if ctx == nil {
		return ctx, llm.TrustedTriggerContext{}, llm.ErrTrustedTriggerRequired
	}
	subject := llm.ExecutionSubjectFromContext(ctx)
	if !subject.HasTriggerer() {
		return ctx, llm.TrustedTriggerContext{}, llm.ErrTrustedTriggerRequired
	}
	if strings.TrimSpace(subject.ExecutionID) == "" || strings.TrimSpace(subject.RunID) == "" {
		return ctx, llm.TrustedTriggerContext{}, llm.ErrTrustedExecutionChildInvalid
	}
	childID := uuid.NewSHA1(uuid.NameSpaceOID, []byte(strings.Join([]string{
		"deeix-chat:file-embedding",
		subject.RunID,
		subject.ExecutionID,
		strings.TrimSpace(fileID),
		strings.TrimSpace(embeddingSignature),
	}, "\x00"))).String()
	childCtx, err := llm.WithTrustedExecutionChild(ctx, childID)
	if err != nil {
		return ctx, llm.TrustedTriggerContext{}, err
	}
	return childCtx, llm.ExecutionSubjectFromContext(childCtx).TrustedTriggerContext, nil
}

func normalizePurpose(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func validPurpose(value string) bool {
	return value == PurposeFileExtract || value == PurposeFileEmbedding
}
