package tools

import (
	"fmt"
	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/services_server"
	"nudgebee/llm/tools/core"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var (
	lokiStreamSelectorRe = regexp.MustCompile(`\{([^}]*)\}`)
	lokiLabelNameRe      = regexp.MustCompile(`(\w+)\s*[!=~]`)
)

const ToolGetLokiLogs = "loki_execute"

func init() {
	core.RegisterNBToolFactory(ToolGetLokiLogs, func(accountId string) (core.NBTool, error) {
		return LokiExecuteTool{
			accountId: accountId,
		}, nil
	})
}

func GetLokiLabels(accountId string) ([]string, error) {
	resp, err := executeFetchLogLabels(accountId, services_server.ObservabilityProvider{
		IntegrationSource: "agent",
		Provider:          "loki",
	})
	if err != nil {
		return nil, err
	}
	labels := make([]string, 0, len(resp.Labels))
	for _, label := range resp.Labels {
		labels = append(labels, label.Label)
	}

	return labels, nil
}

type LokiExecuteTool struct {
	accountId string
	labels    []string
}

func (m LokiExecuteTool) Name() string {
	return ToolGetLokiLogs
}

func (m LokiExecuteTool) GetType() core.NBToolType {
	return core.NBToolTypeTool
}

func (t *LokiExecuteTool) QueryLabels() []string {
	if len(t.labels) == 0 {
		lokiLabels, err := GetLokiLabels(t.accountId)
		if err != nil {
			return []string{}
		}
		if len(lokiLabels) > 0 {
			t.labels = lokiLabels
		}
	}
	return t.labels
}

func (t *LokiExecuteTool) QueryLabelValues() []string {
	return []string{}
}

func (t *LokiExecuteTool) GetOperators() []string {
	return []string{}
}

func (m LokiExecuteTool) Description() string {
	return `Executes a LogQL query against a Loki instance and retrieves the corresponding log data. 

	Usage:

	* Input: Provide a well-formatted LogQL query as input.
	* Output: The tool will return the log data retrieved from Loki based on your query.

	Important Notes:

	* Ensure the LogQL query is syntactically correct and adheres to the Loki query language specification.
	* The output may contain a large volume of log data. You should process and summarize the data appropriately before presenting it to the user.`
}

func (m LokiExecuteTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.ToolSchemaProperty{
			"command": {
				Type:        core.ToolSchemaTypeString,
				Description: "The Loki query to execute",
			},
			"start_time": {
				Type:        core.ToolSchemaTypeString,
				Description: "Start Time for the query. Format: RFC3339 or Unix timestamp",
			},
			"end_time": {
				Type:        core.ToolSchemaTypeString,
				Description: "End Time for the query. Format: RFC3339 or Unix timestamp",
			},
			"range": {
				Type:        core.ToolSchemaTypeString,
				Description: "Time range for the query (e.g., '2d', '1w', '1h'). If provided, start_time is calculated relative to end_time.",
			},
		},
		Required: []string{"command"},
	}
}

// cleanupQuery sanitizes LLM-generated LogQL queries by stripping common formatting
// artifacts (markdown code blocks, backticks, quotes) and incomplete trailing operators.
func (m LokiExecuteTool) cleanupQuery(query string) string {
	finalQuery := strings.TrimSpace(query)

	// Strip markdown code block wrappers (```logql ... ``` or ``` ... ```)
	if strings.HasPrefix(finalQuery, "```") {
		if firstNewline := strings.Index(finalQuery, "\n"); firstNewline > 0 {
			finalQuery = finalQuery[firstNewline+1:]
		} else {
			finalQuery = strings.TrimPrefix(finalQuery, "```")
		}
		finalQuery = strings.TrimSuffix(strings.TrimSpace(finalQuery), "```")
		finalQuery = strings.TrimSpace(finalQuery)
	}

	// Strip single backtick wrapping
	finalQuery = strings.TrimSuffix(finalQuery, "`")
	finalQuery = strings.TrimPrefix(finalQuery, "`")

	// Strip double-quote wrapping
	if len(finalQuery) >= 2 && finalQuery[0] == '"' && finalQuery[len(finalQuery)-1] == '"' {
		finalQuery = finalQuery[1 : len(finalQuery)-1]
	}

	finalQuery = strings.TrimSpace(finalQuery)

	// Strip incomplete trailing pipe operators
	finalQuery = strings.TrimSuffix(finalQuery, "|")
	finalQuery = strings.TrimSuffix(finalQuery, "|~")
	finalQuery = strings.TrimSuffix(finalQuery, "|=")

	return strings.TrimSpace(finalQuery)
}

// findInvalidLabels extracts label names from a LogQL stream selector and checks
// them against the valid label set. Returns labels used in the query that are not valid.
func (m LokiExecuteTool) findInvalidLabels(query string, validLabels []string) []string {
	matches := lokiStreamSelectorRe.FindStringSubmatch(query)
	if len(matches) < 2 {
		return nil
	}

	validSet := make(map[string]bool, len(validLabels))
	for _, l := range validLabels {
		validSet[l] = true
	}

	labelMatches := lokiLabelNameRe.FindAllStringSubmatch(matches[1], -1)
	var invalid []string
	seen := make(map[string]bool)
	for _, lm := range labelMatches {
		if len(lm) > 1 {
			label := lm[1]
			if !validSet[label] && !seen[label] {
				invalid = append(invalid, label)
				seen[label] = true
			}
		}
	}
	return invalid
}

func (m LokiExecuteTool) updateLimit(query string, limit int) string {
	if limit == 0 {
		limit = config.Config.LLMServerAgentMaxLogLines
	}

	// fix for wrong query limit generation for loki
	if strings.Contains(query, "| limit=") {
		querySplits := strings.Split(query, "| limit=")
		query = querySplits[0]
		limit1, err := strconv.ParseInt(querySplits[1], 10, 64)
		if err == nil {
			limit = int(limit1)
		}
	} else if strings.Contains(query, "| limit ") {
		querySplits := strings.Split(query, "| limit ")
		query = querySplits[0]
		limit1, err := strconv.ParseInt(querySplits[1], 10, 64)
		if err == nil {
			limit = int(limit1)
		}
	}

	if !strings.Contains(query, "limit=") {
		query = fmt.Sprintf("%s&limit=%d", query, limit)
	}
	return query
}

func (m LokiExecuteTool) Call(nbRequestContext core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {
	nbRequestContext.Ctx.GetLogger().Info("loki: executing getResourceLogs tool call", "query", input.Command)
	finalQuery := m.cleanupQuery(input.Command)

	// Validate labels before executing to give the LLM actionable feedback
	validLabels := m.QueryLabels()
	if len(validLabels) > 0 {
		if invalidLabels := m.findInvalidLabels(finalQuery, validLabels); len(invalidLabels) > 0 {
			suggestion := fmt.Sprintf(
				"Query contains invalid label(s): %s. Valid labels are: %s. Please regenerate the query using only valid labels.",
				strings.Join(invalidLabels, ", "),
				strings.Join(validLabels, ", "),
			)
			return core.NBToolResponse{
				Data:   suggestion,
				Status: core.NBToolResponseStatusError,
			}, nil
		}
	}

	limit := 0
	if limitAny, ok := input.Arguments["limit"]; ok {
		switch limitValue := limitAny.(type) {
		case string:
			limit, _ = strconv.Atoi(limitValue)
		case float64:
			limit = int(limitValue)
		case int:
			limit = limitValue
		}
	}
	if limit == 0 {
		limit = 100
	}
	finalQuery = m.updateLimit(finalQuery, limit)

	start := time.Now().Add(-1 * time.Hour)
	end := time.Now()

	// Parse time range arguments (start_time, end_time, range)
	t1, t2, err := ExtractStartEndtimeFromLabels(nbRequestContext, input.Arguments)
	if err == nil {
		start = t1
		end = t2
	}
	start, end = ExpandNarrowTimeWindow(nbRequestContext.Ctx.GetLogger(), start, end)

	if !strings.HasSuffix(finalQuery, "query=") {
		finalQuery = fmt.Sprintf("query=%s", finalQuery)
	}
	if strings.Contains(finalQuery, "?") {
		finalQuery = fmt.Sprintf("%s?", finalQuery)
	}
	if !strings.Contains(finalQuery, "start=") {
		finalQuery = fmt.Sprintf("%s&start=%d", finalQuery, start.UnixNano())
	}
	if !strings.Contains(finalQuery, "end=") {
		finalQuery = fmt.Sprintf("%s&end=%d", finalQuery, end.UnixNano())
	}

	response, err := executeFetchLogs(nbRequestContext, services_server.ObservabilityProvider{
		Provider:          "loki",
		IntegrationSource: "agent",
	}, finalQuery, map[string]any{
		"end_time":   end.UnixMilli(),
		"start_time": start.UnixMilli(),
		"limit":      limit,
	})

	if err != nil {
		nbRequestContext.Ctx.GetLogger().Error("loki: unable to execute api call", "error", err.Error())
		errMsg := fmt.Sprintf("Failed to fetch logs. Error: %s. Query attempted: %s", err.Error(), input.Command)
		return core.NBToolResponse{
			Data:   errMsg,
			Status: core.NBToolResponseStatusError,
		}, err
	}
	if len(response.Logs) == 0 {
		nbRequestContext.Ctx.GetLogger().Info("loki: no logs found for given query", "query", finalQuery, "end_time", end.UnixMilli(), "start_time", start.UnixMilli(), "limit", limit)
		noLogsMsg := fmt.Sprintf(
			"No logs found for query: %s (time range: %s to %s, limit: %d). "+
				"Suggestions: check if label names/values are correct, try broader regex filters (|~ instead of |=), or expand the time range.",
			input.Command, start.Format(time.RFC3339), end.Format(time.RFC3339), limit,
		)
		return core.NBToolResponse{
			Data:   noLogsMsg,
			Status: core.NBToolResponseStatusError,
		}, nil
	}

	data, err := common.MarshalJson(response)
	if err != nil {
		nbRequestContext.Ctx.GetLogger().Error("loki: unable to serialize json", "error", err.Error())
		return core.NBToolResponse{
			Data:   "",
			Status: core.NBToolResponseStatusError,
		}, err
	}

	referenceQuery := finalQuery
	referenceQuery, _ = strings.CutPrefix(referenceQuery, "query=")

	return core.NBToolResponse{
		Data:       string(data),
		Type:       core.NBToolResponseTypeJson,
		Status:     core.NBToolResponseStatusSuccess,
		References: []core.NBToolResponseReference{core.GetNudgebeeUIReferenceForClusterDetails(nbRequestContext, []string{"monitoring", "query-logs"}, "Query Logs", map[string]string{"tab": "4", "subtab": "0"}, referenceQuery)},
	}, nil
}
