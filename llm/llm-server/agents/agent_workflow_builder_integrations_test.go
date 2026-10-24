package agents

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nudgebee/llm/agents/core"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"
)

// integrationRegistry is a GET /tasks reply carrying the input_schema shapes the runbook server
// serves for integration parameters.
const integrationRegistry = `{"tasks":[
 {"name":"tickets.create","input_schema":{
   "integration_id":{"type":"ticket","required":true,"sub_types":["jira","servicenow"]},
   "title":{"type":"string","required":true}}},
 {"name":"dbms.query","input_schema":{
   "integration_id":{"type":"integration","required":true,"sub_types":["postgresql","mysql"]}}},
 {"name":"tickets.get_comments","input_schema":{
   "integration_id":{"type":"ticket","required":false}}},
 {"name":"core.print","input_schema":{"message":{"type":"string"}}}
]}`

// integrationTestServer answers GET /tasks with the registry, GET /configs with configs, and any
// other request with reply (status code status), recording "METHOD /suffix" of each request.
func integrationTestServer(t *testing.T, configs string, status int, reply string) *[]string {
	t.Helper()
	var mu sync.Mutex
	seen := &[]string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		suffix := r.URL.Path[strings.LastIndex(r.URL.Path, "/"):]
		mu.Lock()
		*seen = append(*seen, r.Method+" "+suffix)
		mu.Unlock()
		switch {
		case r.Method == http.MethodGet && suffix == "/tasks":
			_, _ = w.Write([]byte(integrationRegistry))
		case r.Method == http.MethodGet && suffix == "/configs":
			_, _ = w.Write([]byte(configs))
		default:
			w.WriteHeader(status)
			_, _ = w.Write([]byte(reply))
		}
	}))
	t.Cleanup(server.Close)
	previous := config.Config.WorkflowServerEndpoint
	config.Config.WorkflowServerEndpoint = server.URL
	t.Cleanup(func() { config.Config.WorkflowServerEndpoint = previous })
	return seen
}

func stubAccountIntegrations(t *testing.T, configs []toolcore.ToolConfig, err error) {
	t.Helper()
	orig := tools.ListAccountIntegrations
	tools.ListAccountIntegrations = func(*security.RequestContext, string) ([]toolcore.ToolConfig, error) { return configs, err }
	t.Cleanup(func() { tools.ListAccountIntegrations = orig })
}

const ticketToConfigWorkflow = `{"name":"event-ticket-creator","definition":{"version":"v1","triggers":[{"type":"manual"}],"tasks":[
 {"id":"create-incident-ticket","type":"tickets.create","params":{"integration_id":"{{ Configs.ticket_integration_id }}","title":"x"}}]}}`

// No ticketing integration on the account, and the step points at a config that does not exist.
// Nothing is saved and the reply names what to connect — not "create config ticket_integration_id".
func TestSave_RefusesWhenTheAccountHasNoIntegrationOfTheKind(t *testing.T) {
	seen := integrationTestServer(t, `[]`, http.StatusOK, `{"id":"wf-1"}`)
	stubAccountIntegrations(t, []toolcore.ToolConfig{{Name: "team-slack", Schema: toolcore.ToolConfigSchema{ConfigType: "slack"}}}, nil)
	agent := newWorkflowBuilderAgent("acct-1")
	agent.state.Mode = "create"

	resp, err := agent.checkMissingConfigs(stepBudgetTestContext(), core.NBAgentRequest{AccountId: "acct-1", SessionId: "session-1"}, ticketToConfigWorkflow)

	require.NoError(t, err)
	assert.True(t, resp.IsTerminal)
	assert.Equal(t, "I did not save this automation, because a step needs an integration it cannot use:\n"+
		"- Step `create-incident-ticket` needs a jira or servicenow integration, but it points at the config `ticket_integration_id`, which does not exist, "+
		"and this account has no jira or servicenow integration connected. Connect one under Integrations, then ask me again.",
		resp.Response[0])
	assert.NotContains(t, *seen, "POST /workflows")
}

func TestSave_RefusalNamesTheIntegrationsTheAccountHas(t *testing.T) {
	integrationTestServer(t, `[]`, http.StatusOK, `{"id":"wf-1"}`)
	stubAccountIntegrations(t, []toolcore.ToolConfig{
		{Name: "prod-jira", Schema: toolcore.ToolConfigSchema{ConfigType: "jira"}},
		{Name: "itsm", Schema: toolcore.ToolConfigSchema{ConfigType: "servicenow"}},
	}, nil)
	agent := newWorkflowBuilderAgent("acct-1")
	agent.state.Mode = "create"

	resp, err := agent.checkMissingConfigs(stepBudgetTestContext(), core.NBAgentRequest{AccountId: "acct-1", SessionId: "session-1"}, ticketToConfigWorkflow)

	require.NoError(t, err)
	assert.Contains(t, resp.Response[0], "This account has: itsm, prod-jira. Ask me again and name the one to use.")
}

// The integration list only shapes the wording; when it cannot be read the save is still refused.
func TestSave_RefusesEvenWhenIntegrationsCannotBeListed(t *testing.T) {
	seen := integrationTestServer(t, `[]`, http.StatusOK, `{"id":"wf-1"}`)
	stubAccountIntegrations(t, nil, errors.New("db down"))
	agent := newWorkflowBuilderAgent("acct-1")
	agent.state.Mode = "create"

	resp, err := agent.checkMissingConfigs(stepBudgetTestContext(), core.NBAgentRequest{AccountId: "acct-1", SessionId: "session-1"}, ticketToConfigWorkflow)

	require.NoError(t, err)
	assert.Contains(t, resp.Response[0], "I did not save this automation")
	assert.NotContains(t, *seen, "POST /workflows")
}

// A config that exists is not a gap: the runbook server checks its value at save.
func TestSave_ExistingConfigIsSaved(t *testing.T) {
	seen := integrationTestServer(t, `[{"key":"ticket_integration_id","value":"prod-jira"}]`, http.StatusOK, `{"id":"wf-1"}`)
	stubAccountIntegrations(t, nil, nil)
	agent := newWorkflowBuilderAgent("acct-1")
	agent.state.Mode = "create"

	_, err := agent.checkMissingConfigs(stepBudgetTestContext(), core.NBAgentRequest{AccountId: "acct-1", SessionId: "session-1"}, ticketToConfigWorkflow)

	require.NoError(t, err)
	assert.Contains(t, *seen, "POST /workflows")
}

func TestValidate_NoIntegrationOfTheKindTellsTheModelToStop(t *testing.T) {
	integrationTestServer(t, `[]`, http.StatusBadRequest,
		`{"error":"task 'create-incident-ticket' parameter 'integration_id' validation failed: this account has no jira or servicenow integration connected"}`)
	agent := newWorkflowBuilderAgent("acct-1")
	require.NoError(t, json.Unmarshal([]byte(`{"name":"t","definition":{"version":"v1","triggers":[{"type":"manual"}],"tasks":[
	 {"id":"create-incident-ticket","type":"tickets.create","params":{"integration_id":"prod-jira","title":"x"}}]}}`), &agent.state.WorkingWorkflow))

	result := agent.executeWorkflowTool(stepBudgetTestContext(), "validate", "", "")

	assert.Contains(t, result, "this account has no jira or servicenow integration connected")
	assert.Contains(t, result, "Do not replace it with a {{ Configs.* }} placeholder or an integration of another kind. Stop building")
}

func TestReadOnlyCheckResults(t *testing.T) {
	integrationTestServer(t, `[]`, http.StatusOK, `{"ok":true}`)
	stubAccountIntegrations(t, nil, nil)
	agent := newWorkflowBuilderAgent("acct-1")
	var wf map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(ticketToConfigWorkflow), &wf))

	got := agent.readOnlyCheckResults(stepBudgetTestContext(), wf)

	assert.Equal(t, "\nCHECK RESULTS (automatic checks run on this automation just now; each problem below stops it from working even when it is active):\n"+
		"- Step `create-incident-ticket` needs a jira or servicenow integration, but it points at the config `ticket_integration_id`, which does not exist, "+
		"and this account has no jira or servicenow integration connected. Connect one under Integrations, then ask me again.\n", got)
}

func TestReadOnlyCheckResults_CleanAutomationAddsNothing(t *testing.T) {
	integrationTestServer(t, `[]`, http.StatusOK, `{"ok":true}`)
	agent := newWorkflowBuilderAgent("acct-1")
	var wf map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(`{"definition":{"tasks":[{"id":"p","type":"core.print","params":{"message":"hi"}}]}}`), &wf))

	assert.Empty(t, agent.readOnlyCheckResults(stepBudgetTestContext(), wf))
}

// An outage on the validate call is not a defect of the automation, so it adds nothing.
func TestReadOnlyCheckResults_ValidateOutageAddsNothing(t *testing.T) {
	integrationTestServer(t, `[]`, http.StatusBadGateway, `upstream down`)
	agent := newWorkflowBuilderAgent("acct-1")
	var wf map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(`{"definition":{"tasks":[{"id":"p","type":"core.print","params":{"message":"hi"}}]}}`), &wf))

	assert.Empty(t, agent.readOnlyCheckResults(stepBudgetTestContext(), wf))
}

func TestReadOnlyCheckResults_ReportsAValidationRejection(t *testing.T) {
	integrationTestServer(t, `[]`, http.StatusBadRequest, `{"error":"trigger filter is invalid"}`)
	agent := newWorkflowBuilderAgent("acct-1")
	var wf map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(`{"definition":{"tasks":[{"id":"p","type":"core.print","params":{"message":"hi"}}]}}`), &wf))

	assert.Contains(t, agent.readOnlyCheckResults(stepBudgetTestContext(), wf), "- The server rejects this definition as saved: ")
}
