package query

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// The app gateway authenticates a raw `sk-nb-…` Personal Access Token by
// filtering user_auth_by_username_v2 on token_sha256 (see
// app/src/lib/apiToken.ts). An unknown where-column is a hard error in
// generateWhereClause, so dropping this column from the table definition turns
// every direct-token API call into a 401 rather than into anything visible in
// this repo's own tests.
func TestUserAuthByUsernameSupportsTokenSha256Lookup(t *testing.T) {
	tableDef := table_metadata["user_auth_by_username_v2"]

	t.Run("token_sha256 is selectable from the derived table", func(t *testing.T) {
		_, ok := tableDef.Columns["token_sha256"]
		assert.True(t, ok, "token_sha256 must stay in Columns for the API-token lookup to resolve")
		assert.Contains(t, tableDef.Def, "ua.token_sha256 as token_sha256")
	})

	t.Run("the gateway's lookup filter generates", func(t *testing.T) {
		clause, err := generateWhereClause(QueryWhereClause{
			Binary: map[string]map[BinaryWhereClauseType]any{
				"provider":     {"_eq": "token"},
				"token_sha256": {"_eq": "0f9c1a"},
			},
		}, tableDef)
		assert.Nil(t, err)
		assert.Contains(t, clause, "token_sha256 = '0f9c1a'")
	})
}
