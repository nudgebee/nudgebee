package integrations

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"nudgebee/services/common"
	"nudgebee/services/event"
	"nudgebee/services/eventrule"
	"nudgebee/services/integrations/core"
	"nudgebee/services/internal/database"
	"nudgebee/services/llm"
	"nudgebee/services/security"
	"nudgebee/services/tenant"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/lib/pq"
)

func init() {
	core.RegisterIntegration(DatadogWebhook{})
}

type DatadogWebhook struct {
}

var datadogIssueIdRegex = regexp.MustCompile(`{issue.id:(\S+)}`)
var datadogTitleRegex = regexp.MustCompile(`^(?:\[(P\d+)\]\s)?\[((?:Re-)?Triggered|Recovered|Alert|Warn|No Data)(?: on [^\]]+)?\]\s(.*)`)
var datadogMonitorURLRegex = regexp.MustCompile(`https(://[a-zA-Z0-9.-]+/monitors/(\d+)[^)]*)`)
var datadogMonitorIDRegex = regexp.MustCompile(`monitor_id=(\d+)`)
var datadogTagsRegex = regexp.MustCompile(`tags=([^&)]+)`)
var datadogMonitorPathRegex = regexp.MustCompile(`/monitors/(\d+)`)

const IntegrationDatadogWebhook = "datadog_webhook"

// HistoricalIncident represents a past Datadog incident title mapped to a service
type HistoricalIncident struct {
	Title   string  `json:"title"`
	Service *string `json:"service"`
}

// Deprecated: use GetSubjectMappingsForPrompt instead.

const TenantAttrHistoricalIncidentsKey = "DATADOG_INCIDENT_TITLE_SERVICE_MAPPING"

func (m DatadogWebhook) Name() string {
	return IntegrationDatadogWebhook
}

type OutputMetricQuery struct {
	Results []QueryResult `json:"results"`
}

type QueryResult struct {
	QueryKey string   `json:"query_key"`
	Payload  []Result `json:"payload"`
}

type Result struct {
	Metric     map[string]string `json:"metric"`
	Timestamps []int64           `json:"timestamps"`
	Values     []float64         `json:"values"`
}

type DatadogResponse struct {
	Status  string          `json:"status"`
	ResType string          `json:"res_type"`
	Series  []SeriesItem    `json:"series"`
	Values  [][]interface{} `json:"values"`
	Times   []int64         `json:"times"`
	GroupBy []string        `json:"group_by"`
}

type SeriesItem struct {
	Unit        []*UnitItem `json:"unit"`
	QueryIdx    int         `json:"query_index"`
	Aggr        string      `json:"aggr"`
	Metric      string      `json:"metric"`
	TagSet      []string    `json:"tag_set"`
	Expression  string      `json:"expression"`
	Scope       string      `json:"scope"`
	Interval    int         `json:"interval"`
	Start       int64       `json:"start"`
	End         int64       `json:"end"`
	PointList   [][]float64 `json:"pointlist"`
	DisplayName string      `json:"display_name"`
}

type UnitItem struct {
	Family      string  `json:"family"`
	ID          int     `json:"id"`
	Name        string  `json:"name"`
	ShortName   string  `json:"short_name"`
	Plural      string  `json:"plural"`
	ScaleFactor float64 `json:"scale_factor"`
}

func (m DatadogWebhook) Category() core.IntegrationCategory {
	return core.IntegrationCategoryIncidentWebhook
}

func (m DatadogWebhook) ConfigSchema() core.IntegrationSchema {
	return core.IntegrationSchema{
		Type:     core.ToolSchemaTypeObject,
		Required: []string{},
		Properties: map[string]core.IntegrationSchemaProperty{
			"integration_config_name": {
				Type:        core.ToolSchemaTypeString,
				Description: "Name of Datadog Webhook",
				Default:     "",
			},
			"account_id": {
				Type:             core.ToolSchemaTypeArray,
				Description:      "Select Account",
				Default:          "",
				AutoGenerateFunc: "listAccounts",
			},
			"token": {
				Type:    core.ToolSchemaTypeString,
				Default: "",
			},
		},
	}
}

func (m DatadogWebhook) ValidateConfig(securityContext *security.SecurityContext, integrationConfig []core.IntegrationConfigValue, accountId string) []error {
	return []error{}
}

func (m DatadogWebhook) MergeEventWebhooks(sc *security.RequestContext, previous core.EventIncomingWebhook, new core.EventIncomingWebhook) (core.EventIncomingWebhook, error) {
	return new, nil
}

func getDatadogEvent(sc *security.RequestContext, apiKey, appKey, site, eventId string) (map[string]any, event.EventEvidence, error) {
	url := fmt.Sprintf("https://%s/api/v1/events/%s", site, eventId)

	sc.GetLogger().Info("datadog_webhook.getDatadogEvent event url", "url", url)
	headers := map[string]string{
		"Content-Type":       "application/json",
		"DD-API-KEY":         apiKey,
		"DD-APPLICATION-KEY": appKey,
	}

	body, err := common.HttpGet(url, common.HttpWithHeaders(headers))
	if err != nil {
		return nil, event.EventEvidence{}, fmt.Errorf("failed to make request to datadog events api: %w", err)
	}

	bodyBytes, err := io.ReadAll(body.Body)
	if err != nil {
		return nil, event.EventEvidence{}, fmt.Errorf("failed to read datadog event details body: %w", err)
	}

	var eventDetails map[string]any
	if err := common.UnmarshalJson(bodyBytes, &eventDetails); err != nil {
		return nil, event.EventEvidence{}, fmt.Errorf("failed to unmarshal datadog event details: %w", err)
	}

	sc.GetLogger().Info("datadog_webhook.getDatadogEvent", "eventDetails", eventDetails)
	evidence := event.EventEvidence{}
	if eventDetails["event"] != nil {
		if event1, ok := eventDetails["event"].(map[string]any); ok {
			evidence.Data = map[string]any{
				"name": "Datadog Event",
				"data": event1,
			}
			evidence.Type = "json"
			evidence.Insight = []event.EventEvidenceInsight{
				{
					Message:  "Datadog Event Id - " + eventId,
					Severity: "info",
				},
			}
			evidence.AdditionalInfo = map[string]any{
				"action_name":            "datadog_event",
				"actual_action_name":     "datadog_event",
				"action_title":           "Datadog Event",
				"conditional_expression": "",
			}
		}
	}

	if eventDetails["errors"] != nil {
		if errs, ok := eventDetails["errors"].([]interface{}); ok && len(errs) > 0 {
			errMsg := fmt.Sprintf("%v", errs[0])
			return map[string]any{}, evidence, fmt.Errorf("%s", errMsg)
		}
	}
	return eventDetails, evidence, nil
}

func getDatadogIncidentDetails(sc *security.RequestContext, apiKey, appKey, site, incidentId string) (map[string]any, event.EventEvidence, error) {
	url := fmt.Sprintf("https://%s/api/v2/incidents/%s", site, incidentId)

	sc.GetLogger().Info("datadog_webhook.getDatadogIncidentDetails incident url", "url", url)
	headers := map[string]string{
		"Content-Type":       "application/json",
		"DD-API-KEY":         apiKey,
		"DD-APPLICATION-KEY": appKey,
	}

	body, err := common.HttpGet(url, common.HttpWithHeaders(headers), common.HttpWithTimeout(60*time.Second))
	if err != nil {
		return nil, event.EventEvidence{}, fmt.Errorf("failed to make request to datadog incidents api: %w", err)
	}
	defer func() {
		err := body.Body.Close()
		if err != nil {
			sc.GetLogger().Error("webhook.getDatadogIncidentDetails: failed to close rows", "error", err)
		}
	}()
	bodyBytes, err := io.ReadAll(body.Body)
	if err != nil {
		return nil, event.EventEvidence{}, fmt.Errorf("failed to read datadog incidents details body: %w", err)
	}

	var incidentDetails map[string]any
	if err := common.UnmarshalJson(bodyBytes, &incidentDetails); err != nil {
		return nil, event.EventEvidence{}, fmt.Errorf("failed to unmarshal datadog incidents details: %w", err)
	}

	sc.GetLogger().Info("datadog_webhook.getDatadogIncidentDetails", "eventDetails", incidentDetails)
	evidence := event.EventEvidence{}
	if incidentDetails["data"] != nil {
		if incident, ok := incidentDetails["data"].(map[string]any); ok {
			evidence.Data = map[string]any{
				"name": "Datadog Incident",
				"data": incident,
			}
			evidence.Type = "json"
			evidence.Insight = []event.EventEvidenceInsight{
				{
					Message:  "Datadog Incident Id - " + incidentId,
					Severity: "info",
				},
			}
			evidence.AdditionalInfo = map[string]any{
				"action_name":            "datadog_incident",
				"actual_action_name":     "datadog_incident",
				"action_title":           "Datadog Incident",
				"conditional_expression": "",
			}
		}
	}

	return incidentDetails, evidence, nil
}

func getDatadogMonitorDetails(sc *security.RequestContext, apiKey, appKey, site, monitorId string) (map[string]any, event.EventEvidence, error) {
	url := fmt.Sprintf("https://%s/api/v1/monitor/%s", site, monitorId)

	sc.GetLogger().Info("datadog_webhook.getDatadogMonitorDetails monitor url", "url", url)
	headers := map[string]string{
		"Content-Type":       "application/json",
		"DD-API-KEY":         apiKey,
		"DD-APPLICATION-KEY": appKey,
	}

	body, err := common.HttpGet(url, common.HttpWithHeaders(headers))
	if err != nil {
		return nil, event.EventEvidence{}, fmt.Errorf("failed to make request to datadog monitors api: %w", err)
	}
	defer func() {
		err := body.Body.Close()
		if err != nil {
			sc.GetLogger().Error("webhook.getDatadogMonitorDetails: failed to close body", "error", err)
		}
	}()
	bodyBytes, err := io.ReadAll(body.Body)
	if err != nil {
		return nil, event.EventEvidence{}, fmt.Errorf("failed to read datadog monitor details body: %w", err)
	}

	var monitorDetails map[string]any
	if err := common.UnmarshalJson(bodyBytes, &monitorDetails); err != nil {
		return nil, event.EventEvidence{}, fmt.Errorf("failed to unmarshal datadog monitor details: %w", err)
	}

	sc.GetLogger().Info("datadog_webhook.getDatadogMonitorDetails", "monitorDetails", monitorDetails)
	evidence := event.EventEvidence{}
	evidence.Data = map[string]any{
		"name": "Datadog Monitor",
		"data": monitorDetails,
	}
	evidence.Type = "json"
	evidence.Insight = []event.EventEvidenceInsight{
		{
			Message:  "Datadog Monitor Id - " + monitorId,
			Severity: "info",
		},
	}
	evidence.AdditionalInfo = map[string]any{
		"action_name":            "datadog_monitor",
		"actual_action_name":     "datadog_monitor",
		"action_title":           "Datadog Monitor",
		"conditional_expression": "",
	}

	return monitorDetails, evidence, nil
}

func getDatadogEventPayload(sc *security.RequestContext, eventPayload map[string]any, datadogPayload DatadogPayload) (map[string]any, event.EventEvidence, error) {
	evidence := event.EventEvidence{}
	evidence.Type = "json"
	jsonBytes, err := json.Marshal(eventPayload)
	if err != nil {
		sc.GetLogger().Error("failed to marshal eventPayload", "error", err)
		return nil, evidence, err
	}
	evidence.Data = string(jsonBytes)
	evidence.Insight = []event.EventEvidenceInsight{
		{
			Message:  "Datadog Event Id - " + datadogPayload.ID,
			Severity: "Info",
		},
	}
	evidence.AdditionalInfo = map[string]any{
		"action_name":            "datadog_event_details",
		"actual_action_name":     "datadog_event_details",
		"action_title":           "Datadog Event Details",
		"conditional_expression": "",
	}
	return eventPayload, evidence, nil
}

func getDatadogTraces(sc *security.RequestContext, apiKey, appKey, site string, tracesQueryParams DatadogQueryParams, datadogPayload DatadogPayload) (map[string]any, event.EventEvidence, error) {
	url := fmt.Sprintf(
		"https://%s/api/v2/spans/events?filter[query]=%s&filter[from]=%d&filter[to]=%d&page[limit]=%d",
		site, tracesQueryParams.TraceQuery, tracesQueryParams.TraceFromTs, tracesQueryParams.TraceToTs, 1000,
	)

	headers := map[string]string{
		"Content-Type":       "application/json",
		"DD-API-KEY":         apiKey,
		"DD-APPLICATION-KEY": appKey,
	}

	body, err := common.HttpGet(url, common.HttpWithHeaders(headers))
	if err != nil {
		return nil, event.EventEvidence{}, fmt.Errorf("failed to make request to datadog traces api: %w", err)
	}

	bodyBytes, err := io.ReadAll(body.Body)
	if err != nil {
		return nil, event.EventEvidence{}, fmt.Errorf("failed to read datadog traces body: %w", err)
	}

	var eventTraces map[string]any
	if err := common.UnmarshalJson(bodyBytes, &eventTraces); err != nil {
		return nil, event.EventEvidence{}, fmt.Errorf("failed to unmarshal datadog traces: %w", err)
	}

	evidence := event.EventEvidence{}
	if raw, ok := eventTraces["data"]; ok {
		rawBytes, err := json.Marshal(raw)
		if err != nil {
			return nil, event.EventEvidence{}, fmt.Errorf("failed to marshal datadog traces: %w", err)
		}

		var ddTrace common.DatadogTrace
		err = json.Unmarshal(rawBytes, &ddTrace.Data)
		if err != nil {
			return nil, event.EventEvidence{}, fmt.Errorf("failed to unmarshal datadog traces: %w", err)
		}

		otelTraces := common.MapDatadogToOpenTelemetry(ddTrace)
		payload := map[string]any{
			"data": otelTraces,
			"name": "datadog_traces",
		}
		jsonStr, err := common.MarshalJson(payload)
		if err != nil {
			return nil, event.EventEvidence{}, fmt.Errorf("marshaling to JSON of otelTraces: %v", err)
		}
		evidence.Data = string(jsonStr)
		evidence.Type = "json"
		evidence.Insight = []event.EventEvidenceInsight{
			{
				Message:  "Datadog Event Id - " + datadogPayload.ID,
				Severity: "info",
			},
		}
		evidence.AdditionalInfo = map[string]any{
			"action_name":            "datadog_traces",
			"actual_action_name":     "datadog_traces",
			"action_title":           "Datadog Traces",
			"conditional_expression": "",
		}
	}

	return eventTraces, evidence, nil
}

func getDatadogMetrics(sc *security.RequestContext, apiKey, appKey, site string, logsQueryParams DatadogQueryParams) (OutputMetricQuery, event.EventEvidence, error) {
	encodedQuery := url.QueryEscape(logsQueryParams.MetricsQuery)
	url := fmt.Sprintf("https://%s/api/v1/query?from=%d&to=%d&query=%s", site, logsQueryParams.MetricsFromTs, logsQueryParams.MetricsToTs, encodedQuery)

	headers := map[string]string{
		"Content-Type":       "application/json",
		"DD-API-KEY":         apiKey,
		"DD-APPLICATION-KEY": appKey,
	}

	body, err := common.HttpGet(url, common.HttpWithHeaders(headers))
	if err != nil {
		return OutputMetricQuery{}, event.EventEvidence{}, fmt.Errorf("datadog_webhook.getDatadogMetrics failed to make request to datadog metrics api for query %q: %w", logsQueryParams.MetricsQuery, err)
	}

	bodyBytes, err := io.ReadAll(body.Body)
	if err != nil {
		return OutputMetricQuery{}, event.EventEvidence{}, fmt.Errorf("datadog_webhook.getDatadogMetrics failed to read body for query %q: %w", logsQueryParams.MetricsQuery, err)
	}
	var data map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &data); err != nil {
		return OutputMetricQuery{}, event.EventEvidence{}, fmt.Errorf("datadog_webhook.getDatadogMetrics failed to parse JSON for query %q: %w", logsQueryParams.MetricsQuery, err)
	}

	if errMsg, ok := data["error"].(string); ok {
		return OutputMetricQuery{}, event.EventEvidence{}, fmt.Errorf("datadog_webhook.getDatadogMetrics datadog query %q returned error: %s", logsQueryParams.MetricsQuery, errMsg)
	}
	queryOutput, err := datadogMapToOutputMetricWithKey(bodyBytes, "1")
	if err != nil {
		return OutputMetricQuery{}, event.EventEvidence{}, fmt.Errorf("datadog_webhook.getDatadogMetrics failed to map datadog response for query %q: %w", logsQueryParams.MetricsQuery, err)
	}
	evidence := event.EventEvidence{}
	evidence.Type = "json"
	jsonBytes, err := json.Marshal(queryOutput)
	if err != nil {
		sc.GetLogger().Error("datadog_webhook.getDatadogMetrics failed to marshal queryOutput", "error", err)
		return OutputMetricQuery{}, evidence, err
	}
	evidence.Data = string(jsonBytes)
	evidence.AdditionalInfo = map[string]any{
		"action_name":            "datadog_metrics",
		"actual_action_name":     "datadog_metrics_details",
		"action_title":           "Datadog Metrics",
		"conditional_expression": "",
	}
	return queryOutput, evidence, nil
}

func datadogMapToOutputMetricWithKey(bodyBytes []byte, queryKey string) (OutputMetricQuery, error) {
	var ddResp DatadogResponse
	if err := json.Unmarshal(bodyBytes, &ddResp); err != nil {
		return OutputMetricQuery{}, fmt.Errorf("failed to unmarshal datadog response: %w", err)
	}

	qr := QueryResult{
		QueryKey: queryKey,
		Payload:  []Result{},
	}

	for _, s := range ddResp.Series {
		metricMap := make(map[string]string)
		metricMap["__name__"] = s.Metric

		for _, t := range s.TagSet {
			if kv := strings.SplitN(t, ":", 2); len(kv) == 2 {
				metricMap[kv[0]] = kv[1]
			}
		}

		if len(s.Unit) > 0 && s.Unit[0] != nil {
			metricMap["unit"] = s.Unit[0].Name
		}

		var timestamps []int64
		var values []float64
		for _, point := range s.PointList {
			if len(point) == 2 {
				t := int64(point[0] / 1000) // ms to sec
				v := point[1]
				timestamps = append(timestamps, t)
				values = append(values, v)
			}
		}

		qr.Payload = append(qr.Payload, Result{
			Metric:     metricMap,
			Timestamps: timestamps,
			Values:     values,
		})
	}

	return OutputMetricQuery{Results: []QueryResult{qr}}, nil
}

func getDatadogErrorTrackingIssueDetails(sc *security.RequestContext, apiKey, appKey, site, issueId string) (map[string]any, event.EventEvidence, error) {
	sources := []struct {
		apiType string
		url     string
		query   string
	}{
		{apiType: "logs", url: fmt.Sprintf("https://%s/api/v2/logs/events", site), query: fmt.Sprintf("issue.id:%s", issueId)},
		{apiType: "traces", url: fmt.Sprintf("https://%s/api/v2/spans/events", site), query: fmt.Sprintf("@issue.id:%s", issueId)},
		{apiType: "rum", url: fmt.Sprintf("https://%s/api/v2/rum/events", site), query: fmt.Sprintf("issue.id:%s", issueId)},
	}

	for _, s := range sources {
		headers := map[string]string{
			"Content-Type":       "application/json",
			"DD-API-KEY":         apiKey,
			"DD-APPLICATION-KEY": appKey,
		}

		requestBody := map[string]any{
			"filter": map[string]string{
				"query": s.query,
			},
			"page": map[string]int{
				"limit": 1, // We only need one event to get details
			},
		}

		var body *http.Response
		var err error

		if s.apiType == "traces" || s.apiType == "logs" {
			query := map[string]string{"filter[query]": s.query}
			query["filter[from]"] = fmt.Sprintf("%d", time.Now().UTC().UnixMilli()-24*60*60*1000)
			body, err = common.HttpGet(s.url, common.HttpWithHeaders(headers), common.HttpWithQueryParams(query), common.HttpWithContext(sc.GetContext()))
		} else {
			body, err = common.HttpPost(s.url, common.HttpWithHeaders(headers), common.HttpWithJsonBody(requestBody))
		}
		if err != nil {
			sc.GetLogger().Error("failed to make request to datadog api", "error", err, "api_type", s.apiType)
			continue
		}

		bodyBytes, err := io.ReadAll(body.Body)
		if err != nil {
			sc.GetLogger().Error("failed to read datadog api response body", "error", err, "api_type", s.apiType)
			continue
		}

		var response map[string]any
		if err := common.UnmarshalJson(bodyBytes, &response); err != nil {
			sc.GetLogger().Error("failed to unmarshal datadog api response", "error", err, "api_type", s.apiType)
			continue
		}

		if data, ok := response["data"].([]any); ok && len(data) > 0 {
			sc.GetLogger().Info("found datadog error tracking issue details", "issue_id", issueId, "api_type", s.apiType)
			return response, event.EventEvidence{
				Type: "json",
				Data: map[string]any{
					"name": "Datadog Error Tracking Issue Details",
					"data": data[0],
				},
				Insight: []event.EventEvidenceInsight{
					{
						Message:  "Datadog Error Tracking Issue Id - " + issueId,
						Severity: "info",
					},
				},
				AdditionalInfo: map[string]any{
					"action_name":        "datadog_error_tracking_issue",
					"actual_action_name": "datadog_error_tracking_issue",
					"action_title":       "Datadog Error Tracking Issue",
				},
			}, nil
		}
	}

	return nil, event.EventEvidence{}, fmt.Errorf("could not find error tracking issue details for issueId: %s in any source", issueId)
}

// DatadogIncident represents the incident section of a Datadog webhook payload.
// Datadog sends the literal string "null" for all incident fields on non-incident
// alerts. The custom UnmarshalJSON normalizes these to empty strings automatically.
type DatadogIncident struct {
	IncidentPublicID     string `json:"incident_public_id"`
	IncidentSeverity     string `json:"incident_severity"`
	IncidentStatus       string `json:"incident_status"`
	IncidentURL          string `json:"incident_url"`
	IncidentUUID         string `json:"incident_uuid"`
	IncidentTitle        string `json:"incident_title"`
	IncidentMessage      string `json:"incident_message"`
	IncidentIntegrations []struct {
		UUID            string `json:"uuid"`
		IntegrationType int    `json:"integration_type"`
		Status          int    `json:"status"`
		Metadata        struct {
			Channels []struct {
				OrgID        int    `json:"org_id"`
				TeamID       string `json:"team_id"`
				ChannelID    string `json:"channel_id"`
				ChannelName  string `json:"channel_name"`
				RedirectURL  string `json:"redirect_url"`
				IncidentUUID string `json:"incident_uuid"`
			} `json:"channels"`
		} `json:"metadata"`
	} `json:"incident_integrations"`
	IncidentFields interface{} `json:"incident_fields"`
}

func (d *DatadogIncident) UnmarshalJSON(data []byte) error {
	// Use an alias to avoid infinite recursion.
	type Alias DatadogIncident
	var raw Alias
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*d = DatadogIncident(raw)

	// Datadog templates emit the literal string "null" for every incident
	// field when the webhook is not related to an incident. Walk all top-level
	// string fields and normalize them.
	for _, ptr := range []*string{
		&d.IncidentPublicID,
		&d.IncidentSeverity,
		&d.IncidentStatus,
		&d.IncidentURL,
		&d.IncidentUUID,
		&d.IncidentTitle,
		&d.IncidentMessage,
	} {
		if *ptr == "null" {
			*ptr = ""
		}
	}
	return nil
}

type DatadogPayload struct {
	ID          string `json:"id"`
	LastUpdated int64  `json:"last_updated,string"`
	EventType   string `json:"event_type"`
	Title       string `json:"title"`
	Date        int64  `json:"date,string"`
	Org         struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"org"`
	Body string `json:"body"`

	Alert struct {
		AlertID              string `json:"alert_id"`
		AlertMetric          string `json:"alert_metric"`
		AlertQuery           string `json:"alert_query"`
		AlertStatus          string `json:"alert_status"`
		AlertScope           string `json:"alert_scope"`
		AlertTransition      string `json:"alert_transition"`
		AlertType            string `json:"alert_type"`
		AlertMetricNamespace string `json:"alert_metric_namespace"`
	} `json:"alert"`

	Incident DatadogIncident `json:"incident"`

	LogsSample string `json:"logs_sample"`

	Event struct {
		AggregKey string `json:"aggreg_key"`
		EventID   string `json:"event_id"`
		EventURL  string `json:"event_url"`
	} `json:"event"`
}

type DatadogQueryParams struct {
	TraceQuery    string
	LogQuery      string
	TraceFromTs   int64
	TraceToTs     int64
	LogFromTs     int64
	LogToTs       int64
	MetricsQuery  string
	MetricsFromTs int64
	MetricsToTs   int64
}

// Helper function to find a URL by key in message_parts.links
func extractLinkByKey(context *security.RequestContext, targetKey string, data any, pathToLinks ...string) string {
	current := data
	for _, key := range pathToLinks {
		m, ok := current.(map[string]any)
		if !ok {
			context.GetLogger().Error("Expected map at key %v, got %T\n", key, current)
			return ""
		}
		current = m[key]
	}

	// At this point, current should be a []any (slice of links)
	links, ok := current.([]any)
	if !ok {
		context.GetLogger().Error("Expected slice at end of path, got %T\n", current)
		return ""
	}

	for _, link := range links {
		if m, ok := link.(map[string]any); ok {
			if keyVal, ok := m["key"].(string); ok && keyVal == targetKey {
				if urlStr, ok := m["url"].(string); ok {
					return urlStr
				}
			}
		}
	}
	return ""
}

var (
	rePrefix        = regexp.MustCompile(`^[a-z_]+\([^)]+\):(.+)`)
	reThreshold     = regexp.MustCompile(`(.+)\s+(?:>|>=|<|<=|==|!=)\s+[0-9\.-]+$`)
	reLogAlertQuery = regexp.MustCompile(`^logs?\("((?:[^"\\]|\\.)*)"\)`)
)

// parseLogQueryFromResultURL extracts the log search query and time range from result.logs_url
func parseLogQueryFromResultURL(logsURL string) (query string, fromTs, toTs int64, ok bool) {
	// logsURL is a relative path like /logs/analytics?query=...&from_ts=...&to_ts=...
	parsed, err := url.Parse(logsURL)
	if err != nil {
		return "", 0, 0, false
	}
	params := parsed.Query()
	query = params.Get("query")
	if query == "" {
		return "", 0, 0, false
	}
	if from := params.Get("from_ts"); from != "" {
		if v, err := strconv.ParseInt(from, 10, 64); err == nil {
			fromTs = v / 1000 // convert ms to seconds
		}
	}
	if to := params.Get("to_ts"); to != "" {
		if v, err := strconv.ParseInt(to, 10, 64); err == nil {
			toTs = v / 1000
		}
	}
	return query, fromTs, toTs, true
}

// parseLogAlertQuery extracts the inner search query from a log alert monitor query
// e.g. logs("service:sqlserver \"Login failed\"").index("*").rollup("count").by("host").last("5m") > 0
// returns: service:sqlserver "Login failed"
// Also handles log() (singular) and escaped quotes inside the query.
func parseLogAlertQuery(monitorQuery string) (string, bool) {
	matches := reLogAlertQuery.FindStringSubmatch(monitorQuery)
	if len(matches) == 2 {
		// Unescape inner quotes from the monitor query DSL
		query := strings.ReplaceAll(matches[1], `\"`, `"`)
		return query, true
	}
	return "", false
}

func parseMetricFromMonitorQuery(monitorQuery string) (string, error) {
	var metricAndThreshold string
	if matches := rePrefix.FindStringSubmatch(monitorQuery); len(matches) == 2 {
		metricAndThreshold = strings.TrimSpace(matches[1])
	} else {
		metricAndThreshold = monitorQuery
	}
	if matches := reThreshold.FindStringSubmatch(metricAndThreshold); len(matches) == 2 {
		return strings.TrimSpace(matches[1]), nil
	}
	if metricAndThreshold == "" {
		return "", fmt.Errorf("failed to parse empty query from monitor: %s", monitorQuery)
	}
	return metricAndThreshold, nil
}

func CallChatCompletionAPI(sc *security.RequestContext, accountId, title, applicationNames string) string {
	tenantId := sc.GetSecurityContext().GetTenantId()

	// Load historical patterns from tenant_attrs (tenant-scoped, TTL-cached)
	mappings, err := GetSubjectMappingsForPrompt(sc, tenantId, TenantAttrHistoricalIncidentsKey, 1000)
	if err != nil {
		sc.GetLogger().Warn("datadogwebhook: failed to load subject mappings, continuing without them", "error", err)
	}

	historicalPatterns := FormatSubjectMappingsForPrompt(mappings, 1000)

	// Create a prompt for the chat completion API with historical patterns
	prompt := `@llm You are a service name matcher for an incident management system. Return ONLY the matching service name(s), nothing else.

Title: "%s"

Historical patterns (title → service):
%s

Running services:
%s

MATCHING RULES (apply in order, stop at the first match):

1. DIRECT KEYWORD MATCH: If the title contains a word that is part of a Running service name, match it.
   - "Rating Down" → "rating-service"
   - "Driver Tracking Down" → "driver-tracking-service"
   - "shipment-search-api latency" → "shipment-search-api"

2. KNOWN ABBREVIATIONS: Expand common abbreviations to match Running services.
   - STS = shipment-tracking-service
   - TTS = truckload-tracking-service
   - TLC = truckload-connector-service
   - SSS = shipment-search-service or shipment-search-api
   - OII = ocean-insights-integration
   - DFP = data-feed-pipeline
   - FH = freighthub
   - MSS = master-shipment-service

3. PRODUCT/BRAND NAME MAPPING: Map product names to their underlying services.
   - VOC (Visibility Operations Center) = portal-v2-ui, portal-v2-service, shipment-search-api
   - Movement = shipment-list-gateway, portal-v2-ui
   - CTT (Container Travel Time) = transit-time-service
   - Analytics / Dashboard / Reporting / Looker = analytics-reporting-gateway
   - Kong = kong
   - YMS (Yard Management) = yms-api

4. FUNCTIONAL DESCRIPTION MATCH: If the title describes a function rather than naming a service, identify which Running service owns that function.
   - "shipment visibility" → shipment-search-api
   - "login" / "authentication" / "401s" / "Okta" → user-service, session-service
   - "LTL rating" / "LTL dispatch" → p44-connector-service
   - "webhook push" / "push delayed" → push-processor
   - "ETA" → look for *-eta-* services

5. HISTORICAL PATTERN MATCH: If rules 1-4 don't match, find the most similar title in Historical patterns and use its service mapping.

6. INFRASTRUCTURE VARIANTS: When a match is found for shared infrastructure (e.g., a gateway, proxy, or message broker), also return ALL variant services from the Running services list that share the same base name (e.g., if "kong" matches, also return "internal-kong" if it exists in Running services).

7. INFRASTRUCTURE-ONLY TITLES: If the title only mentions infrastructure (e.g., "Kafka Down", "Redis Down", "Database issues") without naming or implying any specific application service, return "Not Found" — do not guess.

MULTIPLE MATCHES: If the title refers to multiple services, return all matching FULL names separated by comma and space.

FULL NAMES ONLY: Always return the exact full name as listed in Running services.

IMPORTANT:
- Do NOT force a match when there is no clear connection
- Do NOT guess or return a random service name
- If you cannot find a clear match, return exactly: "Not Found"

Return ONLY the FULL service name(s) from Running services OR "Not Found". NO explanation. NO other text.

Service name:`

	// Construct the request payload
	chatRequest := llm.ConversationApiRequest{
		Query:     fmt.Sprintf(prompt, title, historicalPatterns, applicationNames),
		AccountId: accountId,
		UserId:    sc.GetSecurityContext().GetUserId(),
		Async:     false,
		Source:    "webhook_label_extraction",
	}

	response, err := llm.ChatCompletion(sc, chatRequest)
	if err != nil {
		sc.GetLogger().Error("datadogwebhook: failed to get chat completion request", "error", err)
		return ""
	}
	if response == nil || len(response.Response) == 0 {
		sc.GetLogger().Warn("datadogwebhook: chat completion returned empty response")
		return ""
	}
	if response.Response[0] == "Not Found" {
		return ""
	}
	return response.Response[0]
}

func (m DatadogWebhook) ProcessEventWebook(sc *security.RequestContext, settings []core.IntegrationConfigValue, accountId, webhookPayloadString string) ([]core.EventIncomingWebhook, error) {
	// The incoming payload is a JSON with a "payload" field which is a stringified JSON.
	var outerPayload map[string]any
	if err := common.UnmarshalJson([]byte(webhookPayloadString), &outerPayload); err != nil {
		return nil, fmt.Errorf("datadogwebhook: failed to unmarshal outer payload: %w", err)
	}

	innerPayloadStr, ok := outerPayload["payload"].(string)
	if !ok {
		if _, ok := outerPayload["event_type"]; ok {
			innerPayloadStr = webhookPayloadString
		} else {
			sc.GetLogger().Error("datadogwebhook: unable to process webhook data", "data", webhookPayloadString)
			return nil, errors.New("datadogwebhook: unable to process webhook data")
		}
	}

	var p DatadogPayload
	if err := common.UnmarshalJson([]byte(innerPayloadStr), &p); err != nil {
		return nil, fmt.Errorf("datadogwebhook: failed to unmarshal inner payload: %w", err)
	}

	// Datadog sends literal string "null" for incident fields on non-incident alerts.
	// Normalize these to empty strings so downstream checks work correctly.
	if p.Incident.IncidentPublicID == "null" {
		p.Incident.IncidentPublicID = ""
	}
	if p.Incident.IncidentUUID == "null" {
		p.Incident.IncidentUUID = ""
	}
	if p.Incident.IncidentSeverity == "null" {
		p.Incident.IncidentSeverity = ""
	}
	if p.Incident.IncidentStatus == "null" {
		p.Incident.IncidentStatus = ""
	}
	if p.Incident.IncidentURL == "null" {
		p.Incident.IncidentURL = ""
	}

	// Extract status and priority from title
	titleMatches := datadogTitleRegex.FindStringSubmatch(p.Title)
	var priority, status, cleanTitle string
	if len(titleMatches) == 4 {
		priority = titleMatches[1]
		rawStatus := titleMatches[2]
		switch rawStatus {
		case "Triggered", "Re-Triggered", "Alert":
			status = string(event.EventStatusFiring)
		case "Recovered", "No Data":
			status = string(event.EventStatusResolved)
		case "Warn":
			status = string(event.EventStatusFiring)
		default:
			status = string(event.EventStatusFiring)
		}
		cleanTitle = titleMatches[3]
	} else {
		cleanTitle = p.Title
		// Attempt to extract status from title even if regex doesn't fully match
		if strings.Contains(strings.ToLower(p.Title), "recovered") || strings.Contains(strings.ToLower(p.Title), "no data") {
			status = string(event.EventStatusResolved)
		} else if strings.Contains(strings.ToLower(p.Title), "triggered") || strings.Contains(strings.ToLower(p.Title), "alert") {
			status = string(event.EventStatusFiring)
		} else if p.Incident.IncidentStatus == "completed" {
			status = string(event.EventStatusResolved)
		} else {
			status = string(event.EventStatusFiring)
		}
		priority = "P3" // Default priority if not found in title
	}

	if p.Incident.IncidentSeverity != "" && p.Incident.IncidentSeverity != "UNKNOWN" {
		priority = p.Incident.IncidentSeverity
	}
	if p.Incident.IncidentStatus != "" {
		if p.Incident.IncidentStatus == "active" {
			status = string(event.EventStatusFiring)
		}
		if p.Incident.IncidentStatus == "resolved" || p.Incident.IncidentStatus == "stable" {
			status = string(event.EventStatusResolved)
		}
	}
	if p.Incident.IncidentTitle != "" {
		cleanTitle = p.Incident.IncidentTitle
	}

	var eventURL, monitorID string
	monitorURLMatches := datadogMonitorURLRegex.FindStringSubmatch(p.Body)
	if len(monitorURLMatches) >= 2 {
		eventURL = monitorURLMatches[0]
		monitorID = monitorURLMatches[2] // Capture group 2 for the ID
	} else {
		// Try to extract monitor ID from title if not found in body
		monitorIDMatches := datadogMonitorIDRegex.FindStringSubmatch(p.Title)
		if len(monitorIDMatches) >= 2 {
			monitorID = monitorIDMatches[1]
		}
	}
	// Fallback: use alert_id as monitor ID (e.g. synthetics alerts have alert_id but no /monitors/ URL in body)
	if monitorID == "" && p.Alert.AlertID != "" {
		monitorID = p.Alert.AlertID
	}
	if p.Incident.IncidentURL != "" {
		eventURL = p.Incident.IncidentURL
	}
	if eventURL == "" && p.Event.EventURL != "" {
		eventURL = p.Event.EventURL
	}
	apiKey, appKey, site, err := GetDatadogConfigs(sc, accountId)
	sc.GetLogger().Info("logging keys", "apiKey", apiKey, "appKey", appKey, "site", site, "ID", p.ID)
	if err != nil {
		sc.GetLogger().Error("datadogwebhook: failed to get datadog configs", "error", err)
	}

	evidences := []event.EventEvidence{}

	// Extract tags from URLs in the body
	labels := make(map[string]string)
	var allTags []string

	// Get additional event details from Datadog Events API
	eventDetails := make(map[string]any)
	if apiKey != "" && p.Incident.IncidentPublicID == "" {
		const (
			maxRetries = 3
			retryDelay = 5 * time.Second
		)
		time.Sleep(retryDelay)
		eventDetails1, evidence1, err := getDatadogEvent(sc, apiKey, appKey, site, p.ID)
		attempts := 1
		for i := 1; i <= maxRetries && err != nil; i++ {
			sc.GetLogger().Warn("Failed to get Datadog event details, retrying...", "attempt", i, "error", err)
			time.Sleep(retryDelay)
			eventDetails1, evidence1, err = getDatadogEvent(sc, apiKey, appKey, site, p.ID)
			attempts++
		}
		if err != nil {
			sc.GetLogger().Error("Failed to get Datadog event details after all retries", "attempts", attempts, "error", err)
		} else {
			sc.GetLogger().Info("Successfully fetched Datadog event details", "attempts", attempts)
		}
		eventDetails = eventDetails1
		if evidence1.Type != "" {
			evidences = append(evidences, evidence1)
		}
	}

	tracesURL := ""
	logURL := ""
	var eventPayload map[string]any
	var alertName string
	var subjectName string
	issueId := ""
	if len(eventDetails) > 0 {
		if event, ok := eventDetails["event"].(map[string]any); ok {
			if payload, ok := event["payload"].(string); ok {
				// Unescape and parse the JSON
				if err := json.Unmarshal([]byte(payload), &eventPayload); err != nil {
					sc.GetLogger().Error("failed to unmarshal datadog payload", "error", err)
				}
				tracesURL = extractLinkByKey(sc, "traces", eventPayload, "message_parts", "links")
				logURL = extractLinkByKey(sc, "logs", eventPayload, "message_parts", "links")
				if monitors, ok := eventPayload["monitor"].(map[string]any); ok {
					if name, ok := monitors["name"].(string); ok {
						if name != "" {
							alertName = name
						} else {
							alertName = monitorID
						}
					} else {
						alertName = monitorID
						sc.GetLogger().Warn("monitor.name is missing or empty string")
					}
				} else {
					sc.GetLogger().Warn("eventPayload.monitors is not a map")
				}
				// Fallback: extract monitorID from Datadog API response when body/title regex failed
				if monitorID == "" {
					if mid, ok := event["monitor_id"]; ok {
						switch v := mid.(type) {
						case float64:
							monitorID = fmt.Sprintf("%.0f", v)
						case string:
							monitorID = v
						}
					}
				}
				if monitorGroups, ok := event["monitor_groups"].([]interface{}); ok {
					if len(monitorGroups) > 0 {
						if group, ok := monitorGroups[0].(string); ok && group != "" {
							subjectName = group
						}
					}
				}
				if result, ok := eventPayload["result"].(map[string]interface{}); ok {
					if group, ok := result["group"].(string); ok && group != "" {
						subjectName = group
					}
				}

			}
			if text, ok := event["text"].(string); ok {
				p.Body = text
			}
			if tags, ok := event["tags"].([]any); ok {
				for _, tag := range tags {
					if tagStr, ok := tag.(string); ok {
						allTags = append(allTags, tagStr)
						kv := strings.SplitN(tagStr, ":", 2)
						if len(kv) == 2 {
							labels[kv[0]] = kv[1]
							// Check for issue.id in tags for error tracking
							if kv[0] == "issue.id" {
								issueId = kv[1]
							}
						}
					}
				}
			}
		}
	} else {
		// Fallback to extracting tags from the body if API call fails
		tagsMatches := datadogTagsRegex.FindAllStringSubmatch(p.Body, -1)
		for _, match := range tagsMatches {
			if len(match) >= 2 {
				decodedTags, err := url.QueryUnescape(match[1])
				if err == nil {
					for pair := range strings.SplitSeq(decodedTags, ",") {
						allTags = append(allTags, pair)
						kv := strings.SplitN(pair, ":", 2)
						if len(kv) == 2 {
							labels[kv[0]] = kv[1]
						}
					}
				}
			}
		}
	}

	if p.Incident.IncidentUUID != "" {
		labels["incident_uuid"] = p.Incident.IncidentUUID
		incidentDetails, evidence5, err := getDatadogIncidentDetails(sc, apiKey, appKey, site, p.Incident.IncidentPublicID)
		if err != nil {
			sc.GetLogger().Error("Failed to get Datadog incident details", "error", err)
		} else {
			if incident, ok := incidentDetails["data"].(map[string]interface{}); ok {
				if attributes, ok := incident["attributes"].(map[string]interface{}); ok {
					if fields, ok := attributes["fields"].(map[string]interface{}); ok {
						// Extract ALL incident fields automatically with normalized names
						for fieldName, fieldData := range fields {
							if fieldMap, ok := fieldData.(map[string]interface{}); ok {
								if value := fieldMap["value"]; value != nil {
									// Normalize field names: "Product SKU" → "product_sku"
									normalizedName := strings.ToLower(strings.ReplaceAll(fieldName, " ", "_"))

									switch v := value.(type) {
									case string:
										// Single string value
										labels[normalizedName] = v
										// Backward compatibility: set subjectName for services field
										if normalizedName == "services" {
											subjectName = v
										}

									case []interface{}:
										// Multiselect field (e.g., Env: ["eu-production", "us-production"])
										var parts []string
										for i, item := range v {
											if s, ok := item.(string); ok {
												parts = append(parts, s)
												// Create indexed labels: env_0, env_1, etc.
												labels[fmt.Sprintf("%s_%d", normalizedName, i)] = s
											}
										}
										// Also create main label with first value
										if len(parts) > 0 {
											labels[normalizedName] = parts[0]
											// Backward compatibility: set subjectName for services field
											switch normalizedName {
											case "services":
												subjectName = parts[0]
												labels["service"] = strings.Join(parts, ",")
											case "env":
												labels["env"] = parts[0]
											}
										}

									case float64, int, bool:
										// Numeric or boolean values
										labels[normalizedName] = fmt.Sprintf("%v", v)
									}
								}
							}
						}
					}
				}
			}

			// Extract monitor ID from summary URL (e.g. https://p44.datadoghq.com/monitors/10285541)
			if summary, ok := labels["summary"]; ok {
				if matches := datadogMonitorPathRegex.FindStringSubmatch(summary); len(matches) > 1 {
					labels["monitor_id"] = matches[1]

					// Fetch monitor details from Datadog API
					monitorDetails, monitorEvidence, monitorErr := getDatadogMonitorDetails(sc, apiKey, appKey, site, matches[1])
					if monitorErr != nil {
						sc.GetLogger().Error("Failed to get Datadog monitor details", "monitorId", matches[1], "error", monitorErr)
					} else {
						evidences = append(evidences, monitorEvidence)
						if query, ok := monitorDetails["query"].(string); ok && query != "" {
							labels["monitor_query"] = query
						}
					}
				}
			}

		}
		evidences = append(evidences, evidence5)
	}
	if p.Incident.IncidentTitle != "" {
		alertName = p.Incident.IncidentTitle
	}

	if len(p.Incident.IncidentIntegrations) > 0 {
		var slackChannelIDs []string
		var slackChannelNames []string
		for _, integration := range p.Incident.IncidentIntegrations {
			for _, ch := range integration.Metadata.Channels {
				if ch.RedirectURL == "" {
					continue
				}
				parsedURL, err := url.Parse(ch.RedirectURL)
				if err == nil && strings.HasSuffix(strings.ToLower(parsedURL.Host), "slack.com") {
					slackChannelIDs = append(slackChannelIDs, ch.ChannelID)
					slackChannelNames = append(slackChannelNames, ch.ChannelName)
				}
			}
		}
		if len(slackChannelIDs) > 0 {
			labels["slack_channel_id"] = strings.Join(slackChannelIDs, ",")
			labels["slack_channel_name"] = strings.Join(slackChannelNames, ",")
		}
	}

	//"[P2] [Triggered on {issue.id:2df20914-58c8-11f0-b092-da7ad0900000}] [New issue] New issue to review"
	if issueId == "" && strings.Contains(p.Title, "issue.id") {
		sc.GetLogger().Info("unable to find issue detail for monitor trigger, looking for title", "title", p.Title, "issue", p.ID)
		issueIdMatches := datadogIssueIdRegex.FindStringSubmatch(p.Title)
		if len(issueIdMatches) >= 2 {
			issueId = issueIdMatches[1]
		}
	}

	if issueId != "" {
		sc.GetLogger().Info("fetching issue details from datadog", "issue_id", issueId)
		data, errTrackingEvidence, err := getDatadogErrorTrackingIssueDetails(sc, apiKey, appKey, site, issueId)
		if err != nil {
			sc.GetLogger().Error("failed to get datadog error tracking issue details", "error", err, "issue_id", issueId)
		} else {
			evidences = append(evidences, errTrackingEvidence)
			if data != nil {
				if dataItems, ok := data["data"].([]any); ok && len(dataItems) > 0 {
					if firstItem, ok := dataItems[0].(map[string]any); ok {
						if attributes, ok := firstItem["attributes"].(map[string]any); ok {
							if tags, ok := attributes["tags"].([]any); ok {
								for _, tag := range tags {
									if tagStr, ok := tag.(string); ok {
										allTags = append(allTags, tagStr)
										kv := strings.SplitN(tagStr, ":", 2)
										if len(kv) == 2 {
											labels[kv[0]] = kv[1]
										}
									}
								}
							}
						}
					}
				}
			}
		}
	}

	if p.Incident.IncidentPublicID == "" {
		go func() {
			alertQuery := ""
			alertMessage := ""
			alertDuration := 0.0
			alertCategory := "alert"
			if p.Incident.IncidentPublicID != "" {
				alertCategory = "incident"
			}
			if transition, ok := eventPayload["transition"].(map[string]interface{}); ok {
				if transType, ok := transition["trans_type"].(string); ok {
					alertCategory = transType
				}
			}
			if monitor, ok := eventPayload["monitor"].(map[string]interface{}); ok {
				if query, ok := monitor["query"].(string); ok {
					alertQuery = query
				}
				if message, ok := monitor["message"].(string); ok {
					alertMessage = message
				}
			}
			if duration, ok := eventPayload["duration"].(float64); ok {
				alertDuration = duration
			}

			eventReq := eventrule.EventConfig{
				Annotations: struct {
					Description string `json:"description"`
					Summary     string `json:"summary"`
					Runbook     string `json:"runbook"`
				}{
					Description: alertMessage,
					Summary:     alertMessage,
				},
				Expr: alertQuery,
				Labels: struct {
					Severity string `json:"severity"`
				}{Severity: "warning"},
				Alert:         alertName,
				Duration:      strconv.FormatFloat(alertDuration, 'f', -1, 64),
				AccountID:     accountId,
				Source:        "datadog_webhook",
				Category:      alertCategory,
				Severity:      "warning",
				Enabled:       true,
				TriggerParams: []map[string]interface{}{},
				ActionParams:  []map[string]interface{}{},
			}
			_, err := eventrule.CreateEventRule(sc, eventReq)
			if err != nil {
				sc.GetLogger().Error("CreateEventRule failed", "error", err)
			}
		}()

	}

	var queryParams []DatadogQueryParams

	defaultFromTs := int64(p.Date/1000 - 600)
	defaultToTs := int64(p.Date/1000 + 600)

	if tracesURL != "" {
		traces, err := url.Parse(tracesURL)
		if err != nil {
			sc.GetLogger().Error("failed to parse datadog trace url", "error", err)
		} else {
			tracesQueryParams := traces.Query()
			qp := DatadogQueryParams{}
			if query := tracesQueryParams.Get("query"); query != "" {
				qp.TraceQuery = query
			}

			if fromTs := tracesQueryParams.Get("from_ts"); fromTs != "" {
				if fromInt, err := strconv.Atoi(fromTs); err == nil {
					qp.TraceFromTs = int64(fromInt)
				} else {
					sc.GetLogger().Error("Invalid 'from_ts' value for traces, using fallback", "from_ts", fromTs, "error", err)
					qp.TraceFromTs = defaultFromTs
				}
			} else {
				qp.TraceFromTs = defaultFromTs
			}

			if toTs := tracesQueryParams.Get("to_ts"); toTs != "" {
				if toInt, err := strconv.Atoi(toTs); err == nil {
					qp.TraceToTs = int64(toInt)
				} else {
					sc.GetLogger().Error("Invalid 'to_ts' value for traces, using fallback", "to_ts", toTs, "error", err)
					qp.TraceToTs = defaultToTs
				}
			} else {
				qp.TraceToTs = defaultToTs
			}

			if qp.TraceQuery != "" {
				queryParams = append(queryParams, qp)
			}
		}
	}

	// Fallback to parsing logURL only if no log query was found in playbooks
	if logURL != "" {
		log, err := url.Parse(logURL)
		if err != nil {
			sc.GetLogger().Error("failed to parse datadog log url", "error", err)
		} else {
			logQueryParams := log.Query()
			qp := DatadogQueryParams{}
			if query := logQueryParams.Get("query"); query != "" {
				qp.LogQuery = query
			}

			if fromTs := logQueryParams.Get("from_ts"); fromTs != "" {
				if fromInt, err := strconv.Atoi(fromTs); err == nil {
					qp.LogFromTs = int64(fromInt)
				} else {
					sc.GetLogger().Error("Invalid 'from_ts' value for log, using fallback", "from_ts", fromTs, "error", err)
					qp.LogFromTs = defaultFromTs
				}
			} else {
				qp.LogFromTs = defaultFromTs
			}

			if toTs := logQueryParams.Get("to_ts"); toTs != "" {
				if toInt, err := strconv.Atoi(toTs); err == nil {
					qp.LogToTs = int64(toInt)
				} else {
					sc.GetLogger().Error("Invalid 'to_ts' value for log, using fallback", "to_ts", toTs, "error", err)
					qp.LogToTs = defaultToTs
				}
			} else {
				qp.LogToTs = defaultToTs
			}

			// Default to wildcard when log URL has timestamps but no query
			// (e.g. synthetics_alert where alert_query is "no_query").
			if qp.LogQuery == "" {
				qp.LogQuery = "*"
			}
			queryParams = append(queryParams, qp)
		}
	}

	if len(eventPayload) > 0 {
		_, evidence4, err := getDatadogEventPayload(sc, eventPayload, p)
		if err != nil {
			sc.GetLogger().Error("failed to get datadog event payload", "error", err)
		}
		if evidence4.Type != "" {
			evidences = append(evidences, evidence4)
		}

		if monitors, ok := eventPayload["monitor"].(map[string]any); ok {
			monitorType, _ := monitors["type"].(string)
			monitorQuery, _ := monitors["query"].(string)

			if monitorType == "query alert" && monitorQuery != "" {
				apiQuery, err := parseMetricFromMonitorQuery(monitorQuery)
				if err != nil {
					sc.GetLogger().Error("failed to parse datadog monitor query", "query", monitorQuery, "error", err)
				} else {
					queryParams = append(queryParams, DatadogQueryParams{
						MetricsQuery:  apiQuery,
						MetricsFromTs: defaultFromTs,
						MetricsToTs:   defaultToTs,
					})
				}
			}

			// For log alert monitors, extract the inner log search query.
			// Prefer timestamps from result.logs_url (exact alert window) over defaults.
			if monitorType == "log alert" && monitorQuery != "" && logURL == "" {
				if logQuery, ok := parseLogAlertQuery(monitorQuery); ok {
					sc.GetLogger().Info("extracted log query from log alert monitor", "query", logQuery)
					qp := DatadogQueryParams{
						LogQuery:  logQuery,
						LogFromTs: defaultFromTs,
						LogToTs:   defaultToTs,
					}
					// Try to use precise timestamps from result.logs_url
					if result, ok := eventPayload["result"].(map[string]any); ok {
						if resultLogsURL, ok := result["logs_url"].(string); ok && resultLogsURL != "" {
							if _, fromTs, toTs, ok := parseLogQueryFromResultURL(resultLogsURL); ok {
								if fromTs > 0 {
									qp.LogFromTs = fromTs
								}
								if toTs > 0 {
									qp.LogToTs = toTs
								}
							}
						}
					}
					queryParams = append(queryParams, qp)
					logURL = "from_monitor_query"
				}
			}
		}

		// Fallback: extract log query from result.logs_url (when no monitor query was available)
		if logURL == "" {
			if result, ok := eventPayload["result"].(map[string]any); ok {
				if resultLogsURL, ok := result["logs_url"].(string); ok && resultLogsURL != "" {
					if query, fromTs, toTs, ok := parseLogQueryFromResultURL(resultLogsURL); ok {
						sc.GetLogger().Info("extracted log query from result.logs_url", "query", query, "fromTs", fromTs, "toTs", toTs)
						qp := DatadogQueryParams{LogQuery: query}
						if fromTs > 0 {
							qp.LogFromTs = fromTs
						} else {
							qp.LogFromTs = defaultFromTs
						}
						if toTs > 0 {
							qp.LogToTs = toTs
						} else {
							qp.LogToTs = defaultToTs
						}
						queryParams = append(queryParams, qp)
						logURL = "from_result_logs_url"
					}
				}
			}
		}
	}

	// Fallback: build log query from available tags (K8s and non-K8s)
	if logURL == "" {
		var parts []string
		// K8s labels
		if labels["kube_namespace"] != "" {
			parts = append(parts, fmt.Sprintf("kube_namespace:%s", labels["kube_namespace"]))
		}
		if labels["pod_name"] != "" {
			parts = append(parts, fmt.Sprintf("pod_name:%s", labels["pod_name"]))
		}
		// Non-K8s: use host and service tags
		if len(parts) == 0 {
			if labels["host"] != "" {
				parts = append(parts, fmt.Sprintf("host:%s", labels["host"]))
			}
			if labels["service"] != "" {
				parts = append(parts, fmt.Sprintf("service:%s", labels["service"]))
			}
			if labels["dbms"] != "" {
				parts = append(parts, fmt.Sprintf("service:%s", labels["dbms"]))
			}
		}
		query := strings.Join(parts, " ")
		if query != "" {
			qp := DatadogQueryParams{LogQuery: query}
			monitor, err := url.Parse(eventURL)
			if err != nil {
				sc.GetLogger().Error("failed to parse datadog monitor url", "error", err)
				qp.LogFromTs = defaultFromTs
				qp.LogToTs = defaultToTs
			} else {
				monitorQueryParams := monitor.Query()
				if fromTs := monitorQueryParams.Get("from_ts"); fromTs != "" {
					if fromInt, err := strconv.Atoi(fromTs); err == nil {
						qp.LogFromTs = int64(fromInt)
					} else {
						sc.GetLogger().Error("Invalid 'from_ts' value for monitor, using fallback", "from_ts", fromTs, "error", err)
						qp.LogFromTs = defaultFromTs
					}
				} else {
					qp.LogFromTs = defaultFromTs
				}

				if toTs := monitorQueryParams.Get("to_ts"); toTs != "" {
					if toInt, err := strconv.Atoi(toTs); err == nil {
						qp.LogToTs = int64(toInt)
					} else {
						sc.GetLogger().Error("Invalid 'to_ts' value for monitor, using fallback", "to_ts", toTs, "error", err)
						qp.LogToTs = defaultToTs
					}
				} else {
					qp.LogToTs = defaultToTs
				}
			}
			queryParams = append(queryParams, qp)
		}
	}

	// Set log_query and time range labels for auto-execute actions (datadogLogsAction checks this)
	for _, qp := range queryParams {
		if qp.LogQuery != "" {
			labels["log_query"] = qp.LogQuery
			if qp.LogFromTs > 0 {
				labels["log_from_ts"] = fmt.Sprintf("%d", qp.LogFromTs)
			}
			if qp.LogToTs > 0 {
				labels["log_to_ts"] = fmt.Sprintf("%d", qp.LogToTs)
			}
			break
		}
	}

	if apiKey != "" {
		for _, qp := range queryParams {
			if qp.TraceQuery != "" {
				sc.GetLogger().Info("datadog trace details", "queryParam", qp)
				_, evidence2, err := getDatadogTraces(sc, apiKey, appKey, site, qp, p)
				if err != nil {
					sc.GetLogger().Error("failed to get datadog trace details", "error", err)
				}
				if evidence2.Type != "" {
					evidences = append(evidences, evidence2)
				}
			}
			if qp.MetricsQuery != "" {
				sc.GetLogger().Info("datadog metric details", "queryParam", qp)
				_, evidence4, err := getDatadogMetrics(sc, apiKey, appKey, site, qp)
				if err != nil {
					sc.GetLogger().Error("failed to get datadog metrics data of actions", "error", err, "query", qp.MetricsQuery)
				} else if evidence4.Type != "" {
					evidences = append(evidences, evidence4)
				}
			}
		}
	}

	createdAt := time.UnixMilli(p.Date)
	updatedAt := time.UnixMilli(p.LastUpdated)

	// Add Datadog URLs to labels for auto-execute actions
	if tracesURL != "" {
		labels["traces_url"] = tracesURL
	}
	if logURL != "" {
		labels["log_url"] = logURL
	}

	// Add alert name for potential metric queries
	if alertName != "" {
		labels["alert_name"] = alertName
	}

	priorityMap := map[string]event.EventPriortiy{
		"P1":      event.EventPriortiyHigh,
		"P2":      event.EventPriortiyMedium,
		"P3":      event.EventPriortiyLow,
		"P4":      event.EventPriortiyInfo,
		"Unknown": event.EventPriortiyInfo,
		"SEV-5":   event.EventPriortiyInfo,
		"SEV-4":   event.EventPriortiyLow,
		"SEV-3":   event.EventPriortiyMedium,
		"SEV-2":   event.EventPriortiyHigh,
		"SEV-1":   event.EventPriortiyHigh,
	}

	fingerprint := p.Event.AggregKey
	ruleId := alertName
	findingId := p.ID
	if monitorID != "" && p.EventType != "notification" {
		fingerprint = fmt.Sprintf("%s-%s", monitorID, p.Event.AggregKey)
	} else if p.Incident.IncidentPublicID != "" {
		ruleId = "datadog_incident"
		findingId = p.Incident.IncidentUUID
		fingerprint = p.Incident.IncidentUUID
	}
	// Fallback: ensure ruleId is never empty — use monitorID or cleanTitle
	if ruleId == "" && monitorID != "" {
		ruleId = monitorID
	}
	if ruleId == "" {
		ruleId = cleanTitle
	}
	// Fallback: ensure fingerprint is never empty
	if fingerprint == "" && monitorID != "" {
		fingerprint = monitorID
	}
	if fingerprint == "" && alertName != "" {
		fingerprint = alertName
	}
	investigation := core.EventIncomingWebhookInvestigation{
		RuleName:    cleanTitle,
		RuleId:      ruleId,
		Fingerprint: fingerprint,
		Status:      event.EventStatus(status),
		Severity:    priorityMap[priority],
		SourceUrl:   eventURL,
		Labels:      labels,
		Evidences:   evidences,
	}

	// Remap account before enrichment so workload lookups target the correct account
	accountMapping := core.ParseAccountMapping(settings, sc.GetLogger())
	accountId = core.ApplyAccountMapping(accountId, labels, accountMapping)

	subjectNamespace := ""
	subjectKind := ""
	cloudResourceId := ""
	if subjectName == "" && tenant.IsFeatureEnabled(sc, sc.GetSecurityContext().GetTenantId(), tenant.FEATURE_WEBHOOK_LLM_RESOLUTION) {
		databaseManager, err := database.GetDatabaseManager(database.Metastore)
		if err != nil {
			sc.GetLogger().Error("failed to get database manager", "error", err)
		} else {
			// A single Datadog integration can be wired to multiple cloud accounts via
			// integrations_cloud_accounts. lookupIntegrationByToken arbitrarily picks the
			// first row, so workload lookups scoped to that one accountId can miss the
			// real workload (or worse, hit a same-named workload in the wrong account).
			// Expand the search to every cloud account this integration covers.
			candidateAccountIds, lookupErr := core.GetLinkedCloudAccountIds(sc, accountId, IntegrationDatadogWebhook)
			if lookupErr != nil {
				sc.GetLogger().Warn("failed to expand linked cloud accounts, falling back to single account",
					"error", lookupErr, "account_id", accountId)
			}
			if len(candidateAccountIds) == 0 {
				candidateAccountIds = []string{accountId}
			}

			var names []string
			err = databaseManager.Db.Select(&names, `
				SELECT DISTINCT (labels->>'tags.datadoghq.com/service'::text)
				FROM k8s_workloads
				WHERE tenant_id = $1
				  AND cloud_account_id = ANY($2)
				  AND is_active = true
				  AND kind NOT IN ('Job', 'CronJob')
				  AND labels->>'tags.datadoghq.com/service'::text IS NOT NULL
			`, sc.GetSecurityContext().GetTenantId(), pq.Array(candidateAccountIds))
			if err != nil {
				sc.GetLogger().Error("failed to get data from database for application names", "error", err)
			} else if len(names) > 0 {
				serviceName := CallChatCompletionAPI(sc, accountId, cleanTitle, strings.Join(names, ","))
				sc.GetLogger().Info("LLM returned service name", "service_name", serviceName, "title", cleanTitle)
				resolutionResult := "not_found"
				if serviceName != "" {
					resolutionResult = "matched"
				}
				common.MetricsSubjectResolution(sc.GetContext(), IntegrationDatadogWebhook, "live", resolutionResult, sc.GetSecurityContext().GetTenantId())
				if serviceName != "" {
					labels["service"] = serviceName

					// If LLM returned multiple comma-separated services, use the first one for workload lookup
					lookupService := serviceName
					if parts := strings.SplitN(serviceName, ",", 2); len(parts) > 1 {
						lookupService = strings.TrimSpace(parts[0])
					}

					// Fetch workload details including kind and cloud_resource_id.
					// Search across every linked cloud account so we don't return a
					// workload from the wrong account just because it shares a name.
					var workload struct {
						Name            string `db:"name"`
						Namespace       string `db:"namespace"`
						Kind            string `db:"kind"`
						CloudResourceId string `db:"cloud_resource_id"`
						CloudAccountId  string `db:"cloud_account_id"`
					}
					query := `
						SELECT name, namespace, kind, cloud_resource_id, cloud_account_id::text
						FROM k8s_workloads
						WHERE tenant_id = $1
						  AND cloud_account_id = ANY($2)
						  AND is_active = true
						  AND kind NOT IN ('Job', 'CronJob')
						  AND labels->>'tags.datadoghq.com/service'::text = $3
						LIMIT 1
					`
					err := databaseManager.Db.Get(&workload, query,
						sc.GetSecurityContext().GetTenantId(),
						pq.Array(candidateAccountIds),
						lookupService,
					)
					if err != nil {
						sc.GetLogger().Error("failed to get workload info from database",
							"error", err,
							"service_name", lookupService,
						)
					} else {
						subjectName = workload.Name
						subjectNamespace = workload.Namespace
						subjectKind = strings.ToLower(workload.Kind)
						cloudResourceId = workload.CloudResourceId
						labels["kind"] = workload.Kind
						labels["cloud_resource_id"] = workload.CloudResourceId
						// Re-anchor accountId on the matched workload's cloud account so
						// downstream enrichment (event_incoming_webhooks insert, routing,
						// EventIncomingWebhook.AccountId) targets the correct account.
						if workload.CloudAccountId != "" && workload.CloudAccountId != accountId {
							sc.GetLogger().Info("rebinding webhook accountId to matched workload's cloud account",
								"original_account_id", accountId,
								"matched_account_id", workload.CloudAccountId,
								"service_name", lookupService,
							)
							accountId = workload.CloudAccountId
						}
					}
				}
			}
		}
	} else if subjectName == "" {
		labels["nb_llm_match"] = "disabled"
	}

	// Auto-learn: save confirmed title → service mapping for future LLM prompts
	if subjectName != "" && cleanTitle != "" {
		LearnSubjectMapping(sc, sc.GetSecurityContext().GetTenantId(), TenantAttrHistoricalIncidentsKey, cleanTitle, subjectName)
	}

	event := core.EventIncomingWebhook{
		WebhookId:             p.ID,
		EventType:             p.EventType,
		EventId:               findingId,
		EventUrl:              eventURL,
		EventStatus:           status,
		EventPriority:         priority,
		EventCreatedAt:        createdAt,
		EventEndsAt:           updatedAt,
		EventTitle:            cleanTitle,
		EventDescription:      p.Body,
		EventTags:             allTags,
		Investigation:         investigation,
		EventSubjectName:      subjectName,
		EventSubjectNamespace: subjectNamespace,
		EventSubjectKind:      subjectKind,
		CloudResourceId:       cloudResourceId,
		AccountId:             accountId,
		EventSubjectOwner:     subjectName,
	}
	return []core.EventIncomingWebhook{event}, nil
}
