package tasks

import (
	"context"
	"errors"
	"testing"

	"nudgebee/runbook/internal/tasks/types"
	"nudgebee/runbook/services/security"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/temporal"
)

const jiraID = "6f1c2a3e-0000-4000-8000-000000000001"

// stubIntegrationTypes answers integrationTypes from a fixed tenant: jiraID is
// a Jira integration, "prod" names both a Jira and a GitHub integration.
func stubIntegrationTypes(t *testing.T) {
	t.Helper()
	orig := integrationTypes
	t.Cleanup(func() { integrationTypes = orig })
	integrationTypes = func(_ context.Context, tenantID string, use IntegrationUse) ([]string, error) {
		require.Equal(t, "t", tenantID)
		switch use.Ref {
		case "":
			return use.Types, nil
		case jiraID:
			return []string{"jira"}, nil
		case "prod":
			return []string{"jira", "github"}, nil
		case "flaky":
			return nil, errors.New("metastore down")
		}
		return nil, errIntegrationNotFound
	}
}

// beeAuthorizer is an authorizer whose principal is a bee holding grants on acct-a.
func beeAuthorizer(t *testing.T, grants map[string]bool) StepAuthorizer {
	sc := principalContext(t, nil, nil, map[string]map[string]bool{"acct-a": grants})
	return StepAuthorizer{
		LoadContext: func(context.Context, string, string) (*security.SecurityContext, error) { return sc, nil },
		UserStatus: func(context.Context, string, string) (UserState, error) {
			return UserState{Active: true, Bee: true}, nil
		},
	}
}

func ticketStep(task, ref string) StepAuthInput {
	in := stepInput(task, "acct-a")
	in.Integrations = []IntegrationUse{{Ref: ref}}
	return in
}

func TestAuthorizeBeeNeedsAUseGrantForTheIntegrationsType(t *testing.T) {
	stubIntegrationTypes(t)
	steps := map[string]bool{"workflows:Execute": true, "tickets:Write": true}
	authz := beeAuthorizer(t, steps)
	assertDenied(t, authz.Authorize(context.Background(), ticketStep("tickets.create", jiraID)),
		"needs integrations.jira:Execute on account acct-a")
	assertDenied(t, authz.Authorize(context.Background(), ticketStep("tickets.get", jiraID)),
		"needs integrations.jira:Read on account acct-a")

	// Read lets the bee read through Jira, not change it.
	steps["integrations.jira:Read"] = true
	authz = beeAuthorizer(t, steps)
	assert.NoError(t, authz.Authorize(context.Background(), ticketStep("tickets.get", jiraID)))
	assertDenied(t, authz.Authorize(context.Background(), ticketStep("tickets.create", jiraID)), "integrations.jira:Execute")

	steps["integrations.jira:Execute"] = true
	authz = beeAuthorizer(t, steps)
	assert.NoError(t, authz.Authorize(context.Background(), ticketStep("tickets.create", jiraID)))
}

func TestAuthorizeBeeManageGrantIsNotAUseGrant(t *testing.T) {
	stubIntegrationTypes(t)
	authz := beeAuthorizer(t, map[string]bool{"workflows:Execute": true, "tickets:Write": true, "integrations.jira:Write": true})
	assertDenied(t, authz.Authorize(context.Background(), ticketStep("tickets.get", jiraID)), "integrations.jira:Read")
}

func TestAuthorizeBeeStillNeedsTheStepsModule(t *testing.T) {
	stubIntegrationTypes(t)
	authz := beeAuthorizer(t, map[string]bool{"workflows:Execute": true, "integrations.jira:Execute": true})
	assertDenied(t, authz.Authorize(context.Background(), ticketStep("tickets.create", jiraID)), "needs tickets:Write")
}

func TestAuthorizeBeeNamingAnIntegrationByAmbiguousNameNeedsEveryType(t *testing.T) {
	stubIntegrationTypes(t)
	authz := beeAuthorizer(t, map[string]bool{"workflows:Execute": true, "tickets:Write": true, "integrations.jira:Execute": true})
	assertDenied(t, authz.Authorize(context.Background(), ticketStep("tickets.create", "prod")), "integrations.github:Execute")
}

func TestAuthorizeBeeNamingAnUnknownIntegrationIsRefused(t *testing.T) {
	stubIntegrationTypes(t)
	authz := beeAuthorizer(t, map[string]bool{"workflows:Execute": true, "tickets:Write": true})
	assertDenied(t, authz.Authorize(context.Background(), ticketStep("tickets.create", "nope")), "which this organisation does not have")
}

func TestAuthorizeBeeIntegrationLookupFailureIsRetryable(t *testing.T) {
	stubIntegrationTypes(t)
	authz := beeAuthorizer(t, map[string]bool{"workflows:Execute": true, "tickets:Write": true})
	err := authz.Authorize(context.Background(), ticketStep("tickets.create", "flaky"))
	require.Error(t, err)
	var appErr *temporal.ApplicationError
	assert.False(t, errors.As(err, &appErr), "an outage must stay retryable")
}

func TestAuthorizeBeeGitOpsNeedsTheProvidersGrantWithoutAnIntegrationNamed(t *testing.T) {
	stubIntegrationTypes(t)
	authz := beeAuthorizer(t, map[string]bool{"workflows:Execute": true, "k8s:Write": true})
	in := stepInput("k8s.vertical_rightsize", "acct-a")
	in.Integrations = IntegrationUses(nil, map[string]any{"gitops_config": map[string]any{"enabled": true}})
	assertDenied(t, authz.Authorize(context.Background(), in), "needs integrations.github:Execute")
}

func TestAuthorizeBeeGitOpsRepositoryNameThatNamesNoIntegrationIsNotRefused(t *testing.T) {
	stubIntegrationTypes(t)
	authz := beeAuthorizer(t, map[string]bool{"workflows:Execute": true, "k8s:Write": true, "integrations.github:Execute": true})
	in := stepInput("k8s.pv_rightsize", "acct-a")
	in.Integrations = IntegrationUses(nil, map[string]any{"gitops_config": map[string]any{"enabled": true, "integration_id": jiraID, "repository_name": "org/repo"}})
	assertDenied(t, authz.Authorize(context.Background(), in), "integration \"6f1c2a3e") // jiraID is a Jira integration used as GitHub's
	in.Integrations = IntegrationUses(nil, map[string]any{"gitops_config": map[string]any{"enabled": true, "repository_name": "org/repo"}})
	assert.NoError(t, authz.Authorize(context.Background(), in))
}

func TestAuthorizeAPersonNeedsNoUseGrant(t *testing.T) {
	// People keep today's rules: the step's module is enough.
	stubIntegrationTypes(t)
	sc := principalContext(t, nil, nil, map[string]map[string]bool{"acct-a": {"workflows:Execute": true, "tickets:Write": true}})
	assert.NoError(t, stubAuthorizer(sc, true).Authorize(context.Background(), ticketStep("tickets.create", "nope")))
}

func TestIntegrationUsesReadsTheSchemasIntegrationFields(t *testing.T) {
	schema := &types.Schema{Properties: map[string]types.Property{
		"integration_id": {Type: types.PropertyTypeIntegration, SubType: "redis"},
		"ticket":         {Type: types.PropertyTypeTicket, SubTypes: []string{"pagerduty", "zenduty"}},
		"command":        {Type: types.PropertyTypeString},
		"nested": {Type: types.PropertyTypeObject, Schema: &types.Schema{Properties: map[string]types.Property{
			"integration_id": {Type: types.PropertyTypeIntegration},
		}}},
		"off": {Type: types.PropertyTypeObject, Schema: &types.Schema{Properties: map[string]types.Property{
			"integration_id": {Type: types.PropertyTypeIntegration},
		}}},
	}}
	uses := IntegrationUses(schema, map[string]any{
		"integration_id": " cache ",
		"ticket":         "pd",
		"command":        "PING",
		"nested":         map[string]any{"integration_id": "inner"},
		"off":            map[string]any{"enabled": false, "integration_id": "unused"},
	})
	assert.ElementsMatch(t, []IntegrationUse{
		{Ref: "cache", Types: []string{"redis"}},
		{Ref: "pd", Types: []string{"pagerduty", "zenduty"}},
		{Ref: "inner"},
	}, uses)
	assert.Empty(t, IntegrationUses(schema, map[string]any{"command": "PING"}))
}

func TestIntegrationUsesReadsGitOpsAndTicketSettings(t *testing.T) {
	// repository_name may name the integration or only a repository: the
	// provider's PR then goes through GitHub when nothing else names one.
	assert.Equal(t, []IntegrationUse{{Ref: "repo", Types: []string{"gitlab"}, Optional: true}, {Types: []string{"gitlab", "github"}}},
		IntegrationUses(nil, map[string]any{"gitops_config": map[string]any{"enabled": true, "provider": "gitlab", "repository_name": "repo"}}))
	// Each ref given is checked: tasks differ on which one picks the integration.
	assert.Equal(t, []IntegrationUse{{Ref: jiraID, Types: []string{"github"}}, {Ref: "other", Types: []string{"github"}}},
		IntegrationUses(nil, map[string]any{"gitops_config": map[string]any{"enabled": true, "integration_id": jiraID, "name": "other"}}))
	// With none, the pull request goes through a GitHub integration.
	assert.Equal(t, []IntegrationUse{{Types: []string{"gitlab", "github"}}},
		IntegrationUses(nil, map[string]any{"gitops_config": map[string]any{"enabled": true, "provider": "gitlab"}}))
	assert.Equal(t, []IntegrationUse{{Ref: jiraID}},
		IntegrationUses(nil, map[string]any{"ticket_config": map[string]any{"enabled": true, "configuration_id": jiraID}}))
	assert.Empty(t, IntegrationUses(nil, map[string]any{
		"gitops_config": map[string]any{"enabled": false, "integration_id": "x"},
		"ticket_config": map[string]any{"configuration_id": "y"},
	}))
}

// Every registered task that takes an integration_id declares it as an
// integration (or ticket) field, so IntegrationUses sees it. The GitOps and
// ticket settings are read by name instead.
func TestEveryIntegrationIdIsDeclaredAsAnIntegrationField(t *testing.T) {
	var undeclared []string
	var walk func(task string, schema *types.Schema, path string)
	walk = func(task string, schema *types.Schema, path string) {
		for name, prop := range schema.Properties {
			if name == "integration_id" && prop.Type != types.PropertyTypeIntegration && prop.Type != types.PropertyTypeTicket &&
				path != "gitops_config." && path != "ticket_config." {
				undeclared = append(undeclared, task+": "+path+name)
			}
			if prop.Schema != nil {
				walk(task, prop.Schema, path+name+".")
			}
		}
	}
	for _, task := range NewInitializedTaskRegistry().ListTasks() {
		if schema := task.InputSchema(); schema != nil {
			walk(task.GetName(), schema, "")
		}
	}
	assert.Empty(t, undeclared)
}
