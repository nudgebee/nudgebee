package aws

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"testing"
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
