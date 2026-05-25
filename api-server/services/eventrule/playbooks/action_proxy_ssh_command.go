package playbooks

import (
	"fmt"
	"nudgebee/services/common"
	"nudgebee/services/relay"
)

type proxySSHCommandAction struct{}

type proxySSHCommandParams struct {
	DatasourceID string `json:"datasource_id"`
	Command      string `json:"command"`
	TimeoutMs    int    `json:"timeout_ms,omitempty"`
	AccountID    string `json:"account_id,omitempty"`
}

func (a *proxySSHCommandAction) Execute(ctx PlaybookActionContext, rawParams map[string]any) (PlaybookActionResponse, error) {
	var params proxySSHCommandParams
	if err := common.UnmarshalMapToStruct(rawParams, &params); err != nil {
		return nil, fmt.Errorf("failed to unmarshal params: %w", err)
	}

	if params.DatasourceID == "" {
		return nil, fmt.Errorf("datasource_id is required")
	}
	if params.Command == "" {
		return nil, fmt.Errorf("command is required")
	}

	accountID := params.AccountID
	if accountID == "" {
		accountID = resolveAccountIDForIntegration(params.DatasourceID)
	}
	if accountID == "" {
		accountID = ctx.GetAccountId()
	}
	if accountID == "" {
		return nil, fmt.Errorf("account_id is required")
	}

	actionParams := map[string]any{
		"command": params.Command,
	}
	if params.TimeoutMs > 0 {
		actionParams["timeout_ms"] = params.TimeoutMs
	}

	datasourceKey, err := resolveDatasourceKey(params.DatasourceID)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve datasource: %w", err)
	}

	timeoutSec := 60
	if params.TimeoutMs > 0 {
		timeoutSec = (params.TimeoutMs / 1000) + 5
	}

	result, err := relay.ExecuteProxy(accountID, "ssh_command", datasourceKey, actionParams, timeoutSec)
	if err != nil {
		return nil, fmt.Errorf("proxy ssh_command failed: %w", err)
	}

	title, _ := rawParams["title"].(string)
	if title == "" {
		title = "SSH Command"
	}

	return NewPlaybookActionResponseJson(
		result,
		map[string]any{"title": title},
		nil,
		map[string]any{"query-result-version": "1.0", "command": params.Command},
	), nil
}
