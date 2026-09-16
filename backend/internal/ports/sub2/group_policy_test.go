package sub2

import (
	"errors"
	"testing"
)

func TestParseGlobalGroupPriorityDeduplicatesWithoutChangingOrder(t *testing.T) {
	got, err := ParseGlobalGroupPriority(`[20,10,20,30]`)
	if err != nil {
		t.Fatalf("ParseGlobalGroupPriority() error = %v", err)
	}
	want := []int64{20, 10, 30}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestParseGlobalGroupPriorityFailsClosedForEmptyOrInvalidValues(t *testing.T) {
	for _, value := range []string{"", `[]`, `null`, `[0]`, `{"groups":[1]}`} {
		if _, err := ParseGlobalGroupPriority(value); err == nil || !errors.Is(err, ErrInvalidGlobalGroupPriority) {
			t.Fatalf("ParseGlobalGroupPriority(%q) error = %v, want ErrInvalidGlobalGroupPriority", value, err)
		}
	}
}
