package integrations

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"nudgebee/services/common"
	"nudgebee/services/event"
	"nudgebee/services/integrations/core"
	"nudgebee/services/security"
	"strconv"
	"strings"
	"time"
)

const (
	NewRelicConfigApiKey      = "api_key"
	NewRelicConfigAccountId   = "nr_account_id"
	NewRelicConfigRegion      = "region"
	NewRelicConfigKGSource    = "kg_source"
	NewRelicKGSourceNRQL      = "nrql"
	NewRelicKGSourceNerdGraph = "nerdgraph"
	NewRelicRegionUS          = "us"
	NewRelicRegionEU          = "eu"
	NewRelicAPIEndpointUS     = "api.newrelic.com"
	NewRelicAPIEndpointEU     = "api.eu.newrelic.com"
	IntegrationNewRelic       = "newrelic"
)

func init() {
	core.RegisterIntegration(NewRelic{})
}

type NewRelic struct{}

func (m NewRelic) Name() string {
	return IntegrationNewRelic
}

func (m NewRelic) Category() core.IntegrationCategory {
	return core.IntegrationCategoryObservabilityPlatform
}

func (m NewRelic) ConfigSchema() core.IntegrationSchema {
	return core.IntegrationSchema{
		Type:     core.ToolSchemaTypeObject,
		Testable: true,
		Required: []string{NewRelicConfigApiKey, NewRelicConfigAccountId, NewRelicConfigRegion},
		Properties: map[string]core.IntegrationSchemaProperty{
			core.IntegrationConfigName: {
				Type:             core.ToolSchemaTypeString,
				Description:      "Name for New Relic Integration",
				Default:          "",
				AutoGenerateFunc: "",
				Priority:         100,
			},
			core.AccountId: {
				Type:             core.ToolSchemaTypeArray,
				Description:      "Select Accounts",
				Default:          nil,
				AutoGenerateFunc: "listAccounts",
				Priority:         95,
			},
			NewRelicConfigApiKey: {
				Type:        core.ToolSchemaTypeString,
				Description: "New Relic User API Key",
				IsEncrypted: true,
				Priority:    90,
				IsTestable:  true,
			},
			NewRelicConfigAccountId: {
				Type:        core.ToolSchemaTypeString,
				Description: "New Relic Account ID",
				Priority:    85,
				IsTestable:  true,
			},
			NewRelicConfigRegion: {
				Type:        core.ToolSchemaTypeString,
				Description: "New Relic Region (us or eu)",
				Enum:        []any{NewRelicRegionUS, NewRelicRegionEU},
				Default:     NewRelicRegionUS,
				Priority:    80,
				IsTestable:  true,
			},
			NewRelicConfigKGSource: {
				Type:        core.ToolSchemaTypeString,
				Description: "Knowledge graph fetch strategy: 'nrql' (default — Span aggregation) or 'nerdgraph' (entity relatedEntities; richer for OTel/eBPF tenants, falls back to NRQL on empty)",
				Enum:        []any{NewRelicKGSourceNRQL, NewRelicKGSourceNerdGraph},
				Default:     NewRelicKGSourceNRQL,
				Priority:    25,
			},
			core.DefaultLogProvider: {
				Type:             core.ToolSchemaTypeBoolean,
				Description:      "Make New Relic default Log Provider",
				Default:          false,
				AutoGenerateFunc: "",
				Priority:         30,
			},
			core.DefaultMetricsProvider: {
				Type:             core.ToolSchemaTypeBoolean,
				Description:      "Make New Relic default Metrics Provider",
				Default:          false,
				AutoGenerateFunc: "",
				Priority:         20,
			},
			core.DefaultTraceProvider: {
				Type:             core.ToolSchemaTypeBoolean,
				Description:      "Make New Relic default Trace Provider",
				Default:          false,
				AutoGenerateFunc: "",
				Priority:         10,
			},
		},
	}
}

func (m *NewRelic) ValidateNewRelicConfig(apiKey, nrAccountId, region string) error {
	if apiKey == "" {
		return errors.New("api key must not be empty")
	}
	if nrAccountId == "" {
		return errors.New("account ID must not be empty")
	}
	if region == "" {
		region = NewRelicRegionUS
	}
	if region != NewRelicRegionUS && region != NewRelicRegionEU {
		return fmt.Errorf("invalid region: %s, must be 'us' or 'eu'", region)
	}

	// Test the API key by executing a simple NRQL query
	testQuery := "SELECT count(*) FROM Transaction SINCE 1 minute ago"
	_, err := ExecuteNRQL(apiKey, nrAccountId, region, testQuery)
	if err != nil {
		return fmt.Errorf("failed to validate New Relic credentials: %w", err)
	}
	return nil
}

func (m NewRelic) ValidateConfig(securityContext *security.SecurityContext, integrationConfig []core.IntegrationConfigValue, accountId string) []error {
	apiKey := ""
	nrAccountId := ""
	region := NewRelicRegionUS
	var validationErrors []error

	for _, config := range integrationConfig {
		switch config.Name {
		case NewRelicConfigApiKey:
			apiKey = config.Value
			if config.IsEncrypted {
				var err error
				apiKey, err = common.Decrypt(config.Value)
				if err != nil {
					return []error{fmt.Errorf("new relic config validation failed: %w", err)}
				}
			}
		case NewRelicConfigAccountId:
			nrAccountId = config.Value
		case NewRelicConfigRegion:
			region = config.Value
		}
	}

	if err := m.ValidateNewRelicConfig(apiKey, nrAccountId, region); err != nil {
		validationErrors = []error{fmt.Errorf("new relic config validation failed: %w", err)}
	}
	return validationErrors
}

// GetNewRelicConfigs retrieves and decrypts New Relic configuration for an account
func GetNewRelicConfigs(sc *security.RequestContext, accountId string) (string, string, string, error) {
	apiKey := ""
	nrAccountId := ""
	region := NewRelicRegionUS

	newRelicIntegrations, err := core.ListIntegrationConfigs(sc, accountId, IntegrationNewRelic)
	if err != nil {
		return apiKey, nrAccountId, region, fmt.Errorf("failed to list New Relic integration configs: %w", err)
	}
	if len(newRelicIntegrations) == 0 {
		return apiKey, nrAccountId, region, fmt.Errorf("new Relic integration not found for account: %s", accountId)
	}
	newRelicIntegration := newRelicIntegrations[0]
	for _, config := range newRelicIntegration.Configs {
		switch config.Name {
		case NewRelicConfigApiKey:
			apiKey = config.Value
			if config.IsEncrypted {
				var err error
				apiKey, err = common.Decrypt(config.Value)
				if err != nil {
					return apiKey, nrAccountId, region, fmt.Errorf("failed to decrypt New Relic API key: %w", err)
				}
			}
		case NewRelicConfigAccountId:
			nrAccountId = config.Value
		case NewRelicConfigRegion:
			region = config.Value
		}
	}

	return apiKey, nrAccountId, region, nil
}

// GetNewRelicKGSource returns the configured kg_source ("nrql" | "nerdgraph"),
// defaulting to "nrql" when the integration isn't present, the field isn't set,
// or the stored value is empty/unrecognized. Errors are returned only for hard
// integration-config lookup failures; missing config is not an error.
func GetNewRelicKGSource(sc *security.RequestContext, accountId string) (string, error) {
	configs, err := core.ListIntegrationConfigs(sc, accountId, IntegrationNewRelic)
	if err != nil {
		return NewRelicKGSourceNRQL, fmt.Errorf("failed to list New Relic integration configs: %w", err)
	}
	if len(configs) == 0 {
		return NewRelicKGSourceNRQL, nil
	}
	for _, cfg := range configs[0].Configs {
		if cfg.Name != NewRelicConfigKGSource {
			continue
		}
		switch cfg.Value {
		case NewRelicKGSourceNerdGraph:
			return NewRelicKGSourceNerdGraph, nil
		case NewRelicKGSourceNRQL, "":
			return NewRelicKGSourceNRQL, nil
		default:
			// Unknown value — fall back to default rather than erroring,
			// since the schema enum should already prevent this at write time.
			return NewRelicKGSourceNRQL, nil
		}
	}
	return NewRelicKGSourceNRQL, nil
}

// GetNewRelicEndpoint returns the appropriate API endpoint based on region
func GetNewRelicEndpoint(region string) string {
	if region == NewRelicRegionEU {
		return NewRelicAPIEndpointEU
	}
	return NewRelicAPIEndpointUS
}

// NRQLResponse represents the response structure from New Relic GraphQL API
type NRQLResponse struct {
	Data struct {
		Actor struct {
			Account struct {
				NRQL struct {
					Results []map[string]any `json:"results"`
				} `json:"nrql"`
			} `json:"account"`
		} `json:"actor"`
	} `json:"data"`
	Errors []struct {
		Message    string `json:"message"`
		Extensions struct {
			ErrorClass string `json:"errorClass"`
		} `json:"extensions"`
	} `json:"errors"`
}

// NewRelicNrAiIssue represents a complete issue from NerdGraph aiIssues query
type NewRelicNrAiIssue struct {
	IssueId           interface{} `json:"issueId"`
	Title             interface{} `json:"title"`
	State             interface{} `json:"state"`
	Priority          interface{} `json:"priority"`
	CreatedAt         interface{} `json:"createdAt"`
	ClosedAt          interface{} `json:"closedAt"`
	UpdatedAt         interface{} `json:"updatedAt"`
	ActivatedAt       interface{} `json:"activatedAt"`
	AcknowledgedAt    interface{} `json:"acknowledgedAt"`
	Sources           interface{} `json:"sources"`
	ConditionName     interface{} `json:"conditionName"`
	PolicyName        interface{} `json:"policyName"`
	ConditionFamilyId interface{} `json:"conditionFamilyId"`
	EntityGuids       interface{} `json:"entityGuids"`
	EntityNames       interface{} `json:"entityNames"`
	TotalIncidents    interface{} `json:"totalIncidents"`
}

// NerdGraphIssueResponse represents the response structure from NerdGraph aiIssues query
type NerdGraphIssueResponse struct {
	Data struct {
		Actor struct {
			Account struct {
				AiIssues struct {
					Issues struct {
						Issues []NewRelicNrAiIssue `json:"issues"`
					} `json:"issues"`
				} `json:"aiIssues"`
			} `json:"account"`
		} `json:"actor"`
	} `json:"data"`
	Errors []struct {
		Message    string `json:"message"`
		Extensions struct {
			ErrorClass string `json:"errorClass"`
		} `json:"extensions"`
	} `json:"errors"`
}

// ExecuteNRQL executes an NRQL query against New Relic's GraphQL API
func ExecuteNRQL(apiKey, nrAccountId, region, query string) ([]map[string]any, error) {
	endpoint := GetNewRelicEndpoint(region)
	url := fmt.Sprintf("https://%s/graphql", endpoint)

	// Build GraphQL query
	graphqlQuery := buildNRQLGraphQL(nrAccountId, query)

	requestBody := map[string]string{
		"query": graphqlQuery,
	}

	headers := map[string]string{
		"Content-Type": "application/json",
		"API-Key":      apiKey,
	}

	resp, err := common.HttpPost(url, common.HttpWithHeaders(headers), common.HttpWithJsonBody(requestBody))
	if err != nil {
		return nil, fmt.Errorf("failed to make request to New Relic GraphQL API: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read New Relic response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("new Relic API returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var nrqlResponse NRQLResponse
	if err := json.Unmarshal(respBody, &nrqlResponse); err != nil {
		return nil, fmt.Errorf("failed to unmarshal New Relic response: %w", err)
	}

	if len(nrqlResponse.Errors) > 0 {
		return nil, fmt.Errorf("new Relic API error: %s", nrqlResponse.Errors[0].Message)
	}

	return nrqlResponse.Data.Actor.Account.NRQL.Results, nil
}

// buildNRQLGraphQL constructs a GraphQL query for NRQL
func buildNRQLGraphQL(nrAccountId, nrqlQuery string) string {
	// Validate nrAccountId is numeric to prevent GraphQL injection
	if _, err := strconv.ParseInt(nrAccountId, 10, 64); err != nil {
		// If validation fails, return empty query
		return ""
	}
	// Escape double quotes in the NRQL query
	escapedQuery := strings.ReplaceAll(nrqlQuery, `"`, `\"`)
	return fmt.Sprintf(`{
		actor {
			account(id: %s) {
				nrql(query: "%s") {
					results
				}
			}
		}
	}`, nrAccountId, escapedQuery)
}

// NewRelicMetricPoint represents a single timeseries data point from New Relic Metric table
type NewRelicMetricPoint struct {
	BeginTimeSeconds int64   `json:"beginTimeSeconds"`
	EndTimeSeconds   int64   `json:"endTimeSeconds"`
	Throughput       float64 `json:"throughput"`
	ResponseTimeMs   float64 `json:"responseTime_ms"`
	ErrorRate        float64 `json:"errorRate"`
}

// getNewRelicMetrics fetches APM metrics (throughput, response time, error rate) from New Relic
func getNewRelicMetrics(sc *security.RequestContext, apiKey, nrAccountId, region, serviceName string, fromTs, toTs int64) ([]NewRelicMetricPoint, event.EventEvidence, error) {
	escapedName := strings.ReplaceAll(serviceName, "'", "''")
	nrqlQuery := fmt.Sprintf(
		"SELECT rate(count(apm.service.transaction.duration), 1 minute) AS throughput,"+
			" average(apm.service.transaction.duration) * 1000 AS responseTime_ms,"+
			" rate(count(apm.service.error.count), 1 minute) AS errorRate"+
			" FROM Metric WHERE service.name = '%s' SINCE %d UNTIL %d TIMESERIES 5 minutes LIMIT 1000",
		escapedName, fromTs, toTs,
	)

	results, err := ExecuteNRQL(apiKey, nrAccountId, region, nrqlQuery)
	if err != nil {
		return nil, event.EventEvidence{}, fmt.Errorf("failed to fetch New Relic metrics: %w", err)
	}

	points := make([]NewRelicMetricPoint, 0, len(results))
	for _, r := range results {
		p := NewRelicMetricPoint{}
		if v, ok := r["beginTimeSeconds"].(float64); ok {
			p.BeginTimeSeconds = int64(v)
		}
		if v, ok := r["endTimeSeconds"].(float64); ok {
			p.EndTimeSeconds = int64(v)
		}
		if v, ok := r["throughput"].(float64); ok {
			p.Throughput = v
		}
		if v, ok := r["responseTime_ms"].(float64); ok {
			p.ResponseTimeMs = v
		}
		if v, ok := r["errorRate"].(float64); ok {
			p.ErrorRate = v
		}
		points = append(points, p)
	}

	evidence := event.EventEvidence{
		Type: "json",
		Data: map[string]any{
			"name": "New Relic Metrics",
			"data": points,
		},
		Insight: []event.EventEvidenceInsight{
			{
				Message:  fmt.Sprintf("Fetched %d metric datapoints from New Relic for service '%s'", len(points), serviceName),
				Severity: "info",
			},
		},
		AdditionalInfo: map[string]any{
			"action_name":            "metrics",
			"actual_action_name":     "metrics",
			"action_title":           "New Relic Metrics",
			"conditional_expression": "",
		},
	}

	return points, evidence, nil
}

// NewRelicLog represents a log entry from New Relic
type NewRelicLog struct {
	Timestamp   int64             `json:"timestamp"`
	Message     string            `json:"message"`
	LogType     string            `json:"logtype"`
	Hostname    string            `json:"hostname"`
	ServiceName string            `json:"service.name"`
	Level       string            `json:"level"`
	Attributes  map[string]string `json:"attributes"`
}

// NewRelicSpan represents a span/trace entry from New Relic.
// JSON tags use snake_case names that match what the TracesCard UI component expects.
type NewRelicSpan struct {
	TraceId      string  `json:"trace_id"`
	SpanId       string  `json:"span_id"`
	ParentId     string  `json:"parent_span_id"`
	Name         string  `json:"span_name"`
	ServiceName  string  `json:"service_name"`
	DurationMs   float64 `json:"duration_ns"` // value is in ms; field name aligns with UI expectation
	Timestamp    int64   `json:"timestamp"`
	SpanKind     string  `json:"span_kind"`
	StatusCode   string  `json:"status_code"`
	ErrorMessage string  `json:"error_message"`
}

// NewRelicIncident represents an incident/alert from New Relic
type NewRelicIncident struct {
	IncidentId    string `json:"incidentId"`
	Title         string `json:"title"`
	Priority      string `json:"priority"`
	State         string `json:"state"`
	ConditionName string `json:"conditionName"`
	PolicyName    string `json:"policyName"`
	OpenTime      int64  `json:"openTime"`
	CloseTime     int64  `json:"closeTime"`
	EntityGuid    string `json:"entityGuid"`
	EntityName    string `json:"entityName"`
	AccountId     string `json:"accountId"`
	TargetName    string `json:"targetName"`
	ViolationUrl  string `json:"violationUrl"`
}

// getNewRelicLogs fetches logs from New Relic using NRQL
func getNewRelicLogs(sc *security.RequestContext, apiKey, nrAccountId, region, query string, fromTs, toTs int64) ([]NewRelicLog, event.EventEvidence, error) {
	// Build NRQL query for logs.
	// Exclude eBPF entity-metadata records (recordType = 'eBPF-Entity-Metadata') which are
	// infrastructure metadata with no message/severity content, and filter to rows that
	// actually have a non-empty message so the UI doesn't show blank log entries.
	nrqlQuery := query
	if !strings.Contains(strings.ToUpper(query), "FROM LOG") {
		nrqlQuery = fmt.Sprintf(
			"SELECT * FROM Log WHERE (%s) AND message IS NOT NULL AND message != '' AND recordType != 'eBPF-Entity-Metadata' SINCE %d UNTIL %d LIMIT 1000",
			query, fromTs, toTs,
		)
	}

	results, err := ExecuteNRQL(apiKey, nrAccountId, region, nrqlQuery)
	if err != nil {
		return nil, event.EventEvidence{}, fmt.Errorf("failed to fetch New Relic logs: %w", err)
	}

	logs := make([]NewRelicLog, 0, len(results))
	for _, r := range results {
		log := NewRelicLog{
			Attributes: make(map[string]string),
		}
		if ts, ok := r["timestamp"].(float64); ok {
			log.Timestamp = int64(ts)
		}
		if msg, ok := r["message"].(string); ok {
			log.Message = msg
		}
		if logType, ok := r["logtype"].(string); ok {
			log.LogType = logType
		}
		if host, ok := r["hostname"].(string); ok {
			log.Hostname = host
		}
		if svc, ok := r["service.name"].(string); ok {
			log.ServiceName = svc
		}
		if level, ok := r["level"].(string); ok {
			log.Level = level
		}
		// Store other attributes
		for k, v := range r {
			if str, ok := v.(string); ok {
				log.Attributes[k] = str
			}
		}
		logs = append(logs, log)
	}

	// Transform logs to the format expected by the SignozDatadogLogCard UI component:
	// each item must have {timestamp (ISO string), message, labels (object), severity}.
	type uiLogItem struct {
		Timestamp string         `json:"timestamp"`
		Message   string         `json:"message"`
		Labels    map[string]any `json:"labels"`
		Severity  string         `json:"severity"`
	}
	uiLogs := make([]uiLogItem, 0, len(logs))
	for _, l := range logs {
		labels := make(map[string]any, len(l.Attributes))
		for k, v := range l.Attributes {
			labels[k] = v
		}
		if l.Hostname != "" {
			labels["hostname"] = l.Hostname
		}
		if l.LogType != "" {
			labels["logtype"] = l.LogType
		}
		ts := ""
		if l.Timestamp > 0 {
			ts = time.UnixMilli(l.Timestamp).UTC().Format(time.RFC3339Nano)
		}
		uiLogs = append(uiLogs, uiLogItem{
			Timestamp: ts,
			Message:   l.Message,
			Labels:    labels,
			Severity:  l.Level,
		})
	}

	if len(uiLogs) == 0 {
		return logs, event.EventEvidence{}, nil
	}

	evidence := event.EventEvidence{
		Type: "json",
		Data: map[string]any{
			"name": "New Relic Logs",
			"data": uiLogs,
		},
		Insight: []event.EventEvidenceInsight{
			{
				Message:  fmt.Sprintf("Fetched %d logs from New Relic", len(logs)),
				Severity: "info",
			},
		},
		AdditionalInfo: map[string]any{
			"action_name":            "logs",
			"actual_action_name":     "logs",
			"action_title":           "New Relic Logs",
			"conditional_expression": "",
		},
	}

	return logs, evidence, nil
}

// getNewRelicTraces fetches traces/spans from New Relic using NRQL
func getNewRelicTraces(sc *security.RequestContext, apiKey, nrAccountId, region, query string, fromTs, toTs int64) ([]NewRelicSpan, event.EventEvidence, error) {
	// Build NRQL query for spans
	nrqlQuery := query
	if !strings.Contains(strings.ToUpper(query), "FROM SPAN") {
		nrqlQuery = fmt.Sprintf("SELECT * FROM Span WHERE %s SINCE %d UNTIL %d LIMIT 1000", query, fromTs, toTs)
	}

	results, err := ExecuteNRQL(apiKey, nrAccountId, region, nrqlQuery)
	if err != nil {
		return nil, event.EventEvidence{}, fmt.Errorf("failed to fetch New Relic traces: %w", err)
	}

	spans := make([]NewRelicSpan, 0, len(results))
	for _, r := range results {
		span := NewRelicSpan{}
		if traceId, ok := r["trace.id"].(string); ok {
			span.TraceId = traceId
		}
		if spanId, ok := r["span.id"].(string); ok {
			span.SpanId = spanId
		}
		if parentId, ok := r["parent.id"].(string); ok {
			span.ParentId = parentId
		}
		if name, ok := r["name"].(string); ok {
			span.Name = name
		}
		if svc, ok := r["service.name"].(string); ok {
			span.ServiceName = svc
		}
		if dur, ok := r["duration.ms"].(float64); ok {
			span.DurationMs = dur
		}
		if ts, ok := r["timestamp"].(float64); ok {
			span.Timestamp = int64(ts)
		}
		if kind, ok := r["span.kind"].(string); ok {
			span.SpanKind = kind
		}
		if status, ok := r["otel.status_code"].(string); ok {
			span.StatusCode = status
		}
		if errMsg, ok := r["error.message"].(string); ok {
			span.ErrorMessage = errMsg
		}
		spans = append(spans, span)
	}

	if len(spans) == 0 {
		return spans, event.EventEvidence{}, nil
	}

	evidence := event.EventEvidence{
		Type: "json",
		Data: map[string]any{
			"name": "New Relic Traces",
			"data": spans,
		},
		Insight: []event.EventEvidenceInsight{
			{
				Message:  fmt.Sprintf("Fetched %d spans from New Relic", len(spans)),
				Severity: "info",
			},
		},
		AdditionalInfo: map[string]any{
			"action_name":            "api_traces_enricher_v2",
			"actual_action_name":     "api_traces_enricher_v2",
			"action_title":           "New Relic Traces",
			"conditional_expression": "",
		},
	}

	return spans, evidence, nil
}

// getNewRelicIncidents fetches incidents/alerts from New Relic using NRQL
//
//nolint:unused
func getNewRelicIncidents(sc *security.RequestContext, apiKey, nrAccountId, region string, fromTs, toTs int64) ([]NewRelicIncident, event.EventEvidence, error) {
	// Query NrAiIncident for incidents
	nrqlQuery := fmt.Sprintf("SELECT * FROM NrAiIncident SINCE %d UNTIL %d LIMIT 100", fromTs, toTs)

	results, err := ExecuteNRQL(apiKey, nrAccountId, region, nrqlQuery)
	if err != nil {
		return nil, event.EventEvidence{}, fmt.Errorf("failed to fetch New Relic incidents: %w", err)
	}

	incidents := make([]NewRelicIncident, 0, len(results))
	for _, r := range results {
		incident := NewRelicIncident{}
		if id, ok := r["incidentId"].(string); ok {
			incident.IncidentId = id
		}
		if title, ok := r["title"].(string); ok {
			incident.Title = title
		}
		if priority, ok := r["priority"].(string); ok {
			incident.Priority = priority
		}
		if state, ok := r["state"].(string); ok {
			incident.State = state
		}
		if cond, ok := r["conditionName"].(string); ok {
			incident.ConditionName = cond
		}
		if policy, ok := r["policyName"].(string); ok {
			incident.PolicyName = policy
		}
		if openTime, ok := r["openTime"].(float64); ok {
			incident.OpenTime = int64(openTime)
		}
		if closeTime, ok := r["closeTime"].(float64); ok {
			incident.CloseTime = int64(closeTime)
		}
		if guid, ok := r["entityGuid"].(string); ok {
			incident.EntityGuid = guid
		}
		if name, ok := r["entityName"].(string); ok {
			incident.EntityName = name
		}
		if accId, ok := r["accountId"].(string); ok {
			incident.AccountId = accId
		}
		if target, ok := r["targetName"].(string); ok {
			incident.TargetName = target
		}
		if url, ok := r["violationUrl"].(string); ok {
			incident.ViolationUrl = url
		}
		incidents = append(incidents, incident)
	}

	evidence := event.EventEvidence{
		Type: "json",
		Data: map[string]any{
			"name": "New Relic Incidents",
			"data": incidents,
		},
		Insight: []event.EventEvidenceInsight{
			{
				Message:  fmt.Sprintf("Fetched %d incidents from New Relic", len(incidents)),
				Severity: "info",
			},
		},
		AdditionalInfo: map[string]any{
			"action_name":            "newrelic_incidents",
			"actual_action_name":     "newrelic_incidents",
			"action_title":           "New Relic Incidents",
			"conditional_expression": "",
		},
	}

	return incidents, evidence, nil
}

// getNewRelicEntityDetails fetches entity details using entity GUID
func getNewRelicEntityDetails(apiKey, nrAccountId, region, entityGuid string) (map[string]any, error) {
	graphqlQuery := fmt.Sprintf(`{
		actor {
			entity(guid: "%s") {
				name
				type
				domain
				entityType
				tags {
					key
					values
				}
				... on ApmApplicationEntity {
					apmSummary {
						errorRate
						responseTimeAverage
						throughput
					}
				}
				... on InfrastructureHostEntity {
					hostSummary {
						cpuUtilizationPercent
						memoryUsedPercent
						diskUsedPercent
					}
				}
			}
		}
	}`, entityGuid)

	endpoint := GetNewRelicEndpoint(region)
	url := fmt.Sprintf("https://%s/graphql", endpoint)

	requestBody := map[string]string{
		"query": graphqlQuery,
	}

	headers := map[string]string{
		"Content-Type": "application/json",
		"API-Key":      apiKey,
	}

	resp, err := common.HttpPost(url, common.HttpWithHeaders(headers), common.HttpWithJsonBody(requestBody))
	if err != nil {
		return nil, fmt.Errorf("failed to make request to New Relic GraphQL API: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read New Relic response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("new Relic API returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var result map[string]any
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to unmarshal New Relic entity response: %w", err)
	}

	return result, nil
}

// getNewRelicIssueDetails fetches issue details from NewRelic NerdGraph API.
// If createdAt (epoch ms) is provided, the time window starts from that timestamp;
// otherwise it defaults to 7 days ago.
func getNewRelicIssueDetails(apiKey, nrAccountId, region, issueId string, createdAt *int64) (*NewRelicNrAiIssue, error) {
	// Validate nrAccountId is numeric to prevent GraphQL injection
	if _, err := strconv.ParseInt(nrAccountId, 10, 64); err != nil {
		return nil, fmt.Errorf("invalid New Relic account ID: %s", nrAccountId)
	}

	endpoint := GetNewRelicEndpoint(region)
	url := fmt.Sprintf("https://%s/graphql", endpoint)

	// Build GraphQL query for aiIssues.issues
	// Use filter.ids to query directly and a timeWindow (required by the API —
	// without it the API defaults to a narrow window and returns empty results).
	var startTime, endTime int64
	if createdAt != nil {
		t := time.UnixMilli(*createdAt)
		startTime = t.Add(-30 * time.Minute).UnixMilli()
		endTime = t.Add(30 * time.Minute).UnixMilli()
	} else {
		startTime = time.Now().Add(-7 * 24 * time.Hour).UnixMilli()
		endTime = time.Now().UnixMilli()
	}
	graphqlQuery := fmt.Sprintf(`{
		actor {
			account(id: %s) {
				aiIssues {
					issues(
						filter: {ids: ["%s"]}
						timeWindow: {startTime: %d, endTime: %d}
					) {
						issues {
							issueId
							title
							state
							priority
							createdAt
							closedAt
							updatedAt
							activatedAt
							acknowledgedAt
							sources
							conditionName
							policyName
							conditionFamilyId
							entityGuids
							entityNames
							totalIncidents
						}
					}
				}
			}
		}
	}`, nrAccountId, issueId, startTime, endTime)

	requestBody := map[string]string{
		"query": graphqlQuery,
	}

	headers := map[string]string{
		"Content-Type": "application/json",
		"API-Key":      apiKey,
	}

	resp, err := common.HttpPost(url, common.HttpWithHeaders(headers), common.HttpWithJsonBody(requestBody))
	if err != nil {
		return nil, fmt.Errorf("failed to make request to New Relic NerdGraph API: %w", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			fmt.Printf("error closing response body: %v\n", err)
		}
	}()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read New Relic response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("new Relic API returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var nerdGraphResponse NerdGraphIssueResponse
	if err := json.Unmarshal(respBody, &nerdGraphResponse); err != nil {
		return nil, fmt.Errorf("failed to unmarshal New Relic response: %w", err)
	}

	if len(nerdGraphResponse.Errors) > 0 {
		return nil, fmt.Errorf("new Relic API error: %s", nerdGraphResponse.Errors[0].Message)
	}

	issues := nerdGraphResponse.Data.Actor.Account.AiIssues.Issues.Issues

	// Filter to find the specific issue by ID
	for i := range issues {
		issueIdVal := fmt.Sprintf("%v", issues[i].IssueId)
		if issueIdVal == issueId {
			return &issues[i], nil
		}
	}

	return nil, fmt.Errorf("issue not found: %s", issueId)
}

// NewRelicQueryParams holds parameters for New Relic queries
type NewRelicQueryParams struct {
	LogQuery    string
	TraceQuery  string
	LogFromTs   int64
	LogToTs     int64
	TraceFromTs int64
	TraceToTs   int64
}

// buildTimeRangeNRQL constructs SINCE/UNTIL clause from timestamps
//
//nolint:unused
func buildTimeRangeNRQL(fromTs, toTs int64) string {
	// Convert milliseconds to seconds if needed
	if fromTs > 1e12 {
		fromTs = fromTs / 1000
	}
	if toTs > 1e12 {
		toTs = toTs / 1000
	}
	return fmt.Sprintf("SINCE %d UNTIL %d", fromTs, toTs)
}

// formatNRQLTimestamp formats a time.Time for NRQL
//
//nolint:unused
func formatNRQLTimestamp(t time.Time) int64 {
	return t.UnixMilli()
}
