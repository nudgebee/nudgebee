package alertrule

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"nudgebee/services/common"
	"nudgebee/services/security"
)

// SolarWindsAlertRuleSource manages alert rules in SolarWinds Observability.
//
// What it can and cannot do is dictated by the API, not by what is implemented
// so far:
//
//   - Reads are by id only. SolarWinds ships no list, search or enumerate
//     operation, and /v1/tfproxy allowlists exactly the operations its Terraform
//     provider ships — so there is nothing to allowlist. This type therefore
//     does NOT implement AlertRuleLister, and must not: an alert Nudgebee has
//     never seen fire cannot be discovered at all, and deletion reconciliation
//     is impossible. See solarwinds_graphql.go.
//   - An id is learned when an alert fires. Every webhook delivery carries
//     alertDefinitionId, which the SolarWinds webhook parser records as the
//     rule's external_rule_id.
//
// Stateless and zero-value usable, because getAlertRuleSource returns a fresh
// &SolarWindsAlertRuleSource{} per call.
type SolarWindsAlertRuleSource struct{}

// solarWindsRequestTimeout is deliberately below the proxy's own ~60s ceiling.
// /v1/tfproxy answers a document it does not recognise by hanging until it 504s,
// so a framing regression surfaces as a named client timeout in 30s rather than
// as a request thread parked for a minute.
const solarWindsRequestTimeout = 30 * time.Second

// solarWindsGraphQLRequest is the request body shape. A struct rather than a map
// so field order is deterministic — the allowlist is byte-sensitive about the
// document, and there is no reason to hand it a body that varies between calls.
type solarWindsGraphQLRequest struct {
	OperationName string `json:"operationName"`
	Query         string `json:"query"`
	Variables     any    `json:"variables"`
}

// solarWindsAlertAction is one notification attached to an alert.
//
// This is the field a careless update destroys: AlertDefinitionInput is a full
// replace, so an update built without carrying these over removes the alert's
// paging. Reading it is what makes a non-destructive update possible.
type solarWindsAlertAction struct {
	ConfigurationIDs      []string `json:"configurationIds"`
	Type                  string   `json:"type"`
	ReceivingType         string   `json:"receivingType"`
	IncludeDetails        bool     `json:"includeDetails"`
	ResendIntervalSeconds int      `json:"resendIntervalSeconds"`
}

// SolarWindsAlertDefinition is one alert as SolarWinds holds it.
//
// The field set is not a choice: it is whatever the allowlisted
// getAlertDefinitionById document selects, and it cannot be extended without a
// document SolarWinds' own provider ships. FlatCondition is kept as raw JSON
// because it is a node graph keyed by uuid rather than the conditions[] array
// the Terraform resource exposes — translating it is not needed to display a
// rule, and guessing at it would be the wrong thing to do on a full-replace API.
type SolarWindsAlertDefinition struct {
	ID                  string                  `json:"id"`
	Name                string                  `json:"name"`
	Description         string                  `json:"description"`
	Severity            string                  `json:"severity"`
	Enabled             bool                    `json:"enabled"`
	RunbookLink         string                  `json:"runbookLink"`
	TriggerDelaySeconds int                     `json:"triggerDelaySeconds"`
	NoDataResetSeconds  int                     `json:"noDataResetSeconds"`
	TriggerResetActions bool                    `json:"triggerResetActions"`
	TargetEntityTypes   []string                `json:"targetEntityTypes"`
	ConditionType       string                  `json:"conditionType"`
	Actions             []solarWindsAlertAction `json:"actions"`
	FlatCondition       json.RawMessage         `json:"flatCondition"`
}

// GetAlertRule reads one alert from SolarWinds by its id.
func (s *SolarWindsAlertRuleSource) GetAlertRule(ctx *security.RequestContext, accountId, externalRuleId string) (*ExternalAlertRule, error) {
	definition, err := s.getAlertDefinition(ctx, accountId, externalRuleId)
	if err != nil {
		return nil, err
	}
	return solarWindsDefinitionToExternalRule(definition), nil
}

// getAlertDefinition resolves the account's SolarWinds credentials and fetches
// the raw definition, which is what an update needs in order to preserve the
// fields it is not changing.
func (s *SolarWindsAlertRuleSource) getAlertDefinition(ctx *security.RequestContext, accountId, externalRuleId string) (*SolarWindsAlertDefinition, error) {
	if strings.TrimSpace(externalRuleId) == "" {
		return nil, fmt.Errorf("solarwinds: an alert id is required; SolarWinds cannot be searched by name")
	}

	conn, err := getSolarWindsConfigs(ctx, accountId)
	if err != nil {
		return nil, err
	}
	return fetchSolarWindsAlertDefinition(conn, externalRuleId)
}

// fetchSolarWindsAlertDefinition reads one alert for an already-resolved
// connection. Split from getAlertDefinition so the fetch can be exercised
// against a real organisation without a database (see the liveSolarWinds test)
// and against an httptest server without a data center.
// solarWindsEndpoint resolves the GraphQL endpoint for a connection. A
// package-level seam so tests can point the read at an httptest server without a
// real data center, mirroring solarWindsDoGET in observability/solarwinds_logs.go.
var solarWindsEndpoint = func(conn solarWindsConn) string { return conn.graphQLURL() }

func fetchSolarWindsAlertDefinition(conn solarWindsConn, externalRuleId string) (*SolarWindsAlertDefinition, error) {
	if strings.TrimSpace(externalRuleId) == "" {
		return nil, fmt.Errorf("solarwinds: an alert id is required; SolarWinds cannot be searched by name")
	}

	var response struct {
		Data struct {
			AlertQueries struct {
				AlertDefinitions struct {
					AlertDefinitions []SolarWindsAlertDefinition `json:"alertDefinitions"`
				} `json:"alertDefinitions"`
			} `json:"alertQueries"`
		} `json:"data"`
	}
	if err := solarWindsGraphQL(solarWindsEndpoint(conn), conn.APIToken, swoOpGetAlertDefinitionByID,
		map[string]any{"id": externalRuleId}, &response); err != nil {
		return nil, err
	}

	definitions := response.Data.AlertQueries.AlertDefinitions.AlertDefinitions
	if len(definitions) == 0 {
		// Distinct from a transport failure, and not retryable: the alert was
		// deleted in SolarWinds and Nudgebee's copy is stale.
		return nil, fmt.Errorf("solarwinds: alert %s no longer exists in SolarWinds", externalRuleId)
	}
	return &definitions[0], nil
}

// solarWindsDefinitionToExternalRule maps a SolarWinds alert onto the shape the
// alert-rule layer speaks.
//
// Query is left empty on purpose. SolarWinds expresses its condition as a node
// graph (flatCondition), not as a query string, and inventing a PromQL-looking
// rendering of it would put a value in the field that cannot be written back.
// ProviderConfig carries the SolarWinds-specific truth instead.
func solarWindsDefinitionToExternalRule(definition *SolarWindsAlertDefinition) *ExternalAlertRule {
	configurationIDs := []string{}
	resendIntervalSeconds := 0
	for _, action := range definition.Actions {
		configurationIDs = append(configurationIDs, action.ConfigurationIDs...)
		if action.ResendIntervalSeconds > resendIntervalSeconds {
			resendIntervalSeconds = action.ResendIntervalSeconds
		}
	}

	annotations := map[string]string{"summary": definition.Name}
	if definition.Description != "" {
		annotations["description"] = definition.Description
	}
	if definition.RunbookLink != "" {
		annotations["runbook"] = definition.RunbookLink
	}

	return &ExternalAlertRule{
		ExternalRuleId: definition.ID,
		Name:           definition.Name,
		AlertType:      "metric",
		Severity:       normalizeSolarWindsSeverity(definition.Severity),
		Duration:       solarWindsDurationString(definition.TriggerDelaySeconds),
		Annotations:    annotations,
		Labels:         map[string]string{"severity": normalizeSolarWindsSeverity(definition.Severity)},
		Enabled:        definition.Enabled,
		ProviderConfig: map[string]any{
			"condition_type":          definition.ConditionType,
			"target_entity_types":     definition.TargetEntityTypes,
			"trigger_delay_seconds":   definition.TriggerDelaySeconds,
			"no_data_reset_seconds":   definition.NoDataResetSeconds,
			"trigger_reset_actions":   definition.TriggerResetActions,
			"notification_ids":        configurationIDs,
			"resend_interval_seconds": resendIntervalSeconds,
		},
	}
}

// normalizeSolarWindsSeverity maps a SolarWinds severity onto the values
// event_rule_severity accepts. Unknown values become warning rather than being
// passed through, so a row never carries a severity the UI cannot render.
func normalizeSolarWindsSeverity(severity string) string {
	switch strings.ToUpper(strings.TrimSpace(severity)) {
	case "CRITICAL", "FATAL", "ERROR":
		return "critical"
	case "INFO", "INFORMATIONAL":
		return "info"
	default:
		return "warning"
	}
}

// solarWindsDurationString renders seconds as the Prometheus-style string the
// alert-rule layer uses elsewhere. Zero stays "0": SolarWinds really does mean
// "fire on the first breach", and rendering that as "" would read as unknown.
func solarWindsDurationString(seconds int) string {
	if seconds <= 0 {
		return "0"
	}
	if seconds%60 == 0 {
		return fmt.Sprintf("%dm", seconds/60)
	}
	return fmt.Sprintf("%ds", seconds)
}

// solarWindsGraphQL posts one allowlisted operation and decodes its response.
//
// Takes the endpoint and token rather than a solarWindsConn so the transport can
// be exercised against an httptest server without a database or a real data
// center — the same reason cubeAPMAdminRequest takes its config by value.
func solarWindsGraphQL(endpoint, apiToken, operationName string, variables any, out any) error {
	document, held := swoAlertDocuments[operationName]
	if !held {
		// Not a user-facing condition: it means code asked for an operation whose
		// document was never captured. Only the allowlisted set can be sent.
		return fmt.Errorf("solarwinds: no allowlisted GraphQL document for operation %q", operationName)
	}

	body := solarWindsGraphQLRequest{
		OperationName: operationName,
		Query:         document,
		Variables:     variables,
	}

	resp, err := common.HttpPost(endpoint,
		common.HttpWithHeaders(map[string]string{
			"Authorization": "Bearer " + apiToken,
			"Content-Type":  "application/json",
			"Accept":        "application/json",
		}),
		common.HttpWithJsonBody(body),
		common.HttpWithTimeout(solarWindsRequestTimeout),
	)
	if err != nil {
		return solarWindsTransportError(operationName, err)
	}
	defer func() { _ = resp.Body.Close() }()

	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("solarwinds: failed to read the response to %s: %w", operationName, err)
	}
	if resp.StatusCode != http.StatusOK {
		return solarWindsStatusError(operationName, resp.StatusCode, responseBody)
	}

	// GraphQL answers a logical failure with HTTP 200 and an errors array, so
	// this check is what separates success from failure — not the status code.
	var envelope struct {
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(responseBody, &envelope); err != nil {
		return fmt.Errorf("solarwinds: failed to parse the response to %s: %w", operationName, err)
	}
	if len(envelope.Errors) > 0 {
		return fmt.Errorf("solarwinds: %s failed: %s", operationName, envelope.Errors[0].Message)
	}

	if err := json.Unmarshal(responseBody, out); err != nil {
		return fmt.Errorf("solarwinds: failed to parse the %s result: %w", operationName, err)
	}
	return nil
}

// solarWindsTransportError names the framing as a likely cause of a timeout,
// because that is how /v1/tfproxy behaves when it does not recognise a document.
func solarWindsTransportError(operationName string, err error) error {
	if strings.Contains(err.Error(), "context deadline exceeded") || strings.Contains(err.Error(), "Timeout") {
		return fmt.Errorf("solarwinds: %s did not answer within %s, which is how /v1/tfproxy behaves "+
			"when a GraphQL document is sent without its framing newlines: %w",
			operationName, solarWindsRequestTimeout, err)
	}
	return fmt.Errorf("solarwinds: %s failed: %w", operationName, err)
}

// solarWindsStatusError turns a status code into something that names the real
// cause, so the allowlist case is not mistaken for a credentials problem.
func solarWindsStatusError(operationName string, statusCode int, body []byte) error {
	text := string(body)
	if len(text) > 500 {
		text = text[:500] + "…"
	}

	switch {
	case statusCode == http.StatusForbidden && strings.Contains(text, "GraphQL request is not allowed"):
		return fmt.Errorf("solarwinds: %s was rejected by the /v1/tfproxy allowlist, not by the "+
			"credentials. The allowlist matches the GraphQL document byte for byte, so the copy in "+
			"solarwinds_graphql.go has drifted from what SolarWinds accepts. This is not a "+
			"configuration problem: HTTP %d: %s", operationName, statusCode, text)
	case statusCode == http.StatusUnauthorized:
		return fmt.Errorf("solarwinds: %s was refused — check the integration's api_token: HTTP %d: %s",
			operationName, statusCode, text)
	case statusCode == http.StatusForbidden:
		return fmt.Errorf("solarwinds: %s was refused — the api_token may be an Ingestion token rather "+
			"than an API Access token: HTTP %d: %s", operationName, statusCode, text)
	default:
		return fmt.Errorf("solarwinds: %s failed with HTTP %d: %s", operationName, statusCode, text)
	}
}

// ---------------------------------------------------------------------------
// AlertRuleSource — writes are not implemented yet
// ---------------------------------------------------------------------------

// errSolarWindsWritesNotImplemented is returned by the write methods until the
// read-merge-write path exists.
//
// They cannot be written as a naive create/update: AlertDefinitionInput is a
// full replace, so an update that does not first read the alert and carry over
// its actions, triggerResetActions, noDataResetSeconds and runbookLink removes
// the customer's notification wiring. The read half of that is what this file
// provides; the merge and the write follow.
var errSolarWindsWritesNotImplemented = fmt.Errorf(
	"solarwinds: changing an alert in SolarWinds is not supported yet; it requires reading the alert " +
		"first and merging, because the SolarWinds update replaces the whole definition and would " +
		"otherwise clear its notification settings")

func (s *SolarWindsAlertRuleSource) CreateAlertRule(ctx *security.RequestContext, config AlertRuleConfig) (*AlertRuleResult, error) {
	return nil, errSolarWindsWritesNotImplemented
}

func (s *SolarWindsAlertRuleSource) UpdateAlertRule(ctx *security.RequestContext, externalRuleId string, config AlertRuleConfig) (*AlertRuleResult, error) {
	return nil, errSolarWindsWritesNotImplemented
}

func (s *SolarWindsAlertRuleSource) DeleteAlertRule(ctx *security.RequestContext, accountId string, externalRuleId string) error {
	return errSolarWindsWritesNotImplemented
}
