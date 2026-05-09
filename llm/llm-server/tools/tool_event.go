package tools

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"nudgebee/llm/common"
	"nudgebee/llm/events"
	"nudgebee/llm/tools/core"
	"regexp"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/samber/lo"
)

func init() {
	core.RegisterNBToolFactory(ToolEventExecuteSql, func(accountId string) (core.NBTool, error) {
		return EventsExecuteTool{}, nil
	})
}

const ToolEventExecuteSql = "events_execute"

const eventsViewWithRanking = `select *
from (
	SELECT
	    id::text,
	    id::text as event_id,
	    created_at,
	    updated_at,
	    finding_id,
	    title,
	    description,
	    source,
	    aggregation_key,
	    failure,
	    finding_type,
	    category,
	    priority,
	    subject_type,
	    subject_name,
	    subject_namespace,
	    subject_node,
	    subject_owner,
	    ends_at,
	    starts_at,
	    fingerprint,
	    evidences::text,
	    cloud_account_id::text,
	    status,
	    labels::text,
	    nb_status,
	    urgency,
	    computed_priority,
	    computed_score,
	    cluster,
	    service_key,
	    snoozed_until,
	    event_count
	FROM (
	    SELECT
	        *,
	        COUNT(*) OVER (PARTITION BY aggregation_key, subject_name, subject_type, subject_namespace, fingerprint) as event_count,
	        ROW_NUMBER() OVER (PARTITION BY aggregation_key, subject_name, subject_type, subject_namespace, fingerprint ORDER BY starts_at DESC) as rn
	    FROM
	        events
		WHERE starts_at >= '__DATE_FILTER__' and cloud_account_id = '__ACCOUNT_ID__'::uuid
	) ranked_events
	WHERE ranked_events.rn = 1
) as e`

const eventsViewWithId = `
select *
from (
	SELECT
	    id::text,
	    id::text as event_id,
	    created_at,
	    updated_at,
	    finding_id,
	    title,
	    description,
	    source,
	    aggregation_key,
	    failure,
	    finding_type,
	    category,
	    priority,
	    subject_type,
	    subject_name,
	    subject_namespace,
	    subject_node,
	    subject_owner,
	    ends_at,
	    starts_at,
	    fingerprint,
	    evidences::text,
	    cloud_account_id::text,
	    status,
	    labels::text,
	    nb_status,
	    urgency,
	    computed_priority,
	    computed_score,
	    cluster,
	    service_key,
	    snoozed_until,
	    1 as event_count
	FROM events WHERE cloud_account_id = '__ACCOUNT_ID__'::uuid AND starts_at >= '__DATE_FILTER__'
) as e
`

type EventsExecuteTool struct {
}

func (m EventsExecuteTool) Name() string {
	return ToolEventExecuteSql
}

func (m EventsExecuteTool) GetType() core.NBToolType {
	return core.NBToolTypeTool
}

func (m EventsExecuteTool) Description() string {
	return "Executes a SQL query for events and returns the data for event."
}

func (m EventsExecuteTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.ToolSchemaProperty{
			"command": {
				Type:        core.ToolSchemaTypeString,
				Description: "events SQL Query to execute",
			},
		},
		Required: []string{"command"},
	}
}

func (m EventsExecuteTool) processRowWithMessages(data map[string]any, i, c int, messagesMap map[string][]map[string]string) map[string]any {
	investigateData := events.InvestigateData{}
	if data["evidences"] != nil {
		evidence := data["evidences"].(string)
		evidences := []events.Evidence{}
		err := common.UnmarshalJson([]byte(evidence), &evidences)
		if err != nil {
			slog.Error("Error unmarshaling JSON:", "error", err)
		}
		for _, evidence := range evidences {

			if evidence.Type == "diff" {
				evidenceData := evidence.Data.(map[string]any)
				//for now we dont wantt to diff data as its normally very large
				// keep only insights for now
				if evidenceData["data"] != nil {
					delete(evidenceData, "data")
				}
				evidenceData["start_at"] = evidence.StartAt
				investigateData.Deployment = events.InvestigateDataInsight{
					Data:           evidenceData,
					Insight:        evidence.Insights,
					AdditionalInfo: evidence.AdditionalInfo,
					Title:          evidence.Type,
				}
			} else if evidence.Type == "markdown" && evidence.AdditionalInfo != nil && evidence.AdditionalInfo["type"] == "nubi_enricher" && evidence.LLMResponse != nil && evidence.LLMResponse["session_id"] != nil {
				// add response from Nubi Enricher
				sessionId := evidence.LLMResponse["session_id"].(string)
				var messages []map[string]string
				if val, ok := messagesMap[sessionId]; ok {
					messages = val
				}

				for _, msg := range messages {
					investigateData.Markdowns = append(investigateData.Markdowns, events.InvestigateDataInsight{
						Data: map[string]any{
							"message":  msg["message"],
							"response": msg["response"],
						},
						Insight: []events.Insight{
							{
								Message:  msg["response"],
								Severity: "info",
							},
						},
						Type: evidence.Type,
					})
				}
			} else if evidence.Type == "markdown" {
				investigateData.Markdowns = append(investigateData.Markdowns, events.InvestigateDataInsight{
					Data:           evidence.Data,
					Insight:        evidence.Insights,
					AdditionalInfo: evidence.AdditionalInfo,
					Title:          evidence.Type,
				})
			} else if evidence.Type == "table" {
				evidenceTable := []map[string]any{}

				evidenceData := evidence.Data.(map[string]any)
				rows := evidenceData["rows"]
				headers := evidenceData["headers"]
				if headers != nil && rows != nil {
					for _, row := range rows.([]any) {
						rowMap := make(map[string]any)
						for i, header := range headers.([]any) {
							if len(row.([]any)) > i {
								rowMap[header.(string)] = row.([]any)[i]
							} else {
								rowMap[header.(string)] = ""
							}
						}
						evidenceTable = append(evidenceTable, rowMap)
					}
				}
				switch evidenceData["table_name"] {
				case "*Alert labels*":
					investigateData.AlertLabels.Data = evidenceTable
					investigateData.AlertLabels.Insight = evidence.Insights
				case "*Job information*":
					investigateData.JobInformation.Data = evidenceTable
					investigateData.JobInformation.Insight = evidence.Insights
					investigateData.JobInformation.AdditionalInfo = evidence.AdditionalInfo
					investigateData.JobInformation.Title = evidence.Type
				case "*Related Events*":
					investigateData.RelatedEvents.Data = evidenceTable
					investigateData.RelatedEvents.Insight = evidence.Insights
					investigateData.RelatedEvents.AdditionalInfo = evidence.AdditionalInfo
					investigateData.RelatedEvents.Title = evidence.Type
				case "*Job events:*":
					investigateData.JobEvents.Data = evidenceTable
					investigateData.JobEvents.Insight = evidence.Insights
					investigateData.JobEvents.AdditionalInfo = evidence.AdditionalInfo
					investigateData.JobEvents.Title = evidence.Type
				case "*Pod events:*":
					eventData := make([]any, len(evidenceTable))
					for i, v := range evidenceTable {
						eventData[i] = v
					}
					investigateData.PodEvents = append(investigateData.PodEvents, events.InvestigateDataInsight{
						Data:           eventData,
						Insight:        evidence.Insights,
						AdditionalInfo: evidence.AdditionalInfo,
						Title:          evidence.Type,
					})
				case "*Node events:*":
					eventData := make([]any, len(evidenceTable))
					for i, v := range evidenceTable {
						eventData[i] = v
					}
					investigateData.NodeEvents = append(investigateData.NodeEvents, events.InvestigateDataInsight{
						Data:           eventData,
						Insight:        evidence.Insights,
						AdditionalInfo: evidence.AdditionalInfo,
						Title:          evidence.Type,
					})
				case "*Job pod events:*":
					investigateData.JobPodEvents.Data = evidenceTable
					investigateData.JobPodEvents.Insight = evidence.Insights
					investigateData.JobPodEvents.AdditionalInfo = evidence.AdditionalInfo
					investigateData.JobPodEvents.Title = evidence.Type
				case "*StatefulSet events:*", "*DaemonSet events:*", "*Deployment events:*":
					// K8s resource events — store alongside pod events
					eventData := make([]any, len(evidenceTable))
					for i, v := range evidenceTable {
						eventData[i] = v
					}
					investigateData.PodEvents = append(investigateData.PodEvents, events.InvestigateDataInsight{
						Data:           eventData,
						Insight:        evidence.Insights,
						AdditionalInfo: evidence.AdditionalInfo,
						Title:          evidenceData["table_name"].(string),
					})
				case "*Pod and Node OOMKilled data*":
					// OOMKilled data — store alongside pod events for context
					eventData := make([]any, len(evidenceTable))
					for i, v := range evidenceTable {
						eventData[i] = v
					}
					investigateData.PodEvents = append(investigateData.PodEvents, events.InvestigateDataInsight{
						Data:           eventData,
						Insight:        evidence.Insights,
						AdditionalInfo: evidence.AdditionalInfo,
						Title:          "*Pod and Node OOMKilled data*",
					})
				default:
					investigateData.Others = append(investigateData.Others, events.InvestigateDataInsight{
						Data:           evidence.Data,
						Insight:        evidence.Insights,
						Type:           evidence.Type,
						AdditionalInfo: evidence.AdditionalInfo,
						Title:          evidence.Type,
					})
				}
			} else if evidence.Type == "gz" && (strings.Contains(evidence.Filename, "log.gz") || strings.Contains(evidence.Filename, "txt.gz")) {
				dataStr, ok := evidence.Data.(string)
				if !ok {
					slog.Warn("evidence data is not a string for gz type, skipping", "actual_type", fmt.Sprintf("%T", evidence.Data))
					continue
				}
				evidenceData := dataStr // fallback: use raw data if decompression fails
				encodedString := strings.TrimSpace(dataStr)
				switch {
				case strings.HasPrefix(encodedString, "b'") && strings.HasSuffix(encodedString, "'"):
					encodedString = strings.TrimSuffix(strings.TrimPrefix(encodedString, "b'"), "'")
				case strings.HasPrefix(encodedString, `b"`) && strings.HasSuffix(encodedString, `"`):
					encodedString = strings.TrimSuffix(strings.TrimPrefix(encodedString, `b"`), `"`)
				}
				decodedData, err := base64.StdEncoding.DecodeString(encodedString)
				if err != nil {
					slog.Error("Error decoding Base64 string:", "error", err)
				} else {
					reader := bytes.NewReader(decodedData)
					gzipReader, err := gzip.NewReader(reader)
					if err != nil {
						slog.Error("Error creating gzip reader:", "error", err)
					} else {
						var buf bytes.Buffer
						_, err = io.Copy(&buf, gzipReader)
						if err != nil {
							slog.Error("Error decompressing data:", "error", err)
						} else {
							evidenceData = buf.String()
						}
						if err := gzipReader.Close(); err != nil {
							slog.Error("Error closing gzip reader:", "error", err)
						}
					}
				}
				if investigateData.LogData != "" {
					investigateData.LogData = investigateData.LogData + "\n" + evidenceData
				} else {
					investigateData.LogData = evidenceData
				}

			} else if evidence.Type == "json" && evidence.AdditionalInfo != nil && (evidence.AdditionalInfo["action_name"] == "signoz_logs_enricher" || evidence.AdditionalInfo["action_name"] == "logs_enricher" || evidence.AdditionalInfo["action_name"] == "cloud_logs" || evidence.AdditionalInfo["action_name"] == "logs") {
				dataStr, ok := evidence.Data.(string)
				if !ok {
					// evidence.Data is already a parsed object (map), try to marshal it back to JSON
					jsonBytes, marshalErr := json.Marshal(evidence.Data)
					if marshalErr != nil {
						slog.Warn("evidence data is not a string for json type and cannot marshal, skipping", "actual_type", fmt.Sprintf("%T", evidence.Data))
						continue
					}
					dataStr = string(jsonBytes)
				}
				jsonString := strings.TrimSpace(dataStr)
				decodedDataMap := map[string]any{}
				err := common.UnmarshalJson([]byte(jsonString), &decodedDataMap)
				if err != nil {
					slog.Error("Error decoding json string:", "error", err)
					investigateData.LogData = investigateData.LogData + "\n" + dataStr
				} else {
					if dataAny, ok := decodedDataMap["data"]; ok {
						if dataMap, ok := dataAny.(map[string]any); ok && dataMap["result"] != nil {
							if resultArray, ok := dataMap["result"].([]any); ok {
								for _, result := range resultArray {
									if resultMap, ok := result.(map[string]any); ok && resultMap["list"] != nil {
										for _, logMessageAny := range resultMap["list"].([]any) {
											if logMessageMap, ok := logMessageAny.(map[string]any); ok {
												if dataAny, ok := logMessageMap["data"]; ok {
													if dataMap, ok := dataAny.(map[string]any); ok && dataMap["body"] != nil {
														if bodyStr, ok := dataMap["body"].(string); ok {
															investigateData.LogData = investigateData.LogData + "\n" + bodyStr
														}
													}
												}
											}
										}
									}
								}

							}

						} else if resultArray, ok := dataAny.([]any); ok {
							for _, result := range resultArray {
								if dataMap, ok := result.(map[string]any); ok && dataMap["message"] != nil {
									if bodyStr, ok := dataMap["message"].(string); ok {
										investigateData.LogData = investigateData.LogData + "\n" + bodyStr
									}
								}
							}

						}

					}

				}

			} else if evidence.Type == "api_traces_enricher_v2" {
				investigateData.Traces.Data = evidence.Data
				if stringData, ok := evidence.Data.(string); ok {
					tracesMap := map[string]any{}
					if err := common.UnmarshalJson([]byte(stringData), &tracesMap); err != nil {
						investigateData.Traces.Data = map[string]any{}
					} else {
						investigateData.Traces.Data = tracesMap
					}
				}
				investigateData.Traces.Insight = evidence.Insights
				investigateData.Traces.AdditionalInfo = evidence.AdditionalInfo
				investigateData.Traces.Title = evidence.Type
			} else if evidence.Type == "json" {
				evidenceDataJson := map[string]any{}
				additionalInfo := evidence.AdditionalInfo
				if evidenceStr, ok := evidence.Data.(string); ok {
					parsed, err := common.UnmarshalJsonAsMap([]byte(evidenceStr))
					if err != nil {
						slog.Error("Error unmarshaling JSON:", "error", err, "evidence-data", evidence.Data)
					} else {
						evidenceDataJson = parsed
					}
				} else if evidenceMap, ok := evidence.Data.(map[string]any); ok {
					evidenceDataJson = evidenceMap
				}
				if evidenceDataJson["name"] == "pod_details" {
					investigateData.PodData.Data = evidenceDataJson["data"].(map[string]any)
					investigateData.PodData.Insight = evidence.Insights
					investigateData.PodData.AdditionalInfo = evidence.AdditionalInfo
					investigateData.PodData.Title = evidence.Type
				} else if (func() bool {
					if name, ok := evidenceDataJson["name"].(string); ok {
						return strings.Contains(name, "api_failure_enricher")
					}
					return false
				}()) || additionalInfo["title"] == "Nginx Ingress Controller Request" || additionalInfo["title"] == "Kong Request Count for 5xx Error Metrics" {
					investigateData.ApiFailures = append(investigateData.ApiFailures, events.InvestigateDataInsight{
						Data:           evidenceDataJson,
						Insight:        evidence.Insights,
						AdditionalInfo: evidence.AdditionalInfo,
						Title:          evidence.Type,
					})
				} else if evidenceDataJson["name"] == "noisy_neighbours" {
					investigateData.NoisyNeighbours = append(investigateData.NoisyNeighbours, events.InvestigateDataInsight{
						Data:           evidenceDataJson,
						Insight:        evidence.Insights,
						AdditionalInfo: evidence.AdditionalInfo,
						Title:          evidence.Type,
					})
				} else if evidenceDataJson["name"] == "node_metric" || additionalInfo["title"] == "Memory Usage Percentage By Nodes" || additionalInfo["title"] == "CPU Usage By Nodes (Core)" {
					investigateData.NodeMetrics = append(investigateData.NodeMetrics, events.InvestigateDataInsight{
						Data:           evidenceDataJson["data"],
						Insight:        evidence.Insights,
						AdditionalInfo: evidence.AdditionalInfo,
						Title:          evidence.Type,
					})
				} else if evidenceDataJson["name"] == "pod_metric" {
					investigateData.PodMetrics = append(investigateData.PodMetrics, events.InvestigateDataInsight{
						Data:           evidenceDataJson,
						Insight:        evidence.Insights,
						AdditionalInfo: evidence.AdditionalInfo,
						Title:          evidence.Type,
					})
				} else if evidenceDataJson["name"] == "container_metric" {
					investigateData.ContainerMetrics.Data = evidenceDataJson
					investigateData.ContainerMetrics.Insight = evidence.Insights
					investigateData.ContainerMetrics.AdditionalInfo = evidence.AdditionalInfo
					investigateData.ContainerMetrics.Title = evidence.Type
				} else if evidenceDataJson["name"] == "api_traces_enricher" {
					investigateData.Traces.Data = evidenceDataJson
					investigateData.Traces.Insight = evidence.Insights
					investigateData.Traces.AdditionalInfo = evidence.AdditionalInfo
					investigateData.Traces.Title = evidence.Type
				} else if evidenceDataJson["type"] == "database_query_response" {
					investigateData.RDBMSQueryData = append(investigateData.RDBMSQueryData, events.InvestigateDataInsight{
						Data:           evidenceDataJson,
						Type:           evidenceDataJson["type"].(string),
						Insight:        evidence.Insights,
						AdditionalInfo: evidence.AdditionalInfo,
						Title:          evidence.Type,
					})
				} else if evidenceDataJson["type"] == "pod_script_run_enricher" {
					investigateData.UserActions = append(investigateData.UserActions, events.InvestigateDataInsight{
						Data: map[string]any{
							"command":  evidenceDataJson["command"],
							"response": evidenceDataJson["response"],
						},
						Insight:        evidence.Insights,
						Type:           evidence.Type,
						AdditionalInfo: evidence.AdditionalInfo,
						Title:          evidence.Type,
					})
				} else if data["source"] == "AWS_CloudWatch_Alarm" && evidenceDataJson["AlarmArn"] != nil {
					investigateData.AlertData = events.InvestigateDataInsight{
						Data:           evidenceDataJson,
						Insight:        evidence.Insights,
						Type:           "AWS_CloudWatch_Alarm",
						AdditionalInfo: evidence.AdditionalInfo,
						Title:          evidence.Type,
					}
				} else {
					investigateData.Others = append(investigateData.Others, events.InvestigateDataInsight{
						Data:           evidenceDataJson,
						Insight:        evidence.Insights,
						Type:           evidence.Type,
						AdditionalInfo: evidence.AdditionalInfo,
						Title:          evidence.Type,
					})
				}
			} else if evidence.Type == "prometheus" {
				investigateData.MetricsData = append(investigateData.MetricsData, events.InvestigateDataInsight{
					Data:           evidence.Data,
					Insight:        evidence.Insights,
					AdditionalInfo: evidence.AdditionalInfo,
					Title:          evidence.Type,
				})
			} else if evidence.Type == "service_map" {
				investigateData.ServiceMap = events.InvestigateDataInsight{
					Data:           evidence.Data,
					Insight:        evidence.Insights,
					Type:           evidence.Type,
					AdditionalInfo: evidence.AdditionalInfo,
					Title:          evidence.Type,
				}
			} else if evidence.Type == "metric-anomaly" {
				// Structured anomaly detection result with PromQL query
				investigateData.MetricsData = append(investigateData.MetricsData, events.InvestigateDataInsight{
					Data:           evidence.Data,
					Insight:        evidence.Insights,
					AdditionalInfo: evidence.AdditionalInfo,
					Title:          "metric-anomaly",
				})
			} else if evidence.Type == "file" {
				// Base64 gzipped data — decode like gz type
				if dataStr, ok := evidence.Data.(string); ok {
					encodedString := strings.TrimSpace(dataStr)
					switch {
					case strings.HasPrefix(encodedString, "b'") && strings.HasSuffix(encodedString, "'"):
						encodedString = strings.TrimSuffix(strings.TrimPrefix(encodedString, "b'"), "'")
					case strings.HasPrefix(encodedString, `b"`) && strings.HasSuffix(encodedString, `"`):
						encodedString = strings.TrimSuffix(strings.TrimPrefix(encodedString, `b"`), `"`)
					}
					decodedData, err := base64.StdEncoding.DecodeString(encodedString)
					if err == nil {
						reader := bytes.NewReader(decodedData)
						gzipReader, err := gzip.NewReader(reader)
						if err == nil {
							var buf bytes.Buffer
							_, err = io.Copy(&buf, gzipReader)
							if err == nil {
								if investigateData.LogData != "" {
									investigateData.LogData = investigateData.LogData + "\n" + buf.String()
								} else {
									investigateData.LogData = buf.String()
								}
							}
							_ = gzipReader.Close()
						}
					}
				}
			}
		}

		if investigateData.LogData != "" {
			errorLines := GetErrorLinesFromLogStringOrDefault(investigateData.LogData, true)
			investigateData.ErrorLogData = errorLines
		}

		// only send full data if overall count is 1, else use error lines as normally this results in very huge data
		if c != 1 || len(investigateData.ErrorLogData) > 0 {
			investigateData.LogData = ""
		}

		if c == 1 && len(investigateData.LogData) > 0 && len(investigateData.LogData) > 500 {
			investigateData.LogData = lo.Substring(investigateData.LogData, -500, 500)
		}

		data["evidences"] = investigateData
	}

	return data
}

// isValidSQL performs basic validation to check if input looks like SQL
func isValidSQL(input string) bool {
	input = strings.TrimSpace(strings.ToLower(input))

	// Empty input
	if input == "" {
		return false
	}

	// Check if it starts with SQL keywords
	validSQLStarters := []string{"select", "with", "desc", "describe", "show"}
	for _, starter := range validSQLStarters {
		if strings.HasPrefix(input, starter+" ") || input == starter {
			return true
		}
	}

	return false
}

// isDescriptiveText detects if input is descriptive text rather than SQL
func isDescriptiveText(input string) bool {
	input = strings.ToLower(input)

	// Common descriptive phrases that indicate non-SQL input
	descriptivePhrases := []string{
		"the user is asking",
		"i need to use",
		"i will use",
		"this query should",
		"the sql query should",
		"i need to query",
		"let me query",
		"i want to",
	}

	for _, phrase := range descriptivePhrases {
		if strings.Contains(input, phrase) {
			return true
		}
	}

	return false
}

// bareTimestampRe matches ISO 8601 timestamps that are NOT already inside single quotes.
// Captures: optional comparison operator + bare timestamp like 2025-01-25T13:00:00Z or 2025-01-25 13:00:00
var bareTimestampRe = regexp.MustCompile(`([><=!]+\s*)(\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:?\d{2})?)`)

// fixBareTimestamps wraps unquoted ISO timestamps in single quotes to prevent
// "trailing junk after numeric literal" errors in PostgreSQL.
func fixBareTimestamps(sql string) string {
	return bareTimestampRe.ReplaceAllStringFunc(sql, func(match string) string {
		// Don't double-quote if already quoted
		idx := strings.Index(sql, match)
		if idx > 0 && sql[idx-1] == '\'' {
			return match
		}
		// Extract the operator and timestamp parts
		parts := bareTimestampRe.FindStringSubmatch(match)
		if len(parts) < 3 {
			return match
		}
		return parts[1] + "'" + parts[2] + "'"
	})
}

func (m EventsExecuteTool) Call(nbRequestContext core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {
	// Validate that the input is actually SQL, not descriptive text
	if isDescriptiveText(input.Command) {
		return core.NBToolResponse{
			Data:   "Error: Please provide a valid SQL query, not a description. Example: 'SELECT * FROM events WHERE id = 'your-event-id'' or 'SELECT * FROM events ORDER BY starts_at DESC LIMIT 5'",
			Status: core.NBToolResponseStatusError,
		}, nil
	}

	if !isValidSQL(input.Command) {
		return core.NBToolResponse{
			Data:   "Error: Invalid SQL query. Please provide a valid SQL SELECT statement. Example: 'SELECT * FROM events WHERE priority = 'high'' or 'DESC events'",
			Status: core.NBToolResponseStatusError,
		}, nil
	}

	// check only for last 30 days data
	eventsView1 := eventsViewWithRanking
	upperCmd := strings.ToUpper(input.Command)
	isIdLookup := strings.Contains(input.Command, "id = '") || strings.Contains(input.Command, "id='")
	isAggregate := strings.Contains(upperCmd, "GROUP BY") || strings.Contains(upperCmd, "COUNT(") ||
		strings.Contains(upperCmd, "COUNT (") || strings.Contains(upperCmd, "SUM(") ||
		strings.Contains(upperCmd, "AVG(")
	if isIdLookup || isAggregate {
		eventsView1 = eventsViewWithId
	}

	if isIdLookup {
		// skip date filter for direct ID lookups so older events are still accessible
		eventsView1 = strings.ReplaceAll(eventsView1, " AND starts_at >= '__DATE_FILTER__'", "")
	} else if strings.Contains(eventsView1, "__DATE_FILTER__") {
		last30days := time.Now().AddDate(0, 0, -30)
		daysStr := last30days.Format("2006-01-02")
		eventsView1 = strings.ReplaceAll(eventsView1, "__DATE_FILTER__", daysStr)
	}

	if nbRequestContext.AccountId != "" {
		eventsView1 = strings.ReplaceAll(eventsView1, "__ACCOUNT_ID__", nbRequestContext.AccountId)
	}

	input.Command = strings.TrimSuffix(input.Command, ";")
	input.Command = fixBareTimestamps(input.Command)

	resp, data, err := sqlToolCall(nbRequestContext, input.Command, "events", eventsView1, 0, nil)
	if err != nil {
		return resp, err
	}

	references := []core.NBToolResponseReference{}
	if len(data) > 0 {
		m.enrichData(data)
		bytesData, marshalErr := common.MarshalJson(data)
		if marshalErr != nil {
			slog.Error("events: failed to marshal enriched data", "error", marshalErr)
			return core.NBToolResponse{}, marshalErr
		}
		resp.Data = string(bytesData)

		for _, e := range data {
			if e["id"] != nil {
				references = append(references, core.GetNudgebeeUIReference(nbRequestContext, "investigate", "Investigate - "+e["id"].(string), map[string]string{
					"id":        e["id"].(string),
					"accountId": nbRequestContext.AccountId,
				}, ""))
			}
		}
	}
	// Always include the generic Event Details reference so users can investigate
	// further, even when the query returned no rows.
	references = append(references, core.GetNudgebeeUIReferenceForClusterDetails(nbRequestContext, []string{"events", "summary"}, "Event Details", nil, ""))
	resp.References = references

	return resp, nil
}

func (m EventsExecuteTool) enrichData(data []map[string]any) {
	// 1. Collect session IDs
	sessionIds := []string{}
	parsedEvidences := make([][]events.Evidence, len(data))

	for i, row := range data {
		if row["evidences"] != nil {
			evidenceStr, ok := row["evidences"].(string)
			if ok {
				evs := []events.Evidence{}
				if err := common.UnmarshalJson([]byte(evidenceStr), &evs); err == nil {
					parsedEvidences[i] = evs
					for _, ev := range evs {
						if ev.Type == "markdown" && ev.AdditionalInfo != nil && ev.AdditionalInfo["type"] == "nubi_enricher" && ev.LLMResponse != nil && ev.LLMResponse["session_id"] != nil {
							if sid, ok := ev.LLMResponse["session_id"].(string); ok {
								sessionIds = append(sessionIds, sid)
							}
						}
					}
				}
			}
		}
	}

	// 2. Batch fetch messages
	messagesMap := make(map[string][]map[string]string) // sessionId -> []{message, response}
	if len(sessionIds) > 0 {
		sessionIds = lo.Uniq(sessionIds)
		dbms, err := common.GetDatabaseManager(common.Metastore)
		if err == nil {
			query, args, err := sqlx.In("select lc.session_id, lcm.message, lcm.response from llm_conversation_messages lcm join llm_conversations lc on lc.id = lcm.conversation_id where lc.session_id IN (?) and lcm.status = 'COMPLETED' and lcm.agent_name != 'router'", sessionIds)
			if err == nil {
				query = dbms.Db.Rebind(query)
				rows, err := dbms.Query(query, args...)
				if err == nil {
					defer func() {
						if err := rows.Close(); err != nil {
							slog.Error("events: failed to close rows", "error", err)
						}
					}()
					for rows.Next() {
						var sid, msg, resp string
						if err := rows.Scan(&sid, &msg, &resp); err == nil {
							messagesMap[sid] = append(messagesMap[sid], map[string]string{"message": msg, "response": resp})
						}
					}
				}
			}
		}
	}

	// 3. Process rows
	// Check if any row has parseable evidence — if none do, this is likely an
	// aggregated query (COUNT/GROUP BY) and we should skip evidence processing entirely.
	hasAnyEvidence := false
	for _, evs := range parsedEvidences {
		if len(evs) > 0 {
			hasAnyEvidence = true
			break
		}
	}
	if !hasAnyEvidence {
		// Aggregated query results — remove raw evidence strings, keep other columns intact
		for _, row := range data {
			delete(row, "evidences")
		}
		return
	}

	c := len(data)
	for i, row := range data {
		if c > manifestThreshold {
			// For large result sets, produce lightweight manifests instead of full evidence
			m.buildEvidenceManifest(row, parsedEvidences[i])
		} else {
			m.processRowWithMessages(row, i, c, messagesMap)
		}
	}
}

// manifestThreshold is the number of events above which we produce evidence manifests
// instead of full evidence data. This keeps the response small for the LLM to reason over.
// The LLM can then use get_event_evidence to drill into specific events.
const manifestThreshold = 3

// buildEvidenceManifest replaces the raw evidence JSONB with a lightweight manifest
// listing available evidence types and key insights, without the full raw data.
func (m EventsExecuteTool) buildEvidenceManifest(data map[string]any, evidences []events.Evidence) {
	availableTypes := []string{}
	insights := []string{}
	hasLogs := false
	hasMetrics := false
	hasTraces := false
	hasDeployment := false
	errorSummary := ""

	for _, ev := range evidences {
		// Collect insights from all evidence types
		for _, insight := range ev.Insights {
			if insight.Message != "" {
				insights = append(insights, insight.Message)
			}
		}

		switch ev.Type {
		case "diff":
			hasDeployment = true
			availableTypes = append(availableTypes, "deployment")
		case "gz":
			if strings.Contains(ev.Filename, "log.gz") || strings.Contains(ev.Filename, "txt.gz") {
				hasLogs = true
				availableTypes = append(availableTypes, "logs")
			}
		case "json":
			if ev.AdditionalInfo == nil {
				availableTypes = append(availableTypes, "json_other")
				continue
			}
			actionName, _ := ev.AdditionalInfo["action_name"].(string)
			switch {
			case actionName == "signoz_logs_enricher" || actionName == "logs_enricher" || actionName == "cloud_logs" || actionName == "logs":
				hasLogs = true
				availableTypes = append(availableTypes, "logs")
			case actionName == "pod_details":
				availableTypes = append(availableTypes, "pod_data")
			case strings.Contains(actionName, "api_failure_enricher"):
				availableTypes = append(availableTypes, "api_failures")
			case actionName == "noisy_neighbours":
				availableTypes = append(availableTypes, "noisy_neighbours")
			case actionName == "node_metric":
				hasMetrics = true
				availableTypes = append(availableTypes, "node_metrics")
			case actionName == "pod_metric":
				hasMetrics = true
				availableTypes = append(availableTypes, "pod_metrics")
			case actionName == "container_metric":
				hasMetrics = true
				availableTypes = append(availableTypes, "container_metrics")
			case actionName == "api_traces_enricher":
				hasTraces = true
				availableTypes = append(availableTypes, "traces")
			case actionName == "database_query_response":
				availableTypes = append(availableTypes, "rdbms_query_response")
			case actionName == "pod_script_run_enricher":
				availableTypes = append(availableTypes, "user_actions")
			default:
				title, _ := ev.AdditionalInfo["title"].(string)
				switch title {
				case "Nginx Ingress Controller Request", "Kong Request Count for 5xx Error Metrics":
					availableTypes = append(availableTypes, "api_failures")
				case "Memory Usage Percentage By Nodes", "CPU Usage By Nodes (Core)":
					hasMetrics = true
					availableTypes = append(availableTypes, "node_metrics")
				default:
					availableTypes = append(availableTypes, "json_other")
				}
			}
		case "api_traces_enricher_v2":
			hasTraces = true
			availableTypes = append(availableTypes, "traces")
		case "prometheus":
			hasMetrics = true
			availableTypes = append(availableTypes, "metrics_data")
		case "table":
			evidenceData, ok := ev.Data.(map[string]any)
			if ok {
				tableName, _ := evidenceData["table_name"].(string)
				switch tableName {
				case "*Alert labels*":
					availableTypes = append(availableTypes, "alert_labels")
				case "*Pod events:*":
					availableTypes = append(availableTypes, "pod_events")
				case "*Node events:*":
					availableTypes = append(availableTypes, "node_events")
				case "*Related Events*":
					availableTypes = append(availableTypes, "related_events")
				case "*Job information*":
					availableTypes = append(availableTypes, "job_information")
				default:
					availableTypes = append(availableTypes, "table_other")
				}
			}
		case "markdown":
			availableTypes = append(availableTypes, "markdowns")
		case "service_map":
			availableTypes = append(availableTypes, "service_map")
		case "metric-anomaly":
			hasMetrics = true
			availableTypes = append(availableTypes, "metric_anomaly")
		}
	}

	// Deduplicate available types
	seen := map[string]bool{}
	dedupTypes := []string{}
	for _, t := range availableTypes {
		if !seen[t] {
			seen[t] = true
			dedupTypes = append(dedupTypes, t)
		}
	}

	// Limit insights to first 5 to keep manifest small
	if len(insights) > 5 {
		insights = insights[:5]
	}

	// Extract first error line as summary if we have log insights
	if hasLogs && errorSummary == "" {
		for _, insight := range insights {
			lower := strings.ToLower(insight)
			if strings.Contains(lower, "error") || strings.Contains(lower, "exception") || strings.Contains(lower, "fatal") || strings.Contains(lower, "oom") {
				errorSummary = insight
				break
			}
		}
	}

	manifest := map[string]any{
		"available_evidence_types": dedupTypes,
		"insights":                 insights,
		"has_logs":                 hasLogs,
		"has_metrics":              hasMetrics,
		"has_traces":               hasTraces,
		"has_deployment":           hasDeployment,
		"evidence_count":           len(evidences),
	}
	if errorSummary != "" {
		manifest["error_summary"] = errorSummary
	}

	data["evidences"] = manifest
}
