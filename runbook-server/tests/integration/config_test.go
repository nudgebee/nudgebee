package integration_test

import (
	"fmt"
	"io"
	"net/http" // Required for http.Status... constants
	"nudgebee/runbook/internal/model"
)

// TestConfigsAndSecrets is an integration test for the configs and secrets feature.
func (s *IntegrationTestSuite) TestConfigsAndSecrets() {
	// 1. Create a Config
	configKey := "test_api_url"
	configValue := "https://api.example.com"
	s.createConfig(configKey, configValue, "config", nil, nil)

	// 2. Create a Secret
	secretKey := "test_api_key"
	secretValue := "super_secret_token_123"
	s.createConfig(secretKey, secretValue, "secret", nil, nil)

	// 3. Verify Config Retrieval (Secrets should be masked)
	configResp := s.getConfig(configKey)
	s.Assert().Equal(configValue, configResp.Value)
	s.Assert().Nil(configResp.Metadata) // No metadata expected

	secretResp := s.getConfig(secretKey)
	s.Assert().Equal("*****", secretResp.Value) // Should be masked
	s.Assert().Nil(secretResp.Metadata)         // No metadata expected

	// 4. Create a Workflow that uses these configs/secrets
	wfName := "config-test-workflow"
	wfDef := model.WorkflowDefinition{
		Version: "v1",
		Triggers: []model.Trigger{
			{
				Type: model.WorkflowTriggerManual,
			},
		},
		Tasks: []model.Task{
			{
				ID:   "echo_config",
				Type: "core.print",
				Params: map[string]any{
					"message": "{{ Configs.test_api_url }}",
				},
			},
			{
				ID:   "echo_secret",
				Type: "core.print",
				Params: map[string]any{
					"message": "{{ Secrets.test_api_key }}",
				},
				DependsOn: []string{"echo_config"},
			},
		},
	}

	wfID, _, err := s.createAndActivateWorkflow(model.Workflow{Name: wfName, Definition: wfDef})
	s.Require().NoError(err)

	// 5. Execute Workflow
	runID, err := s.executeWorkflow(wfID.ID, map[string]any{})
	s.Require().NoError(err)

	// 6. Wait for completion
	execDetails := s.waitForWorkflowCompletion(wfID.ID, runID)
	s.Require().NotNil(execDetails, "Workflow execution details should not be nil")

	// If workflow failed, print its error for debugging
	if execDetails.Status == model.WorkflowExecutionStatusFailed {
		s.T().Logf("Workflow failed: %s", execDetails.Error)
		// Optionally, dump task errors
		for _, task := range execDetails.Tasks {
			if task.Error != "" {
				s.T().Logf("Task %s failed: %s", task.ID, task.Error)
			}
		}
		s.FailNow("Workflow execution failed")
	}
	s.Require().Equal(model.WorkflowExecutionStatusCompleted, execDetails.Status, "Workflow should complete successfully")

	// 7. Verify Execution Results
	var configOutput, secretOutput string
	for _, task := range execDetails.Tasks {
		if task.ID == "echo_config" {
			// PrintTask returns map[string]string{"data": message}
			if outMap, ok := task.Output.(map[string]any); ok {
				if data, dataOk := outMap["data"]; dataOk {
					configOutput = fmt.Sprintf("%v", data)
				}
			}
		}
		if task.ID == "echo_secret" {
			// PrintTask returns map[string]string{"data": message}
			if outMap, ok := task.Output.(map[string]any); ok {
				if data, dataOk := outMap["data"]; dataOk {
					secretOutput = fmt.Sprintf("%v", data)
				}
			}
		}
	}

	s.Assert().Contains(configOutput, configValue, "Config value should be present in task output")
	s.Assert().Contains(secretOutput, secretValue, "Secret value should be present in task output (it's decrypted for the task)")

	// --- New Test Cases ---

	s.Run("UpdateConfigAndSecret", func() {
		s.T().Cleanup(func() {
			s.deleteConfig("updatable_config")
			s.deleteConfig("updatable_secret")
		})

		// Test updating a config
		updateConfigKey := "updatable_config"
		s.createConfig(updateConfigKey, "initial_value", "config", nil, nil)
		updatedLabels := map[string]string{"env": "staging"}
		updatedMetadata := map[string]any{"owner": "team-b"}
		s.createConfig(updateConfigKey, "updated_value", "config", updatedLabels, updatedMetadata) // Same key, so it's an update

		updatedConfig := s.getConfig(updateConfigKey)
		s.Assert().Equal("updated_value", updatedConfig.Value)
		s.Assert().Equal(updatedLabels, updatedConfig.Labels)
		s.Assert().Equal(updatedMetadata["owner"], updatedConfig.Metadata["owner"])

		// Test updating a secret
		updateSecretKey := "updatable_secret"
		s.createConfig(updateSecretKey, "initial_secret", "secret", nil, nil)
		updatedSecretLabels := map[string]string{"purpose": "db-access"}
		updatedSecretMetadata := map[string]any{"expires": "2026-01-01"}
		s.createConfig(updateSecretKey, "updated_secret_value", "secret", updatedSecretLabels, updatedSecretMetadata) // Update secret

		updatedSecret := s.getConfig(updateSecretKey)
		s.Assert().Equal("*****", updatedSecret.Value) // Value should still be masked for API
		s.Assert().Equal(updatedSecretLabels, updatedSecret.Labels)
		s.Assert().Equal(updatedSecretMetadata["expires"], updatedSecret.Metadata["expires"])
	})

	s.Run("LabelFilteringEdgeCases", func() {
		s.T().Cleanup(func() {
			s.deleteConfig("label_test_1")
			s.deleteConfig("label_test_2")
		})

		s.createConfig("label_test_1", "val1", "config", map[string]string{"env": "dev"}, nil)
		s.createConfig("label_test_2", "val2", "config", map[string]string{"env": "prod", "owner": "team-x"}, nil)

		// List all (no labels filter)
		allConfigs := s.listConfigs(nil)
		s.Assert().GreaterOrEqual(len(allConfigs), 2, "Should list at least 2 configs")

		// List with empty labels filter
		emptyLabelConfigs := s.listConfigs(map[string]string{})
		s.Assert().GreaterOrEqual(len(emptyLabelConfigs), 2, "Should list at least 2 configs with empty labels filter")

		// Filter by existing label
		devConfigs := s.listConfigs(map[string]string{"env": "dev"})
		s.Assert().Len(devConfigs, 1)
		s.Assert().Equal("label_test_1", devConfigs[0].Key)

		// Filter by multiple labels (AND condition)
		prodTeamXConfigs := s.listConfigs(map[string]string{"env": "prod", "owner": "team-x"})
		s.Assert().Len(prodTeamXConfigs, 1)
		s.Assert().Equal("label_test_2", prodTeamXConfigs[0].Key)

		// Filter by non-existent label
		nonExistentConfigs := s.listConfigs(map[string]string{"nonexistent": "label"})
		s.Assert().Len(nonExistentConfigs, 0)
	})

	s.Run("InvalidConfigCreation", func() {
		// Helper to make an API call that's expected to fail
		expectFailure := func(key, value, typeStr string, labels map[string]string, metadata map[string]any, expectedStatusCode int) {
			payload := map[string]any{
				"key":      key,
				"value":    value,
				"type":     typeStr,
				"labels":   labels,
				"metadata": metadata,
			}
			resp := s.request("POST", "/configs", payload)
			bodyBytes, err := io.ReadAll(resp.Body)
			s.Require().NoError(err)
			s.Require().NoError(resp.Body.Close()) // Check error return value

			if resp.StatusCode != expectedStatusCode {
				s.T().Logf("expectFailure mismatch for key '%s'. Expected %d, got %d. Response Body: %s", key, expectedStatusCode, resp.StatusCode, string(bodyBytes))
			}
			s.Require().Equal(expectedStatusCode, resp.StatusCode)
		}

		// Missing key
		expectFailure("", "some_value", "config", nil, nil, http.StatusBadRequest)
		// Missing value
		expectFailure("missing_value_config", "", "config", nil, nil, http.StatusBadRequest)
		// Missing type
		expectFailure("missing_type_config", "some_value", "", nil, nil, http.StatusBadRequest)
		// Invalid type
		expectFailure("invalid_type_config", "some_value", "unknown", nil, nil, http.StatusBadRequest)

		// Value exceeding MaxConfigSize (100KB)
		longValue := make([]byte, 100*1024+1)                                                                    // 100KB + 1 byte
		expectFailure("too_large_config", string(longValue), "config", nil, nil, http.StatusInternalServerError) // Service layer error for now
	})

	// --- End New Test Cases ---

	// 8. Test Labels and Filtering (Original, moved to subtest above)
	// 9. Test Metadata (Original, kept for context, but cleanup moved below)

	// 10. Cleanup
	s.T().Cleanup(func() {
		s.deleteConfig(configKey)
		s.deleteConfig(secretKey)
		s.deleteConfig("labeled_config")
		s.deleteConfig("metadata_config")
		s.deleteWorkflow(wfID.ID, false) // delete created workflow
	})
}
