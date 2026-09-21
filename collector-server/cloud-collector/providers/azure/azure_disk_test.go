package azure

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPremiumV2Decision(t *testing.T) {
	// The gate must fail OPEN on any untrustworthy answer and suppress only when a region
	// positively lists disk SKUs without PremiumV2_LRS among them.
	tests := []struct {
		name          string
		listErr       bool
		sawDiskSKU    bool
		foundV2       bool
		wantAvailable bool
	}{
		{"list error fails open", true, false, false, true},
		{"list error fails open even if disks were seen", true, true, false, true},
		{"no disk SKUs seen fails open (query/format issue)", false, false, false, true},
		{"disks seen with PremiumV2_LRS present is available", false, true, true, true},
		{"disks seen without PremiumV2_LRS is unavailable", false, true, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.wantAvailable, premiumV2Decision(tt.listErr, tt.sawDiskSKU, tt.foundV2))
		})
	}
}
