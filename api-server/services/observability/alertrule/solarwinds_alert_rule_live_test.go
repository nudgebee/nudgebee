//go:build liveSolarWinds

// Live verification against a real SolarWinds organisation.
//
// Excluded from CI by the build tag, not merely by an env guard, because this is
// the only test that can verify the thing CI structurally cannot: whether
// /v1/tfproxy still accepts the GraphQL documents pinned in
// solarwinds_graphql.go. That allowlist is a server-side property. The offline
// digest test proves our bytes have not drifted; only this proves SolarWinds
// still accepts them.
//
// Read-only. It sends one allowlisted query and asserts nothing is written.
//
// Run it when changing a document, bumping the provider version the documents
// came from, or investigating a 403 in production:
//
//	cd api-server/services
//	SWO_LIVE_TOKEN=<api access token> \
//	SWO_LIVE_DATACENTER=na-01 \
//	SWO_LIVE_ALERT_ID=<an alert's alertDefinitionId> \
//	  go test -tags liveSolarWinds ./observability/alertrule/ -run TestLiveSolarWinds -v
//
// The alert id is any id from the account — a firing webhook carries it as
// alertDefinitionId, and it is also the last path segment of the alert's URL in
// the SolarWinds console. There is no way to discover one from the API, which is
// the limitation this whole provider is shaped around.
package alertrule

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func liveSolarWindsConn(t *testing.T) (solarWindsConn, string) {
	t.Helper()
	token := os.Getenv("SWO_LIVE_TOKEN")
	alertID := os.Getenv("SWO_LIVE_ALERT_ID")
	dataCenter := os.Getenv("SWO_LIVE_DATACENTER")
	if dataCenter == "" {
		dataCenter = solarWindsDefaultDataCenter
	}
	if token == "" || alertID == "" {
		t.Skip("set SWO_LIVE_TOKEN and SWO_LIVE_ALERT_ID to run the live SolarWinds test")
	}
	require.True(t, solarWindsDataCenters[dataCenter], "SWO_LIVE_DATACENTER=%q is not a known data center", dataCenter)
	return solarWindsConn{APIToken: token, DataCenter: dataCenter}, alertID
}

// The allowlist still accepts our pinned document. A 403 here with the offline
// digest test green means SolarWinds changed what it accepts, and the documents
// must be re-captured from a current provider build.
func TestLiveSolarWindsAllowlistAcceptsThePinnedDocument(t *testing.T) {
	conn, alertID := liveSolarWindsConn(t)

	var response struct {
		Data struct {
			AlertQueries struct {
				AlertDefinitions struct {
					AlertDefinitions []SolarWindsAlertDefinition `json:"alertDefinitions"`
				} `json:"alertDefinitions"`
			} `json:"alertQueries"`
		} `json:"data"`
	}

	err := solarWindsGraphQL(conn.graphQLURL(), conn.APIToken, swoOpGetAlertDefinitionByID,
		map[string]any{"id": alertID}, &response)
	if err != nil && strings.Contains(err.Error(), "allowlist") {
		t.Fatalf("SolarWinds no longer accepts the pinned document — re-capture it from the "+
			"Terraform provider binary and update the digest in the same change: %v", err)
	}
	require.NoError(t, err)

	definitions := response.Data.AlertQueries.AlertDefinitions.AlertDefinitions
	require.Len(t, definitions, 1, "expected exactly one alert for id %s", alertID)
	assert.Equal(t, alertID, definitions[0].ID)
	assert.NotEmpty(t, definitions[0].Name)
}

// The selection set must still return the fields a non-destructive update needs.
// If SolarWinds ever drops one, an update built on this read would start
// silently clearing it — so this is the test that gates the write phases.
func TestLiveSolarWindsReadReturnsTheFieldsAnUpdateMustPreserve(t *testing.T) {
	conn, alertID := liveSolarWindsConn(t)

	definition, err := fetchSolarWindsAlertDefinition(conn, alertID)
	require.NoError(t, err)

	assert.NotEmpty(t, definition.Name)
	assert.NotEmpty(t, definition.Severity)
	assert.NotEmpty(t, definition.ConditionType)
	assert.NotEmpty(t, definition.FlatCondition, "the condition must be readable")
	assert.NotEmpty(t, definition.TargetEntityTypes)

	// Not every alert has a notification attached, so this is reported rather
	// than asserted — an alert with one must round-trip its configurationIds.
	if len(definition.Actions) == 0 {
		t.Log("this alert has no notification actions; attach one to exercise the field an update must preserve")
		return
	}
	for _, action := range definition.Actions {
		assert.NotEmpty(t, action.ConfigurationIDs,
			"a notification action with no configurationIds cannot be carried over by an update")
		assert.NotEmpty(t, action.Type)
	}
}
