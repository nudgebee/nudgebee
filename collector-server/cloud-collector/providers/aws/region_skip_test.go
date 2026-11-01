package aws

import (
	"bytes"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws/retry"
	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	"github.com/aws/smithy-go"
	smithyhttp "github.com/aws/smithy-go/transport/http"
	"github.com/stretchr/testify/assert"
)

// testHTTPFailure is the error chain the SDK returns for an HTTP error status after
// its retries, as seen for MSK ListClustersV2 in a region that cannot be reached.
func testHTTPFailure(status int, requestID string) error {
	respErr := &awshttp.ResponseError{
		ResponseError: &smithyhttp.ResponseError{
			Response: &smithyhttp.Response{Response: &http.Response{StatusCode: status}},
			Err:      errors.New("deserialization failed, failed to decode response body, invalid character '<'"),
		},
		RequestID: requestID,
	}
	return &smithy.OperationError{ServiceID: "Kafka", OperationName: "ListClustersV2", Err: &retry.MaxAttemptsError{Attempt: 5, Err: respErr}}
}

// The failure the issue describes: DescribeAlarms in me-south-1 never connects.
var testDialFailure = &url.Error{Op: "Post", URL: "https://monitoring.me-south-1.amazonaws.com/",
	Err: &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("i/o timeout")}}

func TestClassifyRegionSkip(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want regionSkipKind
	}{
		{"no error", nil, regionSkipNone},
		{"dns name not found", &net.DNSError{Name: "ses.ap-east-2.amazonaws.com", IsNotFound: true}, regionSkipEndpointMissing},
		{"service not offered in region", &smithy.GenericAPIError{Code: "InvalidAction"}, regionSkipServiceUnavailable},
		{"connection never established", testDialFailure, regionSkipUnreachable},
		{"5xx from a gateway, no AWS request id", testHTTPFailure(504, ""), regionSkipUnreachable},
		{"5xx from AWS itself is a real error", testHTTPFailure(503, "4f1c2a"), regionSkipNone},
		{"4xx without a request id is a real error", testHTTPFailure(400, ""), regionSkipNone},
		{"5xx with an AWS error code is a real error", &smithy.OperationError{ServiceID: "Kafka", OperationName: "ListClustersV2", Err: errors.Join(testHTTPFailure(500, ""), &smithy.GenericAPIError{Code: "InternalServerError"})}, regionSkipNone},
		{"region not enabled", &smithy.GenericAPIError{Code: "OptInRequired"}, regionSkipDisabled},
		{"rate limited", &smithy.GenericAPIError{Code: "ThrottlingException"}, regionSkipThrottled},
		{"permission gap is a real error", &smithy.GenericAPIError{Code: "AccessDenied"}, regionSkipNone},
		{"anything else is a real error", errors.New("boom"), regionSkipNone},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, classifyRegionSkip(tt.err))
		})
	}
}

func captureLogger() (*slog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	return slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})), &buf
}

func TestSkipRegionLogsOnceAtTheRightLevel(t *testing.T) {
	logger, logs := captureLogger()

	assert.True(t, skipRegion(logger, testDialFailure, "me-south-1", "service", "cloudwatch alarms"))
	out := logs.String()
	assert.Contains(t, out, "level=WARN")
	assert.Contains(t, out, "skipping unreachable region endpoint")
	assert.Contains(t, out, "region=me-south-1")
	assert.Contains(t, out, `service="cloudwatch alarms"`)
	assert.NotContains(t, out, "level=ERROR")

	logs.Reset()
	assert.False(t, skipRegion(logger, errors.New("AccessDenied"), "us-east-1"), "a real failure must still be reported")
	assert.Empty(t, logs.String(), "the caller logs a real failure, not skipRegion")
}

func TestRegionBackoff(t *testing.T) {
	clock := time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)
	backoff := newRegionBackoff(2, 30*time.Minute)
	backoff.now = func() time.Time { return clock }

	// A single failure is not enough: one network blip must not silence a region.
	assert.False(t, backoff.recordUnreachable("111", "me-south-1"))
	assert.False(t, backoff.shouldSkip("111", "me-south-1"))

	// A second failure in a row sets it aside for the cooldown.
	assert.True(t, backoff.recordUnreachable("111", "me-south-1"))
	assert.True(t, backoff.shouldSkip("111", "me-south-1"))

	// Only that account and region.
	assert.False(t, backoff.shouldSkip("222", "me-south-1"))
	assert.False(t, backoff.shouldSkip("111", "us-east-1"))

	// After the cooldown it is tried again, without a restart.
	clock = clock.Add(30*time.Minute + time.Second)
	assert.False(t, backoff.shouldSkip("111", "me-south-1"))

	// Still down: set aside again straight away.
	assert.True(t, backoff.recordUnreachable("111", "me-south-1"))
	assert.True(t, backoff.shouldSkip("111", "me-south-1"))

	// A success clears it completely, including the failure count.
	clock = clock.Add(31 * time.Minute)
	backoff.recordSuccess("111", "me-south-1")
	assert.False(t, backoff.recordUnreachable("111", "me-south-1"))
	assert.False(t, backoff.shouldSkip("111", "me-south-1"))
}

func TestRegionBackoffIsSafeForConcurrentRegions(t *testing.T) {
	backoff := newRegionBackoff(2, time.Minute)
	done := make(chan struct{})
	for _, region := range []string{"a", "b", "c", "d"} {
		go func(region string) {
			for i := 0; i < 100; i++ {
				backoff.recordUnreachable("111", region)
				backoff.shouldSkip("111", region)
				backoff.recordSuccess("111", region)
			}
			done <- struct{}{}
		}(region)
	}
	for i := 0; i < 4; i++ {
		<-done
	}
}
