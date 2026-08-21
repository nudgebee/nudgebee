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

// newTestRecord builds a sys_user row the way the SDK models it. TableRecord is a
// struct rather than the plain map TableEntry used to be, so fields go in via
// SetValue instead of a composite literal.
func newTestRecord(t *testing.T, fields map[string]any) *tableapi.TableRecord {
	t.Helper()
	rec := tableapi.NewTableRecord()
	for k, v := range fields {
		assert.NoError(t, rec.SetValue(k, v))
	}
	return rec
}

func TestMapServiceNowUser(t *testing.T) {
	tests := []struct {
		name string
		row  map[string]any
		want core.ExternalUser
	}{
		{
			name: "full row auto-matches by email",
			row:  map[string]any{"sys_id": "abc123", "user_name": "jdoe", "email": "jdoe@example.com", "name": "John Doe"},
			want: core.ExternalUser{ID: "abc123", Username: "jdoe", Email: "jdoe@example.com", DisplayName: "John Doe"},
		},
		{
			name: "no email stays login-only",
			row:  map[string]any{"sys_id": "svc1", "user_name": "integration.user", "email": "", "name": "Integration User"},
			want: core.ExternalUser{ID: "svc1", Username: "integration.user", Email: "", DisplayName: "Integration User"},
		},
		{
			name: "trims whitespace and tolerates missing fields",
			row:  map[string]any{"sys_id": "  x9 ", "email": " a@b.com "},
			want: core.ExternalUser{ID: "x9", Username: "", Email: "a@b.com", DisplayName: ""},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, mapServiceNowUser(newTestRecord(t, tt.row)))
		})
	}
}

// A nil record must not panic — ListUsers skips nils, but the mapper is exported
// to the package and should stay total.
func TestMapServiceNowUser_NilRecord(t *testing.T) {
	assert.Equal(t, core.ExternalUser{}, mapServiceNowUser(nil))
}

func TestNormalizeServiceNowURL(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		// A bare host used to work, because the old client prefixed "https://"
		// itself. The SDK's url.ParseRequestURI rejects it, so keep accepting it.
		{"bare host gets https", "acme.service-now.com", "https://acme.service-now.com"},
		{"already fully qualified", "https://acme.service-now.com", "https://acme.service-now.com"},
		// "{+baseurl}/api/now" is a reserved expansion, so a trailing slash would
		// otherwise produce "//api/now".
		{"trailing slash removed", "https://acme.service-now.com/", "https://acme.service-now.com"},
		{"several trailing slashes removed", "https://acme.service-now.com///", "https://acme.service-now.com"},
		{"surrounding whitespace removed", "  https://acme.service-now.com/  ", "https://acme.service-now.com"},
		{"explicit http preserved", "http://acme.service-now.com", "http://acme.service-now.com"},
		{"port preserved", "https://acme.service-now.com:8443/", "https://acme.service-now.com:8443"},
		{"subpath kept, trailing slash dropped", "https://acme.service-now.com/nav/", "https://acme.service-now.com/nav"},
		{"empty stays empty", "", ""},
		{"whitespace only stays empty", "   ", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, normalizeServiceNowURL(tt.in))
		})
	}
}
