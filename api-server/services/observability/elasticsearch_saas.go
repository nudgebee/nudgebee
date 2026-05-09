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
	"net/http"
	"nudgebee/services/common"
	"nudgebee/services/integrations/core"
	"nudgebee/services/security"
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
)

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
	cfg := &ElasticsearchConfig{AuthType: "basic"}

	for _, c := range integration.Configs {
		value := c.Value
		if c.IsEncrypted && value != "" {
			decrypted, err := common.Decrypt(value)
			if err != nil {
				return nil, fmt.Errorf("failed to decrypt config %s: %w", c.Name, err)
			}
			value = decrypted
		}
		switch c.Name {
		case ElasticsearchUrl:
			cfg.Url = value
		case ElasticsearchUsername:
			cfg.Username = value
		case ElasticsearchPassword:
			cfg.Password = value
		case ElasticsearchAuthType:
			cfg.AuthType = value
		case ElasticsearchRegion:
			cfg.Region = value
		case ElasticsearchUserPoolId:
			cfg.UserPoolId = value
		case ElasticsearchIdentityPoolId:
			cfg.IdentityPoolId = value
		case ElasticsearchAppClientId:
			cfg.AppClientId = value
		case ElasticsearchApiKey:
			cfg.ApiKey = value
		case ElasticsearchBearerToken:
			cfg.BearerToken = value
		}
	}

	if cfg.Url == "" {
		return nil, fmt.Errorf("missing required elasticsearch URL")
	}
	switch cfg.AuthType {
	case "api_key":
		if cfg.ApiKey == "" {
			return nil, fmt.Errorf("missing api_key for auth_type 'api_key'")
		}
	case "bearer_token":
		if cfg.BearerToken == "" {
			return nil, fmt.Errorf("missing bearer_token for auth_type 'bearer_token'")
		}
	default: // "basic", "cognito"
		if cfg.Username == "" || cfg.Password == "" {
			return nil, fmt.Errorf("missing required elasticsearch username/password")
		}
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

// esHTTPClient is an HTTP client that skips TLS verification for user-managed
// OpenSearch instances that commonly use self-signed certificates.
var esHTTPClient = func() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // User-configured OpenSearch with self-signed certs
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

	return esHTTPClient.Do(req)
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
	return []string{"_eq", "_neq", "_contains", "_in", "_not_in", "_like", "_nlike", "_gt", "_lt", "_is_null"}
}

func (e *ElasticSaasSource) GetQuery(ctx *security.RequestContext, fetchLogRequest FetchLogRequest) (string, error) {
	return buildESQueryFromWhere(fetchLogRequest.QueryRequest.Where)
}

func (e *ElasticSaasSource) QueryLogs(ctx *security.RequestContext, fetchLogRequest FetchLogRequest) ([]OutputLog, error) {
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

	var rawJSON string

	switch queryType {
	case "dsl":
		index, _ := fetchLogRequest.Request["index"].(string)
		if index == "" {
			return nil, fmt.Errorf("index is required for DSL query")
		}

		resp, err := esRequest("POST", fmt.Sprintf("%s/%s/_search", cfg.Url, index), fetchLogRequest.Query, cfg)
		if err != nil {
			return nil, fmt.Errorf("failed to execute elasticsearch DSL query: %w", err)
		}

		bodyBytes, err := readResponse(resp, "elasticsearch DSL query")
		if err != nil {
			return nil, err
		}
		rawJSON = string(bodyBytes)

	case "ppl":
		pplBody := map[string]any{"query": fetchLogRequest.Query}
		if fetchLogRequest.Limit > 0 {
			pplBody["fetch_size"] = fetchLogRequest.Limit
		} else {
			pplBody["fetch_size"] = 100
		}

		resp, err := esRequestJSON("POST", fmt.Sprintf("%s/_plugins/_ppl", cfg.Url), pplBody, cfg)
		if err != nil {
			return nil, fmt.Errorf("failed to execute elasticsearch PPL query: %w", err)
		}

		bodyBytes, err := readResponse(resp, "elasticsearch PPL query")
		if err != nil {
			return nil, err
		}
		rawJSON = string(bodyBytes)

	default:
		return nil, fmt.Errorf("unsupported query_type: %v", queryType)
	}

	var output []OutputLog

	if queryType == "ppl" {
		var pplResp PPLResponse
		if err := json.Unmarshal([]byte(rawJSON), &pplResp); err != nil {
			return nil, fmt.Errorf("failed to unmarshal PPL response: %w", err)
		}

		output = make([]OutputLog, 0, len(pplResp.DataRows))
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
	} else {
		var searchResp SearchResponse
		if err := json.Unmarshal([]byte(rawJSON), &searchResp); err != nil {
			return nil, fmt.Errorf("failed to unmarshal DSL response: %w", err)
		}

		output = make([]OutputLog, 0, len(searchResp.Hits.Hits))
		for _, hit := range searchResp.Hits.Hits {
			if log, ok := ParseSourceMap(hit.Source); ok {
				output = append(output, log)
			}
		}
	}

	return output, nil
}

func (e *ElasticSaasSource) QueryLabels(ctx *security.RequestContext, fetchLogRequest FetchLogLabelRequest) ([]OutputLogLabel, error) {
	cfg, err := GetElasticsearchConfig(ctx, fetchLogRequest.AccountId)
	if err != nil {
		return nil, err
	}

	resp, err := esRequest("GET", fmt.Sprintf("%s/_cat/indices?format=json", cfg.Url), "", cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to query elasticsearch indices: %w", err)
	}

	bodyBytes, err := readResponse(resp, "elasticsearch indices query")
	if err != nil {
		return nil, err
	}

	var indices []map[string]any
	if err := json.Unmarshal(bodyBytes, &indices); err != nil {
		return nil, fmt.Errorf("failed to unmarshal indices response: %w", err)
	}

	var output []OutputLogLabel
	for _, idx := range indices {
		if indexName, ok := idx["index"].(string); ok && indexName != "" {
			output = append(output, OutputLogLabel{
				Label:      indexName,
				Attributes: map[string]any{},
			})
		}
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

	// Try .keyword suffix first (required for text fields), fall back to original field name.
	fieldsToTry := []string{fetchLogRequest.LabelName}
	if !strings.HasSuffix(fetchLogRequest.LabelName, ".keyword") {
		fieldsToTry = []string{fetchLogRequest.LabelName + ".keyword", fetchLogRequest.LabelName}
	}

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
		resp, err := esRequestJSON("POST", searchURL, aggsQuery, cfg)
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
		return nil, fmt.Errorf("index is required for querying index fields")
	}

	resp, err := esRequest("GET", fmt.Sprintf("%s/%s/_mapping", cfg.Url, index), "", cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to query elasticsearch mapping: %w", err)
	}

	bodyBytes, err := readResponse(resp, "elasticsearch mapping query")
	if err != nil {
		return nil, err
	}

	var mappingResp map[string]any
	if err := json.Unmarshal(bodyBytes, &mappingResp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal mapping response: %w", err)
	}

	var result []OutputLogLabelFields

	// The mapping response has the structure: {index_name: {mappings: {properties: {field: {type: ...}}}}}
	for _, indexData := range mappingResp {
		indexMap, ok := indexData.(map[string]any)
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
		result = extractFieldsFromProperties(properties, "")
		break // only process the first index mapping
	}

	return result, nil
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
func (e *ElasticSaasSource) QueryLogGroup(ctx *security.RequestContext, req FetchLogGroupRequest) (LogGroupOutput, error) {
	cfg, err := GetElasticsearchConfig(ctx, req.AccountId)
	if err != nil {
		return LogGroupOutput{}, fmt.Errorf("es_saas.QueryLogGroup: failed to get config: %w", err)
	}

	selectedNamespace := common.GetString(req.Request, "selectedNamespace")
	selectedWorkload := common.GetString(req.Request, "selectedWorkload")
	index := common.GetString(req.Request, "index")
	if index == "" {
		index = "*"
	}

	// Build filter for error/critical logs
	filters := []any{
		map[string]any{"bool": map[string]any{
			"should": []map[string]any{
				{"terms": map[string]any{"level": []string{"error", "critical", "fatal", "ERROR", "CRITICAL", "FATAL"}}},
				{"terms": map[string]any{"severity": []string{"error", "critical", "fatal", "ERROR", "CRITICAL", "FATAL"}}},
			},
			"minimum_should_match": 1,
		}},
		map[string]any{"range": map[string]any{
			"@timestamp": map[string]any{
				"gte":    req.StartTime,
				"lte":    req.EndTime,
				"format": "epoch_millis",
			},
		}},
	}
	if selectedNamespace != "" {
		filters = append(filters, map[string]any{
			"term": map[string]any{"kubernetes.namespace_name.keyword": selectedNamespace},
		})
	}
	if selectedWorkload != "" {
		filters = append(filters, map[string]any{
			"wildcard": map[string]any{"kubernetes.pod_name.keyword": escapeESWildcard(selectedWorkload) + "*"},
		})
	}

	queryBody := map[string]any{
		"size": 0,
		"query": map[string]any{
			"bool": map[string]any{"filter": filters},
		},
		"aggs": map[string]any{
			"log_groups": map[string]any{
				"terms": map[string]any{
					"field": "log.keyword",
					"size":  100,
				},
				"aggs": map[string]any{
					"namespaces": map[string]any{
						"terms": map[string]any{"field": "kubernetes.namespace_name.keyword", "size": 10},
					},
					"workloads": map[string]any{
						"terms": map[string]any{"field": "kubernetes.pod_name.keyword", "size": 10},
					},
					"containers": map[string]any{
						"terms": map[string]any{"field": "kubernetes.container_name.keyword", "size": 10},
					},
					"levels": map[string]any{
						"terms": map[string]any{"field": "level", "size": 10},
					},
				},
			},
		},
	}

	searchURL := fmt.Sprintf("%s/%s/_search", cfg.Url, index)
	resp, err := esRequestJSON("POST", searchURL, queryBody, cfg)
	if err != nil {
		return LogGroupOutput{}, fmt.Errorf("es_saas.QueryLogGroup: request failed: %w", err)
	}

	bodyBytes, err := readResponse(resp, "QueryLogGroup")
	if err != nil {
		return LogGroupOutput{}, err
	}

	// Reuse the same parsing logic as ElasticSource
	return parseESLogGroupResponse(string(bodyBytes), req.EndTime)
}
