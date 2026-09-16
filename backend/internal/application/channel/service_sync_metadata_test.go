package channel

import (
	"testing"

	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/llm"
)

func TestNormalizeRemoteModelItemsMergesProtocolAndSourceMetadata(t *testing.T) {
	items := normalizeRemoteModelItems([]llm.ModelItem{
		{ID: "model-a", OwnedBy: "provider", Protocols: []string{"openai_responses"}, SourceGroupIDs: []int64{2}},
		{ID: "model-a", Protocols: []string{"openai_chat_completions"}, SourceGroupIDs: []int64{1, 2}},
	})
	if len(items) != 1 {
		t.Fatalf("expected one merged model, got %#v", items)
	}
	if len(items[0].Protocols) != 2 || items[0].Protocols[0] != "openai_chat_completions" || items[0].Protocols[1] != "openai_responses" {
		t.Fatalf("protocol metadata was dropped or unstable: %#v", items[0].Protocols)
	}
	if len(items[0].SourceGroupIDs) != 2 || items[0].SourceGroupIDs[0] != 1 || items[0].SourceGroupIDs[1] != 2 {
		t.Fatalf("source-group metadata was dropped or unstable: %#v", items[0].SourceGroupIDs)
	}
}
