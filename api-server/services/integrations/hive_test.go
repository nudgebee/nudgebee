package integrations

import (
	"strings"
	"testing"

	"nudgebee/services/integrations/core"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// listHiveColumns has four pre-Thrift bailout paths. We can exercise all of
// them without a HiveServer2 connection. The Thrift path itself is covered
// by the hiveclient package's own tests and integration tests against a
// running HiveServer2 (out of scope here since the cluster doesn't deploy
// HiveServer2 yet).

func TestListHiveColumns_EmptyUrlPromptsForUrlAndTable(t *testing.T) {
	res, err := listHiveColumns(nil, map[string]any{
		"hive_table": "k8s_logs",
	})
	require.NoError(t, err)
	assert.Empty(t, res.Options)
	assert.Contains(t, res.Message, "hive_url")
}

func TestListHiveColumns_EmptyTablePromptsForUrlAndTable(t *testing.T) {
	res, err := listHiveColumns(nil, map[string]any{
		"hive_url": "hiveserver2.hive.svc.cluster.local:10000",
	})
	require.NoError(t, err)
	assert.Empty(t, res.Options)
	assert.Contains(t, res.Message, "hive_table")
}

func TestListHiveColumns_LdapWithoutPasswordPromptsForPassword(t *testing.T) {
	res, err := listHiveColumns(nil, map[string]any{
		"hive_url":      "hiveserver2.hive.svc.cluster.local:10000",
		"hive_table":    "k8s_logs",
		"hive_database": "default",
		"auth_type":     "ldap",
		"username":      "alice",
		// password intentionally omitted — common when editing an existing integration.
	})
	require.NoError(t, err, "missing-password is a soft fail, not an error")
	assert.Empty(t, res.Options)
	assert.Contains(t, strings.ToLower(res.Message), "password")
}

func TestListHiveColumns_InvalidUrlReturnsError(t *testing.T) {
	_, err := listHiveColumns(nil, map[string]any{
		"hive_url":   "not-a-host-port",
		"hive_table": "k8s_logs",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "hive_url")
}

func TestListHiveColumns_NoneAuthSkipsPasswordRequirement(t *testing.T) {
	// auth_type=none means we don't gate on password — instead we'd proceed
	// to Connect (which will fail because there's no HiveServer2 on localhost).
	// We assert the function gets PAST the bailout and into Connect (which
	// produces a "connect:" error string), proving the password check is
	// only enforced for LDAP.
	_, err := listHiveColumns(nil, map[string]any{
		"hive_url":   "127.0.0.1:1", // unreachable port; we just want past the bailouts
		"hive_table": "k8s_logs",
		"auth_type":  "none",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "connect:", "should have proceeded past bailouts to Connect")
}

func TestListHiveColumns_DefaultDatabaseAppliedWhenEmpty(t *testing.T) {
	// When hive_database is empty, the handler should use "default". We
	// can't observe this through the public return values (the bailout for
	// unreachable server fires first), but we can at least confirm the
	// function doesn't bail out early just because database is empty.
	_, err := listHiveColumns(nil, map[string]any{
		"hive_url":   "127.0.0.1:1",
		"hive_table": "k8s_logs",
		// hive_database omitted
		"auth_type": "none",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "connect:")
}

func TestBoolFromForm(t *testing.T) {
	form := map[string]any{
		"native_true":  true,
		"native_false": false,
		"string_true":  "true",
		"string_True":  "True ",
		"string_false": "false",
		"string_other": "yes",
		"non_bool":     42,
	}
	assert.True(t, boolFromForm(form, "native_true"))
	assert.False(t, boolFromForm(form, "native_false"))
	assert.True(t, boolFromForm(form, "string_true"))
	assert.True(t, boolFromForm(form, "string_True"), "case-insensitive + trimmed")
	assert.False(t, boolFromForm(form, "string_false"))
	assert.False(t, boolFromForm(form, "string_other"))
	assert.False(t, boolFromForm(form, "non_bool"))
	assert.False(t, boolFromForm(form, "missing"))
	assert.False(t, boolFromForm(nil, "any"))
}

// ValidateConfig early-exit paths — required-field checks that don't open a
// Thrift connection. The connectivity probe paths are exercised indirectly
// via the integration tests against a real HiveServer2; a unit-level mock
// would have to replicate gohive's Cursor interface in full, which isn't
// worth the maintenance burden for the small additional coverage.
func TestHiveValidateConfig_MissingRequiredFields(t *testing.T) {
	errs := Hive{}.ValidateConfig(nil, []core.IntegrationConfigValue{}, "acc-1")
	require.NotEmpty(t, errs)
	// Collect into a single string so order-independent assertion works.
	joined := ""
	for _, e := range errs {
		joined += e.Error() + "|"
	}
	assert.Contains(t, joined, "hive_url is required")
	assert.Contains(t, joined, "hive_table is required")
	assert.Contains(t, joined, "hive_timestamp_col is required")
	assert.Contains(t, joined, "hive_message_col is required")
}

func TestHiveValidateConfig_LdapMissingCredentials(t *testing.T) {
	errs := Hive{}.ValidateConfig(nil, []core.IntegrationConfigValue{
		{Name: "hive_url", Value: "h:10000"},
		{Name: "hive_table", Value: "t"},
		{Name: "hive_timestamp_col", Value: "time_ms"},
		{Name: "hive_message_col", Value: "log"},
		{Name: "auth_type", Value: "ldap"},
	}, "acc-1")
	joined := ""
	for _, e := range errs {
		joined += e.Error() + "|"
	}
	assert.Contains(t, joined, "username is required for LDAP")
	assert.Contains(t, joined, "password is required for LDAP")
}

func TestHiveValidateConfig_BadUrlFailsBeforeConnect(t *testing.T) {
	errs := Hive{}.ValidateConfig(nil, []core.IntegrationConfigValue{
		{Name: "hive_url", Value: "not-a-host-port"},
		{Name: "hive_table", Value: "t"},
		{Name: "hive_timestamp_col", Value: "time_ms"},
		{Name: "hive_message_col", Value: "log"},
		{Name: "auth_type", Value: "none"},
	}, "acc-1")
	require.Len(t, errs, 1)
	assert.Contains(t, errs[0].Error(), "hive_url")
}

func TestHiveValidateConfig_UnreachableHostFailsAtConnect(t *testing.T) {
	// 127.0.0.1:1 is reliably unreachable on every CI box — connect aborts
	// fast, validation surfaces a single descriptive error.
	errs := Hive{}.ValidateConfig(nil, []core.IntegrationConfigValue{
		{Name: "hive_url", Value: "127.0.0.1:1"},
		{Name: "hive_table", Value: "k8s_logs"},
		{Name: "hive_timestamp_col", Value: "time_ms"},
		{Name: "hive_message_col", Value: "log"},
		{Name: "auth_type", Value: "none"},
	}, "acc-1")
	require.Len(t, errs, 1)
	assert.Contains(t, errs[0].Error(), "cannot reach HiveServer2")
}

func TestStringFromForm(t *testing.T) {
	form := map[string]any{
		"present":      "value",
		"with_spaces":  "  trimmed  ",
		"non_string":   123,
		"empty_string": "",
	}
	assert.Equal(t, "value", stringFromForm(form, "present"))
	assert.Equal(t, "trimmed", stringFromForm(form, "with_spaces"), "trimmed of whitespace")
	assert.Equal(t, "", stringFromForm(form, "non_string"), "non-string returns empty")
	assert.Equal(t, "", stringFromForm(form, "empty_string"))
	assert.Equal(t, "", stringFromForm(form, "missing"))
	assert.Equal(t, "", stringFromForm(nil, "any"))
}
