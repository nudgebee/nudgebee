package observability

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"nudgebee/services/common"
	"nudgebee/services/integrations/core"
	"nudgebee/services/security"
	"sort"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/cognitoidentity"
	"github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider"
	cidp "github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider/types"
)

type ElasticSaasSource struct{}

const (
	ElasticsearchUrl            = "url"
	ElasticsearchUsername       = "username"
	ElasticsearchPassword       = "password"
	ElasticsearchAuthType       = "auth_type"
	ElasticsearchRegion         = "region"
	ElasticsearchUserPoolId     = "user_pool_id"
	ElasticsearchIdentityPoolId = "identity_pool_id"
	ElasticsearchAppClientId    = "app_client_id"
	ElasticsearchApiKey         = "api_key"
	ElasticsearchBearerToken    = "bearer_token"
	ElasticsearchLogIndex       = "log_index"
	ElasticsearchMetricsIndex   = "metrics_index"
	ElasticsearchTraceIndex     = "trace_index"
	ElasticsearchTLSSkipVerify  = "es_tls_skip_verify"
	// ElasticsearchIndexAccountMapping is the "Advanced Settings" config value: a
	// JSON array of per-account index overrides. When a bound account has an entry,
	// its index wins over the top-level log_index / metrics_index / trace_index.
	ElasticsearchIndexAccountMapping = "index_account_mapping"
)

// esIndexOverride is one per-account row of the index_account_mapping blob.
// Empty fields fall back to the integration's top-level index.
type esIndexOverride struct {
	AccountId    string `json:"account_id"`
	LogIndex     string `json:"log_index"`
	MetricsIndex string `json:"metrics_index"`
	TraceIndex   string `json:"trace_index"`
}

// resolveESIndexOverride returns the per-account index for the given indexType
// ("logs" | "metrics" | "traces") from the index_account_mapping JSON blob, or ""
// when the blob is empty/malformed or has no (matching, non-empty) entry for the
// account. Tolerant by design: a bad blob must never break query resolution — the
// caller falls back to the top-level index on "".
func resolveESIndexOverride(mappingJSON, accountId, indexType string) string {
	mappingJSON = strings.TrimSpace(mappingJSON)
	if mappingJSON == "" || accountId == "" {
		return ""
	}
	var rows []esIndexOverride
	if err := json.Unmarshal([]byte(mappingJSON), &rows); err != nil {
		return ""
	}
	for _, r := range rows {
		if r.AccountId != accountId {
			continue
		}
		switch indexType {
		case "logs":
			return strings.TrimSpace(r.LogIndex)
		case "metrics":
			return strings.TrimSpace(r.MetricsIndex)
		case "traces":
			return strings.TrimSpace(r.TraceIndex)
		}
	}
	return ""
}

type ElasticsearchConfig struct {
	Url            string
	Username       string
	Password       string
	AuthType       string // "basic", "cognito", "api_key", or "bearer_token"
	Region         string
	UserPoolId     string
	IdentityPoolId string
	AppClientId    string
	ApiKey         string // Base64-encoded id:api_key for ES API Key auth
	BearerToken    string // OAuth2 / service-account bearer token
	LogIndex       string // index pattern for log queries; empty means the caller must supply one
	MetricsIndex   string // index pattern for utilisation queries; defaults to "*"
	TraceIndex     string // index pattern for trace queries; defaults to esTraceIndex
	TLSSkipVerify  bool   // user-configured opt-in for self-signed certs
}

// BuildElasticsearchConfigFromValues maps already-decrypted config values into an
// ElasticsearchConfig. Used both by GetElasticsearchConfig (saved integration) and by
// the "list indices from config values" endpoint (add flow, before the integration is
// saved). It does not apply the per-account index override — that needs an accountId.
func BuildElasticsearchConfigFromValues(values []core.IntegrationConfigValue) *ElasticsearchConfig {
	cfg := &ElasticsearchConfig{AuthType: "basic"}
	for _, c := range values {
		switch c.Name {
		case ElasticsearchUrl:
			cfg.Url = c.Value
		case ElasticsearchUsername:
			cfg.Username = c.Value
		case ElasticsearchPassword:
			cfg.Password = c.Value
		case ElasticsearchAuthType:
			cfg.AuthType = c.Value
		case ElasticsearchRegion:
			cfg.Region = c.Value
		case ElasticsearchUserPoolId:
			cfg.UserPoolId = c.Value
		case ElasticsearchIdentityPoolId:
			cfg.IdentityPoolId = c.Value
		case ElasticsearchAppClientId:
			cfg.AppClientId = c.Value
		case ElasticsearchApiKey:
			cfg.ApiKey = c.Value
		case ElasticsearchBearerToken:
			cfg.BearerToken = c.Value
		case ElasticsearchLogIndex:
			cfg.LogIndex = c.Value
		case ElasticsearchMetricsIndex:
			cfg.MetricsIndex = c.Value
		case ElasticsearchTraceIndex:
			cfg.TraceIndex = c.Value
		case ElasticsearchTLSSkipVerify:
			cfg.TLSSkipVerify = strings.EqualFold(strings.TrimSpace(c.Value), "true")
		}
	}
	cfg.Url = strings.TrimRight(strings.TrimSpace(cfg.Url), "/")
	if cfg.AuthType == "" {
		cfg.AuthType = "basic"
	}
	return cfg
}

// validateESConfig checks that a resolved config carries the fields its auth
// method requires. Split out from GetElasticsearchConfig so the per-auth-type
// requirements are unit-testable without a live integration record.
func validateESConfig(cfg *ElasticsearchConfig) error {
	if cfg.Url == "" {
		return fmt.Errorf("missing required elasticsearch URL")
	}
	switch cfg.AuthType {
	case "api_key":
		if cfg.ApiKey == "" {
			return fmt.Errorf("missing api_key for auth_type 'api_key'")
		}
	case "bearer_token":
		if cfg.BearerToken == "" {
			return fmt.Errorf("missing bearer_token for auth_type 'bearer_token'")
		}
	default: // "basic", "cognito"
		if cfg.Username == "" || cfg.Password == "" {
			return fmt.Errorf("missing required elasticsearch username/password")
		}
	}
	return nil
}

func GetElasticsearchConfig(ctx *security.RequestContext, accountId string) (*ElasticsearchConfig, error) {
	integrationDto, err := core.ListIntegrationConfigs(ctx, accountId, "ES")
	if err != nil {
		return nil, fmt.Errorf("failed to get elasticsearch integration: %w", err)
	}

	// Filter for source="user" since both agent and user ES integrations share type "ES".
	// Only user-sourced integrations have URL/auth config needed here.
	var userIntegrations []core.IntegrationDto
	for _, dto := range integrationDto {
		if dto.Source == "user" {
			userIntegrations = append(userIntegrations, dto)
		}
	}
	if len(userIntegrations) == 0 {
		return nil, errors.New("no elasticsearch integrations found")
	}

	integration := userIntegrations[0]

	// Decrypt config values (secrets round-trip as ciphertext), capturing the
	// per-account index override blob for the accountId-specific resolution below.
	values := make([]core.IntegrationConfigValue, 0, len(integration.Configs))
	var indexAccountMapping string
	for _, c := range integration.Configs {
		value := c.Value
		if c.IsEncrypted && value != "" {
			decrypted, err := common.Decrypt(value)
			if err != nil {
				return nil, fmt.Errorf("failed to decrypt config %s: %w", c.Name, err)
			}
			value = decrypted
		}
		if c.Name == ElasticsearchIndexAccountMapping {
			indexAccountMapping = value
		}
		values = append(values, core.IntegrationConfigValue{Name: c.Name, Value: value})
	}

	cfg := BuildElasticsearchConfigFromValues(values)

	// Advanced Settings: a per-account index override wins over the top-level
	// log_index / metrics_index / trace_index for this account. These become the
	// query-time defaults when the request doesn't carry an explicit index.
	if override := resolveESIndexOverride(indexAccountMapping, accountId, "logs"); override != "" {
		cfg.LogIndex = override
	}
	if override := resolveESIndexOverride(indexAccountMapping, accountId, "metrics"); override != "" {
		cfg.MetricsIndex = override
	}
	if override := resolveESIndexOverride(indexAccountMapping, accountId, "traces"); override != "" {
		cfg.TraceIndex = override
	}

	if err := validateESConfig(cfg); err != nil {
		return nil, err
	}
	cfg.Url = strings.TrimRight(cfg.Url, "/")

	if cfg.AuthType == "" {
		cfg.AuthType = "basic"
	}

	return cfg, nil
}

// getCognitoAWSCredentials authenticates via Cognito USER_PASSWORD_AUTH and returns temporary AWS credentials.
func getCognitoAWSCredentials(cfg *ElasticsearchConfig) (aws.Credentials, error) {
	ctx := context.Background()

	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(cfg.Region))
	if err != nil {
		return aws.Credentials{}, fmt.Errorf("failed to load AWS config: %w", err)
	}

	// Step 1: Authenticate with Cognito User Pool
	idpClient := cognitoidentityprovider.NewFromConfig(awsCfg)
	authOutput, err := idpClient.InitiateAuth(ctx, &cognitoidentityprovider.InitiateAuthInput{
		AuthFlow: cidp.AuthFlowTypeUserPasswordAuth,
		ClientId: aws.String(cfg.AppClientId),
		AuthParameters: map[string]string{
			"USERNAME": cfg.Username,
			"PASSWORD": cfg.Password,
		},
	})
	if err != nil {
		return aws.Credentials{}, fmt.Errorf("cognito InitiateAuth failed: %w", err)
	}
	if authOutput.AuthenticationResult == nil || authOutput.AuthenticationResult.IdToken == nil {
		return aws.Credentials{}, fmt.Errorf("cognito InitiateAuth returned no ID token")
	}
	idToken := *authOutput.AuthenticationResult.IdToken

	// Step 2: Get Identity ID from Identity Pool
	idClient := cognitoidentity.NewFromConfig(awsCfg)
	loginKey := fmt.Sprintf("cognito-idp.%s.amazonaws.com/%s", cfg.Region, cfg.UserPoolId)

	getIdOutput, err := idClient.GetId(ctx, &cognitoidentity.GetIdInput{
		IdentityPoolId: aws.String(cfg.IdentityPoolId),
		Logins:         map[string]string{loginKey: idToken},
	})
	if err != nil {
		return aws.Credentials{}, fmt.Errorf("cognito GetId failed: %w", err)
	}

	// Step 3: Get temporary AWS credentials
	credsOutput, err := idClient.GetCredentialsForIdentity(ctx, &cognitoidentity.GetCredentialsForIdentityInput{
		IdentityId: getIdOutput.IdentityId,
		Logins:     map[string]string{loginKey: idToken},
	})
	if err != nil {
		return aws.Credentials{}, fmt.Errorf("cognito GetCredentialsForIdentity failed: %w", err)
	}
	if credsOutput.Credentials == nil {
		return aws.Credentials{}, fmt.Errorf("cognito returned no credentials")
	}

	c := credsOutput.Credentials
	return aws.Credentials{
		AccessKeyID:     aws.ToString(c.AccessKeyId),
		SecretAccessKey: aws.ToString(c.SecretKey),
		SessionToken:    aws.ToString(c.SessionToken),
		CanExpire:       true,
		Expires:         aws.ToTime(c.Expiration),
	}, nil
}

func basicAuthHeader(username, password string) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(username+":"+password))
}

// esVerifyClient is the default HTTP client for ES/OpenSearch — TLS verified.
var esVerifyClient = &http.Client{Timeout: 30 * time.Second}

// esSkipVerifyClient is used only when the user has explicitly set es_tls_skip_verify=true.
var esSkipVerifyClient = func() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // explicit user opt-in, default false
	return &http.Client{
		Transport: transport,
		Timeout:   30 * time.Second,
	}
}()

// esRequest executes an HTTP request to OpenSearch with the configured auth method.
func esRequest(method, rawURL, body string, cfg *ElasticsearchConfig) (*http.Response, error) {
	var bodyReader io.Reader
	if body != "" {
		bodyReader = strings.NewReader(body)
	}

	req, err := http.NewRequest(method, rawURL, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	switch cfg.AuthType {
	case "cognito":
		creds, err := getCognitoAWSCredentials(cfg)
		if err != nil {
			return nil, err
		}

		signer := v4.NewSigner()
		payloadHash := hashPayload(body)
		err = signer.SignHTTP(context.Background(), creds, req, payloadHash, "es", cfg.Region, time.Now())
		if err != nil {
			return nil, fmt.Errorf("failed to sign request with SigV4: %w", err)
		}

	case "api_key":
		req.Header.Set("Authorization", "ApiKey "+cfg.ApiKey)
	case "bearer_token":
		req.Header.Set("Authorization", "Bearer "+cfg.BearerToken)
	default: // "basic"
		req.Header.Set("Authorization", basicAuthHeader(cfg.Username, cfg.Password))
	}

	client := esVerifyClient
	if cfg.TLSSkipVerify {
		client = esSkipVerifyClient
	}
	return client.Do(req)
}

// esRequestJSON executes an HTTP request with a JSON body to OpenSearch.
func esRequestJSON(method, url string, jsonBody any, cfg *ElasticsearchConfig) (*http.Response, error) {
	data, err := json.Marshal(jsonBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request body: %w", err)
	}
	return esRequest(method, url, string(data), cfg)
}

func hashPayload(payload string) string {
	h := sha256.New()
	h.Write([]byte(payload))
	return hex.EncodeToString(h.Sum(nil))
}

// readResponse reads and validates the HTTP response body.
func readResponse(resp *http.Response, operation string) ([]byte, error) {
	defer func() { _ = resp.Body.Close() }()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read %s response body: %w", operation, err)
	}

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("%s failed with status %d: %s", operation, resp.StatusCode, string(bodyBytes))
	}

	return bodyBytes, nil
}

func (e *ElasticSaasSource) GetLabelMapping() map[string]string {
	return map[string]string{}
}

func (e *ElasticSaasSource) GetSupportedOperators() []string {
	return []string{"_eq", "_neq", "_contains", "_like", "_ilike", "_nlike", "_gt", "_lt", "_is_null"}
}

func (e *ElasticSaasSource) GetQuery(ctx *security.RequestContext, fetchLogRequest FetchLogRequest) (string, error) {
	return buildESQueryFromWhere(fetchLogRequest.QueryRequest.Where)
}

// defaultESLogQuerySize bounds a log search when the request carries no limit,
// so we never fall through to Elasticsearch's default of 10 hits.
const defaultESLogQuerySize = 1000

// esLogTimeRangeClause bounds a log search on @timestamp. Each boundary is added
// only when supplied (> 0), so a one-sided window (e.g. "from start to now")
// still bounds the scan instead of pinning the missing edge to epoch 0.
func esLogTimeRangeClause(startMillis, endMillis int64) map[string]any {
	ts := map[string]any{"format": "epoch_millis"}
	if startMillis > 0 {
		ts["gte"] = startMillis
	}
	if endMillis > 0 {
		ts["lte"] = endMillis
	}
	return map[string]any{"range": map[string]any{"@timestamp": ts}}
}

// buildESLogSort renders sort_fields into an ES sort clause, defaulting to newest
// first (@timestamp desc) when none are supplied or all are blank.
func buildESLogSort(sortFields []SortField) []any {
	sort := make([]any, 0, len(sortFields))
	for _, sf := range sortFields {
		if sf.ColumnName == "" {
			continue
		}
		order := strings.ToLower(sf.Order)
		if order != "asc" && order != "desc" {
			order = "desc"
		}
		sort = append(sort, map[string]any{sf.ColumnName: map[string]any{"order": order}})
	}
	if len(sort) == 0 {
		return []any{map[string]any{"@timestamp": map[string]any{"order": "desc"}}}
	}
	return sort
}

// finalizeESLogQueryBody parses the rendered log query (the WHERE-built
// {"query": ...} or a raw DSL body) and applies the request's time window,
// limit, offset and sort. Without this the builder path produced only the
// query clause, leaving Elasticsearch to default to size 10, no time bound and
// index order — so the start/end/limit on the request were silently ignored.
// A size/from/sort already present in a raw DSL body is respected; whenever a
// start or end is supplied the @timestamp range is AND-merged so scans stay bounded.
func finalizeESLogQueryBody(queryJSON string, startMillis, endMillis int64, limit, offset int, sortFields []SortField) (map[string]any, error) {
	body := map[string]any{}
	if strings.TrimSpace(queryJSON) != "" {
		if err := json.Unmarshal([]byte(queryJSON), &body); err != nil {
			return nil, fmt.Errorf("failed to parse log query body: %w", err)
		}
		if body == nil {
			body = map[string]any{}
		}
	}

	if startMillis > 0 || endMillis > 0 {
		userQuery, ok := body["query"].(map[string]any)
		if !ok {
			userQuery = map[string]any{"match_all": map[string]any{}}
		}
		body["query"] = map[string]any{
			"bool": map[string]any{
				"filter": []any{userQuery, esLogTimeRangeClause(startMillis, endMillis)},
			},
		}
	}

	if _, ok := body["size"]; !ok {
		if limit > 0 {
			body["size"] = limit
		} else {
			body["size"] = defaultESLogQuerySize
		}
	}
	if _, ok := body["from"]; !ok && offset > 0 {
		body["from"] = offset
	}
	if _, ok := body["sort"]; !ok {
		body["sort"] = buildESLogSort(sortFields)
	}
	return body, nil
}

// parseESSearchLogs decodes a standard ES _search response into OutputLogs,
// dropping hits ParseSourceMap can't interpret. Shared by the dsl and kql
// query paths, which differ only in how the request body is built.
func parseESSearchLogs(rawJSON string) ([]OutputLog, error) {
	var searchResp SearchResponse
	if err := json.Unmarshal([]byte(rawJSON), &searchResp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal DSL response: %w", err)
	}
	output := make([]OutputLog, 0, len(searchResp.Hits.Hits))
	for _, hit := range searchResp.Hits.Hits {
		if log, ok := ParseSourceMap(hit.Source); ok {
			output = append(output, log)
		}
	}
	return output, nil
}

// parseESPPLLogs decodes an OpenSearch PPL response (schema + datarows) into
// OutputLogs by zipping each row against the column names before ParseSourceMap.
func parseESPPLLogs(rawJSON string) ([]OutputLog, error) {
	var pplResp PPLResponse
	if err := json.Unmarshal([]byte(rawJSON), &pplResp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal PPL response: %w", err)
	}
	output := make([]OutputLog, 0, len(pplResp.DataRows))
	colNames := make([]string, len(pplResp.Schema))
	for i, col := range pplResp.Schema {
		colNames[i] = col.Name
	}
	for _, row := range pplResp.DataRows {
		src := make(map[string]any)
		for i, val := range row {
			if i < len(colNames) {
				src[colNames[i]] = val
			}
		}
		if log, ok := ParseSourceMap(src); ok {
			output = append(output, log)
		}
	}
	return output, nil
}

func (e *ElasticSaasSource) QueryLogs(ctx *security.RequestContext, fetchLogRequest FetchLogRequest) ([]OutputLog, error) {
	// A where clause that failed to render must never reach the cluster as an
	// unfiltered search. FetchLogs treats a GetQuery error as non-fatal — some
	// providers (Signoz, Datadog) apply the where clause natively inside
	// QueryLogs — and leaves Query empty; Elasticsearch has no such path, the
	// rendered body is the only place a filter can live, and
	// finalizeESLogQueryBody turns an empty body into match_all. The caller
	// would then get every document in the time window back as though the
	// filter had applied. Re-run the render here so the reason it failed (e.g.
	// "_is_null operator requires boolean value") reaches the UI instead of
	// wrong rows.
	if strings.TrimSpace(fetchLogRequest.Query) == "" && hasWhereData(fetchLogRequest.QueryRequest.Where) {
		_, qErr := e.GetQuery(ctx, fetchLogRequest)
		if qErr == nil {
			qErr = errors.New("filter rendered to an empty query")
		}
		return nil, fmt.Errorf("elasticsearch: refusing to run an unfiltered search: %w", qErr)
	}

	cfg, err := GetElasticsearchConfig(ctx, fetchLogRequest.AccountId)
	if err != nil {
		return nil, err
	}

	var queryType string
	if fetchLogRequest.Request != nil {
		queryType, _ = fetchLogRequest.Request["query_type"].(string)
	}
	if queryType == "" {
		queryType = "dsl"
	}

	switch queryType {
	case "dsl", "kql":
		index, _ := fetchLogRequest.Request["index"].(string)
		if index == "" {
			// No explicit index in the request — fall back to the account's default:
			// per-account index_account_mapping override → top-level log_index
			// (both resolved into cfg.LogIndex by GetElasticsearchConfig).
			index = cfg.LogIndex
		}
		if index == "" {
			return nil, fmt.Errorf("index is required for %s query", queryType)
		}

		// dsl passes a raw DSL body straight through; kql translates the typed
		// KQL expression into standard DSL (see elasticsearch_kql.go). Both then
		// share the identical time-window / size / offset / sort merge and _search
		// response parsing, so kql works on every ES version + OpenSearch.
		var body map[string]any
		var berr error
		if queryType == "kql" {
			body, berr = buildESKQLQueryBody(fetchLogRequest.Query, fetchLogRequest.StartTime, fetchLogRequest.EndTime, fetchLogRequest.Limit, fetchLogRequest.Offset, fetchLogRequest.SortFields)
		} else {
			body, berr = finalizeESLogQueryBody(fetchLogRequest.Query, fetchLogRequest.StartTime, fetchLogRequest.EndTime, fetchLogRequest.Limit, fetchLogRequest.Offset, fetchLogRequest.SortFields)
		}
		if berr != nil {
			return nil, berr
		}

		// Log the exact index and body sent upstream. Labels/label-values hit the
		// same cluster over aggregations with no time bound, so when they return
		// data and this does not, the difference is in the resolved index or in
		// this body — both need to be replayable by hand against the cluster.
		renderedJSON, _ := json.Marshal(body)
		slog.Info("ES log query", "index", index,
			"url", fmt.Sprintf("%s/%s/_search", cfg.Url, index),
			"start_ms", fetchLogRequest.StartTime, "end_ms", fetchLogRequest.EndTime,
			"body", string(renderedJSON))

		resp, err := esRequestJSON("POST", fmt.Sprintf("%s/%s/_search", cfg.Url, index), body, cfg)
		if err != nil {
			return nil, fmt.Errorf("failed to execute elasticsearch %s query: %w", queryType, err)
		}
		defer func() { _ = resp.Body.Close() }()

		bodyBytes, err := readResponse(resp, "elasticsearch "+queryType+" query")
		if err != nil {
			return nil, err
		}
		return parseESSearchLogs(string(bodyBytes))

	case "ppl":
		pplBody := map[string]any{"query": fetchLogRequest.Query}
		if fetchLogRequest.Limit > 0 {
			pplBody["fetch_size"] = fetchLogRequest.Limit
		} else {
			pplBody["fetch_size"] = 100
		}

		resp, err := esRequestJSON("POST", fmt.Sprintf("%s/_plugins/_ppl", cfg.Url), pplBody, cfg) //nolint:bodyclose
		if err != nil {
			return nil, fmt.Errorf("failed to execute elasticsearch PPL query: %w", err)
		}

		bodyBytes, err := readResponse(resp, "elasticsearch PPL query")
		if err != nil {
			return nil, err
		}
		return parseESPPLLogs(string(bodyBytes))

	default:
		return nil, fmt.Errorf("unsupported query_type: %v", queryType)
	}

}

// esHitsTotal best-effort reads hits.total from a raw _search response for
// logging. ES 7+/OpenSearch return an object ({value, relation}); ES 6 returned a
// bare number. Decoded separately from SearchResponse and tolerant of both so a
// shape it does not know can never break query parsing. Returns -1 when unknown.
func esHitsTotal(rawJSON string) int64 {
	var probe struct {
		Hits struct {
			Total json.RawMessage `json:"total"`
		} `json:"hits"`
	}
	if err := json.Unmarshal([]byte(rawJSON), &probe); err != nil || len(probe.Hits.Total) == 0 {
		return -1
	}
	var asObject struct {
		Value int64 `json:"value"`
	}
	if err := json.Unmarshal(probe.Hits.Total, &asObject); err == nil {
		return asObject.Value
	}
	var asNumber int64
	if err := json.Unmarshal(probe.Hits.Total, &asNumber); err == nil {
		return asNumber
	}
	return -1
}

// esSourceFieldNames returns the sorted top-level _source field names of one hit,
// so a schema mismatch can be identified from the log without dumping document
// contents (which may hold customer data).
func esSourceFieldNames(src map[string]any) []string {
	keys := make([]string, 0, len(src))
	for k := range src {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func (e *ElasticSaasSource) QueryLabels(ctx *security.RequestContext, fetchLogRequest FetchLogLabelRequest) ([]OutputLogLabel, error) {
	cfg, err := GetElasticsearchConfig(ctx, fetchLogRequest.AccountId)
	if err != nil {
		return nil, err
	}

	// List stable data-stream names, not the rolled-over ".ds-*" backing indices
	// that _cat/indices exposes. No type-prefix filter: client clusters don't
	// necessarily name log streams "logs-*", so every queryable target is offered.
	// See ListAllESIndexTargets.
	indexNames, err := ListAllESIndexTargets(cfg)
	if err != nil {
		return nil, err
	}

	output := make([]OutputLogLabel, 0, len(indexNames))
	for _, indexName := range indexNames {
		output = append(output, OutputLogLabel{
			Label:      indexName,
			Attributes: map[string]any{},
		})
	}

	return output, nil
}

func (e *ElasticSaasSource) QueryLabelValues(ctx *security.RequestContext, fetchLogRequest FetchLogLabelValuesRequest) ([]OutputLogLabelValue, error) {
	cfg, err := GetElasticsearchConfig(ctx, fetchLogRequest.AccountId)
	if err != nil {
		return nil, err
	}

	index, _ := fetchLogRequest.Request["index"].(string)
	if index == "" {
		return nil, fmt.Errorf("index is required for querying label values")
	}

	fieldsToTry := []string{fetchLogRequest.LabelName}

	searchURL := fmt.Sprintf("%s/%s/_search", cfg.Url, index)
	var bodyBytes []byte
	for _, field := range fieldsToTry {
		aggsQuery := map[string]any{
			"size": 0,
			"aggs": map[string]any{
				"values": map[string]any{
					"terms": map[string]any{
						"field": field,
						"size":  1000,
					},
				},
			},
		}
		resp, err := esRequestJSON("POST", searchURL, aggsQuery, cfg) //nolint:bodyclose
		if err != nil {
			return nil, fmt.Errorf("failed to query elasticsearch field values: %w", err)
		}
		bodyBytes, err = readResponse(resp, "elasticsearch field values query")
		if err == nil {
			break
		}
		// If this was the last field to try, return the error
		if field == fieldsToTry[len(fieldsToTry)-1] {
			return nil, err
		}
	}

	return parseESLabelValuesResponse(bodyBytes)
}

// parseESLabelValuesResponse pulls the terms-aggregation bucket keys out of an ES
// aggregation response into label values, skipping empty keys. Split out from
// QueryLabelValues so the response-shape handling is unit-testable.
func parseESLabelValuesResponse(bodyBytes []byte) ([]OutputLogLabelValue, error) {
	var result map[string]any
	if err := json.Unmarshal(bodyBytes, &result); err != nil {
		return nil, fmt.Errorf("failed to unmarshal aggregation response: %w", err)
	}

	aggs, ok := result["aggregations"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("missing 'aggregations' in response")
	}
	values, ok := aggs["values"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("missing 'values' aggregation in response")
	}
	buckets, ok := values["buckets"].([]any)
	if !ok {
		return nil, fmt.Errorf("missing 'buckets' in aggregation response")
	}

	var output []OutputLogLabelValue
	for _, b := range buckets {
		bucket, ok := b.(map[string]any)
		if !ok {
			continue
		}
		key := fmt.Sprintf("%v", bucket["key"])
		if key != "" {
			output = append(output, OutputLogLabelValue{
				Value:      key,
				Attributes: map[string]any{},
			})
		}
	}

	return output, nil
}

func (e *ElasticSaasSource) QueryIndexFields(ctx *security.RequestContext, fetchLogRequest FetchLogLabelRequest) ([]OutputLogLabelFields, error) {
	cfg, err := GetElasticsearchConfig(ctx, fetchLogRequest.AccountId)
	if err != nil {
		return nil, err
	}

	index, _ := fetchLogRequest.Request["index"].(string)
	if index == "" {
		// FetchLogLabelsOrIndexFields settles this when it can, but the integration
		// it reads the default from is nil whenever the caller supplied both
		// log_provider and log_provider_source (llm-server does). cfg.LogIndex is
		// read off the integration record itself, so it still resolves here.
		index = cfg.LogIndex
	}
	if index == "" {
		// Nothing configured anywhere. A union across every index beats no field
		// list, but parseESMappingFields merges them all, so the caller gets fields
		// that need not coexist in whichever index its query runs against — worth a
		// warning rather than a silent widening.
		index = esAllIndicesWildcard
		ctx.GetLogger().Warn("ES index fields: no index requested and none configured, listing across every index",
			"account_id", fetchLogRequest.AccountId,
			"hint", "set log_index on the ES integration (or a per-account entry under Advanced Settings → index mapping)")
	}

	resp, err := esRequest("GET", fmt.Sprintf("%s/%s/_mapping", cfg.Url, index), "", cfg) //nolint:bodyclose
	if err != nil {
		return nil, fmt.Errorf("failed to query elasticsearch mapping: %w", err)
	}

	bodyBytes, err := readResponse(resp, "elasticsearch mapping query")
	if err != nil {
		return nil, err
	}

	return parseESMappingFields(bodyBytes)
}

// parseESMappingFields flattens an ES _mapping response into the field list
// (including multi-fields) via extractFieldsFromProperties. Split out from
// QueryIndexFields so the nested unwrap is unit-testable. Returns nil when the
// response carries no recognizable {index: {mappings: {properties}}} shape.
func parseESMappingFields(bodyBytes []byte) ([]OutputLogLabelFields, error) {
	var mappingResp map[string]any
	if err := json.Unmarshal(bodyBytes, &mappingResp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal mapping response: %w", err)
	}

	// Merge every index the pattern matched.
	//
	// This used to return after the FIRST index in the response. A log index pattern
	// fans out over one backing index per data stream — commonly one per namespace,
	// plus rollovers — and those mappings genuinely differ, because a field only
	// appears where a document carried it. Measured on the dev cluster, the pattern
	// `logs-kubernetes.container_logs-*` matches 5 indices holding 77-84 fields each:
	// 86 distinct fields in the union, but only 70 common to all. So 16 fields —
	// `kubernetes.labels.component`, `kubernetes.job.name`, `kubernetes.statefulset.name`
	// and similar — were visible or invisible depending on which index was picked.
	//
	// And the pick was a coin flip: this ranges over a Go map, whose iteration order is
	// deliberately randomized, so the same account could get a different field list on
	// consecutive calls. The result feeds applyLabelDataTypes, which skips any field it
	// cannot type (`if !known { continue }`) — so a missing field silently disables the
	// operator guard and the value coercion for it, and a query the type check would
	// have rejected reaches Elasticsearch to fail there as a raw error or an empty
	// result. That is "we asked wrongly" arriving disguised as "there is no data".
	//
	// Indices are visited in sorted order so that a field mapped with conflicting types
	// across indices resolves the same way every time.
	indices := make([]string, 0, len(mappingResp))
	for index := range mappingResp {
		indices = append(indices, index)
	}
	sort.Strings(indices)

	seen := make(map[string]bool)
	output := make([]OutputLogLabelFields, 0)
	for _, index := range indices {
		indexMap, ok := mappingResp[index].(map[string]any)
		if !ok {
			continue
		}
		mappings, ok := indexMap["mappings"].(map[string]any)
		if !ok {
			continue
		}
		properties, ok := mappings["properties"].(map[string]any)
		if !ok {
			continue
		}
		for _, f := range extractFieldsFromProperties(properties, "") {
			if seen[f.Field] {
				continue
			}
			seen[f.Field] = true
			output = append(output, f)
		}
	}

	if len(output) == 0 {
		return nil, nil
	}

	// Stable order, so the same estate always yields the same field list.
	sort.Slice(output, func(i, j int) bool { return output[i].Field < output[j].Field })

	return output, nil
}

func extractFieldsFromProperties(properties map[string]any, prefix string) []OutputLogLabelFields {
	var result []OutputLogLabelFields
	for fieldName, fieldData := range properties {
		fullName := fieldName
		if prefix != "" {
			fullName = prefix + "." + fieldName
		}

		fieldMap, ok := fieldData.(map[string]any)
		if !ok {
			continue
		}

		// If this field has nested properties (object type), recurse
		if nestedProps, ok := fieldMap["properties"].(map[string]any); ok {
			result = append(result, extractFieldsFromProperties(nestedProps, fullName)...)
			continue
		}

		attrs := make(map[string]any)
		if fieldType, ok := fieldMap["type"]; ok {
			attrs["type"] = fieldType
		}

		result = append(result, OutputLogLabelFields{
			Field:      fullName,
			Attributes: attrs,
		})

		// Also emit multi-fields (e.g. <field>.keyword) so callers that need
		// an aggregatable variant of a text field can find it in this list.
		if multiFields, ok := fieldMap["fields"].(map[string]any); ok {
			for subName, subData := range multiFields {
				subMap, ok := subData.(map[string]any)
				if !ok {
					continue
				}
				subAttrs := make(map[string]any)
				if subType, ok := subMap["type"]; ok {
					subAttrs["type"] = subType
				}
				result = append(result, OutputLogLabelFields{
					Field:      fullName + "." + subName,
					Attributes: subAttrs,
				})
			}
		}
	}
	return result
}

// QueryLogGroup implements LogGroupSource for Elasticsearch SaaS.
// Uses ES terms aggregation to group error/critical logs by message, namespace, and workload.
// QueryLogGroup implements LogGroupSource for Elasticsearch SaaS (Loki-style).
//
// Like the agent ElasticSource variant, it fetches raw error/critical/fatal log
// documents from the selected index via QueryLogs (which parses both Fluent-Bit
// and OTel-native doc shapes through ParseSourceMap) and groups them in-memory
// via the shared groupESLogsByPattern, instead of a terms aggregation on
// hardcoded Fluent-Bit keyword fields that returned nothing for OTel indices.
// The time range is injected by finalizeESLogQueryBody inside QueryLogs.
func (e *ElasticSaasSource) QueryLogGroup(ctx *security.RequestContext, req FetchLogGroupRequest) (LogGroupOutput, error) {
	// When the caller doesn't pick an index, prefer the account's configured log
	// index (per-account index_account_mapping → top-level log_index, both resolved
	// into cfg.LogIndex) over scanning every index on the cluster. Fall back to "*"
	// only when nothing is configured, matching the prior behaviour.
	index := common.GetString(req.Request, "index")
	if index == "" {
		if cfg, cfgErr := GetElasticsearchConfig(ctx, req.AccountId); cfgErr == nil && cfg.LogIndex != "" {
			index = cfg.LogIndex
		} else {
			index = "*"
		}
	}
	selectedNamespace := common.GetString(req.Request, "selectedNamespace")
	selectedWorkload := common.GetString(req.Request, "selectedWorkload")

	body, err := json.Marshal(map[string]any{
		"query": esLogGroupErrorQuery(),
		"sort":  esLogGroupSort(),
	})
	if err != nil {
		return LogGroupOutput{}, fmt.Errorf("es_saas.QueryLogGroup: failed to marshal query: %w", err)
	}

	logs, err := e.QueryLogs(ctx, FetchLogRequest{
		AccountId: req.AccountId,
		Query:     string(body),
		Request:   map[string]any{"index": index, "query_type": "dsl"},
		StartTime: req.StartTime,
		EndTime:   req.EndTime,
		Limit:     esLogGroupFetchLimit,
	})
	if err != nil {
		return LogGroupOutput{}, fmt.Errorf("es_saas.QueryLogGroup: failed to fetch logs: %w", err)
	}

	return groupESLogsByPattern(logs, selectedNamespace, selectedWorkload, req.EndTime), nil
}
