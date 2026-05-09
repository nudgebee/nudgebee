package azure

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestParseAzureVMResourceID_CaseInsensitive is the regression for the bug
// that caused Azure VM stop/start to fail when the resource ID had lowercase
// path segments (e.g. "resourcegroups" instead of "resourceGroups"). Azure
// resource IDs are case-insensitive in practice and the parser must match
// segment names with EqualFold.
func TestParseAzureVMResourceID_CaseInsensitive(t *testing.T) {
	tests := []struct {
		name             string
		resourceID       string
		wantSubscription string
		wantRG           string
		wantVM           string
	}{
		{
			name:             "canonical PascalCase segments",
			resourceID:       "/subscriptions/19e207a9-769d-4afd-b261-10bbed2d43e8/resourceGroups/nudgebee-dev_group/providers/Microsoft.Compute/virtualMachines/nudgebee-windows-vm",
			wantSubscription: "19e207a9-769d-4afd-b261-10bbed2d43e8",
			wantRG:           "nudgebee-dev_group",
			wantVM:           "nudgebee-windows-vm",
		},
		{
			name:             "all-lowercase segments (real-world failure case)",
			resourceID:       "/subscriptions/19e207a9-769d-4afd-b261-10bbed2d43e8/resourcegroups/nudgebee-dev_group/providers/microsoft.compute/virtualmachines/nudgebee-windows-vm",
			wantSubscription: "19e207a9-769d-4afd-b261-10bbed2d43e8",
			wantRG:           "nudgebee-dev_group",
			wantVM:           "nudgebee-windows-vm",
		},
		{
			name:             "mixed-case segments",
			resourceID:       "/Subscriptions/abc/ResourceGroups/myrg/providers/Microsoft.Compute/VirtualMachines/myvm",
			wantSubscription: "abc",
			wantRG:           "myrg",
			wantVM:           "myvm",
		},
		{
			name:             "missing virtualMachines segment",
			resourceID:       "/subscriptions/abc/resourceGroups/myrg",
			wantSubscription: "abc",
			wantRG:           "myrg",
			wantVM:           "",
		},
		{
			name:             "empty resource id returns all empty",
			resourceID:       "",
			wantSubscription: "",
			wantRG:           "",
			wantVM:           "",
		},
		{
			name:             "trailing virtualMachines without name",
			resourceID:       "/subscriptions/abc/resourceGroups/myrg/providers/Microsoft.Compute/virtualMachines",
			wantSubscription: "abc",
			wantRG:           "myrg",
			wantVM: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sub, rg, vm := parseAzureVMResourceID(tt.resourceID)
			assert.Equal(t, tt.wantSubscription, sub)
			assert.Equal(t, tt.wantRG, rg)
			assert.Equal(t, tt.wantVM, vm)
		})
	}
}
