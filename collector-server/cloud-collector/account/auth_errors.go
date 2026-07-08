package account

import (
	"errors"
	"nudgebee/collector/cloud/providers/gcloud"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/aws/smithy-go"
	smithyhttp "github.com/aws/smithy-go/transport/http"
)

// isAuthFailure reports whether err indicates a credential or permission
// problem (revoked SP, expired key, missing role) requiring operator action.
// 429s, 5xx, timeouts, and unknown errors return false and are treated as
// transient — the agent stays CONNECTED so the per-feature error is recorded
// in connection_status but no false "agent disconnected" alert fires.
func isAuthFailure(err error) bool {
	if err == nil {
		return false
	}
	// Azure credential-acquisition failures (expired/revoked client secret, bad
	// cert) surface as *azidentity.AuthenticationFailedError. This type has no
	// Unwrap method and does NOT unwrap to azcore.ResponseError, so it must be
	// matched explicitly — it only exposes the token-endpoint response via
	// RawResponse. A 401/403 there (e.g. AADSTS7000222 "client secret expired")
	// is permanent; a 5xx or missing response is transient.
	var authErr *azidentity.AuthenticationFailedError
	if errors.As(err, &authErr) {
		if authErr.RawResponse != nil {
			sc := authErr.RawResponse.StatusCode
			return sc == 401 || sc == 403
		}
		return false
	}
	var azErr *azcore.ResponseError
	if errors.As(err, &azErr) {
		return azErr.StatusCode == 401 || azErr.StatusCode == 403
	}
	var httpErr *smithyhttp.ResponseError
	if errors.As(err, &httpErr) {
		sc := httpErr.HTTPStatusCode()
		if sc == 401 || sc == 403 {
			return true
		}
	}
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.ErrorCode() {
		case "AccessDenied", "AuthFailure", "UnauthorizedOperation",
			"InvalidClientTokenId", "SignatureDoesNotMatch",
			"ExpiredToken", "TokenExpired", "InvalidAccessKeyId":
			return true
		}
	}
	// GCP: googleapi.Error 401/403 (REST APIs) and gRPC Unauthenticated/
	// PermissionDenied (Pub/Sub, Logging, Monitoring). These render as
	// "googleapi: Error 403:" or carry no HTTP status at all, so neither the
	// string regex nor the checks above catch them.
	if _, _, _, ok := gcloud.IsGCPPermissionError(err); ok {
		return true
	}
	return false
}
