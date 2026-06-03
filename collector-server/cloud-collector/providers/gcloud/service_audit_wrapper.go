package gcloud

import (
	"context"
	"nudgebee/collector/cloud/providers"
	"nudgebee/collector/cloud/security"
	"time"
)

// GCPAuditInfo carries account and service context for GCP permission audit recording.
type GCPAuditInfo struct {
	TenantID       string
	CloudAccountID string
	AccountNumber  string
	ServiceName    string
}

type gcpAuditCtxKey struct{}

// WithGCPAuditInfo stores audit info in the context.
func WithGCPAuditInfo(ctx context.Context, info *GCPAuditInfo) context.Context {
	return context.WithValue(ctx, gcpAuditCtxKey{}, info)
}

// GetGCPAuditInfo retrieves audit info from the context.
func GetGCPAuditInfo(ctx context.Context) *GCPAuditInfo {
	if v, ok := ctx.Value(gcpAuditCtxKey{}).(*GCPAuditInfo); ok {
		return v
	}
	return nil
}

// auditedGcloudService wraps a gcloudService and records permission errors
// to the PermissionAuditCollector without modifying any individual service file.
type auditedGcloudService struct {
	inner       gcloudService
	serviceName string
}

// gcpAuditContextWrapper enriches a CloudProviderContext with GCP permission audit info.
type gcpAuditContextWrapper struct {
	providers.CloudProviderContext
	enrichedCtx context.Context
}

func (w *gcpAuditContextWrapper) GetContext() context.Context {
	return w.enrichedCtx
}

// enrichContext injects permission audit info into the CloudProviderContext so that
// service code can record permission errors for swallowed errors.
func (a *auditedGcloudService) enrichContext(ctx providers.CloudProviderContext, account providers.Account) providers.CloudProviderContext {
	return &gcpAuditContextWrapper{
		CloudProviderContext: ctx,
		enrichedCtx: WithGCPAuditInfo(ctx.GetContext(), &GCPAuditInfo{
			TenantID:       extractGcloudTenantID(ctx),
			CloudAccountID: account.ID,
			AccountNumber:  account.AccountNumber,
			ServiceName:    a.serviceName,
		}),
	}
}

func (a *auditedGcloudService) GetMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	enrichedCtx := a.enrichContext(ctx, account)
	resp, err := a.inner.GetMetrices(enrichedCtx, account, filter)
	a.checkAndRecord(ctx, account, filter.Region, "GetMetrices", err)
	return resp, err
}

func (a *auditedGcloudService) GetResources(ctx providers.CloudProviderContext, account providers.Account, region string) ([]providers.Resource, error) {
	enrichedCtx := a.enrichContext(ctx, account)
	resp, err := a.inner.GetResources(enrichedCtx, account, region)
	a.checkAndRecord(ctx, account, region, "GetResources", err)
	return resp, err
}

func (a *auditedGcloudService) GetRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, existingResources []providers.Resource) ([]providers.Recommendation, error) {
	enrichedCtx := a.enrichContext(ctx, account)
	resp, err := a.inner.GetRecommendations(enrichedCtx, account, filter, existingResources)
	a.checkAndRecord(ctx, account, "", "GetRecommendations", err)
	return resp, err
}

func (a *auditedGcloudService) ApplyRecommendation(ctx providers.CloudProviderContext, account providers.Account, recommendation providers.Recommendation) error {
	enrichedCtx := a.enrichContext(ctx, account)
	err := a.inner.ApplyRecommendation(enrichedCtx, account, recommendation)
	a.checkAndRecord(ctx, account, recommendation.ResourceRegion, "ApplyRecommendation", err)
	return err
}

func (a *auditedGcloudService) ApplyCommand(ctx providers.CloudProviderContext, account providers.Account, command providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
	enrichedCtx := a.enrichContext(ctx, account)
	resp, err := a.inner.ApplyCommand(enrichedCtx, account, command)
	a.checkAndRecord(ctx, account, command.Region, "ApplyCommand", err)
	return resp, err
}

func (a *auditedGcloudService) GetLogFilter(ctx providers.CloudProviderContext, account providers.Account, resourceId string) string {
	return a.inner.GetLogFilter(ctx, account, resourceId)
}

func (a *auditedGcloudService) checkAndRecord(ctx providers.CloudProviderContext, account providers.Account, region, wrapperMethod string, err error) {
	if err == nil {
		return
	}

	apiOperation, errorCode, errorMessage, isPermErr := IsGCPPermissionError(err)
	if !isPermErr {
		return
	}

	tenantID := extractGcloudTenantID(ctx)

	providers.GetPermissionAuditCollector().Record(providers.PermissionAuditRecord{
		TenantID:       tenantID,
		CloudAccountID: account.ID,
		AccountNumber:  account.AccountNumber,
		CloudProvider:  "GCP",
		ServiceName:    a.serviceName,
		APIOperation:   apiOperation,
		WrapperMethod:  wrapperMethod,
		ErrorCode:      errorCode,
		ErrorMessage:   errorMessage,
		Region:         region,
		OccurredAt:     time.Now(),
	})
}

// RecordGCPPermissionError records a permission error from within a GCP service method.
// It reads audit info from the context (injected by enrichContext).
// Call this at points where errors are caught and swallowed internally.
func RecordGCPPermissionError(ctx providers.CloudProviderContext, err error) {
	if err == nil {
		return
	}

	apiOperation, errorCode, errorMessage, isPermErr := IsGCPPermissionError(err)
	if !isPermErr {
		return
	}

	info := GetGCPAuditInfo(ctx.GetContext())
	if info == nil {
		ctx.GetLogger().Warn("RecordGCPPermissionError called with context missing GCPAuditInfo")
		return
	}

	providers.GetPermissionAuditCollector().Record(providers.PermissionAuditRecord{
		TenantID:       info.TenantID,
		CloudAccountID: info.CloudAccountID,
		AccountNumber:  info.AccountNumber,
		CloudProvider:  "GCP",
		ServiceName:    info.ServiceName,
		APIOperation:   apiOperation,
		ErrorCode:      errorCode,
		ErrorMessage:   errorMessage,
		OccurredAt:     time.Now(),
	})
}

func extractGcloudTenantID(ctx providers.CloudProviderContext) string {
	if rc, ok := ctx.(*security.RequestContext); ok {
		return rc.GetSecurityContext().GetTenantId()
	}
	if w, ok := ctx.(*gcpAuditContextWrapper); ok {
		return extractGcloudTenantID(w.CloudProviderContext)
	}
	return ""
}
