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

// Regression for issue #31091: the collector lowercases Azure resource IDs
// before storing them (account/etl_resources.go), so ApplyCommand resource IDs
// arrive lowercased. parseAzureResourceIDSegments must match segment keys
// case-insensitively, otherwise camelCase segments like "resourceGroups" and
// "flexibleServers" never resolve and the DB handlers reject the call with
// "invalid resource ID".
func TestParseAzureResourceIDSegments(t *testing.T) {
	tests := []struct {
		name         string
		resourceID   string
		serverKey    string
		wantRG       string
		wantServer   string
		wantDatabase string
	}{
		{
			name:         "lowercased SQL database ID (as stored)",
			resourceID:   "/subscriptions/sub-id/resourcegroups/my-rg/providers/microsoft.sql/servers/my-srv/databases/my-db",
			serverKey:    "servers",
			wantRG:       "my-rg",
			wantServer:   "my-srv",
			wantDatabase: "my-db",
		},
		{
			name:       "lowercased Postgres flexible server ID (as stored)",
			resourceID: "/subscriptions/sub-id/resourcegroups/my-rg/providers/microsoft.dbforpostgresql/flexibleservers/my-srv",
			serverKey:  "flexibleservers",
			wantRG:     "my-rg",
			wantServer: "my-srv",
		},
		{
			name:         "original camelCase ID still parses",
			resourceID:   "/subscriptions/sub-id/resourceGroups/my-rg/providers/Microsoft.Sql/servers/my-srv/databases/my-db",
			serverKey:    "servers",
			wantRG:       "my-rg",
			wantServer:   "my-srv",
			wantDatabase: "my-db",
		},
		{
			// A resource named like a well-known type must not pollute the
			// keyspace: the rg here is literally named "databases", and this is a
			// server-only ID, so the databases lookup must stay empty rather than
			// pick up the segment following the rg name.
			name:       "resource named like a type does not clobber lookups",
			resourceID: "/subscriptions/sub-id/resourcegroups/databases/providers/microsoft.sql/servers/my-srv",
			serverKey:  "servers",
			wantRG:     "databases",
			wantServer: "my-srv",
			// wantDatabase intentionally empty (server-only ID).
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			seg := parseAzureResourceIDSegments(tt.resourceID)
			assert.Equal(t, tt.wantRG, seg["resourcegroups"])
			assert.Equal(t, tt.wantServer, seg[tt.serverKey])
			assert.Equal(t, tt.wantDatabase, seg["databases"])
		})
	}

	// A server-only ID has no databases segment, and an empty ID resolves
	// nothing — both must leave the lookups empty so handlers still reject them.
	seg := parseAzureResourceIDSegments("/subscriptions/sub-id/resourcegroups/my-rg/providers/microsoft.sql/servers/my-srv")
	assert.Empty(t, seg["databases"])
	assert.Empty(t, parseAzureResourceIDSegments(""))
}
