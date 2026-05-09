package workflow

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"strings"

	"github.com/google/uuid"
)

func getWorkflowServerURL(path string) string {
	url := config.Config.WorkflowServerEndpoint
	if !strings.HasSuffix(url, "/") {
		url += "/"
	}
	// remove leading slash from path if present to avoid double slash
	path = strings.TrimPrefix(path, "/")
	return url + path
}

func doWorkflowRequest(method, path string, body any, accountId, tenantId, userId string) ([]byte, error) {
	url := getWorkflowServerURL(path)

	var reqBody io.Reader
	if body != nil {
		jsonBytes, err := common.MarshalJson(body)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal request body: %w", err)
		}
		reqBody = bytes.NewBuffer(jsonBytes)
	}

	req, err := http.NewRequest(method, url, reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	// Assuming authentication via token or account ID headers is handled similarly to other services
	req.Header.Set("X-Account-ID", accountId)
	req.Header.Set("X-Tenant-ID", tenantId)
	if userId != "" && userId != uuid.Nil.String() {
		req.Header.Set("X-User-ID", userId)
	}

	client := common.HttpClient()
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("workflow server returned error (status %d): %s", resp.StatusCode, string(respBytes))
	}

	return respBytes, nil
}

type ListWorkflowsRequest struct {
	Limit int    `json:"limit"`
	Name  string `json:"name"`
}

func ListWorkflows(ctx *security.RequestContext, accountId string, req ListWorkflowsRequest) ([]byte, error) {
	if accountId == "" {
		return nil, fmt.Errorf("account ID is required")
	}
	queryParams := []string{}
	if req.Limit > 0 {
		queryParams = append(queryParams, fmt.Sprintf("limit=%v", req.Limit))
	}
	if req.Name != "" {
		queryParams = append(queryParams, fmt.Sprintf("name=%v", req.Name))
	}

	path := "workflows"
	if len(queryParams) > 0 {
		path += "?" + strings.Join(queryParams, "&")
	}

	return doWorkflowRequest("GET", path, nil, accountId, ctx.GetSecurityContext().GetTenantId(), ctx.GetSecurityContext().GetUserId())
}

func DescribeWorkflow(ctx *security.RequestContext, accountId string, workflowId string) ([]byte, error) {
	if accountId == "" {
		return nil, fmt.Errorf("account ID is required")
	}
	if workflowId == "" {
		return nil, fmt.Errorf("workflow ID is required")
	}
	path := "workflows/" + workflowId

	return doWorkflowRequest("GET", path, nil, accountId, ctx.GetSecurityContext().GetTenantId(), ctx.GetSecurityContext().GetUserId())
}

func TriggerWorkflow(ctx *security.RequestContext, accountId string, workflowId string, inputs any) ([]byte, error) {
	if accountId == "" {
		return nil, fmt.Errorf("account ID is required")
	}
	if workflowId == "" {
		return nil, fmt.Errorf("workflow ID is required")
	}
	path := "workflows/" + workflowId + "/trigger"

	resp, err := doWorkflowRequest("POST", path, inputs, accountId, ctx.GetSecurityContext().GetTenantId(), ctx.GetSecurityContext().GetUserId())
	if err != nil {
		return nil, err
	}
	return resp, nil
}

func ValidateWorkflow(ctx *security.RequestContext, accountId string, definition any) ([]byte, error) {
	if accountId == "" {
		return nil, fmt.Errorf("account ID is required")
	}
	if definition == nil {
		return nil, fmt.Errorf("definition is required")
	}
	path := "workflows/validate"

	resp, err := doWorkflowRequest("POST", path, definition, accountId, ctx.GetSecurityContext().GetTenantId(), ctx.GetSecurityContext().GetUserId())
	if err != nil {
		return nil, err
	}
	return resp, nil
}

func CreateWorkflow(ctx *security.RequestContext, accountId string, definition any) ([]byte, error) {
	if accountId == "" {
		return nil, fmt.Errorf("account ID is required")
	}
	if definition == nil {
		return nil, fmt.Errorf("definition is required")
	}
	path := "workflows"

	resp, err := doWorkflowRequest("POST", path, definition, accountId, ctx.GetSecurityContext().GetTenantId(), ctx.GetSecurityContext().GetUserId())
	if err != nil {
		return nil, err
	}
	return resp, nil
}

func ListTasks(ctx *security.RequestContext, accountId string) ([]byte, error) {
	if accountId == "" {
		return nil, fmt.Errorf("account ID is required")
	}
	path := "tasks"

	resp, err := doWorkflowRequest("GET", path, nil, accountId, ctx.GetSecurityContext().GetTenantId(), ctx.GetSecurityContext().GetUserId())
	if err != nil {
		return nil, err
	}
	return resp, nil
}
