package alertrule

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Captured from the na-01 organisation on 2026-10-13 by sending the allowlisted
// getAlertDefinitionById document. Trimmed to the fields the mapper reads, with
// synthetic ids and names. flatCondition is kept as a node graph rather than
// simplified, because that shape is the reason Query is left empty.
const solarWindsGetAlertResponse = `{
  "data": {
    "alertQueries": {
      "alertDefinitions": {
        "alertDefinitions": [
          {
            "id": "aaaaaaaa-0000-0000-0000-000000000001",
            "name": "K8s pod restarting repeatedly",
            "description": "A pod's containers restarted 3 or more times in 15 minutes.",
            "severity": "CRITICAL",
            "enabled": true,
            "runbookLink": null,
            "triggerDelaySeconds": 600,
            "noDataResetSeconds": 86400,
            "triggerResetActions": true,
            "targetEntityTypes": ["KubernetesPod"],
            "conditionType": "ENTITY_METRIC",
            "actions": [
              {"configurationIds": ["115622"], "type": "email", "receivingType": "NOT_SPECIFIED",
               "includeDetails": true, "resendIntervalSeconds": 3600},
              {"configurationIds": ["115624"], "type": "webhook", "receivingType": "NOT_SPECIFIED",
               "includeDetails": true, "resendIntervalSeconds": 1800}
            ],
            "flatCondition": [
              {"id": "60de0568-bf54-46e9-b071-7d80d435951b",
               "links": [{"name": "operands", "values": ["b3cf44aa-3004-4532-8a86-95c2a1cfe130"]}]}
            ]
          }
        ]
      }
    }
  }
}`

// swoTestServer stands in for /v1/tfproxy, capturing what was sent.
func swoTestServer(t *testing.T, status int, body string) (*httptest.Server, *solarWindsGraphQLRequest, *http.Header) {
	t.Helper()
	var captured solarWindsGraphQLRequest
	var headers http.Header

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		headers = r.Header.Clone()
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &captured)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	return server, &captured, &headers
}

// The document has to leave the process exactly as captured. If anything in the
// request pipeline re-encodes or trims it, the proxy answers 403 — so assert on
// what actually went over the wire, not on the constant.
func TestSolarWindsGraphQLSendsTheFramedDocumentUnchanged(t *testing.T) {
	server, captured, headers := swoTestServer(t, http.StatusOK, solarWindsGetAlertResponse)

	var out struct{}
	require.NoError(t, solarWindsGraphQL(server.URL, "test-token", swoOpGetAlertDefinitionByID,
		map[string]any{"id": "aaaaaaaa-0000-0000-0000-000000000001"}, &out))

	assert.Equal(t, swoAlertDocuments[swoOpGetAlertDefinitionByID], captured.Query,
		"the document must arrive byte-identical to the allowlisted copy")
	assert.True(t, strings.HasPrefix(captured.Query, "\n"), "leading framing newline survived the round trip")
	assert.True(t, strings.HasSuffix(captured.Query, "\n"), "trailing framing newline survived the round trip")
	assert.Equal(t, swoOpGetAlertDefinitionByID, captured.OperationName,
		"operationName must be set; without it /v1/tfproxy hangs to a 504")
	assert.Equal(t, "Bearer test-token", headers.Get("Authorization"))
	assert.Equal(t, "application/json", headers.Get("Content-Type"))
}

// GraphQL reports logical failure with HTTP 200 and an errors array. Treating
// 200 as success is the classic way to turn a failed write into a silent one.
func TestSolarWindsGraphQLTreatsErrorsArrayAsFailure(t *testing.T) {
	server, _, _ := swoTestServer(t, http.StatusOK,
		`{"data":null,"errors":[{"message":"Invalid arguments"}]}`)

	var out struct{}
	err := solarWindsGraphQL(server.URL, "test-token", swoOpGetAlertDefinitionByID, nil, &out)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Invalid arguments")
}

// A 403 from the allowlist is not a credentials problem, and saying so saves the
// next person rotating a perfectly good token.
func TestSolarWindsGraphQLNamesTheAllowlistOnA403(t *testing.T) {
	server, _, _ := swoTestServer(t, http.StatusForbidden, "GraphQL request is not allowed.")

	var out struct{}
	err := solarWindsGraphQL(server.URL, "test-token", swoOpGetAlertDefinitionByID, nil, &out)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "allowlist")
	assert.Contains(t, err.Error(), "byte for byte")
	assert.Contains(t, err.Error(), "not a configuration problem")
}

func TestSolarWindsGraphQLDistinguishesAnUnauthorizedToken(t *testing.T) {
	server, _, _ := swoTestServer(t, http.StatusUnauthorized, `{"error":"unauthorized"}`)

	var out struct{}
	err := solarWindsGraphQL(server.URL, "test-token", swoOpGetAlertDefinitionByID, nil, &out)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "api_token")
	assert.NotContains(t, err.Error(), "allowlist")
}

// Only documents that were captured can be sent; the allowlist accepts nothing else.
func TestSolarWindsGraphQLRefusesAnUncapturedOperation(t *testing.T) {
	var out struct{}
	err := solarWindsGraphQL("http://example.invalid", "t", "createAlertDefinitionMutation", nil, &out)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no allowlisted GraphQL document")
}

// The mapping exists so Alert Manager can show an alert's real settings. The
// notification fields matter most: they are what a later update must preserve.
func TestSolarWindsDefinitionToExternalRule(t *testing.T) {
	var response struct {
		Data struct {
			AlertQueries struct {
				AlertDefinitions struct {
					AlertDefinitions []SolarWindsAlertDefinition `json:"alertDefinitions"`
				} `json:"alertDefinitions"`
			} `json:"alertQueries"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal([]byte(solarWindsGetAlertResponse), &response))
	definitions := response.Data.AlertQueries.AlertDefinitions.AlertDefinitions
	require.Len(t, definitions, 1)

	rule := solarWindsDefinitionToExternalRule(&definitions[0])

	assert.Equal(t, "aaaaaaaa-0000-0000-0000-000000000001", rule.ExternalRuleId)
	assert.Equal(t, "K8s pod restarting repeatedly", rule.Name)
	assert.Equal(t, "critical", rule.Severity)
	assert.True(t, rule.Enabled)
	// 600s is the alert's real delay. The webhook-sourced row stores "0" because
	// the payload carries no duration at all, which is the reason reading the
	// definition is worth doing.
	assert.Equal(t, "10m", rule.Duration)
	assert.Equal(t, "A pod's containers restarted 3 or more times in 15 minutes.", rule.Annotations["description"])

	// Query stays empty: SolarWinds has no query string, only a node graph, and a
	// made-up rendering could not be written back.
	assert.Empty(t, rule.Query)

	assert.Equal(t, []string{"115622", "115624"}, rule.ProviderConfig["notification_ids"],
		"both notification targets must survive the mapping")
	assert.Equal(t, 3600, rule.ProviderConfig["resend_interval_seconds"])
	assert.Equal(t, true, rule.ProviderConfig["trigger_reset_actions"])
	assert.Equal(t, 86400, rule.ProviderConfig["no_data_reset_seconds"])
	assert.Equal(t, []string{"KubernetesPod"}, rule.ProviderConfig["target_entity_types"])
}

func TestNormalizeSolarWindsSeverity(t *testing.T) {
	for input, want := range map[string]string{
		"CRITICAL": "critical", "critical": "critical", "FATAL": "critical", "ERROR": "critical",
		"WARNING": "warning", "": "warning", "something-new": "warning",
		"INFO": "info", "Informational": "info",
	} {
		assert.Equal(t, want, normalizeSolarWindsSeverity(input), "severity %q", input)
	}
}

func TestSolarWindsDurationString(t *testing.T) {
	for seconds, want := range map[int]string{0: "0", -1: "0", 30: "30s", 60: "1m", 600: "10m", 90: "90s"} {
		assert.Equal(t, want, solarWindsDurationString(seconds), "%d seconds", seconds)
	}
}

// An id is mandatory: SolarWinds cannot be searched by name, so a blank id has
// to fail before any request is made rather than fetch an arbitrary alert.
func TestSolarWindsGetAlertRuleRequiresAnId(t *testing.T) {
	source := &SolarWindsAlertRuleSource{}
	_, err := source.getAlertDefinition(nil, "acct-1", "   ")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "an alert id is required")
}

// The account guard precedes any database work, so this needs no database.
func TestGetSolarWindsConfigsRequiresAccountID(t *testing.T) {
	_, err := getSolarWindsConfigs(nil, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "account_id is required")
}

// Writes must fail loudly and say why, not half-succeed. A naive update would
// replace the whole definition and wipe the notification settings above.
func TestSolarWindsWritesAreRefusedWithAReason(t *testing.T) {
	source := &SolarWindsAlertRuleSource{}

	_, createErr := source.CreateAlertRule(nil, AlertRuleConfig{Name: "x"})
	_, updateErr := source.UpdateAlertRule(nil, "id", AlertRuleConfig{Name: "x"})
	deleteErr := source.DeleteAlertRule(nil, "acct-1", "id")

	for name, err := range map[string]error{"create": createErr, "update": updateErr, "delete": deleteErr} {
		t.Run(name, func(t *testing.T) {
			require.Error(t, err)
			assert.Contains(t, err.Error(), "not supported yet")
			assert.Contains(t, err.Error(), "notification settings")
		})
	}
}

// Capability contract. The negative assertions are the load-bearing ones:
// SolarWinds can never be enumerated, because its API ships no list operation,
// and a half-working list would silently present a partial inventory as whole.
func TestSolarWindsAlertRuleSourceContract(t *testing.T) {
	source := &SolarWindsAlertRuleSource{}

	_, isSource := interface{}(source).(AlertRuleSource)
	assert.True(t, isSource, "must satisfy AlertRuleSource")

	_, isGetter := interface{}(source).(AlertRuleGetter)
	assert.True(t, isGetter, "must satisfy AlertRuleGetter — reading by id is the whole point")

	_, isLister := interface{}(source).(AlertRuleLister)
	assert.False(t, isLister,
		"must NOT satisfy AlertRuleLister: SolarWinds ships no list/search/enumerate operation, "+
			"so any implementation would be inventing results")

	assert.False(t, SupportsListing("solarwinds", "user"), "listing is impossible, not merely unimplemented")
	assert.True(t, SupportsGet("solarwinds", "user"), "reading one rule by id is supported")
}

func TestSolarWindsAlertRuleRoutedFromDispatcher(t *testing.T) {
	source, err := getAlertRuleSource("solarwinds", "user")
	require.NoError(t, err)
	assert.IsType(t, &SolarWindsAlertRuleSource{}, source)

	// Only the user-configured integration exists; there is no agent flavour.
	_, err = getAlertRuleSource("solarwinds", "agent")
	assert.Error(t, err)
}

// withSolarWindsEndpoint points the read at a test server for the duration of a
// test, restoring the real resolver afterwards.
func withSolarWindsEndpoint(t *testing.T, url string) {
	t.Helper()
	previous := solarWindsEndpoint
	solarWindsEndpoint = func(solarWindsConn) string { return url }
	t.Cleanup(func() { solarWindsEndpoint = previous })
}

func TestFetchSolarWindsAlertDefinition(t *testing.T) {
	server, _, _ := swoTestServer(t, http.StatusOK, solarWindsGetAlertResponse)
	withSolarWindsEndpoint(t, server.URL)

	definition, err := fetchSolarWindsAlertDefinition(
		solarWindsConn{APIToken: "t", DataCenter: "na-01"}, "aaaaaaaa-0000-0000-0000-000000000001")
	require.NoError(t, err)

	assert.Equal(t, "K8s pod restarting repeatedly", definition.Name)
	assert.Equal(t, 600, definition.TriggerDelaySeconds)
	assert.True(t, definition.TriggerResetActions)
	require.Len(t, definition.Actions, 2)
	assert.Equal(t, []string{"115622"}, definition.Actions[0].ConfigurationIDs)
	assert.Equal(t, 3600, definition.Actions[0].ResendIntervalSeconds)
	assert.NotEmpty(t, definition.FlatCondition)
}

// An empty result means the alert was deleted in SolarWinds while Nudgebee kept
// a row for it. That is a distinct, non-retryable condition: a later update must
// abort rather than treat it as "nothing to preserve" and write a bare definition.
func TestFetchSolarWindsAlertDefinitionReportsADeletedAlert(t *testing.T) {
	server, _, _ := swoTestServer(t, http.StatusOK,
		`{"data":{"alertQueries":{"alertDefinitions":{"alertDefinitions":[]}}}}`)
	withSolarWindsEndpoint(t, server.URL)

	_, err := fetchSolarWindsAlertDefinition(
		solarWindsConn{APIToken: "t", DataCenter: "na-01"}, "aaaaaaaa-0000-0000-0000-000000000001")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no longer exists in SolarWinds")
}
