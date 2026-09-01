package observability

import (
	"strings"

	"nudgebee/services/eventrule/playbooks"
	"nudgebee/services/security"
)

func init() {
	playbooks.RegisterAction("dependency_logs", &dependencyLogsAction{})
}

// dependency_logs collects logs for the *callee* of a client-side alert.
//
// Every other enricher resolves off the event's subject, which for a client-side
// alert is the caller. "High gRPC client error rate from ad calling
// flagd.evaluation.v2.Service/EventStream" is a statement about flagd, but the
// logs, metrics, deployment history and traces all described ad. Measured over a
// week on dev: 934 of 934 log payloads for that alert mentioned neither flagd nor
// EventStream, so the error the alert fired on was never once in its own evidence.
//
// Deliberately a separate action rather than a second card from `logs`: the log
// actions are mutually exclusive (eventrule.logActions), so folding the callee
// into `logs` would make collecting one suppress the other.
type dependencyLogsAction struct{}

// calleeLabelKeys name the downstream a client-side alert is complaining about,
// most specific first.
var calleeLabelKeys = []string{"rpc_service", "callee", "destination_service", "upstream_service"}

// resolveCallee returns the workload name of the dependency the event is about,
// or "" when this is not a dependency alert.
//
// rpc_service carries a fully-qualified gRPC service name
// ("flagd.evaluation.v2.Service"); the workload is its first segment.
// downstream_services, when the knowledge graph populated it, is the authority on
// what the caller actually talks to — a candidate that appears in both beats a
// bare prefix guess, and one that appears in neither is not worth a query.
func resolveCallee(event playbooks.PlaybookEvent) string {
	if event.Labels == nil {
		return ""
	}
	downstream := parseServiceListLabel(event.Labels["downstream_services"])
	for _, key := range calleeLabelKeys {
		raw := strings.TrimSpace(event.Labels[key])
		if raw == "" {
			continue
		}
		candidate := raw
		if i := strings.Index(candidate, "."); i > 0 {
			candidate = candidate[:i]
		}
		// The callee has to be someone other than the subject, or this is just
		// the workload query that already ran.
		if candidate == "" ||
			strings.EqualFold(candidate, event.SubjectOwner) ||
			strings.EqualFold(candidate, event.SubjectName) {
			continue
		}
		if len(downstream) > 0 && !downstream[candidate] {
			continue
		}
		return candidate
	}
	return ""
}

// parseServiceListLabel reads the knowledge graph's "[kafka flagd kube-dns]"
// label rendering into a set.
func parseServiceListLabel(raw string) map[string]bool {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "[")
	raw = strings.TrimSuffix(raw, "]")
	out := map[string]bool{}
	for _, field := range strings.FieldsFunc(raw, func(r rune) bool { return r == ' ' || r == ',' }) {
		if field = strings.TrimSpace(field); field != "" {
			out[field] = true
		}
	}
	return out
}

func (a *dependencyLogsAction) CanAutoExecute(ctx playbooks.PlaybookActionContext) bool {
	if getEventNamespace(ctx.GetEvent()) == "" {
		return false
	}
	if resolveCallee(ctx.GetEvent()) == "" {
		return false
	}
	// Mirror observabilityLogAction.CanAutoExecute: a missing or unreachable log
	// source is not a reason to skip, because the agent can still answer for a
	// K8s subject. Requiring a configured source here meant a transient lookup
	// failure produced no callee card at all while the subject's own log card
	// still rendered — an asymmetry that hides the gap rather than showing it.
	requestCtx := security.NewRequestContextForTenantAdmin(ctx.GetTenantId(), ctx.GetLogger(), nil, nil)
	if source, err := getLogSourceForAccount(requestCtx, ctx.GetAccountId(), "", ""); err == nil && source != nil {
		return true
	}
	return isK8sLogTarget(resolveCallee(ctx.GetEvent()), getEventNamespace(ctx.GetEvent())) &&
		!isCloudEventSource(ctx.GetEvent().Source)
}

func (a *dependencyLogsAction) AutoExecute(ctx playbooks.PlaybookActionContext) (playbooks.PlaybookActionResponse, error) {
	callee := resolveCallee(ctx.GetEvent())
	if callee == "" {
		return nil, nil
	}
	return a.Execute(ctx, map[string]any{
		"callee":    callee,
		"namespace": getEventNamespace(ctx.GetEvent()),
	})
}

// fetchCalleeLogsViaKubectl is the agent fallback, reusing the same
// time-bounded kubectl path the subject's own logs take. The callee is a
// workload name, so it resolves as a deployment.
func (a *dependencyLogsAction) fetchCalleeLogsViaKubectl(ctx playbooks.PlaybookActionContext, callee, namespace string) (playbooks.PlaybookActionResponse, error) {
	if !isK8sLogTarget(callee, namespace) || isCloudEventSource(ctx.GetEvent().Source) {
		return nil, nil
	}
	resp, err := (&observabilityLogAction{}).fetchLogsViaKubectl(ctx, "deployment", callee, namespace)
	if err != nil || resp == nil {
		ctx.GetLogger().Info("observability: dependency kubectl logs returned nothing",
			"callee", callee, "namespace", namespace, "error", err)
		return nil, nil
	}
	// Re-badge as this action so it renders as the dependency card and does not
	// collide with the subject's own log evidence.
	if info := resp.GetAdditionalInfo(); info != nil {
		info["action_name"] = "dependency_logs"
		info["title"] = "Logs — " + callee + " (downstream dependency)"
		info["callee"] = callee
	}
	return resp, nil
}

func (a *dependencyLogsAction) Execute(ctx playbooks.PlaybookActionContext, rawParams map[string]any) (playbooks.PlaybookActionResponse, error) {
	callee, _ := rawParams["callee"].(string)
	namespace, _ := rawParams["namespace"].(string)
	if callee == "" || namespace == "" {
		return nil, nil
	}

	// Same window as the subject's own logs, so the two cards line up.
	startTime, endTime := resolveLogQueryWindow(ctx.GetEvent(), 0)

	requestCtx := security.NewRequestContextForTenantAdmin(ctx.GetTenantId(), ctx.GetLogger(), nil, nil)
	logResult, err := FetchLogs(requestCtx, FetchLogRequest{
		AccountId: ctx.GetAccountId(),
		StartTime: startTime,
		EndTime:   endTime,
		Limit:     1000,
		QueryRequest: LogsQueryBuilderRequest{
			Where: buildWorkloadLogWhereClause(callee, namespace),
		},
	})
	if err != nil || len(logResult.Logs) == 0 {
		ctx.GetLogger().Info("observability: dependency log source returned nothing, trying the agent",
			"callee", callee, "namespace", namespace, "error", err)
		return a.fetchCalleeLogsViaKubectl(ctx, callee, namespace)
	}

	additionalInfo := addExecutedQueryInfo(map[string]any{
		"action_name": "dependency_logs",
		"title":       "Logs — " + callee + " (downstream dependency)",
		"callee":      callee,
		"namespace":   namespace,
	}, logResult)

	metaQuery := map[string]any{
		"callee":     callee,
		"namespace":  namespace,
		"start_time": startTime,
		"end_time":   endTime,
		"source":     "configured_integration",
	}
	return logsResponseOrNil(buildLogsActionResponse(
		logResult.Logs, "Logs — "+callee+" (downstream dependency)",
		additionalInfo, actionLogMaxInsightErrors, metaQuery, nil, nil))
}
