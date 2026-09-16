package app

import (
	"bytes"
	"net/http"
	"strings"
	"testing"

	model "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/persistence/models"
)

func TestPublishedCacheWriteRatesRejectAggregateOnlyInput(t *testing.T) {
	if _, _, err := publishedCacheWriteRates("claude", 1_000_000_000); err == nil || !strings.Contains(err.Error(), "explicit independent") {
		t.Fatalf("aggregate-only cache write price error = %v, want explicit TTL fail-closed", err)
	}
	fiveMinute, oneHour, err := publishedCacheWriteRates("free", 0)
	if err != nil || fiveMinute != 0 || oneHour != 0 {
		t.Fatalf("zero cache write rates = (%d, %d, %v), want (0, 0, nil)", fiveMinute, oneHour, err)
	}
}

func TestBuildGlobalGroupSelectionsUsesExplicitGlobalOrderAndDeduplicates(t *testing.T) {
	upstreams := []model.LLMUpstream{
		{ControlPlaneModel: model.ControlPlaneModel{ID: 10}, Sub2GroupIDsJSON: `[10,20,20]`},
		{ControlPlaneModel: model.ControlPlaneModel{ID: 20}, Sub2GroupIDsJSON: `[20,30]`},
	}
	selections, groupsByUpstream, err := buildGlobalGroupSelections(upstreams, []int64{20, 10, 30, 20})
	if err != nil {
		t.Fatalf("buildGlobalGroupSelections: %v", err)
	}
	want := []int64{20, 10, 30}
	if len(selections) != len(want) {
		t.Fatalf("selection count = %d, want %d: %#v", len(selections), len(want), selections)
	}
	for i, item := range selections {
		if item["group_id"] != want[i] || item["priority"] != i {
			t.Fatalf("selection[%d] = %#v, want group=%d priority=%d", i, item, want[i], i)
		}
	}
	if got := groupsByUpstream[10]; len(got) != 2 || got[0] != 10 || got[1] != 20 {
		t.Fatalf("upstream 10 groups = %#v, want [10 20]", got)
	}
	reversed, _, err := buildGlobalGroupSelections([]model.LLMUpstream{upstreams[1], upstreams[0]}, []int64{20, 10, 30})
	if err != nil || len(reversed) != len(selections) {
		t.Fatalf("upstream traversal changed explicit selection: %#v, %v", reversed, err)
	}
	for i := range selections {
		if reversed[i]["group_id"] != selections[i]["group_id"] || reversed[i]["priority"] != selections[i]["priority"] {
			t.Fatalf("reversed selection[%d] = %#v, want %#v", i, reversed[i], selections[i])
		}
	}
}

func TestBuildGlobalGroupSelectionsRequiresExplicitOrder(t *testing.T) {
	upstreams := []model.LLMUpstream{{ControlPlaneModel: model.ControlPlaneModel{ID: 10}, Sub2GroupIDsJSON: `[10]`}}
	if _, _, err := buildGlobalGroupSelections(upstreams); err == nil || !strings.Contains(err.Error(), "save an explicit ordered group list") {
		t.Fatalf("missing configured order error = %v, want actionable fail-closed error", err)
	}
}
func TestBuildGlobalGroupSelectionsFailsClosedForMissingOrStaleMembers(t *testing.T) {
	upstreams := []model.LLMUpstream{{ControlPlaneModel: model.ControlPlaneModel{ID: 10}, Sub2GroupIDsJSON: `[10,20]`}}
	for name, configured := range map[string][]int64{
		"empty":   {},
		"missing": {10},
		"stale":   {10, 20, 30},
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := buildGlobalGroupSelections(upstreams, configured); err == nil || !strings.Contains(err.Error(), "active upstream groups") {
				t.Fatalf("expected actionable explicit group order validation error, got %v", err)
			}
		})
	}
}

func TestTerminalSub2FailureCodeBlocksManagedKeyFailover(t *testing.T) {
	response := &http.Response{StatusCode: http.StatusForbidden, Body: ioNopCloserString(`{"code":"managed_key_unavailable"}`)}
	if got := terminalSub2FailureCode(response); got != "managed_key_unavailable" {
		t.Fatalf("terminal code = %q, want managed_key_unavailable", got)
	}
	if response.Body == nil {
		t.Fatal("classifier must restore the response body for non-terminal callers")
	}
}

func TestTerminalSub2FailureCodeIgnoresOrdinaryAdmissionErrors(t *testing.T) {
	response := &http.Response{StatusCode: http.StatusForbidden, Body: ioNopCloserString(`{"code":"model_admission_denied"}`)}
	if got := terminalSub2FailureCode(response); got != "" {
		t.Fatalf("ordinary admission code = %q, want empty", got)
	}
}

type stringReadCloser struct{ *bytes.Reader }

func (s stringReadCloser) Close() error { return nil }

func ioNopCloserString(value string) stringReadCloser {
	return stringReadCloser{Reader: bytes.NewReader([]byte(value))}
}
