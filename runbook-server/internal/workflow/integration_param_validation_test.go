package workflow

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"

	"nudgebee/runbook/internal/tasks/tickets"
	"nudgebee/runbook/internal/tasks/types"
	"nudgebee/runbook/services/security"
)

func TestIntegrationKinds(t *testing.T) {
	assert.Equal(t, []string{"pagerduty", "zenduty"},
		integrationKinds(types.Property{Type: types.PropertyTypeTicket, SubTypes: []string{"pagerduty", "zenduty"}}))
	assert.Equal(t, []string{"github"},
		integrationKinds(types.Property{Type: types.PropertyTypeTicket, SubType: "github"}))
	assert.Equal(t, tickets.TicketPlatforms(),
		integrationKinds(types.Property{Type: types.PropertyTypeTicket}))
	assert.Equal(t, []string{"redis"},
		integrationKinds(types.Property{Type: types.PropertyTypeIntegration, SubType: "redis"}))
	assert.Nil(t, integrationKinds(types.Property{Type: types.PropertyTypeIntegration}),
		"an integration parameter that declares no kind is not checked")
}

type resolveCall struct {
	value string
	kinds []string
}

// stubResolver replaces the database lookup for one test and records what it was asked.
func stubResolver(t *testing.T, err error) *[]resolveCall {
	t.Helper()
	calls := &[]resolveCall{}
	orig := resolveIntegrationParam
	resolveIntegrationParam = func(_ *security.RequestContext, value string, kinds []string) (string, error) {
		*calls = append(*calls, resolveCall{value, kinds})
		if err != nil {
			return "", err
		}
		return "resolved-" + value, nil
	}
	t.Cleanup(func() { resolveIntegrationParam = orig })
	return calls
}

func configs(values map[string]string) func(string) (string, bool) {
	return func(key string) (string, bool) {
		v, ok := values[key]
		return v, ok
	}
}

func TestValidateIntegrationParam(t *testing.T) {
	ticketProp := types.Property{Type: types.PropertyTypeTicket, SubTypes: []string{"jira", "servicenow"}}
	noConfigs := configs(nil)

	t.Run("literal value is checked against the declared kinds", func(t *testing.T) {
		calls := stubResolver(t, nil)
		id, err := validateIntegrationParam(nil, "task 'create'", "integration_id", ticketProp, "prod-jira", noConfigs)
		assert.NoError(t, err)
		assert.Equal(t, "resolved-prod-jira", id)
		assert.Equal(t, []resolveCall{{"prod-jira", []string{"jira", "servicenow"}}}, *calls)
	})

	t.Run("lone Configs reference is checked through the config's value", func(t *testing.T) {
		calls := stubResolver(t, errors.New("this account has no jira or servicenow integration connected"))
		_, err := validateIntegrationParam(nil, "task 'create'", "integration_id", ticketProp,
			"{{ Configs.ticketing_integration_id }}", configs(map[string]string{"ticketing_integration_id": "dead-id"}))
		assert.EqualError(t, err, "task 'create' parameter 'integration_id' (set from config 'ticketing_integration_id') validation failed: this account has no jira or servicenow integration connected")
		assert.Equal(t, []resolveCall{{"dead-id", []string{"jira", "servicenow"}}}, *calls)
	})

	t.Run("missing config is left to run time", func(t *testing.T) {
		calls := stubResolver(t, nil)
		id, err := validateIntegrationParam(nil, "task 'create'", "integration_id", ticketProp, "{{ Configs.ticket_integration_id }}", noConfigs)
		assert.NoError(t, err)
		assert.Empty(t, id)
		assert.Empty(t, *calls)
	})

	t.Run("other templates are left to run time", func(t *testing.T) {
		calls := stubResolver(t, nil)
		for _, v := range []string{"{{ Inputs.integration_id }}", "{{ Configs.a }}-{{ Configs.b }}", "{% if x %}a{% endif %}"} {
			id, err := validateIntegrationParam(nil, "task 'create'", "integration_id", ticketProp, v, configs(map[string]string{"a": "x"}))
			assert.NoError(t, err)
			assert.Empty(t, id)
		}
		assert.Empty(t, *calls)
	})

	t.Run("parameter with no declared kind is not checked", func(t *testing.T) {
		calls := stubResolver(t, nil)
		id, err := validateIntegrationParam(nil, "task 'script'", "integration_id",
			types.Property{Type: types.PropertyTypeIntegration}, "some-ssh-box", noConfigs)
		assert.NoError(t, err)
		assert.Empty(t, id)
		assert.Empty(t, *calls)
	})

	t.Run("lookup failure names the owner and parameter", func(t *testing.T) {
		stubResolver(t, errors.New("integration 'incident-webhook' is a workflow_webhook integration; this step needs a jira or servicenow integration"))
		_, err := validateIntegrationParam(nil, "hook 'failure[0]' for 'notify'", "integration_id", ticketProp, "incident-webhook", noConfigs)
		assert.EqualError(t, err, "hook 'failure[0]' for 'notify' parameter 'integration_id' validation failed: integration 'incident-webhook' is a workflow_webhook integration; this step needs a jira or servicenow integration")
	})
}
