package ml

import (
	"fmt"
	"io"
	"log/slog"
	"nudgebee/services/common"
	"nudgebee/services/config"
	"nudgebee/services/security"
	"time"
)

func GetRecommendation(context *security.RequestContext, query RecommendationRequest) (RecommendationResponse, error) {
	err := common.ValidateStruct(query)
	if err != nil {
		return RecommendationResponse{}, err
	}

	if !context.GetSecurityContext().HasAccountAccess(query.Account, security.SecurityAccessTypeRead) {
		return RecommendationResponse{}, common.ErrorUnauthorized("unauthorized")
	}

	resp, err := common.HttpPost(fmt.Sprintf("%s/generate", config.Config.MlServiceUrl), common.HttpWithHeaders(map[string]string{
		"Content-Type": "application/json",
		"Accept":       "application/json",
	}), common.HttpWithJsonBody(query))

	if err != nil {
		return RecommendationResponse{}, err
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			// Log the error, but don't return it as the main operation might have succeeded
			fmt.Printf("Error closing response body: %v\n", err)
		}
	}()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return RecommendationResponse{}, err
	}

	if resp.StatusCode != 200 {
		errorResponse := map[string]any{}
		err := common.UnmarshalJson(body, &errorResponse)
		if err != nil {
			return RecommendationResponse{}, fmt.Errorf("ml: error executing request: %s", resp.Status)
		}
		if errorResponse["message"] != nil {
			return RecommendationResponse{}, fmt.Errorf("ml: error executing request: %s", errorResponse["message"])
		}
		return RecommendationResponse{}, fmt.Errorf("ml: error executing request: %s", resp.Status)
	}

	var response RecommendationResponse
	err = common.UnmarshalJson(body, &response)
	if err != nil {
		return RecommendationResponse{}, err
	}

	return response, nil
}

func GetNodeRecommendation(context *security.RequestContext, query NodeRecommendationRequest) (NodeRecommendationResponse, error) {
	err := common.ValidateStruct(query)
	if err != nil {
		return NodeRecommendationResponse{}, err
	}

	if !context.GetSecurityContext().HasAccountAccess(query.Account, security.SecurityAccessTypeRead) {
		return NodeRecommendationResponse{}, common.ErrorUnauthorized("unauthorized")
	}

	resp, err := common.HttpPost(fmt.Sprintf("%s/rightsizing/cluster", config.Config.MlServiceUrl), common.HttpWithHeaders(map[string]string{
		"Content-Type": "application/json",
		"Accept":       "application/json",
	}), common.HttpWithJsonBody(query))

	if err != nil {
		return NodeRecommendationResponse{}, err
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			// Log the error, but don't return it as the main operation might have succeeded
			fmt.Printf("Error closing response body: %v\n", err)
		}
	}()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return NodeRecommendationResponse{}, err
	}

	if resp.StatusCode != 200 {
		errorResponse := map[string]any{}
		err := common.UnmarshalJson(body, &errorResponse)
		if err != nil {
			return NodeRecommendationResponse{}, fmt.Errorf("ml: error executing node recommendation request: %s", resp.Status)
		}
		if errorResponse["message"] != nil {
			return NodeRecommendationResponse{}, fmt.Errorf("ml: error executing node recommendation request: %s", errorResponse["message"])
		}
		return NodeRecommendationResponse{}, fmt.Errorf("ml: error executing node recommendation request: %s", resp.Status)
	}

	var response NodeRecommendationResponse
	err = common.UnmarshalJson(body, &response)
	if err != nil {
		return NodeRecommendationResponse{}, err
	}

	return response, nil
}

func GetMetrics(query MetricsRequest, context *security.RequestContext) (MetricsResponse, error) {
	err := common.ValidateStruct(query)
	if err != nil {
		return MetricsResponse{}, err
	}

	if !context.GetSecurityContext().HasAccountAccess(query.Account, security.SecurityAccessTypeRead) {
		return MetricsResponse{}, common.ErrorUnauthorized("unauthorized")
	}

	resp, err := common.HttpPost(fmt.Sprintf("%s/metrics", config.Config.MlServiceUrl), common.HttpWithHeaders(map[string]string{
		"Content-Type": "application/json",
		"Accept":       "application/json",
	}), common.HttpWithJsonBody(query))

	if err != nil {
		return MetricsResponse{}, err
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			// Log the error, but don't return it as the main operation might have succeeded
			fmt.Printf("Error closing response body: %v\n", err)
		}
	}()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return MetricsResponse{}, err
	}

	if resp.StatusCode != 200 {
		errorResponse := map[string]any{}
		err := common.UnmarshalJson(body, &errorResponse)
		if err != nil {
			return MetricsResponse{}, fmt.Errorf("ml: error executing request: %s", resp.Status)
		}
		if errorResponse["message"] != nil {
			return MetricsResponse{}, fmt.Errorf("ml: error executing request: %s", errorResponse["message"])
		}
		return MetricsResponse{}, fmt.Errorf("ml: error executing request: %s", resp.Status)
	}

	var response MetricsResponse
	err = common.UnmarshalJson(body, &response)
	if err != nil {
		return MetricsResponse{}, err
	}

	return response, nil
}

func GetAnomaly(context *security.RequestContext, anomalyRequest AnomalyRequest) ([]AnomalyResponse, error) {
	err := common.ValidateStruct(anomalyRequest)
	if err != nil {
		return []AnomalyResponse{}, err
	}

	if !context.GetSecurityContext().HasAccountAccess(anomalyRequest.Account, security.SecurityAccessTypeRead) {
		return []AnomalyResponse{}, common.ErrorUnauthorized("unauthorized")
	}

	anomalyRequestMap := map[string]any{
		"account":    anomalyRequest.Account,
		"tenant":     anomalyRequest.Tenant,
		"type":       anomalyRequest.Type,
		"namespace":  anomalyRequest.Namespace,
		"deployment": anomalyRequest.Deployment,
	}

	if anomalyRequest.StartTime != nil {
		anomalyRequestMap["start_time"] = anomalyRequest.StartTime.Format(time.RFC3339)
	}
	if anomalyRequest.EndTime != nil {
		anomalyRequestMap["end_time"] = anomalyRequest.EndTime.Format(time.RFC3339)
	}
	if anomalyRequest.EvaluationPeriodMinutes != nil {
		anomalyRequestMap["evaluation_period"] = int64(*anomalyRequest.EvaluationPeriodMinutes)
	}

	resp, err := common.HttpPost(fmt.Sprintf("%s/anomaly", config.Config.MlServiceUrl), common.HttpWithHeaders(map[string]string{
		"Content-Type": "application/json",
		"Accept":       "application/json",
	}), common.HttpWithJsonBody(anomalyRequestMap))

	if err != nil {
		return []AnomalyResponse{}, err
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			// Log the error, but don't return it as the main operation might have succeeded
			fmt.Printf("Error closing response body: %v\n", err)
		}
	}()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return []AnomalyResponse{}, err
	}

	if resp.StatusCode != 200 {
		context.GetLogger().Warn("ml: unable to process anomaly", "request", slog.AnyValue(anomalyRequest), "response", resp.Status, "body", string(body))
		return []AnomalyResponse{}, nil
	}

	var response []AnomalyResponse
	err = common.UnmarshalJson(body, &response)
	if err != nil {
		return []AnomalyResponse{}, err
	}

	return response, nil
}

func DetectMetricAnomaly(context *security.RequestContext, request MetricAnomalyDetectRequest) ([]MetricAnomalyDetectResponse, error) {
	err := common.ValidateStruct(request)
	if err != nil {
		return []MetricAnomalyDetectResponse{}, err
	}

	if !context.GetSecurityContext().HasAccountAccess(request.Account, security.SecurityAccessTypeRead) {
		return []MetricAnomalyDetectResponse{}, common.ErrorUnauthorized("unauthorized")
	}

	resp, err := common.HttpPost(fmt.Sprintf("%s/anomaly/detect", config.Config.MlServiceUrl), common.HttpWithHeaders(map[string]string{
		"Content-Type": "application/json",
		"Accept":       "application/json",
	}), common.HttpWithJsonBody(request))

	if err != nil {
		return []MetricAnomalyDetectResponse{}, err
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			// Log the error, but don't return it as the main operation might have succeeded
			fmt.Printf("Error closing response body: %v\n", err)
		}
	}()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return []MetricAnomalyDetectResponse{}, err
	}

	if resp.StatusCode != 200 {
		context.GetLogger().Warn("ml: unable to process metric anomaly detection", "request", slog.AnyValue(request), "response", resp.Status, "body", string(body))
		return []MetricAnomalyDetectResponse{}, fmt.Errorf("ml: error executing metric anomaly detection request: %s", resp.Status)
	}

	var response []MetricAnomalyDetectResponse
	err = common.UnmarshalJson(body, &response)
	if err != nil {
		return []MetricAnomalyDetectResponse{}, err
	}

	return response, nil
}

// TriggerVerticalRightsizing sends a request to ml-k8s-server to queue vertical rightsizing processing
func TriggerVerticalRightsizing(context *security.RequestContext, request VerticalRightsizingRequest) (VerticalRightsizingResponse, error) {
	err := common.ValidateStruct(request)
	if err != nil {
		return VerticalRightsizingResponse{}, err
	}

	resp, err := common.HttpPost(fmt.Sprintf("%s/rightsizing/vertical", config.Config.MlServiceUrl), common.HttpWithHeaders(map[string]string{
		"Content-Type": "application/json",
		"Accept":       "application/json",
	}), common.HttpWithJsonBody(request))

	if err != nil {
		return VerticalRightsizingResponse{}, err
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			fmt.Printf("Error closing response body: %v\n", err)
		}
	}()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return VerticalRightsizingResponse{}, err
	}

	if resp.StatusCode != 200 {
		errorResponse := map[string]any{}
		err := common.UnmarshalJson(body, &errorResponse)
		if err != nil {
			return VerticalRightsizingResponse{}, fmt.Errorf("ml: error executing vertical rightsizing request: %s", resp.Status)
		}
		if errorResponse["message"] != nil {
			return VerticalRightsizingResponse{}, fmt.Errorf("ml: error executing vertical rightsizing request: %s", errorResponse["message"])
		}
		return VerticalRightsizingResponse{}, fmt.Errorf("ml: error executing vertical rightsizing request: %s", resp.Status)
	}

	var response VerticalRightsizingResponse
	err = common.UnmarshalJson(body, &response)
	if err != nil {
		return VerticalRightsizingResponse{}, err
	}

	return response, nil
}

// TriggerVolumeRightsizing sends a request to ml-k8s-server to queue volume rightsizing processing
func TriggerVolumeRightsizing(context *security.RequestContext, request VolumeRightsizingRequest) (VolumeRightsizingResponse, error) {
	err := common.ValidateStruct(request)
	if err != nil {
		return VolumeRightsizingResponse{}, err
	}

	resp, err := common.HttpPost(fmt.Sprintf("%s/rightsizing/volume", config.Config.MlServiceUrl), common.HttpWithHeaders(map[string]string{
		"Content-Type": "application/json",
		"Accept":       "application/json",
	}), common.HttpWithJsonBody(request))

	if err != nil {
		return VolumeRightsizingResponse{}, err
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			fmt.Printf("Error closing response body: %v\n", err)
		}
	}()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return VolumeRightsizingResponse{}, err
	}

	if resp.StatusCode != 200 {
		errorResponse := map[string]any{}
		err := common.UnmarshalJson(body, &errorResponse)
		if err != nil {
			return VolumeRightsizingResponse{}, fmt.Errorf("ml: error executing volume rightsizing request: %s", resp.Status)
		}
		if errorResponse["message"] != nil {
			return VolumeRightsizingResponse{}, fmt.Errorf("ml: error executing volume rightsizing request: %s", errorResponse["message"])
		}
		return VolumeRightsizingResponse{}, fmt.Errorf("ml: error executing volume rightsizing request: %s", resp.Status)
	}

	var response VolumeRightsizingResponse
	err = common.UnmarshalJson(body, &response)
	if err != nil {
		return VolumeRightsizingResponse{}, err
	}

	return response, nil
}
