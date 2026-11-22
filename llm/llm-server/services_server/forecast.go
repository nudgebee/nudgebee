package services_server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
)

// ErrForecastForbidden is returned when api-server answers the forecast RPC with
// 403: the caller has no access to the account's metrics. Retrying does not help.
var ErrForecastForbidden = errors.New("forecast: forbidden")

// forecastRequestTimeout bounds one forecast round-trip. api-server gives the
// action 90s; this leaves room for the response on top of that.
// Package-level for test override; do not mutate at runtime.
var forecastRequestTimeout = 100 * time.Second

// ForecastRequest is the request of api-server's forecast_generate action
// (api-server/services/forecast). Metric names one of its forecast presets.
type ForecastRequest struct {
	AccountId         string   `json:"account_id"`
	WorkloadNamespace string   `json:"workload_namespace"`
	WorkloadName      string   `json:"workload_name"`
	Kind              string   `json:"kind,omitempty"`
	Metric            string   `json:"metric"`
	Container         string   `json:"container,omitempty"`
	Threshold         *float64 `json:"threshold,omitempty"`
	EndTime           int64    `json:"end_time,omitempty"`
}

// GenerateForecast runs an on-demand metric forecast for one workload and
// returns the forecast Result JSON unchanged (the "data" of the response). It
// forwards EffectiveUserIdForRPC, so a system-user run (event
// auto-investigation) takes api-server's tenant-admin branch.
func GenerateForecast(ctx security.RequestContext, request ForecastRequest) (json.RawMessage, error) {
	payload := map[string]any{
		"action": map[string]any{"name": "forecast_generate"},
		"input":  map[string]any{"request": request},
	}

	tenant := ctx.GetSecurityContext().GetTenantId()
	if tenant == "" {
		t, err := security.GetTenantIdFromAccountId(request.AccountId)
		if err != nil {
			return nil, fmt.Errorf("services: forecast, unable to resolve tenant: %w", err)
		}
		tenant = t
	}
	if tenant == "" {
		return nil, errors.New("services: forecast, tenant id is empty")
	}

	resp, err := common.HttpPost(fmt.Sprintf("%s/rpc/slo", config.Config.ServiceEndpoint), common.HttpWithHeaders(map[string]string{
		"Content-Type":   contentTypeJson,
		"Accept":         contentTypeJson,
		"X-ACTION-TOKEN": config.Config.ServiceApiServerToken,
		"x-tenant-id":    tenant,
		"x-user-id":      ctx.GetSecurityContext().EffectiveUserIdForRPC(),
	}), common.HttpWithJsonBody(payload), common.HttpWithContext(ctx.GetContext()), common.HttpWithTimeout(forecastRequestTimeout))
	if err != nil {
		return nil, fmt.Errorf("services: forecast, unable to process request: %w", err)
	}

	jsonBody, err := readAndCloseServicesResponseBody(resp, "services_server")
	if err != nil {
		return nil, fmt.Errorf("services: forecast, unable to read response: %w", err)
	}

	if resp.StatusCode == http.StatusForbidden {
		var errResp struct {
			Message string `json:"message"`
		}
		_ = common.UnmarshalJson(jsonBody, &errResp)
		return nil, fmt.Errorf("%w: %s", ErrForecastForbidden, errResp.Message)
	}
	if err := servicesHTTPStatusError("forecast", resp.StatusCode, jsonBody); err != nil {
		return nil, err
	}

	var body struct {
		Data json.RawMessage `json:"data"`
	}
	if err := common.UnmarshalJson(jsonBody, &body); err != nil {
		return nil, fmt.Errorf("services: forecast, unable to parse response: %w", err)
	}
	if len(body.Data) == 0 || string(body.Data) == "null" {
		return nil, errors.New("services: forecast, response has no data")
	}
	return body.Data, nil
}
