package ai

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"nudgebee/runbook/internal/tasks/testutils"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLLMA2ATask_Execute_JSONRPC(t *testing.T) {
	// Mock JSON-RPC Server
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		// Assert Request
		if req["method"] == "test.echo" {
			params := req["params"].(map[string]any)
			w.Header().Set("Content-Type", "application/json")
			if err := json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      req["id"],
				"result":  params["msg"],
			}); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
			}
			return
		}

		if req["method"] == "test.error" {
			w.Header().Set("Content-Type", "application/json")
			if err := json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      req["id"],
				"error": map[string]any{
					"code":    -32000,
					"message": "Server Error",
				},
			}); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
			}
			return
		}
	}))
	defer ts.Close()

	task := &LLMA2ATask{}
	taskCtx := testutils.NewTestTaskContext(os.Getenv("TEST_TENANT_ID"), os.Getenv("TEST_ACCOUNT_ID"), os.Getenv("TEST_USER_ID"), nil)

	// Test Case 1: Success
	params := map[string]any{
		"url":    ts.URL,
		"method": "test.echo",
		"params": map[string]any{"msg": "hello world"},
	}
	res, err := task.Execute(taskCtx, params)
	assert.NoError(t, err)
	resMap := res.(map[string]any)
	assert.Equal(t, "hello world", resMap["result"])

	// Test Case 2: Error
	paramsErr := map[string]any{
		"url":    ts.URL,
		"method": "test.error",
	}
	_, err2 := task.Execute(taskCtx, paramsErr)
	assert.Error(t, err2)
	assert.Contains(t, err2.Error(), "Server Error")
}

func TestLLMA2ATask_InputSchema(t *testing.T) {
	task := &LLMA2ATask{}
	schema := task.InputSchema()

	assert.NotNil(t, schema)
	assert.Contains(t, schema.Properties, "url")
	assert.True(t, schema.Properties["url"].Required)
	assert.Contains(t, schema.Properties, "method")
	assert.True(t, schema.Properties["method"].Required)
	assert.Contains(t, schema.Properties, "params")
	assert.False(t, schema.Properties["params"].Required)
}

func TestLLMA2ATask_OutputSchema(t *testing.T) {
	task := &LLMA2ATask{}
	schema := task.OutputSchema()

	assert.NotNil(t, schema)
	assert.Contains(t, schema.Properties, "result")
	assert.Contains(t, schema.Properties, "id")
	assert.Contains(t, schema.Properties, "jsonrpc")
}

func TestLLMA2ATask_GetName(t *testing.T) {
	task := &LLMA2ATask{}
	assert.Equal(t, "llm.a2a_call", task.GetName())
}
