package account

import (
	"context"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/require"
	"nudgebee/services/security"
)

func TestUpsertAccountAttrsRejectsAccountOutsideRoleScope(t *testing.T) {
	for _, tc := range []struct {
		name string
		wire string
	}{
		{
			name: "account admin cannot write another account",
			wire: `{"TenantId":"tenant","AccountIds":["a","b"],"Roles":["account_admin"],"ScopedEntityIds":{"account_admin":["a"]}}`,
		},
		{
			name: "namespace admin cannot write account attributes",
			wire: `{"TenantId":"tenant","AccountIds":["a","b"],"Roles":["k8s_namespace_admin"],"ScopedEntityIds":{"k8s_namespace_admin":["b"]}}`,
		},
		{
			name: "custom cloud write does not grant account access",
			wire: `{"TenantId":"tenant","AccountIds":["a","b"],"Roles":[],"CustomPermissions":{"cloud:Write":true}}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var sc security.SecurityContext
			require.NoError(t, json.Unmarshal([]byte(tc.wire), &sc))
			ctx := security.NewRequestContext(context.Background(), &sc, slog.Default(), nil, nil)

			_, err := UpsertAccountAttrs(ctx, AccountAttrUpsertRequest{Objects: []AccountAttr{{
				CloudAccountId: "b",
				Name:           "log_labels",
				Value:          "[]",
			}}})
			require.ErrorContains(t, err, "Not Allowed")
		})
	}
}

func TestCanWriteAccountAttrsRequiresAccountScopeAndWriterRole(t *testing.T) {
	var scopedAdmin security.SecurityContext
	require.NoError(t, json.Unmarshal([]byte(`{"TenantId":"tenant","AccountIds":["a","b"],"Roles":["account_admin"],"ScopedEntityIds":{"account_admin":["a"]}}`), &scopedAdmin))
	var tenantAdmin security.SecurityContext
	require.NoError(t, json.Unmarshal([]byte(`{"TenantId":"tenant","AccountIds":["a","b"],"Roles":["tenant_admin"]}`), &tenantAdmin))

	tests := []struct {
		name      string
		security  *security.SecurityContext
		accountId string
		want      bool
	}{
		{name: "scoped account admin", security: &scopedAdmin, accountId: "a", want: true},
		{name: "account admin cannot cross accounts", security: &scopedAdmin, accountId: "b", want: false},
		{name: "tenant admin", security: &tenantAdmin, accountId: "b", want: true},
		{name: "empty account id", security: security.NewSecurityContextForSuperAdmin(), accountId: "", want: false},
		{name: "server super admin", security: security.NewSecurityContextForSuperAdmin(), accountId: "b", want: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, canWriteAccountAttrs(tc.security, tc.accountId))
		})
	}
}

func TestUpsertAccountAttrsRejectsMixedScopeBatch(t *testing.T) {
	var sc security.SecurityContext
	require.NoError(t, json.Unmarshal([]byte(`{"TenantId":"tenant","AccountIds":["a","b"],"Roles":["account_admin"],"ScopedEntityIds":{"account_admin":["a"]}}`), &sc))
	ctx := security.NewRequestContext(context.Background(), &sc, slog.Default(), nil, nil)
	_, err := UpsertAccountAttrs(ctx, AccountAttrUpsertRequest{Objects: []AccountAttr{
		{CloudAccountId: "a", Name: "log_labels", Value: "[]"},
		{CloudAccountId: "b", Name: "log_labels", Value: "[]"},
	}})
	require.ErrorContains(t, err, "Not Allowed")
}
