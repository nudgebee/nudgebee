package account

import (
	"log/slog"
	"nudgebee/services/internal/testenv"
	"nudgebee/services/security"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRefreshEvent(t *testing.T) {
	testenv.RequireMetastore(t)
	m := testenv.RequireEnv(t, "TEST_DELETE_ACCOUNT_IDS")
	for _, s := range strings.Split(m["TEST_DELETE_ACCOUNT_IDS"], ",") {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		_, err := DeleteAccount(security.NewRequestContextForSuperAdmin(slog.Default(), nil, nil), AccountDeleteRequest{
			Id:        s,
			OnlyClean: false,
		})
		assert.Nil(t, err)
		if err != nil {
			break
		}
	}
}

func TestDeleteAccount(t *testing.T) {
	testenv.RequireMetastore(t)
	m := testenv.RequireEnv(t, "TEST_DELETE_ACCOUNT_ID")
	_, err := DeleteAccount(security.NewRequestContextForSuperAdmin(slog.Default(), nil, nil), AccountDeleteRequest{
		Id:        m["TEST_DELETE_ACCOUNT_ID"],
		OnlyClean: false,
	})
	assert.Nil(t, err)
}

func TestExtractAwsAccountIdFromRoleArn(t *testing.T) {
	tests := []struct {
		name        string
		roleArn     string
		expected    string
		expectError bool
	}{
		{
			name:        "Valid IAM role ARN",
			roleArn:     "arn:aws:iam::123456789012:role/NudgebeeRole",
			expected:    "123456789012",
			expectError: false,
		},
		{
			name:        "Valid IAM role ARN with path",
			roleArn:     "arn:aws:iam::987654321098:role/service/MyRole",
			expected:    "987654321098",
			expectError: false,
		},
		{
			name:        "Empty ARN",
			roleArn:     "",
			expected:    "",
			expectError: true,
		},
		{
			name:        "Invalid ARN format - too few parts",
			roleArn:     "arn:aws:iam:role",
			expected:    "",
			expectError: true,
		},
		{
			name:        "Invalid ARN - missing account ID",
			roleArn:     "arn:aws:iam:::role/MyRole",
			expected:    "",
			expectError: true,
		},
		{
			name:        "Valid ARN with different AWS partition",
			roleArn:     "arn:aws-us-gov:iam::111122223333:role/GovRole",
			expected:    "111122223333",
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := extractAwsAccountIdFromRoleArn(tt.roleArn)
			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expected, result)
			}
		})
	}
}
