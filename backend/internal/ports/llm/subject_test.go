package llm

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestTrustedTriggerSurvivesOwnerAndChildContextChanges(t *testing.T) {
	ctx, err := WithAuthenticatedExecutionSubject(context.Background(), 11, "run-a")
	if err != nil {
		t.Fatal(err)
	}
	ctx = WithExecutionSubject(ctx, 22, "run-a")
	subject := ExecutionSubjectFromContext(ctx)
	if subject.TriggererUserID != 11 {
		t.Fatalf("triggerer = %d, want 11", subject.TriggererUserID)
	}
	if subject.ResourceOwnerUserID != 22 || subject.UserID != 22 {
		t.Fatalf("owner = %d/%d, want 22", subject.ResourceOwnerUserID, subject.UserID)
	}
	if subject.RunID != "run-a" {
		t.Fatalf("run = %q, want run-a", subject.RunID)
	}

	withoutCancel := context.WithoutCancel(ctx)
	child := WithExecutionSubject(withoutCancel, 33, "run-b")
	childSubject := ExecutionSubjectFromContext(child)
	if childSubject.TriggererUserID != 11 || childSubject.RunID != "run-a" {
		t.Fatalf("child changed trusted trigger: %#v", childSubject)
	}
}

func TestRestoredTrustedTriggerRejectsActorOrRunMismatch(t *testing.T) {
	ctx, err := WithAuthenticatedExecutionSubject(context.Background(), 11, "run-a")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := WithTrustedTriggerContext(ctx, TrustedTriggerContext{TriggererUserID: 12, RunID: "run-a"}); !errors.Is(err, ErrTrustedTriggerMismatch) {
		t.Fatalf("actor mismatch error = %v", err)
	}
	if _, err := WithTrustedTriggerContext(ctx, TrustedTriggerContext{TriggererUserID: 11, RunID: "run-b"}); !errors.Is(err, ErrTrustedTriggerMismatch) {
		t.Fatalf("run mismatch error = %v", err)
	}
	ctx, err = WithTrustedTriggerContext(ctx, TrustedTriggerContext{TriggererUserID: 11, RunID: "run-a", ExecutionID: "exec-a"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := WithTrustedTriggerContext(ctx, TrustedTriggerContext{TriggererUserID: 11, RunID: "run-a", ExecutionID: "exec-b"}); !errors.Is(err, ErrTrustedTriggerMismatch) {
		t.Fatalf("execution mismatch error = %v", err)
	}
}

func TestTrustedTriggerContextContainsNoSecretSerializationFields(t *testing.T) {
	raw, err := json.Marshal(TrustedTriggerContext{
		TriggererUserID:     11,
		ResourceOwnerUserID: 0,
		Purpose:             "file.extract",
		RunID:               "run-a",
		ExecutionID:         "exec-a",
	})
	if err != nil {
		t.Fatal(err)
	}
	serialized := strings.ToLower(string(raw))
	for _, forbidden := range []string{"token", "assertion", "secret", "api_key", "credential"} {
		if strings.Contains(serialized, forbidden) {
			t.Fatalf("trusted trigger serialized forbidden field %q: %s", forbidden, serialized)
		}
	}
}

func TestTrustedTriggerAllowsPlatformOwnerZero(t *testing.T) {
	ctx, err := WithAuthenticatedExecutionSubject(context.Background(), 11, "run-platform")
	if err != nil {
		t.Fatal(err)
	}
	ctx, err = WithTrustedTriggerContext(ctx, TrustedTriggerContext{
		TriggererUserID:     11,
		ResourceOwnerUserID: 0,
		Purpose:             "file.embedding",
		RunID:               "run-platform",
	})
	if err != nil {
		t.Fatal(err)
	}
	subject := ExecutionSubjectFromContext(ctx)
	if subject.TriggererUserID != 11 || subject.ResourceOwnerUserID != 0 {
		t.Fatalf("subject = %#v, want actor 11 and platform owner 0", subject)
	}
}

func TestTrustedExecutionChildPreservesActorRunAndLinksParent(t *testing.T) {
	ctx, err := WithAuthenticatedExecutionSubject(context.Background(), 11, "run-a")
	if err != nil {
		t.Fatal(err)
	}
	ctx, err = WithTrustedTriggerContext(ctx, TrustedTriggerContext{
		TriggererUserID:     11,
		ResourceOwnerUserID: 22,
		Purpose:             "chat.main",
		RunID:               "run-a",
		ExecutionID:         "exec-root",
	})
	if err != nil {
		t.Fatal(err)
	}
	child, err := WithTrustedExecutionChild(ctx, "exec-tool")
	if err != nil {
		t.Fatal(err)
	}
	subject := ExecutionSubjectFromContext(child)
	if subject.TriggererUserID != 11 || subject.ResourceOwnerUserID != 22 || subject.Purpose != "chat.main" || subject.RunID != "run-a" {
		t.Fatalf("child changed trusted lineage: %#v", subject)
	}
	if subject.ExecutionID != "exec-tool" || subject.ParentExecutionID != "exec-root" {
		t.Fatalf("child execution lineage = %#v", subject)
	}
	if _, err := WithTrustedExecutionChild(ctx, "exec-root"); !errors.Is(err, ErrTrustedExecutionChildInvalid) {
		t.Fatalf("same execution child error = %v", err)
	}
	if _, err := WithTrustedTriggerContext(ctx, TrustedTriggerContext{TriggererUserID: 11, RunID: "run-a", ExecutionID: "exec-tool"}); !errors.Is(err, ErrTrustedTriggerMismatch) {
		t.Fatalf("strict restore error = %v", err)
	}
}

func TestAuthenticatedRootRejectsStaleDifferentActor(t *testing.T) {
	ctx, err := WithAuthenticatedExecutionSubject(context.Background(), 11, "run-a")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := WithAuthenticatedExecutionSubject(ctx, 12, ""); !errors.Is(err, ErrTrustedTriggerMismatch) {
		t.Fatalf("stale actor error = %v", err)
	}
}
