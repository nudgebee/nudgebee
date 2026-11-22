package gcloud

import (
	"testing"

	"nudgebee/collector/cloud/providers"

	"github.com/stretchr/testify/require"
)

func TestExecuteCliCommandV2RejectsCredentialAndWrongCliCommands(t *testing.T) {
	tests := []struct {
		command string
		want    string
	}{
		{command: "gcloud --quiet auth print-access-token", want: "not allowed"},
		{command: "gcloud auth activate-service-account --key-file other.json", want: "not allowed"},
		{command: "gcloud config set project other", want: "not allowed"},
		{command: "gsutil config", want: "not allowed"},
		{command: "gsutil -o test=value config", want: "not allowed"},
		{command: "gsutil -m cp local.txt gs://bucket", want: "cannot transfer"},
		{command: "gcloud storage cp gs://bucket/file local.txt", want: "cannot transfer"},
		{command: "bq extract dataset.table local.csv", want: "cannot transfer"},
		{command: "az vm list", want: "must start with gcloud, gsutil, or bq"},
		{command: "gcloud compute instances list --account other@example.com", want: "--account is not allowed"},
		{command: "gcloud compute instances list --account=other@example.com", want: "--account is not allowed"},
	}
	for _, tt := range tests {
		_, err := (&gcloudProvider{}).ExecuteCliCommandV2(nil, providers.Account{}, tt.command)
		require.ErrorContains(t, err, tt.want)
	}
}

func TestGcloudPrintAccessTokenIsTheOnlyAllowedAuthShape(t *testing.T) {
	require.True(t, isGcloudPrintAccessToken([]string{"gcloud", "auth", "print-access-token"}))
	require.NoError(t, validateGcloudWorkspaceBoundary([]string{"gcloud", "auth", "print-access-token"}))
	require.False(t, isGcloudPrintAccessToken([]string{"gcloud", "--quiet", "auth", "print-access-token"}))
	require.False(t, isGcloudPrintAccessToken([]string{"gcloud", "auth", "print-access-token", "--impersonate-service-account", "other@example.com"}))
}
