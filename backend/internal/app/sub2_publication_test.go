package app

import "testing"

func TestPublicationFingerprintIncludesModelAndReleaseMetadata(t *testing.T) {
	plan := &sub2ModelPublicationPayload{
		SyncItems:    []map[string]any{{"model": "provider-a", "protocol": "openai_responses", "source_group_id": int64(1)}},
		Publications: []map[string]any{{"model": "provider-a", "protocol": "openai_responses"}},
	}
	selections := []map[string]any{{"group_id": int64(1), "priority": 0}}
	first := publicationFingerprint("price-hash", 2, 3, 4, 5, selections, plan)
	if first == "" {
		t.Fatal("expected publication fingerprint")
	}
	if got := publicationFingerprint("price-hash", 2, 3, 4, 5, selections, plan); got != first {
		t.Fatalf("fingerprint is not deterministic: %q != %q", got, first)
	}
	plan.Publications[0]["protocol"] = "openai_chat_completions"
	if got := publicationFingerprint("price-hash", 2, 3, 4, 5, selections, plan); got == first {
		t.Fatal("model protocol change did not invalidate publication fingerprint")
	}
}

func TestFilterPublishedGroupIDsHonorsSourceCatalog(t *testing.T) {
	got := filterPublishedGroupIDs([]int64{1, 2, 3}, `{"source_group_ids":[2,3]}`)
	if len(got) != 2 || got[0] != 2 || got[1] != 3 {
		t.Fatalf("unexpected filtered groups: %#v", got)
	}
	fallback := filterPublishedGroupIDs([]int64{1, 2}, `{}`)
	if len(fallback) != 0 {
		t.Fatalf("empty source metadata must fail closed: %#v", fallback)
	}
}

func TestFilterPublishedGroupIDsNeverExpandsConfiguredGroups(t *testing.T) {
	got := filterPublishedGroupIDs([]int64{20}, `{"source_group_ids":[20,30]}`)
	if len(got) != 1 || got[0] != 20 {
		t.Fatalf("source catalog expanded configured groups: %#v", got)
	}
}
func TestStableModelSyncPayloadHashCanonicalizesSnapshotOrder(t *testing.T) {
	first := []map[string]any{
		{"model": "provider-b", "protocol": "OPENAI_RESPONSES", "source_group_id": int64(2), "capabilities": map[string]any{"retail_models": []string{"alias-b"}}},
		{"model": "provider-a", "protocol": "openai_chat_completions", "source_group_id": int64(1), "capabilities": map[string]any{"retail_models": []string{"alias-a"}}},
	}
	second := []map[string]any{
		{"model": " provider-a ", "protocol": "OPENAI_CHAT_COMPLETIONS", "source_group_id": int64(1), "capabilities": map[string]any{"retail_models": []string{"alias-a"}}},
		{"model": "provider-b", "protocol": "openai_responses", "source_group_id": int64(2), "capabilities": map[string]any{"retail_models": []string{"alias-b"}}},
	}
	left, err := stableModelSyncPayloadHash(first)
	if err != nil {
		t.Fatalf("hash first payload: %v", err)
	}
	right, err := stableModelSyncPayloadHash(second)
	if err != nil {
		t.Fatalf("hash second payload: %v", err)
	}
	if left == "" || left != right {
		t.Fatalf("canonical payload hashes differ: %q != %q", left, right)
	}
	if left != "682eb70c2e7fbe9499d996690621eb44bb7c28671bb13f311583a797e792db57" {
		t.Fatalf("wire payload hash changed unexpectedly: %q", left)
	}
}

func TestStableModelSyncPayloadHashRetainsEverySourceGroup(t *testing.T) {
	one, err := stableModelSyncPayloadHash([]map[string]any{{"model": "provider-a", "protocol": "openai_responses", "source_group_id": int64(1)}})
	if err != nil {
		t.Fatalf("hash one-group payload: %v", err)
	}
	two, err := stableModelSyncPayloadHash([]map[string]any{
		{"model": "provider-a", "protocol": "openai_responses", "source_group_id": int64(1)},
		{"model": "provider-a", "protocol": "openai_responses", "source_group_id": int64(2)},
	})
	if err != nil {
		t.Fatalf("hash two-group payload: %v", err)
	}
	if one == two {
		t.Fatal("source-group set was omitted from model sync payload hash")
	}
}

func TestPublicationFingerprintChangesWhenGlobalGroupOrderChanges(t *testing.T) {
	plan := &sub2ModelPublicationPayload{
		SyncItems:    []map[string]any{{"model": "provider-a", "protocol": "openai_responses", "source_group_id": int64(1)}},
		Publications: []map[string]any{{"model": "provider-a", "protocol": "openai_responses"}},
	}
	first := publicationFingerprint("price-hash", 2, 3, 4, 5, []map[string]any{{"group_id": int64(1), "priority": 0}, {"group_id": int64(2), "priority": 1}}, plan)
	second := publicationFingerprint("price-hash", 2, 3, 4, 5, []map[string]any{{"group_id": int64(2), "priority": 0}, {"group_id": int64(1), "priority": 1}}, plan)
	if first == second {
		t.Fatal("reordered global group priority did not invalidate publication fingerprint")
	}
}
