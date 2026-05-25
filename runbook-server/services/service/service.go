package service

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"nudgebee/runbook/common"
	"nudgebee/runbook/config"
	"nudgebee/runbook/services/integrations"
	"nudgebee/runbook/services/security"
	"strings"
	"time"
)

const contentTypeJson = "application/json"

func GetResourceID(ctx *security.RequestContext, accountID, namespace, workloadName, workloadType string) (string, error) {
	request := map[string]any{
		"action": Action{
			Name: "k8s_workloads_v2",
		},
		"input": map[string]any{
			"where": map[string]any{
				"account_id": QueryCondition{Eq: accountID},
				"namespace":  QueryCondition{Eq: namespace},
				"name":       QueryCondition{Eq: workloadName},
			},
		},
	}

	resp, err := common.HttpPost(fmt.Sprintf("%s/hasura/query", config.Config.ServiceEndpoint), common.HttpWithHeaders(map[string]string{
		"Content-Type":   contentTypeJson,
		"Accept":         contentTypeJson,
		"X-ACTION-TOKEN": config.Config.ServiceApiServerToken,
		"x-tenant-id":    ctx.GetSecurityContext().GetTenantId(),
		"x-user-id":      ctx.GetSecurityContext().GetUserId(),
	}), common.HttpWithJsonBody(request))

	if err != nil {
		return "", fmt.Errorf("services: get_resource_id, unable to process request: %v", err)
	}
	defer func() {
		if resp.Body != nil {
			if err := resp.Body.Close(); err != nil {
				slog.Info("services_server: failed to close response body", "error", err)
			}
		}
	}()

	jsonBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	if resp.StatusCode != 200 {
		ctx.GetLogger().Error("services: get_resource_id failed with status", "status", resp.StatusCode, "body", string(jsonBody))
		return "", fmt.Errorf("services: get_resource_id failed with status %d: %s", resp.StatusCode, string(jsonBody))
	}

	var responseData map[string]any
	if err := common.UnmarshalJson(jsonBody, &responseData); err != nil {
		return "", err
	}

	if rows, ok := responseData["rows"].([]any); ok && len(rows) > 0 {
		if firstRow, ok := rows[0].(map[string]any); ok {
			if id, ok := firstRow["cloud_resource_id"].(string); ok {
				return id, nil
			}
		}
	}

	return "", nil
}

// GetCloudResourceField looks up a single cloud resource by account, name, type
// and status, then returns the value of the requested field (e.g. "resource_id").
// Returns "" when no matching resource is found.
func GetCloudResourceField(ctx *security.RequestContext, accountID, name, resourceType, status, field string) (string, error) {
	request := map[string]any{
		"action": Action{
			Name: "cloud_resource_v2",
		},
		"input": map[string]any{
			"where": map[string]any{
				"account": QueryCondition{Eq: accountID},
				"name":    QueryCondition{Eq: name},
				"type":    QueryCondition{Eq: resourceType},
				"status":  QueryCondition{Eq: status},
			},
		},
	}

	resp, err := common.HttpPost(fmt.Sprintf("%s/hasura/query", config.Config.ServiceEndpoint), common.HttpWithHeaders(map[string]string{
		"Content-Type":   contentTypeJson,
		"Accept":         contentTypeJson,
		"X-ACTION-TOKEN": config.Config.ServiceApiServerToken,
		"x-tenant-id":    ctx.GetSecurityContext().GetTenantId(),
		"x-user-id":      ctx.GetSecurityContext().GetUserId(),
	}), common.HttpWithJsonBody(request))

	if err != nil {
		return "", fmt.Errorf("services: get_cloud_resource_field, unable to process request: %v", err)
	}
	defer func() {
		if resp.Body != nil {
			if err := resp.Body.Close(); err != nil {
				slog.Info("services_server: failed to close response body", "error", err)
			}
		}
	}()

	jsonBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("services: get_cloud_resource_field failed with status %d: %s", resp.StatusCode, string(jsonBody))
	}

	var responseData map[string]any
	if err := common.UnmarshalJson(jsonBody, &responseData); err != nil {
		return "", err
	}

	if rows, ok := responseData["rows"].([]any); ok && len(rows) > 0 {
		if firstRow, ok := rows[0].(map[string]any); ok {
			if val, ok := firstRow[field].(string); ok {
				return val, nil
			}
		}
	}

	return "", nil
}

func ExecuteQuery(ctx *security.RequestContext, serviceRequest ServicesQueryRequest) (map[string]string, error) {
	response := make(map[string]string)

	resp, err := common.HttpPost(fmt.Sprintf("%s/hasura/query", config.Config.ServiceEndpoint), common.HttpWithHeaders(map[string]string{
		"Content-Type":   contentTypeJson,
		"Accept":         contentTypeJson,
		"X-ACTION-TOKEN": config.Config.ServiceApiServerToken,
	}), common.HttpWithJsonBody(serviceRequest))

	if err != nil {
		return response, fmt.Errorf("services: executequery, unable to process request: %v", err)
	}

	if resp.StatusCode == 401 {
		return response, fmt.Errorf("unauthorized: %v", resp.Body)
	}

	if resp.StatusCode == 500 {
		return response, fmt.Errorf("internal Server Error from Services Server, %v", resp.Body)
	}

	defer func() {
		if err := resp.Body.Close(); err != nil {
			slog.Info("services_server: failed to close response body", "error", err)
		}
	}()
	jsonBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return response, err
	}

	var responseData map[string]any
	err = common.UnmarshalJson(jsonBody, &responseData)
	if err != nil {
		return response, err
	}
	for index, value := range responseData["rows"].([]any) {
		data := make(map[string]any)
		for key, value := range value.(map[string]any) {
			if strings.Contains(key, "ServiceName") {
				data["ServiceName"] = value
			}
			if strings.Contains(key, "SpanName") {
				data["SpanName"] = value
			}
			if strings.Contains(key, "status_code") {
				data["StatusCode"] = value
			}
			if strings.Contains(key, "Timestamp") {
				data["Timestamp"] = value
			}
			if strings.Contains(key, "destination.name") {
				data["DestinationName"] = value
			}
			if strings.Contains(key, "destination.workload_namespace") {
				data["DestinationWorkloadNamespace"] = value
			}
			if strings.Contains(key, "destination.workload_name") {
				data["DestinationWorkloadName"] = value
			}
			if strings.Contains(key, "source.workload_name") {
				data["SourceWorkloadName"] = value
			}
			if strings.Contains(key, "source.workload_namespace") {
				data["SourceWorkloadNamespace"] = value
			}
			if key == "Timestamp" {
				data["Timestamp"] = value
			}

		}
		dataJson, err := common.MarshalJson(data)
		if err != nil {
			return response, err
		}
		response[fmt.Sprintf("%v", index)] = string(dataJson)
	}
	return response, err
}

func ExecuteScanImageQuery(ctx *security.RequestContext, scanImageRequest ScanImageServiceRequest) (map[string]string, error) {
	response := make(map[string]string)

	resp, err := common.HttpPost(fmt.Sprintf("%s/hasura/recommendation", config.Config.ServiceEndpoint), common.HttpWithHeaders(map[string]string{
		"Content-Type":   contentTypeJson,
		"Accept":         contentTypeJson,
		"X-ACTION-TOKEN": config.Config.ServiceApiServerToken,
	}), common.HttpWithJsonBody(scanImageRequest))

	if err != nil {
		return response, fmt.Errorf("services: scanimage, unable to process request: %v", err)
	}

	if resp.StatusCode == 401 {
		return response, fmt.Errorf("unauthorized: %v", resp.Body)
	}

	if resp.StatusCode == 500 {
		return response, fmt.Errorf("internal Server Error from Services Server, %v", resp.Body)
	}

	defer func() {
		if err := resp.Body.Close(); err != nil {
			slog.Info("services_server: failed to close response body", "error", err)
		}
	}()
	jsonBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return response, err
	}

	var responseData RecommendationApplyResponse
	err = common.UnmarshalJson(jsonBody, &responseData)
	if err != nil {
		return response, err
	}

	// Marshal the struct to JSON and store as a single entry in the map
	dataJson, err := common.MarshalJson(responseData.Data)
	if err != nil {
		return response, err
	}
	response["result"] = string(dataJson)
	return response, nil
}

func ExecuteScanCisQuery(ctx *security.RequestContext, scanCisRequest ScanCisServiceRequest) (map[string]string, error) {
	response := make(map[string]string)

	resp, err := common.HttpPost(fmt.Sprintf("%s/hasura/recommendation", config.Config.ServiceEndpoint), common.HttpWithHeaders(map[string]string{
		"Content-Type":   contentTypeJson,
		"Accept":         contentTypeJson,
		"X-ACTION-TOKEN": config.Config.ServiceApiServerToken,
	}), common.HttpWithJsonBody(scanCisRequest))

	if err != nil {
		return response, fmt.Errorf("services: scancis, unable to process request: %v", err)
	}

	if resp.StatusCode == 401 {
		return response, fmt.Errorf("unauthorized: %v", resp.Body)
	}

	if resp.StatusCode == 500 {
		return response, fmt.Errorf("internal Server Error from Services Server, %v", resp.Body)
	}

	defer func() {
		if err := resp.Body.Close(); err != nil {
			slog.Info("services: failed to close response body", "error", err)
		}
	}()
	jsonBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return response, err
	}

	var responseData ScanCisServiceResponse
	err = common.UnmarshalJson(jsonBody, &responseData)
	if err != nil {
		return response, err
	}

	// Marshal the struct to JSON and store as a single entry in the map
	dataJson, err := common.MarshalJson(responseData.Data)
	if err != nil {
		return response, err
	}
	response["result"] = string(dataJson)
	return response, nil
}

func QueryLogs(ctx *security.RequestContext, request ObservabilityLogQueryRequest) (ObservabilityLogResponse, error) {
	queryPayload := map[string]any{
		"action": map[string]any{
			"name": "logs_query",
		},
		"input": map[string]any{
			"request": request,
		},
	}

	observabilityResp := ObservabilityLogResponse{
		Metadata: ObservabilityLogMetadata{
			StartTime: request.StartTime,
			EndTime:   request.EndTime,
			Limit:     request.Limit,
			Query:     request.Query,
			Provider:  request.LogProvider,
		},
	}

	tenant := ctx.GetSecurityContext().GetTenantId()
	if tenant == "" {
		tenant1, err := security.GetTenantIdFromAccountId(request.AccountId)
		if err != nil {
			return observabilityResp, err
		}
		tenant = tenant1
	}

	if tenant == "" {
		return observabilityResp, errors.New("tenant id is empty")
	}

	resp, err := common.HttpPost(fmt.Sprintf("%s/hasura/logs", config.Config.ServiceEndpoint), common.HttpWithHeaders(map[string]string{
		"Content-Type":   contentTypeJson,
		"Accept":         contentTypeJson,
		"X-ACTION-TOKEN": config.Config.ServiceApiServerToken,
		"x-tenant-id":    tenant,
		"x-user-id":      ctx.GetSecurityContext().GetUserId(),
	}), common.HttpWithJsonBody(queryPayload))

	if err != nil {
		return observabilityResp, fmt.Errorf("services: logs, unable to process request: %v", err)
	}
	defer func() {
		if resp.Body != nil {
			if err := resp.Body.Close(); err != nil {
				slog.Info("services_server: failed to close response body", "error", err)
			}
		}
	}()

	jsonBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return observabilityResp, err
	}

	if resp.StatusCode != 200 {
		return observabilityResp, fmt.Errorf("logs query failed (status %d): %s", resp.StatusCode, string(jsonBody))
	}

	response := make([]ObservabilityLog, 0, 100)
	err = common.UnmarshalJson(jsonBody, &response)
	if err != nil {
		return observabilityResp, err
	}

	observabilityResp.Logs = response

	return observabilityResp, nil
}

func QueryTraces(ctx *security.RequestContext, request ObservabilityTracesV3Request) (ObservabilityTraceResponse, error) {
	queryPayload := map[string]any{
		"action": map[string]any{
			"name": "traces_query",
		},
		"input": map[string]any{
			"request": request,
		},
	}

	observabilityResp := ObservabilityTraceResponse{
		Metadata: ObservabilityTraceMetadata{
			StartTime: request.StartTime,
			EndTime:   request.EndTime,
			Limit:     request.Limit,
			Query:     request.Query,
			Provider:  request.ProviderType,
		},
	}

	tenant := ctx.GetSecurityContext().GetTenantId()
	if tenant == "" {
		tenant1, err := security.GetTenantIdFromAccountId(request.AccountId)
		if err != nil {
			return observabilityResp, err
		}
		tenant = tenant1
	}

	if tenant == "" {
		return observabilityResp, errors.New("tenant id is empty")
	}
	jsonBodyTemp := common.HttpWithJsonBody(queryPayload)

	resp, err := common.HttpPost(fmt.Sprintf("%s/hasura/traces", config.Config.ServiceEndpoint), common.HttpWithHeaders(map[string]string{
		"Content-Type":   contentTypeJson,
		"Accept":         contentTypeJson,
		"X-ACTION-TOKEN": config.Config.ServiceApiServerToken,
		"x-tenant-id":    tenant,
		"x-user-id":      ctx.GetSecurityContext().GetUserId(),
	}), jsonBodyTemp)

	if err != nil {
		return observabilityResp, fmt.Errorf("services: traces, unable to process request: %v", err)
	}
	defer func() {
		if resp.Body != nil {
			if err := resp.Body.Close(); err != nil {
				slog.Info("services_server: failed to close response body", "error", err)
			}
		}
	}()

	jsonBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return observabilityResp, err
	}

	if resp.StatusCode != 200 {
		return observabilityResp, fmt.Errorf("traces query failed (status %d): %s", resp.StatusCode, string(jsonBody))
	}

	// API may return a JSON error object (e.g. {"message":"..."}) with 200 status
	trimmed := bytes.TrimSpace(jsonBody)
	if len(trimmed) > 0 && trimmed[0] == '{' {
		var errResp struct {
			Message string `json:"message"`
		}
		if parseErr := common.UnmarshalJson(jsonBody, &errResp); parseErr == nil && errResp.Message != "" {
			return observabilityResp, fmt.Errorf("traces query error: %s", errResp.Message)
		}
	}

	response := make([]ObservabilityTrace, 0, 100)
	err = common.UnmarshalJson(jsonBody, &response)
	if err != nil {
		return observabilityResp, err
	}

	observabilityResp.Traces = response

	return observabilityResp, nil
}

func QueryLogLabels(ctx *security.RequestContext, accountId string, provider ObservabilityProvider) (ObservabilityLogLabelResponse, error) {
	if provider.IntegrationSource == "" {
		provider.IntegrationSource = "agent"
	}
	queryPayload := map[string]any{
		"action": map[string]any{
			"name": "logs_list_labels",
		},
		"input": map[string]any{
			"request": map[string]any{
				"account_id":          accountId,
				"log_provider":        provider.Provider,
				"log_provider_source": provider.IntegrationSource,
				"start_time":          time.Now().Add(-1 * time.Hour).UnixMilli(),
				"end_time":            time.Now().UnixMilli(),
			},
		},
	}

	tenant := ctx.GetSecurityContext().GetTenantId()
	if tenant == "" {
		tenant1, err := security.GetTenantIdFromAccountId(accountId)
		if err != nil {
			return ObservabilityLogLabelResponse{}, err
		}
		tenant = tenant1
	}

	if tenant == "" {
		return ObservabilityLogLabelResponse{}, errors.New("tenant id is empty")
	}

	resp, err := common.HttpPost(fmt.Sprintf("%s/hasura/logs", config.Config.ServiceEndpoint), common.HttpWithHeaders(map[string]string{
		"Content-Type":   contentTypeJson,
		"Accept":         contentTypeJson,
		"X-ACTION-TOKEN": config.Config.ServiceApiServerToken,
		"x-tenant-id":    tenant,
		"x-user-id":      ctx.GetSecurityContext().GetUserId(),
	}), common.HttpWithJsonBody(queryPayload))

	if err != nil {
		return ObservabilityLogLabelResponse{}, fmt.Errorf("services: loglabels, unable to process request: %v", err)
	}
	defer func() {
		if resp.Body != nil {
			if err := resp.Body.Close(); err != nil {
				slog.Info("services_server: failed to close response body", "error", err)
			}
		}
	}()

	jsonBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return ObservabilityLogLabelResponse{}, err
	}

	if resp.StatusCode == 401 {
		return ObservabilityLogLabelResponse{}, fmt.Errorf("unauthorized: %v", string(jsonBody))
	}

	if resp.StatusCode == 500 {
		return ObservabilityLogLabelResponse{}, fmt.Errorf("internal Server Error from Services Server, %v", string(jsonBody))
	}

	response := make([]ObservabilityLogLabel, 0, 100)

	err = common.UnmarshalJson(jsonBody, &response)
	if err != nil {
		return ObservabilityLogLabelResponse{}, err
	}

	return ObservabilityLogLabelResponse{Labels: response}, nil
}

type ObservabilityProvider struct {
	IntegrationSource string `json:"integrationSource"`
	Provider          string `json:"provider"`
}

func GetObservabilityProvider(ctx *security.RequestContext, accountId, provider string) (ObservabilityProvider, error) {
	queryPayload := map[string]any{
		"action": map[string]any{
			"name": "get_default_provider",
		},
		"input": map[string]any{
			"request": map[string]any{
				"account_id":    accountId,
				"provider_type": provider,
			},
		},
	}

	tenant := ctx.GetSecurityContext().GetTenantId()
	if tenant == "" {
		tenant1, err := security.GetTenantIdFromAccountId(accountId)
		if err != nil {
			return ObservabilityProvider{}, err
		}
		tenant = tenant1
	}

	if tenant == "" {
		return ObservabilityProvider{}, errors.New("tenant id is empty")
	}

	resp, err := common.HttpPost(fmt.Sprintf("%s/hasura/provider", config.Config.ServiceEndpoint), common.HttpWithHeaders(map[string]string{
		"Content-Type":   contentTypeJson,
		"Accept":         contentTypeJson,
		"X-ACTION-TOKEN": config.Config.ServiceApiServerToken,
		"x-tenant-id":    tenant,
	}), common.HttpWithJsonBody(queryPayload))
	if err != nil {
		return ObservabilityProvider{}, fmt.Errorf("services: GetObservabilityProvider unable to process request: %v", err)
	}
	defer func() {
		if resp.Body != nil {
			if err := resp.Body.Close(); err != nil {
				slog.Info("services: GetObservabilityProvider: failed to close response body", "error", err)
			}
		}
	}()

	jsonBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return ObservabilityProvider{}, err
	}

	if resp.StatusCode == 401 {
		return ObservabilityProvider{}, fmt.Errorf("GetObservabilityProvider unauthorized: %v", string(jsonBody))
	}
	if resp.StatusCode == 500 {
		return ObservabilityProvider{}, fmt.Errorf("GetObservabilityProvider internal Server Error from Services Server, %v", string(jsonBody))
	}
	var response ObservabilityProvider
	if err := common.UnmarshalJson(jsonBody, &response); err != nil {
		return ObservabilityProvider{}, err
	}

	return response, nil
}

func CreateIntegrationConfig(ctx *security.RequestContext, serviceRequest IntegrationCreateConfig) (map[string]any, error) {
	response := make(map[string]any)

	resp, err := common.HttpPost(
		fmt.Sprintf("%s/hasura/integration", config.Config.ServiceEndpoint),
		common.HttpWithHeaders(map[string]string{
			"Content-Type":   contentTypeJson,
			"Accept":         contentTypeJson,
			"X-ACTION-TOKEN": config.Config.ServiceApiServerToken,
		}),
		common.HttpWithJsonBody(serviceRequest),
	)
	if err != nil {
		return response, fmt.Errorf("services: integrations, unable to process request: %v", err)
	}
	defer func() {
		if cerr := resp.Body.Close(); cerr != nil {
			slog.Info("services_server: failed to close response body", "error", cerr)
		}
	}()

	jsonBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return response, fmt.Errorf("failed to read response body: %v", err)
	}

	if resp.StatusCode == 401 {
		return response, fmt.Errorf("unauthorized: %s", string(jsonBody))
	}
	if resp.StatusCode == 500 {
		return response, fmt.Errorf("internal server error from services server: %s", string(jsonBody))
	}

	var responseData map[string]any
	if err := common.UnmarshalJson(jsonBody, &responseData); err != nil {
		return response, fmt.Errorf("failed to unmarshal response: %v", err)
	}

	return responseData, nil
}

func CreatePullRequest(ctx *security.RequestContext, input GitPullRequest) (map[string]any, error) {
	response := make(map[string]any)

	serviceRequest := PullRequestServiceRequest{
		Action: Action{
			Name: "create_pull_request",
		},
		Input: input,
		SessionVariables: SessionVariables{
			UserID:       ctx.GetSecurityContext().GetUserId(),
			UserTenantID: ctx.GetSecurityContext().GetTenantId(),
		},
	}

	resp, err := common.HttpPost(
		fmt.Sprintf("%s/hasura/gitops", config.Config.ServiceEndpoint),
		common.HttpWithHeaders(map[string]string{
			"Content-Type":   contentTypeJson,
			"Accept":         contentTypeJson,
			"X-ACTION-TOKEN": config.Config.ServiceApiServerToken,
			"x-tenant-id":    ctx.GetSecurityContext().GetTenantId(),
			"x-user-id":      ctx.GetSecurityContext().GetUserId(),
		}),
		common.HttpWithJsonBody(serviceRequest),
	)

	if err != nil {
		return response, fmt.Errorf("services: create_pull_request, unable to process request: %v", err)
	}

	defer func() {
		if cerr := resp.Body.Close(); cerr != nil {
			slog.Info("services_server: failed to close response body", "error", cerr)
		}
	}()

	jsonBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return response, fmt.Errorf("failed to read response body: %v", err)
	}

	if resp.StatusCode == 401 {
		return response, fmt.Errorf("unauthorized: %s", string(jsonBody))
	}
	if resp.StatusCode == 500 {
		return response, fmt.Errorf("internal server error from services server: %s", string(jsonBody))
	}

	if err := common.UnmarshalJson(jsonBody, &response); err != nil {
		return response, fmt.Errorf("failed to unmarshal response: %v", err)
	}

	return response, nil
}

func RaisePR(ctx *security.RequestContext, input GitPushRequest) (string, error) {

	if input.ProviderConfig.Name == "" {
		integrationConfigs, err := integrations.ListIntegrationsByType(ctx, input.AccountID, "github")
		if err != nil {
			return "", fmt.Errorf("failed to get integration details: %w", err)
		}

		if len(integrationConfigs) > 0 {
			input.ProviderConfig.Name = integrationConfigs[0].Name
		} else {
			return "", fmt.Errorf("no github integration found for account: %s", input.AccountID)
		}
	}

	serviceRequest := PushRequestServiceRequest{
		Action: Action{
			Name: "pr_raise",
		},
		Input: input,
	}

	resp, err := common.HttpPost(
		fmt.Sprintf("%s/hasura/pr-raise", config.Config.ServiceEndpoint),
		common.HttpWithHeaders(map[string]string{
			"Content-Type":   contentTypeJson,
			"Accept":         contentTypeJson,
			"X-ACTION-TOKEN": config.Config.ServiceApiServerToken,
			"x-tenant-id":    input.TenantID,
			"x-user-id":      input.CreatedBy,
		}),
		common.HttpWithJsonBody(serviceRequest),
	)

	if err != nil {
		return "", fmt.Errorf("services: pr_raise, unable to process request: %v", err)
	}

	defer func() {
		if cerr := resp.Body.Close(); cerr != nil {
			slog.Info("services_server: failed to close response body", "error", cerr)
		}
	}()

	jsonBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response body: %v", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("pr_raise failed with status %d: %s", resp.StatusCode, string(jsonBody))
	}

	var response PushRequestResponse
	if err := common.UnmarshalJson(jsonBody, &response); err != nil {
		return "", fmt.Errorf("failed to unmarshal response: %v", err)
	}

	if response.Status != "" && response.Status != "InProgress" {
		return "", fmt.Errorf("got %s from service server for PR request", response.Status)
	}

	return response.Resolution.ID, nil
}

func RecommendationResolve(ctx *security.RequestContext, input RecommendationResolutionRequest) (string, error) {
	serviceRequest := map[string]any{
		"action": map[string]any{
			"name": "recommendation_resolve",
		},
		"input": input,
	}

	resp, err := common.HttpPost(
		fmt.Sprintf("%s/hasura/recommendation", config.Config.ServiceEndpoint),
		common.HttpWithHeaders(map[string]string{
			"Content-Type":   contentTypeJson,
			"Accept":         contentTypeJson,
			"X-ACTION-TOKEN": config.Config.ServiceApiServerToken,
			"x-tenant-id":    ctx.GetSecurityContext().GetTenantId(),
			"x-user-id":      ctx.GetSecurityContext().GetUserId(),
		}),
		common.HttpWithJsonBody(serviceRequest),
	)

	if err != nil {
		return "", fmt.Errorf("services: push_request_recommendation, unable to process request: %w", err)
	}

	defer func() {
		if cerr := resp.Body.Close(); cerr != nil {
			slog.Info("services_server: failed to close response body", "error", cerr)
		}
	}()

	jsonBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode == 401 {
		return "", fmt.Errorf("unauthorized: %s", string(jsonBody))
	}
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("service server error status %d: %s", resp.StatusCode, string(jsonBody))
	}

	var response struct {
		Status     string `json:"status"`
		Resolution struct {
			ID string `json:"id"`
		} `json:"resolution"`
	}

	if err := common.UnmarshalJson(jsonBody, &response); err != nil {
		return "", fmt.Errorf("failed to unmarshal response: %w", err)
	}

	if response.Status != "" && response.Status != "InProgress" {
		return "", fmt.Errorf("got %s from service server for PR request", response.Status)
	}

	return response.Resolution.ID, nil
}

func ListMetricsSeries(ctx *security.RequestContext, accountId, provider string) (ObservabilityMetricsSeriesResponse, error) {
	queryPayload := map[string]any{
		"action": map[string]any{
			"name": "metrics_list",
		},
		"input": map[string]any{
			"request": map[string]any{
				"account_id":      accountId,
				"metric_provider": provider,
			},
		},
	}

	tenant := ctx.GetSecurityContext().GetTenantId()
	if tenant == "" {
		tenant1, err := security.GetTenantIdFromAccountId(accountId)
		if err != nil {
			return ObservabilityMetricsSeriesResponse{}, err
		}
		tenant = tenant1
	}

	if tenant == "" {
		return ObservabilityMetricsSeriesResponse{}, errors.New("tenant id is empty")
	}

	resp, err := common.HttpPost(fmt.Sprintf("%s/hasura/metrics", config.Config.ServiceEndpoint), common.HttpWithHeaders(map[string]string{
		"Content-Type":   contentTypeJson,
		"Accept":         contentTypeJson,
		"X-ACTION-TOKEN": config.Config.ServiceApiServerToken,
		"x-tenant-id":    tenant,
		"x-user-id":      ctx.GetSecurityContext().GetUserId(),
	}), common.HttpWithJsonBody(queryPayload))

	if err != nil {
		return ObservabilityMetricsSeriesResponse{}, fmt.Errorf("services: list_metrics_series, unable to process request: %v", err)
	}
	defer func() {
		if resp.Body != nil {
			if err := resp.Body.Close(); err != nil {
				slog.Info("services_server: failed to close response body", "error", err)
			}
		}
	}()

	jsonBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return ObservabilityMetricsSeriesResponse{}, err
	}

	if resp.StatusCode == 401 {
		return ObservabilityMetricsSeriesResponse{}, fmt.Errorf("unauthorized: %v", string(jsonBody))
	}

	if resp.StatusCode == 500 {
		return ObservabilityMetricsSeriesResponse{}, fmt.Errorf("internal Server Error from Services Server, %v", string(jsonBody))
	}

	response := make([]ObservabilityMetricsSeries, 0, 100)

	err = common.UnmarshalJson(jsonBody, &response)
	if err != nil {
		return ObservabilityMetricsSeriesResponse{}, err
	}

	return ObservabilityMetricsSeriesResponse{Series: response}, nil
}

func ListMetricsSeriesLabels(ctx *security.RequestContext, accountId, provider, seriesName string) (ObservabilityMetricsSeriesLabelsResponse, error) {
	queryPayload := map[string]any{
		"action": map[string]any{
			"name": "metrics_list_labels",
		},
		"input": map[string]any{
			"request": map[string]any{
				"account_id":      accountId,
				"metric_provider": provider,
				"metric":          seriesName,
			},
		},
	}

	tenant := ctx.GetSecurityContext().GetTenantId()
	if tenant == "" {
		tenant1, err := security.GetTenantIdFromAccountId(accountId)
		if err != nil {
			return ObservabilityMetricsSeriesLabelsResponse{}, err
		}
		tenant = tenant1
	}

	if tenant == "" {
		return ObservabilityMetricsSeriesLabelsResponse{}, errors.New("tenant id is empty")
	}

	resp, err := common.HttpPost(fmt.Sprintf("%s/hasura/metrics", config.Config.ServiceEndpoint), common.HttpWithHeaders(map[string]string{
		"Content-Type":   contentTypeJson,
		"Accept":         contentTypeJson,
		"X-ACTION-TOKEN": config.Config.ServiceApiServerToken,
		"x-tenant-id":    tenant,
		"x-user-id":      ctx.GetSecurityContext().GetUserId(),
	}), common.HttpWithJsonBody(queryPayload))

	if err != nil {
		return ObservabilityMetricsSeriesLabelsResponse{}, fmt.Errorf("services: loglabels, unable to process request: %v", err)
	}
	defer func() {
		if resp.Body != nil {
			if err := resp.Body.Close(); err != nil {
				slog.Info("services_server: failed to close response body", "error", err)
			}
		}
	}()

	jsonBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return ObservabilityMetricsSeriesLabelsResponse{}, err
	}

	if resp.StatusCode == 401 {
		return ObservabilityMetricsSeriesLabelsResponse{}, fmt.Errorf("unauthorized: %v", string(jsonBody))
	}

	if resp.StatusCode == 500 {
		return ObservabilityMetricsSeriesLabelsResponse{}, fmt.Errorf("internal Server Error from Services Server, %v", string(jsonBody))
	}

	response := make([]ObservabilityMetricsSeriesLabel, 0, 100)

	err = common.UnmarshalJson(jsonBody, &response)
	if err != nil {
		return ObservabilityMetricsSeriesLabelsResponse{}, err
	}

	return ObservabilityMetricsSeriesLabelsResponse{Labels: response}, nil
}

func ListMetricsSeriesLabelValues(ctx *security.RequestContext, accountId, provider, label string) (ObservabilityMetricsLabelValuesResponse, error) {
	queryPayload := map[string]any{
		"action": map[string]any{
			"name": "metrics_list_label_values",
		},
		"input": map[string]any{
			"request": map[string]any{
				"account_id":      accountId,
				"metric_provider": provider,
				"label":           label,
			},
		},
	}

	tenant := ctx.GetSecurityContext().GetTenantId()
	if tenant == "" {
		tenant1, err := security.GetTenantIdFromAccountId(accountId)
		if err != nil {
			return ObservabilityMetricsLabelValuesResponse{}, err
		}
		tenant = tenant1
	}

	if tenant == "" {
		return ObservabilityMetricsLabelValuesResponse{}, errors.New("tenant id is empty")
	}

	resp, err := common.HttpPost(fmt.Sprintf("%s/hasura/metrics", config.Config.ServiceEndpoint), common.HttpWithHeaders(map[string]string{
		"Content-Type":   contentTypeJson,
		"Accept":         contentTypeJson,
		"X-ACTION-TOKEN": config.Config.ServiceApiServerToken,
		"x-tenant-id":    tenant,
		"x-user-id":      ctx.GetSecurityContext().GetUserId(),
	}), common.HttpWithJsonBody(queryPayload))

	if err != nil {
		return ObservabilityMetricsLabelValuesResponse{}, fmt.Errorf("services: loglabels, unable to process request: %v", err)
	}
	defer func() {
		if resp.Body != nil {
			if err := resp.Body.Close(); err != nil {
				slog.Info("services_server: failed to close response body", "error", err)
			}
		}
	}()

	jsonBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return ObservabilityMetricsLabelValuesResponse{}, err
	}

	if resp.StatusCode == 401 {
		return ObservabilityMetricsLabelValuesResponse{}, fmt.Errorf("unauthorized: %v", string(jsonBody))
	}

	if resp.StatusCode == 500 {
		return ObservabilityMetricsLabelValuesResponse{}, fmt.Errorf("internal Server Error from Services Server, %v", string(jsonBody))
	}

	response := make([]ObservabilityMetricsLabelValue, 0, 100)

	err = common.UnmarshalJson(jsonBody, &response)
	if err != nil {
		return ObservabilityMetricsLabelValuesResponse{}, err
	}

	return ObservabilityMetricsLabelValuesResponse{Values: response}, nil
}

func QueryMetrics(ctx *security.RequestContext, request ObservabilityMetricsQueryRequest) (ObservabilityMetricsQueryResponse, error) {
	queryPayload := map[string]any{
		"action": map[string]any{
			"name": "metrics_query",
		},
		"input": map[string]any{
			"request": request,
		},
	}

	observabilityResp := ObservabilityMetricsQueryResponse{}

	tenant := ctx.GetSecurityContext().GetTenantId()
	if tenant == "" {
		tenant1, err := security.GetTenantIdFromAccountId(request.AccountId)
		if err != nil {
			return observabilityResp, err
		}
		tenant = tenant1
	}

	if tenant == "" {
		return observabilityResp, errors.New("tenant id is empty")
	}
	jsonBodyTemp := common.HttpWithJsonBody(queryPayload)

	resp, err := common.HttpPost(fmt.Sprintf("%s/hasura/metrics", config.Config.ServiceEndpoint), common.HttpWithHeaders(map[string]string{
		"Content-Type":   contentTypeJson,
		"Accept":         contentTypeJson,
		"X-ACTION-TOKEN": config.Config.ServiceApiServerToken,
		"x-tenant-id":    tenant,
		"x-user-id":      ctx.GetSecurityContext().GetUserId(),
	}), jsonBodyTemp)

	if err != nil {
		return observabilityResp, fmt.Errorf("services: traces, unable to process request: %v", err)
	}
	defer func() {
		if resp.Body != nil {
			if err := resp.Body.Close(); err != nil {
				slog.Info("services_server: failed to close response body", "error", err)
			}
		}
	}()

	jsonBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return observabilityResp, err
	}

	if resp.StatusCode != 200 {
		return observabilityResp, fmt.Errorf("metrics query failed (status %d): %s", resp.StatusCode, string(jsonBody))
	}

	err = common.UnmarshalJson(jsonBody, &observabilityResp)
	if err != nil {
		return observabilityResp, err
	}

	return observabilityResp, nil
}

func InvestigateEvent(tenantId string, events []Event) ([]string, error) {

	for _, e := range events {
		if err := common.ValidateStruct(e); err != nil {
			return nil, err
		}
	}

	serviceRequest := map[string]any{
		"action": map[string]any{
			"name": "trigger_investigation",
		},
		"input": map[string]any{
			"events": events,
		},
	}

	resp, err := common.HttpPost(fmt.Sprintf("%s/hasura/event", config.Config.ServiceEndpoint), common.HttpWithHeaders(map[string]string{
		"Content-Type":   contentTypeJson,
		"Accept":         contentTypeJson,
		"X-ACTION-TOKEN": config.Config.ServiceApiServerToken,
		"x-tenant-id":    tenantId,
	}), common.HttpWithJsonBody(serviceRequest))

	if err != nil {
		return nil, fmt.Errorf("unable to process events: %v", err)
	}

	defer func() {
		if resp.Body != nil {
			if err := resp.Body.Close(); err != nil {
				slog.Info("services_server: failed to close response body", "error", err)
			}
		}
	}()

	jsonBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("unable to process events: %v", string(jsonBody))
	}

	var responseData map[string]any
	err = json.Unmarshal(jsonBody, &responseData)
	if err != nil {
		return nil, err
	}

	if idsAny, ok := responseData["id"].([]any); ok {
		ids := make([]string, 0, len(idsAny))
		for _, v := range idsAny {
			if id, ok := v.(string); ok {
				ids = append(ids, id)
			}
		}
		return ids, nil
	} else if idsAny, ok := responseData["id"].([]string); ok {
		ids := idsAny
		return ids, nil
	}

	return nil, errors.New("events: unable to process request")
}

// FetchLogGroup invokes the Hasura `log_group` action (handler:
// {{SERVICE_API_SERVER_URL}}/hasura/logs-group) which routes by
// (LogProvider, LogProviderSource) to the per-provider
// LogSource.QueryLogGroup implementation on api-server. Use this in place of
// the old PromQL-on-container_log_messages_total approach so cloud (AWS,
// Datadog, NewRelic), SaaS (Signoz, Dynatrace), and ES paths all work.
func FetchLogGroup(ctx *security.RequestContext, request ObservabilityLogGroupQueryRequest) (ObservabilityLogGroupQueryResponse, error) {
	queryPayload := map[string]any{
		"action": map[string]any{
			"name": "log_group",
		},
		"input": map[string]any{
			"request": request,
		},
	}

	tenant := ctx.GetSecurityContext().GetTenantId()
	if tenant == "" {
		tenant1, err := security.GetTenantIdFromAccountId(request.AccountId)
		if err != nil {
			return ObservabilityLogGroupQueryResponse{}, err
		}
		tenant = tenant1
	}
	if tenant == "" {
		return ObservabilityLogGroupQueryResponse{}, errors.New("tenant id is empty")
	}

	resp, err := common.HttpPost(fmt.Sprintf("%s/hasura/logs-group", config.Config.ServiceEndpoint), common.HttpWithHeaders(map[string]string{
		"Content-Type":   contentTypeJson,
		"Accept":         contentTypeJson,
		"X-ACTION-TOKEN": config.Config.ServiceApiServerToken,
		"x-tenant-id":    tenant,
		"x-user-id":      ctx.GetSecurityContext().GetUserId(),
	}), common.HttpWithJsonBody(queryPayload))
	if err != nil {
		return ObservabilityLogGroupQueryResponse{}, fmt.Errorf("services: log_group, unable to process request: %v", err)
	}
	defer func() {
		if resp.Body != nil {
			if err := resp.Body.Close(); err != nil {
				slog.Info("services_server: failed to close response body", "error", err)
			}
		}
	}()

	jsonBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return ObservabilityLogGroupQueryResponse{}, err
	}

	if resp.StatusCode != 200 {
		return ObservabilityLogGroupQueryResponse{}, fmt.Errorf("log_group query failed (status %d): %s", resp.StatusCode, string(jsonBody))
	}

	var response ObservabilityLogGroupQueryResponse
	if err := common.UnmarshalJson(jsonBody, &response); err != nil {
		return ObservabilityLogGroupQueryResponse{}, err
	}
	return response, nil
}
