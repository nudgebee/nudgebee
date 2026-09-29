package toolapproval

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// commandRule builds an account rule; all-accounts rules are covered by
// their own tests.
func commandRule(kind MatchKind, pattern string) Rule {
	dir := "/workspace"
	account := uuid.New()
	return Rule{AccountID: &account, Name: "rule", Enabled: true, ToolName: "shell_execute", MatchKind: kind, CommandPattern: &pattern, WorkingDir: &dir, Conditions: json.RawMessage("[]")}
}

func TestExactCommandMatchesTokenEquivalentInvocation(t *testing.T) {
	rule := commandRule(MatchExactCommand, `kubectl rollout restart deployment/api -n staging`)
	matched, _ := MatchRule(rule, "SHELL_EXECUTE", `{"command":"kubectl rollout restart 'deployment/api' -n staging","work_dir":"/workspace"}`, "")
	require.True(t, matched)
}

func TestCommandRulesFailClosedForChangedTargetAndCompoundShell(t *testing.T) {
	rule := commandRule(MatchExactCommand, `kubectl rollout restart deployment/api -n staging`)
	for _, input := range []string{
		`{"command":"kubectl rollout restart deployment/api -n production","work_dir":"/workspace"}`,
		`{"command":"kubectl rollout restart deployment/api -n staging && kubectl delete pod api","work_dir":"/workspace"}`,
		`{"command":"env KUBECONFIG=/tmp/prod kubectl rollout restart deployment/api -n staging","work_dir":"/workspace"}`,
		`{"command":"sh -c 'kubectl rollout restart deployment/api -n staging'","work_dir":"/workspace"}`,
		`{"command":"kubectl rollout restart deployment/api -n staging > result.txt","work_dir":"/workspace"}`,
		`{"command":"kubectl rollout restart deployment/$TARGET -n staging","work_dir":"/workspace"}`,
		`{"command":"kubectl rollout restart deployment/* -n staging","work_dir":"/workspace"}`,
	} {
		matched, _ := MatchRule(rule, "shell_execute", input, "")
		require.False(t, matched, input)
	}
}

// Wrappers, interpreters and scripts are the admin's choice: a rule may name
// them, and then matches only that command.
func TestCommandRulesAllowWrappersAndInterpreters(t *testing.T) {
	for _, command := range []string{
		`/usr/bin/env kubectl delete pod api`,
		`KUBECONFIG=/tmp/prod kubectl delete pod api`,
		`./deploy.sh production`,
		`python3 /tmp/deploy.py production`,
		`sudo systemctl restart nginx`,
	} {
		rule := commandRule(MatchExactCommand, command)
		require.NoError(t, ValidateRule(rule), command)
		input, _ := json.Marshal(map[string]string{"command": command, "work_dir": "/workspace"})
		matched, reason := MatchRule(rule, "shell_execute", string(input), "")
		require.True(t, matched, "%s: %s", command, reason)
	}
}

func TestRuleMustMatchResolvedExecutionTarget(t *testing.T) {
	target := "config-staging"
	rule := commandRule(MatchExactCommand, `kubectl rollout restart deployment/api -n staging`)
	rule.ToolConfigID = &target
	input := `{"command":"kubectl rollout restart deployment/api -n staging","work_dir":"/workspace"}`

	matched, _ := MatchRule(rule, "shell_execute", input, "config-staging")
	require.True(t, matched)
	matched, _ = MatchRule(rule, "shell_execute", input, "config-production")
	require.False(t, matched)
	matched, _ = MatchRule(rule, "shell_execute", input, UnresolvedTargetID)
	require.False(t, matched)

	unbound := commandRule(MatchExactCommand, `kubectl rollout restart deployment/api -n staging`)
	matched, _ = MatchRule(unbound, "shell_execute", input, "config-staging")
	require.False(t, matched, "configured invocations must never match an unbound rule")
}

func TestExactCommandCanBindWorkingDirectory(t *testing.T) {
	dir := "/workspace/repo"
	rule := commandRule(MatchExactCommand, `git push origin main`)
	rule.WorkingDir = &dir
	matched, _ := MatchRule(rule, "shell_execute", `{"command":"git push origin main","work_dir":"/workspace/other"}`, "")
	require.False(t, matched)
	matched, _ = MatchRule(rule, "shell_execute", `{"command":"git push origin main","work_dir":"/workspace/repo"}`, "")
	require.True(t, matched)
}

func TestShellCommandRuleRequiresWorkingDirectory(t *testing.T) {
	pattern := `git push origin main`
	rule := Rule{Name: "git", Enabled: true, ToolName: "shell_execute", MatchKind: MatchExactCommand, CommandPattern: &pattern, Conditions: json.RawMessage("[]")}
	require.Error(t, ValidateRule(rule))
}

func TestArgumentConditionsPreserveTypesAndUseFullRegex(t *testing.T) {
	rule := Rule{
		Name: "ticket", Enabled: true, ToolName: "ticket_update", MatchKind: MatchArguments,
		Conditions: json.RawMessage(`[
          {"path":"ticket_id","operator":"equals","value":42},
          {"path":"status","operator":"regex","value":"resolved|closed"}
        ]`),
	}
	matched, _ := MatchRule(rule, "ticket_update", `{"ticket_id":42,"status":"resolved"}`, "")
	require.True(t, matched)
	matched, _ = MatchRule(rule, "ticket_update", `{"ticket_id":"42","status":"resolved"}`, "")
	require.False(t, matched)
	matched, _ = MatchRule(rule, "ticket_update", `{"ticket_id":42,"status":"resolved-now"}`, "")
	require.False(t, matched)
}

func TestInvalidRegexNeverMatches(t *testing.T) {
	pattern := `[invalid`
	rule := Rule{Name: "rule", Enabled: true, ToolName: "kubectl_execute", MatchKind: MatchCommandRegex, CommandPattern: &pattern, Conditions: json.RawMessage("[]")}
	require.Error(t, ValidateRule(rule))
	matched, _ := MatchRule(rule, "shell_execute", `{"command":"kubectl delete pod api"}`, "")
	require.False(t, matched)
}

func TestShellCommandRegexIsAllowed(t *testing.T) {
	rule := commandRule(MatchCommandRegex, `kubectl .*`)
	require.NoError(t, ValidateRule(rule))
}

func TestExactCommandRejectsBackslashInsideDoubleQuotes(t *testing.T) {
	rule := commandRule(MatchExactCommand, `cat "foo"`)
	require.NoError(t, ValidateRule(rule))
	// shlex reads both as [cat foo]; the shell opens the file `\foo`.
	matched, _ := MatchRule(rule, "shell_execute", `{"command":"cat \"\\foo\"","work_dir":"/workspace"}`, "")
	require.False(t, matched)
	require.Error(t, ValidateRule(commandRule(MatchExactCommand, `cat "\foo"`)))
}

func TestRegexCannotEscapeFullMatchAnchoring(t *testing.T) {
	pattern := `a)|(b`
	rule := Rule{Name: "rule", Enabled: true, ToolName: "kubectl_execute", MatchKind: MatchCommandRegex, CommandPattern: &pattern, Conditions: json.RawMessage("[]")}
	require.Error(t, ValidateRule(rule))
	matched, _ := MatchRule(rule, "kubectl_execute", `{"command":"a rm -rf /"}`, "")
	require.False(t, matched)

	arguments := Rule{Name: "rule", Enabled: true, ToolName: "ticket_update", MatchKind: MatchArguments,
		Conditions: json.RawMessage(`[{"path":"status","operator":"regex","value":"a)|(b"}]`)}
	require.Error(t, ValidateRule(arguments))
	matched, _ = MatchRule(arguments, "ticket_update", `{"status":"a-anything"}`, "")
	require.False(t, matched)
}

func TestAllAccountsRuleMatchesAnyResolvedTarget(t *testing.T) {
	pattern := `kubectl rollout restart deployment/api -n staging`
	rule := Rule{Name: "restart", Enabled: true, ToolName: "kubectl_execute", MatchKind: MatchExactCommand, CommandPattern: &pattern, Conditions: json.RawMessage("[]")}
	require.True(t, rule.AllAccounts())
	require.NoError(t, ValidateRule(rule))
	for _, target := range []string{"cluster-a", "cluster-b", ""} {
		matched, reason := MatchRule(rule, "kubectl_execute", `{"command":"kubectl rollout restart deployment/api -n staging"}`, target)
		require.True(t, matched, "target %q: %s", target, reason)
	}
	matched, _ := MatchRule(rule, "kubectl_execute", `{"command":"kubectl rollout restart deployment/api -n staging"}`, UnresolvedTargetID)
	require.False(t, matched, "an unresolved target never matches")
}

func TestAccountRuleWithoutTargetDoesNotMatchConfiguredCall(t *testing.T) {
	pattern := `kubectl rollout restart deployment/api -n staging`
	account := uuid.New()
	rule := Rule{AccountID: &account, Name: "restart", Enabled: true, ToolName: "kubectl_execute", MatchKind: MatchExactCommand, CommandPattern: &pattern, Conditions: json.RawMessage("[]")}
	matched, _ := MatchRule(rule, "kubectl_execute", `{"command":"kubectl rollout restart deployment/api -n staging"}`, "cluster-a")
	require.False(t, matched)
}

func TestOrganisationRulePinnedToIntegrationMatchesOnlyIt(t *testing.T) {
	integration := "integration-a"
	rule := Rule{Name: "tool", Enabled: true, ToolName: "postgres_execute", MatchKind: MatchTool, ToolConfigID: &integration, Conditions: json.RawMessage("[]")}
	require.True(t, rule.AllAccounts())
	require.NoError(t, ValidateRule(rule))
	matched, _ := MatchRule(rule, "postgres_execute", `{}`, "integration-a")
	require.True(t, matched)
	matched, _ = MatchRule(rule, "postgres_execute", `{}`, "integration-b")
	require.False(t, matched, "another integration of the same type does not match")
	matched, _ = MatchRule(rule, "postgres_execute", `{}`, "")
	require.False(t, matched, "a call with no integration does not match a pinned rule")
}

// Operators split a chain with or without surrounding spaces, so a second
// command can never hide inside the first one's last word.
func TestCommandRulesSplitUnspacedOperators(t *testing.T) {
	rule := commandRule(MatchExactCommand, `kubectl rollout restart deployment/api -n staging`)
	for _, command := range []string{
		`kubectl rollout restart deployment/api -n staging;kubectl delete pod api`,
		`kubectl rollout restart deployment/api -n staging&&kubectl delete pod api`,
		`kubectl rollout restart deployment/api -n staging||kubectl delete pod api`,
		`kubectl rollout restart deployment/api -n staging|sh`,
		`kubectl rollout restart deployment/api -n staging&kubectl delete pod api`,
	} {
		parts, err := splitCommands(command)
		require.NoError(t, err, command)
		require.Len(t, parts, 2, command)
		input, _ := json.Marshal(map[string]string{"command": command, "work_dir": "/workspace"})
		matched, _ := MatchRule(rule, "shell_execute", string(input), "")
		require.False(t, matched, command)
	}
}

func TestSplitCommands(t *testing.T) {
	for command, want := range map[string][]string{
		`kubectl get pods`:                          {`kubectl get pods`},
		`a && b || c; d | e & f`:                    {`a`, `b`, `c`, `d`, `e`, `f`},
		"a\nb":                                      {`a`, `b`},
		`kubectl logs api 2>&1 | grep error`:        {`kubectl logs api 2>&1`, `grep error`},
		`kubectl get pods &> out.txt`:               {`kubectl get pods &> out.txt`},
		`kubectl get pods >| out.txt`:               {`kubectl get pods >| out.txt`},
		`kubectl annotate pod api note="a; b && c"`: {`kubectl annotate pod api note="a; b && c"`},
		`echo 'a | b' \; done`:                      {`echo 'a | b' \; done`},
		`a;`:                                        {`a`},
	} {
		got, err := splitCommands(command)
		require.NoError(t, err, command)
		require.Equal(t, want, got, command)
	}
	for _, command := range []string{
		`kubectl delete pod $(cat target)`,
		"kubectl delete pod `cat target`",
		`kubectl apply -f <(cat manifest)`,
		`(kubectl delete pod api)`,
		`kubectl annotate pod api note="$(id)"`,
		`echo "unterminated`,
		`kubectl get $'\'' ; echo x # '`,
		`kubectl get $"x"`,
		` ; `,
	} {
		_, err := splitCommands(command)
		require.Error(t, err, command)
	}
	// An escaped > or < is a literal character, so a following & or | splits.
	for _, command := range []string{`kubectl get pods \>& echo x`, `kubectl get pods \>| echo x`, `kubectl get pods \<& echo x`} {
		parts, err := splitCommands(command)
		require.NoError(t, err, command)
		require.Len(t, parts, 2, command)
	}
	_, err := splitCommands(`kubectl annotate pod api note='$(id)'`)
	require.NoError(t, err, "single quotes keep $( literal")
}

// An argument rule checks only the fields it names; any other field,
// including a command, is approved with the value sent (the editor says so).
func TestArgumentRulesCheckOnlyNamedFields(t *testing.T) {
	rule := Rule{Name: "db", Enabled: true, ToolName: "db_update", MatchKind: MatchArguments,
		Conditions: json.RawMessage(`[{"path":"database","operator":"equals","value":"staging"}]`)}
	require.NoError(t, ValidateRule(rule))
	matched, _ := MatchRule(rule, "db_update", `{"database":"staging","sql":"UPDATE jobs SET state = 'queued'"}`, "")
	require.True(t, matched)
	matched, _ = MatchRule(rule, "db_update", `{"database":"prod","sql":"UPDATE jobs SET state = 'queued'"}`, "")
	require.False(t, matched)
}

// A condition on a command field parses the command first, as the command
// rules do, so a chain or substitution never matches.
func TestArgumentConditionOnCommandFieldParsesCommand(t *testing.T) {
	rule := Rule{Name: "get", Enabled: true, ToolName: "shell_execute", MatchKind: MatchArguments,
		Conditions: json.RawMessage(`[{"path":"command","operator":"regex","value":"kubectl get .*"}]`)}
	require.NoError(t, ValidateRule(rule))
	matched, _ := MatchRule(rule, "shell_execute", `{"command":"kubectl get pods -n app"}`, "")
	require.True(t, matched)
	for _, command := range []string{
		`kubectl get pods && kubectl delete pod api`,
		`kubectl get pods; kubectl delete ns prod`,
		`kubectl get pods $(id)`,
	} {
		input, _ := json.Marshal(map[string]string{"command": command})
		matched, _ = MatchRule(rule, "shell_execute", string(input), "")
		require.False(t, matched, command)
	}
	matched, _ = MatchRule(rule, "shell_execute", `{"command":"kubectl get pods > /tmp/out"}`, "")
	require.True(t, matched, "a redirect is part of the command")
}

// A list of CLI commands (recommendation_execute_cli's `commands`) matches only
// when every command is a single safe command that matches the condition.
func TestArgumentConditionOnCommandListChecksEveryCommand(t *testing.T) {
	rule := Rule{Name: "describe", Enabled: true, ToolName: "recommendation_execute_cli", MatchKind: MatchArguments,
		Conditions: json.RawMessage(`[{"path":"commands","operator":"regex","value":"aws ec2 describe-.*"}]`)}
	require.NoError(t, ValidateRule(rule))
	matched, _ := MatchRule(rule, "recommendation_execute_cli", `{"commands":["aws ec2 describe-instances","aws ec2 describe-vpcs"]}`, "")
	require.True(t, matched)
	for _, input := range []string{
		`{"commands":["aws ec2 describe-instances","aws ec2 terminate-instances --instance-ids i-1"]}`,
		`{"commands":["aws ec2 describe-instances; aws ec2 terminate-instances --instance-ids i-1"]}`,
		`{"commands":["aws ec2 describe-instances",42]}`,
		`{"commands":[]}`,
	} {
		matched, _ = MatchRule(rule, "recommendation_execute_cli", input, "")
		require.False(t, matched, input)
	}
}

// Conditions are read back from jsonb, which reorders object keys and keeps a
// number as written, so equals compares values rather than bytes.
func TestArgumentEqualsComparesValuesAfterJSONBRoundTrip(t *testing.T) {
	rule := Rule{Name: "labels", Enabled: true, ToolName: "deploy", MatchKind: MatchArguments,
		Conditions: json.RawMessage(`[{"path": "labels", "operator": "equals", "value": {"ns": "a", "app": "b"}}, {"path": "ratio", "operator": "equals", "value": 1.50}, {"path": "hosts", "operator": "equals", "value": ["x", "y"]}]`)}
	matched, reason := MatchRule(rule, "deploy", `{"labels":{"app":"b","ns":"a"},"ratio":1.5,"hosts":["x","y"]}`, "")
	require.True(t, matched, reason)
	for _, input := range []string{
		`{"labels":{"app":"b","ns":"c"},"ratio":1.5,"hosts":["x","y"]}`,
		`{"labels":{"app":"b","ns":"a"},"ratio":"1.5","hosts":["x","y"]}`,
		`{"labels":{"app":"b","ns":"a"},"ratio":1.5,"hosts":["y","x"]}`,
	} {
		matched, _ = MatchRule(rule, "deploy", input, "")
		require.False(t, matched, input)
	}
}

// A chain is approved only when every command in it matches the rule.
func TestChainMatchesOnlyWhenEveryCommandMatches(t *testing.T) {
	pattern := `kubectl rollout restart .*`
	regex := Rule{Name: "restart", Enabled: true, ToolName: "kubectl_execute", MatchKind: MatchCommandRegex, CommandPattern: &pattern, Conditions: json.RawMessage("[]")}
	exactPattern := `kubectl rollout restart deployment/api && kubectl rollout status deployment/api`
	exact := Rule{Name: "restart", Enabled: true, ToolName: "kubectl_execute", MatchKind: MatchExactCommand, CommandPattern: &exactPattern, Conditions: json.RawMessage("[]")}
	check := func(rule Rule, command string, want bool) {
		input, _ := json.Marshal(map[string]string{"command": command})
		matched, reason := MatchRule(rule, "kubectl_execute", string(input), "")
		require.Equal(t, want, matched, "%s: %s", command, reason)
	}
	check(regex, `kubectl rollout restart deployment/api && kubectl rollout restart deployment/web`, true)
	check(regex, `kubectl rollout restart deployment/api; kubectl delete ns prod`, false)
	check(regex, `kubectl rollout restart deployment/api | sh`, false)
	check(regex, `kubectl rollout restart deployment/$(cat target)`, false)
	check(regex, `kubectl exec pod -- sh -c "echo; rollout restart"`, false)
	check(exact, `kubectl rollout restart deployment/api && kubectl rollout status deployment/api`, true)
	check(exact, `kubectl rollout status deployment/api`, true)
	check(exact, `kubectl rollout restart deployment/api && kubectl delete deployment api`, false)
}
