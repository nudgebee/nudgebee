package integrations

import (
	"testing"

	tableapi "github.com/michaeldcanady/servicenow-sdk-go/table-api"
	"nudgebee/services/integrations/core"

	"github.com/stretchr/testify/assert"
)

// ServiceNow must be discoverable as a core.UserLister so identity sync pulls its users.
func TestServiceNow_ImplementsUserLister(t *testing.T) {
	var _ core.UserLister = ServiceNow{}

	intg, found := core.GetIntegration(IntegrationServiceNow)
	assert.True(t, found, "servicenow must be registered")
	_, ok := intg.(core.UserLister)
	assert.True(t, ok, "servicenow must implement core.UserLister")
}

func TestMapServiceNowUser(t *testing.T) {
	tests := []struct {
		name string
		row  tableapi.TableEntry
		want core.ExternalUser
	}{
		{
			name: "full row auto-matches by email",
			row:  tableapi.TableEntry{"sys_id": "abc123", "user_name": "jdoe", "email": "jdoe@example.com", "name": "John Doe"},
			want: core.ExternalUser{ID: "abc123", Username: "jdoe", Email: "jdoe@example.com", DisplayName: "John Doe"},
		},
		{
			name: "no email stays login-only",
			row:  tableapi.TableEntry{"sys_id": "svc1", "user_name": "integration.user", "email": "", "name": "Integration User"},
			want: core.ExternalUser{ID: "svc1", Username: "integration.user", Email: "", DisplayName: "Integration User"},
		},
		{
			name: "trims whitespace and tolerates missing fields",
			row:  tableapi.TableEntry{"sys_id": "  x9 ", "email": " a@b.com "},
			want: core.ExternalUser{ID: "x9", Username: "", Email: "a@b.com", DisplayName: ""},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			row := tt.row
			assert.Equal(t, tt.want, mapServiceNowUser(&row))
		})
	}
}
