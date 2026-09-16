package app

import (
	"testing"

	appbilling "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/application/billing"
	domainbilling "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/billing"
	sub2port "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/sub2"
)

func TestBuildSub2ServiceSettlementRequestUsesStableMCPPayload(t *testing.T) {
	request, err := buildSub2ServiceSettlementRequest("deeix-chat", "run-1", []appbilling.MCPToolUsageInput{
		{ServerID: 9, ToolName: "fetch", CallCount: 1, PriceNanousd: 5000},
		{ServerID: 2, ToolName: "search", CallCount: 2, PriceNanousd: 1000},
	})
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if request.PayloadFingerprint != "10a63d4ca7a2208e2f70d8b24be24bd88d8173d0d5b247b5a48da920bd319b43" {
		t.Fatalf("payload fingerprint = %q", request.PayloadFingerprint)
	}
	if request.RequestKey != "s2svc2ae36b2e339089a09bd3a76327af496bcd3b6341bd55ffb956811213605" || len(request.RequestKey) != 64 {
		t.Fatalf("request key = %q", request.RequestKey)
	}
	if len(request.Items) != 2 || request.Items[0].Code != "mcp:2:search" {
		t.Fatalf("normalized items = %#v", request.Items)
	}
}

func TestBuildSub2ServiceSettlementRequestExcludesUnpricedMCPCalls(t *testing.T) {
	request, err := buildSub2ServiceSettlementRequest("deeix-chat", "run-1", []appbilling.MCPToolUsageInput{
		{ServerID: 1, ToolName: "free", CallCount: 2, PriceNanousd: 0},
	})
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if len(request.Items) != 0 || request.RequestKey != "" || request.PayloadFingerprint != "" {
		t.Fatalf("unpriced request = %#v", request)
	}
}


func TestProjectSub2ExecutionUsageUsesAuthoritativeUsageSnapshot(t *testing.T) {
	inputTokens := int64(17)
	outputTokens := int64(23)
	result := &sub2port.ExecutionQueryResponse{
		ExecutionID:  "execution-1",
		State:        sub2port.ServiceSettlementStateSettled,
		Terminal:     true,
		BilledNanousd: 999999,
		AuthoritativeUsage: &sub2port.AuthoritativeUsageSnapshot{
			SchemaVersion: sub2port.AuthoritativeUsageSnapshotVersion,
			Availability:  sub2port.UsageSnapshotAvailable,
			Authoritative:  true,
			State:         sub2port.ServiceSettlementStateSettled,
			BilledNanousd: 1234,
			Usage: &sub2port.AuthoritativeUsageMeasures{
				InputTokens:  &inputTokens,
				OutputTokens: &outputTokens,
			},
		},
	}
	projection := projectSub2ExecutionUsage([]sub2ExecutionUsageQuery{{ExecutionID: "execution-1", Result: result}})
	if !projection.Complete || projection.State != sub2AuthoritativeUsageAvailable || projection.BilledNanousd != 1234 {
		t.Fatalf("projection = %#v, want complete authoritative snapshot", projection)
	}
	ledger := &domainbilling.UsageLedger{}
	applySub2UsageProjection(ledger, projection, true)
	if ledger.InputTokens != inputTokens || ledger.OutputTokens != outputTokens || ledger.BilledNanousd != 1234 {
		t.Fatalf("ledger = %#v, want authoritative measures", ledger)
	}
}

func TestProjectSub2ExecutionUsageDoesNotPromoteLegacyAmountWithoutSnapshot(t *testing.T) {
	result := &sub2port.ExecutionQueryResponse{
		ExecutionID:   "execution-legacy",
		State:         sub2port.ServiceSettlementStateSettled,
		Terminal:      true,
		BilledNanousd: 777,
		UsageOutbox:   &sub2port.UsageOutboxStatus{State: "PENDING", AttemptCount: 2},
	}
	projection := projectSub2ExecutionUsage([]sub2ExecutionUsageQuery{{ExecutionID: "execution-legacy", Result: result}})
	if projection.Complete || projection.BilledNanousd != 0 || projection.State != sub2AuthoritativeUsagePending {
		t.Fatalf("legacy projection = %#v, want pending without amount promotion", projection)
	}
	ledger := &domainbilling.UsageLedger{}
	applySub2UsageProjection(ledger, projection, true)
	if ledger.BilledNanousd != 0 || ledger.InputTokens != 0 || ledger.OutputTokens != 0 {
		t.Fatalf("legacy ledger = %#v, want no guessed usage", ledger)
	}
}
