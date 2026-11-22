package account

import (
	"github.com/stretchr/testify/require"
	"nudgebee/services/security"
	"testing"
)

func TestNudgebeeWorkloadsAttribute(t *testing.T) {
	admin := security.NewSecurityContextForSuperAdmin()
	denied := security.NewSecurityContextForAuditRelay("tenant", "user")
	attr := func(value string) AccountAttr {
		return AccountAttr{CloudAccountId: "account", Name: "nudgebee_workloads", Value: value}
	}
	for _, value := range []string{
		`{}`,
		`{"hide_in_notifications":false}`,
		`{"hide_in_notifications":true,"namespaces":["nudgebee","acme-nudgebee"]}`,
		`{"namespaces":null}`,
	} {
		require.NoError(t, validateNudgebeeWorkloadsAttribute(admin, attr(value)), value)
		require.Error(t, validateNudgebeeWorkloadsAttribute(denied, attr(value)), value)
	}
	for _, value := range []string{
		``,
		`not json`,
		`[]`,
		`"nudgebee"`,
		`{"hide_in_notifications":"no"}`,
		`{"namespaces":"nudgebee"}`,
		`{"namespaces":["Nudgebee"]}`,
		`{"namespaces":[""]}`,
		`{"unknown_field":true}`,
		`{} x`,
		`{} }`,
		"{}\n[1",
	} {
		require.ErrorContains(t, validateNudgebeeWorkloadsAttribute(admin, attr(value)), "invalid nudgebee_workloads", value)
	}
	require.NoError(t, validateNudgebeeWorkloadsAttribute(denied, AccountAttr{Name: "unrelated", Value: "anything"}))
	require.Error(t, validateNudgebeeWorkloadsAttribute(nil, attr(`{}`)))
}
