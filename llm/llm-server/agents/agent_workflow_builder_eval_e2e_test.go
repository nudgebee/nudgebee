//go:build e2e

package agents

// Automation-builder eval. The scenarios come, anonymized, from real sessions in
// which the builder produced automations that could not run. Each one scores
// the saved definition on the properties that decided whether those automations
// worked:
//   - built-in actions (k8s.cli, aws.cli, …) instead of scripts that wrap the CLIs
//   - no login code in scripts: the step already runs as its account
//   - every script step's account can run on its executor
//   - a dry run when the user asks to test
//   - a question, not a workaround, when the needed account is not connected
//
// Results are also written as JSON to $EVAL_OUT so a baseline run and a branch
// run can be compared. Fixtures that must exist in a "broken" state are created
// through $EVAL_FIXTURE_WORKFLOW_SERVER_URL (defaults to WORKFLOW_SERVER_URL), so
// they can be seeded through a server that does not refuse the broken pairing.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"nudgebee/llm/agents/core"
	"nudgebee/llm/common"
	"nudgebee/llm/security"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"
	"os"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

var (
	// Login code a script step does not need: the platform runs it as its account.
	evalLoginCode = regexp.MustCompile(`(?i)sts\s+assume-role|arn:aws:iam::\d{12}:role/|update-kubeconfig|\baz\s+login\b|gcloud\s+auth\s+activate|unset\s+KUBERNETES_SERVICE_HOST|AWS_(ACCESS_KEY_ID|SECRET_ACCESS_KEY)`)
	// A script calling a CLI that has a built-in action.
	evalCLIInScript = regexp.MustCompile(`(?m)(^|[;&|(\s])(kubectl|aws|az|gcloud)\s+[a-z]`)
	// A reply that asks for, or warns about, a missing connection.
	evalAsksForAccess  = regexp.MustCompile(`(?i)not (currently )?(connected|configured|available|found)|isn'?t (connected|configured)|connect (the|an|your)|add (the|an|your) .*(account|subscription)|no (azure|aws|gcp)? ?(account|subscription)|which (configured )?(azure |aws |gcp )?(cloud )?(account|subscription)`)
	evalAutomationLink = regexp.MustCompile(`/automation/([0-9a-f-]{36})`)
	evalPlanShown      = regexp.MustCompile(`(?i)automation plan|here'?s my (updated )?plan`)
)

type evalAccount struct{ id, name, provider string }

type evalEnv struct {
	tenantId, userId string
	cluster          evalAccount // the automation's own account (TEST_ACCOUNT)
	aws, azure, gcp  evalAccount
	providers        map[string]string // account id → provider, for scoring
}

type evalResult struct {
	Scenario   string          `json:"scenario"`
	Pass       bool            `json:"pass"`
	Checks     map[string]bool `json:"checks"`
	Notes      []string        `json:"notes,omitempty"`
	TaskTypes  []string        `json:"task_types,omitempty"`
	ToolCalls  []string        `json:"tool_calls,omitempty"`
	Asked      bool            `json:"asked"`
	WorkflowId string          `json:"workflow_id,omitempty"`
	Seconds    float64         `json:"seconds"`
}

var (
	evalResultsMu sync.Mutex
	evalResults   []evalResult
)

func loadEvalEnv(t *testing.T) evalEnv {
	t.Helper()
	accountId := os.Getenv("TEST_ACCOUNT")
	if accountId == "" {
		t.Skip("Skipping eval: TEST_ACCOUNT not set")
	}
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		t.Skipf("Skipping eval: db unavailable: %v", err)
	}
	env := evalEnv{tenantId: os.Getenv("TEST_TENANT"), userId: os.Getenv("TEST_USER"), providers: map[string]string{}}
	rows, err := dbms.Db.Query(`SELECT id::text, account_name, cloud_provider, assume_role IS NOT NULL
		FROM cloud_accounts WHERE tenant = $1 AND status = 'active' ORDER BY created_at`, env.tenantId)
	if err != nil {
		t.Skipf("Skipping eval: cannot list accounts: %v", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var a evalAccount
		var roleBased bool
		if err := rows.Scan(&a.id, &a.name, &a.provider, &roleBased); err != nil {
			t.Fatalf("scan account: %v", err)
		}
		env.providers[a.id] = a.provider
		switch {
		case a.id == accountId:
			env.cluster = a
		case a.provider == "AWS" && roleBased && env.aws.id == "":
			env.aws = a
		case a.provider == "Azure" && env.azure.id == "":
			env.azure = a
		case a.provider == "GCP" && env.gcp.id == "":
			env.gcp = a
		}
	}
	if env.cluster.provider != "K8s" {
		t.Skipf("Skipping eval: TEST_ACCOUNT must be a Kubernetes cluster account, got %q", env.cluster.provider)
	}
	if env.aws.id == "" || env.azure.id == "" || env.gcp.id == "" {
		t.Skip("Skipping eval: the tenant needs a role-based AWS, an Azure and a GCP account")
	}
	return env
}

// evalPairOK mirrors CheckAccountSupports in runbook-server's executors
// package: which account providers each executor can run a script for.
func evalPairOK(executor, provider string) bool {
	wanted := map[string][]string{
		"kubernetes":        {"aws", "azure", "gcp"},
		"aws_ssm":           {"aws"},
		"azure_run_command": {"azure"},
		"gcp_compute_ssh":   {"gcp"},
	}[strings.ToLower(executor)]
	p := strings.ToLower(provider)
	if p == "k8s" || p == "kubernetes" {
		p = "k8s"
	}
	return wanted == nil || p == "" || slices.Contains(wanted, p)
}

type evalTask struct {
	id, typ string
	params  map[string]any
}

func evalTasks(def map[string]any) []evalTask {
	var out []evalTask
	var walk func(list []any)
	walk = func(list []any) {
		for _, raw := range list {
			m, _ := raw.(map[string]any)
			if m == nil {
				continue
			}
			params, _ := m["params"].(map[string]any)
			id, _ := m["id"].(string)
			typ, _ := m["type"].(string)
			out = append(out, evalTask{id: id, typ: typ, params: params})
			if nested, ok := m["tasks"].([]any); ok {
				walk(nested)
			}
			if params != nil {
				if nested, ok := params["tasks"].([]any); ok {
					walk(nested)
				}
			}
		}
	}
	tasks, _ := def["tasks"].([]any)
	walk(tasks)
	return out
}

// accountOf is the provider of the account a task runs against, "" if templated.
func (e evalEnv) accountOf(task evalTask) (string, string) {
	id, _ := task.params["account_id"].(string)
	if id == "" {
		id = e.cluster.id
	}
	if strings.Contains(id, "{{") {
		return id, ""
	}
	return id, e.providers[id]
}

func scriptText(task evalTask) string {
	var b strings.Builder
	s, _ := task.params["script"].(string)
	b.WriteString(s)
	if envs, ok := task.params["env"].(map[string]any); ok {
		for k, v := range envs {
			fmt.Fprintf(&b, "\n%s=%v", k, v)
		}
	}
	return b.String()
}

// scoreCommon fills the checks every scenario shares.
func (e evalEnv) scoreCommon(def map[string]any, r *evalResult) {
	login, pairs, cliScripts := true, true, true
	for _, task := range evalTasks(def) {
		r.TaskTypes = append(r.TaskTypes, task.typ)
		if task.typ != "scripting.run_script" {
			continue
		}
		text := scriptText(task)
		if evalLoginCode.MatchString(text) {
			login = false
			r.Notes = append(r.Notes, fmt.Sprintf("%s: login code %q", task.id, evalLoginCode.FindString(text)))
		}
		executor, _ := task.params["executor_type"].(string)
		_, provider := e.accountOf(task)
		if !evalPairOK(executor, provider) {
			pairs = false
			r.Notes = append(r.Notes, fmt.Sprintf("%s: %s account on %s executor", task.id, provider, executor))
		}
		onVM := slices.Contains([]string{"aws_ssm", "azure_run_command", "gcp_compute_ssh", "ssh"}, executor)
		if !onVM && evalCLIInScript.MatchString(text) {
			cliScripts = false
			r.Notes = append(r.Notes, fmt.Sprintf("%s: script wraps %q", task.id, strings.TrimSpace(evalCLIInScript.FindString(text))))
		}
	}
	r.Checks["no_login_code"] = login
	r.Checks["valid_account_executor"] = pairs
	r.Checks["no_cli_in_scripts"] = cliScripts
}

func hasTaskType(def map[string]any, types ...string) bool {
	for _, task := range evalTasks(def) {
		if slices.Contains(types, task.typ) {
			return true
		}
	}
	return false
}

// latestDefinition reads a workflow's current definition — the draft in
// workflows.definition. An edit saved through the API only changes the draft
// (no new version row), so reading the newest version would score the
// definition as it was before the builder touched it.
func latestDefinition(t *testing.T, workflowId string) map[string]any {
	t.Helper()
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	var raw []byte
	if err := dbms.Db.QueryRow(`SELECT definition FROM workflows WHERE id = $1`, workflowId).Scan(&raw); err != nil {
		t.Fatalf("read workflow %s: %v", workflowId, err)
	}
	def := map[string]any{}
	if err := json.Unmarshal(raw, &def); err != nil {
		t.Fatalf("decode workflow %s: %v", workflowId, err)
	}
	return def
}

func sessionToolCalls(t *testing.T, sessionId, accountId string) []string {
	t.Helper()
	// Tool calls are written asynchronously; give the writer a moment.
	var names []string
	for attempt := 0; attempt < 4; attempt++ {
		if names = querySessionToolCalls(sessionId, accountId); len(names) > 0 {
			return names
		}
		time.Sleep(3 * time.Second)
	}
	return names
}

func querySessionToolCalls(sessionId, accountId string) []string {
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return nil
	}
	rows, err := dbms.Db.Query(`SELECT tc.tool_name FROM llm_conversation_tool_calls tc
		JOIN llm_conversations c ON c.id = tc.conversation_id
		WHERE c.session_id = $1 AND c.account_id = $2 ORDER BY tc.created_at`, sessionId, accountId)
	if err != nil {
		return nil
	}
	defer func() { _ = rows.Close() }()
	var names []string
	for rows.Next() {
		var n string
		if rows.Scan(&n) == nil {
			names = append(names, n)
		}
	}
	return names
}

// fixtureRequest seeds or removes a fixture workflow, optionally through a
// different workflow-server than the one under test.
func (e evalEnv) fixtureRequest(t *testing.T, method, path string, body any) []byte {
	t.Helper()
	base := os.Getenv("EVAL_FIXTURE_WORKFLOW_SERVER_URL")
	if base == "" {
		out, err := tools.DoRunbookRequest(method, path, body, e.cluster.id, e.tenantId, e.userId)
		if err != nil {
			t.Fatalf("fixture %s %s: %v", method, path, err)
		}
		return out
	}
	var reader io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		reader = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, strings.TrimSuffix(base, "/")+"/"+path, reader)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Account-ID", e.cluster.id)
	req.Header.Set("X-Tenant-ID", e.tenantId)
	req.Header.Set("X-User-ID", e.userId)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("fixture %s %s: %v", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	out, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		t.Fatalf("fixture %s %s: %d %s", method, path, resp.StatusCode, out)
	}
	return out
}

func (e evalEnv) seedWorkflow(t *testing.T, name string, tasks []map[string]any) string {
	t.Helper()
	wf := map[string]any{
		"name": name,
		"definition": map[string]any{
			"version":  "v1",
			"triggers": []any{map[string]any{"type": "manual", "params": map[string]any{}}},
			"inputs":   []any{map[string]any{"id": "namespace", "type": "string", "default": "default", "description": "Namespace to inspect"}},
			"tasks":    tasks,
		},
	}
	out := e.fixtureRequest(t, "POST", "workflows", wf)
	created := map[string]any{}
	_ = json.Unmarshal(out, &created)
	id, _ := created["id"].(string)
	if id == "" {
		t.Fatalf("fixture create returned no id: %s", out)
	}
	if os.Getenv("EVAL_KEEP") == "" {
		t.Cleanup(func() { e.fixtureRequest(t, "DELETE", "workflows/"+id, nil) })
	} else {
		t.Logf("EVAL_KEEP: fixture %s kept", id)
	}
	return id
}

// brokenClusterScript is the shape that failed in production: a script step on
// a Kubernetes cluster account, pinned to the executor that needs cloud
// credentials.
func (e evalEnv) brokenClusterScript() []map[string]any {
	return []map[string]any{{
		"id":   "list-pods",
		"type": "scripting.run_script",
		"params": map[string]any{
			"account_id":    e.cluster.id,
			"executor_type": "kubernetes",
			"language":      "bash",
			"script":        "kubectl get pods -n \"$NAMESPACE\" -o wide",
			"env":           map[string]any{"NAMESPACE": "{{ Inputs.namespace }}"},
		},
	}}
}

type evalRun struct {
	t         *testing.T
	env       evalEnv
	agent     *WorkflowBuilderAgent
	sc        *security.RequestContext
	sessionId string
	config    *toolcore.NBQueryConfig
	watermark string
	turns     []string // status and start of each reply, for the result notes
}

func (e evalEnv) newRun(t *testing.T, name string, workflowId string) *evalRun {
	r := &evalRun{
		t:         t,
		env:       e,
		agent:     newWorkflowBuilderAgent(e.cluster.id),
		sc:        security.NewRequestContextForTenantAccountAdmin(e.tenantId, e.userId, []string{e.cluster.id}),
		sessionId: "eval-wb-" + name + "-" + uuid.NewString()[:8],
	}
	if workflowId != "" {
		r.config = &toolcore.NBQueryConfig{WorkflowId: workflowId}
	}
	if os.Getenv("EVAL_KEEP") == "" {
		t.Cleanup(func() { _ = core.DeleteConversationBySession(r.sessionId, e.cluster.id, e.userId) })
	}
	r.watermark = workflowWatermark(t, e.cluster.id)
	return r
}

// workflowWatermark is the newest workflow creation time in the account, so a
// workflow the builder saved without linking it can still be found.
func workflowWatermark(t *testing.T, accountId string) string {
	t.Helper()
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return ""
	}
	var ts string
	_ = dbms.Db.QueryRow(`SELECT COALESCE(max(created_at), '1970-01-01')::text FROM workflows WHERE account_id = $1`, accountId).Scan(&ts)
	return ts
}

// say sends one user message as a new turn, as a user typing in the chat would.
func (r *evalRun) say(query string) core.NBAgentResponse {
	opts := []core.ConversationSessionRequestConfig{core.ConversationSessionRequestWithEnableQueryRefinement(false)}
	if r.config != nil {
		opts = append(opts, core.ConversationSessionRequestWithConfig(*r.config))
	}
	resp, err := core.HandleConversationSessionRequest(r.sc, r.agent, r.env.userId, r.env.cluster.id, r.sessionId, query, opts...)
	r.note(resp, err)
	return resp
}

func (r *evalRun) note(resp core.NBAgentResponse, err error) {
	text := displayText(resp)
	entry := fmt.Sprintf("[%s] %s", resp.Status, text[:min(400, len(text))])
	if err != nil {
		entry += " | error: " + err.Error()
	}
	r.turns = append(r.turns, entry)
}

// build drives a build to completion: approves the plan whenever it is shown
// and answers clarifying questions with a manual trigger and "use your
// judgement". Returns whether the builder asked anything besides the plan.
func (r *evalRun) build(query string) (core.NBAgentResponse, bool) {
	resp := r.say(query)
	asked := false
	for i := 0; i < 6 && resp.Status == core.ConversationStatusWaiting; i++ {
		if evalPlanShown.MatchString(displayText(resp)) {
			resp = handlePlanApprovalIfNeeded(r.t, r.sc, r.agent, r.env.userId, r.env.cluster.id, r.sessionId, resp)
			r.note(resp, nil)
			continue
		}
		asked = true
		messageId, _ := uuid.Parse(resp.MessageId)
		agentId, _ := uuid.Parse(resp.AgentId)
		next, err := core.HandleConversationSessionRequest(r.sc, r.agent, r.env.userId, r.env.cluster.id, r.sessionId,
			"Use a manual trigger. For anything else, use your best judgement with the accounts that are connected and proceed.",
			core.ConversationSessionRequestWithMessageId(uuid.NullUUID{UUID: messageId, Valid: true}),
			core.ConversationSessionRequestWithAgentId(uuid.NullUUID{UUID: agentId, Valid: true}))
		r.note(next, err)
		resp = next
	}
	return resp, asked
}

func (r *evalRun) savedWorkflowId(resp core.NBAgentResponse) string {
	if m := evalAutomationLink.FindStringSubmatch(displayText(resp)); m != nil {
		return m[1]
	}
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil || r.watermark == "" {
		return ""
	}
	var id string
	_ = dbms.Db.QueryRow(`SELECT id::text FROM workflows WHERE account_id = $1 AND created_at > $2::timestamp
		ORDER BY created_at DESC LIMIT 1`, r.env.cluster.id, r.watermark).Scan(&id)
	return id
}

func (r *evalRun) cleanupSaved(workflowId string) {
	if workflowId == "" || os.Getenv("EVAL_KEEP") != "" {
		return
	}
	r.t.Cleanup(func() {
		_, _ = tools.DoRunbookRequest("DELETE", "workflows/"+workflowId, nil, r.env.cluster.id, r.env.tenantId, r.env.userId)
	})
}

func record(t *testing.T, run *evalRun, r evalResult, started time.Time) {
	r.Seconds = time.Since(started).Seconds()
	if run != nil {
		r.Notes = append(r.Notes, run.turns...)
	}
	r.Pass = len(r.Checks) > 0
	for _, ok := range r.Checks {
		r.Pass = r.Pass && ok
	}
	evalResultsMu.Lock()
	evalResults = append(evalResults, r)
	evalResultsMu.Unlock()
	b, _ := json.Marshal(r)
	t.Logf("EVAL %s", b)
	for name, ok := range r.Checks {
		assert.True(t, ok, "%s: check %s failed (%v)", r.Scenario, name, r.Notes)
	}
}

func TestWorkflowBuilderEval(t *testing.T) {
	env := loadEvalEnv(t)
	t.Cleanup(func() {
		out := os.Getenv("EVAL_OUT")
		if out == "" {
			return
		}
		b, _ := json.MarshalIndent(evalResults, "", "  ")
		if err := os.WriteFile(out, b, 0o644); err != nil {
			t.Logf("write %s: %v", out, err)
		}
	})

	t.Run("diagnose_cluster_app_and_cloud_db", func(t *testing.T) {
		started := time.Now()
		r := env.newRun(t, "diag", "")
		resp, _ := r.build(fmt.Sprintf("Build an automation that diagnoses a slow PostgreSQL-backed app. "+
			"In the Kubernetes cluster %q check the app's pods, recent events and logs in the namespace given as an input. "+
			"The database is an RDS instance in the AWS account %q: check its status and its CloudWatch CPU and connection metrics. "+
			"Finish with a short summary.", env.cluster.name, env.aws.name))
		res := evalResult{Scenario: "diagnose_cluster_app_and_cloud_db", Checks: map[string]bool{}}
		res.WorkflowId = r.savedWorkflowId(resp)
		r.cleanupSaved(res.WorkflowId)
		res.Checks["saved"] = res.WorkflowId != ""
		if res.WorkflowId != "" {
			def := latestDefinition(t, res.WorkflowId)
			env.scoreCommon(def, &res)
			res.Checks["uses_k8s_cli"] = hasTaskType(def, "k8s.cli", "cloud.k8s.cli")
			res.Checks["uses_aws_cli"] = hasTaskType(def, "aws.cli", "cloud.aws.cli")
			awsOnAws := true
			for _, task := range evalTasks(def) {
				if task.typ == "aws.cli" || task.typ == "cloud.aws.cli" {
					if _, p := env.accountOf(task); p != "AWS" {
						awsOnAws = false
						res.Notes = append(res.Notes, fmt.Sprintf("%s: AWS CLI on a %s account", task.id, p))
					}
				}
			}
			res.Checks["aws_steps_on_aws_account"] = awsOnAws
		}
		res.ToolCalls = sessionToolCalls(t, r.sessionId, env.cluster.id)
		record(t, r, res, started)
	})

	t.Run("restart_service_on_vms_and_cluster", func(t *testing.T) {
		started := time.Now()
		r := env.newRun(t, "vmrestart", "")
		resp, _ := r.build(fmt.Sprintf("Build an automation that restarts a service. Inputs: cloud (aws, azure, gcp or kubernetes), "+
			"target (VM instance id, or deployment name for kubernetes), region or zone, namespace, and service name. "+
			"For aws use the AWS account %q, for azure the Azure account %q, for gcp the GCP account %q: restart the systemd service on the VM. "+
			"For kubernetes restart the deployment in the cluster %q. Report whether the restart succeeded.",
			env.aws.name, env.azure.name, env.gcp.name, env.cluster.name))
		res := evalResult{Scenario: "restart_service_on_vms_and_cluster", Checks: map[string]bool{}}
		res.WorkflowId = r.savedWorkflowId(resp)
		r.cleanupSaved(res.WorkflowId)
		res.Checks["saved"] = res.WorkflowId != ""
		if res.WorkflowId != "" {
			def := latestDefinition(t, res.WorkflowId)
			env.scoreCommon(def, &res)
			runCommand := false
			for _, task := range evalTasks(def) {
				if executor, _ := task.params["executor_type"].(string); slices.Contains([]string{"aws_ssm", "azure_run_command", "gcp_compute_ssh"}, executor) {
					runCommand = true
				}
			}
			res.Checks["vm_restart_waits_for_result"] = runCommand
			res.Checks["cluster_restart_native"] = hasTaskType(def, "k8s.workload_restart", "k8s.cli", "cloud.k8s.cli")
		}
		res.ToolCalls = sessionToolCalls(t, r.sessionId, env.cluster.id)
		record(t, r, res, started)
	})

	t.Run("terraform_from_repo", func(t *testing.T) {
		started := time.Now()
		r := env.newRun(t, "terraform", "")
		resp, _ := r.build(fmt.Sprintf("Build an automation that clones a GitHub repository given as an input, runs terraform init and "+
			"terraform apply in its ec2/ directory to create an EC2 instance in the AWS account %q, then confirms the instance is running.", env.aws.name))
		res := evalResult{Scenario: "terraform_from_repo", Checks: map[string]bool{}}
		res.WorkflowId = r.savedWorkflowId(resp)
		r.cleanupSaved(res.WorkflowId)
		res.Checks["saved"] = res.WorkflowId != ""
		if res.WorkflowId != "" {
			def := latestDefinition(t, res.WorkflowId)
			env.scoreCommon(def, &res)
			// Terraform is a legitimate script; only its account and identity are scored.
			delete(res.Checks, "no_cli_in_scripts")
			tfOnAws := false
			for _, task := range evalTasks(def) {
				if task.typ == "scripting.run_script" && strings.Contains(scriptText(task), "terraform") {
					if _, p := env.accountOf(task); p == "AWS" {
						tfOnAws = true
					}
				}
			}
			res.Checks["terraform_step_on_aws_account"] = tfOnAws
		}
		res.ToolCalls = sessionToolCalls(t, r.sessionId, env.cluster.id)
		record(t, r, res, started)
	})

	t.Run("fix_cluster_account_on_cloud_executor", func(t *testing.T) {
		started := time.Now()
		id := env.seedWorkflow(t, "eval-broken-cluster-script-"+uuid.NewString()[:6], env.brokenClusterScript())
		r := env.newRun(t, "fixexec", id)
		r.say("This automation fails with: this account could not supply credentials for a script running on Nudgebee: " +
			"cloud: could not get credentials for this account: account number not found. Fix it.")
		res := evalResult{Scenario: "fix_cluster_account_on_cloud_executor", WorkflowId: id, Checks: map[string]bool{}}
		def := latestDefinition(t, id)
		env.scoreCommon(def, &res)
		delete(res.Checks, "no_cli_in_scripts") // kubectl in a script on the agent is an acceptable fix
		res.Checks["keeps_cluster_account"] = evalKeepsCluster(env, def, &res)
		res.ToolCalls = sessionToolCalls(t, r.sessionId, env.cluster.id)
		record(t, r, res, started)
	})

	t.Run("test_with_inputs_dry_runs", func(t *testing.T) {
		started := time.Now()
		id := env.seedWorkflow(t, "eval-list-pods-"+uuid.NewString()[:6], []map[string]any{{
			"id": "list-pods", "type": "k8s.cli",
			"params": map[string]any{"command": "kubectl get pods -n {{ Inputs.namespace }}"},
		}})
		r := env.newRun(t, "dryrun", id)
		resp := r.say("Test this automation with namespace=kube-system and tell me what it returns.")
		res := evalResult{Scenario: "test_with_inputs_dry_runs", WorkflowId: id, Checks: map[string]bool{}}
		res.ToolCalls = sessionToolCalls(t, r.sessionId, env.cluster.id)
		res.Checks["dry_run_called"] = slices.Contains(res.ToolCalls, "dry_run")
		if !res.Checks["dry_run_called"] {
			text := displayText(resp)
			res.Notes = append(res.Notes, "reply: "+text[:min(300, len(text))])
		}
		record(t, r, res, started)
	})

	t.Run("yes_applies_the_proposal", func(t *testing.T) {
		started := time.Now()
		id := env.seedWorkflow(t, "eval-broken-cluster-script-"+uuid.NewString()[:6], env.brokenClusterScript())
		r := env.newRun(t, "yes", id)
		r.say("Runs fail with 'account number not found'. What is wrong and how would you fix it? Don't change anything yet.")
		r.say("yes")
		res := evalResult{Scenario: "yes_applies_the_proposal", WorkflowId: id, Checks: map[string]bool{}}
		def := latestDefinition(t, id)
		env.scoreCommon(def, &res)
		delete(res.Checks, "no_cli_in_scripts")
		res.Checks["keeps_cluster_account"] = evalKeepsCluster(env, def, &res)
		res.ToolCalls = sessionToolCalls(t, r.sessionId, env.cluster.id)
		record(t, r, res, started)
	})

	t.Run("asks_when_account_missing", func(t *testing.T) {
		started := time.Now()
		r := env.newRun(t, "missing", "")
		// Drive the whole conversation: the builder may ask about the trigger
		// first. Passing means that somewhere it asks for, or warns about, the
		// subscription that is not connected — instead of quietly binding the
		// Azure steps to some other account.
		resp, _ := r.build("Build an automation that lists the VMs in the Azure subscription 'contoso-finance-prod' and starts the ones that are stopped.")
		res := evalResult{Scenario: "asks_when_account_missing", Checks: map[string]bool{}}
		res.Asked = evalAsksForAccess.MatchString(strings.Join(r.turns, "\n"))
		res.Checks["asks_or_warns"] = res.Asked
		if id := r.savedWorkflowId(resp); id != "" {
			r.cleanupSaved(id)
			res.WorkflowId = id
			def := latestDefinition(t, id)
			env.scoreCommon(def, &res)
			delete(res.Checks, "no_cli_in_scripts")
			delete(res.Checks, "valid_account_executor")
			azureElsewhere := false
			for _, task := range evalTasks(def) {
				if task.typ == "azure.cli" || task.typ == "cloud.azure.cli" {
					if _, p := env.accountOf(task); p != "Azure" {
						azureElsewhere = true
						res.Notes = append(res.Notes, fmt.Sprintf("%s: Azure CLI bound to a %s account", task.id, p))
					}
				}
			}
			res.Checks["no_azure_steps_on_other_accounts"] = !azureElsewhere
		}
		res.ToolCalls = sessionToolCalls(t, r.sessionId, env.cluster.id)
		record(t, r, res, started)
	})
}

// evalKeepsCluster: the fix keeps the step on the cluster — either the script
// stays on the cluster account (now on the agent) or it became a built-in
// kubectl action on it. Moving it to another account or dropping it fails.
func evalKeepsCluster(env evalEnv, def map[string]any, res *evalResult) bool {
	for _, task := range evalTasks(def) {
		if task.typ == "scripting.run_script" || task.typ == "k8s.cli" || task.typ == "cloud.k8s.cli" {
			if _, p := env.accountOf(task); p == "K8s" {
				return true
			}
		}
	}
	res.Notes = append(res.Notes, "no step still runs against the cluster account")
	return false
}
