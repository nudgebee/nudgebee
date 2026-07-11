package aws

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"testing"

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

// timeoutError mimics poll.TimeoutError ("i/o timeout"): a net.Error whose
// Timeout() reports true, as the runtime surfaces for a TCP dial deadline.
type timeoutError struct{}

func (timeoutError) Error() string   { return "i/o timeout" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return true }
