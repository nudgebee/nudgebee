package tools

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"
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

func TestIntegrationParamsByTaskType(t *testing.T) {
	params := IntegrationParamsByTaskType([]byte(integrationRegistry))

	assert.Equal(t, map[string]IntegrationParam{
		"integration_id": {Kinds: []string{"jira", "servicenow"}, Ticket: true, Required: true},
	}, params["tickets.create"])
	assert.Equal(t, []string{"postgresql", "mysql"}, params["dbms.query"]["integration_id"].Kinds)
	assert.Equal(t, IntegrationParam{Ticket: true}, params["tickets.get_comments"]["integration_id"])
	assert.NotContains(t, params, "core.print")

	bare := IntegrationParamsByTaskType([]byte(`[{"name":"scm.github.cli","input_schema":{"integration_id":{"type":"integration","sub_type":"github"}}}]`))
	assert.Equal(t, []string{"github"}, bare["scm.github.cli"]["integration_id"].Kinds)

	assert.Empty(t, IntegrationParamsByTaskType([]byte(`not json`)))
}

func TestIntegrationParam_KindLabel(t *testing.T) {
	assert.Equal(t, "jira", IntegrationParam{Kinds: []string{"jira"}}.KindLabel())
	assert.Equal(t, "jira, servicenow or github", IntegrationParam{Kinds: []string{"jira", "servicenow", "github"}}.KindLabel())
	assert.Equal(t, "ticketing", IntegrationParam{Ticket: true}.KindLabel(), "older runbook servers declare no kinds on ticket params")
}

func TestFindIntegrationGaps(t *testing.T) {
	var wf map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(`{"definition":{"tasks":[
	  {"id":"missing-config","type":"tickets.create","params":{"integration_id":"{{ Configs.ticket_integration_id }}"}},
	  {"id":"existing-config","type":"tickets.create","params":{"integration_id":"{{ Configs.jira_id }}"}},
	  {"id":"by-name","type":"tickets.create","params":{"integration_id":"prod-jira"}},
	  {"id":"from-input","type":"tickets.create","params":{"integration_id":"{{ Inputs.integration }}"}},
	  {"id":"empty-required","type":"dbms.query","params":{}},
	  {"id":"empty-optional","type":"tickets.get_comments","params":{}},
	  {"id":"unknown-type","type":"made.up","params":{"integration_id":"{{ Configs.nope }}"}},
	  {"id":"loop","type":"core.foreach","params":{"tasks":[
	    {"id":"inner","type":"dbms.query","params":{"integration_id":"{{ Configs.db }}"}}]},
	   "hooks":{"failure":[{"type":"tickets.create","params":{"integration_id":"{{ Configs.on_fail }}"}}]}}
	],"hooks":{"always":[{"type":"tickets.create","params":{"integration_id":""}}]}}}`), &wf))

	gaps := FindIntegrationGaps(wf, IntegrationParamsByTaskType([]byte(integrationRegistry)), func(key string) bool { return key == "jira_id" })

	var got []string
	for _, g := range gaps {
		got = append(got, g.TaskID+"|"+g.ConfigKey)
	}
	assert.Equal(t, []string{
		"missing-config|ticket_integration_id",
		"empty-required|",
		"loop (failure hook 1)|on_fail",
		"inner|db",
		"automation (always hook 1)|",
	}, got)
}

func TestFindIntegrationGaps_UnreadableRegistryFindsNothing(t *testing.T) {
	var wf map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(`{"definition":{"tasks":[{"id":"t","type":"tickets.create","params":{}}]}}`), &wf))
	assert.Empty(t, FindIntegrationGaps(wf, IntegrationParamsByTaskType(nil), func(string) bool { return false }))
}

func stubAccountIntegrations(t *testing.T, configs []core.ToolConfig, err error) {
	t.Helper()
	orig := ListAccountIntegrations
	ListAccountIntegrations = func(*security.RequestContext, string) ([]core.ToolConfig, error) { return configs, err }
	t.Cleanup(func() { ListAccountIntegrations = orig })
}

func gapsTestContext() *security.RequestContext {
	secCtx := security.NewSecurityContextForTenantAccountAdmin("tenant-1", "user-1", []string{"acct"})
	return security.NewRequestContext(context.Background(), secCtx, slog.New(slog.NewTextHandler(io.Discard, nil)), nil, nil)
}

func TestIntegrationGapLines(t *testing.T) {
	stubAccountIntegrations(t, []core.ToolConfig{
		{Name: "prod-jira", Schema: core.ToolConfigSchema{ConfigType: "jira"}},
		{Name: "team-slack", Schema: core.ToolConfigSchema{ConfigType: "slack"}},
	}, nil)
	gaps := []IntegrationGap{
		{TaskID: "create", Param: "integration_id", ConfigKey: "ticket_integration_id", Need: IntegrationParam{Kinds: []string{"jira", "servicenow"}, Ticket: true}},
		{TaskID: "query", Param: "integration_id", Need: IntegrationParam{Kinds: []string{"postgresql"}}},
	}

	assert.Equal(t, []string{
		"- Step `create` needs a jira or servicenow integration, but it points at the config `ticket_integration_id`, which does not exist. This account has: prod-jira. Ask me again and name the one to use.",
		"- Step `query` needs a postgresql integration, but none is set, and this account has no postgresql integration connected. Connect one under Integrations, then ask me again.",
	}, IntegrationGapLines(gapsTestContext(), "acct", gaps))
}

func TestIntegrationGapLines_ListingFailsDoesNotClaimNoneConnected(t *testing.T) {
	stubAccountIntegrations(t, nil, errors.New("db down"))
	lines := IntegrationGapLines(gapsTestContext(), "acct", []IntegrationGap{{TaskID: "create", Need: IntegrationParam{Ticket: true}}})
	assert.Equal(t, []string{"- Step `create` needs a ticketing integration, but none is set. Name the ticketing integration to use (connect one under Integrations if the account has none), then ask me again."}, lines,
		"a listing that failed must not read as \"none connected\"")
}

// runbookStub answers GET /tasks with the registry, GET /configs with configs (or a 500 when
// configs is ""), and GET /workflows/<id> with workflow.
func integrationRunbookStub(t *testing.T, configs, workflow string) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/tasks"):
			_, _ = w.Write([]byte(integrationRegistry))
		case strings.HasSuffix(r.URL.Path, "/configs"):
			if configs == "" {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			_, _ = w.Write([]byte(configs))
		default:
			_, _ = w.Write([]byte(workflow))
		}
	}))
	t.Cleanup(server.Close)
	previous := config.Config.WorkflowServerEndpoint
	config.Config.WorkflowServerEndpoint = server.URL
	t.Cleanup(func() { config.Config.WorkflowServerEndpoint = previous })
}

const ticketToMissingConfig = `{"id":"wf-1","name":"triage-ticket","status":"INACTIVE","definition":{"tasks":[
 {"id":"create-incident-ticket","type":"tickets.create","params":{"integration_id":"{{ Configs.ticket_integration_id }}","title":"x"}}]}}`

func TestWorkflowIntegrationGaps_ConfigsUnreadableReportsNothing(t *testing.T) {
	integrationRunbookStub(t, "", "")
	var wf map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(ticketToMissingConfig), &wf))
	assert.Empty(t, WorkflowIntegrationGaps(gapsTestContext(), "acct", wf), "could not tell must not read as missing")
}

// Nubi's answer to "why doesn't it create tickets" reads workflow_get; it now carries the missing
// integration as a fact instead of leaving the model to blame status or configs.
func TestWorkflowGet_ReportsIntegrationProblems(t *testing.T) {
	integrationRunbookStub(t, `[]`, ticketToMissingConfig)
	stubAccountIntegrations(t, nil, nil)
	toolCtx := core.NbToolContext{AccountId: "acct", Ctx: gapsTestContext()}

	resp, err := WorkflowGetTool{}.Call(toolCtx, core.NBToolCallRequest{Arguments: map[string]any{"id": "wf-1"}})

	require.NoError(t, err)
	var out map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(resp.Data), &out))
	assert.Equal(t, []interface{}{
		"- Step `create-incident-ticket` needs a jira or servicenow integration, but it points at the config `ticket_integration_id`, which does not exist, and this account has no jira or servicenow integration connected. Connect one under Integrations, then ask me again.",
	}, out["integration_problems"])
	assert.Equal(t, "triage-ticket", out["name"])
}

func TestWorkflowGet_CleanAutomationHasNoProblemsField(t *testing.T) {
	integrationRunbookStub(t, `[{"key":"ticket_integration_id"}]`, ticketToMissingConfig)
	toolCtx := core.NbToolContext{AccountId: "acct", Ctx: gapsTestContext()}

	resp, err := WorkflowGetTool{}.Call(toolCtx, core.NBToolCallRequest{Arguments: map[string]any{"id": "wf-1"}})

	require.NoError(t, err)
	assert.NotContains(t, resp.Data, "integration_problems")
}

func TestIsValidationRejection(t *testing.T) {
	assert.True(t, IsValidationRejection(&runbookStatusError{status: http.StatusBadRequest, body: "bad"}))
	assert.False(t, IsValidationRejection(&runbookStatusError{status: http.StatusBadGateway}))
	assert.False(t, IsValidationRejection(errors.New("dial tcp: connection refused")))
}
