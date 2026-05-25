package relay

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net"
	"net/http"
	"nudgebee/services/common"
	"nudgebee/services/config"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

const errMsgAgentNotConnected = "agent not connected"

var relayHttpClient *http.Client

func init() {
	transport := &http.Transport{
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		MaxIdleConns:        50,
		MaxIdleConnsPerHost: 50,
		IdleConnTimeout:     90 * time.Second,
		TLSHandshakeTimeout: 10 * time.Second,
		ForceAttemptHTTP2:   true,
	}
	relayHttpClient = &http.Client{
		Transport: otelhttp.NewTransport(transport),
	}
}

func isRetryableError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
		return true
	}
	if errors.Is(err, syscall.ECONNRESET) {
		return true
	}
	var netErr *net.OpError
	return errors.As(err, &netErr)
}

func ExecutePrometheus(accountId string, startTime time.Time, endTime time.Time, queriesMap map[string]string, instant bool) (map[string]any, error) {

	promsqlQueries := make([]map[string]any, 0, len(queriesMap))
	for key, query := range queriesMap {
		promsqlQueries = append(promsqlQueries, map[string]any{
			"key":   key,
			"query": query,
		})
	}

	// The relay/agent interpret these timestamps as actual UTC. time.Now()
	// returns local time, and Format("...UTC") only appends a literal "UTC"
	// label — it doesn't convert the value. Without the explicit .UTC()
	// conversion, a host in IST sends "18:17 UTC" when it means "12:47 UTC",
	// putting the query 5.5h in the future and causing Prometheus to return
	// an empty result set. Reproduces locally when api-server runs outside a
	// UTC container (every flow-source resolver that calls ExecutePrometheus
	// silently degrades to no resolution).
	relayResponse, err := Execute(RelayExecuteRequest{
		Body: ActionExecuteBody{
			AccountID:  accountId,
			ActionName: "prometheus_queries_enricher",
			ActionParams: map[string]any{
				"duration": map[string]any{
					"ends_at":   endTime.UTC().Format("2006-01-02 15:04:05 UTC"),
					"starts_at": startTime.UTC().Format("2006-01-02 15:04:05 UTC"),
				},
				"promql_query":   "",
				"promql_queries": promsqlQueries,
				"instant":        instant,
			},
			Origin: "services-server",
		},
		NoSinks: true,
		Cache:   false,
	})

	if err != nil {
		slog.Error("relay: unable to process request", "error", err)
		return map[string]any{}, err
	}

	relayDataAny := relayResponse["data"]
	if relayDataAny == nil {
		slog.Error("relay: unable to process request", "response", slog.AnyValue(relayResponse))
		return map[string]any{}, errors.New("relay: unable to execute relay query")
	}

	relayData, ok := relayDataAny.(map[string]any)
	if !ok {
		slog.Error("relay: unable to process request", "response", slog.AnyValue(relayResponse))
		return map[string]any{}, errors.New("relay: unable to execute relay query")
	}

	if relayData["success"] != nil {
		if success, ok := relayData["success"].(bool); !ok || !success {
			slog.Error("relay: relay query success is false",
				"relay_error_code", relayData["error_code"],
				"relay_msg", relayData["msg"],
				"response", slog.AnyValue(relayData))
			return map[string]any{}, errors.New("relay: unable to execute relay query")
		}
	}

	findingsAny, ok := relayData["findings"]
	if !ok {
		slog.Error("relay: relay findings not found", "response", slog.AnyValue(relayData))
		return map[string]any{}, errors.New("relay: unable to execute relay query")
	}

	findingsArray, ok := findingsAny.([]any)
	if !ok {
		slog.Error("relay: relay findings not an array", "response", slog.AnyValue(relayData))
		return map[string]any{}, errors.New("relay: unable to execute relay query")
	}

	if len(findingsArray) == 0 {
		slog.Warn("relay: relay findings is empty", "response", slog.AnyValue(relayData))
		return map[string]any{}, errors.New("relay: unable to execute relay query")
	}

	findingMap, ok := findingsArray[0].(map[string]any)
	if !ok {
		slog.Error("relay: relay findings not an object", "response", slog.AnyValue(findingsArray[0]))
		return map[string]any{}, errors.New("relay: unable to execute relay query")
	}

	evidenceAny := findingMap["evidence"]
	if evidenceAny == nil {
		slog.Error("relay: relay findings evidence not found", "response", slog.AnyValue(findingMap))
		return map[string]any{}, errors.New("relay: unable to execute relay query")
	}

	evidenceArray, ok := evidenceAny.([]any)
	if !ok {
		slog.Error("relay: relay findings evidence not an array", "response", slog.AnyValue(evidenceAny))
		return map[string]any{}, errors.New("relay: unable to execute relay query")
	}

	if len(evidenceArray) == 0 {
		slog.Error("relay: evidence is empty", "response", slog.AnyValue(evidenceArray))
		return map[string]any{}, errors.New("relay: unable to execute relay query")
	}

	evidenceMap, ok := evidenceArray[0].(map[string]any)
	if !ok {
		slog.Error("relay: relay findings evidence not an object", "response", slog.AnyValue(evidenceArray[0]))
		return map[string]any{}, errors.New("relay: unable to execute relay query")
	}

	evidenceData := evidenceMap["data"]
	if evidenceData == nil {
		slog.Error("relay: relay findings evidence data not found", "response", slog.AnyValue(evidenceMap))
		return map[string]any{}, errors.New("relay: unable to execute relay query")
	}

	dataMapArray := []map[string]any{}
	evidenceDataStr, ok := evidenceData.(string)
	if !ok {
		slog.Error("relay: relay findings evidence data not a string", "response", slog.AnyValue(evidenceData))
		return map[string]any{}, errors.New("relay: unable to execute relay query")
	}
	err = common.UnmarshalJson([]byte(evidenceDataStr), &dataMapArray)
	if err != nil {
		slog.Error("relay: relay findings evidence data not an array", "response", slog.AnyValue(evidenceData))
		return map[string]any{}, errors.New("relay: unable to execute relay query")
	}

	if len(dataMapArray) == 0 {
		slog.Error("relay: no data in findings", "response", slog.AnyValue(evidenceData))
		return map[string]any{}, errors.New("relay: unable to execute relay query")
	}

	dataResponse := dataMapArray[0]

	dataStr := dataResponse["data"]
	if dataStr == nil {
		slog.Error("relay: no data in evidence", "response", slog.AnyValue(dataResponse))
		return map[string]any{}, errors.New("relay: unable to execute relay query")
	}

	dataMap := map[string]any{}
	dataStrVal, ok := dataStr.(string)
	if !ok {
		return map[string]any{}, errors.New("relay: data is not a string")
	}
	err = common.UnmarshalJson([]byte(dataStrVal), &dataMap)
	if err != nil {
		return map[string]any{}, err
	}

	return dataMap, nil
}

func ExecuteAndExtractResponse(relayRequest RelayExecuteRequest) (map[string]any, map[string]any, error) {
	relayResponse, err := Execute(relayRequest)

	if err != nil {
		return nil, nil, err
	}

	relayDataAny := relayResponse["data"]
	if relayDataAny == nil {
		slog.Error("relay: unable to process request", "response", slog.AnyValue(relayResponse))
		return nil, nil, errors.New("relay: unable to execute relay query")
	}

	relayData, ok := relayDataAny.(map[string]any)
	if !ok {
		slog.Error("relay: unable to process request", "response", slog.AnyValue(relayResponse))
		return nil, nil, errors.New("relay: unable to execute relay query")
	}

	if relayData["success"] != nil {
		if success, ok := relayData["success"].(bool); !ok || !success {
			slog.Error("relay: relay query success is false",
				"relay_error_code", relayData["error_code"],
				"relay_msg", relayData["msg"],
				"response", slog.AnyValue(relayData))
			return nil, nil, fmt.Errorf("relay: unable to execute relay query -  %v", relayData["msg"])
		}
	}

	findingsAny, ok := relayData["findings"]
	if !ok {
		slog.Error("relay: relay findings not found", "response", slog.AnyValue(relayData))
		return nil, nil, errors.New("relay: unable to execute relay query")
	}

	findingsArray, ok := findingsAny.([]any)
	if !ok {
		slog.Error("relay: relay findings not an array", "response", slog.AnyValue(relayData))
		return nil, nil, errors.New("relay: unable to execute relay query")
	}

	if len(findingsArray) == 0 {
		slog.Warn("relay: relay findings is empty", "response", slog.AnyValue(relayData))
		return nil, nil, errors.New("relay: unable to execute relay query")
	}

	findingMap, ok := findingsArray[0].(map[string]any)
	if !ok {
		slog.Error("relay: relay findings not an object", "response", slog.AnyValue(findingsArray[0]))
		return nil, nil, errors.New("relay: unable to execute relay query")
	}

	evidenceAny := findingMap["evidence"]
	if evidenceAny == nil {
		slog.Error("relay: relay findings evidence not found", "response", slog.AnyValue(findingMap))
		return nil, nil, errors.New("relay: unable to execute relay query")
	}

	evidenceArray, ok := evidenceAny.([]any)
	if !ok {
		slog.Error("relay: relay findings evidence not an array", "response", slog.AnyValue(evidenceAny))
		return nil, nil, errors.New("relay: unable to execute relay query")
	}

	if len(evidenceArray) == 0 {
		slog.Error("relay: evidence is empty", "response", slog.AnyValue(evidenceArray))
		return nil, nil, errors.New("relay: unable to execute relay query")
	}

	evidenceMap, ok := evidenceArray[0].(map[string]any)
	if !ok {
		slog.Error("relay: relay findings evidence not an object", "response", slog.AnyValue(evidenceArray[0]))
		return nil, nil, errors.New("relay: unable to execute relay query")
	}

	evidenceData := evidenceMap["data"]
	if evidenceData == nil {
		slog.Error("relay: relay findings evidence data not found", "response", slog.AnyValue(evidenceMap))
		return nil, nil, errors.New("relay: unable to execute relay query")
	}

	evidenceDataString, ok := evidenceData.(string)
	if !ok {
		slog.Error("relay: relay findings evidence data not a string", "response", slog.AnyValue(evidenceData))
		return nil, nil, errors.New("relay: unable to execute relay query")
	}
	responseArr := []map[string]any{}
	err = json.Unmarshal([]byte(evidenceDataString), &responseArr)
	if err != nil {
		slog.Error("relay: relay findings evidence data not an array", "response", slog.AnyValue(evidenceData))
	}
	if len(responseArr) == 0 {
		return nil, nil, errors.New("relay: unable to execute relay query")
	}
	agentActionResponse := responseArr[0]
	additionalInfo, ok := agentActionResponse["additional_info"].(map[string]any)
	if !ok {
		additionalInfo = map[string]any{}
	}
	return agentActionResponse, additionalInfo, nil
}

func Execute(relayRequest RelayExecuteRequest) (map[string]any, error) {
	data := make(map[string]any)

	maxRetries := 3
	baseRetryInterval := 2 * time.Second

	// Extract Account ID for better logging
	accountID := relayRequest.Body.AccountID // Adjust this based on actual struct field

	timeout := time.Duration(relayRequest.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 180 * time.Second
	}

	for attempt := 0; attempt < maxRetries; attempt++ {
		// Create body fresh each attempt — bytes.Reader is consumed after first read
		reqBody := common.HttpWithJsonBody(relayRequest)
		headers := map[string]string{
			"Content-Type": "application/json",
			"Accept":       "application/json",
			"X-SECRET-KEY": config.Config.RelayServerSecretKey,
		}
		if relayRequest.AgentType != "" {
			headers["X-NB-Agent-Type"] = relayRequest.AgentType
		}
		resp, err := common.HttpPost(fmt.Sprintf("%s/request", config.Config.RelayServerEndpoint), common.HttpWithClient(relayHttpClient), common.HttpWithHeaders(headers), reqBody, common.HttpWithTimeout(timeout))

		retryDelay := baseRetryInterval * time.Duration(1<<uint(attempt))

		if err != nil {
			if isRetryableError(err) && attempt < maxRetries-1 {
				slog.Warn("relay: Transient error sending request, retrying..", "account_id", accountID, "attempt", attempt+1, "max_retries", maxRetries, "error", err)
				time.Sleep(retryDelay)
				continue
			}
			slog.Error("relay: Failed to send request to relay server", "account_id", accountID, "error", err)
			return data, errors.New("unable to consume relay API")
		}

		if resp.StatusCode == 401 {
			if closeErr := resp.Body.Close(); closeErr != nil {
				slog.Warn("relay: error closing response body", "error", closeErr)
			}
			slog.Warn("relay: Unauthorized access", "account_id", accountID, "status", resp.StatusCode)
			return data, errors.New("relay: unauthorized")
		}
		if resp.StatusCode == 504 {
			if closeErr := resp.Body.Close(); closeErr != nil {
				slog.Warn("relay: error closing response body", "error", closeErr)
			}
			slog.Warn("relay: Relay server request timeout", "account_id", accountID, "status", resp.StatusCode)
			return data, errors.New("relay: request timeout")
		}

		jsonBody, readErr := io.ReadAll(resp.Body)
		if closeErr := resp.Body.Close(); closeErr != nil {
			slog.Warn("relay: error closing response body", "error", closeErr)
		}

		if readErr != nil {
			if isRetryableError(readErr) && attempt < maxRetries-1 {
				slog.Warn("relay: Transient error reading response body, retrying..", "account_id", accountID, "attempt", attempt+1, "max_retries", maxRetries, "error", readErr)
				time.Sleep(retryDelay)
				continue
			}
			slog.Error("relay: Failed to read response body", "account_id", accountID, "error", readErr)
			return data, errors.New("relay: failed to read response body")
		}

		switch resp.StatusCode {
		case 400:
			// Check if error matches "Agent not found/connected"
			var errorResponse struct {
				Errors []struct {
					Code    int    `json:"code"`
					Message string `json:"message"`
				} `json:"errors"`
			}

			if err := common.UnmarshalJson(jsonBody, &errorResponse); err == nil {
				for _, e := range errorResponse.Errors {
					if e.Code == 400 && e.Message == errMsgAgentNotConnected {
						slog.Warn("relay: Agent not found/connected", "account_id", accountID)
						return data, errors.New(errMsgAgentNotConnected)
					} else {
						slog.Error("relay: Bad Request", "account_id", accountID, "error_code", e.Code, "error_message", e.Message)
						return data, errors.New("relay: request failed")
					}
				}
			} else {
				return data, errors.New("relay: request failed")
			}
		case 200:
			// Successful response
			responseData := map[string]any{}
			err = common.UnmarshalJson(jsonBody, &responseData)
			if err != nil {
				slog.Error("relay: Failed to parse response JSON", "account_id", accountID, "error", err)
				return data, errors.New("relay: failed to parse response")
			}

			if responseDataMap, ok := responseData["data"].(map[string]interface{}); ok {
				if errorMsg, ok := responseDataMap["error_msg"].(string); ok {
					slog.Warn("relay: Received error message from relay", "account_id", accountID, "error_msg", errorMsg)
					return responseData, common.ErrorBadRequest("relay: operation failed")
				}
			}

			slog.Info("relay: Successfully fetched data from relay", "account_id", accountID, "status", resp.StatusCode)
			return responseData, nil
		default:
			slog.Error("relay: Unexpected error while fetching data from relay", "account_id", accountID, "status", resp.StatusCode)
			return nil, errors.New("relay: unexpected error")
		}
	}

	slog.Error("relay: Max retries reached, aborting", "account_id", accountID)
	return data, errors.New("agent not found/connected after max retries")
}

func ExecuteRelayProxyApi(accountID string, params map[string]any, apiPath string) (map[string]any, error) {
	data := make(map[string]any)

	maxRetries := 3
	baseRetryInterval := 2 * time.Second

	for attempt := 0; attempt < maxRetries; attempt++ {
		resp, err := common.HttpGet(fmt.Sprintf("%s/%s", config.Config.RelayServerEndpoint, apiPath), common.HttpWithClient(relayHttpClient), common.HttpWithHeaders(map[string]string{
			"Content-Type":    "application/json",
			"Accept":          "application/json",
			"X-SECRET-KEY":    config.Config.RelayServerSecretKey,
			"X-NB-ACCOUNT-ID": accountID,
		}), common.HttpWithTimeout(time.Second*time.Duration(120)))

		retryDelay := baseRetryInterval * time.Duration(1<<uint(attempt))

		if err != nil {
			if isRetryableError(err) && attempt < maxRetries-1 {
				slog.Warn("relay: Transient error sending request, retrying..", "account_id", accountID, "attempt", attempt+1, "max_retries", maxRetries, "error", err)
				time.Sleep(retryDelay)
				continue
			}
			slog.Error("relay: Failed to send request to relay server", "account_id", accountID, "error", err)
			return data, errors.New("unable to consume relay API")
		}

		if resp.StatusCode == 401 {
			if closeErr := resp.Body.Close(); closeErr != nil {
				slog.Warn("relay: error closing response body", "error", closeErr)
			}
			slog.Warn("relay: Unauthorized access", "account_id", accountID, "status", resp.StatusCode)
			return data, errors.New("relay: unauthorized")
		}
		if resp.StatusCode == 504 {
			if closeErr := resp.Body.Close(); closeErr != nil {
				slog.Warn("relay: error closing response body", "error", closeErr)
			}
			slog.Warn("relay: Relay server request timeout", "account_id", accountID, "status", resp.StatusCode)
			return data, errors.New("relay: request timeout")
		}

		jsonBody, readErr := io.ReadAll(resp.Body)
		if closeErr := resp.Body.Close(); closeErr != nil {
			slog.Warn("relay: error closing response body", "error", closeErr)
		}

		if readErr != nil {
			if isRetryableError(readErr) && attempt < maxRetries-1 {
				slog.Warn("relay: Transient error reading response body, retrying..", "account_id", accountID, "attempt", attempt+1, "max_retries", maxRetries, "error", readErr)
				time.Sleep(retryDelay)
				continue
			}
			slog.Error("relay: Failed to read response body", "account_id", accountID, "error", readErr)
			return data, errors.New("relay: failed to read response body")
		}

		switch resp.StatusCode {
		case 400:
			// Check if error matches "Agent not found/connected"
			var errorResponse struct {
				Errors []struct {
					Code    int    `json:"code"`
					Message string `json:"message"`
				} `json:"errors"`
			}

			if err := common.UnmarshalJson(jsonBody, &errorResponse); err == nil {
				for _, e := range errorResponse.Errors {
					if e.Code == 400 && e.Message == errMsgAgentNotConnected {
						slog.Warn("relay: Agent not found/connected", "account_id", accountID)
						return data, errors.New(errMsgAgentNotConnected)
					} else {
						slog.Error("relay: Bad Request", "account_id", accountID, "error_code", e.Code, "error_message", e.Message)
						return data, errors.New("relay: request failed")
					}
				}
			} else {
				return data, errors.New("relay: request failed")
			}
		case 200:
			// Successful response
			responseData := map[string]any{}
			err = common.UnmarshalJson(jsonBody, &responseData)
			if err != nil {
				slog.Error("relay: Failed to parse response JSON", "account_id", accountID, "error", err)
				return data, errors.New("relay: failed to parse response")
			}

			if responseDataMap, ok := responseData["data"].(map[string]interface{}); ok {
				if errorMsg, ok := responseDataMap["error_msg"].(string); ok {
					slog.Warn("relay: Received error message from relay", "account_id", accountID, "error_msg", errorMsg)
					return responseData, common.ErrorBadRequest("relay: operation failed")
				}
			}

			slog.Info("relay: Successfully fetched data from relay", "account_id", accountID, "status", resp.StatusCode)
			return responseData, nil
		default:
			slog.Error("relay: Unexpected error while fetching data from relay", "account_id", accountID, "status", resp.StatusCode)
			return nil, errors.New("relay: unexpected error")
		}
	}

	slog.Error("relay: Max retries reached, aborting", "account_id", accountID)
	return data, errors.New("agent not found/connected after max retries")
}

func PodActionExecutor(accountId string, podName string, namespace string, action string, params map[string]any) (map[string]any, error) {
	actionParams := map[string]any{
		"name":      podName,
		"namespace": namespace,
	}
	// append params to actionParams
	maps.Copy(actionParams, params)

	relayRequest := RelayExecuteRequest{
		Body: ActionExecuteBody{
			AccountID:    accountId,
			ActionName:   action,
			ActionParams: actionParams,
		},
	}
	evidence, err := Execute(relayRequest)

	if err != nil {
		return map[string]any{}, err
	}
	return evidence, nil
}

func WorkloadMetricsExecutor(accountId string, workloadName string, namespace string, resourceType string, startTime time.Time, endTime time.Time) (map[string]any, error) {
	var promql_query string
	switch resourceType {
	case "cpu":
		promql_query = fmt.Sprintf(`sum(rate(container_cpu_usage_seconds_total{ __CLUSTER__ pod=~"%s.*", namespace="%s"}[5m])) by (pod,namespace)`, workloadName, namespace)
	case "memory":
		promql_query = fmt.Sprintf(`sum(container_memory_usage_bytes{ __CLUSTER__ pod=~"%s.*", namespace="%s"}) by (pod, namespace)`, workloadName, namespace)
	case "network":
		promql_query = fmt.Sprintf(`sum(rate(container_network_receive_bytes_total{ __CLUSTER__ pod=~"%s.*", namespace="%s"}[5m])) + sum(rate(container_network_transmit_bytes_total{ __CLUSTER__ pod=~"%s.*", namespace="%s"}[5m]))`, workloadName, namespace, workloadName, namespace)
	case "latency":
		promql_query = fmt.Sprintf(`histogram_quantile(0.99, sum(rate(container_http_requests_duration_seconds_total_bucket{ __CLUSTER__ actual_destination_workload_name=~"%s.*", actual_destination_workload_namespace="%s"}[5m])) by (le))`, workloadName, namespace)
	case "error_rate":
		promql_query = fmt.Sprintf(`sum(rate(container_http_requests_duration_seconds_total_count{ __CLUSTER__ actual_destination_workload_name=~"%s.*", actual_destination_workload_namespace="%s"}[5m]))`, workloadName, namespace)
	case "replicas":
		promql_query = fmt.Sprintf(`sum(kube_deployment_status_replicas{ __CLUSTER__ deployment=~"%s.*", namespace="%s"})`, workloadName, namespace)
	case "cpu_throttling":
		promql_query = fmt.Sprintf(`sum(rate(container_resources_cpu_throttled_seconds_total{ __CLUSTER__ container_id=~".*%s/%s.*"}[5m])) by (container_id)`, namespace, workloadName)
	default:
		return map[string]any{}, errors.New("relay: invalid resource type")
	}
	relayRequest := RelayExecuteRequest{
		Body: ActionExecuteBody{
			AccountID:  accountId,
			ActionName: "prometheus_enricher",
			ActionParams: map[string]any{
				"promql_query": promql_query,
				"duration": map[string]any{
					"starts_at": startTime.UTC().Format("2006-01-02 15:04:05 UTC"),
					"ends_at":   endTime.UTC().Format("2006-01-02 15:04:05 UTC"),
				},
			},
		},
	}

	evidence, err := Execute(relayRequest)

	if err != nil {
		return map[string]any{}, err
	}
	return evidence, nil
}

func CommandExecutor(accountId string, command string, secretName string, envFromSecret map[string]string) (map[string]any, error) {

	if accountId == "" {
		return map[string]any{}, errors.New("account_id is required")
	}

	if command == "" {
		return map[string]any{}, errors.New("command is required")
	}

	actionName := "pod_script_run_enricher"
	actionParams := map[string]any{
		"image":    config.Config.LlmServerShellImage,
		"command":  command,
		"pod_name": "nb-services-" + uuid.NewString(),
	}

	if secretName != "" {
		if strings.Contains(secretName, "/") {
			namespaceAndSecret := strings.Split(secretName, "/")
			actionParams["namespace"] = namespaceAndSecret[0]
			actionParams["secret"] = namespaceAndSecret[1]
		} else {
			actionParams["secret"] = secretName
		}
	}

	actionParams["env_from_secret_keys"] = envFromSecret

	relayRequest := RelayExecuteRequest{
		Body: ActionExecuteBody{
			AccountID:    accountId,
			ActionName:   actionName,
			ActionParams: actionParams,
		},
		TimeoutSeconds: config.Config.ServicesServerRelayCommandExecutionTimeoutSeconds,
	}
	evidence, _, err := ExecuteAndExtractResponse(relayRequest)

	if err != nil {
		return map[string]any{}, err
	}

	if evidenceData, ok := evidence["data"].(string); ok {
		evidenceMap := map[string]any{}
		err = common.UnmarshalJson([]byte(evidenceData), &evidenceMap)
		if err != nil {
			return map[string]any{}, err
		}
		return evidenceMap, nil
	}

	return map[string]any{}, errors.New("unable to execute command")
}

func FormatEvidenceResponseFromAgent(title string, data map[string]any) (map[string]any, error) {
	// Check if "data" exists in response
	findingsData, ok := data["data"].(map[string]any)
	if !ok {
		return map[string]any{}, fmt.Errorf("event: failed to fetch metrics from server: response data is empty")
	}

	// Check if "findings" exists and has elements
	findings, ok := findingsData["findings"].([]any)
	if !ok || len(findings) == 0 {
		return findingsData, nil
	}

	// Get first finding
	firstFinding, ok := findings[0].(map[string]any)
	if !ok {
		return map[string]any{}, fmt.Errorf("event: failed to fetch metrics from server: invalid findings structure")
	}

	// Check if "evidence" exists and has elements
	evidence, ok := firstFinding["evidence"].([]any)
	if !ok || len(evidence) == 0 {
		return map[string]any{}, fmt.Errorf("event: failed to fetch metrics from server: response data evidence is empty")
	}

	// Get first evidence
	firstEvidence, ok := evidence[0].(map[string]any)
	if !ok {
		return map[string]any{}, fmt.Errorf("event: failed to fetch metrics from server: invalid evidence structure")
	}

	// Check if "data" exists in firstEvidence
	rawData, ok := firstEvidence["data"].(string)
	if !ok {
		return map[string]any{}, fmt.Errorf("event: failed to fetch metrics from server: response data evidence is empty")
	}

	// Parse JSON data
	var result []map[string]any
	if err := common.UnmarshalJson([]byte(rawData), &result); err != nil {
		return map[string]any{}, fmt.Errorf("event: failed to parse metrics data: %v", err)
	}

	if len(result) == 0 {
		return map[string]any{}, fmt.Errorf("event: failed to fetch metrics from server: parsed data is empty")
	}
	finalResult := result[0]
	finalResult["insight"] = make([]map[string]any, 0)

	// check if result has additional_info and ensure it's the correct type
	additionalInfo, ok := finalResult["additional_info"].(map[string]any)
	if !ok {
		additionalInfo = map[string]any{}
		finalResult["additional_info"] = additionalInfo
	}
	additionalInfo["title"] = title

	return finalResult, nil
}

// ProxyDatasourceConfig mirrors the relay server's DatasourceConfig model.
type ProxyDatasourceConfig struct {
	ID               string            `json:"id"`
	Type             string            `json:"type"`
	ProxyType        string            `json:"proxy_type"`
	Name             string            `json:"name,omitempty"`
	Config           map[string]any    `json:"config"`
	Credentials      map[string]string `json:"credentials,omitempty"`
	CredentialSource string            `json:"credential_source"`
	CredentialRef    string            `json:"credential_ref,omitempty"`
}

// PushProxyConfig sends the full datasource config list for an account to the relay server,
// which forwards it to the connected proxy agent over WebSocket.
// If the agent is offline, the relay returns 202 and the config syncs on reconnect.
func PushProxyConfig(accountID string, datasources []ProxyDatasourceConfig) {
	go pushProxyConfigAsync(accountID, datasources)
}

// TestProxyDatasourceConfig sends a single datasource config to the relay for
// the proxy agent to validate connectivity. Returns nil on success, error on failure.
func TestProxyDatasourceConfig(accountID string, datasource ProxyDatasourceConfig) error {
	payload := map[string]any{
		"account_id": accountID,
		"datasource": datasource,
	}

	resp, err := common.HttpPost(
		fmt.Sprintf("%s/proxy/config/test", config.Config.RelayServerEndpoint),
		common.HttpWithClient(relayHttpClient),
		common.HttpWithHeaders(map[string]string{
			"Content-Type": "application/json",
			"Accept":       "application/json",
			"X-SECRET-KEY": config.Config.RelayServerSecretKey,
		}),
		common.HttpWithJsonBody(payload),
		common.HttpWithTimeout(25*time.Second),
	)
	if err != nil {
		return fmt.Errorf("failed to reach relay server for config test: %w", err)
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			slog.Warn("relay: error closing response body", "error", closeErr)
		}
	}()

	body, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return fmt.Errorf("failed to read relay test response: %w", readErr)
	}

	if resp.StatusCode == http.StatusBadRequest {
		var errResp struct {
			Errors []struct {
				Message string `json:"message"`
			} `json:"errors"`
		}
		if json.Unmarshal(body, &errResp) == nil && len(errResp.Errors) > 0 {
			return fmt.Errorf("%s", errResp.Errors[0].Message)
		}
		return fmt.Errorf("proxy agent not connected")
	}

	if resp.StatusCode == http.StatusGatewayTimeout {
		return fmt.Errorf("connection test timed out — agent may be unreachable or the target is not responding")
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected relay response: %d", resp.StatusCode)
	}

	var result struct {
		Success bool   `json:"success"`
		Error   string `json:"error"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return fmt.Errorf("invalid relay test response: %w", err)
	}

	if !result.Success {
		return fmt.Errorf("%s", result.Error)
	}
	return nil
}

func pushProxyConfigAsync(accountID string, datasources []ProxyDatasourceConfig) {
	payload := map[string]any{
		"account_id":  accountID,
		"datasources": datasources,
	}

	resp, err := common.HttpPost(
		fmt.Sprintf("%s/proxy/config/push", config.Config.RelayServerEndpoint),
		common.HttpWithClient(relayHttpClient),
		common.HttpWithHeaders(map[string]string{
			"Content-Type": "application/json",
			"Accept":       "application/json",
			"X-SECRET-KEY": config.Config.RelayServerSecretKey,
		}),
		common.HttpWithJsonBody(payload),
		common.HttpWithTimeout(30*time.Second),
	)
	if err != nil {
		slog.Error("relay: failed to push proxy config", "account_id", accountID, "error", err)
		return
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			slog.Warn("relay: error closing response body", "error", closeErr)
		}
	}()

	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusAccepted {
		slog.Info("relay: proxy config push successful", "account_id", accountID, "status", resp.StatusCode)
	} else {
		body, _ := io.ReadAll(resp.Body)
		slog.Error("relay: proxy config push failed", "account_id", accountID, "status", resp.StatusCode, "body", string(body))
	}
}

// ExecuteProxy sends a request to the proxy agent (forager) via the relay server.
// The proxy agent returns a flat {data: "<json-string>"} response, unlike the k8s
// agent's nested findings/evidence envelope.
func ExecuteProxy(accountID string, action string, datasourceID string, params map[string]any, timeoutSeconds int) (map[string]any, error) {
	proxyParams := make(map[string]any, len(params)+1)
	for k, v := range params {
		proxyParams[k] = v
	}
	proxyParams["datasource_id"] = datasourceID

	relayRequest := RelayExecuteRequest{
		Body: ActionExecuteBody{
			AccountID:    accountID,
			ActionName:   action,
			ActionParams: proxyParams,
			Origin:       "services-server",
		},
		NoSinks:        true,
		Cache:          false,
		AgentType:      "proxy",
		TimeoutSeconds: timeoutSeconds,
	}

	response, err := Execute(relayRequest)
	if err != nil {
		return nil, err
	}

	dataStr, ok := response["data"].(string)
	if !ok {
		return nil, errors.New("proxy response missing 'data' field")
	}

	var result map[string]any
	if err := json.Unmarshal([]byte(dataStr), &result); err != nil {
		return nil, fmt.Errorf("failed to parse proxy response data: %w", err)
	}

	if errMsg, ok := result["error"].(string); ok && errMsg != "" {
		return nil, fmt.Errorf("proxy agent error: %s", errMsg)
	}

	return result, nil
}
