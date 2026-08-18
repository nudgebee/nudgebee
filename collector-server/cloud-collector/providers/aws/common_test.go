package aws

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/retry"
	smithy "github.com/aws/smithy-go"
	smithyhttp "github.com/aws/smithy-go/transport/http"
)

func TestIsRegionEndpointMissing(t *testing.T) {
	nxdomain := &net.DNSError{Name: "email.ap-east-2.amazonaws.com", IsNotFound: true}

	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"plain string error", errors.New("boom"), false},
		{"dns timeout (not NXDOMAIN)", &net.DNSError{IsTimeout: true}, false},
		{"raw NXDOMAIN", nxdomain, true},
		{"wrapped via fmt.Errorf %w", fmt.Errorf("op failed: %w", nxdomain), true},
		{
			name: "wrapped through net.OpError and url.Error (sdk transport shape)",
			err: &url.Error{
				Op:  "Post",
				URL: "https://email.ap-east-2.amazonaws.com/",
				Err: &net.OpError{Op: "dial", Net: "tcp", Err: nxdomain},
			},
			want: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := isRegionEndpointMissing(tc.err)
			if got != tc.want {
				t.Fatalf("isRegionEndpointMissing(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestIsRegionUnreachable(t *testing.T) {
	// Shape of `dial tcp 16.24.112.74:443: i/o timeout` once the aws-sdk-go-v2
	// transport wraps it (observed for a far opt-in region like me-south-1).
	dialTimeout := &net.OpError{Op: "dial", Net: "tcp", Err: &timeoutError{}}
	dialRefused := &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connect: connection refused")}
	// A mid-request read timeout against a reachable region — must NOT be skipped.
	readTimeout := &net.OpError{Op: "read", Net: "tcp", Err: &timeoutError{}}

	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"plain string error", errors.New("boom"), false},
		{"raw dial timeout", dialTimeout, true},
		{"dial connection refused", dialRefused, true},
		{"read timeout (reachable but slow)", readTimeout, false},
		{"wrapped via fmt.Errorf %w", fmt.Errorf("op failed: %w", dialTimeout), true},
		{
			name: "wrapped through url.Error (sdk transport shape)",
			err: &url.Error{
				Op:  "Post",
				URL: "https://elasticbeanstalk.me-south-1.amazonaws.com/",
				Err: dialTimeout,
			},
			want: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := isRegionUnreachable(tc.err)
			if got != tc.want {
				t.Fatalf("isRegionUnreachable(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

// TestIsRegionUnreachableRealSdkChain pins the FULL aws-sdk-go-v2 wrapping that a
// dial failure to a far opt-in region (me-south-1) produces in production:
// smithy.OperationError -> retry.MaxAttemptsError -> http.RequestSendError ->
// url.Error -> net.OpError{Op:"dial"}. The cases above only exercise the
// url.Error layer; this guards against an SDK upgrade inserting a non-unwrapping
// wrapper that would silently re-poison the resources feature with sync errors.
func TestIsRegionUnreachableRealSdkChain(t *testing.T) {
	dial := &net.OpError{Op: "dial", Net: "tcp", Err: &timeoutError{}}
	urlErr := &url.Error{Op: "Get", URL: "https://eks.me-south-1.amazonaws.com/clusters", Err: dial}
	reqSend := &smithyhttp.RequestSendError{Err: urlErr}
	maxAtt := &retry.MaxAttemptsError{Attempt: 3, Err: reqSend}
	opErr := &smithy.OperationError{ServiceID: "EKS", OperationName: "ListClusters", Err: maxAtt}

	if !isRegionUnreachable(opErr) {
		t.Fatalf("isRegionUnreachable=false for the real SDK dial-failure chain; the region skip would not trigger in prod")
	}
}

func TestIsServiceUnavailableInRegion(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"plain string error", errors.New("boom"), false},
		{"InvalidAction api error", &smithy.GenericAPIError{Code: "InvalidAction", Message: ""}, true},
		{"UnknownOperationException", &smithy.GenericAPIError{Code: "UnknownOperationException", Message: ""}, true},
		{"UnknownOperation", &smithy.GenericAPIError{Code: "UnknownOperation", Message: ""}, true},
		{"not-supported message", &smithy.GenericAPIError{Code: "ValidationException", Message: "SES is not supported in this region"}, true},
		{"not-supported message mixed case", &smithy.GenericAPIError{Code: "ValidationException", Message: "SES is Not Supported In This Region"}, true},
		{"unrelated api error still surfaces", &smithy.GenericAPIError{Code: "AccessDenied", Message: "no perms"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isServiceUnavailableInRegion(c.err); got != c.want {
				t.Fatalf("isServiceUnavailableInRegion=%v, want %v", got, c.want)
			}
		})
	}
}

// TestIsServiceUnavailableInRegionRealSdkChain pins the FULL aws-sdk-go-v2 wrapping
// that SES ListIdentities produces in an opt-in region where SES isn't offered
// (eu-south-2): smithy.OperationError -> smithy.GenericAPIError{Code:"InvalidAction"}.
// Guards against an SDK upgrade breaking the errors.As unwrap that drives the skip.
func TestIsServiceUnavailableInRegionRealSdkChain(t *testing.T) {
	apiErr := &smithy.GenericAPIError{Code: "InvalidAction", Message: ""}
	opErr := &smithy.OperationError{ServiceID: "SES", OperationName: "ListIdentities", Err: apiErr}

	if !isServiceUnavailableInRegion(opErr) {
		t.Fatalf("isServiceUnavailableInRegion=false for the real SDK SES InvalidAction chain; the region skip would not trigger in prod")
	}
}

func TestIsThrottleError(t *testing.T) {
	resp429 := &smithyhttp.ResponseError{Response: &smithyhttp.Response{Response: &http.Response{StatusCode: 429}}}
	resp400 := &smithyhttp.ResponseError{Response: &smithyhttp.Response{Response: &http.Response{StatusCode: 400}}}

	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"plain string error", errors.New("boom"), false},
		{"http 429 response error", resp429, true},
		{"http 400 response error", resp400, false},
		{"ThrottlingException code", &smithy.GenericAPIError{Code: "ThrottlingException"}, true},
		{"RequestLimitExceeded code (EC2)", &smithy.GenericAPIError{Code: "RequestLimitExceeded"}, true},
		{"SlowDown code (S3)", &smithy.GenericAPIError{Code: "SlowDown"}, true},
		{"ProvisionedThroughputExceededException (DynamoDB)", &smithy.GenericAPIError{Code: "ProvisionedThroughputExceededException"}, true},
		{"unrelated api error still surfaces", &smithy.GenericAPIError{Code: "AccessDenied"}, false},
		{"wrapped via fmt.Errorf %w", fmt.Errorf("fetch failed: %w", resp429), true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isThrottleError(c.err); got != c.want {
				t.Fatalf("isThrottleError=%v, want %v", got, c.want)
			}
		})
	}
}

// TestIsThrottleErrorRealSdkChain pins the FULL aws-sdk-go-v2 wrapping that a
// throttled ListClustersV2 produces in prod after the SDK exhausts its retries:
// smithy.OperationError -> retry.MaxAttemptsError -> smithyhttp.ResponseError{429}.
// This is exactly the chain behind the "[Rackspace] Cloud: Feature Sync Errors"
// MSK alert; guards against an SDK upgrade breaking the errors.As unwrap.
func TestIsThrottleErrorRealSdkChain(t *testing.T) {
	respErr := &smithyhttp.ResponseError{Response: &smithyhttp.Response{Response: &http.Response{StatusCode: 429}}, Err: &smithy.GenericAPIError{Code: "ThrottlingException"}}
	maxAtt := &retry.MaxAttemptsError{Attempt: 3, Err: respErr}
	opErr := &smithy.OperationError{ServiceID: "Kafka", OperationName: "ListClustersV2", Err: maxAtt}

	if !isThrottleError(opErr) {
		t.Fatalf("isThrottleError=false for the real SDK 429 max-attempts chain; the region skip would not trigger in prod and the alert would keep firing")
	}
}

// timeoutError mimics poll.TimeoutError ("i/o timeout"): a net.Error whose
// Timeout() reports true, as the runtime surfaces for a TCP dial deadline.
type timeoutError struct{}

func (timeoutError) Error() string   { return "i/o timeout" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return true }

// Regression test: the Cost & Usage Report API only exists in us-east-1.
// getUsageBucketFromCostReport used to build its CUR client straight from
// the account's own aws.Config, so an account operating in any other region
// (e.g. eu-west-1) failed CUR discovery with a DNS lookup error against a
// nonexistent regional endpoint (cur.eu-west-1.amazonaws.com — no such
// host), even though the account's credentials and CUR report were fine.
func TestCurServiceConfigForcesUsEast1(t *testing.T) {
	cases := []struct {
		name         string
		inputRegion  string
		expectRegion string
	}{
		{name: "non-us-east-1 account region gets overridden", inputRegion: "eu-west-1", expectRegion: "us-east-1"},
		{name: "already us-east-1 stays us-east-1", inputRegion: "us-east-1", expectRegion: "us-east-1"},
		{name: "empty region gets set to us-east-1", inputRegion: "", expectRegion: "us-east-1"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := aws.Config{Region: tc.inputRegion}
			got := curServiceConfig(cfg)
			if got.Region != tc.expectRegion {
				t.Fatalf("curServiceConfig(region=%q).Region = %q, want %q", tc.inputRegion, got.Region, tc.expectRegion)
			}
			if cfg.Region != tc.inputRegion {
				t.Fatalf("curServiceConfig mutated the input config's region: got %q, want unchanged %q", cfg.Region, tc.inputRegion)
			}
		})
	}
}
