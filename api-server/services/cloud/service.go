package cloud

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"nudgebee/services/common"
	"nudgebee/services/config"
	"nudgebee/services/security"
	"strings"
	"sync"
	"time"
)

const (
	defaultMaxRetries     = 3
	defaultRetryBaseDelay = 500 * time.Millisecond
	defaultRetryMaxDelay  = 10 * time.Second
)

// isRetryableError determines if an error or status code should trigger a retry
func isRetryableError(err error, statusCode int) bool {
	if err != nil {
		// Retry on network errors (connection refused, timeout, etc.)
		return true
	}
	// Retry on 429 (rate limiting) only. Cloud-collector 5xx responses are
	// typically deterministic upstream failures (malformed CLI input, missing
	// creds, disabled APIs) — retrying amplifies the load without changing the
	// outcome. Transport-level errors above still retry via err != nil.
	return statusCode == 429
}

// calculateBackoff returns the delay for a given retry attempt using exponential backoff with jitter
func calculateBackoff(attempt int, baseDelay, maxDelay time.Duration) time.Duration {
	delay := baseDelay * time.Duration(1<<uint(attempt)) // 2^attempt * baseDelay
	if delay > maxDelay {
		delay = maxDelay
	}
	// Add jitter: +/- 25% of the delay
	jitter := time.Duration(float64(delay) * 0.25 * (0.5 - float64(time.Now().UnixNano()%100)/100))
	return delay + jitter
}

func ExecuteCli(ctx *security.RequestContext, cloudCliRequest CloudExecuteCliCommandRequest) (map[string]any, error) {
	return ExecuteCliWithRetry(ctx, cloudCliRequest, defaultMaxRetries)
}

func ExecuteCliWithRetry(ctx *security.RequestContext, cloudCliRequest CloudExecuteCliCommandRequest, maxRetries int) (map[string]any, error) {
	data := make(map[string]any)

	headersMap := map[string]string{
		"Content-Type": "application/json",
		"Accept":       "application/json",
	}

	if config.Config.CloudCollectorServerUrl == "" {
		return map[string]any{}, errors.New("cloud: cloud collector server url not set")
	}
	if config.Config.CloudCollectorServerToken != "" {
		headersMap["X-ACTION-TOKEN"] = config.Config.CloudCollectorServerToken
	}
	headersMap["x-tenant-id"] = ctx.GetSecurityContext().GetTenantId()
	headersMap["x-user-id"] = ctx.GetSecurityContext().GetUserId()

	var lastErr error
	var lastStatusCode int

	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			backoff := calculateBackoff(attempt-1, defaultRetryBaseDelay, defaultRetryMaxDelay)
			slog.Warn("retrying ExecuteCli request", "attempt", attempt, "maxRetries", maxRetries, "backoff", backoff, "lastError", lastErr, "lastStatusCode", lastStatusCode)
			time.Sleep(backoff)
		}

		resp, err := common.HttpPost(fmt.Sprintf("%s/v1/cloud/execute_cli", config.Config.CloudCollectorServerUrl), common.HttpWithTimeout(60*time.Second), common.HttpWithHeaders(headersMap), common.HttpWithJsonBody(map[string]any{
			"account_id": cloudCliRequest.AccountID,
			"command":    cloudCliRequest.Command,
		}))

		if err != nil {
			lastErr = err
			lastStatusCode = 0
			if attempt < maxRetries {
				continue
			}
			slog.Error("unable to access cloud server after retries", "error", err, "attempts", attempt+1)
			return data, fmt.Errorf("unable to access cloud server after %d attempts: %v", attempt+1, err)
		}

		jsonBody, err := io.ReadAll(resp.Body)
		closeErr := resp.Body.Close()
		if closeErr != nil {
			slog.Error("cloud: failed to close response body", "error", closeErr)
		}

		if err != nil {
			lastErr = err
			lastStatusCode = resp.StatusCode
			if attempt < maxRetries {
				continue
			}
			return data, fmt.Errorf("failed to read response body after %d attempts: %v", attempt+1, err)
		}

		lastStatusCode = resp.StatusCode

		// Check if we should retry based on status code
		if isRetryableError(nil, resp.StatusCode) && attempt < maxRetries {
			lastErr = fmt.Errorf("server returned status %d", resp.StatusCode)
			slog.Warn("received retryable status code", "statusCode", resp.StatusCode, "body", string(jsonBody))
			continue
		}

		responseData := map[string]any{}
		err = json.Unmarshal(jsonBody, &responseData)
		if err != nil {
			return data, err
		}

		if resp.StatusCode != 200 {
			ctx.GetLogger().Error("failed to fetch data from cloud",
				"status", resp.StatusCode,
				"account_id", cloudCliRequest.AccountID,
				"command", truncateForLog(cloudCliRequest.Command, 200),
				"data", string(jsonBody))
			return responseData, fmt.Errorf("cloud collector returned status %d: %s", resp.StatusCode, string(jsonBody))
		}

		return responseData, nil
	}

	// This shouldn't be reached, but handle it just in case
	return data, fmt.Errorf("ExecuteCli failed after %d attempts: %v", maxRetries+1, lastErr)
}

// truncateForLog returns s capped to maxLen runes for safe inclusion in log fields.
func truncateForLog(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

func StoreUsageReport(ctx *security.RequestContext, usageReportRequest StoreUsageRequest) (map[string]any, error) {

	if usageReportRequest.AccountId == "" {
		return map[string]any{}, errors.New("account_id is required")
	}

	if usageReportRequest.Month == 0 || usageReportRequest.Year == 0 {
		return map[string]any{}, errors.New("month/year is required")
	}

	if ctx.GetSecurityContext().GetTenantId() == "" {
		return map[string]any{}, errors.New("tenant is required")
	}

	if ctx.GetSecurityContext().GetUserId() == "" {
		return map[string]any{}, errors.New("user is required")
	}

	resp, err := common.HttpPost(config.Config.CloudCollectorServerUrl+"/v1/cloud/store_usage", common.HttpWithJsonBody(map[string]any{
		"account_id": usageReportRequest.AccountId,
		"month":      usageReportRequest.Month,
		"year":       usageReportRequest.Year,
	}), common.HttpWithHeaders(map[string]string{
		config.Config.CloudCollectorServerTokenHeader: config.Config.CloudCollectorServerToken,
		"x-tenant-id": ctx.GetSecurityContext().GetTenantId(),
		"x-user-id":   ctx.GetSecurityContext().GetUserId(),
	}))

	if err != nil {
		return map[string]any{}, err
	}

	var data []byte
	if resp.Body != nil {
		defer func() {
			if err := resp.Body.Close(); err != nil {
				slog.Error("unable to close body", "error", err)
			}
		}()
		data, err = io.ReadAll(resp.Body)
		if err != nil {
			return map[string]any{}, err
		}
	}

	if resp.StatusCode != 200 {
		ctx.GetLogger().Error("cloud: unable to send request", "body", string(data), "status", resp.StatusCode)
		return map[string]any{}, errors.New(string(data))
	}

	return map[string]any{
		"data": string(data),
	}, nil
}

type queryMetricsResponse struct {
	Data QueryMetricsResponse `json:"data"`
}

func QueryMetrics(ctx *security.RequestContext, metricRequest QueryMetricsRequest) (QueryMetricsResponse, error) {

	response := QueryMetricsResponse{}

	if metricRequest.AccountId == "" {
		return response, errors.New("account_id is required")
	}

	if ctx.GetSecurityContext().GetTenantId() == "" {
		return response, errors.New("tenant is required")
	}

	if metricRequest.Query.ServiceName == "" {
		return response, errors.New("query.service_name is required")
	}

	resp, err := common.HttpPost(config.Config.CloudCollectorServerUrl+"/v1/cloud/get_metrics", common.HttpWithTimeout(30*time.Second), common.HttpWithJsonBody(map[string]any{
		"account_id": metricRequest.AccountId,
		"query":      metricRequest.Query,
	}), common.HttpWithHeaders(map[string]string{
		config.Config.CloudCollectorServerTokenHeader: config.Config.CloudCollectorServerToken,
		"x-tenant-id": ctx.GetSecurityContext().GetTenantId(),
		"x-user-id":   ctx.GetSecurityContext().GetUserId(),
	}))

	if err != nil {
		return response, err
	}

	body := resp.Body
	defer func() {
		err := body.Close()
		if err != nil {
			ctx.GetLogger().Error("Error closing response body", "error", err)
		}
	}()
	bodyData, err := io.ReadAll(body)
	if err != nil {
		return response, err
	}

	if resp.StatusCode != 200 {
		return response, errors.New("Error while fetching metrics - " + string(bodyData))
	}

	response2 := queryMetricsResponse{}
	err = json.Unmarshal(bodyData, &response2)
	if err != nil {
		return response, err
	}

	return response2.Data, nil
}

type listMetricsApiResponse struct {
	Data ListMetricsResponse `json:"data"`
}

func ListMetrics(ctx *security.RequestContext, accountId string, request ListMetricsRequest) (ListMetricsResponse, error) {
	if accountId == "" {
		return ListMetricsResponse{}, errors.New("account_id is required")
	}

	resp, err := common.HttpPost(config.Config.CloudCollectorServerUrl+"/v1/cloud/list_metrics", common.HttpWithTimeout(10*time.Second), common.HttpWithJsonBody(map[string]any{
		"account_id": accountId,
		"request":    request,
	}), common.HttpWithHeaders(map[string]string{
		config.Config.CloudCollectorServerTokenHeader: config.Config.CloudCollectorServerToken,
		"x-tenant-id": ctx.GetSecurityContext().GetTenantId(),
		"x-user-id":   ctx.GetSecurityContext().GetUserId(),
	}))

	if err != nil {
		return ListMetricsResponse{}, err
	}

	body := resp.Body
	defer func() {
		err := body.Close()
		if err != nil {
			ctx.GetLogger().Error("Error closing response body", "error", err)
		}
	}()
	bodyData, err := io.ReadAll(body)
	if err != nil {
		return ListMetricsResponse{}, err
	}

	if resp.StatusCode != 200 {
		return ListMetricsResponse{}, errors.New("Error while listing metrics - " + string(bodyData))
	}

	response2 := listMetricsApiResponse{}
	err = json.Unmarshal(bodyData, &response2)
	if err != nil {
		return ListMetricsResponse{}, err
	}

	return response2.Data, nil
}

type queryResourcesResponse struct {
	Data QueryResourceResponse `json:"data"`
}

func QueryResources(ctx *security.RequestContext, resourceRequest QueryResourceRequest) (QueryResourceResponse, error) {
	queryResponse := QueryResourceResponse{}

	if resourceRequest.AccountId == "" {
		return queryResponse, errors.New("account_id is required")
	}

	if ctx.GetSecurityContext().GetTenantId() == "" {
		return queryResponse, errors.New("tenant_id is required")
	}

	if resourceRequest.ServiceName == "" {
		return queryResponse, errors.New("service_name is required")
	}

	headersMap := map[string]string{
		"Content-Type": "application/json",
		"Accept":       "application/json",
	}

	if config.Config.CloudCollectorServerUrl == "" {
		return queryResponse, errors.New("cloud: cloud collector server url not set")
	}
	if config.Config.CloudCollectorServerToken != "" {
		headersMap["X-ACTION-TOKEN"] = config.Config.CloudCollectorServerToken
	}
	headersMap["x-tenant-id"] = ctx.GetSecurityContext().GetTenantId()
	headersMap["x-user-id"] = ctx.GetSecurityContext().GetUserId()

	resp, err := common.HttpPost(fmt.Sprintf("%s/v1/cloud/get_resources", config.Config.CloudCollectorServerUrl), common.HttpWithTimeout(60*time.Second), common.HttpWithHeaders(headersMap), common.HttpWithJsonBody(map[string]any{
		"account_id":   resourceRequest.AccountId,
		"service_name": resourceRequest.ServiceName,
		"resource_ids": resourceRequest.ResourceIds,
		"regions":      resourceRequest.Regions,
	}))

	if err != nil {
		slog.Error("unable to access cloud server", "error", err)
		return queryResponse, fmt.Errorf("unable to access cloud server %v", err)
	}

	defer func() {
		if err := resp.Body.Close(); err != nil {
			slog.Error("cloud: failed to close response body", "error", err)
		}
	}()

	jsonBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return queryResponse, err
	}

	queryRespone2 := queryResourcesResponse{}
	err = json.Unmarshal(jsonBody, &queryRespone2)
	if err != nil {
		return queryResponse, err
	}

	if resp.StatusCode != 200 {
		ctx.GetLogger().Error("failed to fetch data from cloud",
			"status", resp.StatusCode,
			"account_id", resourceRequest.AccountId,
			"data", string(jsonBody))
		return queryResponse, fmt.Errorf("cloud collector returned status %d: %s", resp.StatusCode, string(jsonBody))
	}

	return queryRespone2.Data, nil
}

type queryLogResponse struct {
	Data QueryLogResponse `json:"data"`
}

func QueryLogs(ctx *security.RequestContext, logsRequest QueryLogsRequest) (QueryLogResponse, error) {
	queryResponse := QueryLogResponse{}

	if logsRequest.AccountId == "" {
		return queryResponse, errors.New("account_id is required")
	}

	if ctx.GetSecurityContext().GetTenantId() == "" {
		return queryResponse, errors.New("tenant_id is required")
	}

	if logsRequest.Query.LogGroupName == "" && logsRequest.Query.ServiceName == "" && logsRequest.Query.ResourceId == "" {
		return queryResponse, errors.New("log_group_name or (service_name and resource_id) is required")
	}

	headersMap := map[string]string{
		"Content-Type": "application/json",
		"Accept":       "application/json",
	}

	if config.Config.CloudCollectorServerUrl == "" {
		return queryResponse, errors.New("cloud: cloud collector server url not set")
	}
	if config.Config.CloudCollectorServerToken != "" {
		headersMap["X-ACTION-TOKEN"] = config.Config.CloudCollectorServerToken
	}
	headersMap["x-tenant-id"] = ctx.GetSecurityContext().GetTenantId()
	headersMap["x-user-id"] = ctx.GetSecurityContext().GetUserId()

	if logsRequest.Query.EndTime == nil {
		now := time.Now()
		logsRequest.Query.EndTime = &now
	}

	if logsRequest.Query.StartTime == nil {
		startTime := logsRequest.Query.EndTime.Add(-1 * time.Hour)
		logsRequest.Query.StartTime = &startTime
	}

	if logsRequest.Query.QueryString == "" && logsRequest.Query.LogMetricName == "" && !strings.Contains(logsRequest.Query.ResourceId, "microsoft") {
		logsRequest.Query.QueryString = "fields @timestamp, @message"
	}

	if logsRequest.Query.StartTime != nil {
		utcTime := logsRequest.Query.StartTime.UTC()
		logsRequest.Query.StartTime = &utcTime
	}

	if logsRequest.Query.EndTime != nil {
		utcTime := logsRequest.Query.EndTime.UTC()
		logsRequest.Query.EndTime = &utcTime
	}

	resp, err := common.HttpPost(fmt.Sprintf("%s/v1/cloud/query_logs", config.Config.CloudCollectorServerUrl), common.HttpWithTimeout(30*time.Second), common.HttpWithHeaders(headersMap), common.HttpWithJsonBody(map[string]any{
		"account_id": logsRequest.AccountId,
		"query":      logsRequest.Query,
	}))

	if err != nil {
		slog.Error("unable to access cloud server", "error", err)
		return queryResponse, fmt.Errorf("unable to access cloud server %v", err)
	}

	defer func() {
		if err := resp.Body.Close(); err != nil {
			slog.Error("cloud: failed to close response body", "error", err)
		}
	}()

	jsonBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return queryResponse, err
	}

	if resp.StatusCode != 200 {
		ctx.GetLogger().Error("failed to fetch data from cloud",
			"status", resp.StatusCode,
			"account_id", logsRequest.AccountId,
			"data", string(jsonBody))
		return queryResponse, fmt.Errorf("cloud collector returned status %d: %s", resp.StatusCode, string(jsonBody))
	}

	logResponse2 := queryLogResponse{}
	err = json.Unmarshal(jsonBody, &logResponse2)
	if err != nil {
		return queryResponse, err
	}

	return logResponse2.Data, nil
}

type queryServiceMapResponse struct {
	Data QueryServiceMapResponse `json:"data"`
}

func QueryServiceMap(ctx *security.RequestContext, serviceMapRequest QueryServiceMapRequest) (QueryServiceMapResponse, error) {
	queryResponse := QueryServiceMapResponse{}

	if serviceMapRequest.AccountId == "" {
		return queryResponse, errors.New("account_id is required")
	}

	if ctx.GetSecurityContext().GetTenantId() == "" {
		return queryResponse, errors.New("tenant_id is required")
	}

	if serviceMapRequest.Query.Region == "" {
		serviceMapRequest.Query.Region = "us-east-1"
	}

	if len(serviceMapRequest.Query.Resources) == 0 {
		return queryResponse, errors.New("atleast one resource is required")
	}

	headersMap := map[string]string{
		"Content-Type": "application/json",
		"Accept":       "application/json",
	}

	if config.Config.CloudCollectorServerUrl == "" {
		return queryResponse, errors.New("cloud: cloud collector server url not set")
	}
	if config.Config.CloudCollectorServerToken != "" {
		headersMap["X-ACTION-TOKEN"] = config.Config.CloudCollectorServerToken
	}
	headersMap["x-tenant-id"] = ctx.GetSecurityContext().GetTenantId()
	headersMap["x-user-id"] = ctx.GetSecurityContext().GetUserId()

	resp, err := common.HttpPost(fmt.Sprintf("%s/v1/cloud/query_service_map", config.Config.CloudCollectorServerUrl), common.HttpWithTimeout(180*time.Second), common.HttpWithHeaders(headersMap), common.HttpWithJsonBody(map[string]any{
		"account_id": serviceMapRequest.AccountId,
		"query":      serviceMapRequest.Query,
	}))

	if err != nil {
		slog.Error("unable to access cloud server", "error", err)
		return queryResponse, fmt.Errorf("unable to access cloud server %v", err)
	}

	defer func() {
		if err := resp.Body.Close(); err != nil {
			slog.Error("cloud: failed to close response body", "error", err)
		}
	}()

	jsonBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return queryResponse, err
	}

	serviceMapResponse := queryServiceMapResponse{}
	err = json.Unmarshal(jsonBody, &serviceMapResponse)
	if err != nil {
		return queryResponse, err
	}

	if resp.StatusCode != 200 {
		ctx.GetLogger().Error("failed to fetch data from cloud",
			"status", resp.StatusCode,
			"account_id", serviceMapRequest.AccountId,
			"data", string(jsonBody))
		return queryResponse, fmt.Errorf("cloud collector returned status %d: %s", resp.StatusCode, string(jsonBody))
	}

	return serviceMapResponse.Data, nil
}

type queryDatabasePerformanceResponse struct {
	Data QueryDatabasePerformanceResponse `json:"data"`
}

func QueryDatabasePerformance(ctx *security.RequestContext, perfRequest QueryDatabasePerformanceRequest) (QueryDatabasePerformanceResponse, error) {
	queryResponse := QueryDatabasePerformanceResponse{}

	// Validate required fields
	if perfRequest.AccountId == "" {
		return queryResponse, errors.New("account_id is required")
	}

	if perfRequest.DatabaseIdentifier == "" {
		return queryResponse, errors.New("database_identifier is required")
	}

	if perfRequest.Region == "" {
		return queryResponse, errors.New("region is required")
	}

	if ctx.GetSecurityContext().GetTenantId() == "" {
		return queryResponse, errors.New("tenant_id is required")
	}

	if ctx.GetSecurityContext().GetUserId() == "" {
		return queryResponse, errors.New("user_id is required")
	}

	// Set defaults if not provided
	if perfRequest.EndTime == nil {
		now := time.Now()
		perfRequest.EndTime = &now
	}

	if perfRequest.StartTime == nil {
		startTime := perfRequest.EndTime.Add(-1 * time.Hour)
		perfRequest.StartTime = &startTime
	}

	if perfRequest.GranularitySeconds == 0 {
		perfRequest.GranularitySeconds = 60 // Default to 1 minute granularity
	}

	if perfRequest.TopN == 0 {
		perfRequest.TopN = 10 // Default to top 10
	}

	// Prepare headers
	headersMap := map[string]string{
		"Content-Type": "application/json",
		"Accept":       "application/json",
	}

	if config.Config.CloudCollectorServerUrl == "" {
		return queryResponse, errors.New("cloud: cloud collector server url not set")
	}

	if config.Config.CloudCollectorServerToken != "" {
		headersMap["X-ACTION-TOKEN"] = config.Config.CloudCollectorServerToken
	}
	headersMap["x-tenant-id"] = ctx.GetSecurityContext().GetTenantId()
	headersMap["x-user-id"] = ctx.GetSecurityContext().GetUserId()

	// Make the request to collector server
	resp, err := common.HttpPost(
		fmt.Sprintf("%s/v1/cloud/database_performance", config.Config.CloudCollectorServerUrl),
		common.HttpWithTimeout(30*time.Second),
		common.HttpWithHeaders(headersMap),
		common.HttpWithJsonBody(map[string]any{
			"account_id":          perfRequest.AccountId,
			"database_identifier": perfRequest.DatabaseIdentifier,
			"region":              perfRequest.Region,
			"start_time":          perfRequest.StartTime,
			"end_time":            perfRequest.EndTime,
			"granularity_seconds": perfRequest.GranularitySeconds,
			"include_top_queries": perfRequest.IncludeTopQueries,
			"include_wait_events": perfRequest.IncludeWaitEvents,
			"include_top_users":   perfRequest.IncludeTopUsers,
			"include_top_hosts":   perfRequest.IncludeTopHosts,
			"top_n":               perfRequest.TopN,
		}),
	)

	if err != nil {
		slog.Error("unable to access cloud collector server for database performance insights", "error", err)
		return queryResponse, fmt.Errorf("unable to access cloud collector server: %v", err)
	}

	defer func() {
		if err := resp.Body.Close(); err != nil {
			slog.Error("cloud: failed to close response body", "error", err)
		}
	}()

	jsonBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return queryResponse, err
	}

	// Parse response
	perfResponse := queryDatabasePerformanceResponse{}
	err = json.Unmarshal(jsonBody, &perfResponse)
	if err != nil {
		slog.Error("failed to unmarshal database performance response", "error", err, "body", string(jsonBody))
		return queryResponse, fmt.Errorf("failed to parse response: %v", err)
	}

	if resp.StatusCode != 200 {
		slog.Error("failed to fetch database performance from cloud", "status", resp.StatusCode, "data", string(jsonBody))
		return queryResponse, fmt.Errorf("server returned status %d: %s", resp.StatusCode, string(jsonBody))
	}

	return perfResponse.Data, nil
}

// TriggerCloudAccountSync triggers a full data sync for a cloud account.
// It calls store_usage (which cascades to resources + recommendations via post-report job)
// and store_events concurrently.
func TriggerCloudAccountSync(ctx *security.RequestContext, accountId string) (TriggerCloudSyncResponse, error) {
	if accountId == "" {
		return TriggerCloudSyncResponse{}, errors.New("account_id is required")
	}
	if ctx.GetSecurityContext().GetTenantId() == "" {
		return TriggerCloudSyncResponse{}, errors.New("tenant is required")
	}

	headers := map[string]string{
		config.Config.CloudCollectorServerTokenHeader: config.Config.CloudCollectorServerToken,
		"x-tenant-id": ctx.GetSecurityContext().GetTenantId(),
	}
	if ctx.GetSecurityContext().GetUserId() != "" {
		headers["x-user-id"] = ctx.GetSecurityContext().GetUserId()
	}

	now := time.Now()
	var usageErr, eventsErr error
	var wg sync.WaitGroup
	wg.Add(2)

	// Trigger usage report (publishes to RabbitMQ, returns immediately)
	go func() {
		defer wg.Done()
		usageResp, err := common.HttpPost(config.Config.CloudCollectorServerUrl+"/v1/cloud/store_usage",
			common.HttpWithTimeout(120*time.Second),
			common.HttpWithJsonBody(map[string]any{
				"account_id": accountId,
				"month":      int(now.Month()),
				"year":       now.Year(),
			}),
			common.HttpWithHeaders(headers),
		)
		if err != nil {
			usageErr = fmt.Errorf("store_usage: %w", err)
			return
		}
		if usageResp.Body != nil {
			defer func() { _ = usageResp.Body.Close() }()
		}
		if usageResp.StatusCode != 200 {
			body, readErr := io.ReadAll(usageResp.Body)
			if readErr != nil {
				usageErr = fmt.Errorf("store_usage returned %d (failed to read body: %w)", usageResp.StatusCode, readErr)
				return
			}
			usageErr = fmt.Errorf("store_usage returned %d: %s", usageResp.StatusCode, string(body))
		}
	}()

	// Trigger events sync (publishes to RabbitMQ, returns immediately)
	go func() {
		defer wg.Done()
		eventsResp, err := common.HttpPost(config.Config.CloudCollectorServerUrl+"/v1/cloud/store_events",
			common.HttpWithTimeout(30*time.Second),
			common.HttpWithJsonBody(map[string]any{
				"account_id": accountId,
			}),
			common.HttpWithHeaders(headers),
		)
		if err != nil {
			eventsErr = fmt.Errorf("store_events: %w", err)
			return
		}
		if eventsResp.Body != nil {
			defer func() { _ = eventsResp.Body.Close() }()
		}
		if eventsResp.StatusCode != 200 {
			body, readErr := io.ReadAll(eventsResp.Body)
			if readErr != nil {
				eventsErr = fmt.Errorf("store_events returned %d (failed to read body: %w)", eventsResp.StatusCode, readErr)
				return
			}
			eventsErr = fmt.Errorf("store_events returned %d: %s", eventsResp.StatusCode, string(body))
		}
	}()

	wg.Wait()

	if usageErr != nil || eventsErr != nil {
		errMsgs := []string{}
		if usageErr != nil {
			errMsgs = append(errMsgs, usageErr.Error())
		}
		if eventsErr != nil {
			errMsgs = append(errMsgs, eventsErr.Error())
		}
		return TriggerCloudSyncResponse{
			Success: false,
			Message: strings.Join(errMsgs, "; "),
		}, nil
	}

	return TriggerCloudSyncResponse{
		Success: true,
		Message: "Sync triggered successfully. Data will be available shortly.",
	}, nil
}

func ApplyCommand(ctx *security.RequestContext, cmdRequest ApplyCommandRequest) (ApplyCommandResponse, error) {
	if cmdRequest.AccountId == "" {
		return ApplyCommandResponse{}, errors.New("account_id is required")
	}
	if ctx.GetSecurityContext().GetTenantId() == "" {
		return ApplyCommandResponse{}, errors.New("tenant is required")
	}

	headers := map[string]string{
		config.Config.CloudCollectorServerTokenHeader: config.Config.CloudCollectorServerToken,
		"x-tenant-id": ctx.GetSecurityContext().GetTenantId(),
	}
	if ctx.GetSecurityContext().GetUserId() != "" {
		headers["x-user-id"] = ctx.GetSecurityContext().GetUserId()
	}

	payload := map[string]interface{}{
		"account_id":   cmdRequest.AccountId,
		"service_name": cmdRequest.ServiceName,
		"region":       cmdRequest.Region,
		"resource_id":  cmdRequest.ResourceId,
		"command":      cmdRequest.Command,
		"args":         cmdRequest.Args,
	}

	resp, err := common.HttpPost(config.Config.CloudCollectorServerUrl+"/v1/cloud/apply_command",
		common.HttpWithTimeout(280*time.Second),
		common.HttpWithJsonBody(payload),
		common.HttpWithHeaders(headers),
	)

	if err != nil {
		return ApplyCommandResponse{}, fmt.Errorf("failed to call cloud-collector: %w", err)
	}

	if resp.Body != nil {
		defer func() { _ = resp.Body.Close() }()
	}

	body, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return ApplyCommandResponse{}, fmt.Errorf("failed to read response body: %w", readErr)
	}

	return parseApplyCommandResponse(resp.StatusCode, body)
}

// parseApplyCommandResponse turns a cloud-collector response into an
// ApplyCommandResponse. The cloud-collector wraps every response (success
// and failure) in the shape `{"data": {...}, "errors": [{"message": "..."}]}`
// (see cloud-collector/api/api.go buildApiResponse). When the call fails on
// the cloud side we surface the provider-specific message verbatim — AWS
// UnauthorizedOperation, Azure AuthorizationFailed, GCP 403 missing-permission
// — as success=false with a nil error, so the RPC handler propagates it
// as 200 + GraphQL data instead of swallowing the detail behind an opaque 500.
// A non-nil error is returned only for true transport / parse failures.
func parseApplyCommandResponse(statusCode int, body []byte) (ApplyCommandResponse, error) {
	var parsed struct {
		Data *struct {
			Success bool   `json:"success"`
			Message string `json:"message"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	parseErr := json.Unmarshal(body, &parsed)

	if statusCode != 200 {
		message := fmt.Sprintf("cloud-collector returned %d", statusCode)
		if parseErr == nil && len(parsed.Errors) > 0 && parsed.Errors[0].Message != "" {
			message = parsed.Errors[0].Message
		} else if len(body) > 0 {
			message = fmt.Sprintf("%s: %s", message, string(body))
		}
		return ApplyCommandResponse{
			Success: false,
			Message: message,
		}, nil
	}

	if parseErr != nil {
		return ApplyCommandResponse{}, fmt.Errorf("failed to parse response: %w", parseErr)
	}
	if parsed.Data == nil {
		return ApplyCommandResponse{}, fmt.Errorf("cloud-collector returned 200 with no data field")
	}

	return ApplyCommandResponse{
		Success: parsed.Data.Success,
		Message: parsed.Data.Message,
	}, nil
}
