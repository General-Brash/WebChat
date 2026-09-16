package sub2

import (
	"encoding/json"
	"testing"
)

func TestAuthoritativeUsageSnapshotCodecIsAdditiveAndVersioned(t *testing.T) {
	available := int64(7)
	input := AuthoritativeUsageSnapshot{
		SchemaVersion:       AuthoritativeUsageSnapshotVersion,
		Availability:        UsageSnapshotAvailable,
		Authoritative:       true,
		ExecutionID:         "execution-1",
		RequestFingerprints: []string{"fingerprint-1"},
		Usage:               &AuthoritativeUsageMeasures{InputTokens: &available},
	}
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	var decoded AuthoritativeUsageSnapshot
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.SchemaVersion != AuthoritativeUsageSnapshotVersion || !decoded.Authoritative || len(decoded.RequestFingerprints) != 1 || decoded.Usage == nil || decoded.Usage.InputTokens == nil || *decoded.Usage.InputTokens != available {
		t.Fatalf("usage snapshot codec lost versioned typed fields: %#v", decoded)
	}

	var legacy struct {
		ExecutionID string `json:"execution_id"`
		Billed      int64  `json:"billed_nanousd"`
	}
	if err := json.Unmarshal([]byte(`{"execution_id":"legacy","billed_nanousd":12}`), &legacy); err != nil {
		t.Fatal(err)
	}
	if legacy.ExecutionID != "legacy" || legacy.Billed != 12 {
		t.Fatalf("legacy amount-only response no longer decodes: %#v", legacy)
	}
}
