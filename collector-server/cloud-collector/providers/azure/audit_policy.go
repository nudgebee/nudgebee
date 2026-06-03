package azure

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"nudgebee/collector/cloud/providers"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
)

// AzureAuditInfo carries account and service context for the Azure SDK audit policy.
type AzureAuditInfo struct {
	TenantID       string
	CloudAccountID string
	AccountNumber  string
	ServiceName    string
}

type azureAuditCtxKey struct{}

// WithAzureAuditInfo stores audit info in the context for the SDK policy.
func WithAzureAuditInfo(ctx context.Context, info *AzureAuditInfo) context.Context {
	return context.WithValue(ctx, azureAuditCtxKey{}, info)
}

func getAzureAuditInfo(ctx context.Context) *AzureAuditInfo {
	if v, ok := ctx.Value(azureAuditCtxKey{}).(*AzureAuditInfo); ok {
		return v
	}
	return nil
}

// permissionAuditPolicy is an Azure SDK policy that intercepts HTTP responses
// to catch permission errors at the transport level, before service code can swallow them.
type permissionAuditPolicy struct {
	info *AzureAuditInfo
}

func (p *permissionAuditPolicy) Do(req *policy.Request) (*http.Response, error) {
	resp, err := req.Next()

	if resp != nil && (resp.StatusCode == 401 || resp.StatusCode == 403) {
		apiOperation := extractAzureOperation(req.Raw().URL.Path)
		errorCode, errorMessage := extractAzureErrorFromResponse(resp)

		if errorCode == "" {
			errorCode = fmt.Sprintf("HTTP_%d", resp.StatusCode)
		}
		if errorMessage == "" {
			errorMessage = resp.Status
		}

		providers.GetPermissionAuditCollector().Record(providers.PermissionAuditRecord{
			TenantID:       p.info.TenantID,
			CloudAccountID: p.info.CloudAccountID,
			AccountNumber:  p.info.AccountNumber,
			CloudProvider:  "Azure",
			ServiceName:    p.info.ServiceName,
			APIOperation:   apiOperation,
			ErrorCode:      errorCode,
			ErrorMessage:   errorMessage,
			OccurredAt:     time.Now(),
		})
	}

	return resp, err
}

// extractAzureErrorFromResponse reads the error code and message from an Azure error response body.
// It restores the body after reading so downstream code can still consume it.
func extractAzureErrorFromResponse(resp *http.Response) (errorCode, errorMessage string) {
	if resp.Body == nil {
		return "", ""
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		slog.Warn("failed to read azure error response body", "error", err)
		return "", ""
	}
	// Restore the body for downstream consumers
	resp.Body = io.NopCloser(bytes.NewReader(body))

	var envelope struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &envelope) == nil && envelope.Error.Code != "" {
		return envelope.Error.Code, envelope.Error.Message
	}

	return "", string(body)
}

// azureAuditClientOptions returns ARM client options with the permission audit policy attached.
func azureAuditClientOptions(info *AzureAuditInfo) *arm.ClientOptions {
	if info == nil {
		return nil
	}
	return &arm.ClientOptions{
		ClientOptions: policy.ClientOptions{
			PerCallPolicies: []policy.Policy{
				&permissionAuditPolicy{info: info},
			},
		},
	}
}
