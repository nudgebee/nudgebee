package account

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/aws/smithy-go"
	"google.golang.org/api/googleapi"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestIsAuthFailure(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"azure 401", &azcore.ResponseError{StatusCode: 401}, true},
		{"azure 403", &azcore.ResponseError{StatusCode: 403}, true},
		{"azure 429", &azcore.ResponseError{StatusCode: 429}, false},
		{"azure 500", &azcore.ResponseError{StatusCode: 500}, false},
		{"azure 400", &azcore.ResponseError{StatusCode: 400}, false},
		{"wrapped azure 401", fmt.Errorf("getUsageReport: %w", &azcore.ResponseError{StatusCode: 401}), true},
		{"wrapped azure 429", fmt.Errorf("rate limited: %w", &azcore.ResponseError{StatusCode: 429}), false},
		{"azure cred 401 (expired secret)", &azidentity.AuthenticationFailedError{RawResponse: &http.Response{StatusCode: 401}}, true},
		{"azure cred 403", &azidentity.AuthenticationFailedError{RawResponse: &http.Response{StatusCode: 403}}, true},
		{"azure cred 500 transient", &azidentity.AuthenticationFailedError{RawResponse: &http.Response{StatusCode: 500}}, false},
		{"azure cred no response", &azidentity.AuthenticationFailedError{}, false},
		{"wrapped azure cred 401", fmt.Errorf("failed to get alerts: failed to get next page: %w", &azidentity.AuthenticationFailedError{RawResponse: &http.Response{StatusCode: 401}}), true},
		{"aws AccessDenied", &smithy.GenericAPIError{Code: "AccessDenied"}, true},
		{"aws AuthFailure", &smithy.GenericAPIError{Code: "AuthFailure"}, true},
		{"aws UnauthorizedOperation", &smithy.GenericAPIError{Code: "UnauthorizedOperation"}, true},
		{"aws ExpiredToken", &smithy.GenericAPIError{Code: "ExpiredToken"}, true},
		{"aws InvalidAccessKeyId", &smithy.GenericAPIError{Code: "InvalidAccessKeyId"}, true},
		{"aws ThrottlingException", &smithy.GenericAPIError{Code: "ThrottlingException"}, false},
		{"aws RequestLimitExceeded", &smithy.GenericAPIError{Code: "RequestLimitExceeded"}, false},
		{"wrapped aws AccessDenied", fmt.Errorf("scan: %w", &smithy.GenericAPIError{Code: "AccessDenied"}), true},
		{"gcp googleapi 403", &googleapi.Error{Code: 403}, true},
		{"gcp googleapi 401", &googleapi.Error{Code: 401}, true},
		{"gcp googleapi 404", &googleapi.Error{Code: 404}, false},
		{"gcp grpc PermissionDenied", status.Error(codes.PermissionDenied, "caller lacks permission"), true},
		{"gcp grpc Unauthenticated", status.Error(codes.Unauthenticated, "invalid credentials"), true},
		{"gcp grpc Unavailable transient", status.Error(codes.Unavailable, "backend unavailable"), false},
		{"wrapped gcp googleapi 403", fmt.Errorf("listEvents: %w", &googleapi.Error{Code: 403}), true},
		{"plain error", errors.New("boom"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isAuthFailure(tc.err); got != tc.want {
				t.Errorf("isAuthFailure(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestIsPermanentProviderError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		// Regression: the Azure SDK renders credential 401s as "RESPONSE 401:",
		// which the httpStatusCodePattern regex ("returned NNN:") never matched,
		// so expired-secret errors were retried forever. Now caught by type.
		{"azure expired secret (cred 401)", fmt.Errorf("failed to get alerts: failed to get next page: %w", &azidentity.AuthenticationFailedError{RawResponse: &http.Response{StatusCode: 401}}), true},
		{"azure api 401", &azcore.ResponseError{StatusCode: 401}, true},
		{"aws expired token", &smithy.GenericAPIError{Code: "ExpiredToken"}, true},
		{"gcp googleapi 403", &googleapi.Error{Code: 403}, true},
		{"gcp grpc PermissionDenied", status.Error(codes.PermissionDenied, "denied"), true},
		{"provider 4xx string (regex path)", errors.New("provider returned 404: not found"), true},
		{"provider 429 is transient", errors.New("provider returned 429: too many requests"), false},
		{"provider 408 is transient", errors.New("provider returned 408: request timeout"), false},
		{"provider 5xx string is transient", errors.New("provider returned 503: unavailable"), false},
		{"plain transient error", errors.New("connection reset by peer"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isPermanentProviderError(tc.err); got != tc.want {
				t.Errorf("isPermanentProviderError(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}
