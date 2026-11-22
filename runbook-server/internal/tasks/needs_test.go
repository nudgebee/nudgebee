package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"slices"
	"sort"
	"strings"
	"testing"

	"nudgebee/runbook/internal/tasks/tickets"
	"nudgebee/runbook/internal/tasks/types"
	"nudgebee/runbook/services/security"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/temporal"
)

const (
	jiraID   = "6f1c2a3e-0000-4000-8000-000000000001"
	githubID = "6f1c2a3e-0000-4000-8000-000000000002"
	sshID    = "6f1c2a3e-0000-4000-8000-000000000003"
	pgID     = "6f1c2a3e-0000-4000-8000-000000000004"
	mcpID    = "6f1c2a3e-0000-4000-8000-000000000005"
	argoID   = "6f1c2a3e-0000-4000-8000-000000000006"
)

// fakeTenant answers integrationTypes for tenant "t": the ids above are one
// integration each, "prod" names both a Jira and a GitHub integration, "flaky"
// fails, and "id-<type>" is an integration of that type.
func fakeTenant(t *testing.T) func(context.Context, string, string, []string) ([]string, error) {
	return func(_ context.Context, tenantID, ref string, accepted []string) ([]string, error) {
		if t != nil {
			require.Equal(t, "t", tenantID)
		}
		byID := map[string]string{jiraID: "jira", githubID: "github", sshID: "ssh", pgID: "postgresql", mcpID: "mcp", argoID: "argocd"}
		if typ, ok := byID[ref]; ok {
			return []string{typ}, nil
		}
		if typ, ok := strings.CutPrefix(ref, "id-"); ok {
			return []string{typ}, nil
		}
		switch ref {
		case "prod":
			var found []string
			for _, typ := range []string{"jira", "github"} {
				if len(accepted) == 0 || slices.Contains(accepted, typ) {
					found = append(found, typ)
				}
			}
			if len(found) > 0 {
				return found, nil
			}
		case "flaky":
			return nil, errors.New("metastore down")
		}
		return nil, ErrIntegrationNotFound
	}
}

func stubIntegrationTypes(t *testing.T) {
	t.Helper()
	orig := integrationTypes
	t.Cleanup(func() { integrationTypes = orig })
	integrationTypes = fakeTenant(t)
}

// grantsOn is a custom-role principal (person or bee: they answer the same)
// holding grants on acct-a, plus the right to run automations there.
func grantsOn(t *testing.T, grants ...string) StepAuthorizer {
	held := map[string]bool{"workflows:Execute": true}
	for _, g := range grants {
		held[g] = true
	}
	return stubAuthorizer(principalContext(t, nil, nil, map[string]map[string]bool{"acct-a": held}), true)
}

func step(task string, params map[string]any) StepAuthInput {
	in := stepInput(task, "acct-a")
	in.Params = params
	return in
}

func ok(t *testing.T, authz StepAuthorizer, in StepAuthInput) {
	t.Helper()
	assert.NoError(t, authz.Authorize(context.Background(), in), in.TaskName)
}

func refused(t *testing.T, authz StepAuthorizer, in StepAuthInput, contains string) {
	t.Helper()
	assertDenied(t, authz.Authorize(context.Background(), in), contains)
}

func id(ref string) map[string]any { return map[string]any{"integration_id": ref} }

// A step that acts through an integration needs exactly its use grant: the
// module grant it needed before does not do, and the use grant needs no module.
func TestThroughStepsNeedOnlyTheIntegrationsUseGrant(t *testing.T) {
	stubIntegrationTypes(t)
	for _, c := range []struct {
		task, ref, grant, module string
	}{
		{"tickets.create", jiraID, "integrations.jira:Execute", "tickets:Write"},
		{"tickets.update", jiraID, "integrations.jira:Execute", "tickets:Write"},
		{"tickets.resolve", "id-pagerduty", "integrations.pagerduty:Execute", "tickets:Write"},
		{"tickets.get", jiraID, "integrations.jira:Read", "tickets:Read"},
		{"dbms.query", pgID, "integrations.postgresql:Execute", "database:Write"},
		{"dbms.redis.cli", "id-redis", "integrations.redis:Execute", "database:Write"},
		{"integrations.ssh", sshID, "integrations.ssh:Execute", "integrations:Write"},
		{"mq.rabbitmqadmin.cli", "id-rabbitmq", "integrations.rabbitmq:Execute", "integrations:Write"},
		{"scm.github.cli", githubID, "integrations.github:Execute", "integrations:Write"},
		{"scm.gitlab.cli", "id-gitlab", "integrations.gitlab:Execute", "integrations:Write"},
		{"cicd.argocd.cli", argoID, "integrations.argocd:Execute", "k8s:Write"},
		{"llm.mcp_call", mcpID, "integrations.mcp:Execute", "integrations:Write"},
	} {
		in := step(c.task, id(c.ref))
		ok(t, grantsOn(t, c.grant), in)
		refused(t, grantsOn(t, c.module), in, "needs "+c.grant+" on account acct-a")
		// Write on the integration's module is the right to manage it, not to use it.
		refused(t, grantsOn(t, strings.TrimSuffix(strings.TrimSuffix(c.grant, ":Execute"), ":Read")+":Write"), in, c.grant)
	}
}

func TestReadGrantRunsOnlyReadSteps(t *testing.T) {
	stubIntegrationTypes(t)
	authz := grantsOn(t, "integrations.jira:Read")
	ok(t, authz, step("tickets.get", id(jiraID)))
	ok(t, authz, step("tickets.get_comments", id(jiraID)))
	refused(t, authz, step("tickets.create", id(jiraID)), "needs integrations.jira:Execute")
	ok(t, grantsOn(t, "integrations.jira:Execute"), step("tickets.get", id(jiraID))) // Execute covers reading
}

// Execute on the integrations module itself (granted for the MCP health check)
// runs no step: the SSH, CLI and MCP steps need their integration's grant, and
// HTTP needs Write.
func TestBareIntegrationsExecuteRunsNoStep(t *testing.T) {
	stubIntegrationTypes(t)
	authz := grantsOn(t, "integrations:Execute")
	refused(t, authz, step("integrations.ssh", id(sshID)), "needs integrations.ssh:Execute")
	refused(t, authz, step("scm.github.cli", id(githubID)), "needs integrations.github:Execute")
	refused(t, authz, step("integrations.http", map[string]any{"url": "https://example.com"}), "needs integrations:Write")
	refused(t, authz, step("llm.mcp_call", map[string]any{"url": "https://example.com/mcp"}), "needs integrations:Write")
}

// A step calling an endpoint of its own choosing, with no integration behind
// it, needs integrations:Write, as before.
func TestFreeFormEndpointsNeedIntegrationsWrite(t *testing.T) {
	stubIntegrationTypes(t)
	authz := grantsOn(t, "integrations:Write")
	ok(t, authz, step("integrations.http", map[string]any{"url": "https://example.com"}))
	ok(t, authz, step("llm.a2a_call", map[string]any{"url": "https://example.com"}))
	ok(t, authz, step("llm.mcp_call", map[string]any{"connection_mode": "direct", "url": "https://example.com/mcp"}))
	// Through an MCP integration it is the integration's grant, whatever the mode says.
	refused(t, authz, step("llm.mcp_call", map[string]any{"connection_mode": "direct", "integration_id": mcpID}), "needs integrations.mcp:Execute")
}

func TestScriptNeedsWorkflowsWriteAndOverSSHTheSSHGrant(t *testing.T) {
	stubIntegrationTypes(t)
	overSSH := step("scripting.run_script", map[string]any{"executor_type": "ssh", "integration_id": sshID})
	refused(t, grantsOn(t, "integrations.ssh:Execute"), overSSH, "needs workflows:Write")
	refused(t, grantsOn(t, "workflows:Write"), overSSH, "needs integrations.ssh:Execute")
	ok(t, grantsOn(t, "workflows:Write", "integrations.ssh:Execute"), overSSH)

	// Other executors do not use integration_id.
	ok(t, grantsOn(t, "workflows:Write"), step("scripting.run_script", map[string]any{"executor_type": "kubernetes", "integration_id": sshID}))
	ok(t, grantsOn(t, "workflows:Write"), step("scripting.run_script", map[string]any{"script": "echo"}))
}

// With GitOps on, a rightsize step opens a pull request through the
// integration and only reads the cluster; with a ticket setting on, it files a
// ticket; otherwise it patches the cluster.
func TestRightsizeNeedsFollowWhatTheStepDoes(t *testing.T) {
	stubIntegrationTypes(t)
	gitops := func(cfg map[string]any) map[string]any {
		cfg["enabled"] = true
		return map[string]any{"gitops_config": cfg}
	}
	pr := grantsOn(t, "k8s:Read", "integrations.github:Execute")
	for _, task := range []string{"k8s.vertical_rightsize", "k8s.horizontal_rightsize", "k8s.pv_rightsize"} {
		ok(t, pr, step(task, gitops(map[string]any{"integration_id": githubID})))
		refused(t, grantsOn(t, "integrations.github:Execute"), step(task, gitops(map[string]any{"integration_id": githubID})), "needs k8s:Read")
		refused(t, grantsOn(t, "k8s:Read"), step(task, gitops(map[string]any{"integration_id": githubID})), "needs integrations.github:Execute")
		// GitOps naming no integration goes through one of the provider's type.
		ok(t, pr, step(task, gitops(map[string]any{})))
		// GitOps off: a cluster change.
		refused(t, pr, step(task, map[string]any{"gitops_config": map[string]any{"enabled": false, "integration_id": githubID}}), "needs k8s:Execute")
		ok(t, grantsOn(t, "k8s:Execute"), step(task, nil))
		// An id of another type needs that type's grant.
		refused(t, pr, step(task, gitops(map[string]any{"integration_id": jiraID})), "needs integrations.jira:Execute")
	}

	// vertical and horizontal pick the integration by name too, and a GitLab provider.
	ok(t, grantsOn(t, "k8s:Read", "integrations.gitlab:Execute"), step("k8s.vertical_rightsize", gitops(map[string]any{"provider": "gitlab"})))
	refused(t, pr, step("k8s.vertical_rightsize", gitops(map[string]any{"provider": "gitlab"})), "needs integrations.gitlab:Execute")
	refused(t, pr, step("k8s.vertical_rightsize", gitops(map[string]any{"name": "nope"})), "which this organisation does not have")
	// repository_name may be a repository: one the tenant has no integration
	// named for counts as naming none.
	ok(t, pr, step("k8s.horizontal_rightsize", gitops(map[string]any{"repository_name": "org/repo"})))
	// pv_rightsize reads only integration_id (repository_name is its repository).
	ok(t, pr, step("k8s.pv_rightsize", gitops(map[string]any{"repository_name": "org/repo"})))

	// A ticket in place of the change (vertical, horizontal).
	ticket := map[string]any{"ticket_config": map[string]any{"enabled": true, "configuration_id": jiraID}}
	ok(t, grantsOn(t, "k8s:Read", "integrations.jira:Execute"), step("k8s.vertical_rightsize", ticket))
	refused(t, grantsOn(t, "k8s:Write"), step("k8s.vertical_rightsize", ticket), "needs integrations.jira:Execute")
	refused(t, grantsOn(t, "k8s:Read", "integrations.jira:Execute"), step("k8s.vertical_rightsize",
		map[string]any{"ticket_config": map[string]any{"enabled": true}}), "names no integration")
	// pv_rightsize ignores a ticket setting: it patches.
	refused(t, grantsOn(t, "k8s:Read", "integrations.jira:Execute"), step("k8s.pv_rightsize", ticket), "needs k8s:Execute")
	// A step that ignores GitOps (pod delete) still changes the cluster.
	refused(t, pr, step("k8s.pod_delete", gitops(map[string]any{"integration_id": githubID})), "needs k8s:Execute")
}

func TestUseIsCheckedOnTheStepsAccount(t *testing.T) {
	stubIntegrationTypes(t)
	in := step("tickets.create", id(jiraID))
	in.StepAccountID = "acct-b"
	refused(t, grantsOn(t, "integrations.jira:Execute"), in, "on account acct-b")
}

func TestIntegrationResolution(t *testing.T) {
	stubIntegrationTypes(t)
	jira := grantsOn(t, "integrations.jira:Execute")
	// A name matching integrations of two types needs both grants.
	refused(t, jira, step("tickets.create", id("prod")), "needs integrations.github:Execute")
	ok(t, grantsOn(t, "integrations.jira:Execute", "integrations.github:Execute"), step("tickets.create", id("prod")))
	// An integration the organisation does not have.
	refused(t, jira, step("tickets.create", id("nope")), "which this organisation does not have")
	// No integration named, and more than one type it could be.
	refused(t, jira, step("tickets.create", nil), "names no integration in integration_id")
	// No integration named, one type it can be: that type's grant.
	refused(t, jira, step("integrations.ssh", nil), "needs integrations.ssh:Execute")
	ok(t, grantsOn(t, "integrations.ssh:Execute"), step("integrations.ssh", nil))
	// A template that was not rendered.
	refused(t, jira, step("tickets.create", id("{{ Inputs.tracker }}")), "by a template")
	// A lookup failure is an outage, not a refusal.
	err := jira.Authorize(context.Background(), step("tickets.create", id("flaky")))
	require.Error(t, err)
	var appErr *temporal.ApplicationError
	assert.False(t, errors.As(err, &appErr), "an outage must stay retryable")
	assert.Contains(t, err.Error(), "authorization unavailable")
}

// A built-in account role needs no lookup: it allows any integration, as it
// allowed the module.
func TestBuiltInRoleNeedsNoIntegrationLookup(t *testing.T) {
	orig := integrationTypes
	t.Cleanup(func() { integrationTypes = orig })
	integrationTypes = func(context.Context, string, string, []string) ([]string, error) {
		t.Fatal("a built-in role's step must not read integrations")
		return nil, nil
	}
	admin := stubAuthorizer(principalContext(t, []string{security.AUTH_ACCOUNT_ADMIN_ROLE}, map[string][]string{security.AUTH_ACCOUNT_ADMIN_ROLE: {"acct-a"}}, nil), true)
	ok(t, admin, step("tickets.create", id("nope")))
	ok(t, admin, step("tickets.create", nil))
}

// beforeTable is the step table built-in roles are held to.
func beforeTable(t *testing.T) map[string]Grant {
	t.Helper()
	body, err := os.ReadFile("testdata/step_requirements_before.json")
	require.NoError(t, err)
	var table struct {
		Steps map[string]Grant `json:"steps"`
	}
	require.NoError(t, json.Unmarshal(body, &table))
	require.Len(t, table.Steps, 80)
	return table.Steps
}

// paramVariants are params that exercise each branch of a step's needs: every
// type it accepts by id, none named, GitOps and ticket settings on and off.
func paramVariants(req StepRequirement) []map[string]any {
	variants := []map[string]any{nil}
	refs := []string{"", "nope"}
	if req.Through != nil {
		for _, typ := range req.Through.Types {
			refs = append(refs, "id-"+typ)
		}
	}
	refs = append(refs, githubID, jiraID, sshID, mcpID)
	for _, ref := range refs {
		variants = append(variants,
			map[string]any{"integration_id": ref},
			map[string]any{"executor_type": "ssh", "integration_id": ref},
			map[string]any{"gitops_config": map[string]any{"enabled": true, "integration_id": ref}},
			map[string]any{"ticket_config": map[string]any{"enabled": true, "configuration_id": ref}},
		)
	}
	return variants
}

// builtInContexts are the built-in roles, each scoped to acct-a where it is
// account-scoped.
func builtInContexts(t *testing.T) map[string]*security.SecurityContext {
	scoped := func(role string) *security.SecurityContext {
		return principalContext(t, []string{role}, map[string][]string{role: {"acct-a"}}, nil)
	}
	return map[string]*security.SecurityContext{
		"super_admin":                  principalContext(t, []string{security.AUTH_SUPER_ADMIN_FULL_ROLE}, nil, nil),
		"super_admin_readonly":         principalContext(t, []string{security.AUTH_SUPER_ADMIN_READONLY_ROLE}, nil, nil),
		"tenant_admin":                 principalContext(t, []string{security.AUTH_TENANT_ADMIN_ROLE}, nil, nil),
		"tenant_admin_readonly":        principalContext(t, []string{security.AUTH_TENANT_READ_ADMIN_ROLE}, nil, nil),
		"account_admin":                scoped(security.AUTH_ACCOUNT_ADMIN_ROLE),
		"account_admin_readonly":       scoped(security.AUTH_ACCOUNT_READ_ADMIN_ROLE),
		"k8s_namespace_admin":          scoped(security.AUTH_K8S_NAMESPACE_ADMIN_ROLE),
		"k8s_namespace_admin_readonly": scoped(security.AUTH_K8S_NAMESPACE_READ_ADMIN_ROLE),
		"no role":                      principalContext(t, nil, nil, nil),
	}
}

// Every built-in role is allowed exactly the steps it was allowed before, on
// every account, whatever integration a step names: it passes a use check by
// the same account access it passed the module check with.
func TestBuiltInRolesAnswerAsBefore(t *testing.T) {
	stubIntegrationTypes(t)
	before := beforeTable(t)
	for role, sc := range builtInContexts(t) {
		authz := stubAuthorizer(sc, true)
		for name, old := range before {
			req, found := RequirementFor(name)
			require.Truef(t, found, "%s is no longer declared", name)
			for _, account := range []string{"acct-a", "acct-b"} {
				want := old.Module == "" || sc.Can(account, old.Module, old.Class)
				for _, params := range paramVariants(req) {
					in := StepAuthInput{TaskName: name, TenantID: "t", Principal: "u", StepAccountID: account, Params: params}
					err := authz.Authorize(context.Background(), in)
					assert.Equalf(t, want, err == nil, "%s on %s, step %s with %v: %v", role, account, name, params, err)
				}
			}
		}
	}
}

// integrationFieldPaths are the params of a task's schema that name an
// integration (integration- or ticket-typed), nested ones as dotted paths.
func integrationFieldPaths(schema *types.Schema, prefix string) []string {
	var paths []string
	for name, prop := range schema.Properties {
		switch {
		case prop.Type == types.PropertyTypeIntegration, prop.Type == types.PropertyTypeTicket:
			paths = append(paths, prefix+name)
		case prop.Schema != nil:
			paths = append(paths, integrationFieldPaths(prop.Schema, prefix+name+".")...)
		}
	}
	return paths
}

// withParam sets a dotted param, turning on the settings objects on the way.
func withParam(path, value string) map[string]any {
	keys := strings.Split(path, ".")
	params := map[string]any{}
	cur := params
	for _, key := range keys[:len(keys)-1] {
		next := map[string]any{"enabled": true}
		cur[key] = next
		cur = next
	}
	cur[keys[len(keys)-1]] = value
	return params
}

// Every param a task can be pointed at an integration with is one its step is
// checked on, and naming an integration there makes the step need that
// integration's grant. This is what keeps an integration from being used
// without a grant now that there is no separate check of every integration a
// step names: a new integration field fails here until the table covers it.
func TestEveryIntegrationFieldIsChecked(t *testing.T) {
	for _, task := range NewInitializedTaskRegistry().ListTasks() {
		req, found := RequirementFor(task.GetName())
		require.True(t, found, task.GetName())
		if schema := task.InputSchema(); schema != nil {
			for _, path := range integrationFieldPaths(schema, "") {
				assert.Containsf(t, req.IntegrationFields(), path, "%s takes an integration in %s that its step is not checked on", task.GetName(), path)
			}
		}
		for _, path := range req.IntegrationFields() {
			params := withParam(path, "id-x")
			if task.GetName() == "scripting.run_script" {
				params["executor_type"] = "ssh"
			}
			var used bool
			for _, n := range req.stepNeeds(params) {
				used = used || n.use != nil && n.use.param == path && n.use.ref == "id-x"
			}
			assert.Truef(t, used, "%s: naming an integration in %s does not make the step need its grant", task.GetName(), path)
		}
	}
}

// The types a through step accepts are the ones its task resolves with.
func TestThroughTypesMatchTheTasks(t *testing.T) {
	registry := NewInitializedTaskRegistry()
	assert.Equal(t, []string{"jira", "github", "gitlab", "pagerduty", "zenduty", "servicenow", "freshdesk"}, tickets.TicketPlatforms())
	assert.Equal(t, []string{"pagerduty", "zenduty"}, tickets.IncidentPlatforms())
	for name, req := range stepRequirements {
		if req.Through == nil {
			continue
		}
		task, err := registry.GetTask(name)
		require.NoError(t, err)
		prop := task.InputSchema().Properties[req.Through.Param]
		require.Containsf(t, []types.PropertyType{types.PropertyTypeIntegration, types.PropertyTypeTicket}, prop.Type, "%s.%s", name, req.Through.Param)
		switch {
		case len(prop.SubTypes) > 0:
			assert.Equalf(t, prop.SubTypes, req.Through.Types, "%s", name)
		case prop.SubType != "":
			assert.Equalf(t, []string{prop.SubType}, req.Through.Types, "%s", name)
		}
		for _, typ := range req.Through.Types {
			assert.Equalf(t, strings.ToLower(typ), typ, "%s: types are matched lower-case", name)
		}
	}
	dbms, err := registry.GetTask("dbms.query")
	require.NoError(t, err)
	assert.Equal(t, dbms.InputSchema().Properties["dbms_type"].Options, stepRequirements["dbms.query"].Through.Types)
}

// stepNeedsCases is the shared table of StepNeeds answers (also read by the
// bee install's grant computation tests).
type stepNeedsCase struct {
	Name   string              `json:"name"`
	Task   string              `json:"task"`
	Params map[string]any      `json:"params"`
	Types  map[string][]string `json:"types"`
	Want   []Grant             `json:"want"`
	Error  string              `json:"error"`
}

func TestStepNeedsCases(t *testing.T) {
	body, err := os.ReadFile("testdata/step_needs_cases.json")
	require.NoError(t, err)
	var table struct {
		Cases []stepNeedsCase `json:"cases"`
	}
	require.NoError(t, json.Unmarshal(body, &table))
	require.NotEmpty(t, table.Cases)
	for _, c := range table.Cases {
		resolve := func(_ context.Context, ref string, _ []string) ([]string, error) {
			if types, ok := c.Types[ref]; ok {
				return types, nil
			}
			return nil, ErrIntegrationNotFound
		}
		got, err := StepNeeds(context.Background(), c.Task, c.Params, resolve)
		if c.Error != "" {
			require.Errorf(t, err, c.Name)
			assert.Containsf(t, err.Error(), c.Error, c.Name)
			continue
		}
		require.NoErrorf(t, err, c.Name)
		sort.Slice(got, func(i, j int) bool { return got[i].Module+got[i].Class < got[j].Module+got[j].Class })
		want := c.Want
		if len(want) == 0 {
			assert.Emptyf(t, got, c.Name)
			continue
		}
		sort.Slice(want, func(i, j int) bool { return want[i].Module+want[i].Class < want[j].Module+want[j].Class })
		assert.Equalf(t, want, got, c.Name)
	}
}

// Every step type has at least one case in the shared table.
func TestStepNeedsCasesCoverEveryStep(t *testing.T) {
	body, err := os.ReadFile("testdata/step_needs_cases.json")
	require.NoError(t, err)
	var table struct {
		Cases []stepNeedsCase `json:"cases"`
	}
	require.NoError(t, json.Unmarshal(body, &table))
	covered := map[string]bool{}
	for _, c := range table.Cases {
		covered[c.Task] = true
	}
	var missing []string
	for name := range stepRequirements {
		if !covered[name] && !isExtension(name) {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	assert.Empty(t, missing)
}

func isExtension(name string) bool {
	for _, task := range extensionTasks {
		if task.GetName() == name {
			return true
		}
	}
	return false
}
