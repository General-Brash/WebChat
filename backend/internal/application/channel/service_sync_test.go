package channel

import (
	"context"
	"testing"
)

func TestMergeImportedModelPermissionGroupIDsPreservesExistingManualGroups(t *testing.T) {
	writer := &permissionGroupWriterStub{
		manualGroupIDs: []uint{3, 1, 3},
	}

	ids, err := mergeImportedModelPermissionGroupIDs(context.Background(), writer, 10, []uint{2, 1})
	if err != nil {
		t.Fatalf("mergeImportedModelPermissionGroupIDs() error = %v", err)
	}

	want := []uint{1, 2, 3}
	if len(ids) != len(want) {
		t.Fatalf("ids len = %d, want %d: %#v", len(ids), len(want), ids)
	}
	for index := range want {
		if ids[index] != want[index] {
			t.Fatalf("ids = %#v, want %#v", ids, want)
		}
	}
	if writer.requestedModelID != 10 {
		t.Fatalf("requested model id = %d, want 10", writer.requestedModelID)
	}
}

type permissionGroupWriterStub struct {
	manualGroupIDs   []uint
	requestedModelID uint
}

func (s *permissionGroupWriterStub) PermissionGroupExists(context.Context, uint) (bool, error) {
	return true, nil
}

func (s *permissionGroupWriterStub) ListModelManualGroupIDs(_ context.Context, platformModelID uint) ([]uint, error) {
	s.requestedModelID = platformModelID
	return s.manualGroupIDs, nil
}

func (s *permissionGroupWriterStub) SetModelManualGroups(context.Context, uint, []uint) error {
	return nil
}
