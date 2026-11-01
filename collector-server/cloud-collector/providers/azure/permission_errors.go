package azure

import (
	"errors"
	"fmt"
	"nudgebee/collector/cloud/common"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
)

// isAzurePermissionErrorCode reports whether an Azure API error code indicates a
// permission/access issue: either a rejected credential or a missing role. The
// code lists live in common so the cost-error classifier uses the same ones.
func isAzurePermissionErrorCode(code string) bool {
	return common.IsAzureCredentialErrorCode(code) || common.IsAzureAccessDeniedErrorCode(code)
}

// IsAzurePermissionError inspects an error returned by an Azure SDK call and
// determines whether it represents a permission/access error.
// Returns the API operation (extracted from request URL), error code, message, and whether it is a permission error.
func IsAzurePermissionError(err error) (apiOperation, errorCode, errorMessage string, ok bool) {
	if err == nil {
		return "", "", "", false
	}

	var respErr *azcore.ResponseError
	if errors.As(err, &respErr) {
		errorCode = respErr.ErrorCode
		errorMessage = respErr.Error()

		// Extract API operation from the HTTP request URL path
		if respErr.RawResponse != nil && respErr.RawResponse.Request != nil {
			apiOperation = extractAzureOperation(respErr.RawResponse.Request.URL.Path)
		}

		// Check by error code
		if isAzurePermissionErrorCode(errorCode) {
			return apiOperation, errorCode, errorMessage, true
		}

		// Check by HTTP status code (403 Forbidden)
		if respErr.StatusCode == 403 {
			if errorCode == "" {
				errorCode = fmt.Sprintf("HTTP_%d", respErr.StatusCode)
			}
			return apiOperation, errorCode, errorMessage, true
		}

		// 401 Unauthorized can also indicate permission issues
		if respErr.StatusCode == 401 {
			if errorCode == "" {
				errorCode = fmt.Sprintf("HTTP_%d", respErr.StatusCode)
			}
			return apiOperation, errorCode, errorMessage, true
		}
	}

	return "", "", "", false
}

// extractAzureOperation extracts the resource provider and type from an Azure REST API URL path.
// e.g., "/subscriptions/.../providers/Microsoft.Compute/virtualMachines/..." → "Microsoft.Compute/virtualMachines"
func extractAzureOperation(path string) string {
	idx := strings.Index(path, "/providers/")
	if idx == -1 {
		return path
	}
	// Get everything after "/providers/"
	remainder := path[idx+len("/providers/"):]

	// Take the first two path segments (provider/resourceType)
	parts := strings.SplitN(remainder, "/", 3)
	if len(parts) >= 2 {
		return parts[0] + "/" + parts[1]
	}
	if len(parts) == 1 {
		return parts[0]
	}
	return remainder
}
