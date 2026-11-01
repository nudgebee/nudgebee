package account

import (
	"github.com/stretchr/testify/require"
	"nudgebee/services/security"
	"testing"
)

func TestEventAnalysisAttribute(t *testing.T) {
	admin := security.NewSecurityContextForSuperAdmin()
	denied := security.NewSecurityContextForAuditRelay("tenant", "user")
	attr := func(value string) AccountAttr {
		return AccountAttr{CloudAccountId: "account", Name: "event_analysis", Value: value}
	}
	for _, value := range []string{
		`{}`,
		`{"reuse_related":false}`,
		`{"reuse_window_hours":1,"always_fresh_namespaces":["demo"],"excluded_namespaces":["kube-system","nudgebee-agent"]}`,
		`{"reuse_window_hours":720,"always_fresh_namespaces":[],"excluded_namespaces":null}`,
	} {
		require.NoError(t, validateEventAnalysisAttribute(admin, attr(value)), value)
		require.Error(t, validateEventAnalysisAttribute(denied, attr(value)), value)
	}
	for _, value := range []string{
		``,
		`not json`,
		`{"reuse_window_hours":0}`,
		`{"reuse_window_hours":721}`,
		`{"reuse_window_hours":"24"}`,
		`{"always_fresh_namespaces":["Demo"]}`,
		`{"excluded_namespaces":[""]}`,
		`{"excluded_namespaces":["bad_name"]}`,
		`{"unknown_field":true}`,
	} {
		require.ErrorContains(t, validateEventAnalysisAttribute(admin, attr(value)), "invalid event_analysis", value)
	}
	require.NoError(t, validateEventAnalysisAttribute(denied, AccountAttr{Name: "unrelated", Value: "anything"}))
	require.Error(t, validateEventAnalysisAttribute(nil, attr(`{}`)))
}
