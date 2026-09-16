package sub2

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// GlobalGroupPrioritySettingKey is the single persisted Chat setting that owns
// the administrator-selected order of Sub2 groups for publication.
const GlobalGroupPrioritySettingKey = "sub2.global_group_priority"

const GlobalGroupPrioritySettingDescription = "Sub2 全局分组优先级（管理员显式配置）"

var ErrInvalidGlobalGroupPriority = errors.New("invalid Sub2 global group priority")

// NormalizeGlobalGroupPriority validates and de-duplicates a configured order
// without changing the first-seen priority of any group.
func NormalizeGlobalGroupPriority(ids []int64) ([]int64, error) {
	if len(ids) == 0 {
		return nil, fmt.Errorf("%w: the ordered group list must not be empty", ErrInvalidGlobalGroupPriority)
	}
	seen := make(map[int64]struct{}, len(ids))
	result := make([]int64, 0, len(ids))
	for _, id := range ids {
		if id <= 0 {
			return nil, fmt.Errorf("%w: group IDs must be positive", ErrInvalidGlobalGroupPriority)
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		result = append(result, id)
	}
	return result, nil
}

// ParseGlobalGroupPriority parses the JSON array stored by the existing LLM
// settings API. An absent, null, or empty array is intentionally invalid so a
// missing administrator decision can never mean "all groups".
func ParseGlobalGroupPriority(value string) ([]int64, error) {
	if strings.TrimSpace(value) == "" {
		return nil, fmt.Errorf("%w: configure an explicit ordered group list", ErrInvalidGlobalGroupPriority)
	}
	var ids []int64
	if err := json.Unmarshal([]byte(value), &ids); err != nil {
		return nil, fmt.Errorf("%w: value must be a JSON array of group IDs", ErrInvalidGlobalGroupPriority)
	}
	return NormalizeGlobalGroupPriority(ids)
}
