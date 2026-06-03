package gcloud

import (
	"errors"
	"fmt"
	"nudgebee/collector/cloud/common"
	"nudgebee/collector/cloud/providers"
	"os"
	"strings"
	"time"

	"github.com/google/shlex"
)

// gcloudServiceMap maps Google Cloud official service names to service implementations.
// Service names are case-insensitive and match what appears in GCP Billing Export.
var gcloudServiceMap = map[string]gcloudService{
	"compute engine":              &computeEngineService{},
	"cloud storage":               &cloudStorageService{},
	"bigquery":                    &bigQueryService{},
	"cloud sql":                   &cloudSQLService{},
	"kubernetes engine":           &gkeService{},
	"cloud functions":             &cloudFunctionsService{},
	"cloud run":                   &cloudRunService{},
	"cloud pub/sub":               &pubSubService{},
	"cloud monitoring":            &cloudMonitoringService{},
	"networking":                  &networkingService{},
	"vm manager":                  &vmManagerService{},
	"vertex ai":                   &vertexAIService{},
	"gemini api":                  &geminiService{},
	"cloud load balancing":        &cloudLoadBalancingService{},
	"recommender":                 &gcloudRecommenderService{},
	"compute.googleapis.com/disk": &diskService{},
	"compute.googleapis.com/networkinterface": &networkInterfaceService{},
	"artifact registry":                       &artifactRegistryService{},
	"iam":                                     &iamService{},
}

func GetGcloudService(serviceName string) (gcloudService, bool) {
	service, ok := gcloudServiceMap[strings.ToLower(serviceName)]
	return service, ok
}

// GetProviderForPubSub returns a gcloudProvider instance for use by Pub/Sub event processor
func GetProviderForPubSub() *gcloudProvider {
	return &gcloudProvider{}
}

type gcloudProvider struct {
}

func (a *gcloudProvider) QueryLogs(ctx providers.CloudProviderContext, account providers.Account, query providers.QueryLogsRequest) (providers.QueryLogsResponse, error) {
	return queryGcloudLogs(ctx, account, query)
}

func (a *gcloudProvider) QueryMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	service, ok := GetGcloudService(filter.ServiceName)
	if !ok {
		return providers.QueryMetricsResponse{
			Items: []providers.MetricItem{},
		}, nil
	}
	return service.GetMetrices(ctx, account, filter)
}

func (a *gcloudProvider) ListMetrics(ctx providers.CloudProviderContext, account providers.Account, request providers.ListMetricsRequest) (providers.ListMetricsResponse, error) {
	cacheKey := "gcp:" + account.ID + ":" + request.ServiceName
	if cached := providers.GetCachedMetrics(cacheKey); cached != nil {
		return *cached, nil
	}

	resp, err := listGcloudMonitoringMetricsDynamic(ctx, account, request.ServiceName)
	if err == nil && len(resp.Metrics) > 0 {
		providers.SetCachedMetrics(cacheKey, resp)
		return resp, nil
	}
	if err != nil {
		ctx.GetLogger().Warn("dynamic GCP ListMetrics failed, falling back to static", "service", request.ServiceName, "error", err)
	}
	resp, err = listGcloudMonitoringMetrics(request)
	if err == nil {
		providers.SetCachedMetrics(cacheKey, resp)
	}
	return resp, err
}

func (a *gcloudProvider) ListResources(ctx providers.CloudProviderContext, account providers.Account, query providers.ListResourceRequest) (providers.ListResourcesResponse, error) {
	resources := []providers.Resource{}
	regions := query.Regions
	serviceName := query.ServiceName
	if serviceName == "" {
		return providers.ListResourcesResponse{
			Items: resources,
		}, errors.New("gcloud: service_name is required")
	}

	// Pre-check: skip services whose GCP API is not enabled on this project.
	// This avoids wasted API calls and false-positive permission error recordings.
	session, err := getGcloudSessionFromAccount(ctx, account)
	if err != nil {
		return providers.ListResourcesResponse{Items: resources}, fmt.Errorf("failed to get gcloud session: %w", err)
	}
	enabledAPIs, err := getEnabledAPIs(ctx, session)
	if err != nil {
		ctx.GetLogger().Warn("failed to fetch enabled APIs, proceeding with all services", "error", err, "project", session.ProjectId)
	}
	if !isServiceEnabled(serviceName, enabledAPIs) {
		ctx.GetLogger().Info("skipping service — required GCP API not enabled", "service", serviceName, "project", session.ProjectId)
		return providers.ListResourcesResponse{Items: resources}, nil
	}

	if len(regions) == 0 {
		// Fetch all regions if not specified
		gcloudRegions, err := getAllRegions(ctx, account)
		if err != nil {
			ctx.GetLogger().Error("failed to fetch regions", "error", err, "accountNumber", account.AccountNumber)
			return providers.ListResourcesResponse{
				Items: resources,
			}, err
		}
		regions = gcloudRegions
	}

	// Fetch alert policies once upfront to avoid N API calls (one per service)
	// This is cached for the duration of this ListResources call
	alertPolicies, err := fetchAlertPoliciesOnce(ctx, account)
	if err != nil {
		ctx.GetLogger().Warn("failed to fetch alert policies, resources will be returned without policies attached", "error", err)
		// Continue without alert policies rather than failing the entire operation
	}

	// Track seen resources to avoid duplicates across regions
	// GCP has global resources (Storage, BigQuery, Gemini, Vertex AI) that return the same resource for every region
	seenResources := make(map[string]bool)
	var duplicatesSkipped int

	for _, regionName := range regions {
		ctx.GetLogger().Info("fetching resources", "service", serviceName, "region", regionName)
		service, ok := GetGcloudService(serviceName)
		if !ok {
			return providers.ListResourcesResponse{
				Items: resources,
			}, nil
		}
		serviceResources, err := service.GetResources(ctx, account, regionName)
		if err != nil {
			ctx.GetLogger().Error("failed to fetch resources", "error", err, "service", serviceName, "region", regionName)
			return providers.ListResourcesResponse{
				Items: resources,
			}, err
		}

		// Deduplicate resources by their unique identifier (Id + ServiceName + Type)
		for _, resource := range serviceResources {
			// Create unique key: service:type:resourceId
			uniqueKey := fmt.Sprintf("%s:%s:%s", resource.ServiceName, resource.Type, resource.Id)
			if seenResources[uniqueKey] {
				duplicatesSkipped++
				ctx.GetLogger().Debug("skipping duplicate resource", "id", resource.Id, "name", resource.Name, "region", regionName, "service", serviceName)
				continue
			}
			seenResources[uniqueKey] = true
			resources = append(resources, resource)
		}
	}

	if duplicatesSkipped > 0 {
		ctx.GetLogger().Info("deduplicated resources across regions", "service", serviceName, "duplicatesSkipped", duplicatesSkipped, "uniqueResources", len(resources))
	}

	// Attach alert policies to resources using the pre-fetched policies
	if len(resources) > 0 && len(alertPolicies) > 0 {
		if err := attachAlertPoliciesToResourcesWithCache(ctx, resources, alertPolicies); err != nil {
			ctx.GetLogger().Warn("failed to attach alert policies to resources", "error", err, "service", serviceName)
			// Don't fail the entire operation if alert policy attachment fails
		}
	}

	return providers.ListResourcesResponse{
		Items: resources,
	}, nil
}

func (a *gcloudProvider) GetUsageReport(ctx providers.CloudProviderContext, account providers.Account, month time.Month, year int) (providers.GetUsageReportResponse, error) {
	return getGcloudUsageReport(ctx, account, month, year)
}

func (a *gcloudProvider) ListRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, existingResources []providers.Resource) (providers.ListRecommendationsResponse, error) {
	// Pre-check: skip if the required GCP API is not enabled
	session, err := getGcloudSessionFromAccount(ctx, account)
	if err != nil {
		return providers.ListRecommendationsResponse{}, fmt.Errorf("failed to get gcloud session: %w", err)
	}
	enabledAPIs, err := getEnabledAPIs(ctx, session)
	if err != nil {
		ctx.GetLogger().Warn("failed to fetch enabled APIs for recommendations, proceeding", "error", err, "project", session.ProjectId)
	}
	if !isServiceEnabled(filter.ServiceName, enabledAPIs) {
		ctx.GetLogger().Info("skipping recommendations — required GCP API not enabled", "service", filter.ServiceName, "project", session.ProjectId)
		return providers.ListRecommendationsResponse{Items: []providers.Recommendation{}}, nil
	}

	service, ok := GetGcloudService(filter.ServiceName)
	if !ok {
		return providers.ListRecommendationsResponse{
			Items: []providers.Recommendation{},
		}, nil
	}
	recommendations, err := service.GetRecommendations(ctx, account, filter, existingResources)
	if err != nil {
		ctx.GetLogger().Error("failed to get recommendations", "error", err, "service", filter.ServiceName)
		return providers.ListRecommendationsResponse{}, err
	}
	return providers.ListRecommendationsResponse{Items: recommendations}, nil
}

func (a *gcloudProvider) ListSupportedRecommendations(ctx providers.CloudProviderContext) []providers.ListSupportedRecommendationsResponse {
	return []providers.ListSupportedRecommendationsResponse{}
}

func (a *gcloudProvider) ListEvents(ctx providers.CloudProviderContext, account providers.Account, query providers.ListEventRequest) (providers.ListEventResponse, error) {
	logger := ctx.GetLogger()

	logger.Info("fetching GCP incidents",
		"account", account.AccountNumber,
		"method", "v3_alerts_api")

	// Primary method: GCP v3 Alerts API
	// This is the source of truth - GCP already evaluates policies and creates incidents
	incidents, err := getGCPIncidentsV3(ctx, &account, query)
	if err != nil {
		// Only fall back to Cloud Logging when v3 API errors (not when it returns empty)
		logger.Warn("failed to fetch incidents from v3 API, falling back to Cloud Logging",
			"error", err,
			"account", account.AccountNumber)

		// Fallback to Cloud Logging method
		// Requires: notification channels configured, Cloud Logging enabled
		return getGCPAlertIncidents(ctx, account, query)
	}

	// v3 API succeeded - return results (even if empty, which means no alerts firing)
	logger.Info("fetched GCP incidents via v3 API",
		"count", len(incidents.Items),
		"account", account.AccountNumber)

	return incidents, nil
}

func (a *gcloudProvider) ApplyRecommendation(ctx providers.CloudProviderContext, account providers.Account, recommendation providers.Recommendation) error {
	// Check if this is an alarm/alert policy recommendation
	if _, ok := recommendation.Data["alarm_config"]; ok {
		ctx.GetLogger().Info("gcp: applying alarm recommendation",
			"rule_name", recommendation.RuleName,
			"resource_id", recommendation.ResourceId)
		return CreateGCPAlertPolicyFromRecommendation(ctx, account, recommendation)
	}

	// Check if there's a service-specific implementation
	service, ok := GetGcloudService(recommendation.ResourceServiceName)
	if !ok {
		ctx.GetLogger().Warn("gcp: no ApplyRecommendation implementation",
			"service", recommendation.ResourceServiceName,
			"rule_name", recommendation.RuleName)
		return fmt.Errorf("gcloud: service '%s' not found for applying recommendation", recommendation.ResourceServiceName)
	}
	return service.ApplyRecommendation(ctx, account, recommendation)
}

func (a *gcloudProvider) ApplyCommand(ctx providers.CloudProviderContext, account providers.Account, command providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
	if command.ServiceName == "" {
		return providers.ApplyCommandResponse{}, errors.New("gcloud: service_name is required for applying command")
	}
	service, ok := GetGcloudService(command.ServiceName)
	if !ok {
		return providers.ApplyCommandResponse{}, fmt.Errorf("gcloud: service '%s' not found for applying command", command.ServiceName)
	}

	return service.ApplyCommand(ctx, account, command)
}

var gcloudBlockedCommands = []string{
	"auth",
	"config set",
	"config unset",
	"init",
}

func (a *gcloudProvider) ExecuteCliCommand(ctx providers.CloudProviderContext, account providers.Account, command string) (string, error) {
	command = strings.TrimSpace(command)
	if !strings.HasPrefix(command, "gcloud ") {
		command = "gcloud " + command
	}

	if err := common.ValidateCliCommand(command, gcloudBlockedCommands); err != nil {
		return "", err
	}

	session, err := getGcloudSessionFromAccount(ctx, account)
	if err != nil {
		return "", fmt.Errorf("failed to get access secret: %w", err)
	}

	// Create a temporary file for the service account key
	tmpFile, err := os.CreateTemp("", "gcloud-key-*.json")
	if err != nil {
		return "", fmt.Errorf("failed to create temporary file: %w", err)
	}
	defer func() {
		if err := os.Remove(tmpFile.Name()); err != nil {
			ctx.GetLogger().Error("gcloud: failed to remove temporary key file", "error", err, "file", tmpFile.Name())
		}
	}()

	if err := os.Chmod(tmpFile.Name(), 0600); err != nil {
		return "", fmt.Errorf("failed to set permissions on temporary file: %w", err)
	}

	if _, err := tmpFile.Write([]byte(session.AccountCred)); err != nil {
		return "", fmt.Errorf("failed to write to temporary file: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return "", fmt.Errorf("failed to close temporary file: %w", err)
	}

	// Use a per-request gcloud config directory so concurrent requests for different
	// accounts don't overwrite each other's active gcloud account. Without this,
	// SecureExecute's minimal environment (PATH only) can cause gcloud to share a
	// single config dir, and the auth command's state may not be visible to the
	// subsequent user command ("no active account selected").
	configDir, err := os.MkdirTemp("", "gcloud-config-*")
	if err != nil {
		return "", fmt.Errorf("failed to create gcloud config directory: %w", err)
	}
	defer func() {
		if err := os.RemoveAll(configDir); err != nil {
			ctx.GetLogger().Error("gcloud: failed to remove config directory", "error", err, "dir", configDir)
		}
	}()

	// Build a base environment shared by both auth and user commands.
	// HOME is required for gcloud to resolve its config directory via ~/.config/gcloud.
	// CLOUDSDK_CONFIG pins gcloud to our isolated per-request directory.
	baseEnv := []string{
		"CLOUDSDK_CONFIG=" + configDir,
	}
	if home := os.Getenv("HOME"); home != "" {
		baseEnv = append(baseEnv, "HOME="+home)
	} else {
		// Fallback to the temp config dir as a safe HOME to ensure dependent tools have a writable area
		baseEnv = append(baseEnv, "HOME="+configDir)
	}

	// Authenticate using the key file
	authCmd := fmt.Sprintf("gcloud auth activate-service-account --key-file %s --project %s --account %s", tmpFile.Name(), session.ProjectId, session.ClientEmail)
	_, stderr, err := common.SecureExecute(ctx.GetContext(), common.SecureCommandOptions{
		Command: authCmd,
		Env:     baseEnv,
		Timeout: 30 * time.Second,
	})
	if err != nil {
		ctx.GetLogger().Error("gcloud auth command failed", "error", err, "stderr", stderr)
		return "", fmt.Errorf("gcloud auth failed: %w", err)
	}

	// Handle backslash line continuations before parsing
	cleanCommand := strings.ReplaceAll(command, "\\\r\n", " ")
	cleanCommand = strings.ReplaceAll(cleanCommand, "\\\n", " ")

	// Execute the user command, choosing the executor based on pipelining.
	// Reuse the same baseEnv (with CLOUDSDK_CONFIG) so gcloud finds the auth state
	// from the activate-service-account call above.
	var stdout string
	opts := common.SecureCommandOptions{
		Command: cleanCommand,
		Env:     append(baseEnv, "GOOGLE_APPLICATION_CREDENTIALS="+tmpFile.Name()),
	}

	// Determine if the command uses a pipe (pipeline)
	// We use shlex to properly handle quoted strings so that a pipe character inside a quote
	// doesn't trigger pipeline execution.
	usePipeline := false
	execArgs, err := shlex.Split(cleanCommand)
	if err == nil {
		for _, arg := range execArgs {
			if arg == "|" {
				usePipeline = true
				break
			}
		}
	} else {
		// If parsing fails, we fall back to a naive check
		if strings.Contains(cleanCommand, "|") {
			usePipeline = true
		}
	}

	if usePipeline {
		stdout, stderr, err = common.SecureExecutePipeline(ctx.GetContext(), opts)
	} else {
		stdout, stderr, err = common.SecureExecute(ctx.GetContext(), opts)
	}

	if err != nil {
		ctx.GetLogger().Error("gcloud CLI command execution failed", "error", err, "stderr", stderr, "command", command, "stdout", stdout)
		return stdout, fmt.Errorf("gcloud CLI command failed: %w, Stderr: %s", err, stderr)
	}

	return stdout, nil
}

func (a *gcloudProvider) QueryServiceMap(ctx providers.CloudProviderContext, account providers.Account, query providers.QueryServiceMapRequest) (providers.QueryServiceMapResponse, error) {
	return providers.QueryServiceMapResponse{}, errors.ErrUnsupported
}

func (a *gcloudProvider) ListEventRules(ctx providers.CloudProviderContext, account providers.Account) (providers.ListEventRules, error) {
	// Fetch alert policies from GCP Cloud Monitoring
	return getGCPAlertPolicies(ctx, account)
}

func (a *gcloudProvider) Name() string {
	return "GCP"
}

func init() {
	// Wrap all services with permission audit decorator
	for key, svc := range gcloudServiceMap {
		gcloudServiceMap[key] = &auditedGcloudService{inner: svc, serviceName: key}
	}

	providers.RegisterProvider(&gcloudProvider{})
}

func getAllRegions(ctx providers.CloudProviderContext, account providers.Account) ([]string, error) {
	// Fetch all available GCP regions using gcloud CLI
	stdout, err := (&gcloudProvider{}).ExecuteCliCommand(ctx, account, "gcloud compute regions list --format='value(name)'")
	if err != nil {
		ctx.GetLogger().Error("failed to fetch GCP regions using gcloud CLI, falling back to default list", "error", err)
		// Fallback to default regions if API call fails
		return []string{
			"us-central1",
			"us-east1",
			"us-west1",
			"europe-west1",
			"asia-east1",
		}, nil
	}

	// Parse the output to get region names
	regions := []string{}
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	for _, line := range lines {
		region := strings.TrimSpace(line)
		if region != "" {
			regions = append(regions, region)
		}
	}

	if len(regions) == 0 {
		ctx.GetLogger().Warn("no regions found from gcloud CLI, using default list")
		return []string{
			"us-central1",
			"us-east1",
			"us-west1",
			"europe-west1",
			"asia-east1",
		}, nil
	}

	ctx.GetLogger().Info("fetched GCP regions", "count", len(regions))
	return regions, nil
}
