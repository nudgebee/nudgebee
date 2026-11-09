package account

import (
	"nudgebee/services/common"
	"nudgebee/services/config"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testGCPPrivateKey = "-----BEGIN PRIVATE KEY-----\nMIIEvQIBADANBgkqhkiG9w0BAQEFAASC\n-----END PRIVATE KEY-----\n"

// useTestEncryptionKey swaps the package-level key for the test's lifetime.
// Tests that call it must not use t.Parallel.
func useTestEncryptionKey(t *testing.T) {
	t.Helper()
	orig := config.Config.NudgebeeEncryptionKey
	config.Config.NudgebeeEncryptionKey = "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f"
	t.Cleanup(func() { config.Config.NudgebeeEncryptionKey = orig })
}

func TestOnboardingErrorConfigRedactsCredentials(t *testing.T) {
	useTestEncryptionKey(t)

	cases := []struct {
		name    string
		query   AccountCreateRequest
		secrets []string // must not appear in the stored config
		kept    []string // diagnostic fields that must survive
	}{
		{
			name: "aws access keys",
			query: AccountCreateRequest{
				AccountName: "prod-aws", CloudProvider: "AWS", Region: "us-east-1",
				AccessKey: "AKIAABCDEFGHIJKLMNOP", AccessSecret: "aws-secret-access-key-value",
			},
			secrets: []string{"AKIAABCDEFGHIJKLMNOP", "aws-secret-access-key-value"},
			kept:    []string{"prod-aws", "us-east-1"},
		},
		{
			name: "aws assume role keeps the role",
			query: AccountCreateRequest{
				AccountName: "role-aws", CloudProvider: "AWS",
				AssumeRole: "arn:aws:iam::123456789012:role/NudgebeeRole",
			},
			kept: []string{"role-aws", "arn:aws:iam::123456789012:role/NudgebeeRole"},
		},
		{
			name: "gcp key in access_secret",
			query: AccountCreateRequest{
				AccountName: "gcp-a", CloudProvider: "GCP",
				AccessSecret: `{"type":"service_account","project_id":"proj-a","private_key":"` + "-----BEGIN PRIVATE KEY-----\\nMIIEvQ\\n-----END PRIVATE KEY-----\\n" + `"}`,
			},
			secrets: []string{"BEGIN PRIVATE KEY", "MIIEvQ"},
			kept:    []string{"gcp-a"},
		},
		{
			name: "gcp key in data",
			query: AccountCreateRequest{
				AccountName: "gcp-b", CloudProvider: "GCP",
				Data: map[string]any{
					"type":           "service_account",
					"project_id":     "proj-b",
					"private_key_id": "0123456789abcdef0123456789abcdef01234567",
					"private_key":    testGCPPrivateKey,
					"client_email":   "nudgebee@proj-b.iam.gserviceaccount.com",
				},
			},
			secrets: []string{"0123456789abcdef0123456789abcdef01234567", "BEGIN PRIVATE KEY", "MIIEvQIBADANBgkqhkiG9w0BAQEFAASC"},
			kept:    []string{"gcp-b", "proj-b", "nudgebee@proj-b.iam.gserviceaccount.com"},
		},
		{
			name: "cloudfoundry client secret",
			query: AccountCreateRequest{
				AccountName: "cf", CloudProvider: "CloudFoundry",
				AccessKey: "uaa-client", AccessSecret: "cf-uaa-client-secret",
				Data: map[string]any{"cf_api_url": "https://api.cf.example.com"},
			},
			secrets: []string{"cf-uaa-client-secret"},
			kept:    []string{"cf", "https://api.cf.example.com"},
		},
		{
			name: "password and agent secret",
			query: AccountCreateRequest{
				AccountName: "db", CloudProvider: "K8s",
				Username: "admin", Password: "db-password-value",
				AgentAccessSecret: "agent-secret-value",
			},
			secrets: []string{"db-password-value", "agent-secret-value"},
			kept:    []string{"db", "admin"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stored, err := onboardingErrorConfig(tc.query)
			require.NoError(t, err)

			plain, err := common.Decrypt(stored)
			require.NoError(t, err, "config must stay encrypted")

			for _, s := range tc.secrets {
				assert.NotContains(t, plain, s)
			}
			for _, k := range tc.kept {
				assert.Contains(t, plain, k)
			}
		})
	}
}

func TestOnboardingErrorConfigDoesNotMutateRequest(t *testing.T) {
	useTestEncryptionKey(t)

	query := AccountCreateRequest{
		AccountName: "gcp", CloudProvider: "GCP", AccessSecret: "secret",
		Data: map[string]any{"private_key": testGCPPrivateKey},
	}
	_, err := onboardingErrorConfig(query)
	require.NoError(t, err)

	// CreateAccount goes on to validate and store these values.
	assert.Equal(t, "secret", query.AccessSecret)
	assert.Equal(t, testGCPPrivateKey, query.Data["private_key"])
}
