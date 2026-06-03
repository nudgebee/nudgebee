//go:build e2e

package api

import (
	"fmt"
	"log/slog"
	"nudgebee/llm/agents"
	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFunctions_CreateLLMFunction(t *testing.T) {
	userId := os.Getenv("TEST_USER")
	accountId := os.Getenv("TEST_ACCOUNT")
	sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), userId, []string{accountId})

	// Skip test if required environment variables are not set
	if userId == "" || accountId == "" || os.Getenv("TEST_TENANT") == "" {
		t.Skip("Required test environment variables not set")
	}

	customFunction := core.LLMFunctionDto{
		Name:        "test_function_one",
		Description: "Custom function for testing purposes",
		Prompt:      "You are a helpful assistant. Answer the user's question: <user_question>. Use the context if provided: <context>",
		Variables:   []string{"user_question", "context"},
		VariableDefaults: map[string]any{
			"user_question": "What is Kubernetes?",
			"context":       "Kubernetes is a container orchestration platform",
		},
		Status:    "active",
		Version:   1,
		CreatedBy: userId,
		UpdatedBy: userId,
	}

	// Create the function
	createdFunction, err := core.CreateLLMFunction(sc, accountId, customFunction)
	assert.Nil(t, err)
	if err != nil {
		slog.Error("Error creating custom function", "error", err)
		return
	}

	// Verify the function was created successfully
	assert.NotEmpty(t, createdFunction.Id)
	assert.Equal(t, customFunction.Name, createdFunction.Name)
	assert.Equal(t, customFunction.Description, createdFunction.Description)
	assert.Equal(t, customFunction.Prompt, createdFunction.Prompt)
	assert.Equal(t, len(customFunction.Variables), len(createdFunction.Variables))
	assert.Equal(t, customFunction.Status, createdFunction.Status)
	assert.Equal(t, accountId, createdFunction.AccountId)
	assert.Equal(t, sc.GetSecurityContext().GetTenantId(), createdFunction.TenantId)
	assert.NotZero(t, createdFunction.CreatedAt)
	assert.NotZero(t, createdFunction.UpdatedAt)

	slog.Info("Function created successfully", "function_id", createdFunction.Id, "function_name", createdFunction.Name)
}

func TestFunctions_DeleteLLMFunction(t *testing.T) {
	userId := os.Getenv("TEST_USER")
	accountId := os.Getenv("TEST_ACCOUNT")
	sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), userId, []string{accountId})

	// Skip test if required environment variables are not set
	if userId == "" || accountId == "" || os.Getenv("TEST_TENANT") == "" {
		t.Skip("Required test environment variables not set")
	}

	// First create a function to delete
	customFunction := core.LLMFunctionDto{
		Name:        "test_function_delete",
		Description: "Custom function for delete testing purposes",
		Prompt:      "You are a helpful assistant for testing delete functionality.",
		Variables:   []string{"test_var"},
		VariableDefaults: map[string]any{
			"test_var": "test_value",
		},
		Status:    "active",
		Version:   1,
		CreatedBy: userId,
		UpdatedBy: userId,
	}

	// Create the function
	createdFunction, err := core.CreateLLMFunction(sc, accountId, customFunction)
	assert.Nil(t, err)
	if err != nil {
		slog.Error("Error creating function for delete test", "error", err)
		return
	}
	assert.NotEmpty(t, createdFunction.Id)
	slog.Info("Function created for delete test", "function_id", createdFunction.Id)

	// Now delete the function
	_, err = core.DeleteLLMFunction(sc, accountId, createdFunction.Id)
	assert.Nil(t, err)
	if err != nil {
		slog.Error("Error deleting function", "error", err)
		return
	}

	slog.Info("Function deleted successfully", "function_id", createdFunction.Id, "function_name", createdFunction.Name)
}

func TestFunctions_UpdateLLMFunction(t *testing.T) {
	userId := os.Getenv("TEST_USER")
	accountId := os.Getenv("TEST_ACCOUNT")
	sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), userId, []string{accountId})

	// Skip test if required environment variables are not set
	if userId == "" || accountId == "" || os.Getenv("TEST_TENANT") == "" {
		t.Skip("Required test environment variables not set")
	}

	// First create a function to update
	originalFunction := core.LLMFunctionDto{
		Name:        "test_function_update_original",
		Description: "Original function for update testing",
		Prompt:      "You are a helpful assistant. Original prompt.",
		Variables:   []string{"original_var"},
		VariableDefaults: map[string]any{
			"original_var": "original_value",
		},
		Status:    "draft",
		CreatedBy: userId,
		UpdatedBy: userId,
	}

	// Create the function
	createdFunction, err := core.CreateLLMFunction(sc, accountId, originalFunction)
	assert.Nil(t, err)
	if err != nil {
		slog.Error("Error creating function for update test", "error", err)
		return
	}
	assert.NotEmpty(t, createdFunction.Id)
	assert.Equal(t, 1, createdFunction.Version)
	slog.Info("Function created for update test", "function_id", createdFunction.Id)

	// Now update the function
	updatedFunction := core.LLMFunctionDto{
		Name:        "-test_function_update_modified",
		Description: "Modified function after update testing",
		Prompt:      "You are a helpful assistant. Updated prompt with new instructions.",
		Variables:   []string{"updated_var", "new_var"},
		VariableDefaults: map[string]any{
			"updated_var": "updated_value",
			"new_var":     "new_value",
		},
		Status:    "active",
		UpdatedBy: userId,
	}

	// Update the function
	resultFunction, err := core.UpdateLLMFunction(sc, accountId, createdFunction.Id, updatedFunction)
	assert.Nil(t, err)
	if err != nil {
		slog.Error("Error updating function", "error", err)
		return
	}

	// Verify the function was updated successfully
	assert.Equal(t, createdFunction.Id, resultFunction.Id)
	assert.Equal(t, updatedFunction.Name, resultFunction.Name)
	assert.Equal(t, updatedFunction.Description, resultFunction.Description)
	assert.Equal(t, updatedFunction.Prompt, resultFunction.Prompt)
	assert.Equal(t, len(updatedFunction.Variables), len(resultFunction.Variables))
	assert.Equal(t, updatedFunction.Status, resultFunction.Status)
	assert.Equal(t, accountId, resultFunction.AccountId)
	assert.Equal(t, sc.GetSecurityContext().GetTenantId(), resultFunction.TenantId)
	assert.Equal(t, userId, resultFunction.UpdatedBy)
	assert.NotZero(t, resultFunction.UpdatedAt)

	// Verify variables and variable defaults
	assert.Contains(t, resultFunction.Variables, "updated_var")
	assert.Contains(t, resultFunction.Variables, "new_var")
	assert.Equal(t, "updated_value", resultFunction.VariableDefaults["updated_var"])
	assert.Equal(t, "new_value", resultFunction.VariableDefaults["new_var"])

	slog.Info("Function updated successfully",
		"function_id", resultFunction.Id,
		"function_name", resultFunction.Name,
		"old_version", createdFunction.Version,
		"new_version", resultFunction.Version)

	// Clean up - delete the test function
	_, err = core.DeleteLLMFunction(sc, accountId, createdFunction.Id)
	assert.Nil(t, err)
	if err != nil {
		slog.Error("Error cleaning up test function", "error", err)
	}
}

func TestFunctions_ValidateFunctionName(t *testing.T) {
	userId := os.Getenv("TEST_USER")
	accountId := os.Getenv("TEST_ACCOUNT")
	sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), userId, []string{accountId})

	// Skip test if required environment variables are not set
	if userId == "" || accountId == "" || os.Getenv("TEST_TENANT") == "" {
		t.Skip("Required test environment variables not set")
	}

	// Test cases for valid function names
	validNames := []string{
		"get_user_data",
		"calculate-tax",
		"send_email",
		"validate-input-v2",
		"process123",
		"api-handler",
		"user_profile",
		"get-order-status",
		"handle_payment",
		"validate123",
		"test_function_abc",
		"my-awesome-function",
	}

	// Test cases for invalid function names
	invalidNames := []struct {
		name   string
		reason string
	}{
		{"Get_User", "contains uppercase"},
		{"_private", "starts with underscore"},
		{"function_", "ends with underscore"},
		{"my-function-", "ends with hyphen"},
		{"my function", "contains space"},
		{"123function", "starts with number"},
		{"function.name", "contains dot"},
		{"my@function", "contains special character"},
		{"", "empty name"},
		{"a", "too short (single character)"},
		{"UPPERCASE", "all uppercase"},
		{"-starts-with-hyphen", "starts with hyphen"},
		{"ends-with_", "ends with underscore"},
		{"has spaces", "contains spaces"},
		{"has.dots", "contains dots"},
		{"has$special", "contains dollar sign"},
	}

	baseFunction := core.LLMFunctionDto{
		Description: "Test function for name validation",
		Prompt:      "Test prompt for validation",
		Variables:   []string{"test_var"},
		VariableDefaults: map[string]any{
			"test_var": "test_value",
		},
		Status:    "draft",
		CreatedBy: userId,
		UpdatedBy: userId,
	}

	// Test valid function names
	for _, validName := range validNames {
		t.Run("Valid_"+validName, func(t *testing.T) {
			testFunction := baseFunction
			testFunction.Name = validName

			createdFunction, err := core.CreateLLMFunction(sc, accountId, testFunction)
			assert.Nil(t, err, "Expected valid name '%s' to be accepted", validName)

			if err == nil {
				assert.Equal(t, validName, createdFunction.Name)
				slog.Info("Valid function name test passed", "name", validName)

				// Clean up
				_, deleteErr := core.DeleteLLMFunction(sc, accountId, createdFunction.Id)
				assert.Nil(t, deleteErr, "Failed to clean up test function")
			}
		})
	}

	// Test invalid function names
	for _, invalidCase := range invalidNames {
		t.Run("Invalid_"+invalidCase.name+"_"+invalidCase.reason, func(t *testing.T) {
			testFunction := baseFunction
			testFunction.Name = invalidCase.name

			_, err := core.CreateLLMFunction(sc, accountId, testFunction)
			assert.NotNil(t, err, "Expected invalid name '%s' to be rejected (%s)", invalidCase.name, invalidCase.reason)

			if err != nil {
				assert.Contains(t, err.Error(), "function.name", "Error message should mention function.name")
				slog.Info("Invalid function name test passed", "name", invalidCase.name, "reason", invalidCase.reason, "error", err.Error())
			}
		})
	}
}

func TestFunctions_UpdateFunctionNameValidation(t *testing.T) {
	userId := os.Getenv("TEST_USER")
	accountId := os.Getenv("TEST_ACCOUNT")
	sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), userId, []string{accountId})

	// Skip test if required environment variables are not set
	if userId == "" || accountId == "" || os.Getenv("TEST_TENANT") == "" {
		t.Skip("Required test environment variables not set")
	}

	// Create a function with valid name first
	originalFunction := core.LLMFunctionDto{
		Name:        "test_update_validation",
		Description: "Original function for update name validation",
		Prompt:      "Test prompt for update validation",
		Variables:   []string{"test_var"},
		VariableDefaults: map[string]any{
			"test_var": "test_value",
		},
		Status:    "draft",
		CreatedBy: userId,
		UpdatedBy: userId,
	}

	createdFunction, err := core.CreateLLMFunction(sc, accountId, originalFunction)
	assert.Nil(t, err)
	if err != nil {
		t.Fatalf("Failed to create test function: %v", err)
	}

	// Test updating to valid name
	t.Run("UpdateToValidName", func(t *testing.T) {
		updateFunction := originalFunction
		updateFunction.Name = "updated-valid-name"
		updateFunction.UpdatedBy = userId

		updatedFunction, err := core.UpdateLLMFunction(sc, accountId, createdFunction.Id, updateFunction)
		assert.Nil(t, err, "Should allow updating to valid name")
		assert.Equal(t, "updated-valid-name", updatedFunction.Name)
	})

	// Test updating to invalid names
	invalidUpdateNames := []struct {
		name   string
		reason string
	}{
		{"Invalid Name", "contains space"},
		{"_invalid", "starts with underscore"},
		{"invalid-", "ends with hyphen"},
		{"123invalid", "starts with number"},
		{"INVALID", "uppercase letters"},
	}

	for _, invalidCase := range invalidUpdateNames {
		t.Run("UpdateToInvalid_"+invalidCase.name, func(t *testing.T) {
			updateFunction := originalFunction
			updateFunction.Name = invalidCase.name
			updateFunction.UpdatedBy = userId

			_, err := core.UpdateLLMFunction(sc, accountId, createdFunction.Id, updateFunction)
			assert.NotNil(t, err, "Should reject invalid name '%s' (%s)", invalidCase.name, invalidCase.reason)

			if err != nil {
				assert.Contains(t, err.Error(), "function.name", "Error message should mention function.name")
			}
		})
	}

	// Clean up
	_, err = core.DeleteLLMFunction(sc, accountId, createdFunction.Id)
	assert.Nil(t, err, "Failed to clean up test function")
}

func TestFunctions_InvokeFunction(t *testing.T) {
	tenantId := os.Getenv("TEST_TENANT")
	userId := os.Getenv("TEST_USER")
	accountId := os.Getenv("TEST_ACCOUNT")
	sessionId := "nb-function-1"
	query := "/call pg_connections_pods"

	sc := security.NewRequestContextForTenantAccountAdmin(tenantId, userId, []string{})
	err := core.DeleteConversationBySession(sessionId, accountId, userId)
	assert.Nil(t, err)

	defaultAgent, ok := core.GetNBAgent(sc, agents.AgentK8sDebugName, accountId, core.AgentStatusEnabled)
	if !ok {
		t.Fatal("default agent not found")
	}
	resp, err := core.HandleConversationSessionRequest(sc, defaultAgent, userId, accountId, sessionId, query)

	fmt.Println("response - ", resp.Response)
	fmt.Println("tools - ", resp.AgentStepResponse)

	assert.Nil(t, err)
	assert.NotNil(t, resp)
	assert.NotEmpty(t, resp.Query)
	assert.NotNil(t, resp.AgentStepResponse)
	assert.Greater(t, len(resp.Response), 0)
	assert.Nil(t, err)
	assert.Equal(t, resp.Status, "COMPLETED")
}
