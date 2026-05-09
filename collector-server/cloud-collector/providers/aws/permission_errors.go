package aws

import (
	"errors"

	"github.com/aws/smithy-go"
)

// awsPermissionErrorCodes lists AWS API error codes that indicate permission/access issues.
var awsPermissionErrorCodes = map[string]bool{
	"AccessDenied":                true,
	"AccessDeniedException":       true,
	"AuthFailure":                 true,
	"UnauthorizedOperation":       true,
	"UnrecognizedClientException": true,
	"ExpiredTokenException":       true,
	"InvalidClientTokenId":        true,
	"UnauthorizedAccess":          true,
	"InvalidIdentityToken":        true,
	"InvalidAccessException":      true,
	"AuthorizationError":          true,
}

// IsAWSPermissionError inspects an error returned by an AWS SDK call and
// determines whether it represents a permission/access error.
// Returns the API operation name, error code, error message, and whether it is a permission error.
func IsAWSPermissionError(err error) (apiOperation, errorCode, errorMessage string, ok bool) {
	if err == nil {
		return "", "", "", false
	}

	// Extract the operation name from smithy.OperationError
	var opErr *smithy.OperationError
	if errors.As(err, &opErr) {
		apiOperation = opErr.Operation()
	}

	// Check for GenericAPIError (most common for permission errors)
	var apiErr *smithy.GenericAPIError
	if errors.As(err, &apiErr) {
		if awsPermissionErrorCodes[apiErr.ErrorCode()] {
			return apiOperation, apiErr.ErrorCode(), apiErr.ErrorMessage(), true
		}
	}

	// Check for smithy.APIError interface (covers other error types)
	var smithyAPIErr smithy.APIError
	if errors.As(err, &smithyAPIErr) {
		if awsPermissionErrorCodes[smithyAPIErr.ErrorCode()] {
			return apiOperation, smithyAPIErr.ErrorCode(), smithyAPIErr.ErrorMessage(), true
		}
	}

	return "", "", "", false
}
