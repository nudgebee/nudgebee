package common

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
)

func azureRespErr(status int, code string) error {
	return &azcore.ResponseError{StatusCode: status, ErrorCode: code}
}

// TestClassifyAzureCostError pins what a failed cost query means for the
// account. The key case: a 403 on the cost call is only "cost-only" when the
// same credential can still read the subscription. A principal that lost all
// access produces the same 403 and must still disconnect.
func TestClassifyAzureCostError(t *testing.T) {
	const sub = "00000000-0000-0000-0000-000000000001"
	probeOK := func() error { return nil }
	probeDenied := func() error { return azureRespErr(http.StatusForbidden, "AuthorizationFailed") }
	probeGone := func() error { return azureRespErr(http.StatusNotFound, "SubscriptionNotFound") }
	probeTimeout := func() error { return fmt.Errorf("list resource groups: %w", context.DeadlineExceeded) }
	probeThrottled := func() error { return azureRespErr(http.StatusTooManyRequests, "") }

	tests := []struct {
		name        string
		err         error
		probe       func() error
		want        AzureCostFailureKind
		wantInMsg   string
		probeCalled bool
		noProbe     bool
	}{
		{
			name:      "expired secret is a broken credential without probing",
			err:       fmt.Errorf("query: %w", &azidentity.AuthenticationFailedError{}),
			probe:     probeOK,
			want:      AzureCostCredentialBroken,
			wantInMsg: "rejected the service principal's credentials",
			noProbe:   true,
		},
		{
			name:      "subscription moved to another tenant is a broken credential",
			err:       azureRespErr(http.StatusUnauthorized, "InvalidAuthenticationTokenTenant"),
			probe:     probeOK,
			want:      AzureCostCredentialBroken,
			wantInMsg: "InvalidAuthenticationTokenTenant",
		},
		{
			name:      "rate limit is transient without probing",
			err:       fmt.Errorf("after retries: %w", azureRespErr(http.StatusTooManyRequests, "429")),
			probe:     probeDenied,
			want:      AzureCostTransient,
			wantInMsg: "next daily sync retries",
			noProbe:   true,
		},
		{
			name:  "throttled code is transient",
			err:   azureRespErr(http.StatusBadRequest, "ResourceRequestsThrottled"),
			probe: probeOK,
			want:  AzureCostTransient,
		},
		{
			name:  "server error is transient",
			err:   azureRespErr(http.StatusServiceUnavailable, ""),
			probe: probeOK,
			want:  AzureCostTransient,
		},
		{
			name:        "cost 403 with all access revoked is a broken credential",
			err:         azureRespErr(http.StatusForbidden, "AuthorizationFailed"),
			probe:       probeDenied,
			want:        AzureCostCredentialBroken,
			wantInMsg:   "cannot read subscription",
			probeCalled: true,
		},
		{
			name:        "subscription gone is a broken credential",
			err:         azureRespErr(http.StatusNotFound, "SubscriptionNotFound"),
			probe:       probeGone,
			want:        AzureCostCredentialBroken,
			probeCalled: true,
		},
		{
			name:        "probe timeout is transient, never broken",
			err:         azureRespErr(http.StatusForbidden, "AuthorizationFailed"),
			probe:       probeTimeout,
			want:        AzureCostTransient,
			wantInMsg:   "could not be confirmed",
			probeCalled: true,
		},
		{
			name:        "probe throttled is transient, never broken",
			err:         azureRespErr(http.StatusForbidden, "AuthorizationFailed"),
			probe:       probeThrottled,
			want:        AzureCostTransient,
			probeCalled: true,
		},
		{
			name:        "cost 403 while subscription readable asks for a cost role",
			err:         azureRespErr(http.StatusForbidden, "AuthorizationFailed"),
			probe:       probeOK,
			want:        AzureCostUnavailable,
			wantInMsg:   "Reader or Cost Management Reader",
			probeCalled: true,
		},
		{
			name:        "RBAC denial while subscription readable asks for a cost role",
			err:         azureRespErr(http.StatusUnauthorized, "RBACAccessDenied"),
			probe:       probeOK,
			want:        AzureCostUnavailable,
			wantInMsg:   "Reader or Cost Management Reader",
			probeCalled: true,
		},
		{
			name:        "EA account owner view charges off names the enterprise admin",
			err:         azureRespErr(http.StatusUnauthorized, "AccountCostDisabled"),
			probe:       probeOK,
			want:        AzureCostUnavailable,
			wantInMsg:   "Account owners can view charges",
			probeCalled: true,
		},
		{
			name:        "EA department view charges off names the enterprise admin",
			err:         azureRespErr(http.StatusUnauthorized, "DepartmentCostDisabled"),
			probe:       probeOK,
			want:        AzureCostUnavailable,
			wantInMsg:   "Department admins can view charges",
			probeCalled: true,
		},
		{
			name:        "partner pricing not published names the partner",
			err:         azureRespErr(http.StatusUnauthorized, "IndirectCostDisabled"),
			probe:       probeOK,
			want:        AzureCostUnavailable,
			wantInMsg:   "partner",
			probeCalled: true,
		},
		{
			name:        "unsupported offer type",
			err:         azureRespErr(http.StatusBadRequest, "SubscriptionTypeNotSupported"),
			probe:       probeOK,
			want:        AzureCostUnavailable,
			wantInMsg:   "offer type",
			probeCalled: true,
		},
		{
			name:        "disallowed operation is an unsupported offer type",
			err:         azureRespErr(http.StatusBadRequest, "DisallowedOperation"),
			probe:       probeOK,
			want:        AzureCostUnavailable,
			probeCalled: true,
		},
		{
			name:        "no cost data yet for a readable subscription",
			err:         azureRespErr(http.StatusNotFound, "SubscriptionNotFound"),
			probe:       probeOK,
			want:        AzureCostUnavailable,
			wantInMsg:   "48 hours",
			probeCalled: true,
		},
		{
			name:        "unrecognised failure with a working credential is unexpected",
			err:         azureRespErr(http.StatusBadRequest, "BadRequest"),
			probe:       probeOK,
			want:        AzureCostUnexpected,
			wantInMsg:   "BadRequest",
			probeCalled: true,
		},
		{
			name:        "non-Azure error with a working credential is unexpected",
			err:         errors.New("connection reset by peer"),
			probe:       probeOK,
			want:        AzureCostUnexpected,
			probeCalled: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			called := false
			probe := func() error {
				called = true
				return tt.probe()
			}
			got := ClassifyAzureCostError(tt.err, sub, probe)
			if got.Kind != tt.want {
				t.Fatalf("Kind = %v, want %v (message %q)", got.Kind, tt.want, got.Message)
			}
			if tt.wantInMsg != "" && !strings.Contains(got.Message, tt.wantInMsg) {
				t.Errorf("Message = %q, want it to contain %q", got.Message, tt.wantInMsg)
			}
			if tt.probeCalled && !called {
				t.Error("probe was not called")
			}
			if tt.noProbe && called {
				t.Error("probe ran, but the cost error alone is conclusive")
			}
		})
	}
}

// TestClassifyAzureCostErrorNilProbe keeps a caller with no way to probe from
// panicking; the cost error alone decides.
func TestClassifyAzureCostErrorNilProbe(t *testing.T) {
	got := ClassifyAzureCostError(azureRespErr(http.StatusForbidden, "AuthorizationFailed"), "sub", nil)
	if got.Kind != AzureCostUnavailable {
		t.Fatalf("Kind = %v, want AzureCostUnavailable", got.Kind)
	}
}
