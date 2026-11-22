package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"

	"nudgebee/services/integrations/core"
	"nudgebee/services/security"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func diagnosisRequestContext(t *testing.T, accountID string) *security.RequestContext {
	t.Helper()
	var sc security.SecurityContext
	wire := `{"TenantId":"tenant-1","UserId":"user-1","AccountIds":["` + accountID + `"],"Roles":["account_admin_readonly"],"ScopedEntityIds":{"account_admin_readonly":["` + accountID + `"]}}`
	require.NoError(t, json.Unmarshal([]byte(wire), &sc))
	return security.NewRequestContext(context.Background(), &sc, slog.Default(), nil, nil)
}

func TestDiagnoseIntegrationConnectionRequiresLinkedAccountReadAccess(t *testing.T) {
	previousList := listDiagnosisIntegrationAccountIDs
	previousTest := testDiagnosisIntegrationConnection
	t.Cleanup(func() {
		listDiagnosisIntegrationAccountIDs = previousList
		testDiagnosisIntegrationConnection = previousTest
	})
	listDiagnosisIntegrationAccountIDs = func(_ *security.RequestContext, _ string) ([]string, error) {
		return []string{"account-B"}, nil
	}
	testCalled := false
	testDiagnosisIntegrationConnection = func(_ *security.RequestContext, _, _ string) error {
		testCalled = true
		return nil
	}

	_, err := diagnoseIntegrationConnection(diagnosisRequestContext(t, "account-A"), "integration-1")
	assert.ErrorIs(t, err, errIntegrationNotFound)
	assert.False(t, testCalled)
}

// A person whose access comes from custom roles diagnoses an integration with
// the integrations:Read grant the gateway classifies the action as, on a linked
// account the grant covers, and not with another module's grant.
func TestDiagnoseIntegrationConnectionHonoursTheReadGrant(t *testing.T) {
	previousList := listDiagnosisIntegrationAccountIDs
	previousTest := testDiagnosisIntegrationConnection
	t.Cleanup(func() {
		listDiagnosisIntegrationAccountIDs = previousList
		testDiagnosisIntegrationConnection = previousTest
	})
	listDiagnosisIntegrationAccountIDs = func(_ *security.RequestContext, _ string) ([]string, error) {
		return []string{"account-A"}, nil
	}
	testDiagnosisIntegrationConnection = func(_ *security.RequestContext, _, _ string) error { return nil }

	person := func(key string, grants map[string]bool) *security.RequestContext {
		raw, err := json.Marshal(map[string]any{
			"TenantId": "tenant-1", "UserId": "user-1", "AccountIds": []string{"account-A"}, "Roles": []string{},
			"ScopedCustomPermissions": map[string]any{"account-A": grants},
		})
		require.NoError(t, err)
		var sc security.SecurityContext
		require.NoError(t, json.Unmarshal(raw, &sc))
		ctx := security.NewRequestContext(context.Background(), &sc, slog.Default(), nil, nil)
		ctx.SetActionPermission(key)
		return ctx
	}

	_, err := diagnoseIntegrationConnection(person("integrations:Read", map[string]bool{"integrations:Read": true}), "integration-1")
	assert.NoError(t, err)

	_, err = diagnoseIntegrationConnection(person("integrations:Read", map[string]bool{"k8s:Read": true}), "integration-1")
	assert.ErrorIs(t, err, errIntegrationNotFound, "a grant for another module")

	_, err = diagnoseIntegrationConnection(person("", map[string]bool{"integrations:Read": true}), "integration-1")
	assert.ErrorIs(t, err, errIntegrationNotFound, "no key: built-in roles only")
}

func TestDiagnoseIntegrationConnectionHidesMissingIntegration(t *testing.T) {
	previousList := listDiagnosisIntegrationAccountIDs
	previousTest := testDiagnosisIntegrationConnection
	t.Cleanup(func() {
		listDiagnosisIntegrationAccountIDs = previousList
		testDiagnosisIntegrationConnection = previousTest
	})
	listDiagnosisIntegrationAccountIDs = func(_ *security.RequestContext, _ string) ([]string, error) {
		return nil, nil
	}
	testCalled := false
	testDiagnosisIntegrationConnection = func(_ *security.RequestContext, _, _ string) error {
		testCalled = true
		return nil
	}

	_, err := diagnoseIntegrationConnection(diagnosisRequestContext(t, "account-A"), "missing-integration")
	assert.ErrorIs(t, err, errIntegrationNotFound)
	assert.False(t, testCalled)
}

func TestDiagnoseIntegrationConnectionPreservesLookupFailure(t *testing.T) {
	previousList := listDiagnosisIntegrationAccountIDs
	t.Cleanup(func() { listDiagnosisIntegrationAccountIDs = previousList })
	lookupErr := errors.New("database unavailable")
	listDiagnosisIntegrationAccountIDs = func(_ *security.RequestContext, _ string) ([]string, error) {
		return nil, lookupErr
	}

	_, err := diagnoseIntegrationConnection(diagnosisRequestContext(t, "account-A"), "integration-1")
	assert.ErrorIs(t, err, lookupErr)
	assert.NotErrorIs(t, err, errIntegrationNotFound)
}

func TestDiagnoseIntegrationConnectionSanitizesProviderFailure(t *testing.T) {
	previousList := listDiagnosisIntegrationAccountIDs
	previousTest := testDiagnosisIntegrationConnection
	t.Cleanup(func() {
		listDiagnosisIntegrationAccountIDs = previousList
		testDiagnosisIntegrationConnection = previousTest
	})
	listDiagnosisIntegrationAccountIDs = func(_ *security.RequestContext, _ string) ([]string, error) {
		return []string{"account-A"}, nil
	}
	testDiagnosisIntegrationConnection = func(_ *security.RequestContext, _, accountID string) error {
		assert.Equal(t, "account-A", accountID)
		return errors.New("upstream returned 401 for https://admin:super-secret@example.test/api?token=secret")
	}

	diagnosis, err := diagnoseIntegrationConnection(diagnosisRequestContext(t, "account-A"), "integration-1")
	require.NoError(t, err)
	assert.False(t, diagnosis.Success)
	assert.Equal(t, "failed", diagnosis.TestStatus)
	assert.Equal(t, "unhealthy", diagnosis.Health)
	assert.Equal(t, "authentication", diagnosis.Stage)
	assert.Equal(t, "AUTHENTICATION_FAILED", diagnosis.ReasonCode)
	assert.Equal(t, "The integration endpoint rejected authentication.", diagnosis.Summary)
	assert.Contains(t, diagnosis.RecommendedAction, "credentials")
	encoded, marshalErr := json.Marshal(diagnosis)
	require.NoError(t, marshalErr)
	assert.NotContains(t, string(encoded), "super-secret")
	assert.NotContains(t, string(encoded), "token=secret")
}

func TestDiagnoseIntegrationConnectionSuccess(t *testing.T) {
	previousList := listDiagnosisIntegrationAccountIDs
	previousTest := testDiagnosisIntegrationConnection
	t.Cleanup(func() {
		listDiagnosisIntegrationAccountIDs = previousList
		testDiagnosisIntegrationConnection = previousTest
	})
	listDiagnosisIntegrationAccountIDs = func(_ *security.RequestContext, _ string) ([]string, error) {
		return []string{"account-A"}, nil
	}
	testDiagnosisIntegrationConnection = func(_ *security.RequestContext, _, accountID string) error {
		assert.Equal(t, "account-A", accountID)
		return nil
	}

	diagnosis, err := diagnoseIntegrationConnection(diagnosisRequestContext(t, "account-A"), "integration-1")
	require.NoError(t, err)
	assert.True(t, diagnosis.Success)
	assert.Equal(t, "passed", diagnosis.TestStatus)
	assert.Equal(t, "healthy", diagnosis.Health)
	assert.Equal(t, "CONNECTION_SUCCEEDED", diagnosis.ReasonCode)
}

func TestDiagnoseIntegrationConnectionReportsUnsupportedProbeAsUnknown(t *testing.T) {
	previousList := listDiagnosisIntegrationAccountIDs
	previousTest := testDiagnosisIntegrationConnection
	t.Cleanup(func() {
		listDiagnosisIntegrationAccountIDs = previousList
		testDiagnosisIntegrationConnection = previousTest
	})
	listDiagnosisIntegrationAccountIDs = func(_ *security.RequestContext, _ string) ([]string, error) {
		return []string{"account-A"}, nil
	}
	testDiagnosisIntegrationConnection = func(_ *security.RequestContext, _, _ string) error {
		return core.ErrConnectionTestNotSupported
	}

	diagnosis, err := diagnoseIntegrationConnection(diagnosisRequestContext(t, "account-A"), "integration-1")
	require.NoError(t, err)
	assert.False(t, diagnosis.Success)
	assert.Equal(t, "not_supported", diagnosis.TestStatus)
	assert.Equal(t, "unknown", diagnosis.Health)
	assert.Equal(t, "CONNECTION_TEST_NOT_SUPPORTED", diagnosis.ReasonCode)
	assert.NotContains(t, diagnosis.Summary, "healthy")
}

func TestClassifyIntegrationDiagnosisError(t *testing.T) {
	tests := []struct {
		err    string
		stage  string
		reason string
	}{
		{"dial tcp: lookup prometheus.internal: no such host", "dns", "DNS_RESOLUTION_FAILED"},
		{"x509: certificate signed by unknown authority", "tls", "TLS_VALIDATION_FAILED"},
		{"context deadline exceeded", "connectivity", "CONNECTION_TIMEOUT"},
		{"connect: connection refused", "connectivity", "ENDPOINT_UNREACHABLE"},
		{"403 forbidden", "authorization", "AUTHORIZATION_FAILED"},
		{"unexpected upstream body containing secret-value", "connection", "CONNECTION_FAILED"},
	}
	for _, test := range tests {
		t.Run(test.reason, func(t *testing.T) {
			stage, reason, summary, recommendedAction := classifyIntegrationDiagnosisError(errors.New(test.err))
			assert.Equal(t, test.stage, stage)
			assert.Equal(t, test.reason, reason)
			assert.NotContains(t, summary, test.err)
			assert.NotEmpty(t, recommendedAction)
		})
	}
}

// A probe that has pinned the failure on a specific field prefixes its message with
// "configuration:", and that verdict must win over the substring branches. The Splunk
// index check quotes the customer's own index name, so an index called dns_logs or
// tls_events would otherwise be reported to nubi as a DNS or TLS failure. The port hint
// is the other way round: it names the URL, but a hung connection is still a
// connectivity failure.
func TestClassifyIntegrationDiagnosisError_ConfigurationPrefixWins(t *testing.T) {
	tests := []struct {
		name   string
		err    string
		stage  string
		reason string
	}{
		{
			"event index configured as the metric index",
			`configuration: splunk_metric_index "otel_logs" is an event index — metrics need an index created with datatype=metric`,
			"configuration", "CONFIGURATION_INVALID",
		},
		{
			"index name containing dns",
			`configuration: splunk_log_index "dns_logs" is a metrics index — logs need an event index`,
			"configuration", "CONFIGURATION_INVALID",
		},
		{
			"index name containing tls",
			`configuration: splunk_trace_index "tls_events" is disabled in Splunk — enable it, or point splunk_trace_index at an enabled index`,
			"configuration", "CONFIGURATION_INVALID",
		},
		{
			"missing port on a timeout",
			`failed to connect to Splunk at https://splunk.example.com: Get "https://splunk.example.com/services/server/info?output_mode=json": ` +
				`context deadline exceeded (Client.Timeout exceeded while awaiting headers) - the URL has no port, so port 443 was used; ` +
				`Splunk's management API normally listens on 8089 — if yours does, use https://splunk.example.com:8089`,
			"connectivity", "CONNECTION_TIMEOUT",
		},
		{
			"missing port on a refused connection",
			`failed to connect to Splunk at http://splunk.example.com - the server may be down, or a tunnel/port-forward may have died: ` +
				`Get "http://splunk.example.com/services/server/info?output_mode=json": dial tcp 10.0.0.1:80: connect: connection refused - ` +
				`the URL has no port, so port 80 was used; Splunk's management API normally listens on 8089 — if yours does, use http://splunk.example.com:8089`,
			"connectivity", "ENDPOINT_UNREACHABLE",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stage, reason, _, _ := classifyIntegrationDiagnosisError(errors.New(test.err))
			assert.Equal(t, test.stage, stage)
			assert.Equal(t, test.reason, reason)
		})
	}
}
