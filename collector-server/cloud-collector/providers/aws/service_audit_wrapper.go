package aws

import (
	"nudgebee/collector/cloud/providers"
	"nudgebee/collector/cloud/security"
	"time"
)

// auditedAwsService wraps an awsService and records permission errors
// to the PermissionAuditCollector without modifying any individual service file.
type auditedAwsService struct {
	inner       awsService
	serviceName string
}

// enrichContext injects permission audit info into the CloudProviderContext so that
// the SDK middleware in getAwsConfigFromAccount can record internal permission errors.
func (a *auditedAwsService) enrichContext(ctx providers.CloudProviderContext, account providers.Account) providers.CloudProviderContext {
	return &auditContextWrapper{
		CloudProviderContext: ctx,
		enrichedCtx: WithPermAuditInfo(ctx.GetContext(), &PermAuditInfo{
			TenantID:       extractTenantID(ctx),
			CloudAccountID: account.ID,
			AccountNumber:  account.AccountNumber,
			CloudProvider:  "AWS",
			ServiceName:    a.serviceName,
		}),
	}
}

func (a *auditedAwsService) QueryMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	enrichedCtx := a.enrichContext(ctx, account)
	resp, err := a.inner.QueryMetrices(enrichedCtx, account, filter)
	a.checkAndRecord(ctx, account, filter.Region, "QueryMetrices", err)
	return resp, err
}

func (a *auditedAwsService) GetResources(ctx providers.CloudProviderContext, account providers.Account, region string) ([]providers.Resource, error) {
	enrichedCtx := a.enrichContext(ctx, account)
	resp, err := a.inner.GetResources(enrichedCtx, account, region)
	a.checkAndRecord(ctx, account, region, "GetResources", err)
	return resp, err
}

func (a *auditedAwsService) GetRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, existingResources []providers.Resource) ([]providers.Recommendation, error) {
	enrichedCtx := a.enrichContext(ctx, account)
	resp, err := a.inner.GetRecommendations(enrichedCtx, account, filter, existingResources)
	a.checkAndRecord(ctx, account, "", "GetRecommendations", err)
	return resp, err
}

func (a *auditedAwsService) ApplyRecommendation(ctx providers.CloudProviderContext, account providers.Account, recommendation providers.Recommendation) error {
	enrichedCtx := a.enrichContext(ctx, account)
	err := a.inner.ApplyRecommendation(enrichedCtx, account, recommendation)
	a.checkAndRecord(ctx, account, recommendation.ResourceRegion, "ApplyRecommendation", err)
	return err
}

func (a *auditedAwsService) ApplyCommand(ctx providers.CloudProviderContext, account providers.Account, command providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
	enrichedCtx := a.enrichContext(ctx, account)
	resp, err := a.inner.ApplyCommand(enrichedCtx, account, command)
	a.checkAndRecord(ctx, account, command.Region, "ApplyCommand", err)
	return resp, err
}

func (a *auditedAwsService) GetLogGroupName(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (string, error) {
	enrichedCtx := a.enrichContext(ctx, account)
	resp, err := a.inner.GetLogGroupName(enrichedCtx, account, region, resourceId)
	a.checkAndRecord(ctx, account, region, "GetLogGroupName", err)
	return resp, err
}

func (a *auditedAwsService) GetServiceMap(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (providers.ServiceMapApplication, error) {
	enrichedCtx := a.enrichContext(ctx, account)
	resp, err := a.inner.GetServiceMap(enrichedCtx, account, region, resourceId)
	a.checkAndRecord(ctx, account, region, "GetServiceMap", err)
	return resp, err
}

func (a *auditedAwsService) ListMetrics(ctx providers.CloudProviderContext, account providers.Account, request providers.ListMetricsRequest) (providers.ListMetricsResponse, error) {
	return a.inner.ListMetrics(ctx, account, request)
}

func (a *auditedAwsService) GetResourcesByIds(ctx providers.CloudProviderContext, account providers.Account, region string, resourceIds []string) ([]providers.Resource, error) {
	enrichedCtx := a.enrichContext(ctx, account)
	resp, err := a.inner.GetResourcesByIds(enrichedCtx, account, region, resourceIds)
	a.checkAndRecord(ctx, account, region, "GetResourcesByIds", err)
	return resp, err
}

func (a *auditedAwsService) IsGlobal() bool {
	return a.inner.IsGlobal()
}

func (a *auditedAwsService) DescribeResource(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (*ResourceMetadata, error) {
	enrichedCtx := a.enrichContext(ctx, account)
	resp, err := a.inner.DescribeResource(enrichedCtx, account, region, resourceId)
	a.checkAndRecord(ctx, account, region, "DescribeResource", err)
	return resp, err
}

func (a *auditedAwsService) checkAndRecord(ctx providers.CloudProviderContext, account providers.Account, region, wrapperMethod string, err error) {
	if err == nil {
		return
	}

	apiOperation, errorCode, errorMessage, isPermErr := IsAWSPermissionError(err)
	if !isPermErr {
		return
	}

	tenantID := extractTenantID(ctx)

	providers.GetPermissionAuditCollector().Record(providers.PermissionAuditRecord{
		TenantID:       tenantID,
		CloudAccountID: account.ID,
		AccountNumber:  account.AccountNumber,
		CloudProvider:  "AWS",
		ServiceName:    a.serviceName,
		APIOperation:   apiOperation,
		WrapperMethod:  wrapperMethod,
		ErrorCode:      errorCode,
		ErrorMessage:   errorMessage,
		Region:         region,
		OccurredAt:     time.Now(),
	})
}

func extractTenantID(ctx providers.CloudProviderContext) string {
	if rc, ok := ctx.(*security.RequestContext); ok {
		return rc.GetSecurityContext().GetTenantId()
	}
	return ""
}
