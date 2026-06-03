package cloud

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"nudgebee/runbook/common"
	"nudgebee/runbook/config"
	"nudgebee/runbook/services/security"
)

func ExecuteCli(ctx *security.RequestContext, cloudCliRequest CloudExecuteCliCommandRequest) (string, error) {
	headersMap := map[string]string{
		"Content-Type": "application/json",
		"Accept":       "application/json",
	}

	if config.Config.CloudCollectorServerUrl == "" {
		return "", errors.New("cloud: cloud collector server url not set")
	}
	if config.Config.CloudCollectorServerToken != "" {
		headersMap["X-ACTION-TOKEN"] = config.Config.CloudCollectorServerToken
	}
	headersMap["x-tenant-id"] = ctx.GetSecurityContext().GetTenantId()
	headersMap["x-user-id"] = ctx.GetSecurityContext().GetUserId()

	resp, err := common.HttpPost(fmt.Sprintf("%s/v1/cloud/execute_cli", config.Config.CloudCollectorServerUrl), common.HttpWithHeaders(headersMap), common.HttpWithJsonBody(map[string]any{
		"account_id": cloudCliRequest.AccountID,
		"command":    cloudCliRequest.Command,
	}))

	if err != nil {
		slog.Error("unable to access cloud server", "error", err)
		return "", fmt.Errorf("unable to access cloud server %v", err)
	}

	defer func() {
		if err := resp.Body.Close(); err != nil {
			slog.Error("cloud: failed to close response body", "error", err)
		}
	}()
	jsonBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	responseData := map[string]any{}
	err = common.UnmarshalJson(jsonBody, &responseData)
	if err != nil {
		return "", err
	}

	if resp.StatusCode != 200 {
		slog.Error("failed to fetch data from cloud", "status", resp.StatusCode, "data", string(jsonBody))
	}

	data := ""
	if responseData["data"] != nil {
		data = responseData["data"].(string)
	} else if responseData["errors"] != nil {
		errorsArr, ok := responseData["errors"].([]any)
		if ok && len(errorsArr) > 0 {
			errorMap, ok := errorsArr[0].(map[string]any)
			if ok {
				errorData := errorMap["message"].(string)
				return "", errors.New(errorData)
			}
		}
	}

	return data, nil
}
