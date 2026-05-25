package azure

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestExtractResourceGroup(t *testing.T) {
	tests := []struct {
		name    string
		id      string
		want    string
		wantErr bool
	}{
		{
			name: "camelCase resourceGroups",
			id:   "/subscriptions/sub1/resourceGroups/my-rg/providers/Microsoft.Sql/servers/srv1",
			want: "my-rg",
		},
		{
			name: "lowercase resourcegroups",
			id:   "/subscriptions/sub1/resourcegroups/my-rg/providers/microsoft.operationalinsights/workspaces/ws1",
			want: "my-rg",
		},
		{
			name: "uppercase RESOURCEGROUPS",
			id:   "/subscriptions/sub1/RESOURCEGROUPS/my-rg/providers/Microsoft.Compute/virtualMachines/vm1",
			want: "my-rg",
		},
		{
			name:    "missing resourceGroups segment",
			id:      "/subscriptions/sub1/providers/Microsoft.Sql/servers/srv1",
			wantErr: true,
		},
		{
			name:    "empty string",
			id:      "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := extractResourceGroup(tt.id)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.want, got)
			}
		})
	}
}
