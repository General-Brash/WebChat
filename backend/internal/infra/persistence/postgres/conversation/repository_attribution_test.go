package conversation

import (
	"testing"
	"time"

	domainconversation "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/conversation"
	models "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/persistence/models"
)

func TestFileProcessingAndRunAttributionMappersRoundTrip(t *testing.T) {
	createdAt := time.Date(2026, time.September, 16, 9, 0, 0, 0, time.UTC)
	fileModel := models.FileObject{
		BaseModel:                     models.BaseModel{ID: 7},
		UserID:                        22,
		ProcessingTriggererUserID:     11,
		ProcessingResourceOwnerUserID: 22,
		ProcessingPurpose:             "file.extract",
		ProcessingRunID:               "run_file_a",
		ProcessingExecutionID:         "exec_file_a",
		ProcessingParentExecutionID:   "exec_parent",
		ProcessingTriggerCreatedAt:    &createdAt,
		ProcessingStatus:              "extracting",
	}
	state := toFileObjectProcessingStateDomain(fileModel)
	if state.UserID != 22 || state.TriggererUserID != 11 || state.ResourceOwnerUserID != 22 ||
		state.Purpose != "file.extract" || state.RunID != "run_file_a" ||
		state.ExecutionID != "exec_file_a" || state.ParentExecutionID != "exec_parent" ||
		state.TriggerCreatedAt == nil || !state.TriggerCreatedAt.Equal(createdAt) {
		t.Fatalf("file processing attribution did not round-trip from model: %#v", state)
	}
	updates := fileObjectProcessingStateUpdates(&state)
	for key, want := range map[string]any{
		"processing_triggerer_user_id":      11,
		"processing_resource_owner_user_id": 22,
		"processing_purpose":                "file.extract",
		"processing_run_id":                 "run_file_a",
		"processing_execution_id":           "exec_file_a",
		"processing_parent_execution_id":    "exec_parent",
	} {
		if got := updates[key]; got != want {
			t.Fatalf("file processing update %q = %#v, want %#v", key, got, want)
		}
	}

	runModel := models.ConversationRun{
		RunID:               "run_chat_a",
		UserID:              22,
		TriggererUserID:     11,
		ResourceOwnerUserID: 22,
		Purpose:             "chat.main",
		ExecutionID:         "exec_chat_a",
		ParentExecutionID:   "exec_parent",
		TriggerCreatedAt:    &createdAt,
	}
	run := toConversationRunDomain(runModel)
	if run.UserID != 22 || run.TriggererUserID != 11 || run.ResourceOwnerUserID != 22 ||
		run.Purpose != "chat.main" || run.ExecutionID != "exec_chat_a" ||
		run.ParentExecutionID != "exec_parent" || run.TriggerCreatedAt == nil || !run.TriggerCreatedAt.Equal(createdAt) {
		t.Fatalf("conversation run attribution did not round-trip from model: %#v", run)
	}
	runRoundTrip := toConversationRunModel(&domainconversation.Run{
		RunID:               run.RunID,
		UserID:              run.UserID,
		TriggererUserID:     run.TriggererUserID,
		ResourceOwnerUserID: run.ResourceOwnerUserID,
		Purpose:             run.Purpose,
		ExecutionID:         run.ExecutionID,
		ParentExecutionID:   run.ParentExecutionID,
		TriggerCreatedAt:    run.TriggerCreatedAt,
	})
	if runRoundTrip.TriggererUserID != 11 || runRoundTrip.ResourceOwnerUserID != 22 ||
		runRoundTrip.Purpose != "chat.main" || runRoundTrip.ExecutionID != "exec_chat_a" ||
		runRoundTrip.ParentExecutionID != "exec_parent" || runRoundTrip.TriggerCreatedAt == nil ||
		!runRoundTrip.TriggerCreatedAt.Equal(createdAt) {
		t.Fatalf("conversation run attribution did not round-trip to model: %#v", runRoundTrip)
	}
}

func TestLegacyFileProcessingStateDoesNotEraseStoredAttributionOnLocalUpdate(t *testing.T) {
	legacy := &domainconversation.FileObjectProcessing{FileObjectID: 7, UserID: 22, ProcessingStatus: "ready"}
	updates := fileObjectProcessingStateUpdates(legacy)
	for _, key := range []string{
		"processing_triggerer_user_id",
		"processing_resource_owner_user_id",
		"processing_purpose",
		"processing_run_id",
		"processing_execution_id",
		"processing_parent_execution_id",
		"processing_trigger_created_at",
	} {
		if _, ok := updates[key]; ok {
			t.Fatalf("legacy/local state update unexpectedly clears attribution field %q", key)
		}
	}
}
