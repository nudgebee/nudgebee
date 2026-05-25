package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"nudgebee/runbook/internal/model"
)

func (s *IntegrationTestSuite) TestExecuteTaskHasura_NotFound() {
	s.T().Log("Running TestExecuteTaskHasura_NotFound...")

	// Construct Hasura Action Request for unknown task
	payload := map[string]any{
		"action": map[string]string{
			"name": "workflow_trigger_task",
		},
		"input": map[string]any{
			"account_id": testAccountID,
			"task_type":  "unknown_task_type_123",
			"params":     map[string]any{},
		},
	}

	bodyBytes, err := json.Marshal(payload)
	s.Require().NoError(err)

	req, err := http.NewRequest(http.MethodPost, fmt.Sprintf("%s/hasura", apiBaseURL), bytes.NewBuffer(bodyBytes))
	s.Require().NoError(err)
	s.addRequestHeaders(req)

	resp, err := http.DefaultClient.Do(req)
	s.Require().NoError(err)
	defer func() {
		if err := resp.Body.Close(); err != nil {
			s.T().Logf("failed to close response body: %v", err)
		}
	}()

	s.Assert().Equal(http.StatusNotFound, resp.StatusCode, "Expected 404 Not Found for non-existent task type via Hasura action")
}

func (s *IntegrationTestSuite) TestExecuteTaskHasura_WithConfigAndSecrets() {
	s.T().Log("Running TestExecuteTaskHasura_WithConfigAndSecrets...")

	ctx := context.Background()

	// Create a config
	configKey := "test_api_url"
	configValue := "https://api.example.com"
	testAcc := testAccountID
	_, err := s.configService.SaveConfig(ctx, model.Config{
		Key:       configKey,
		Value:     configValue,
		Type:      model.ConfigTypeConfig,
		TenantID:  testTenantID,
		AccountID: &testAcc,
		CreatedBy: testUserID,
		UpdatedBy: testUserID,
	})
	s.Require().NoError(err, "Failed to create test config")

	// Create a secret
	secretKey := "test_api_token"
	secretValue := "secret-token-123"
	_, err = s.configService.SaveConfig(ctx, model.Config{
		Key:       secretKey,
		Value:     secretValue,
		Type:      model.ConfigTypeSecret,
		TenantID:  testTenantID,
		AccountID: &testAcc,
		CreatedBy: testUserID,
		UpdatedBy: testUserID,
	})
	s.Require().NoError(err, "Failed to create test secret")

	// Clean up configs after test
	defer func() {
		_ = s.configService.DeleteConfig(ctx, testTenantID, &testAcc, configKey)
		_ = s.configService.DeleteConfig(ctx, testTenantID, &testAcc, secretKey)
	}()

	// Construct Hasura Action Request with template syntax in params
	payload := map[string]any{
		"action": map[string]string{
			"name": "workflow_trigger_task",
		},
		"input": map[string]any{
			"account_id": testAccountID,
			"task_type":  "core.print",
			"params": map[string]any{
				"message": "URL: {{ Configs.test_api_url }}, Token: {{ Secrets.test_api_token }}",
			},
		},
	}

	bodyBytes, err := json.Marshal(payload)
	s.Require().NoError(err)

	req, err := http.NewRequest(http.MethodPost, fmt.Sprintf("%s/hasura", apiBaseURL), bytes.NewBuffer(bodyBytes))
	s.Require().NoError(err)
	s.addRequestHeaders(req)

	resp, err := http.DefaultClient.Do(req)
	s.Require().NoError(err)
	defer func() {
		if err := resp.Body.Close(); err != nil {
			s.T().Logf("failed to close response body: %v", err)
		}
	}()

	s.Assert().Equal(http.StatusOK, resp.StatusCode, "Expected 200 OK for task execution with config/secret templates")

	// Verify the response contains the rendered values
	body, err := io.ReadAll(resp.Body)
	s.Require().NoError(err, "Failed to read response body")

	var response map[string]any
	err = json.Unmarshal(body, &response)
	s.Require().NoError(err, "Failed to decode response")

	result, ok := response["result"].(map[string]any)
	s.Require().True(ok, "Expected result to be a map")

	data, ok := result["data"].(string)
	s.Require().True(ok, "Expected result.data to be a string")

	// Verify that the config value was rendered
	s.Assert().Contains(data, configValue, "Expected rendered output to contain config value")
	// Verify that the secret value was rendered
	s.Assert().Contains(data, secretValue, "Expected rendered output to contain secret value")
}
