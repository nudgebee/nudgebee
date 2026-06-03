package tools

import (
	"fmt"
	"nudgebee/llm/security"
	"nudgebee/llm/services_server"
	"nudgebee/llm/tools/core"
	"strings"
)

const ToolTriggerCisScan = "trigger_cis_scan"

func init() {
	core.RegisterNBToolFactory(ToolTriggerCisScan, func(accountId string) (core.NBTool, error) {
		return CisScanTool{}, nil
	})
}

type CisScanTool struct {
}

func (m CisScanTool) Name() string {
	return ToolTriggerCisScan
}

func (m CisScanTool) GetType() core.NBToolType {
	return core.NBToolTypeTool
}

func (m CisScanTool) Description() string {
	return `Triggers a CIS scan on a cluster.

**Usage:**

* Use this tool when you are asked to trigger cis scans.
* **Input:** command.
* **Output:** Returns the status of the scan.

**Example Input:**
{
	"AccountId": "abc",
	"job_name": "trivy_cis_scan"
}

**Important Notes:**
* The job_name must be valid.
* Use the returned status to guide further action or suggest remediation.
`
}

func (m CisScanTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.ToolSchemaProperty{
			"command": {
				Type:        core.ToolSchemaTypeString,
				Description: "command to execute",
			},
		},
		Required: []string{"command"},
	}
}

func (m CisScanTool) Call(nbRequestContext core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {
	command := strings.TrimSpace(input.Command)
	if command == "" {
		return core.NBToolResponse{}, fmt.Errorf("empty command")
	}
	jobName := "trivy_cis_scan"

	tenantId, err := security.GetTenantIdFromAccountId(nbRequestContext.AccountId)
	if err != nil {
		fmt.Println("Error getting tenant ID:", err)
		return core.NBToolResponse{}, err
	}

	nbRequestContext.Ctx.GetLogger().Info("triggering CIS scan", "accountId", nbRequestContext.AccountId, "job_name", jobName)

	response, err := TriggerCisScanApi(tenantId, nbRequestContext.AccountId, nbRequestContext.UserId, jobName)
	if err != nil {
		nbRequestContext.Ctx.GetLogger().Error("Unable to trigger CIS Scan", "error", err.Error())
		return core.NBToolResponse{
			Data:   "",
			Status: core.NBToolResponseStatusError,
		}, err
	}

	return core.NBToolResponse{
		Data:   response,
		Type:   core.NBToolResponseTypeText,
		Status: core.NBToolResponseStatusSuccess,
	}, nil
}

// func TriggerCisScanApi(tenantId, accountId, userId, jobName string) (string, error) {
// 	return `{"Scan triggered successfully. The data will be updated in sometimes"}`, nil
// }

func TriggerCisScanApi(tenantId, accountId, userId, jobName string) (string, error) {
	// Construct the ServicesRequest payload
	request := services_server.ScanCisServiceRequest{
		Action: services_server.Action{
			Name: "recommendation_job_create",
		},
		Input: services_server.ScanCisRequest{
			AccountId: accountId,
			JobName:   jobName,
		},
		SessionVariables: services_server.SessionVariables{
			UserID:       userId,
			UserTenantID: tenantId,
		},
	}

	// Call the Execute function
	_, err := services_server.ExecuteScanCisQuery(request)
	if err != nil {
		return "", fmt.Errorf("failed to trigger CIS scan: %w", err)
	}

	return "CIS scan triggered successfully. Data will be updated in sometime", nil

}

func (m CisScanTool) InferToolRequestTypePrompt(ctx *security.RequestContext, toolName, input string) (string, error) {
	prompt := `You are a knowledgeable and concise Kubernetes expert, acting as an SRE. Your primary tool is "trigger_cis_scan,". 

	Based on provided command, you need to categorize it into one of the following types:
	* read - Command can trigger a CIS scan

	you should only return command type without any explanations and internal thoughts added added. If you are unable to identify command-type then return 'unknown'
	
	For example -
	command - trigger a CIS scan
	answer - read
	reason - command is triggering a CIS scan
	`
	return prompt, nil
}
