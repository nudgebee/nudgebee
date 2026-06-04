package observability

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"nudgebee/services/common"
	"nudgebee/services/integrations"
	"nudgebee/services/query"
	"nudgebee/services/security"
	"strconv"
	"strings"
	"time"
)

type ObserveLog struct {
	// Existing fields
	Body               string         `json:"body"`
	Cluster            string         `json:"cluster"`
	Attributes         map[string]any `json:"attributes"`
	Container          string         `json:"container"`
	Fields             map[string]any `json:"fields"`
	Meta               map[string]any `json:"meta"`
	Namespace          string         `json:"namespace"`
	Node               string         `json:"node"`
	Pod                string         `json:"pod"`
	ResourceAttributes map[string]any `json:"resource_attributes"`
	Stream             string         `json:"stream"`
	Timestamp          string         `json:"timestamp"`

	// Newly discovered fields (Spring Boot/Sleuth format)
	Level           string         `json:"level"`
	Message         string         `json:"message"`
	LoggerName      string         `json:"loggerName"`
	SleuthSpanId    string         `json:"sleuthSpanId"`
	SleuthTraceId   string         `json:"sleuthTraceId"`
	ApplicationName string         `json:"applicationName"`
	Host            string         `json:"host"`
	Tags            interface{}    `json:"tags"`
	Thread          string         `json:"thread"`
	UserId          string         `json:"userId"`
	TenantId        string         `json:"tenantId"`
	DataStream      map[string]any `json:"data_stream"`
	Thrown          map[string]any `json:"thrown"` // Exception details: message, name, extendedStackTrace
	ExtraData       string         `json:"extraData"`

	// Uppercase FIELDS variant (some logs use uppercase)
	FieldsUppercase map[string]any `json:"FIELDS"`

	// RawFields holds the full JSON map from parse time, avoiding a
	// marshal+unmarshal round-trip when extracting unknown fields.
	RawFields map[string]interface{} `json:"-"`

	// TypeMismatches accumulates fields whose JSON type didn't match the struct's
	// expected type during UnmarshalJSON (e.g. number instead of string). Populated
	// per-line; QueryLogs aggregates across a batch and logs a summary so producer
	// bugs are discoverable instead of silently dropped.
	TypeMismatches []ObserveTypeMismatch `json:"-"`
}

// ObserveTypeMismatch records one field whose JSON value didn't match the
// expected typed-struct shape. Sample is a short string rendering of the value
// so an operator can grep producer logs for the offending pattern.
type ObserveTypeMismatch struct {
	Field   string
	GotType string
	Sample  string
}

// jsonTypeOf returns a human-readable name for the underlying JSON kind of a
// decoded interface{} value, used in TypeMismatch.GotType.
func jsonTypeOf(v interface{}) string {
	switch v.(type) {
	case nil:
		return "null"
	case bool:
		return "bool"
	case float64:
		return "number"
	case string:
		return "string"
	case []interface{}:
		return "array"
	case map[string]interface{}:
		return "object"
	default:
		return fmt.Sprintf("%T", v)
	}
}

// observeMismatchSampleMax caps the per-mismatch Sample length so a giant
// stringified payload can't dominate a log line.
const observeMismatchSampleMax = 80

// UnmarshalJSON parses the JSON payload once into a map and extracts the typed
// fields from it, instead of unmarshaling the same bytes twice (once into the
// typed struct and once into a map). Avoids ~2x JSON parse cost on a hot log
// ingest path. Type mismatches on individual fields are tolerated (field stays
// zero-valued) and recorded in TypeMismatches so the caller can surface them.
func (o *ObserveLog) UnmarshalJSON(data []byte) error {
	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	o.RawFields = raw

	recordMismatch := func(field string, v interface{}) {
		sample := fmt.Sprintf("%v", v)
		if len(sample) > observeMismatchSampleMax {
			sample = sample[:observeMismatchSampleMax] + "..."
		}
		o.TypeMismatches = append(o.TypeMismatches, ObserveTypeMismatch{
			Field:   field,
			GotType: jsonTypeOf(v),
			Sample:  sample,
		})
	}

	getStr := func(key string) string {
		v, present := raw[key]
		if !present || v == nil {
			return ""
		}
		if s, ok := v.(string); ok {
			return s
		}
		recordMismatch(key, v)
		return ""
	}
	getMap := func(key string) map[string]any {
		v, present := raw[key]
		if !present || v == nil {
			return nil
		}
		if m, ok := v.(map[string]any); ok {
			return m
		}
		recordMismatch(key, v)
		return nil
	}

	o.Body = getStr("body")
	o.Cluster = getStr("cluster")
	o.Attributes = getMap("attributes")
	o.Container = getStr("container")
	o.Fields = getMap("fields")
	o.Meta = getMap("meta")
	o.Namespace = getStr("namespace")
	o.Node = getStr("node")
	o.Pod = getStr("pod")
	o.ResourceAttributes = getMap("resource_attributes")
	o.Stream = getStr("stream")
	o.Timestamp = getStr("timestamp")
	o.Level = getStr("level")
	o.Message = getStr("message")
	o.LoggerName = getStr("loggerName")
	o.SleuthSpanId = getStr("sleuthSpanId")
	o.SleuthTraceId = getStr("sleuthTraceId")
	o.ApplicationName = getStr("applicationName")
	o.Host = getStr("host")
	o.Tags = raw["tags"]
	o.Thread = getStr("thread")
	o.UserId = getStr("userId")
	o.TenantId = getStr("tenantId")
	o.DataStream = getMap("data_stream")
	o.Thrown = getMap("thrown")
	o.ExtraData = getStr("extraData")
	o.FieldsUppercase = getMap("FIELDS")

	return nil
}

// ObserveSource is a LogSource implementation for Observe.
type ObserveSource struct{}

func operatorToOpal(op query.BinaryWhereClauseType) (string, bool, error) {
	switch op {
	case query.Eq:
		return "=", false, nil
	case query.Nq:
		return "!=", false, nil
	case query.Like:
		return "like", true, nil
	case query.Contains:
		return "~", false, nil
	default:
		return "", false, fmt.Errorf("unsupported operator: %s", op)
	}
}

func (m *ObserveSource) exprString(q query.QueryWhereClause) (string, error) {
	var parts []string
	for field, ops := range q.Binary {
		for op, value := range ops {
			switch v := value.(type) {
			case string:
				exp, funcTypeOpr, err := operatorToOpal(op)
				if err != nil {
					return "", err
				}
				if funcTypeOpr {
					parts = append(parts, fmt.Sprintf(`%s(%s, %q)`, exp, field, v))
				} else {
					parts = append(parts, fmt.Sprintf(`%s %s %q`, field, exp, v))
				}
			case []any:
				vals := make([]string, len(v))
				for i, item := range v {
					vals[i] = fmt.Sprintf(`"%v"`, item)
				}
				exp, _, err := operatorToOpal(op)
				if err != nil {
					return "", err
				}
				parts = append(parts, fmt.Sprintf(`%s %s (%s)`, field, exp, strings.Join(vals, ", ")))
			default:
				exp, _, err := operatorToOpal(op)
				if err != nil {
					return "", err
				}
				parts = append(parts, fmt.Sprintf(`%s %s %v`, field, exp, v))
			}
		}
	}

	if len(q.And) > 0 {
		andParts := make([]string, len(q.And))
		for i, clause := range q.And {
			op, err := m.exprString(clause)
			if err != nil {
				return "", err
			}
			andParts[i] = op
		}
		parts = append(parts, "("+strings.Join(andParts, " and ")+")")
	}

	if len(q.Or) > 0 {
		orParts := make([]string, len(q.Or))
		for i, clause := range q.Or {
			op, err := m.exprString(clause)
			if err != nil {
				return "", err
			}
			orParts[i] = op
		}
		parts = append(parts, "("+strings.Join(orParts, " or ")+")")
	}

	if q.Not != nil {
		op, err := m.exprString(*q.Not)
		if err != nil {
			return "", err
		}
		parts = append(parts, fmt.Sprintf("not(%s)", op))
	}

	return strings.Join(parts, " and "), nil
}

func (m *ObserveSource) ToOpal(q query.QueryWhereClause) (string, error) {
	filters := ""
	op, err := m.exprString(q)
	if err != nil {
		return "", err
	}
	if op != "" {
		filters = "filter " + op
	}
	return filters, nil
}

func (s *ObserveSource) GetLabelMapping() map[string]string {
	return map[string]string{}
}

func (s *ObserveSource) GetSupportedOperators() []string {
	return []string{"_eq", "_neq", "_like", "_contains"}
}

func (s *ObserveSource) QueryLogs(ctx *security.RequestContext, fetchLogRequest FetchLogRequest) ([]OutputLog, error) {
	obsrvCnf, err := integrations.GetObserveConfigs(ctx, fetchLogRequest.AccountId)
	if err != nil {
		ctx.GetLogger().Error("observe: failed to get observe configs", "error", err)
		return nil, fmt.Errorf("failed to get observe configs: %w", err)
	}
	observeQuery := fetchLogRequest.Query
	if observeQuery == "" {
		observeQuery, err = s.ToOpal(fetchLogRequest.QueryRequest.Where)
		if err != nil {
			return nil, fmt.Errorf("failed to convert query to opal: %w", err)
		}
	}
	ctx.GetLogger().Info("observe: executing observe log query", "query", observeQuery)

	observ := integrations.Observe{}
	if err != nil {
		return nil, fmt.Errorf("failed to get observe configs: %w", err)
	}
	token, err := observ.GetToken(obsrvCnf)
	if err != nil {
		return nil, fmt.Errorf("failed to get token: %w", err)
	}
	rowCount := "1000"
	if fetchLogRequest.Limit > 0 {
		rowCount = strconv.Itoa(fetchLogRequest.Limit)
	}

	dataSetId := obsrvCnf.LogDatasetID

	if fetchLogRequest.Request["dataset_id"] != nil {
		dataSetId = fetchLogRequest.Request["dataset_id"].(string)
	}

	// Split comma-separated dataset IDs
	datasetIds := strings.Split(dataSetId, ",")
	for i := range datasetIds {
		datasetIds[i] = strings.TrimSpace(datasetIds[i])
	}

	allowMultipleDataset := false
	if v, ok := fetchLogRequest.Request["allow_multiple_dataset"].(bool); ok {
		allowMultipleDataset = v
	}

	var inputs []integrations.ObserveInput
	pipeline := observeQuery

	if allowMultipleDataset && len(datasetIds) > 1 {
		// Multiple datasets: create an input per dataset and union them via OPAL
		for i, id := range datasetIds {
			inputs = append(inputs, integrations.ObserveInput{
				InputName: fmt.Sprintf("ds%d", i+1),
				DatasetId: id,
			})
		}
		unionRefs := make([]string, 0, len(datasetIds)-1)
		for i := 1; i < len(datasetIds); i++ {
			unionRefs = append(unionRefs, fmt.Sprintf("@ds%d", i+1))
		}
		pipeline = fmt.Sprintf("union %s\n%s", strings.Join(unionRefs, ", "), observeQuery)
	} else {
		// Default: use only the first dataset ID
		inputs = []integrations.ObserveInput{
			{InputName: "main", DatasetId: datasetIds[0]},
		}
	}

	executeQueryURL := fmt.Sprintf("%s/v1/meta/export/query", observ.BaseURL(obsrvCnf))
	logApiRequest := integrations.ObserveLogApiRequest{
		Query: integrations.ObserveQuery{
			OutputStage: "output_stage",
			Stages: []integrations.ObserveStage{
				{
					Input:    inputs,
					StageID:  "output_stage",
					Pipeline: pipeline,
				},
			},
		},
		RowCount: rowCount,
	}

	params := map[string]string{
		"startTime": time.UnixMilli(fetchLogRequest.StartTime).UTC().Format(time.RFC3339),
		"endTime":   time.UnixMilli(fetchLogRequest.EndTime).UTC().Format(time.RFC3339)}
	headers := map[string]string{
		"Content-Type":  "application/json",
		"Accept":        "application/x-ndjson",
		"Authorization": fmt.Sprintf("Bearer %s %s", obsrvCnf.CustomerID, token),
	}
	jsonBody := common.HttpWithJsonBody(logApiRequest)
	res, err := common.HttpPost(executeQueryURL, common.HttpWithHeaders(headers), common.HttpWithQueryParams(params), jsonBody)
	if err != nil {
		ctx.GetLogger().Error("observe: failed to execute observe log query", "error", err)
		return nil, fmt.Errorf("failed to execute observe log query: %w", err)
	}

	results := make([]ObserveLog, 0)
	scanner := bufio.NewScanner(res.Body)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 10*1024*1024) // max token size 10 MB
	// Aggregate type mismatches across the batch. Keyed by (field, got_type) so
	// repeated patterns coalesce; one Sample is retained per pattern for grepping.
	type mismatchKey struct{ field, gotType string }
	mismatchCounts := map[mismatchKey]int{}
	mismatchSamples := map[mismatchKey]string{}
	for scanner.Scan() {
		data := scanner.Bytes()

		var obj ObserveLog
		// ObserveLog.UnmarshalJSON does a single map-unmarshal then extracts
		// typed fields from it, populating RawFields as a side effect.
		if err := json.Unmarshal(data, &obj); err != nil {
			ctx.GetLogger().Warn("failed to unmarshal ObserveLog", "error", err)
			continue
		}
		for _, m := range obj.TypeMismatches {
			k := mismatchKey{m.Field, m.GotType}
			if _, seen := mismatchSamples[k]; !seen {
				mismatchSamples[k] = m.Sample
			}
			mismatchCounts[k]++
		}
		obj.TypeMismatches = nil // don't carry per-line slice into results

		if obj.Timestamp == "" {
			if ok, hasOk := obj.RawFields["ok"].(bool); hasOk && !ok {
				if msg, hasMsg := obj.RawFields["message"].(string); hasMsg {
					return nil, fmt.Errorf("API error: %s", msg)
				}
				return nil, errors.New("API returned error without message")
			}
			continue
		}
		results = append(results, obj)
	}
	if len(mismatchCounts) > 0 {
		// One log line per distinct (field, got_type) pair, with count + sample —
		// enough to identify the producer pattern without spamming per-line.
		summary := make([]map[string]any, 0, len(mismatchCounts))
		for k, count := range mismatchCounts {
			summary = append(summary, map[string]any{
				"field":    k.field,
				"got_type": k.gotType,
				"count":    count,
				"sample":   mismatchSamples[k],
			})
		}
		ctx.GetLogger().Warn("observe: type mismatches in log batch (silently coerced to zero values)", "mismatches", summary)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scanner error: %w", err)
	}
	outputLog, err := s.convertObserveToOutputLogs(results)
	if err != nil {
		ctx.GetLogger().Error("observe: failed to get observe configs", "error", err)
		return nil, fmt.Errorf("failed to get observe configs: %w", err)
	}

	return outputLog, nil
}

func (s *ObserveSource) QueryLabels(ctx *security.RequestContext, fetchLogRequest FetchLogLabelRequest) ([]OutputLogLabel, error) {
	observeConf, err := integrations.GetObserveConfigs(ctx, fetchLogRequest.AccountId)
	if err != nil {
		return nil, fmt.Errorf("failed to get observe configs: %w", err)
	}
	observe := integrations.Observe{}
	token, err := observe.GetToken(observeConf)
	if err != nil {
		return nil, fmt.Errorf("failed to get token: %w", err)
	}

	url := fmt.Sprintf("%s/v1/meta?queryName=DatasetSearch", observe.BaseURL(observeConf))

	payload := map[string]any{
		"operationName": "DatasetSearch",
		"variables": map[string]any{
			"withInputs": false,
		},
		"query": "query DatasetSearch($labelMatches: [String!], $projects: [ObjectId!], $columnMatches: [String!], $keyMatchTypes: [String!], $foreignKeyTargetMatches: [String!], $reachableFromDataset: ObjectId, $withInputs: Boolean!) {\n  datasetSearch(\n    labelMatches: $labelMatches\n    projects: $projects\n    columnMatches: $columnMatches\n    keyMatchTypes: $keyMatchTypes\n    foreignKeyTargetMatches: $foreignKeyTargetMatches\n    reachableFromDataset: $reachableFromDataset\n  ) {\n    dataset {\n      ...DatasetSearch\n      inputs @include(if: $withInputs) {\n        datasetId\n        inputRole\n        __typename\n      }\n      __typename\n    }\n    __typename\n  }\n}\n\nfragment WorkspaceEntity on WorkspaceObject {\n  id\n  name\n  description\n  iconUrl\n  workspaceId\n  managedById\n  __typename\n}\n\nfragment LinkDesc on LinkSchema {\n  targetDataset\n  targetStageLabel\n  targetLabelField\n  label\n  src {\n    column\n    path\n    __typename\n  }\n  dstFields\n  __typename\n}\n\nfragment DatasetFieldDesc on FieldDesc {\n  name\n  type {\n    tag\n    __typename\n  }\n  indexDefs {\n    __typename\n    column\n  }\n  linkDesc {\n    ...LinkDesc\n    __typename\n  }\n  isEnum\n  isSearchable\n  isHidden\n  isConst\n  isMetric\n  __typename\n}\n\nfragment DatasetCorrelationTag on CorrelationTagMapping {\n  tag\n  path {\n    column\n    path\n    __typename\n  }\n  __typename\n}\n\nfragment DatasetSearch on Dataset {\n...WorkspaceEntity\nfieldList {\n...DatasetFieldDesc\n__typename\n}\ncorrelationTagMappings {\n...DatasetCorrelationTag\n__typename\n}\neffectiveSettings {\ndataset {\nfreshnessDesired\n__typename\n}\n__typename\n}\n__typename}",
	}

	if err != nil {
		ctx.GetLogger().Error("error creating request", "error", err)
		return nil, fmt.Errorf("error creating request: %w", err)
	}
	headers := map[string]string{
		"Content-Type":  "application/json",
		"Authorization": fmt.Sprintf("Bearer %s %s", observeConf.CustomerID, token),
	}

	jsonBody := common.HttpWithJsonBody(payload)
	res, err := common.HttpPost(url, common.HttpWithHeaders(headers), jsonBody)
	if err != nil {
		ctx.GetLogger().Error("error making request", "error", err)
		return nil, fmt.Errorf("error making request: %w", err)
	}
	defer func() {
		if err := res.Body.Close(); err != nil {
			ctx.GetLogger().Error("error closing response body", "error", err)
		}
	}()

	body, err := io.ReadAll(res.Body)
	if err != nil {
		ctx.GetLogger().Error("error reading response body", "error", err)
		return nil, fmt.Errorf("error reading response body: %w", err)
	}
	var data map[string]map[string]any
	err = json.Unmarshal(body, &data)
	if err != nil {
		return nil, fmt.Errorf("not able to unmarshal data: %w", err)
	}
	var output []OutputLogLabel
	dataMap := data["data"]
	if dataMap == nil {
		return output, nil
	}
	datasetSearch, ok := dataMap["datasetSearch"].([]any)
	if !ok {
		return output, nil
	}
	for _, v := range datasetSearch {
		var dataSet map[string]any
		vJson, err := json.Marshal(v.(map[string]any)["dataset"])
		if err != nil {
			return nil, fmt.Errorf("not able to marshal data: %w", err)
		}
		err = json.Unmarshal(vJson, &dataSet)
		if err != nil {
			return nil, fmt.Errorf("not able to unmarshal data: %w", err)
		}
		id, ok := dataSet["id"]
		firstDatasetId := strings.TrimSpace(strings.Split(observeConf.LogDatasetID, ",")[0])
		if ok && id.(string) == firstDatasetId {
			fields := dataSet["fieldList"].([]any)
			for _, f := range fields {
				field := f.(map[string]any)
				output = append(output, OutputLogLabel{
					Label:      field["name"].(string),
					Attributes: map[string]any{"datatype": field["type"].(map[string]any)["tag"].(string)},
				})
			}
		}

	}
	return output, nil
}

func (s *ObserveSource) QueryLabelValues(ctx *security.RequestContext, fetchLogRequest FetchLogLabelValuesRequest) ([]OutputLogLabelValue, error) {
	return nil, fmt.Errorf("not implemented")
}

func (s *ObserveSource) convertObserveToOutputLogs(observeLog []ObserveLog) ([]OutputLog, error) {
	// Define known fields that are handled explicitly
	knownFields := map[string]bool{
		"body": true, "cluster": true, "attributes": true, "container": true,
		"fields": true, "meta": true, "namespace": true, "node": true,
		"pod": true, "resource_attributes": true, "stream": true, "timestamp": true,
		"level": true, "message": true, "loggerName": true, "sleuthSpanId": true,
		"sleuthTraceId": true, "applicationName": true, "host": true, "tags": true,
		"thread": true, "userId": true, "tenantId": true, "data_stream": true,
		"thrown": true, "extraData": true, "FIELDS": true,
	}

	outputLogs := []OutputLog{}

	for _, log := range observeLog {
		labels := make(map[string]interface{})

		// Extract unknown fields from the raw map captured at parse time
		for key, value := range log.RawFields {
			if !knownFields[key] && value != nil {
				if strVal, ok := value.(string); ok && strVal == "" {
					continue
				}
				labels[key] = value
			}
		}

		// Merge Attributes
		for key, value := range log.Attributes {
			labels[key] = value
		}

		// Merge ResourceAttributes
		for key, value := range log.ResourceAttributes {
			labels[key] = value
		}

		// Merge Meta
		for key, value := range log.Meta {
			labels[key] = value
		}

		// Merge Fields (lowercase)
		for key, value := range log.Fields {
			if key == "timestamp" {
				if timeMilli, err := ParseDateToMillis(fmt.Sprintf("%v", value)); err == nil {
					labels[key] = timeMilli
				}
				continue
			}
			labels[key] = value
		}

		// Merge FIELDS (uppercase) - some logs use uppercase variant
		for key, value := range log.FieldsUppercase {
			if key == "timestamp" {
				if timeMilli, err := ParseDateToMillis(fmt.Sprintf("%v", value)); err == nil {
					labels[key] = timeMilli
				}
				continue
			}
			labels[key] = value
		}

		// Add standard Kubernetes labels
		if log.Cluster != "" {
			labels["cluster"] = log.Cluster
		}
		if log.Container != "" {
			labels["container"] = log.Container
		}
		if log.Namespace != "" {
			labels["namespace"] = log.Namespace
		}
		if log.Node != "" {
			labels["node"] = log.Node
		}
		if log.Pod != "" {
			labels["pod"] = log.Pod
		}
		if log.Stream != "" {
			labels["stream"] = log.Stream
		}

		// Add application metadata
		if log.ApplicationName != "" {
			labels["application_name"] = log.ApplicationName
		}
		if log.Host != "" {
			labels["host"] = log.Host
		}
		if log.LoggerName != "" {
			labels["logger_name"] = log.LoggerName
		}
		if log.Thread != "" {
			labels["thread"] = log.Thread
		}
		if log.UserId != "" {
			labels["user_id"] = log.UserId
		}
		if log.TenantId != "" {
			labels["tenant_id"] = log.TenantId
		}

		// Add distributed tracing context
		if log.SleuthTraceId != "" {
			labels["trace_id"] = log.SleuthTraceId
		}
		if log.SleuthSpanId != "" {
			labels["span_id"] = log.SleuthSpanId
		}

		// Add nested objects as labels
		if len(log.Meta) > 0 {
			labels["meta"] = log.Meta
		}
		if len(log.Fields) > 0 {
			labels["fields"] = log.Fields
		}
		if len(log.FieldsUppercase) > 0 {
			labels["FIELDS"] = log.FieldsUppercase
		}
		if len(log.DataStream) > 0 {
			labels["data_stream"] = log.DataStream
		}
		if log.Tags != nil {
			labels["tags"] = log.Tags
		}
		if len(log.Thrown) > 0 {
			labels["thrown"] = log.Thrown
		}
		if log.ExtraData != "" {
			labels["extra_data"] = log.ExtraData
		}

		// Smart fallback for message content: prefer Message field, fallback to Body
		logMessage := log.Message
		if logMessage == "" {
			logMessage = log.Body
		}

		// Extract thrown object - check both top-level and FIELDS.thrown
		thrownData := log.Thrown
		if len(thrownData) == 0 && len(log.FieldsUppercase) > 0 {
			if fieldsThrown, ok := log.FieldsUppercase["thrown"].(map[string]any); ok {
				thrownData = fieldsThrown
			}
		}

		// Enhance message with exception details if present
		if len(thrownData) > 0 {
			exceptionName := ""
			exceptionMsg := ""

			if name, ok := thrownData["name"].(string); ok && name != "" {
				exceptionName = name
			}
			if msg, ok := thrownData["message"].(string); ok && msg != "" {
				exceptionMsg = msg
			}

			// Build enhanced message: "original message | ExceptionName: exception message"
			if exceptionName != "" && exceptionMsg != "" {
				logMessage = fmt.Sprintf("%s | %s: %s", logMessage, exceptionName, exceptionMsg)
			} else if exceptionName != "" {
				// Include exception name even without message (e.g., NullPointerException)
				logMessage = fmt.Sprintf("%s | %s", logMessage, exceptionName)
			} else if exceptionMsg != "" {
				logMessage = fmt.Sprintf("%s | Exception: %s", logMessage, exceptionMsg)
			}

			// Store full exception details in labels for searchability
			labels["exception_name"] = exceptionName
			labels["exception_message"] = exceptionMsg
			if stackTrace, ok := thrownData["extendedStackTrace"].(string); ok && stackTrace != "" {
				labels["exception_stack_trace"] = stackTrace
			}
		}

		// Smart fallback for severity: prefer Level field, fallback to parsing message
		logSeverity := log.Level
		if logSeverity == "" {
			logSeverity = GetSeverityLevels(logMessage)
		}

		// Parse timestamp
		ts, err := strconv.ParseInt(log.Timestamp, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("failed to parse timestamp %s: %w", log.Timestamp, err)
		}

		outputLog := OutputLog{
			Message:   logMessage,
			Labels:    labels,
			Timestamp: time.Unix(0, ts).UTC().Format(time.RFC3339Nano),
			Severity:  logSeverity,
		}
		outputLogs = append(outputLogs, outputLog)
	}

	return outputLogs, nil
} // extractSeverity attempts to extract severity level from the log message

func (s *ObserveSource) GetQuery(ctx *security.RequestContext, fetchLogRequest FetchLogRequest) (string, error) {
	query, err := s.ToOpal(fetchLogRequest.QueryRequest.Where)
	if err != nil {
		return "", fmt.Errorf("failed to convert query to opal: %w", err)
	}
	return query, nil
}
