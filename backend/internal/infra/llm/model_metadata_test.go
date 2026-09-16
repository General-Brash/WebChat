package llm

import (
	"testing"

	portllm "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/llm"
)

func TestParseOpenAIModelListPreservesCatalogMetadata(t *testing.T) {
	items, err := parseOpenAIModelList([]byte(`{"data":[{"id":"gpt-5","owned_by":"openai","display_name":"GPT 5","protocols":["openai_responses","openai_responses"],"source_group_ids":[2,1,2]}]}`))
	if err != nil {
		t.Fatalf("parse model list: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected one item, got %#v", items)
	}
	item := items[0]
	if item.ID != "gpt-5" || item.OwnedBy != "openai" || item.DisplayName != "GPT 5" {
		t.Fatalf("unexpected identity metadata: %#v", item)
	}
	if len(item.Protocols) != 1 || item.Protocols[0] != "openai_responses" {
		t.Fatalf("unexpected provider protocols: %#v", item.Protocols)
	}
	if len(item.SourceGroupIDs) != 2 || item.SourceGroupIDs[0] != 1 || item.SourceGroupIDs[1] != 2 {
		t.Fatalf("unexpected source groups: %#v", item.SourceGroupIDs)
	}
}

func TestBuildGenerateOutputKeepsReturnedModelSeparate(t *testing.T) {
	output := buildGenerateOutputFromParsedForAdapter(portllm.EndpointResponses, portllm.AdapterOpenAIResponses, map[string]any{
		"id":          "resp_1",
		"model":       "gpt-5-mini",
		"output_text": "hello",
	}, false)
	if output.ReturnedModel != "gpt-5-mini" {
		t.Fatalf("expected provider returned model, got %#v", output)
	}
	if output.ResponseID != "resp_1" || output.Text != "hello" {
		t.Fatalf("unexpected response metadata: %#v", output)
	}
}

func TestBuildOpenAIRequestUsesProviderModelNotCatalogAlias(t *testing.T) {
	body, err := buildOpenAIRequestBody(portllm.AdapterOpenAIResponses, "provider-model", portllm.EndpointResponses, portllm.GenerateInput{
		Options: map[string]any{"model": "catalog-alias"},
	}, false)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if body["model"] != "provider-model" {
		t.Fatalf("request model was conflated with catalog alias: %#v", body["model"])
	}
}
