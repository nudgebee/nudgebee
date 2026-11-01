package common

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
)

// azureCredentialErrorCodes are Azure error codes meaning the credential itself
// was rejected — the principal cannot act on the subscription at all, whatever
// roles it holds. Shared with the permission audit (via the Is* helpers below)
// so the two never disagree on what these codes mean.
var azureCredentialErrorCodes = map[string]bool{
	"InvalidAuthenticationTokenTenant": true,
	"AuthenticationFailed":             true,
	"ExpiredAuthenticationToken":       true,
	"InvalidAuthenticationToken":       true,
}

// azureAccessDeniedErrorCodes are Azure error codes meaning the credential is
// valid but lacks a role for the call that was made.
var azureAccessDeniedErrorCodes = map[string]bool{
	"AuthorizationFailed":             true,
	"AuthorizationPermissionMismatch": true,
	"InsufficientAccountPermissions":  true,
	"AuthorizationPermissionDenied":   true,
	"LinkedAuthorizationFailed":       true,
	"RBACAccessDenied":                true,
	"BillingAccessDenied":             true,
}

// IsAzureCredentialErrorCode reports whether an Azure error code means the
// credential itself was rejected.
func IsAzureCredentialErrorCode(code string) bool { return azureCredentialErrorCodes[code] }

// IsAzureAccessDeniedErrorCode reports whether an Azure error code means the
// credential is valid but lacks a role for the call.
func IsAzureAccessDeniedErrorCode(code string) bool { return azureAccessDeniedErrorCodes[code] }

// AzureCostFailureKind says what a failed Azure Cost Management call means for
// the account as a whole.
type AzureCostFailureKind int

const (
	// AzureCostCredentialBroken: the credential cannot read the subscription at
	// all. The account is genuinely disconnected.
	AzureCostCredentialBroken AzureCostFailureKind = iota + 1
	// AzureCostTransient: Azure rate-limited or failed the request, or access
	// could not be confirmed this run. The next sync retries.
	AzureCostTransient
	// AzureCostUnavailable: the credential works, but cost data is blocked by a
	// known, lasting cause that the customer (or their billing admin or partner)
	// has to fix.
	AzureCostUnavailable
	// AzureCostUnexpected: the credential works, but the cost call failed for a
	// reason not recognised here.
	AzureCostUnexpected
)

// AzureCostFailure is the classified outcome of a failed cost call, with a
// plain-language message naming the cause and who can fix it.
type AzureCostFailure struct {
	Kind    AzureCostFailureKind
	Message string
}

// ClassifyAzureCostError explains a failed Azure Cost Management call.
//
// A 403 on the cost call looks the same whether the principal lacks only cost
// read or has lost all access to the subscription. probe tells them apart: it
// must attempt a cheap read of the subscription (listing resource groups) with
// the same credential and return that call's error. It is only called when the
// cost error alone is not conclusive. A probe that fails for any reason other
// than a denial is treated as transient, never as a broken credential, so one
// network blip cannot disconnect an account.
//
// Error codes and their meaning follow Microsoft's "Troubleshoot common Cost
// Management errors" page.
func ClassifyAzureCostError(err error, subscriptionID string, probe func() error) AzureCostFailure {
	if isAzureCredentialRejected(err) {
		return AzureCostFailure{
			Kind:    AzureCostCredentialBroken,
			Message: "Azure rejected the service principal's credentials (for example, an expired client secret)." + azureCodeSuffix(err),
		}
	}
	if isAzureTransient(err) {
		return AzureCostFailure{
			Kind:    AzureCostTransient,
			Message: fmt.Sprintf("Azure is temporarily rate-limiting or failing cost requests for subscription %s. The next daily sync retries.", subscriptionID) + azureCodeSuffix(err),
		}
	}

	if probe != nil {
		if probeErr := probe(); probeErr != nil {
			if isAzureCredentialRejected(probeErr) || isAzureAccessDenied(probeErr) || azureErrorCode(probeErr) == "SubscriptionNotFound" {
				return AzureCostFailure{
					Kind:    AzureCostCredentialBroken,
					Message: fmt.Sprintf("The service principal cannot read subscription %s. Check that its client secret is valid and that it has the Reader role there.", subscriptionID) + azureCodeSuffix(probeErr),
				}
			}
			return AzureCostFailure{
				Kind:    AzureCostTransient,
				Message: fmt.Sprintf("Cost data could not be read for subscription %s, and access to the subscription could not be confirmed this run. The next daily sync retries.", subscriptionID),
			}
		}
	}

	code := azureErrorCode(err)
	switch code {
	case "AccountCostDisabled":
		return AzureCostFailure{
			Kind:    AzureCostUnavailable,
			Message: fmt.Sprintf("Cost data for subscription %s is turned off in the Enterprise Agreement billing settings. An enterprise admin must enable \"Account owners can view charges\".", subscriptionID) + azureCodeSuffix(err),
		}
	case "DepartmentCostDisabled":
		return AzureCostFailure{
			Kind:    AzureCostUnavailable,
			Message: fmt.Sprintf("Cost data for subscription %s is turned off in the Enterprise Agreement billing settings. An enterprise admin must enable \"Department admins can view charges\".", subscriptionID) + azureCodeSuffix(err),
		}
	case "IndirectCostDisabled":
		return AzureCostFailure{
			Kind:    AzureCostUnavailable,
			Message: fmt.Sprintf("Cost data for subscription %s is unavailable because the Microsoft partner has not published pricing for this enrollment. Contact the partner.", subscriptionID) + azureCodeSuffix(err),
		}
	case "DisallowedOperation", "SubscriptionTypeNotSupported":
		return AzureCostFailure{
			Kind:    AzureCostUnavailable,
			Message: fmt.Sprintf("Azure does not provide cost data for the offer type of subscription %s.", subscriptionID) + azureCodeSuffix(err),
		}
	case "SubscriptionNotFound":
		// The probe succeeded, so the subscription exists and is readable —
		// Cost Management just has nothing for it yet.
		return AzureCostFailure{
			Kind:    AzureCostUnavailable,
			Message: fmt.Sprintf("Azure has no cost data for subscription %s yet. New subscriptions can take up to 48 hours.", subscriptionID) + azureCodeSuffix(err),
		}
	}
	if isAzureAccessDenied(err) {
		return AzureCostFailure{
			Kind: AzureCostUnavailable,
			Message: fmt.Sprintf("The service principal can read resources in subscription %s but not cost data. Assign it the Reader or Cost Management Reader role on the subscription. "+
				"If it already has one, the Enterprise Agreement \"view charges\" setting may be blocking cost access.", subscriptionID) + azureCodeSuffix(err),
		}
	}
	return AzureCostFailure{
		Kind:    AzureCostUnexpected,
		Message: fmt.Sprintf("Cost data could not be read for subscription %s.", subscriptionID) + azureCodeSuffix(err),
	}
}

func isAzureCredentialRejected(err error) bool {
	var authErr *azidentity.AuthenticationFailedError
	if errors.As(err, &authErr) {
		return true
	}
	return IsAzureCredentialErrorCode(azureErrorCode(err))
}

func isAzureAccessDenied(err error) bool {
	var respErr *azcore.ResponseError
	if !errors.As(err, &respErr) {
		return false
	}
	return IsAzureAccessDeniedErrorCode(respErr.ErrorCode) ||
		respErr.StatusCode == http.StatusUnauthorized || respErr.StatusCode == http.StatusForbidden
}

func isAzureTransient(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var respErr *azcore.ResponseError
	if !errors.As(err, &respErr) {
		return false
	}
	switch respErr.ErrorCode {
	case "ResourceRequestsThrottled", "GatewayTimeout", "ServerTimeout":
		return true
	}
	return respErr.StatusCode == http.StatusTooManyRequests || respErr.StatusCode >= http.StatusInternalServerError
}

func azureErrorCode(err error) string {
	var respErr *azcore.ResponseError
	if errors.As(err, &respErr) {
		return respErr.ErrorCode
	}
	return ""
}

func azureCodeSuffix(err error) string {
	if code := azureErrorCode(err); code != "" {
		return fmt.Sprintf(" (Azure error code: %s)", code)
	}
	return ""
}
