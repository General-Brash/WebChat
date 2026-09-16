package cache

import (
	"strings"
	"testing"
	"time"

	portllm "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/llm"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/repository"
	"github.com/go-redis/redis/v8"
)

func TestParseFileEmbeddingMessagePreservesMetadata(t *testing.T) {
	message, err := parseFileProcessingMessage(redis.XMessage{
		ID: "1-0",
		Values: map[string]any{
			"user_id":             "7",
			"file_id":             "file_1",
			"retry":               "1",
			"kind":                repository.FileProcessingKindEmbedding,
			"embedding_signature": "model@1536",
			"embedding_host":      "https://embedding.example/v1/",
		},
	})
	if err != nil {
		t.Fatalf("parse embedding message: %v", err)
	}
	if message.UserID != 7 || message.FileID != "file_1" || message.Retry != 1 ||
		message.Kind != repository.FileProcessingKindEmbedding ||
		message.EmbeddingSignature != "model@1536" ||
		message.EmbeddingHost != "https://embedding.example/v1" {
		t.Fatalf("unexpected embedding message: %#v", message)
	}
}

func TestRedisQueueForMessagePreservesLegacySourceQueue(t *testing.T) {
	legacy := repository.FileProcessingMessage{
		Kind:  repository.FileProcessingKindEmbedding,
		Queue: repository.FileProcessingQueueDefault,
	}
	if queue := redisQueueForMessage(legacy); queue.stream != fileProcessingStreamName {
		t.Fatalf("legacy message routed to %q, want %q", queue.stream, fileProcessingStreamName)
	}

	current := repository.FileProcessingMessage{
		Kind:  repository.FileProcessingKindEmbedding,
		Queue: repository.FileProcessingQueueEmbedding,
	}
	if queue := redisQueueForMessage(current); queue.stream != fileEmbeddingStreamName {
		t.Fatalf("embedding message routed to %q, want %q", queue.stream, fileEmbeddingStreamName)
	}
}

func TestParseFileProcessingMessageRoundTripsPlatformOwnerAndTrustedActor(t *testing.T) {
	createdAt := time.Date(2026, time.September, 16, 9, 0, 0, 0, time.UTC)
	message, err := parseFileProcessingMessage(redis.XMessage{
		ID: "2-0",
		Values: map[string]any{
			"user_id":                "0",
			"file_id":                "platform_file",
			"retry":                  "2",
			"triggerer_user_id":      "11",
			"resource_owner_user_id": "0",
			"purpose":                "file.embedding",
			"run_id":                 "run_platform_a",
			"execution_id":           "exec_platform_a",
			"parent_execution_id":    "exec_parent",
			"trigger_created_at":     createdAt.Format(time.RFC3339Nano),
		},
	})
	if err != nil {
		t.Fatalf("parse platform message: %v", err)
	}
	want := portllm.TrustedTriggerContext{
		TriggererUserID:     11,
		ResourceOwnerUserID: 0,
		Purpose:             "file.embedding",
		RunID:               "run_platform_a",
		ExecutionID:         "exec_platform_a",
		ParentExecutionID:   "exec_parent",
		CreatedAt:           createdAt,
	}
	if message.UserID != 0 || !repository.SameTrustedTriggerContext(message.TriggerContext, want) {
		t.Fatalf("platform owner/actor attribution changed: %#v", message)
	}
}

func TestLegacyRedisPayloadRetainsAbsentActorAndTrustedCodecHasNoSecrets(t *testing.T) {
	legacy, err := parseFileProcessingMessage(redis.XMessage{
		ID: "3-0",
		Values: map[string]any{
			"user_id": "0",
			"file_id": "legacy_platform",
			"retry":   "0",
		},
	})
	if err != nil {
		t.Fatalf("parse legacy platform payload: %v", err)
	}
	if repository.HasTrustedTriggerMetadata(legacy.TriggerContext) || legacy.TriggerContext.TriggererUserID != 0 {
		t.Fatalf("legacy actor was invented: %#v", legacy.TriggerContext)
	}
	encoded := marshalTrustedTriggerContext(portllm.TrustedTriggerContext{
		TriggererUserID: 11,
		Purpose:         "file.extract",
		RunID:           "run_a",
		ExecutionID:     "exec_a",
	})
	for _, forbidden := range []string{"token", "assertion", "api_key", "credential", "secret"} {
		if strings.Contains(strings.ToLower(encoded), forbidden) {
			t.Fatalf("trusted codec serialized forbidden subject material %q: %s", forbidden, encoded)
		}
	}
}
