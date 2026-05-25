package azure

import (
	"nudgebee/collector/cloud/providers"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute"
	"github.com/stretchr/testify/assert"
)

func TestVMPowerStateToStatus(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected providers.ResourceStatus
	}{
		{"running maps to Active", "running", providers.ResourceStatusActive},
		{"starting maps to Active", "starting", providers.ResourceStatusActive},
		{"deallocated maps to Inactive", "deallocated", providers.ResourceStatusInactive},
		{"deallocating maps to Inactive", "deallocating", providers.ResourceStatusInactive},
		{"stopped maps to Inactive", "stopped", providers.ResourceStatusInactive},
		{"stopping maps to Inactive", "stopping", providers.ResourceStatusInactive},
		{"empty string returns empty", "", ""},
		{"unknown state returns empty", "unknown", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := vmPowerStateToStatus(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestExtractVMPowerState(t *testing.T) {
	tests := []struct {
		name         string
		instanceView *armcompute.VirtualMachineInstanceView
		expected     string
	}{
		{
			name:         "nil instance view returns empty",
			instanceView: nil,
			expected:     "",
		},
		{
			name: "running VM",
			instanceView: &armcompute.VirtualMachineInstanceView{
				Statuses: []*armcompute.InstanceViewStatus{
					{Code: strPtr("ProvisioningState/succeeded")},
					{Code: strPtr("PowerState/running")},
				},
			},
			expected: "running",
		},
		{
			name: "deallocated VM",
			instanceView: &armcompute.VirtualMachineInstanceView{
				Statuses: []*armcompute.InstanceViewStatus{
					{Code: strPtr("ProvisioningState/succeeded")},
					{Code: strPtr("PowerState/deallocated")},
				},
			},
			expected: "deallocated",
		},
		{
			name: "stopped VM",
			instanceView: &armcompute.VirtualMachineInstanceView{
				Statuses: []*armcompute.InstanceViewStatus{
					{Code: strPtr("ProvisioningState/succeeded")},
					{Code: strPtr("PowerState/stopped")},
				},
			},
			expected: "stopped",
		},
		{
			name: "no power state in statuses",
			instanceView: &armcompute.VirtualMachineInstanceView{
				Statuses: []*armcompute.InstanceViewStatus{
					{Code: strPtr("ProvisioningState/succeeded")},
				},
			},
			expected: "",
		},
		{
			name: "empty statuses",
			instanceView: &armcompute.VirtualMachineInstanceView{
				Statuses: []*armcompute.InstanceViewStatus{},
			},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractVMPowerState(tt.instanceView)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestVMPowerStateOverridesProvisioningState(t *testing.T) {
	// Simulates the logic in GetResources: provisioning state sets initial status,
	// then power state overrides it
	tests := []struct {
		name              string
		provisioningState string
		powerState        string
		expectedStatus    providers.ResourceStatus
	}{
		{
			name:              "succeeded + running = Active",
			provisioningState: "Succeeded",
			powerState:        "running",
			expectedStatus:    providers.ResourceStatusActive,
		},
		{
			name:              "succeeded + deallocated = Inactive",
			provisioningState: "Succeeded",
			powerState:        "deallocated",
			expectedStatus:    providers.ResourceStatusInactive,
		},
		{
			name:              "succeeded + stopped = Inactive",
			provisioningState: "Succeeded",
			powerState:        "stopped",
			expectedStatus:    providers.ResourceStatusInactive,
		},
		{
			name:              "succeeded + no power state = falls back to provisioning state",
			provisioningState: "Succeeded",
			powerState:        "",
			expectedStatus:    providers.ResourceStatusActive,
		},
		{
			name:              "failed + no power state = Inactive from provisioning",
			provisioningState: "Failed",
			powerState:        "",
			expectedStatus:    providers.ResourceStatusInactive,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Step 1: Set status from provisioning state
			status := providers.ResourceStatusUnknown
			if val, ok := nbStatusFromAzureProvisioningState[tt.provisioningState]; ok {
				status = val
			}

			// Step 2: Override with power state if available
			if ps := vmPowerStateToStatus(tt.powerState); ps != "" {
				status = ps
			}

			assert.Equal(t, tt.expectedStatus, status)
		})
	}
}
