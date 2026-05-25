package services

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"nudgebee/collector/cloud/common"
	"nudgebee/collector/cloud/config"
)

const contentTypeJson = "application/json"

func InvestigateEvent(tenantId string, events []map[string]any) (map[string]any, error) {
	funcName := "cloud-collector.services.InvestigateEvent"
	response := make(map[string]any)

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
		return response, fmt.Errorf("unable to process events: %s, %w", funcName, err)
	}
	if resp == nil {
		return response, fmt.Errorf("nil HTTP response from investigation service: %s", funcName)
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
		return response, err
	}

	if resp.StatusCode != 200 {
		return response, fmt.Errorf("unable to process events: %v, status code %d", string(jsonBody), resp.StatusCode)
	}

	var responseData map[string]any
	err = json.Unmarshal(jsonBody, &responseData)
	if err != nil {
		return response, err
	}
	return responseData, err
}
