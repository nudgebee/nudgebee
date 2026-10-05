package aws

import (
	"testing"
	"time"

	"nudgebee/collector/cloud/providers"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/stretchr/testify/assert"
)

func gp3Mod(state types.VolumeModificationState, start time.Time) types.VolumeModification {
	return types.VolumeModification{
		TargetVolumeType:  types.VolumeTypeGp3,
		ModificationState: state,
		StartTime:         aws.Time(start),
	}
}

func TestEbsGp3ApplyStatus(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name      string
		mods      []types.VolumeModification
		wantFound bool
		want      providers.ApplyStatus
	}{
		{"no modifications", nil, false, ""},
		{"only a non-gp3 modification", []types.VolumeModification{{TargetVolumeType: types.VolumeTypeIo2, ModificationState: types.VolumeModificationStateCompleted}}, false, ""},
		{"modifying", []types.VolumeModification{gp3Mod(types.VolumeModificationStateModifying, t0)}, true, providers.ApplyStatusInProgress},
		{"optimizing", []types.VolumeModification{gp3Mod(types.VolumeModificationStateOptimizing, t0)}, true, providers.ApplyStatusSucceeded},
		{"completed", []types.VolumeModification{gp3Mod(types.VolumeModificationStateCompleted, t0)}, true, providers.ApplyStatusSucceeded},
		{"failed", []types.VolumeModification{gp3Mod(types.VolumeModificationStateFailed, t0)}, true, providers.ApplyStatusFailed},
		{
			"latest gp3 modification wins over an older failure",
			[]types.VolumeModification{
				gp3Mod(types.VolumeModificationStateFailed, t0),
				gp3Mod(types.VolumeModificationStateModifying, t0.Add(time.Hour)),
			},
			true, providers.ApplyStatusInProgress,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, found := ebsGp3ApplyStatus(tt.mods)
			assert.Equal(t, tt.wantFound, found)
			assert.Equal(t, tt.want, got.Status)
		})
	}
}

func TestEbsGp3ApplyStatusFailedCarriesAwsReason(t *testing.T) {
	mod := gp3Mod(types.VolumeModificationStateFailed, time.Now())
	mod.StatusMessage = aws.String("Volume is in an incompatible state")

	got, found := ebsGp3ApplyStatus([]types.VolumeModification{mod})

	assert.True(t, found)
	assert.Contains(t, got.Message, "Volume is in an incompatible state")
}
