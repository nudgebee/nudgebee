package azure

import (
	"errors"
	"net/http"
	"nudgebee/collector/cloud/common"
	"nudgebee/collector/cloud/providers"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
)

// TestWrapAzureCostFailure pins which sentinel each kind of cost failure
// carries, since that alone decides whether the account shows disconnected and
// whether the job is dead-lettered.
func TestWrapAzureCostFailure(t *testing.T) {
	raw := &azcore.ResponseError{StatusCode: http.StatusForbidden, ErrorCode: "AuthorizationFailed"}

	tests := []struct {
		name              string
		kind              common.AzureCostFailureKind
		wantNotConfigured bool
		wantCredentialOK  bool
	}{
		{name: "broken credential carries no sentinel", kind: common.AzureCostCredentialBroken},
		{name: "cost unavailable is not configured", kind: common.AzureCostUnavailable, wantNotConfigured: true},
		{name: "transient keeps the credential-ok sentinel", kind: common.AzureCostTransient, wantCredentialOK: true},
		{name: "unexpected keeps the credential-ok sentinel", kind: common.AzureCostUnexpected, wantCredentialOK: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := wrapAzureCostFailure(common.AzureCostFailure{Kind: tt.kind, Message: "plain reason"}, raw)
			if errors.Is(got, providers.ErrCostNotConfigured) != tt.wantNotConfigured {
				t.Errorf("errors.Is(ErrCostNotConfigured) = %v, want %v", !tt.wantNotConfigured, tt.wantNotConfigured)
			}
			if errors.Is(got, providers.ErrCostFetchFailedCredentialOK) != tt.wantCredentialOK {
				t.Errorf("errors.Is(ErrCostFetchFailedCredentialOK) = %v, want %v", !tt.wantCredentialOK, tt.wantCredentialOK)
			}
			var respErr *azcore.ResponseError
			if !errors.As(got, &respErr) {
				t.Error("original Azure error was dropped from the chain")
			}
			// The Spends error column shows err.Error(): it must be the plain
			// reason alone, not a sentinel prefix plus the raw Azure response.
			if got.Error() != "plain reason" {
				t.Errorf("error text = %q, want only the plain-language reason", got.Error())
			}
		})
	}
}
