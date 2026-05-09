package cloud

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"nudgebee/llm/common"
	"nudgebee/llm/config"
)

func Execute(cloudCliRequest CloudExecuteCliCommandRequest) (map[string]any, error) {
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
	headersMap["X-Hasura-User-Tenant-Id"] = cloudCliRequest.TenantID
	headersMap["X-Hasura-User-Id"] = cloudCliRequest.UserID

	resp, err := common.HttpPost(fmt.Sprintf("%s/v1/cloud/execute_cli", config.Config.CloudCollectorServerUrl), common.HttpWithHeaders(headersMap), common.HttpWithJsonBody(map[string]any{
		"account_id": cloudCliRequest.AccountID,
		"command":    cloudCliRequest.Command,
	}))

	if err != nil {
		slog.Error("unable to access cloud server", "error", err)
		return data, fmt.Errorf("unable to access cloud server %v", err)
	}

	defer func() {
		if err := resp.Body.Close(); err != nil {
			slog.Error("cloud: failed to close response body", "error", err)
		}
	}()
	jsonBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return data, err
	}

	responseData := map[string]any{}
	err = common.UnmarshalJson(jsonBody, &responseData)
	if err != nil {
		return data, err
	}

	if resp.StatusCode != 200 {
		slog.Error("failed to fetch data from cloud", "status", resp.StatusCode, "data", string(jsonBody))
	}

	return responseData, nil
}
