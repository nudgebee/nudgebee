package database

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"nudgebee/tickets-server/common"
)

func ExecuteGql(operation string, variables map[string]any, accessToken string) (interface{}, error) {
	var headers map[string]string

	if accessToken != "" {
		headers = map[string]string{
			"Authorization": "Bearer " + accessToken,
			"Content-Type":  "application/json",
		}
	} else {
		headers = map[string]string{
			"x-hasura-admin-secret": common.Config.HasuraGraphqlAdminSecret,
			"Content-Type":          "application/json",
		}
	}

	requestBody, err := json.Marshal(map[string]interface{}{
		"query":     operation,
		"variables": variables,
	})
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest("POST", common.Config.HasuraGraphqlEndpoint, bytes.NewBuffer(requestBody))
	if err != nil {
		return nil, err
	}

	for key, value := range headers {
		req.Header.Set(key, value)
	}

	resp, err := common.HttpClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer func() {
		if cerr := resp.Body.Close(); cerr != nil {
			slog.Error("Failed to close response body:", "error", cerr)
		}
	}()

	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var data map[string]interface{}
	err = json.Unmarshal(responseBody, &data)
	if err != nil {
		return nil, err
	}

	return data, nil
}
