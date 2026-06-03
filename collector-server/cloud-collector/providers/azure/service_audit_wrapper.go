package azure

import (
	"context"
	"nudgebee/collector/cloud/providers"
	"nudgebee/collector/cloud/security"
	"time"
)

// auditedAzureService wraps an azureService and records permission errors
// to the PermissionAuditCollector without modifying any individual service file.
type auditedAzureService struct {
	inner       azureService
	serviceName string
}

// azureAuditContextWrapper enriches a CloudProviderContext with Azure permission audit info.
type azureAuditContextWrapper struct {
	providers.CloudProviderContext
	enrichedCtx context.Context
}

func (w *azureAuditContextWrapper) GetContext() context.Context {
	return w.enrichedCtx
}

// enrichContext injects permission audit info into the CloudProviderContext so that
// the SDK policy in azureAuditClientOptions can record internal permission errors.
func (a *auditedAzureService) enrichContext(ctx providers.CloudProviderContext, account providers.Account) providers.CloudProviderContext {
	return &azureAuditContextWrapper{
		CloudProviderContext: ctx,
		enrichedCtx: WithAzureAuditInfo(ctx.GetContext(), &AzureAuditInfo{
			TenantID:       extractAzureTenantID(ctx),
			CloudAccountID: account.ID,
			AccountNumber:  account.AccountNumber,
			ServiceName:    a.serviceName,
		}),
	}
}

func (a *auditedAzureService) Name() string {
	return a.inner.Name()
}

func (a *auditedAzureService) Scope() ServiceScope {
	return a.inner.Scope()
}

func (a *auditedAzureService) QueryMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	enrichedCtx := a.enrichContext(ctx, account)
	resp, err := a.inner.QueryMetrices(enrichedCtx, account, filter)
	a.checkAndRecord(ctx, account, filter.Region, "QueryMetrices", err)
	return resp, err
}

func (a *auditedAzureService) GetResources(ctx providers.CloudProviderContext, account providers.Account, region string) ([]providers.Resource, error) {
	enrichedCtx := a.enrichContext(ctx, account)
	resp, err := a.inner.GetResources(enrichedCtx, account, region)
	a.checkAndRecord(ctx, account, region, "GetResources", err)
	return resp, err
}

func (a *auditedAzureService) GetRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, existingResources []providers.Resource) ([]providers.Recommendation, error) {
	enrichedCtx := a.enrichContext(ctx, account)
	resp, err := a.inner.GetRecommendations(enrichedCtx, account, filter, existingResources)
	a.checkAndRecord(ctx, account, "", "GetRecommendations", err)
	return resp, err
}

func (a *auditedAzureService) ApplyRecommendation(ctx providers.CloudProviderContext, account providers.Account, recommendation providers.Recommendation) error {
	enrichedCtx := a.enrichContext(ctx, account)
	err := a.inner.ApplyRecommendation(enrichedCtx, account, recommendation)
	a.checkAndRecord(ctx, account, recommendation.ResourceRegion, "ApplyRecommendation", err)
	return err
}

func (a *auditedAzureService) ApplyCommand(ctx providers.CloudProviderContext, account providers.Account, command providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
	enrichedCtx := a.enrichContext(ctx, account)
	resp, err := a.inner.ApplyCommand(enrichedCtx, account, command)
	a.checkAndRecord(ctx, account, command.Region, "ApplyCommand", err)
	return resp, err
}

func (a *auditedAzureService) GetLogGroupName(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (string, error) {
	enrichedCtx := a.enrichContext(ctx, account)
	resp, err := a.inner.GetLogGroupName(enrichedCtx, account, region, resourceId)
	a.checkAndRecord(ctx, account, region, "GetLogGroupName", err)
	return resp, err
}

func (a *auditedAzureService) GetServiceMap(ctx providers.CloudProviderContext, account providers.Account, resource providers.Resource) (providers.ServiceMapApplication, error) {
	enrichedCtx := a.enrichContext(ctx, account)
	resp, err := a.inner.GetServiceMap(enrichedCtx, account, resource)
	a.checkAndRecord(ctx, account, resource.Region, "GetServiceMap", err)
	return resp, err
}

func (a *auditedAzureService) checkAndRecord(ctx providers.CloudProviderContext, account providers.Account, region, wrapperMethod string, err error) {
	if err == nil {
		return
	}

	apiOperation, errorCode, errorMessage, isPermErr := IsAzurePermissionError(err)
	if !isPermErr {
		return
	}

	tenantID := extractAzureTenantID(ctx)

	providers.GetPermissionAuditCollector().Record(providers.PermissionAuditRecord{
		TenantID:       tenantID,
		CloudAccountID: account.ID,
		AccountNumber:  account.AccountNumber,
		CloudProvider:  "Azure",
		ServiceName:    a.serviceName,
		APIOperation:   apiOperation,
		WrapperMethod:  wrapperMethod,
		ErrorCode:      errorCode,
		ErrorMessage:   errorMessage,
		Region:         region,
		OccurredAt:     time.Now(),
	})
}

// recordProviderPermissionError records permission errors from provider-level methods
// (like ListEvents) that are not wrapped by auditedAzureService.
func recordProviderPermissionError(ctx providers.CloudProviderContext, account providers.Account, serviceName, wrapperMethod string, err error) {
	if err == nil {
		return
	}

	apiOperation, errorCode, errorMessage, isPermErr := IsAzurePermissionError(err)
	if !isPermErr {
		return
	}

	providers.GetPermissionAuditCollector().Record(providers.PermissionAuditRecord{
		TenantID:       extractAzureTenantID(ctx),
		CloudAccountID: account.ID,
		AccountNumber:  account.AccountNumber,
		CloudProvider:  "Azure",
		ServiceName:    serviceName,
		APIOperation:   apiOperation,
		WrapperMethod:  wrapperMethod,
		ErrorCode:      errorCode,
		ErrorMessage:   errorMessage,
		OccurredAt:     time.Now(),
	})
}

func extractAzureTenantID(ctx providers.CloudProviderContext) string {
	if rc, ok := ctx.(*security.RequestContext); ok {
		return rc.GetSecurityContext().GetTenantId()
	}
	// Check if it's an audit context wrapper (for nested contexts)
	if w, ok := ctx.(*azureAuditContextWrapper); ok {
		return extractAzureTenantID(w.CloudProviderContext)
	}
	return ""
}
