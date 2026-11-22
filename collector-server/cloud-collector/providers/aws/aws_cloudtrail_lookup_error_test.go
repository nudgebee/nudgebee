package aws

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"nudgebee/collector/cloud/providers"

	"github.com/aws/aws-sdk-go-v2/service/cloudtrail"
	"github.com/stretchr/testify/require"
)

// TestLookupCloudTrailEventsReturnsErrorNotPanicOnSdkError drives the real
// awsProvider.LookupCloudTrailEvents wrapper against a CloudTrail endpoint that
// answers with an SDK error. On error the SDK returns a nil output, and the
// wrapper used to dereference output.Events before looking at err — a nil
// pointer panic that reached the event processor as a crashed goroutine
// instead of the error it is built to handle.
func TestLookupCloudTrailEventsReturnsErrorNotPanicOnSdkError(t *testing.T) {
	var called atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called.Store(true)
		w.Header().Set("Content-Type", "application/x-amz-json-1.1")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"__type":"AccessDeniedException","Message":"injected SDK error"}`))
	}))
	t.Cleanup(server.Close)

	// Point the provider's real config chain at the fake endpoint, mirroring
	// serveLambdaAPI: env credentials avoid the account-secret decrypt path,
	// and the empty config files keep the test independent of the host.
	emptyFile := filepath.Join(t.TempDir(), "empty")
	require.NoError(t, os.WriteFile(emptyFile, nil, 0o600))
	for k, v := range map[string]string{
		"AWS_ENDPOINT_URL":            server.URL,
		"AWS_ACCESS_KEY_ID":           "test",
		"AWS_SECRET_ACCESS_KEY":       "test",
		"AWS_SESSION_TOKEN":           "",
		"AWS_PROFILE":                 "",
		"CLOUD_COLLECTOR_AWS_PROFILE": "",
		"AWS_CONFIG_FILE":             emptyFile,
		"AWS_SHARED_CREDENTIALS_FILE": emptyFile,
		"AWS_EC2_METADATA_DISABLED":   "true",
	} {
		t.Setenv(k, v)
	}

	pCtx := providers.NewCloudProviderContext(context.Background())
	_, err := (&awsProvider{}).LookupCloudTrailEvents(pCtx,
		providers.Account{AccountNumber: "123456789012"},
		"us-east-1",
		&cloudtrail.LookupEventsInput{},
	)
	require.Error(t, err, "the SDK error must surface to the caller instead of panicking")
	require.True(t, called.Load(), "the real wrapper must reach the local CloudTrail endpoint")
	require.ErrorContains(t, err, "injected SDK error")
}
