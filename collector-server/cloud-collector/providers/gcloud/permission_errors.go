package gcloud

import (
	"errors"
	"regexp"
	"strings"

	"google.golang.org/api/googleapi"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// permissionRegex extracts the permission string from GCP error messages.
// Matches patterns like: "Caller does not have permission compute.instances.list"
// or "Permission 'storage.buckets.list' denied"
var permissionRegex = regexp.MustCompile(`(?:permission|Permission)\s+['"]?([a-zA-Z0-9_.]+(?:\.[a-zA-Z0-9_]+)+)['"]?`)

// IsGCPPermissionError inspects an error returned by a GCP SDK call and
// determines whether it represents a permission/access error.
// Returns the API operation (best-effort from error message), error code, message, and whether it is a permission error.
func IsGCPPermissionError(err error) (apiOperation, errorCode, errorMessage string, ok bool) {
	if err == nil {
		return "", "", "", false
	}

	// Check for googleapi.Error (REST-based APIs: Compute, Storage, BigQuery, etc.)
	var gapiErr *googleapi.Error
	if errors.As(err, &gapiErr) {
		if gapiErr.Code == 403 || gapiErr.Code == 401 {
			errorMessage = gapiErr.Message
			errorCode = extractGCPErrorCode(gapiErr)
			apiOperation = extractPermissionFromMessage(gapiErr.Message)
			return apiOperation, errorCode, errorMessage, true
		}
	}

	// Check for gRPC status errors (gRPC-based APIs: Pub/Sub, Logging, Monitoring, etc.)
	if st, stOk := status.FromError(err); stOk {
		if st.Code() == codes.PermissionDenied || st.Code() == codes.Unauthenticated {
			errorMessage = st.Message()
			errorCode = st.Code().String()
			apiOperation = extractPermissionFromMessage(st.Message())
			return apiOperation, errorCode, errorMessage, true
		}
	}

	return "", "", "", false
}

// extractGCPErrorCode returns a meaningful error code from a googleapi.Error.
func extractGCPErrorCode(err *googleapi.Error) string {
	if len(err.Errors) > 0 {
		if err.Errors[0].Reason != "" {
			return err.Errors[0].Reason
		}
	}
	if err.Code == 403 {
		return "PERMISSION_DENIED"
	}
	if err.Code == 401 {
		return "UNAUTHENTICATED"
	}
	return "UNKNOWN"
}

// extractPermissionFromMessage attempts to extract the GCP permission string
// (e.g., "compute.instances.list") from an error message.
func extractPermissionFromMessage(msg string) string {
	matches := permissionRegex.FindStringSubmatch(msg)
	if len(matches) >= 2 {
		return matches[1]
	}
	// Fallback: look for common GCP permission patterns in the message
	if idx := strings.Index(msg, "does not have"); idx != -1 {
		// Try to find the permission after "does not have"
		rest := msg[idx:]
		parts := strings.Fields(rest)
		for _, p := range parts {
			if strings.Count(p, ".") >= 2 {
				return strings.Trim(p, ".'\"")
			}
		}
	}
	return ""
}
