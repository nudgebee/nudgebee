package core

import (
	"database/sql"
	"encoding/json"
	"errors"
	"nudgebee/services/internal/database"
	"nudgebee/services/llm"
	"nudgebee/services/security"
	"nudgebee/services/tenant"
	"regexp"
	"strings"

	"github.com/prometheus/prometheus/promql/parser"
)

const (
	// workloadLookupSelect is the SELECT/WHERE prefix (through `AND `) for a
	// k8s_workloads match; the caller appends a per-strategy predicate.
	workloadLookupSelect = `SELECT name, namespace, kind, cloud_resource_id FROM k8s_workloads WHERE tenant_id = $1 AND cloud_account_id = $2 AND `
	// workloadLookupFilter is the trailing activeness/kind filter shared by the
	// match lookup and the namespace-ambiguity COUNT query.
	workloadLookupFilter = ` AND is_active = true AND kind NOT IN ('Job','CronJob')`
)

// buildWorkloadLookupQuery assembles the k8s_workloads lookup for one match
// strategy. `predicate` is the strategy's WHERE fragment ($3 = candidate). When
// namespaceScoped is true an `AND namespace = $4` clause is added so a workload
// name present in multiple namespaces resolves to the row for the event's own
// namespace rather than an arbitrary LIMIT 1 pick.
func buildWorkloadLookupQuery(predicate string, namespaceScoped bool) string {
	q := workloadLookupSelect + predicate
	if namespaceScoped {
		q += ` AND namespace = $4`
	}
	return q + workloadLookupFilter + ` LIMIT 1`
}

// MatchWorkloadAndEnrich validates EventSubjectName against the k8s_workloads
// inventory for the given account and enriches the event with namespace, kind,
// cloud_resource_id, and matching labels.
//
// Strategies tried in order per candidate: exact name, app.kubernetes.io/name
// label, app label, tags.datadoghq.com/service label. All four require an
// exact match — there is intentionally no substring/ILIKE fallback because it
// risks overwriting a correct subject with an unrelated workload whose name
// happens to contain the candidate as a substring.
//
// EventSubjectOwner (e.g. the deployment/daemonset a bare pod subject was
// resolved to) is tried next when set and distinct from EventSubjectName — a
// pod name rarely matches k8s_workloads directly since that table stores
// owning workloads, not individual pod instances.
//
// When labels[WorkloadCandidatesLabel] is set (comma-separated list, e.g. the
// dynatrace impacted-entity list), each entry is also tried after the above
// fail — first match wins. The label is consumed before persistence.
//
// When a webhook opts out via labels["nb_skip_workload_match"]="true" (e.g.
// solarwinds title-as-subject events that would false-positive on ILIKE), the
// function returns without querying the workloads table. The opt-out label is
// consumed on its way out so it doesn't leak into the persisted event.
func MatchWorkloadAndEnrich(sc *security.RequestContext, e *EventIncomingWebhook, accountId string) {
	if e.EventSubjectName == "" && e.Investigation.Labels[WorkloadCandidatesLabel] == "" {
		return
	}
	if e.Investigation.Labels[SkipWorkloadMatchLabel] == "true" {
		delete(e.Investigation.Labels, SkipWorkloadMatchLabel)
		delete(e.Investigation.Labels, WorkloadCandidatesLabel)
		return
	}

	candidates := []string{e.EventSubjectName}
	seen := map[string]bool{e.EventSubjectName: true}
	if e.EventSubjectOwner != "" && !seen[e.EventSubjectOwner] {
		seen[e.EventSubjectOwner] = true
		candidates = append(candidates, e.EventSubjectOwner)
	}
	if extra := e.Investigation.Labels[WorkloadCandidatesLabel]; extra != "" {
		for _, c := range strings.Split(extra, ",") {
			c = strings.TrimSpace(c)
			if c == "" || seen[c] {
				continue
			}
			seen[c] = true
			candidates = append(candidates, c)
		}
	}
	delete(e.Investigation.Labels, WorkloadCandidatesLabel)

	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		sc.GetLogger().Error("subject_resolution: failed to get database manager", "error", err)
		return
	}

	tenantId := sc.GetSecurityContext().GetTenantId()

	type workloadResult struct {
		Name            string `db:"name"`
		Namespace       string `db:"namespace"`
		Kind            string `db:"kind"`
		CloudResourceId string `db:"cloud_resource_id"`
	}
	var workload workloadResult

	// Namespace-qualify the lookup whenever the event already knows its namespace
	// (the common case). Without it, a workload NAME present in several namespaces
	// of the same account resolves every event to one arbitrary LIMIT 1 row, so a
	// single cloud_resource_id gets shared across namespaces and triage then emits
	// false same_resource correlations across independent deployments.
	ns := e.EventSubjectNamespace

	// Per-strategy WHERE predicate ($3 = candidate). Exact name first, then the
	// three label conventions. Order is significant (first match wins).
	predicates := []string{
		`name = $3`,
		`labels->>'app.kubernetes.io/name' = $3`,
		`labels->>'app' = $3`,
		// Datadog convention — workloads tagged with tags.datadoghq.com/service.
		`labels->>'tags.datadoghq.com/service' = $3`,
	}
	matched := false
	matchedCandidate := ""
	matchedPredicate := ""
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		for _, pred := range predicates {
			q := buildWorkloadLookupQuery(pred, ns != "")
			args := []interface{}{tenantId, accountId, candidate}
			if ns != "" {
				args = append(args, ns)
			}
			if err := dbms.Db.Get(&workload, q, args...); err == nil {
				matched = true
				matchedCandidate = candidate
				matchedPredicate = pred
				break
			}
		}
		if matched {
			break
		}
	}

	// Namespace-unknown fallback: the name-only match returned an arbitrary row.
	// If the same predicate matches workloads in more than one namespace, the
	// resource identity is ambiguous — do NOT trust the row's cloud_resource_id /
	// service_key (either would fabricate a cross-namespace same_resource edge).
	ambiguous := false
	if matched && ns == "" {
		// Count distinct namespaces, not cloud_resource_id: unresolved workloads
		// often have NULL/empty cloud_resource_id, which would collapse to 0/1 and
		// hide a genuine cross-namespace name collision.
		var distinctNamespaces int
		countQ := `SELECT COUNT(DISTINCT namespace) FROM k8s_workloads WHERE tenant_id = $1 AND cloud_account_id = $2 AND ` + matchedPredicate + workloadLookupFilter
		if err := dbms.Db.Get(&distinctNamespaces, countQ, tenantId, accountId, matchedCandidate); err != nil {
			// Can't verify uniqueness — fail safe and treat as ambiguous so a DB
			// error can't cause a fabricated cross-namespace identity.
			sc.GetLogger().Error("subject_resolution: namespace ambiguity check failed", "error", err, "candidate", matchedCandidate)
			ambiguous = true
		} else if distinctNamespaces > 1 {
			ambiguous = true
		}
	}

	if e.Investigation.Labels == nil {
		e.Investigation.Labels = map[string]string{}
	}

	if !matched {
		sc.GetLogger().Info("subject_resolution: no workload match", "candidates", candidates, "account_id", accountId)
		e.Investigation.Labels["nb_matched_workload"] = "false"
		return
	}
	// Capture the pre-update name so we can keep EventSubjectOwner consistent
	// with EventSubjectName when the webhook had set them in lockstep
	// (datadog/dynatrace/newrelic pattern). Webhooks that intentionally use
	// EventSubjectOwner for a different purpose (e.g. servicenow's AssignedTo
	// user) won't satisfy this equality check, so they're left alone.
	priorSubjectName := e.EventSubjectName

	e.EventSubjectName = workload.Name
	kindLower := strings.ToLower(workload.Kind)
	e.EventSubjectKind = kindLower

	// Namespace-identifying enrichment (namespace, cloud_resource_id, service_key)
	// is only trustworthy when the match was namespace-qualified. In the ambiguous
	// name-only case the row is one arbitrary namespace's — writing it would forge
	// a cross-namespace identity, so skip those fields. Kind/owner are the same
	// across namespaces, so they stay.
	if !ambiguous {
		if e.EventSubjectNamespace == "" {
			e.EventSubjectNamespace = workload.Namespace
		}
		e.CloudResourceId = workload.CloudResourceId
		// Canonical KG-aligned service key (namespace:Kind:name, capitalized kind to
		// match the knowledge-graph node/alias format) so triage correlation's
		// dependency path can key-match. Guarded on empty so cloud webhooks
		// (GCP/PagerDuty) that already set an ARN-style service_key keep it.
		if e.ServiceKey == "" && e.EventSubjectNamespace != "" {
			e.ServiceKey = e.EventSubjectNamespace + ":" + workload.Kind + ":" + workload.Name
		}
	}

	if e.EventSubjectOwner == "" || e.EventSubjectOwner == priorSubjectName {
		e.EventSubjectOwner = workload.Name
	}
	if e.EventSubjectOwnerKind == "" {
		e.EventSubjectOwnerKind = kindLower
	}

	e.Investigation.Labels["pod"] = workload.Name
	e.Investigation.Labels["kind"] = workload.Kind
	e.Investigation.Labels["nb_matched_workload"] = "true"
	if ambiguous {
		e.Investigation.Labels["nb_workload_ambiguous_namespace"] = "true"
	} else {
		if workload.Namespace != "" {
			e.Investigation.Labels["namespace"] = workload.Namespace
		}
		e.Investigation.Labels["cloud_resource_id"] = workload.CloudResourceId
	}
}

// SkipWorkloadMatchLabel marks an event whose EventSubjectName is display-only
// (e.g. an alert title) or a non-workload identity (e.g. a PostgreSQL database
// name) and should not be matched against the k8s_workloads inventory. Exported
// so producing webhooks (e.g. the Prometheus datname path) can set it. The
// label is consumed by MatchWorkloadAndEnrich so it never reaches the persisted
// event payload.
const SkipWorkloadMatchLabel = "nb_skip_workload_match"

// WorkloadCandidatesLabel carries a comma-separated list of additional workload
// names a webhook would like MatchWorkloadAndEnrich to try after EventSubjectName
// (e.g. dynatrace's impacted-entity list). The label is consumed during matching.
const WorkloadCandidatesLabel = "nb_workload_candidates"

const (
	// webhookSubjectAgentMention pins the dedicated single-shot classifier agent in
	// llm-server, bypassing the LLM router.
	webhookSubjectAgentMention = "@webhook_subject_name_extractor"
	// webhookSubjectAgentSource tags the request origin for observability/budgeting.
	webhookSubjectAgentSource = "webhook_subject_name_extractor"
	// webhookSubjectAgentNotFound is the sentinel the agent returns when it declines.
	webhookSubjectAgentNotFound = "Not Found"
)

// clusterScopePromQLParser is a shared, stateless PromQL parser used to classify
// an alert's scope from its rule expression. ParseExpr builds a fresh internal
// parser per call, so one package-level instance is concurrency-safe.
var clusterScopePromQLParser = parser.NewParser(parser.Options{})

// objectDimensionLabels are the Prometheus label names that identify a concrete
// k8s/cloud object an alert is about. When an alert's PromQL references any of
// these (as a selector matcher, an aggregation `by (…)` grouping label, or an
// `on (…)` matching label), it has a per-object subject — so it is NOT cluster
// scoped. `job`, `cluster`, `resource`, `severity`, etc. are intentionally absent:
// `job` is the scrape target, not the alert's subject.
var objectDimensionLabels = map[string]bool{
	"namespace": true, "pod": true, "pod_name": true, "node": true, "instance": true,
	"container": true, "container_name": true, "deployment": true, "statefulset": true,
	"daemonset": true, "replicaset": true, "cronjob": true, "job_name": true,
	"persistentvolumeclaim": true, "horizontalpodautoscaler": true, "hpa": true,
	"service": true, "workload": true, "destination_workload_name": true,
	"source_workload_name": true, "ingress": true, "endpoint": true,
}

// clusterScopedAlertNames is a guaranteed floor of well-known kube-prometheus /
// kubernetes-mixin alerts that are cluster- or control-plane-scoped and name NO
// object. It is the fallback for when the alert's rule expression isn't available
// in event_rules (e.g. the rule sync hasn't run for the tenant, or the very first
// occurrence races rule creation) — the primary, data-driven classifier is
// isClusterScopedByExpr, which reads event_rules.expr. Their PromQL aggregates
// `by (cluster)` or uses `absent(up{job="…"})`, so the firing series carries no
// object-identifying label — there is nothing for the subject extractor to
// resolve. Sending them to the LLM wastes a call and, worse, invites a
// hallucinated subject (observed in prod: KubeSchedulerDown →
// "newrelic-bundle-nrk8s-controlplane", KubeVersionMismatch →
// "prometheus-…-kube-prometheus-prometheus").
//
// Deliberately CONSERVATIVE: only alerts with genuinely no object dimension are
// listed. Alerts that carry a resolvable subject are intentionally excluded even
// when they are "infrastructure" — e.g. TargetDown (job/instance), KubeClientErrors
// (instance), KubeAggregatedAPIDown (name/namespace), AlertmanagerClusterDown
// (namespace/service), etcd* (instance) — because a false skip would HIDE a real
// subject, which is worse than a wasted LLM call.
var clusterScopedAlertNames = map[string]bool{
	// Cluster capacity / quota overcommit — aggregate `by (cluster)`.
	"KubeCPUOvercommit":         true,
	"KubeMemoryOvercommit":      true,
	"KubeCPUQuotaOvercommit":    true,
	"KubeMemoryQuotaOvercommit": true,
	// Control-plane component down — `absent(up{job="…"})`, no object label.
	"KubeSchedulerDown":         true,
	"KubeControllerManagerDown": true,
	"KubeAPIDown":               true,
	"KubeProxyDown":             true,
	"KubeletDown":               true,
	// Cluster-wide apiserver / version / cert health — no object dimension.
	"KubeVersionMismatch":             true,
	"KubeAPIErrorBudgetBurn":          true,
	"KubeAPITerminatedRequests":       true,
	"KubeClientCertificateExpiration": true,
	// Always-firing watchdog — no subject by construction.
	"Watchdog": true,
}

// alertNameFromLabels returns the canonical alert name for an event, using the
// same precedence the playbook lookup uses: `nb_alert_name` (set during webhook
// enrichment, e.g. the real prometheus alert name extracted from a PagerDuty
// title) then the raw `alertname` label. This is path-independent — it survives
// Prometheus → PagerDuty/Datadog relays that flatten the original labels.
func alertNameFromLabels(labels map[string]string) string {
	if labels == nil {
		return ""
	}
	if n := labels["nb_alert_name"]; n != "" {
		return n
	}
	return labels["alertname"]
}

// classifyPromExprClusterScoped parses a Prometheus rule expression and reports
// whether the alert it defines is cluster-scoped — i.e. it produces a series with
// no object-identifying label, so there is nothing to resolve a subject to.
//
// An alert is cluster-scoped iff (a) there is a positive cluster signal — an
// aggregation grouped `by (cluster)` / `by ()` (collapsing to the cluster or a
// global scalar), or an `absent()` — AND (b) no object-dimension label appears
// anywhere in the expression (selector matchers, `by (…)` groupings, or `on (…)`
// matching labels). Requiring the positive signal is what makes this safe: a bare
// per-instance alert like `up{job="x"} == 0` carries a natural `instance` label
// that isn't in any matcher, so it has no cluster signal and is NOT skipped.
//
// The AST is used deliberately: recording-rule metric NAMES such as
// `namespace_cpu:kube_pod_container_resource_requests:sum` contain "namespace" as
// a substring but expose it as `__name__`, not a label — a text scan would
// misclassify; the parser does not. `parsed` is false when expr is not valid
// PromQL (e.g. a Datadog/chronosphere expression), so the caller falls through.
func classifyPromExprClusterScoped(expr string) (isCluster bool, parsed bool) {
	node, err := clusterScopePromQLParser.ParseExpr(expr)
	if err != nil {
		return false, false
	}
	objectSeen := false
	clusterSignal := false
	parser.Inspect(node, func(n parser.Node, _ []parser.Node) error {
		switch e := n.(type) {
		case *parser.VectorSelector:
			for _, m := range e.LabelMatchers {
				if m.Name != "__name__" && objectDimensionLabels[m.Name] {
					objectSeen = true
				}
			}
		case *parser.AggregateExpr:
			// `without (…)` keeps the unlisted labels (unknown, possibly object) and
			// gives no cluster collapse — no signal either way.
			if !e.Without {
				onlyCluster := true
				for _, g := range e.Grouping {
					if objectDimensionLabels[g] {
						objectSeen = true
					}
					if g != "cluster" {
						onlyCluster = false
					}
				}
				if onlyCluster {
					clusterSignal = true
				}
			}
		case *parser.Call:
			if e.Func != nil && (e.Func.Name == "absent" || e.Func.Name == "absent_over_time") {
				clusterSignal = true
			}
		case *parser.BinaryExpr:
			// Only `on (…)` matching labels are carried into the result; `ignoring (…)`
			// labels are dropped, so they don't indicate an object subject.
			if e.VectorMatching != nil && e.VectorMatching.On {
				for _, l := range e.VectorMatching.MatchingLabels {
					if objectDimensionLabels[l] {
						objectSeen = true
					}
				}
			}
		}
		return nil
	})
	return clusterSignal && !objectSeen, true
}

// fetchEventRuleExpr returns the Prometheus rule expression for an alert from the
// event_rules catalog (the per-tenant source of truth, synced from the cluster's
// PrometheusRule CRDs). Returns "" on any miss/error so the caller falls back to
// the curated set.
func fetchEventRuleExpr(sc *security.RequestContext, accountId, alert string) string {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return ""
	}
	var expr string
	err = dbms.Db.Get(&expr,
		`SELECT expr FROM event_rules WHERE alert = $1 AND tenant_id = $2 AND account_id = $3 AND enabled = true AND expr IS NOT NULL AND expr != '' ORDER BY (right(source, 8) = '_webhook'), updated_at DESC LIMIT 1`,
		alert, sc.GetSecurityContext().GetTenantId(), accountId)
	if err != nil {
		return ""
	}
	return expr
}

// isClusterScopedAlert reports whether an alert is cluster-/control-plane-scoped
// with no object subject, so the LLM subject extractor can only return not_found
// or hallucinate. Primary signal is data-driven — the alert's PromQL from
// event_rules (covers custom cluster rules for prometheus/grafana). The curated
// clusterScopedAlertNames set is a floor for when the expression isn't available.
func isClusterScopedAlert(sc *security.RequestContext, accountId string, labels map[string]string) bool {
	alert := alertNameFromLabels(labels)
	if alert == "" {
		return false
	}
	if clusterScopedAlertNames[alert] {
		return true
	}
	if expr := fetchEventRuleExpr(sc, accountId, alert); expr != "" {
		if isCluster, parsed := classifyPromExprClusterScoped(expr); parsed && isCluster {
			return true
		}
	}
	return false
}

// ResolveSubjectNameViaAgent asks the webhook_subject_name_extractor agent to map
// an alert to a single running-service name. The agent owns the running-services
// inventory and the historical title→service patterns (fetched and cached
// server-side), so this sends only the alert itself — no inventory is embedded in
// the request, which is what keeps webhook token cost bounded.
//
// It returns the matched service name, or "" when the agent declines ("Not Found"),
// and records the nb_llm_match label (and a default service label) on labels.
//
// Every exit records nb_llm_match, so an absent label means only one thing: the
// agent was never asked. The values are the matched service name, "not_found"
// (agent declined), "skipped_cluster_scoped" (alert names no object), "error"
// (the call failed) and "empty" (the call returned nothing). They are diagnostic
// — nothing parses them; the sole reader is the presence check in
// enrichEventsWithSubjectResolution.
func ResolveSubjectNameViaAgent(sc *security.RequestContext, accountId, title, description, sourceURL string, labels map[string]string) string {
	if labels == nil {
		labels = map[string]string{}
	}

	// Skip the LLM for cluster-/control-plane-scoped alerts that carry no object
	// subject: the extractor has nothing to resolve and would only waste a call or
	// hallucinate. Scope is classified from the alert's PromQL in event_rules (with a
	// curated floor). Deterministic resolution has already run and come up empty by
	// the time we reach here, so nothing is lost. Runs at this single choke point so
	// it covers every ingestion path (prometheus, pagerduty, datadog, zenduty, …).
	if isClusterScopedAlert(sc, accountId, labels) {
		labels["nb_llm_match"] = "skipped_cluster_scoped"
		labels["nb_subject_resolution"] = "cluster-scoped"
		return ""
	}

	payload, _ := json.Marshal(map[string]any{
		"title":       title,
		"description": description,
		"source_url":  sourceURL,
		"labels":      labels,
	})

	chatRequest := llm.ConversationApiRequest{
		Query:     webhookSubjectAgentMention + "\n" + string(payload),
		AccountId: accountId,
		UserId:    sc.GetSecurityContext().GetUserId(),
		Async:     false,
		Source:    webhookSubjectAgentSource,
	}

	// Both failure exits record the attempt. Without a label an extraction that
	// blew up is indistinguishable from one that never ran (absent = deterministic
	// resolution won), and the alreadyTried guard in enrichEventsWithSubjectResolution
	// — a presence check — would let a failed call be retried immediately by the
	// next stage, doubling the retry cost ChatCompletion has already paid. The error
	// text itself stays out of the label: event labels are rendered in the UI's
	// Alert-labels table, and #35130 was exactly that leak.
	response, err := llm.ChatCompletion(sc, chatRequest)
	if err != nil {
		sc.GetLogger().Error("subject_resolution: webhook_subject_name_extractor call failed", "error", err)
		labels["nb_llm_match"] = "error"
		return ""
	}
	if response == nil || len(response.Response) == 0 {
		sc.GetLogger().Warn("subject_resolution: webhook_subject_name_extractor returned empty response")
		labels["nb_llm_match"] = "empty"
		return ""
	}

	name := strings.TrimSpace(response.Response[0])
	if name == "" || strings.EqualFold(name, webhookSubjectAgentNotFound) {
		labels["nb_llm_match"] = "not_found"
		return ""
	}
	labels["nb_llm_match"] = name
	if labels["service"] == "" {
		labels["service"] = name
	}
	return name
}

// ResolveSubjectViaLLM resolves an event's subject via the
// webhook_subject_name_extractor agent and validates the result against the
// k8s_workloads inventory. The caller should only invoke this when
// EventSubjectName is empty.
func ResolveSubjectViaLLM(sc *security.RequestContext, e *EventIncomingWebhook, accountId string) {
	if e.Investigation.Labels == nil {
		e.Investigation.Labels = map[string]string{}
	}

	name := ResolveSubjectNameViaAgent(sc, accountId, e.EventTitle, e.EventDescription, e.Investigation.SourceUrl, e.Investigation.Labels)
	if name == "" {
		return
	}

	e.EventSubjectName = name
	MatchWorkloadAndEnrich(sc, e, accountId)
}

// titleWordSplitter isolates hyphen-containing tokens from free text (a pod
// name is itself hyphen-separated, so hyphens must survive the split).
var titleWordSplitter = regexp.MustCompile(`[^a-zA-Z0-9-]+`)

// generatedPodNameReplicaSetRegex: Deployment/DaemonSet (and, incidentally,
// CronJob) shape <workload>-<hash>-<suffix>. Hash must contain a digit
// (generatedPodNameHashDigit) so a word like "monitoring-daemonset-xyz12"
// isn't mistaken for a hash. Mirrors podDeploymentRegex in
// traces/knowledge_graph_service.go.
var generatedPodNameReplicaSetRegex = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?-([a-z0-9]{5,10})-[a-z0-9]{5}$`)

// generatedPodNameJobRegex: Job shape <workload>-<hash>, one trailing segment.
var generatedPodNameJobRegex = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?-([a-z0-9]{5,10})$`)

var generatedPodNameHashDigit = regexp.MustCompile(`[0-9]`)

// generatedPodNameStatefulSetRegex: StatefulSet shape <workload>-<ordinal>.
// No leading zero — k8s never zero-pads ordinals — since unlike the
// hash-bearing shapes, "word-number" alone is common in ordinary text.
var generatedPodNameStatefulSetRegex = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?-(?:0|[1-9][0-9]*)$`)

// looksLikeGeneratedPodName reports whether word has the shape of a
// Kubernetes pod name. Pod names are DNS-1123 labels (always lowercase).
func looksLikeGeneratedPodName(word string) bool {
	if word == "" || word != strings.ToLower(word) {
		return false
	}
	if m := generatedPodNameReplicaSetRegex.FindStringSubmatch(word); m != nil && generatedPodNameHashDigit.MatchString(m[1]) {
		return true
	}
	if m := generatedPodNameJobRegex.FindStringSubmatch(word); m != nil && generatedPodNameHashDigit.MatchString(m[1]) {
		return true
	}
	return generatedPodNameStatefulSetRegex.MatchString(word)
}

// lookupPodOwnerByName resolves podName to its owning workload via k8s_pods,
// which the k8s-collector already resolves to the top-level controller.
// namespace may be empty. A no-rows result isn't logged as an error — a title
// word that merely looks like a pod name but isn't a real one is expected.
func lookupPodOwnerByName(sc *security.RequestContext, dbms *database.DatabaseManager, tenantId, accountId, namespace, podName string) (ownerName, ownerKind, ns string, ok bool) {
	var row struct {
		WorkloadName string  `db:"workload_name"`
		WorkloadType *string `db:"workload_type"`
		Namespace    string  `db:"namespace"`
	}
	query := `
		SELECT workload_name, workload_type, namespace
		FROM k8s_pods
		WHERE tenant_id = $1
		  AND cloud_account_id = $2
		  AND name = $3
		  AND workload_name IS NOT NULL AND workload_name <> ''`
	args := []any{tenantId, accountId, podName}
	if namespace != "" {
		query += " AND namespace = $4"
		args = append(args, namespace)
	}
	query += " ORDER BY last_seen DESC NULLS LAST LIMIT 1"
	if err := dbms.Db.Get(&row, query, args...); err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			sc.GetLogger().Error("subject_resolution: title pod-owner lookup failed", "error", err, "pod", podName)
		}
		return "", "", "", false
	}
	var workloadType string
	if row.WorkloadType != nil {
		workloadType = *row.WorkloadType
	}
	return row.WorkloadName, strings.ToLower(workloadType), row.Namespace, true
}

// ResolveSubjectFromTitlePodName scans an alert title for a bare pod name and
// resolves it via k8s_pods — tried before the LLM extractor since it's a
// cheap, exact lookup. Covers titles with no "pod=" structure to anchor a
// regex on (e.g. Alertmanager's default title). Returns true on a match, so
// callers can skip the LLM fallback; call only when EventSubjectName is empty.
func ResolveSubjectFromTitlePodName(sc *security.RequestContext, e *EventIncomingWebhook, accountId string) bool {
	if sc == nil || sc.GetSecurityContext() == nil || e.EventTitle == "" || accountId == "" {
		return false
	}
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		sc.GetLogger().Error("subject_resolution: failed to get database manager", "error", err)
		return false
	}
	tenantId := sc.GetSecurityContext().GetTenantId()

	for _, word := range titleWordSplitter.Split(e.EventTitle, -1) {
		if !looksLikeGeneratedPodName(word) {
			continue
		}
		owner, kind, ns, ok := lookupPodOwnerByName(sc, dbms, tenantId, accountId, e.EventSubjectNamespace, word)
		if !ok {
			continue
		}
		if e.Investigation.Labels == nil {
			e.Investigation.Labels = map[string]string{}
		}
		e.Investigation.Labels["pod"] = word
		e.Investigation.Labels["nb_subject_resolution"] = "title_pod_name"
		e.EventSubjectName = owner
		e.EventSubjectKind = kind
		if e.EventSubjectNamespace == "" {
			e.EventSubjectNamespace = ns
		}
		MatchWorkloadAndEnrich(sc, e, accountId)
		return true
	}
	return false
}

// hasWorkloadValidationInput reports whether an event carries anything the
// k8s_workloads inventory can be asked about: a subject the webhook resolved
// itself, or candidate names it offered via nb_workload_candidates (e.g. the
// elasticsearch container name, the dynatrace impacted-entity list). A
// candidate-only event is a legitimate input — MatchWorkloadAndEnrich promotes
// a candidate to the subject only on an exact match, which is what makes it
// safe for a name that may or may not be a workload.
func hasWorkloadValidationInput(e *EventIncomingWebhook) bool {
	return e.EventSubjectName != "" || e.Investigation.Labels[WorkloadCandidatesLabel] != ""
}

// enrichEventsWithSubjectResolution runs on every webhook event after account
// mapping. When a subject is already identified, it validates against the
// workloads inventory. When one isn't, it falls back to the LLM extractor if
// the feature flag is enabled.
func enrichEventsWithSubjectResolution(sc *security.RequestContext, events []EventIncomingWebhook) {
	if len(events) == 0 {
		return
	}
	tenantId := sc.GetSecurityContext().GetTenantId()
	llmEnabled := tenant.IsFeatureEnabled(sc, tenantId, tenant.FEATURE_WEBHOOK_LLM_RESOLUTION)

	for i := range events {
		accountId := events[i].AccountId
		if accountId == "" {
			continue
		}
		if events[i].Investigation.Labels == nil {
			events[i].Investigation.Labels = map[string]string{}
		}
		if hasWorkloadValidationInput(&events[i]) {
			MatchWorkloadAndEnrich(sc, &events[i], accountId)
			// Candidate-only events whose candidates matched nothing still have no
			// subject; fall through to the remaining resolvers instead of giving up.
			if events[i].EventSubjectName != "" {
				continue
			}
		}
		// An integration-specific resolver (datadog/pagerduty/zenduty) already ran
		// the extractor agent when it set nb_llm_match. Don't fire a second, redundant
		// call here — the agent would just reproduce the prior not_found.
		if _, alreadyTried := events[i].Investigation.Labels["nb_llm_match"]; alreadyTried {
			continue
		}
		if ResolveSubjectFromTitlePodName(sc, &events[i], accountId) {
			continue
		}
		if llmEnabled {
			ResolveSubjectViaLLM(sc, &events[i], accountId)
		} else {
			events[i].Investigation.Labels["nb_llm_match"] = "disabled"
		}
	}
}
