package cloudfoundry

import (
	"errors"
	"fmt"
	"nudgebee/collector/cloud/providers"
	"strings"
	"time"
)

// cfServiceMap maps CF service names to their implementations.
var cfServiceMap = map[string]cfService{
	ServiceNameApps:             &cfAppsService{},
	ServiceNameSpaces:           &cfSpacesService{},
	ServiceNameOrganizations:    &cfOrganizationsService{},
	ServiceNameRoutes:           &cfRoutesService{},
	ServiceNameServiceInstances: &cfServiceInstancesService{},
	ServiceNameBuilds:           &cfBuildsService{},
	ServiceNameDeployments:      &cfDeploymentsService{},
	ServiceNameTasks:            &cfTasksService{},
	ServiceNameServiceBindings:  &cfServiceBindingsService{},
}

type cfProvider struct{}

func (p *cfProvider) Name() string {
	return "CloudFoundry"
}

func (p *cfProvider) ListResources(ctx providers.CloudProviderContext, account providers.Account, query providers.ListResourceRequest) (providers.ListResourcesResponse, error) {
	serviceName := strings.ToLower(query.ServiceName)
	service, ok := cfServiceMap[serviceName]
	if !ok {
		return providers.ListResourcesResponse{Items: []providers.Resource{}}, nil
	}

	client, err := newCFClient(ctx, account)
	if err != nil {
		return providers.ListResourcesResponse{}, fmt.Errorf("failed to create CF client: %w", err)
	}

	resources, err := service.GetResources(ctx, client, "")
	if err != nil {
		return providers.ListResourcesResponse{}, err
	}

	return providers.ListResourcesResponse{Items: resources}, nil
}

func (p *cfProvider) QueryMetrices(ctx providers.CloudProviderContext, account providers.Account, query providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	if strings.ToLower(query.ServiceName) != ServiceNameApps {
		return providers.QueryMetricsResponse{Items: []providers.MetricItem{}}, nil
	}

	client, err := newCFClient(ctx, account)
	if err != nil {
		return providers.QueryMetricsResponse{}, fmt.Errorf("failed to create CF client: %w", err)
	}

	var allMetrics []providers.MetricItem
	for _, resourceID := range query.ResourceIds {
		metrics, err := getAppMetrics(ctx, client, resourceID, query.ServiceName)
		if err != nil {
			ctx.GetLogger().Warn("failed to get metrics for app", "app_guid", resourceID, "error", err)
			continue
		}
		allMetrics = append(allMetrics, metrics...)
	}

	now := time.Now()
	startDate := now.Add(-5 * time.Minute)
	if query.StartDate != nil {
		startDate = *query.StartDate
	}

	return providers.QueryMetricsResponse{
		Items:     allMetrics,
		StartDate: startDate,
		EndDate:   now,
		Step:      time.Minute,
	}, nil
}

func (p *cfProvider) ListMetrics(_ providers.CloudProviderContext, _ providers.Account, request providers.ListMetricsRequest) (providers.ListMetricsResponse, error) {
	if strings.ToLower(request.ServiceName) != ServiceNameApps {
		return providers.ListMetricsResponse{Metrics: []providers.AvailableMetric{}}, nil
	}

	return providers.ListMetricsResponse{
		Metrics: []providers.AvailableMetric{
			{Name: "cpu_usage", Namespace: "cloudfoundry/apps", Statistics: []string{"Average"}},
			{Name: "memory_usage_percent", Namespace: "cloudfoundry/apps", Statistics: []string{"Average"}},
			{Name: "memory_usage_bytes", Namespace: "cloudfoundry/apps", Statistics: []string{"Average"}},
			{Name: "disk_usage_percent", Namespace: "cloudfoundry/apps", Statistics: []string{"Average"}},
			{Name: "disk_usage_bytes", Namespace: "cloudfoundry/apps", Statistics: []string{"Average"}},
			{Name: "instance_count", Namespace: "cloudfoundry/apps", Statistics: []string{"Sum"}},
		},
	}, nil
}

func (p *cfProvider) ListEvents(ctx providers.CloudProviderContext, account providers.Account, query providers.ListEventRequest) (providers.ListEventResponse, error) {
	logger := ctx.GetLogger()

	client, err := newCFClient(ctx, account)
	if err != nil {
		return providers.ListEventResponse{}, fmt.Errorf("failed to create CF client: %w", err)
	}

	events, err := getAuditEvents(ctx, client, query)
	if err != nil {
		logger.Error("CloudFoundry: failed to fetch audit events", "error", err.Error())
		return providers.ListEventResponse{}, err
	}

	// Generate synthetic health events only for unscoped periodic polls,
	// not for resource-specific or historical queries which would return
	// unrelated current-state alerts and incur unnecessary API cost.
	if len(query.ResourceIds) == 0 {
		healthEvents := checkAppHealth(ctx, client)
		events = append(events, healthEvents...)

		logErrorEvents := checkAppLogErrors(ctx, client)
		events = append(events, logErrorEvents...)

		buildFailures := checkBuildFailures(ctx, client, query)
		events = append(events, buildFailures...)

		taskFailures := checkTaskFailures(ctx, client, query)
		events = append(events, taskFailures...)

		siFailures := checkServiceInstanceFailures(ctx, client)
		events = append(events, siFailures...)
	}

	// Enrich HIGH + MEDIUM severity events with evidence context
	events = enrichEvents(ctx, client, events)

	// Compute event summary for resource auto-discovery
	summary := computeEventSummary(events)

	logger.Info("CloudFoundry: ListEvents completed", "eventCount", len(events), "summaryCount", len(summary))
	return providers.ListEventResponse{
		Items:   events,
		Summary: summary,
	}, nil
}

func (p *cfProvider) ApplyCommand(ctx providers.CloudProviderContext, account providers.Account, command providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
	if strings.ToLower(command.ServiceName) != ServiceNameApps {
		return providers.ApplyCommandResponse{}, fmt.Errorf("commands are only supported for apps service")
	}

	client, err := newCFClient(ctx, account)
	if err != nil {
		return providers.ApplyCommandResponse{}, fmt.Errorf("failed to create CF client: %w", err)
	}

	switch strings.ToLower(command.Command) {
	case "start":
		_, err := client.post(fmt.Sprintf("/v3/apps/%s/actions/start", command.ResourceId), nil)
		if err != nil {
			return providers.ApplyCommandResponse{Success: false, Message: err.Error()}, err
		}
		return providers.ApplyCommandResponse{Success: true, Message: "App started successfully"}, nil

	case "stop":
		_, err := client.post(fmt.Sprintf("/v3/apps/%s/actions/stop", command.ResourceId), nil)
		if err != nil {
			return providers.ApplyCommandResponse{Success: false, Message: err.Error()}, err
		}
		return providers.ApplyCommandResponse{Success: true, Message: "App stopped successfully"}, nil

	case "restart":
		_, err := client.post(fmt.Sprintf("/v3/apps/%s/actions/restart", command.ResourceId), nil)
		if err != nil {
			return providers.ApplyCommandResponse{Success: false, Message: err.Error()}, err
		}
		return providers.ApplyCommandResponse{Success: true, Message: "App restarted successfully"}, nil

	case "scale":
		scaleBody := map[string]any{}
		if instances, ok := command.Args["instances"]; ok {
			scaleBody["instances"] = instances
		}
		if memoryMB, ok := command.Args["memory_in_mb"]; ok {
			scaleBody["memory_in_mb"] = memoryMB
		}
		if diskMB, ok := command.Args["disk_in_mb"]; ok {
			scaleBody["disk_in_mb"] = diskMB
		}

		// Scale operates on the web process
		processGUID := command.ResourceId // Default to using app GUID
		if pguid, ok := command.Args["process_guid"]; ok {
			processGUID = fmt.Sprintf("%v", pguid)
		}

		_, err := client.post(fmt.Sprintf("/v3/processes/%s/actions/scale", processGUID), scaleBody)
		if err != nil {
			return providers.ApplyCommandResponse{Success: false, Message: err.Error()}, err
		}
		return providers.ApplyCommandResponse{Success: true, Message: "App scaled successfully"}, nil

	default:
		return providers.ApplyCommandResponse{}, fmt.Errorf("unsupported command: %s (supported: start, stop, restart, scale)", command.Command)
	}
}

func (p *cfProvider) ApplyRecommendation(_ providers.CloudProviderContext, _ providers.Account, _ providers.Recommendation) error {
	return fmt.Errorf("recommendations not supported for Cloud Foundry")
}

func (p *cfProvider) ListRecommendations(_ providers.CloudProviderContext, _ providers.Account, _ providers.ListRecommendationsRequest, _ []providers.Resource) (providers.ListRecommendationsResponse, error) {
	return providers.ListRecommendationsResponse{Items: []providers.Recommendation{}}, nil
}

func (p *cfProvider) ListSupportedRecommendations(_ providers.CloudProviderContext) []providers.ListSupportedRecommendationsResponse {
	return []providers.ListSupportedRecommendationsResponse{}
}

func (p *cfProvider) ExecuteCliCommand(_ providers.CloudProviderContext, _ providers.Account, _ string) (string, error) {
	return "", fmt.Errorf("CLI command execution not yet supported for Cloud Foundry")
}

func (p *cfProvider) QueryLogs(ctx providers.CloudProviderContext, account providers.Account, query providers.QueryLogsRequest) (providers.QueryLogsResponse, error) {
	client, err := newCFClient(ctx, account)
	if err != nil {
		return providers.QueryLogsResponse{
			Results: []providers.LogMessage{},
			Status:  "Failed",
		}, fmt.Errorf("failed to create CF client: %w", err)
	}

	return queryLogs(ctx, client, query)
}

func (p *cfProvider) GetUsageReport(_ providers.CloudProviderContext, _ providers.Account, _ time.Month, _ int) (providers.GetUsageReportResponse, error) {
	return providers.GetUsageReportResponse{Items: []providers.UsageReportItem{}}, nil
}

func (p *cfProvider) QueryServiceMap(_ providers.CloudProviderContext, _ providers.Account, _ providers.QueryServiceMapRequest) (providers.QueryServiceMapResponse, error) {
	return providers.QueryServiceMapResponse{}, errors.ErrUnsupported
}

func (p *cfProvider) ListEventRules(_ providers.CloudProviderContext, _ providers.Account) (providers.ListEventRules, error) {
	return providers.ListEventRules{
		Items: getCFEventRules(),
	}, nil
}

func (p *cfProvider) QueryDatabasePerformance(_ providers.CloudProviderContext, _ providers.Account, _ providers.DatabasePerformanceRequest) (providers.DatabasePerformanceResponse, error) {
	return providers.DatabasePerformanceResponse{}, nil
}

func init() {
	providers.RegisterProvider(&cfProvider{})
}
