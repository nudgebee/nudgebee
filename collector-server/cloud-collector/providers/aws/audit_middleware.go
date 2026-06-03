package aws

import (
	"context"
	"errors"
	"nudgebee/collector/cloud/providers"
	"nudgebee/collector/cloud/security"
	"time"

	awsmiddleware "github.com/aws/aws-sdk-go-v2/aws/middleware"
	"github.com/aws/smithy-go"
	"github.com/aws/smithy-go/middleware"
	smithyhttp "github.com/aws/smithy-go/transport/http"
)

// PermAuditInfo carries account and service context for the SDK audit middleware.
type PermAuditInfo struct {
	TenantID       string
	CloudAccountID string
	AccountNumber  string
	CloudProvider  string
	ServiceName    string
}

type permAuditCtxKey struct{}

// WithPermAuditInfo stores audit info in the context for the SDK middleware.
func WithPermAuditInfo(ctx context.Context, info *PermAuditInfo) context.Context {
	return context.WithValue(ctx, permAuditCtxKey{}, info)
}

func getPermAuditInfo(ctx context.Context) *PermAuditInfo {
	if v, ok := ctx.Value(permAuditCtxKey{}).(*PermAuditInfo); ok {
		return v
	}
	return nil
}

// auditContextWrapper enriches a CloudProviderContext with permission audit info.
type auditContextWrapper struct {
	providers.CloudProviderContext
	enrichedCtx context.Context
}

func (w *auditContextWrapper) GetContext() context.Context {
	return w.enrichedCtx
}

// GetSecurityContext delegates to the embedded CloudProviderContext
func (w *auditContextWrapper) GetSecurityContext() *security.SecurityContext {
	return w.CloudProviderContext.GetSecurityContext()
}

// permissionAuditSDKMiddleware intercepts AWS API responses to catch permission errors.
type permissionAuditSDKMiddleware struct {
	info *PermAuditInfo
}

func (m *permissionAuditSDKMiddleware) ID() string {
	return "PermissionAuditMiddleware"
}

func (m *permissionAuditSDKMiddleware) HandleDeserialize(
	ctx context.Context,
	in middleware.DeserializeInput,
	next middleware.DeserializeHandler,
) (middleware.DeserializeOutput, middleware.Metadata, error) {
	out, meta, err := next.HandleDeserialize(ctx, in)
	if err == nil {
		return out, meta, nil
	}

	// Check if the error is a recognized permission error
	apiOperation, errorCode, errorMessage, isPermErr := IsAWSPermissionError(err)
	if !isPermErr {
		// Fallback: check HTTP status code for 401/403 that may use unrecognized error codes
		if resp, ok := out.RawResponse.(*smithyhttp.Response); ok {
			if resp.StatusCode == 401 || resp.StatusCode == 403 {
				isPermErr = true
				apiOperation = middleware.GetOperationName(ctx)
				// Extract error code and message from smithy error
				var apiErr smithy.APIError
				if errors.As(err, &apiErr) {
					errorCode = apiErr.ErrorCode()
					errorMessage = apiErr.ErrorMessage()
				} else {
					errorCode = resp.Status
					errorMessage = err.Error()
				}
			}
		}
	}

	if !isPermErr {
		return out, meta, err
	}

	if apiOperation == "" {
		apiOperation = middleware.GetOperationName(ctx)
	}

	region := awsmiddleware.GetRegion(ctx)

	providers.GetPermissionAuditCollector().Record(providers.PermissionAuditRecord{
		TenantID:       m.info.TenantID,
		CloudAccountID: m.info.CloudAccountID,
		AccountNumber:  m.info.AccountNumber,
		CloudProvider:  m.info.CloudProvider,
		ServiceName:    m.info.ServiceName,
		APIOperation:   apiOperation,
		ErrorCode:      errorCode,
		ErrorMessage:   errorMessage,
		Region:         region,
		OccurredAt:     time.Now(),
	})

	return out, meta, err
}

// addPermissionAuditMiddleware returns an APIOptions function that adds the audit middleware.
func addPermissionAuditMiddleware(info *PermAuditInfo) func(*middleware.Stack) error {
	return func(stack *middleware.Stack) error {
		return stack.Deserialize.Add(&permissionAuditSDKMiddleware{info: info}, middleware.After)
	}
}
