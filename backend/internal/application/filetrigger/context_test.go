package filetrigger

import (
	"context"
	"testing"

	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/llm"
)

func TestBeginSeparatesActorFromPlatformOwnerAndStableChild(t *testing.T) {
	rootCtx, err := llm.WithAuthenticatedExecutionSubject(context.Background(), 11, "")
	if err != nil {
		t.Fatal(err)
	}
	rootCtx, root, err := Begin(rootCtx, 11, 0, PurposeFileExtract)
	if err != nil {
		t.Fatal(err)
	}
	if root.TriggererUserID != 11 || root.ResourceOwnerUserID != 0 || root.Purpose != PurposeFileExtract {
		t.Fatalf("unexpected root attribution: %#v", root)
	}
	ownerCtx, ownerB, err := Begin(mustAuthenticatedContext(t, 11), 11, 42, PurposeFileExtract)
	if err != nil {
		t.Fatal(err)
	}
	if ownerB.TriggererUserID != 11 || ownerB.ResourceOwnerUserID != 42 || ownerCtx == nil {
		t.Fatalf("owner/actor split was not preserved: %#v", ownerB)
	}

	_, first, err := ChildForFile(rootCtx, "file_platform", "model@1536")
	if err != nil {
		t.Fatal(err)
	}
	_, second, err := ChildForFile(rootCtx, "file_platform", "model@1536")
	if err != nil {
		t.Fatal(err)
	}
	if first.ExecutionID == root.ExecutionID || first.ExecutionID != second.ExecutionID {
		t.Fatalf("child execution is not stable: first=%#v second=%#v root=%#v", first, second, root)
	}
	if first.ParentExecutionID != root.ExecutionID || first.TriggererUserID != 11 || first.ResourceOwnerUserID != 0 {
		t.Fatalf("child lost trusted attribution: %#v", first)
	}
}

func TestRestoreDoesNotGuessLegacyActorFromContext(t *testing.T) {
	ctx, err := llm.WithAuthenticatedExecutionSubject(context.Background(), 22, "")
	if err != nil {
		t.Fatal(err)
	}
	stored := llm.TrustedTriggerContext{
		ResourceOwnerUserID: 0,
		Purpose:             PurposeFileExtract,
		RunID:               "legacy-run",
		ExecutionID:         "legacy-execution",
	}
	_, restored, err := Restore(ctx, stored)
	if err != nil {
		t.Fatal(err)
	}
	if restored.TriggererUserID != 0 {
		t.Fatalf("legacy actor was guessed: %#v", restored)
	}
}

func mustAuthenticatedContext(t *testing.T, actorID uint) context.Context {
	t.Helper()
	ctx, err := llm.WithAuthenticatedExecutionSubject(context.Background(), actorID, "")
	if err != nil {
		t.Fatal(err)
	}
	return ctx
}
