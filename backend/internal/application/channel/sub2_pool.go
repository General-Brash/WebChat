package channel

import "encoding/json"

// This marker satisfies legacy route selection only. It is never sent as a
// billing key: the Sub2 gateway obtains a per-user, per-group managed identity.
const sub2RoutingMarkerKeys = `{"strategy":"round_robin","keys":[{"key":"sub2-subject-only","status":"active"}]}`

func normalizeSub2GroupIDs(ids []int64) (string, error) {
	seen := map[int64]bool{}
	out := make([]int64, 0, len(ids))
	for _, id := range ids {
		if id <= 0 {
			return "", ErrInvalidCompatible
		}
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	raw, err := json.Marshal(out)
	return string(raw), err
}
func decodeSub2GroupIDs(raw string) []int64 {
	result := []int64{}
	_ = json.Unmarshal([]byte(raw), &result)
	return result
}
