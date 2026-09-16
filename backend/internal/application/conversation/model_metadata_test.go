package conversation

import (
	"testing"

	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/application/channel"
)

func TestMessageRouteConfigKeepsSelectedAndProviderModelsSeparate(t *testing.T) {
	route := &channel.ResolvedRoute{
		UserID:               7,
		UpstreamID:           9,
		PlatformModelName:    "chat-alias",
		UpstreamModel:        "provider-model",
		UpstreamModelRawJSON: `{"source_group_ids":[3]}`,
		Protocol:             "openai_responses",
	}
	config := messageRouteConfig(route, "", "")
	if config.RetailModel != "chat-alias" || config.UpstreamModel != "provider-model" {
		t.Fatalf("selected/request model identities were conflated: %#v", config)
	}
	if config.UpstreamModelRawJSON == "" {
		t.Fatal("expected route catalog metadata to reach the request adapter")
	}
}
