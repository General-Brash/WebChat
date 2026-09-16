package conversation

import (
	"context"
	"testing"

	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/llm"
)

// SOURCE-ONLY: this contract case is intentionally not executed in the C1
// static-only boundary.
func TestTrustedOperationSeparatesTriggererFromResourceOwner(t *testing.T) {
	authenticated, err := llm.WithAuthenticatedExecutionSubject(context.TODO(), 101, "run-chat")
	if err != nil {
		t.Fatal(err)
	}
	ctx, trigger, err := establishTrustedOperation(authenticated, nil, 202, trustedPurposeChatMain, "run-chat")
	if err != nil {
		t.Fatal(err)
	}
	if trigger.TriggererUserID != 101 || trigger.ResourceOwnerUserID != 202 {
		t.Fatalf("triggerer/owner collapsed: %+v", trigger)
	}
	if got := llm.ExecutionSubjectFromContext(ctx); got.TriggererUserID != 101 || got.ResourceOwnerUserID != 202 {
		t.Fatalf("trusted context lost actor/owner separation: %+v", got)
	}
}

// SOURCE-ONLY: the first approved execution initializes the root; the next
// model/MCP/media execution must be a child carrying the original actor.
func TestTrustedProviderExecutionUsesRootThenChild(t *testing.T) {
	authenticated, err := llm.WithAuthenticatedExecutionSubject(context.TODO(), 101, "run-chat")
	if err != nil {
		t.Fatal(err)
	}
	operation, _, err := establishTrustedOperation(authenticated, nil, 202, trustedPurposeChatMain, "run-chat")
	if err != nil {
		t.Fatal(err)
	}
	first := llm.GenerateInput{RunID: "run-chat", ExecutionID: "exec-root"}
	root, rootTrigger, err := prepareTrustedProviderExecution(operation, &first, 202, trustedPurposeChatMain, "run-chat")
	if err != nil {
		t.Fatal(err)
	}
	second := llm.GenerateInput{RunID: "run-chat", ExecutionID: "exec-child"}
	child, childTrigger, err := prepareTrustedProviderExecution(root, &second, 202, trustedPurposeChatMain, "run-chat")
	if err != nil {
		t.Fatal(err)
	}
	if rootTrigger.TriggererUserID != 101 || childTrigger.TriggererUserID != 101 || childTrigger.ParentExecutionID != rootTrigger.ExecutionID {
		t.Fatalf("root/child attribution invalid: root=%+v child=%+v", rootTrigger, childTrigger)
	}
	if llm.ExecutionSubjectFromContext(child).ResourceOwnerUserID != 202 {
		t.Fatal("child changed resource owner")
	}
}

// SOURCE-ONLY: a caller-supplied UID cannot replace the verified HTTP actor.
func TestTrustedOperationRejectsCallerUIDSpoof(t *testing.T) {
	authenticated, err := llm.WithAuthenticatedExecutionSubject(context.TODO(), 101, "run-chat")
	if err != nil {
		t.Fatal(err)
	}
	spoof := &llm.TrustedTriggerContext{TriggererUserID: 303, ResourceOwnerUserID: 202, RunID: "run-chat", Purpose: trustedPurposeChatMain}
	if _, _, err = establishTrustedOperation(authenticated, spoof, 202, trustedPurposeChatMain, "run-chat"); err != llm.ErrTrustedTriggerMismatch {
		t.Fatalf("spoof error = %v, want %v", err, llm.ErrTrustedTriggerMismatch)
	}
}

// SOURCE-ONLY: MCP retries retain one stable child execution. Independent MCP
// service pricing remains separate from model/player discounts in settlement.
func TestMCPChildExecutionIsStableForOneLogicalToolCall(t *testing.T) {
	authenticated, err := llm.WithAuthenticatedExecutionSubject(context.TODO(), 101, "run-chat")
	if err != nil {
		t.Fatal(err)
	}
	operation, _, err := establishTrustedOperation(authenticated, nil, 0, trustedPurposeChatMain, "run-chat")
	if err != nil {
		t.Fatal(err)
	}
	rootInput := llm.GenerateInput{RunID: "run-chat", ExecutionID: "exec-root"}
	root, _, err := prepareTrustedProviderExecution(operation, &rootInput, 0, trustedPurposeChatMain, "run-chat")
	if err != nil {
		t.Fatal(err)
	}
	first, firstTrigger, err := trustedMCPChildContext(root, "run-chat", "call-1", 9, "lookup", `{"q":"x"}`)
	if err != nil {
		t.Fatal(err)
	}
	_, secondTrigger, err := trustedMCPChildContext(root, "run-chat", "call-1", 9, "lookup", `{"q":"x"}`)
	if err != nil {
		t.Fatal(err)
	}
	if firstTrigger.ExecutionID != secondTrigger.ExecutionID || firstTrigger.ParentExecutionID == "" || llm.ExecutionSubjectFromContext(first).TriggererUserID != 101 {
		t.Fatalf("unstable MCP child: first=%+v second=%+v", firstTrigger, secondTrigger)
	}
}
