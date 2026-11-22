package query

import (
	"context"
	"encoding/json"
	"log/slog"
	"testing"
	"time"

	"nudgebee/services/security"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// cloud_metric_groupings_v2 reads one account's cloud metrics. Built-in account
// roles read it as before; a cloud:Read custom grant now does too; a grant for
// another module, or none, is still refused.
func TestAgentMetricsHonorsTheTablesModuleGrant(t *testing.T) {
	const account = "11111111-1111-1111-1111-111111111111"
	cases := []struct {
		name    string
		grants  string
		allowed bool
	}{
		{"cloud:Read grant", `{"cloud:Read":true}`, true},
		{"cloud:Write grant implies read", `{"cloud:Write":true}`, true},
		{"grant for another module", `{"k8s:Read":true}`, false},
		{"no grant", `{}`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var sc security.SecurityContext
			require.NoError(t, json.Unmarshal([]byte(`{"TenantId":"tenant-1","UserId":"user-1","AccountIds":["`+account+`"],"CustomPermissions":`+tc.grants+`}`), &sc))
			ctx := security.NewRequestContext(context.Background(), &sc, slog.Default(), nil, nil)

			resp, _ := processAgentMetrices(QueryRequest{
				Table: "cloud_metric_groupings_v2",
				Where: QueryWhereClause{Binary: map[string]map[BinaryWhereClauseType]any{"account_id": {Eq: account}}},
			}, ctx, time.Now(), "cloud")

			denied := len(resp.Errors) > 0 && resp.Errors[0].Message == "Access Denied"
			assert.Equal(t, !tc.allowed, denied, "errors: %v", resp.Errors)
		})
	}
}

// A malformed account id or a missing security context is refused, not a panic.
func TestAgentMetricsFailsClosedOnBadInput(t *testing.T) {
	var sc security.SecurityContext
	require.NoError(t, json.Unmarshal([]byte(`{"TenantId":"tenant-1","UserId":"user-1","Roles":["tenant_admin"]}`), &sc))
	cases := []struct {
		name    string
		ctx     *security.RequestContext
		account any
	}{
		{"non-string account id", security.NewRequestContext(context.Background(), &sc, slog.Default(), nil, nil), 42},
		{"nil request context", nil, "11111111-1111-1111-1111-111111111111"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, _ := processAgentMetrices(QueryRequest{
				Table: "cloud_metric_groupings_v2",
				Where: QueryWhereClause{Binary: map[string]map[BinaryWhereClauseType]any{"account_id": {Eq: tc.account}}},
			}, tc.ctx, time.Now(), "cloud")
			if assert.NotEmpty(t, resp.Errors) {
				assert.Equal(t, "Access Denied", resp.Errors[0].Message)
			}
		})
	}
}
