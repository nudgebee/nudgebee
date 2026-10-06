package api

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"nudgebee/llm/common"
	"nudgebee/llm/events"
	"nudgebee/llm/relay"
	"nudgebee/llm/security"
	"nudgebee/llm/tools"
)

// How an action reaches the system. Decided from facts about the executor, never from the action's text.
const (
	// DeliveryRun: NudgeBee can run the command for the operator.
	DeliveryRun = "run"
	// DeliveryOperator: the command is right but NudgeBee cannot run it now; the operator runs it.
	DeliveryOperator = "operator"
	// DeliverySource: the durable change lives in Helm values, a Git/Argo source or code.
	DeliverySource = "source"
)

// refusalTTL bounds how long a permission refusal shapes new plans, so a granted permission is
// learned even if nobody presses Run on the refused step.
const refusalTTL = 24 * time.Hour

// executorRefusal is the latest permission refusal NudgeBee's executor gave for one verb on an account.
type executorRefusal struct {
	Verb      string
	Message   string
	RefusedAt time.Time
}

// executorFacts is what is known about running commands on one account when a plan is shown.
type executorFacts struct {
	provider  string
	connected bool // the account's cluster agent (k8s accounts only)
	refusals  map[string]executorRefusal
	now       time.Time
}

// assignDelivery sets each action's delivery from the account's executor. It runs whenever a plan is
// returned, not only when it is generated: an agent reconnects, a permission is granted.
func assignDelivery(ctx *security.RequestContext, accountId string, plan *RemediationPlan) {
	if len(plan.Actions) == 0 {
		return
	}
	provider := tools.GetCloudProviderForAccount(accountId)
	facts := executorFacts{
		provider:  provider,
		connected: isK8sProvider(provider) && relay.AgentConnected(accountId),
		refusals:  loadExecutorRefusals(ctx, accountId, provider),
		now:       time.Now().UTC(),
	}
	for i := range plan.Actions {
		plan.Actions[i].Delivery, plan.Actions[i].DeliveryReason, plan.Actions[i].DeliveryRetry = deliveryFor(plan.Actions[i], facts)
	}
}

// deliveryFor decides one action's delivery, the reason written for the operator unless it is run, and
// whether NudgeBee may still run it (only a refusal can have been lifted since).
func deliveryFor(a RemediationAction, f executorFacts) (string, string, bool) {
	if a.AlertRuleChange != nil {
		return DeliveryRun, "", false // applied from the card through NudgeBee's own rule update
	}
	exec := strings.TrimSpace(a.ExecuteCommand)
	if exec == "" {
		return DeliverySource, "", false
	}
	substrate := tools.RemediationSubstrateFor(f.provider, exec)
	if substrate.Reject != "" {
		return DeliveryOperator, "NudgeBee can't run this command on this account: " + substrate.Reject, false
	}
	if substrate.RelayJob != "" && isK8sProvider(f.provider) && !f.connected {
		return DeliveryOperator, "NudgeBee runs commands on this cluster through its agent, and the agent is not connected right now. Run this command yourself against the cluster.", false
	}
	if r, ok := f.refusals[refusalKey(f.provider, exec)]; ok {
		return DeliveryOperator, fmt.Sprintf("NudgeBee was refused permission to run %q on this account %s ago: %s",
			r.Verb, agoText(f.now.Sub(r.RefusedAt)), strings.TrimSpace(r.Message)), true
	}
	return DeliveryRun, "", false
}

func isK8sProvider(provider string) bool {
	return strings.EqualFold(strings.TrimSpace(provider), "k8s")
}

func agoText(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "moments"
	case d < time.Hour:
		return fmt.Sprintf("%d min", int(d.Minutes()))
	default:
		return fmt.Sprintf("%d h", int(d.Hours()))
	}
}

// refusalKey scopes a refusal to what the executor authorized: its substrate and verb, and for kubectl
// the resource kind and namespace, since Kubernetes RBAC grants per resource and per namespace.
func refusalKey(provider, command string) string {
	s := tools.RemediationSubstrateFor(provider, command)
	key := firstNonEmpty(s.CloudCliTool, string(s.RelayJob)) + "|" + executorVerb(command)
	if f := strings.Fields(command); len(f) > 0 && f[0] == "kubectl" {
		key += "|" + tools.KubectlTargetKind(command) + "|" + tools.KubectlNamespace(command)
	}
	return key
}

// planOperatorStep reports whether an operator marked done a step of the plan: the action whose execute
// command this is, or, for a command-less source change, the action of that name.
func planOperatorStep(plan RemediationPlan, command, action string) bool {
	command, action = strings.TrimSpace(command), strings.TrimSpace(action)
	for _, a := range plan.Actions {
		exec := strings.TrimSpace(a.ExecuteCommand)
		if command != "" && exec == command {
			return true
		}
		if command == "" && exec == "" && action != "" && strings.TrimSpace(a.Action) == action {
			return true
		}
	}
	return false
}

// operatorChangePrefix marks the resolution key of a command-less source change the operator made.
const operatorChangePrefix = "change:"

// operatorStepRef is the resolution key for a step done outside NudgeBee: its command, which the
// panel already matches for run steps, or the source change's action name.
func operatorStepRef(command, action string) string {
	if c := strings.TrimSpace(command); c != "" {
		return c
	}
	return operatorChangePrefix + strings.TrimSpace(action)
}

// recordOperatorStep records that the operator carried out a step outside NudgeBee, as an ordinary
// successful CommandExecution marked manual, so ordering and attempt history see it.
func recordOperatorStep(ctx *security.RequestContext, request RemediationExecuteRequest, userId string) {
	dbManager, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		ctx.GetLogger().Warn("remediation: database unavailable, operator step not recorded", "error", err)
		return
	}
	// The request was already bound to the plan, so its command or action name is the plan's own.
	repo := events.NewEventAnalysisRepository(dbManager)
	ref := operatorStepRef(request.Command, request.Action)
	if err := repo.InsertRemediationExecution(ctx, request.EventId, userId, ref, `{"manual":true}`, "Done by the operator outside NudgeBee", true); err != nil {
		ctx.GetLogger().Warn("remediation: failed to record operator step", "error", err)
	}
}

// attemptRow is one recorded remediation run on the account.
type attemptRow struct {
	Command   string    `db:"command"`
	Status    string    `db:"status"`
	Error     string    `db:"error"`
	Manual    bool      `db:"manual"`
	CreatedAt time.Time `db:"created_at"`
}

// loadExecutorRefusals derives the account's live permission refusals from the last day's attempts
// (the record every Run already writes). Fails open: no rows or a DB error -> none.
func loadExecutorRefusals(ctx *security.RequestContext, accountId, provider string) map[string]executorRefusal {
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil || accountId == "" {
		return map[string]executorRefusal{}
	}
	var rows []attemptRow
	// LOCALTIMESTAMP, not NOW(): created_at is a timestamp without time zone, written in the session's zone.
	if err := dbms.Db.Select(&rows, `SELECT er.type_reference_id AS command, er.status::text AS status,
		       COALESCE(er.data->>'error', '') AS error, COALESCE((er.data->>'manual')::boolean, false) AS manual, er.created_at
		  FROM event_resolution er JOIN events e ON e.id = er.event_id
		 WHERE e.cloud_account_id = $1 AND er.type = 'CommandExecution'
		   AND er.created_at > LOCALTIMESTAMP - $2::interval
		 ORDER BY er.created_at DESC LIMIT 200`,
		accountId, fmt.Sprintf("%d seconds", int(refusalTTL.Seconds()))); err != nil {
		ctx.GetLogger().Debug("remediation: attempts not read for refusals", "error", err)
		return map[string]executorRefusal{}
	}
	return refusalsFromAttempts(rows, provider)
}

// refusalsFromAttempts: per executor & verb, the newest success or permission refusal decides (rows
// arrive newest first); manual records and transient failures say nothing about permission.
func refusalsFromAttempts(rows []attemptRow, provider string) map[string]executorRefusal {
	out := map[string]executorRefusal{}
	seen := map[string]bool{}
	for _, r := range rows {
		failed := strings.EqualFold(r.Status, "Failed")
		if r.Manual || (failed && !isPermissionRefusal(r.Error)) {
			// timeout / lost connection -> neither a refusal nor a reason to forget one
			continue
		}
		key := refusalKey(provider, r.Command)
		if seen[key] {
			continue
		}
		seen[key] = true
		if failed {
			out[key] = executorRefusal{Verb: executorVerb(r.Command), Message: errorLine(r.Error, isPermissionRefusal), RefusedAt: r.CreatedAt}
		}
	}
	return out
}

// failureText is what the executor said about a failed run, stdout included: the cloud workspace puts
// the CLI's own error there ("An error occurred (AccessDenied)") and only "exit status 254" in Error.
func failureText(r tools.RemediationExecutionResult) string {
	if r.Success {
		return ""
	}
	return strings.TrimSpace(strings.Join(nonEmpty([]string{strings.TrimSpace(r.Stdout), strings.TrimSpace(r.Stderr), strings.TrimSpace(r.Error)}), "\n"))
}

// permissionRefusalMarkers are how the executors say "not allowed", as opposed to "failed" or
// "unreachable". Kept apart from tool_nudgebee's AUTHORIZATION_FAILED patterns, which classify provider
// API errors and miss the agent's allowlist refusal and gcloud's PERMISSION_DENIED.
var permissionRefusalMarkers = []string{
	"not in read-only allowlist",
	"enablewritepermissions",
	"accessdenied",
	"unauthorizedoperation",
	"is not authorized to perform",
	"permission_denied",
	"does not have permission",
	"authorizationfailed",
}

// kubectlRBACRefusal is Kubernetes RBAC's wording; a bare "Forbidden" is also a quota or webhook rejection.
var kubectlRBACRefusal = regexp.MustCompile(`cannot \S+ resource`)

// isPermissionRefusal reports whether an executor's error text says the command was not allowed.
// Timeouts, overload and lost agent connections are deliberately not permission refusals.
func isPermissionRefusal(text string) bool {
	t := strings.ToLower(text)
	if kubectlRBACRefusal.MatchString(t) {
		return true
	}
	for _, m := range permissionRefusalMarkers {
		if strings.Contains(t, m) {
			return true
		}
	}
	return false
}

// executorVerb is the part of a command the executor authorizes: the kubectl/helm/argocd verb, or a
// cloud CLI's service and action. Empty when the command has none.
func executorVerb(command string) string {
	f := strings.Fields(command)
	if len(f) < 2 {
		return ""
	}
	words := []string{}
	for _, w := range f[1:] {
		if strings.HasPrefix(w, "-") {
			break
		}
		words = append(words, w)
	}
	switch strings.ToLower(f[0]) {
	case "kubectl", "helm", "argocd":
		return firstWords(words, 1)
	case "aws":
		return firstWords(words, 2)
	default:
		// gcloud/az/gsutil/bq group commands as a path ("run services update").
		return firstWords(words, 3)
	}
}

func firstWords(words []string, n int) string {
	if len(words) > n {
		words = words[:n]
	}
	return strings.Join(words, " ")
}
