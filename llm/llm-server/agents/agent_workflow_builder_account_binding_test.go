package agents

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

const testAccountUUID = "a2a30b02-0f67-42e5-a2ab-c658230fd798"

// noConfigs stands in for an account with no configs at all — reachable API, empty result.
func noConfigs(string) (string, bool, bool) { return "", false, true }

// configsUnavailable stands in for a config API that could not be read.
func configsUnavailable(string) (string, bool, bool) { return "", false, false }

func configsHolding(values map[string]string) func(string) (string, bool, bool) {
	return func(key string) (string, bool, bool) {
		v, ok := values[key]
		return v, ok, true
	}
}

func cliTask(id, taskType string, params map[string]interface{}) map[string]interface{} {
	t := map[string]interface{}{"id": id, "type": taskType}
	if params != nil {
		t["params"] = params
	}
	return t
}

// An absent or empty account_id must NOT be reported. The parameter is `Required: false` in every
// task schema and the engine defaults it to the automation's own account — see
// `accountId := taskCtx.GetAccountID()` in runbook-server internal/tasks/k8s/cli.go:35 — which is
// exactly the account the user selected. Omitting it is the correct, idiomatic definition, and
// flagging it would reject the majority of valid automations.
func TestLintCloudAccountIds_OmittedAccountIdIsCorrectNotBroken(t *testing.T) {
	cases := []struct {
		name  string
		tasks []interface{}
	}{
		{
			name:  "params absent entirely",
			tasks: []interface{}{cliTask("fetch-pods", "k8s.cli", nil)},
		},
		{
			name:  "params present, account_id absent",
			tasks: []interface{}{cliTask("fetch-pods", "k8s.cli", map[string]interface{}{"command": "get pods"})},
		},
		{
			name:  "account_id empty string",
			tasks: []interface{}{cliTask("fetch-pods", "k8s.cli", map[string]interface{}{"account_id": ""})},
		},
		{
			name:  "account_id whitespace only",
			tasks: []interface{}{cliTask("fetch-pods", "k8s.cli", map[string]interface{}{"account_id": "   "})},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var issues []string
			walkTasksAndLintAccountIds(tc.tasks, noConfigs, &issues)
			assert.Empty(t, issues, "the engine defaults an omitted account_id to the automation's own account")
		})
	}
}

// 41 task types across tickets, dbms, observability, scripting, github and rabbitmq declare an
// account-typed parameter — not just the four cloud-CLI types this lint originally checked. The
// rule is about the VALUE, so it must apply wherever account_id is set, whatever the task type.
func TestLintCloudAccountIds_AppliesToAnyTaskTypeThatSetsAccountId(t *testing.T) {
	for _, taskType := range []string{
		"k8s.cli", "cloud.aws.cli", "cloud.gcp.cli", "cloud.azure.cli",
		"tickets.create", "dbms.redis", "observability.logs", "scripting.run_script",
		"scm.github", "mq.rabbitmqadmin", "ai.llm_event_investigate",
	} {
		var issues []string
		walkTasksAndLintAccountIds(
			[]interface{}{cliTask("t1", taskType, map[string]interface{}{"account_id": "{{ Configs.nope }}"})},
			noConfigs, &issues)

		assert.Len(t, issues, 1, "%s sets an unresolvable account_id and must be flagged", taskType)
		assert.Contains(t, issues[0], taskType, "the rejection must name the task type")
	}
}

// The live failure shape. The builder emitted `{{ Configs.k8s_dev_account_id }}` instead of the
// UUID it had been given; checkMissingConfigs auto-creates a missing config EMPTY, so this saves
// as Live v1 with its account resolving to "<TODO: set value>". Two such configs already sit on
// the dev account from earlier builds.
func TestLintCloudAccountIds_ConfigTemplateThatWillNotResolve(t *testing.T) {
	cases := []struct {
		name    string
		value   string
		configs func(string) (string, bool, bool)
		wantMsg string
	}{
		{
			name:    "config does not exist yet",
			value:   "{{ Configs.k8s_dev_account_id }}",
			configs: noConfigs,
			wantMsg: "no config named",
		},
		{
			name:    "config exists but holds the auto-created placeholder",
			value:   "{{ Configs.k8s_account_id }}",
			configs: configsHolding(map[string]string{"k8s_account_id": "<TODO: set value>"}),
			wantMsg: "is not an account UUID",
		},
		{
			name:    "config exists but holds a display name",
			value:   "{{ Configs.k8s_dev_account_id }}",
			configs: configsHolding(map[string]string{"k8s_dev_account_id": "k8s-dev"}),
			wantMsg: "is not an account UUID",
		},
		{
			name:    "config exists but is empty",
			value:   "{{Configs.acct}}",
			configs: configsHolding(map[string]string{"acct": ""}),
			wantMsg: "is not an account UUID",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var issues []string
			walkTasksAndLintAccountIds(
				[]interface{}{cliTask("fetch-pods", "k8s.cli", map[string]interface{}{"account_id": tc.value})},
				tc.configs, &issues)

			assert.Len(t, issues, 1)
			assert.Contains(t, issues[0], tc.wantMsg)
			assert.Contains(t, issues[0], tc.value, "echo the value back so the model can see what it wrote")
		})
	}
}

// A config indirection that resolves to a real UUID today is a working automation, not this bug.
// Flagging it would break every automation that legitimately parameterises its account.
func TestLintCloudAccountIds_LeavesWorkingDefinitionsAlone(t *testing.T) {
	cases := []struct {
		name    string
		tasks   []interface{}
		configs func(string) (string, bool, bool)
	}{
		{
			name:    "literal UUID",
			tasks:   []interface{}{cliTask("fetch-pods", "k8s.cli", map[string]interface{}{"account_id": testAccountUUID})},
			configs: noConfigs,
		},
		{
			name:    "UUID with stray whitespace",
			tasks:   []interface{}{cliTask("fetch-pods", "k8s.cli", map[string]interface{}{"account_id": "  " + testAccountUUID + "\n"})},
			configs: noConfigs,
		},
		{
			name:    "config template that resolves to a UUID",
			tasks:   []interface{}{cliTask("fetch-pods", "k8s.cli", map[string]interface{}{"account_id": "{{ Configs.k8s_dev_account_id }}"})},
			configs: configsHolding(map[string]string{"k8s_dev_account_id": testAccountUUID}),
		},
		{
			name:    "task type that does not take an account_id",
			tasks:   []interface{}{cliTask("notify", "notifications.im", map[string]interface{}{"channel": "#ops"})},
			configs: noConfigs,
		},
		{
			name:    "scripting task with no params at all",
			tasks:   []interface{}{cliTask("filter", "scripting.run_script", nil)},
			configs: noConfigs,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var issues []string
			walkTasksAndLintAccountIds(tc.tasks, tc.configs, &issues)
			assert.Empty(t, issues)
		})
	}
}

// "We could not read the configs" must never be reported as "that config does not exist" — the
// automation may be perfectly fine, and a false rejection sends the loop chasing a non-bug.
func TestLintCloudAccountIds_UnreadableConfigApiStaysSilentOnTemplates(t *testing.T) {
	var issues []string
	walkTasksAndLintAccountIds(
		[]interface{}{cliTask("fetch-pods", "k8s.cli", map[string]interface{}{"account_id": "{{ Configs.whatever }}"})},
		configsUnavailable, &issues)

	assert.Empty(t, issues, "an unreachable config API is not evidence of a broken account binding")

	// Nothing else is caught either — every verdict this lint reaches needs the config value.
	issues = nil
	walkTasksAndLintAccountIds(
		[]interface{}{cliTask("fetch-pods", "k8s.cli", map[string]interface{}{"account_id": "{{ Configs.other }}"})},
		configsUnavailable, &issues)
	assert.Empty(t, issues)
}

// Tasks nested inside core.foreach / core.group bodies run the same kubectl and need the same
// binding; the resolver walker already recurses, and this one must match it.
func TestLintCloudAccountIds_RecursesIntoNestedTasks(t *testing.T) {
	tasks := []interface{}{
		map[string]interface{}{
			"id":   "per-namespace",
			"type": "core.foreach",
			"tasks": []interface{}{
				cliTask("inner-good", "k8s.cli", map[string]interface{}{"account_id": testAccountUUID}),
				cliTask("inner-bad", "tickets.create", map[string]interface{}{"account_id": "{{ Configs.nope }}"}),
			},
		},
	}

	var issues []string
	walkTasksAndLintAccountIds(tasks, noConfigs, &issues)

	assert.Len(t, issues, 1)
	assert.Contains(t, issues[0], "inner-bad")
}

// The message is what the model acts on: it must name the task, both valid fixes, and the two
// shapes that are not fixes. Offering "remove it" matters — the parameter is optional and defaults
// to the automation's own account, so deleting it is frequently the RIGHT answer, and a message
// that forbade it would push the model into inventing an account.
func TestCloudAccountLintMessage_TellsTheModelWhatToDo(t *testing.T) {
	msg := cloudAccountLintMessage([]string{"task 'fetch-pods' (k8s.cli) sets `account_id` to {{ Configs.nope }}"})

	assert.Contains(t, msg, "Validation FAILED")
	assert.Contains(t, msg, "fetch-pods")
	assert.Contains(t, msg, "modify_task", "name the tool that fixes it")
	assert.Contains(t, msg, "ACCOUNT ENVIRONMENT", "name where the UUID comes from")
	assert.Contains(t, msg, "Remove `account_id` entirely",
		"removing it is a legitimate fix — it defaults to the automation's own account")
	assert.Contains(t, msg, "Do not use a display name and do not point it at a config")
}
