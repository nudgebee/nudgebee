package services_server

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/relay"
	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"
	"strings"
	"time"
)

const contentTypeJson = "application/json"

func ExecuteQuery(serviceRequest ServicesQueryRequest) (map[string]string, error) {
	response := make(map[string]string)

	resp, err := common.HttpPost(fmt.Sprintf("%s/rpc/query", config.Config.ServiceEndpoint), common.HttpWithHeaders(map[string]string{
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

func ExecuteScanImageQuery(scanImageRequest ScanImageServiceRequest) (map[string]string, error) {
	response := make(map[string]string)

	resp, err := common.HttpPost(fmt.Sprintf("%s/rpc/recommendation", config.Config.ServiceEndpoint), common.HttpWithHeaders(map[string]string{
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

func ExecuteScanCisQuery(scanCisRequest ScanCisServiceRequest) (map[string]string, error) {
	response := make(map[string]string)

	resp, err := common.HttpPost(fmt.Sprintf("%s/rpc/recommendation", config.Config.ServiceEndpoint), common.HttpWithHeaders(map[string]string{
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

func GetServiceDependencyGraph(ctx security.RequestContext, accountId, namespace, workload string) (GetServiceDependencyGraphResponse, error) {
	endTime := time.Now().UTC()
	startTime := endTime.Add(time.Hour * -6)
	relayFmt := "2006-01-02T15:04:05.000Z"
	actionParams := map[string]any{
		"r_end_time":   endTime.Format(relayFmt),
		"r_start_time": startTime.Format(relayFmt),
		"workload_filter": map[string]string{
			"workload_namespace": namespace,
			"workload_name":      workload,
		},
	}

	response, err := relay.Execute(relay.ActionExecuteBody{
		AccountID:    accountId,
		ActionName:   "service_map",
		ActionParams: actionParams,
	})

	if err != nil {
		ctx.GetLogger().Error("services: dependencygraph, unable to process request", "error", err)
		return GetServiceDependencyGraphResponse{}, err
	}

	if response["status_code"] == nil || response["status_code"].(float64) != 200 {
		ctx.GetLogger().Info("services: unable to process request", "response", slog.AnyValue(response))
		return GetServiceDependencyGraphResponse{}, errors.New("services: unable to process request")
	}

	data, ok := response["data"].(map[string]any)

	if !ok {
		ctx.GetLogger().Info("serices: unable to process request", "response", slog.AnyValue(response))
		return GetServiceDependencyGraphResponse{}, errors.New("serices: unable to process request")
	}

	dataArr, ok := data["data"].([]any)
	if !ok {
		ctx.GetLogger().Info("serices: unable to process request", "response", slog.AnyValue(response))
		return GetServiceDependencyGraphResponse{}, errors.New("serices: unable to process request")
	}

	if len(dataArr) == 0 {
		return GetServiceDependencyGraphResponse{
			Dependency:          []ServiceDependency{},
			DependencyStartTime: startTime,
			DependencyEndTime:   endTime,
		}, nil
	}

	byteArr, err := common.MarshalJson(dataArr)
	if err != nil {
		ctx.GetLogger().Info("serices: unable to process request", "error", err, "response", slog.AnyValue(response))
		return GetServiceDependencyGraphResponse{}, err
	}

	deps := make([]ServiceDependency, 0, len(dataArr))

	err = common.UnmarshalJson(byteArr, &deps)

	if err != nil {
		ctx.GetLogger().Info("serices: unable to process request", "error", err, "response", slog.AnyValue(response))
		return GetServiceDependencyGraphResponse{}, err
	}

	workloadKeys := make([]WorkloadKey, 0)
	// First pass: collect keys for batch fetching
	for _, dep := range deps {
		if dep.ID.Name != nil && dep.ID.Kind != nil && (strings.EqualFold(*dep.ID.Kind, "deployment") || strings.EqualFold(*dep.ID.Kind, "statefulset") || strings.EqualFold(*dep.ID.Kind, "daemonset") || strings.EqualFold(*dep.ID.Kind, "job")) {
			workloadKeys = append(workloadKeys, WorkloadKey{
				Namespace:    *dep.ID.Namespace,
				WorkloadName: *dep.ID.Name,
			})
		}
	}

	// Batch fetch repo info
	repos, err := GetSourceCodeReposBatch(&ctx, accountId, workloadKeys)
	if err != nil {
		ctx.GetLogger().Warn("services: failed to batch fetch source code repos", "error", err)
	}

	for i, dep := range deps {
		if dep.ID.Name != nil && dep.ID.Kind != nil && (strings.EqualFold(*dep.ID.Kind, "deployment") || strings.EqualFold(*dep.ID.Kind, "statefulset") || strings.EqualFold(*dep.ID.Kind, "daemonset") || strings.EqualFold(*dep.ID.Kind, "job")) {
			key := WorkloadKey{
				Namespace:    *dep.ID.Namespace,
				WorkloadName: *dep.ID.Name,
			}

			if repos != nil {
				if repo, ok := repos[key]; ok {
					if repo.CodeRepo == "" && repo.CIRepo == "" {
						continue
					}
					dep.SourceCode = ServiceDependencySourceCode{
						CodeRepo: repo.CodeRepo,
						CiCdRepo: repo.CIRepo,
					}
					deps[i] = dep
				}
			}
		}
	}

	return GetServiceDependencyGraphResponse{
		Dependency:                  deps,
		DependencyStartTime:         startTime,
		DependencyEndTime:           endTime,
		DependencyWorkloadName:      workload,
		DependencyWorkloadNamespace: namespace,
	}, nil
}

func QueryLogs(ctx security.RequestContext, request LogQueryRequest) (core.ObservabilityLogResponse, error) {
	if request.Limit == 0 {
		request.Limit = 1000
	}

	queryPayload := map[string]any{
		"action": map[string]any{
			"name": "logs_query",
		},
		"input": map[string]any{
			"request": request,
		},
	}

	observabilityResp := core.ObservabilityLogResponse{
		Metadata: core.ObservabilityLogMetadata{
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

	resp, err := common.HttpPost(fmt.Sprintf("%s/rpc/logs", config.Config.ServiceEndpoint), common.HttpWithHeaders(map[string]string{
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

	if resp.StatusCode == 401 {
		return observabilityResp, fmt.Errorf("unauthorized: %v", string(jsonBody))
	}

	if resp.StatusCode == 500 {
		return observabilityResp, fmt.Errorf("internal Server Error from Services Server, %v", string(jsonBody))
	}

	// The services server may return an error object (e.g. {"message":"..."}) instead of a log array.
	// Detect this before attempting slice unmarshalling.
	trimmed := bytes.TrimLeft(jsonBody, " \t\r\n")
	if len(trimmed) > 0 && trimmed[0] == '{' {
		var errResp struct {
			Message string `json:"message"`
		}
		if unmarshalErr := common.UnmarshalJson(jsonBody, &errResp); unmarshalErr == nil && errResp.Message != "" {
			return observabilityResp, fmt.Errorf("services: logs query error: %s", errResp.Message)
		}
		return observabilityResp, fmt.Errorf("services: logs, unexpected object response: %s", string(jsonBody))
	}

	response := make([]core.ObservabilityLog, 0, 100)
	err = common.UnmarshalJson(jsonBody, &response)
	if err != nil {
		return observabilityResp, err
	}

	observabilityResp.Logs = response

	return observabilityResp, nil
}

func QueryTraces(ctx security.RequestContext, request core.ObservabilityTracesV3Request) (core.ObservabilityTraceResponse, error) {
	queryPayload := map[string]any{
		"action": map[string]any{
			"name": "traces_query",
		},
		"input": map[string]any{
			"request": request,
		},
	}

	observabilityResp := core.ObservabilityTraceResponse{
		Metadata: core.ObservabilityTraceMetadata{
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

	resp, err := common.HttpPost(fmt.Sprintf("%s/rpc/traces", config.Config.ServiceEndpoint), common.HttpWithHeaders(map[string]string{
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

	if resp.StatusCode == 401 {
		return observabilityResp, fmt.Errorf("unauthorized: %v", string(jsonBody))
	}

	if resp.StatusCode == 500 {
		return observabilityResp, fmt.Errorf("internal Server Error from Services Server, %v", string(jsonBody))
	}

	response := make([]core.ObservabilityTrace, 0, 100)
	err = common.UnmarshalJson(jsonBody, &response)
	if err != nil {
		return observabilityResp, err
	}

	observabilityResp.Traces = response

	return observabilityResp, nil
}

func QueryLogLabels(ctx security.RequestContext, accountId string, provider ObservabilityProvider) (core.ObservabilityLogLabelResponse, error) {

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

	if strings.EqualFold(provider.Provider, "ES") || strings.EqualFold(provider.Provider, "elasticsearch") {
		queryPayload["input"].(map[string]any)["request"].(map[string]any)["request"] = map[string]any{
			"index": "fluentk8-*",
		}
		queryPayload["input"].(map[string]any)["request"].(map[string]any)["fetch_index"] = true
	}

	tenant := ctx.GetSecurityContext().GetTenantId()
	if tenant == "" {
		tenant1, err := security.GetTenantIdFromAccountId(accountId)
		if err != nil {
			return core.ObservabilityLogLabelResponse{}, err
		}
		tenant = tenant1
	}

	if tenant == "" {
		return core.ObservabilityLogLabelResponse{}, errors.New("tenant id is empty")
	}

	resp, err := common.HttpPost(fmt.Sprintf("%s/rpc/logs", config.Config.ServiceEndpoint), common.HttpWithHeaders(map[string]string{
		"Content-Type":   contentTypeJson,
		"Accept":         contentTypeJson,
		"X-ACTION-TOKEN": config.Config.ServiceApiServerToken,
		"x-tenant-id":    tenant,
		"x-user-id":      ctx.GetSecurityContext().GetUserId(),
	}), common.HttpWithJsonBody(queryPayload))

	if err != nil {
		return core.ObservabilityLogLabelResponse{}, fmt.Errorf("services: loglabels, unable to process request: %v", err)
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
		return core.ObservabilityLogLabelResponse{}, err
	}

	if resp.StatusCode == 401 {
		return core.ObservabilityLogLabelResponse{}, fmt.Errorf("unauthorized: %v", string(jsonBody))
	}

	if resp.StatusCode == 500 {
		return core.ObservabilityLogLabelResponse{}, fmt.Errorf("internal Server Error from Services Server, %v", string(jsonBody))
	}

	response := make([]core.ObservabilityLogLabel, 0, 100)

	err = common.UnmarshalJson(jsonBody, &response)
	if err != nil {
		return core.ObservabilityLogLabelResponse{}, err
	}

	return core.ObservabilityLogLabelResponse{Labels: response}, nil
}

type ObservabilityProvider struct {
	IntegrationSource string `json:"integrationSource"`
	Provider          string `json:"provider"`
	// DefaultIndex is the backend's account-default log index/pattern as
	// returned by the get_default_provider action (empty for backends that
	// have no index concept, e.g. Loki). Surfaced into the query-generator
	// prompt so the LLM knows what omitting the `index` field resolves to.
	DefaultIndex string `json:"default_index"`
}

func GetObservabilityProvider(ctx security.RequestContext, accountId, provider string) (ObservabilityProvider, error) {
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

	resp, err := common.HttpPost(fmt.Sprintf("%s/rpc/provider", config.Config.ServiceEndpoint), common.HttpWithHeaders(map[string]string{
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

func CreateIntegrationConfig(ctx security.RequestContext, serviceRequest IntegrationCreateConfig) (map[string]any, error) {
	response := make(map[string]any)

	resp, err := common.HttpPost(
		fmt.Sprintf("%s/rpc/integration", config.Config.ServiceEndpoint),
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

func ListMetricsSeries(ctx security.RequestContext, accountId, provider, filter string) (core.ObservabilityMetricsSeriesResponse, error) {
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

	if filter != "" {
		if req, ok := queryPayload["input"].(map[string]any)["request"].(map[string]any); ok {
			req["metric"] = filter
		}
	}

	tenant := ctx.GetSecurityContext().GetTenantId()
	if tenant == "" {
		tenant1, err := security.GetTenantIdFromAccountId(accountId)
		if err != nil {
			return core.ObservabilityMetricsSeriesResponse{}, err
		}
		tenant = tenant1
	}

	if tenant == "" {
		return core.ObservabilityMetricsSeriesResponse{}, errors.New("tenant id is empty")
	}

	resp, err := common.HttpPost(fmt.Sprintf("%s/rpc/metrics", config.Config.ServiceEndpoint), common.HttpWithHeaders(map[string]string{
		"Content-Type":   contentTypeJson,
		"Accept":         contentTypeJson,
		"X-ACTION-TOKEN": config.Config.ServiceApiServerToken,
		"x-tenant-id":    tenant,
		"x-user-id":      ctx.GetSecurityContext().GetUserId(),
	}), common.HttpWithJsonBody(queryPayload))

	if err != nil {
		return core.ObservabilityMetricsSeriesResponse{}, fmt.Errorf("services: list_metrics_series, unable to process request: %v", err)
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
		return core.ObservabilityMetricsSeriesResponse{}, err
	}

	if resp.StatusCode != 200 {
		return core.ObservabilityMetricsSeriesResponse{}, fmt.Errorf("services: metrics_list failed with status %d: %s", resp.StatusCode, string(jsonBody))
	}

	response := make([]core.ObservabilityMetricsSeries, 0, 100)

	err = common.UnmarshalJson(jsonBody, &response)
	if err != nil {
		// Try unmarshaling into a map to handle wrapped responses
		var wrappedResponse struct {
			Data []core.ObservabilityMetricsSeries `json:"data"`
		}
		if err2 := common.UnmarshalJson(jsonBody, &wrappedResponse); err2 == nil {
			response = wrappedResponse.Data
		} else {
			return core.ObservabilityMetricsSeriesResponse{}, err
		}
	}

	return core.ObservabilityMetricsSeriesResponse{Series: response}, nil
}

func ListMetricsSeriesLabels(ctx security.RequestContext, accountId, provider, seriesName string) (core.ObservabilityMetricsSeriesLabelsResponse, error) {
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
			return core.ObservabilityMetricsSeriesLabelsResponse{}, err
		}
		tenant = tenant1
	}

	if tenant == "" {
		return core.ObservabilityMetricsSeriesLabelsResponse{}, errors.New("tenant id is empty")
	}

	resp, err := common.HttpPost(fmt.Sprintf("%s/rpc/metrics", config.Config.ServiceEndpoint), common.HttpWithHeaders(map[string]string{
		"Content-Type":   contentTypeJson,
		"Accept":         contentTypeJson,
		"X-ACTION-TOKEN": config.Config.ServiceApiServerToken,
		"x-tenant-id":    tenant,
		"x-user-id":      ctx.GetSecurityContext().GetUserId(),
	}), common.HttpWithJsonBody(queryPayload))

	if err != nil {
		return core.ObservabilityMetricsSeriesLabelsResponse{}, fmt.Errorf("services: loglabels, unable to process request: %v", err)
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
		return core.ObservabilityMetricsSeriesLabelsResponse{}, err
	}

	if resp.StatusCode != 200 {
		return core.ObservabilityMetricsSeriesLabelsResponse{}, fmt.Errorf("services: metrics_list_labels failed with status %d: %s", resp.StatusCode, string(jsonBody))
	}

	response := make([]core.ObservabilityMetricsSeriesLabel, 0, 100)

	err = common.UnmarshalJson(jsonBody, &response)
	if err != nil {
		// Try unmarshaling into a map to handle wrapped responses
		var wrappedResponse struct {
			Data []core.ObservabilityMetricsSeriesLabel `json:"data"`
		}
		if err2 := common.UnmarshalJson(jsonBody, &wrappedResponse); err2 == nil {
			response = wrappedResponse.Data
		} else {
			return core.ObservabilityMetricsSeriesLabelsResponse{}, err
		}
	}

	return core.ObservabilityMetricsSeriesLabelsResponse{Labels: response}, nil
}

// ListMetricsSeriesLabelValues fetches values for a given label. For ES/Opensearch, pass
// request with metric_name set to the index pattern (e.g. {"metric_name": "metrics-*"}).
func ListMetricsSeriesLabelValues(ctx security.RequestContext, accountId, provider, label string, request ...map[string]any) (core.ObservabilityMetricsLabelValuesResponse, error) {
	reqMap := map[string]any{
		"account_id":      accountId,
		"metric_provider": provider,
		"label":           label,
	}
	if len(request) > 0 && request[0] != nil {
		maps.Copy(reqMap, request[0])
	}
	queryPayload := map[string]any{
		"action": map[string]any{
			"name": "metrics_list_label_values",
		},
		"input": map[string]any{
			"request": reqMap,
		},
	}

	tenant := ctx.GetSecurityContext().GetTenantId()
	if tenant == "" {
		tenant1, err := security.GetTenantIdFromAccountId(accountId)
		if err != nil {
			return core.ObservabilityMetricsLabelValuesResponse{}, err
		}
		tenant = tenant1
	}

	if tenant == "" {
		return core.ObservabilityMetricsLabelValuesResponse{}, errors.New("tenant id is empty")
	}

	resp, err := common.HttpPost(fmt.Sprintf("%s/rpc/metrics", config.Config.ServiceEndpoint), common.HttpWithHeaders(map[string]string{
		"Content-Type":   contentTypeJson,
		"Accept":         contentTypeJson,
		"X-ACTION-TOKEN": config.Config.ServiceApiServerToken,
		"x-tenant-id":    tenant,
		"x-user-id":      ctx.GetSecurityContext().GetUserId(),
	}), common.HttpWithJsonBody(queryPayload))

	if err != nil {
		return core.ObservabilityMetricsLabelValuesResponse{}, fmt.Errorf("services: loglabels, unable to process request: %v", err)
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
		return core.ObservabilityMetricsLabelValuesResponse{}, err
	}

	if resp.StatusCode != 200 {
		return core.ObservabilityMetricsLabelValuesResponse{}, fmt.Errorf("services: metrics_list_label_values failed with status %d: %s", resp.StatusCode, string(jsonBody))
	}

	response := make([]core.ObservabilityMetricsLabelValue, 0, 100)

	err = common.UnmarshalJson(jsonBody, &response)
	if err != nil {
		// Try unmarshaling into a map to handle wrapped responses
		var wrappedResponse struct {
			Data []core.ObservabilityMetricsLabelValue `json:"data"`
		}
		if err2 := common.UnmarshalJson(jsonBody, &wrappedResponse); err2 == nil {
			response = wrappedResponse.Data
		} else {
			return core.ObservabilityMetricsLabelValuesResponse{}, err
		}
	}

	return core.ObservabilityMetricsLabelValuesResponse{Values: response}, nil
}

// QueryMetrics executes a metrics_query action against api-server (e.g. Elasticsearch DSL aggregation).
func QueryMetrics(ctx security.RequestContext, req core.ObservabilityMetricsQueryRequest) (core.ObservabilityMetricsQueryResponse, error) {
	queryPayload := map[string]any{
		"action": map[string]any{
			"name": "metrics_query",
		},
		"input": map[string]any{
			"request": map[string]any{
				"account_id":      req.AccountId,
				"metric_provider": req.MetricProvider,
				"queries":         req.Queries,
				"start_time":      req.StartTime,
				"end_time":        req.EndTime,
				"request":         req.Request,
			},
		},
	}

	tenant := ctx.GetSecurityContext().GetTenantId()
	if tenant == "" {
		tenant1, err := security.GetTenantIdFromAccountId(req.AccountId)
		if err != nil {
			return core.ObservabilityMetricsQueryResponse{}, fmt.Errorf("services: query_metrics, unable to resolve tenant: %w", err)
		}
		tenant = tenant1
	}

	if tenant == "" {
		return core.ObservabilityMetricsQueryResponse{}, errors.New("tenant id is empty")
	}

	resp, err := common.HttpPost(fmt.Sprintf("%s/rpc/metrics", config.Config.ServiceEndpoint), common.HttpWithHeaders(map[string]string{
		"Content-Type":   contentTypeJson,
		"Accept":         contentTypeJson,
		"X-ACTION-TOKEN": config.Config.ServiceApiServerToken,
		"x-tenant-id":    tenant,
		"x-user-id":      ctx.GetSecurityContext().GetUserId(),
	}), common.HttpWithJsonBody(queryPayload))

	if err != nil {
		return core.ObservabilityMetricsQueryResponse{}, fmt.Errorf("services: query_metrics, unable to process request: %w", err)
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
		return core.ObservabilityMetricsQueryResponse{}, fmt.Errorf("services: query_metrics, unable to read response: %w", err)
	}

	if resp.StatusCode != 200 {
		return core.ObservabilityMetricsQueryResponse{}, fmt.Errorf("services: metrics_query failed with status %d: %s", resp.StatusCode, string(jsonBody))
	}

	var response core.ObservabilityMetricsQueryResponse
	err = common.UnmarshalJson(jsonBody, &response)
	if err != nil {
		// Try wrapped response
		var wrappedResponse struct {
			Data core.ObservabilityMetricsQueryResponse `json:"data"`
		}
		if err2 := common.UnmarshalJson(jsonBody, &wrappedResponse); err2 == nil {
			response = wrappedResponse.Data
		} else {
			return core.ObservabilityMetricsQueryResponse{}, fmt.Errorf("services: query_metrics, unable to parse response (direct: %v, wrapped: %v)", err, err2)
		}
	}

	return response, nil
}
