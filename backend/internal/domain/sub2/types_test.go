package sub2

import "testing"

func TestNormalizeAuthorityExecutionState(t *testing.T) {
	tests := []struct {
		name     string
		state    string
		terminal bool
		want     string
	}{
		{name: "pending", state: "RUNNING", want: ExecutionStateReconciling},
		{name: "cancelled", state: "CANCELLED", terminal: true, want: ExecutionStateCanceled},
		{name: "failed", state: "provider_error", terminal: true, want: ExecutionStateFailed},
		{name: "settled", state: "SETTLED", terminal: true, want: ExecutionStateSettled},
		{name: "successful", state: "COMPLETED", terminal: true, want: ExecutionStateSucceeded},
		{name: "unknown terminal", state: "", terminal: true, want: ExecutionStateUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NormalizeAuthorityExecutionState(tt.state, tt.terminal); got != tt.want {
				t.Fatalf("NormalizeAuthorityExecutionState(%q, %t) = %q, want %q", tt.state, tt.terminal, got, tt.want)
			}
		})
	}
}

func TestTerminalExecutionStatesAreNotReplayable(t *testing.T) {
	for _, state := range []string{ExecutionStateSucceeded, ExecutionStateFailed, ExecutionStateCanceled, ExecutionStateSettled} {
		if !IsTerminalExecutionState(state) {
			t.Fatalf("state %q was not recognized as terminal", state)
		}
	}
	for _, state := range []string{ExecutionStateCreated, ExecutionStateAuthorized, ExecutionStateDispatched, ExecutionStateUnknown, ExecutionStateReconciling} {
		if IsTerminalExecutionState(state) {
			t.Fatalf("state %q was incorrectly recognized as terminal", state)
		}
	}
}
