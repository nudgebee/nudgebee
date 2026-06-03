package playbooks

import (
	"fmt"
	"nudgebee/services/common"
	"nudgebee/services/relay"
)

type proxyDBQueryAction struct{}

type proxyDBQueryParams struct {
	DatasourceID string `json:"datasource_id"`
	Query        string `json:"query"`
	Database     string `json:"database,omitempty"`
	MaxRows      int    `json:"max_rows,omitempty"`
	TimeoutMs    int    `json:"timeout_ms,omitempty"`
	AccountID    string `json:"account_id,omitempty"`
}

func (a *proxyDBQueryAction) Execute(ctx PlaybookActionContext, rawParams map[string]any) (PlaybookActionResponse, error) {
	var params proxyDBQueryParams
	if err := common.UnmarshalMapToStruct(rawParams, &params); err != nil {
		return nil, fmt.Errorf("failed to unmarshal params: %w", err)
	}

	if params.DatasourceID == "" {
		return nil, fmt.Errorf("datasource_id is required")
	}
	if params.Query == "" {
		return nil, fmt.Errorf("query is required")
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
		"query": params.Query,
	}
	if params.MaxRows > 0 {
		actionParams["max_rows"] = params.MaxRows
	}
	if params.TimeoutMs > 0 {
		actionParams["timeout_ms"] = params.TimeoutMs
	}
	if params.Database != "" {
		actionParams["database"] = params.Database
	}

	datasourceKey, err := resolveDatasourceKey(params.DatasourceID)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve datasource: %w", err)
	}

	timeoutSec := 60
	if params.TimeoutMs > 0 {
		timeoutSec = (params.TimeoutMs / 1000) + 5
	}

	result, err := relay.ExecuteProxy(accountID, "db_query", datasourceKey, actionParams, timeoutSec)
	if err != nil {
		return nil, fmt.Errorf("proxy db_query failed: %w", err)
	}

	headers, rows := extractTableData(result)

	title, _ := rawParams["title"].(string)
	if title == "" {
		title = "Database Query"
	}

	return PlaybookActionResponseTable{
		Headers:        headers,
		Rows:           rows,
		AdditionalInfo: map[string]any{"title": title},
	}, nil
}

// extractTableData extracts headers and rows from the proxy DB response
// format: {columns: [{name, type}], rows: [[v1, v2], ...]}
func extractTableData(result map[string]any) ([]string, [][]any) {
	columnsRaw, ok := result["columns"].([]any)
	if !ok || len(columnsRaw) == 0 {
		return nil, nil
	}

	headers := make([]string, len(columnsRaw))
	for i, c := range columnsRaw {
		headers[i] = fmt.Sprintf("col_%d", i)
		if colMap, ok := c.(map[string]any); ok {
			if name, ok := colMap["name"].(string); ok && name != "" {
				headers[i] = name
			}
		}
	}

	rowsRaw, _ := result["rows"].([]any)
	rows := make([][]any, 0, len(rowsRaw))
	for _, row := range rowsRaw {
		rowArr, ok := row.([]any)
		if !ok {
			continue
		}
		rows = append(rows, rowArr)
	}

	return headers, rows
}
