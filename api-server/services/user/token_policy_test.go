package user

import (
	"database/sql"
	"database/sql/driver"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizeKind(t *testing.T) {
	// Omitted kind mints an all key: older clients of users_create_token send no kind,
	// and a PAT has always opened both doors. Narrowing is the feature flag's job.
	k, err := normalizeKind("")
	assert.Nil(t, err)
	assert.Equal(t, "all", k)

	k, err = normalizeKind("gateway")
	assert.Nil(t, err)
	assert.Equal(t, "gateway", k)

	k, err = normalizeKind("platform")
	assert.Nil(t, err)
	assert.Equal(t, "platform", k)

	k, err = normalizeKind("all")
	assert.Nil(t, err)
	assert.Equal(t, "all", k)

	_, err = normalizeKind("admin")
	assert.NotNil(t, err)
}

func TestCarriesGatewayTraffic(t *testing.T) {
	assert.True(t, carriesGatewayTraffic(KindGateway))
	assert.True(t, carriesGatewayTraffic(KindAll))
	assert.False(t, carriesGatewayTraffic(KindPlatform))
	assert.False(t, carriesGatewayTraffic(""))
}

func TestValidateLimitsForKind(t *testing.T) {
	limits := []TokenLimit{{Metric: "cost", Period: "month", Value: 20}}

	// A platform key carries expiry and nothing else (D8).
	err := validateLimitsForKind("platform", limits)
	assert.NotNil(t, err)
	assert.Contains(t, err.Error(), "platform")

	// A kind that never reached normalizeKind rejects limits rather than storing them.
	err = validateLimitsForKind("something-else", limits)
	assert.NotNil(t, err)

	assert.Nil(t, validateLimitsForKind("platform", nil))
	assert.Nil(t, validateLimitsForKind("gateway", limits))

	// An all-access key reaches the gateway, so its quotas are meaningful and accepted.
	assert.Nil(t, validateLimitsForKind("all", limits))
	assert.NotNil(t, validateLimitsForKind("all", []TokenLimit{{Metric: "gpus", Period: "month", Value: 1}}))

	// Unknown metric or period is rejected rather than stored and ignored.
	assert.NotNil(t, validateLimitsForKind("gateway", []TokenLimit{{Metric: "gpus", Period: "month", Value: 1}}))
	assert.NotNil(t, validateLimitsForKind("gateway", []TokenLimit{{Metric: "cost", Period: "fortnight", Value: 1}}))
	assert.NotNil(t, validateLimitsForKind("gateway", []TokenLimit{{Metric: "cost", Period: "month", Value: 0}}))

	// Same (metric, period, model) twice would double-count one counter; a different model is fine.
	err = validateLimitsForKind("gateway", []TokenLimit{
		{Metric: "requests", Period: "minute", Value: 5},
		{Metric: "requests", Period: "minute", Value: 10},
	})
	assert.NotNil(t, err)
	assert.Contains(t, err.Error(), "duplicate")
	assert.Nil(t, validateLimitsForKind("gateway", []TokenLimit{
		{Metric: "requests", Period: "minute", Value: 5},
		{Metric: "requests", Period: "minute", Value: 5, Model: "gpt-5"},
	}))
}

func TestDeleteRequestNeedsAnIdentifier(t *testing.T) {
	assert.NotNil(t, validateDeleteRequest(UserTokenDeleteRequest{}))
	assert.Nil(t, validateDeleteRequest(UserTokenDeleteRequest{Id: "key-uuid-1"}))
	assert.Nil(t, validateDeleteRequest(UserTokenDeleteRequest{Name: "laptop"}))
}

func TestResolveDeleteID(t *testing.T) {
	// No match: not found.
	_, err := resolveDeleteID(nil)
	assert.NotNil(t, err)

	// Exactly one match: that id.
	id, err := resolveDeleteID([]string{"key-uuid-1"})
	assert.Nil(t, err)
	assert.Equal(t, "key-uuid-1", id)

	// A name shared across kinds is ambiguous — must not guess which one was meant.
	_, err = resolveDeleteID([]string{"key-uuid-1", "key-uuid-2"})
	assert.NotNil(t, err)
	assert.Contains(t, err.Error(), "ambiguous")
}

func TestValidateLimitsAgainstCeilings(t *testing.T) {
	ceilings := []TokenLimit{
		{Metric: "cost", Period: "month", Value: 100},
	}

	// Below the ceiling passes.
	assert.Nil(t, validateLimitsAgainstCeilings(ceilings, []TokenLimit{{Metric: "cost", Period: "month", Value: 50}}))

	// Exactly at the ceiling passes.
	assert.Nil(t, validateLimitsAgainstCeilings(ceilings, []TokenLimit{{Metric: "cost", Period: "month", Value: 100}}))

	// Above the ceiling is rejected.
	err := validateLimitsAgainstCeilings(ceilings, []TokenLimit{{Metric: "cost", Period: "month", Value: 150}})
	assert.NotNil(t, err)
	assert.Contains(t, err.Error(), "ceiling")

	// A metric+period with no configured ceiling passes through untouched.
	assert.Nil(t, validateLimitsAgainstCeilings(ceilings, []TokenLimit{{Metric: "requests", Period: "minute", Value: 1000000}}))
}

// TestLongestTokenFitsBcryptLimit guards the bcrypt 72-byte input limit: HashPassword
// hashes the whole prefixed token, so prefix + hex-encoded random must never exceed 72
// bytes for any prefix in use. A future longer prefix (or a bumped TokenRandomBytes)
// should fail this test rather than fail bcrypt.GenerateFromPassword in production.
func TestLongestTokenFitsBcryptLimit(t *testing.T) {
	const bcryptMaxBytes = 72
	randomChars := TokenRandomBytes * 2 // hex-encoded

	for _, prefix := range []string{TokenPrefix, GatewayTokenPrefix} {
		length := len(prefix) + randomChars
		assert.LessOrEqualf(t, length, bcryptMaxBytes,
			"token with prefix %q is %d bytes, exceeds bcrypt's %d-byte limit", prefix, length, bcryptMaxBytes)
	}
}

func TestTightestCeiling(t *testing.T) {
	ceilings := []TokenLimit{
		{Metric: "cost", Period: "month", Value: 500},
		{Metric: "cost", Period: "month", Value: 50},
		{Metric: "tokens", Period: "day", Value: 1000},
	}
	v, ok := tightestCeiling(ceilings, "cost", "month")
	assert.True(t, ok)
	assert.Equal(t, 50.0, v)

	_, ok = tightestCeiling(ceilings, "requests", "minute")
	assert.False(t, ok)
}

func TestNormalizeScopes(t *testing.T) {
	// Omitted scopes is full access on any kind — the column stays NULL.
	s, err := normalizeScopes(KindPlatform, nil)
	assert.Nil(t, err)
	assert.Nil(t, s)
	s, err = normalizeScopes(KindAll, nil)
	assert.Nil(t, err)
	assert.Nil(t, s)

	// An explicit empty list must not silently become full access.
	for _, kind := range []string{KindPlatform, KindAll, KindGateway} {
		_, err = normalizeScopes(kind, []string{})
		assert.NotNil(t, err, kind)
	}

	s, err = normalizeScopes(KindPlatform, []string{"tickets:Read", "workflows:Write", "tickets:Read", "ai_tools:Execute"})
	assert.Nil(t, err)
	assert.Equal(t, []string{"tickets:Read", "workflows:Write", "ai_tools:Execute"}, s)
}

func TestNormalizeScopesKinds(t *testing.T) {
	// The AI Gateway never reads scopes, so on a gateway key they would read as a
	// restriction that is not applied. On an all key they bind its app-API side.
	_, err := normalizeScopes(KindGateway, []string{"tickets:Read"})
	assert.NotNil(t, err)
	s, err := normalizeScopes(KindAll, []string{"tickets:Read"})
	assert.Nil(t, err)
	assert.Equal(t, []string{"tickets:Read"}, s)
}

func TestNormalizeScopesRejectsMalformed(t *testing.T) {
	for _, bad := range []string{"tickets", "tickets:read", "tickets:Admin", ":Read", "Tickets:Read", "scoped:tickets:Read", "tickets:Read ", ""} {
		_, err := normalizeScopes(KindPlatform, []string{bad})
		assert.NotNil(t, err, bad)
	}
	tooMany := make([]string, maxScopes+1)
	for i := range tooMany {
		tooMany[i] = "tickets:Read"
	}
	_, err := normalizeScopes(KindPlatform, tooMany)
	assert.NotNil(t, err)
}

func TestScopesColumn(t *testing.T) {
	// Empty must be NULL: an empty array would read as a key that may call nothing.
	assert.Nil(t, scopesColumn(nil))
	assert.Nil(t, scopesColumn([]string{}))
	assert.NotNil(t, scopesColumn([]string{"tickets:Read"}))
}

func TestNormalizeGatewayPolicy(t *testing.T) {
	// Omitted, or present with no lists, is unrestricted on any kind: the columns stay NULL.
	for _, kind := range []string{KindGateway, KindAll, KindPlatform} {
		p, err := normalizeGatewayPolicy(kind, false, nil)
		assert.Nil(t, err)
		assert.Nil(t, p)
		p, err = normalizeGatewayPolicy(kind, false, &GatewayPolicy{})
		assert.Nil(t, err, kind)
		assert.Nil(t, p, kind)
	}

	p, err := normalizeGatewayPolicy(KindGateway, false, &GatewayPolicy{
		Models:    []string{" gpt-4o-mini ", "anthropic/claude-haiku-4-5", "GPT-4o-mini"},
		Endpoints: []string{"chat", "chat"},
	})
	assert.Nil(t, err)
	assert.Nil(t, p.Providers, "an omitted dimension stays unrestricted")
	assert.Equal(t, []string{"gpt-4o-mini", "anthropic/claude-haiku-4-5"}, p.Models)
	assert.Equal(t, []string{"chat"}, p.Endpoints)
}

func TestNormalizeGatewayPolicyKinds(t *testing.T) {
	policy := &GatewayPolicy{Models: []string{"gpt-4o-mini"}}
	_, err := normalizeGatewayPolicy(KindPlatform, true, policy)
	assert.NotNil(t, err, "a platform key never reaches the gateway")
	// Unscoped, an all key's platform side could mint an unrestricted key.
	_, err = normalizeGatewayPolicy(KindAll, false, policy)
	assert.NotNil(t, err)
	p, err := normalizeGatewayPolicy(KindAll, true, policy)
	assert.Nil(t, err)
	assert.Equal(t, []string{"gpt-4o-mini"}, p.Models)
}

func TestNormalizeGatewayPolicyRejectsMalformed(t *testing.T) {
	bad := []GatewayPolicy{
		{Models: []string{}},
		{Providers: []string{}},
		{Endpoints: []string{}},
		{Endpoints: []string{"realtime"}},
		{Endpoints: []string{"Chat"}},
		{Providers: []string{"Open AI"}},
		{Models: []string{""}},
		{Models: []string{"gpt 4"}},
		{Models: []string{"gpt-4\x00o"}},
		{Models: []string{"gpt-4\u00a0o"}},
	}
	for _, p := range bad {
		_, err := normalizeGatewayPolicy(KindGateway, false, &p)
		assert.NotNil(t, err, "%+v", p)
	}
	tooMany := make([]string, maxPolicyEntries+1)
	for i := range tooMany {
		tooMany[i] = "m"
	}
	_, err := normalizeGatewayPolicy(KindGateway, false, &GatewayPolicy{Models: tooMany})
	assert.NotNil(t, err)
}

func TestGatewayPolicyRowsRoundTrip(t *testing.T) {
	assert.Nil(t, policyEntries(nil))
	assert.Empty(t, gatewayPoliciesByKey(nil))

	p := &GatewayPolicy{Models: []string{"gpt-4o-mini", "anthropic/claude-haiku-4-5"}, Endpoints: []string{"chat"}}
	rows := policyEntries(p)
	require.Len(t, rows, 3)
	for i := range rows {
		rows[i].APIKeyID = "k1"
	}
	got := gatewayPoliciesByKey(rows)
	assert.Equal(t, p, got["k1"])
	assert.Nil(t, got["k1"].Providers, "a dimension with no rows stays unrestricted")
	assert.Nil(t, got["k2"], "a key with no rows is unrestricted")
}

// Every statement is scoped to the one key, by key and owner as the gateway matches rows: a
// row with an empty api_key_id would apply to every key of the user (#38963), so a delete or
// insert outside the key's scope could silently widen or narrow other keys.
func TestReplaceKeyGatewayPolicy_ScopesEveryStatementToTheKey(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM llm_gateway_access_policies WHERE api_key_id = $1 AND user_id = $2`)).
		WithArgs("k1", "u1").WillReturnResult(sqlmock.NewResult(0, 2))
	insert := regexp.QuoteMeta(`INSERT INTO llm_gateway_access_policies (tenant_id, user_id, api_key_id, dimension, value) VALUES ($1, $2, $3, $4, $5)`)
	mock.ExpectExec(insert).WithArgs("t1", "u1", "k1", "model", "gpt-4o-mini").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(insert).WithArgs("t1", "u1", "k1", "endpoint", "chat").WillReturnResult(sqlmock.NewResult(0, 1))

	tx, err := db.Begin()
	require.NoError(t, err)
	key := keyScope{KeyID: "k1", UserID: "u1", TenantID: "t1"}
	require.NoError(t, replaceKeyGatewayPolicy(tx, key, &GatewayPolicy{Models: []string{"gpt-4o-mini"}, Endpoints: []string{"chat"}}))
	assert.NoError(t, mock.ExpectationsWereMet())
}

// Clearing a key (an update to unrestricted, or revoking the key) deletes its rows and
// writes none.
func TestReplaceKeyGatewayPolicy_NilClearsTheKey(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM llm_gateway_access_policies`)).
		WithArgs("k1", "u1").WillReturnResult(sqlmock.NewResult(0, 3))

	tx, err := db.Begin()
	require.NoError(t, err)
	require.NoError(t, replaceKeyGatewayPolicy(tx, keyScope{KeyID: "k1", UserID: "u1", TenantID: "t1"}, nil))
	assert.NoError(t, mock.ExpectationsWereMet())
}

// countingExecer accepts every statement, so only the guard can make a blank-scope write fail.
type countingExecer struct{ statements int }

func (c *countingExecer) Exec(string, ...any) (sql.Result, error) {
	c.statements++
	return driver.RowsAffected(0), nil
}

func TestReplaceKeyGatewayPolicy_RefusesABlankScope(t *testing.T) {
	exec := &countingExecer{}
	for _, k := range []keyScope{{UserID: "u1", TenantID: "t1"}, {KeyID: "k1", TenantID: "t1"}, {KeyID: "k1", UserID: "u1"}} {
		err := replaceKeyGatewayPolicy(exec, k, &GatewayPolicy{Models: []string{"gpt-4o-mini"}})
		assert.EqualError(t, err, "gateway policy write needs a key, user and tenant id", "%+v", k)
	}
	assert.Zero(t, exec.statements, "no statement may run without a full scope")
}
