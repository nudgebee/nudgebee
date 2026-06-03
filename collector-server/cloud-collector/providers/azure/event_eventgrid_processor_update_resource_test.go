package azure

import "testing"

func TestDeriveAzureResourceNameAndType(t *testing.T) {
	tests := []struct {
		name       string
		resourceId string
		wantName   string
		wantType   string
	}{
		{
			name:       "managed instance",
			resourceId: "/subscriptions/abc/resourcegroups/rg1/providers/Microsoft.Sql/managedInstances/free-sql-mi-test",
			wantName:   "free-sql-mi-test",
			wantType:   "Microsoft.Sql/managedInstances",
		},
		{
			name:       "managed instance database (sub-resource)",
			resourceId: "/subscriptions/abc/resourceGroups/rg1/providers/Microsoft.Sql/managedInstances/mi1/databases/db1",
			wantName:   "db1",
			wantType:   "Microsoft.Sql/managedInstances/databases",
		},
		{
			name:       "vnet subnet contextual policy (deeply nested)",
			resourceId: "/subscriptions/abc/resourceGroups/rg1/providers/Microsoft.Network/virtualNetworks/vnet1/subnets/sub1/contextualServiceEndpointPolicies/policy1",
			wantName:   "policy1",
			wantType:   "Microsoft.Network/virtualNetworks/subnets/contextualServiceEndpointPolicies",
		},
		{
			name:       "lowercase /providers/ should still match",
			resourceId: "/subscriptions/abc/resourcegroups/rg1/providers/microsoft.compute/virtualMachines/vm1",
			wantName:   "vm1",
			wantType:   "microsoft.compute/virtualMachines",
		},
		{
			name:       "no providers segment",
			resourceId: "/subscriptions/abc/resourcegroups/rg1",
			wantName:   "",
			wantType:   "",
		},
		{
			name:       "empty",
			resourceId: "",
			wantName:   "",
			wantType:   "",
		},
		{
			name:       "trailing slash imbalance (odd segments after provider)",
			resourceId: "/subscriptions/abc/resourceGroups/rg1/providers/Microsoft.Sql/servers/srv1/databases",
			wantName:   "",
			wantType:   "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotName, gotType := deriveAzureResourceNameAndType(tc.resourceId)
			if gotName != tc.wantName {
				t.Errorf("name: got %q want %q", gotName, tc.wantName)
			}
			if gotType != tc.wantType {
				t.Errorf("type: got %q want %q", gotType, tc.wantType)
			}
		})
	}
}
