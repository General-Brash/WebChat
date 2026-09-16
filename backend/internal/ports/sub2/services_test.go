package sub2

import "testing"

func TestServiceSettlementFingerprintAndKeyAreStableAcrossItemOrder(t *testing.T) {
	items := []ServiceSettlementItem{
		{Code: "mcp:9:fetch", Count: 1, ExpectedUnitNanousd: 5000},
		{Code: "mcp:2:search", Count: 2, ExpectedUnitNanousd: 1000},
	}
	fingerprint, err := ServiceSettlementPayloadFingerprint("deeix-chat", "run-1", items)
	if err != nil {
		t.Fatalf("fingerprint: %v", err)
	}
	if fingerprint != "10a63d4ca7a2208e2f70d8b24be24bd88d8173d0d5b247b5a48da920bd319b43" {
		t.Fatalf("fingerprint = %q", fingerprint)
	}
	key := ServiceSettlementRequestKey("deeix-chat", "run-1", fingerprint)
	if key != "s2svc2ae36b2e339089a09bd3a76327af496bcd3b6341bd55ffb956811213605" || len(key) != 64 {
		t.Fatalf("request key = %q", key)
	}
	reversed, err := ServiceSettlementPayloadFingerprint("deeix-chat", "run-1", []ServiceSettlementItem{items[1], items[0]})
	if err != nil || reversed != fingerprint {
		t.Fatalf("reordered items changed fingerprint: %q / %v", reversed, err)
	}
	if ServiceSettlementRequestKey("deeix-chat", "run-2", fingerprint) == key {
		t.Fatal("run identity did not change request key")
	}
}

func TestServiceSettlementFingerprintRejectsInvalidItems(t *testing.T) {
	if _, err := ServiceSettlementPayloadFingerprint("deeix-chat", "run-1", []ServiceSettlementItem{{Code: "mcp:search", Count: 0, ExpectedUnitNanousd: 1}}); err == nil {
		t.Fatal("expected zero-count service item to be rejected")
	}
}
