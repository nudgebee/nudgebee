package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"

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
	assert.Equal(t, "healthy", diagnosis.Health)
	assert.Equal(t, "CONNECTION_SUCCEEDED", diagnosis.ReasonCode)
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
