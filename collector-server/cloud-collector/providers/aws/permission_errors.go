package aws

import (
	"errors"
	"fmt"

	"nudgebee/collector/cloud/providers"

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

// assumeRoleRefusal reports whether err is AWS refusing sts:AssumeRole for the
// account's role, and returns AWS's own message for the refusal. A refused
// AssumeRole means the credential itself is unusable — every call for the
// account fails the same way, whatever was being called at the time.
//
// The SDK fetches credentials inside the call that needs them, so the refusal
// arrives nested in that call's error:
//
//	operation error S3: GetObject, get identity: get credentials: failed to
//	refresh cached credentials, operation error STS: AssumeRole, ... AccessDenied
//
// IsAWSPermissionError pairs the outermost operation with the innermost code
// and so reads this as "S3 GetObject was denied". Walking every nested
// operation is what tells a refused role apart from a refused bucket.
//
// Only AccessDenied counts, and only as AssumeRole's own answer. A throttled or
// unreachable STS says nothing about whether the role still works, and neither
// does a failure to fetch the caller's own credentials, which nests a further
// operation under AssumeRole because the request was never sent.
func assumeRoleRefusal(err error) (awsMessage string, ok bool) {
	for err != nil {
		var opErr *smithy.OperationError
		if !errors.As(err, &opErr) {
			return "", false
		}
		if opErr.ServiceID == "STS" && opErr.OperationName == "AssumeRole" {
			var inner *smithy.OperationError
			if errors.As(opErr.Err, &inner) {
				return "", false
			}
			var apiErr smithy.APIError
			if errors.As(opErr.Err, &apiErr) && apiErr.ErrorCode() == "AccessDenied" {
				return apiErr.ErrorMessage(), true
			}
			return "", false
		}
		err = opErr.Err
	}
	return "", false
}

// assumeRoleRefusedError is a refused sts:AssumeRole in plain language. Its text
// is what the account's health page shows, so it names the role and what to
// check; the original AWS error stays reachable via errors.Is/As.
type assumeRoleRefusedError struct {
	message string
	cause   error
}

func (e *assumeRoleRefusedError) Error() string { return e.message }

func (e *assumeRoleRefusedError) Unwrap() error { return e.cause }

// explainAssumeRoleRefusal rewrites a refused sts:AssumeRole into the reason the
// account's health page shows. Any other error, and nil, is returned unchanged.
//
// The wording does not say whose side changed: AWS answers the same way when
// the role is deleted, when its trust policy or external ID no longer matches,
// and when the calling identity itself is no longer allowed to assume roles.
// AWS's message names both the caller and the role, so it is kept.
func explainAssumeRoleRefusal(account providers.Account, err error) error {
	awsMessage, refused := assumeRoleRefusal(err)
	if !refused {
		return err
	}
	role := "the account's IAM role"
	if account.AssumeRole != nil && *account.AssumeRole != "" {
		role = "the IAM role " + *account.AssumeRole
	}
	message := fmt.Sprintf("AWS refused the request to assume %s, so nothing can be collected from this account. "+
		"Check that the role still exists, that its trust policy still trusts the caller named below, and that the external ID matches.", role)
	if awsMessage != "" {
		message += " AWS said: " + awsMessage
	}
	return &assumeRoleRefusedError{message: message, cause: err}
}
