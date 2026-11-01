package playbooks

import (
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"nudgebee/services/internal/database"
	"sort"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
)

// service_endpoints_enricher adds LIVE state to service_no_endpoints
// findings. The agent-side evidence is a snapshot at fire time; by the time
// someone investigates, the selector may have been fixed or the workload
// recreated. This enricher re-resolves, via relay:
//
//  1. the Service's current spec.selector,
//  2. its current Endpoints (address count — the authoritative signal),
//  3. every Deployment/StatefulSet/DaemonSet pod template in the namespace,
//     so the selector-vs-template mismatch (and the closest candidate) is
//     visible on the card.
//
// and adds how old the problem is: the Service's creation time, and the first
// alert for it still on record. "No endpoints" alone reads as a live outage;
// these two facts tell a long-standing leftover apart from a fresh break.
//
// The pod inventory is deliberately not used for this: it drops a removed pod
// once the pod is more than 30 days old (by creation time), so a long-lived
// pod removed yesterday can already be gone, and "last matching pod" would
// make a fresh break look old.
//
// Output is a markdown block routed to the existing TextEnricherDynamicCard
// via additional_info.actual_action_name="text_enricher" — deliberately no
// new frontend actionType/table-name registration needed.
type serviceEndpointsAction struct{}

func (a *serviceEndpointsAction) CanAutoExecute(ctx PlaybookActionContext) bool {
	ev := ctx.GetEvent()
	if ev.AggregationKey != "service_no_endpoints" {
		return false
	}
	name, ns := subjectServiceNamespace(ev)
	return name != "" && ns != ""
}

func (a *serviceEndpointsAction) AutoExecute(ctx PlaybookActionContext) (PlaybookActionResponse, error) {
	name, ns := subjectServiceNamespace(ctx.GetEvent())
	return a.Execute(ctx, map[string]any{"service_name": name, "namespace": ns})
}

func (a *serviceEndpointsAction) Execute(ctx PlaybookActionContext, rawParams map[string]any) (PlaybookActionResponse, error) {
	serviceName, _ := rawParams["service_name"].(string)
	namespace, _ := rawParams["namespace"].(string)
	if serviceName == "" || namespace == "" {
		return nil, errors.New("service_endpoints_enricher: service_name + namespace required")
	}

	svcData, _, err := getResourceViaRelay(ctx, map[string]any{
		"resource_type":  "services",
		"group":          "",
		"version":        "v1",
		"namespace":      []string{namespace},
		"all_namespaces": false,
		"name":           []string{serviceName},
	})
	if err != nil {
		return nil, fmt.Errorf("service_endpoints_enricher: get service: %w", err)
	}
	svc := resourceDictNamed(svcData, serviceName, namespace)
	if svc == nil {
		return nil, errors.New("service_endpoints_enricher: service not found (deleted since the finding fired?)")
	}
	selector := serviceSelectorFromDict(svc)

	// Endpoints object shares the Service's name. A get error is non-fatal —
	// the selector/template comparison is still worth showing.
	addressCount := -1 // -1 = unknown
	epData, _, err := getResourceViaRelay(ctx, map[string]any{
		"resource_type":  "endpoints",
		"group":          "",
		"version":        "v1",
		"namespace":      []string{namespace},
		"all_namespaces": false,
		"name":           []string{serviceName},
	})
	if err == nil {
		if ep := resourceDictNamed(epData, serviceName, namespace); ep != nil {
			addressCount = endpointsAddressCount(ep)
		}
	}

	// One relay call for all three workload kinds — GetResource concatenates
	// comma-separated resource types into a flat array.
	var workloads []workloadTemplate
	wlData, _, err := getResourceViaRelay(ctx, map[string]any{
		"resource_type":  "deployments,statefulsets,daemonsets",
		"group":          "apps",
		"version":        "v1",
		"namespace":      []string{namespace},
		"all_namespaces": false,
	})
	if err == nil {
		workloads = workloadTemplatesFromList(wlData, namespace)
	}

	facts := serviceAgeFacts{now: time.Now().UTC()}
	facts.createdAt, facts.hasCreatedAt = serviceCreatedAt(svc)
	// Alert history only matters while the Service is still without endpoints,
	// and only for the alert's own Service (Execute can be called for another).
	// Non-fatal: without it the card still shows the live state.
	if fingerprint := ownAlertFingerprint(ctx.GetEvent(), serviceName, namespace); fingerprint != "" && addressCount <= 0 {
		if hist, err := lookupAlertHistory(ctx.GetAccountId(), fingerprint); err == nil {
			facts.alerts = &hist
		} else {
			slog.Warn("service_endpoints_enricher: alert history lookup failed",
				"error", err, "namespace", namespace, "service", serviceName)
		}
	}

	text, insights := buildServiceEndpointsReport(namespace, serviceName, selector, addressCount, workloads, facts)

	return serviceEndpointsMarkdownResponse{
		Title: "Service endpoint status (live)",
		Data:  text,
		AdditionalInfo: map[string]any{
			"title":       "Service endpoint status (live)",
			"action_name": "service_endpoints_enricher",
			// Route to the generic TextEnricherDynamicCard — no frontend
			// registration needed for a new action type.
			"actual_action_name": "text_enricher",
			"service_name":       serviceName,
			"namespace":          namespace,
		},
		Insight: insights,
	}, nil
}

// buildServiceEndpointsReport renders the markdown body + insights. Split out
// (pure) so tests can cover the broken / recovered / selector-removed states
// without a relay.
func buildServiceEndpointsReport(namespace, serviceName string, selector map[string]string, addressCount int, workloads []workloadTemplate, facts serviceAgeFacts) (string, []PlaybookActionResponseInsight) {
	var b strings.Builder
	insights := []PlaybookActionResponseInsight{}
	selStr := formatLabelMap(selector)

	fmt.Fprintf(&b, "**Live check for Service `%s/%s`**\n\n", namespace, serviceName)
	switch {
	case len(selector) == 0:
		b.WriteString("- Selector: *(none — selector was removed since the finding fired)*\n")
	default:
		fmt.Fprintf(&b, "- Selector: `%s`\n", selStr)
	}
	switch {
	case addressCount < 0:
		b.WriteString("- Endpoints: *unavailable (could not fetch)*\n")
	case addressCount == 0:
		b.WriteString("- Endpoints: **0 addresses — still not routing traffic**\n")
	default:
		fmt.Fprintf(&b, "- Endpoints: **%d address(es) — appears recovered**\n", addressCount)
	}
	if facts.hasCreatedAt {
		fmt.Fprintf(&b, "- Service created: %s (%s ago)\n", formatDay(facts.createdAt), humanAge(facts.now, facts.createdAt))
	}
	if line := facts.alertHistoryLine(); line != "" {
		fmt.Fprintf(&b, "- %s\n", line)
	}

	anyMatch := false
	if len(workloads) > 0 && len(selector) > 0 {
		fmt.Fprintf(&b, "\n**Workload pod templates in `%s`:**\n\n", namespace)
		b.WriteString("| Workload | Template labels | Matches selector |\n|---|---|---|\n")
		for _, w := range workloads {
			match := labelsContain(w.labels, selector)
			anyMatch = anyMatch || match
			mark := "✗"
			if match {
				mark = "✓"
			}
			fmt.Fprintf(&b, "| %s | `%s` | %s |\n", w.name, formatLabelMap(w.labels), mark)
		}
	}

	switch {
	case addressCount > 0:
		insights = append(insights, PlaybookActionResponseInsight{
			Severity: "Info",
			Message: fmt.Sprintf("Service %s/%s now has %d endpoint address(es) — the selector mismatch appears resolved",
				namespace, serviceName, addressCount),
		})
	case len(selector) > 0 && !anyMatch:
		insights = append(insights, PlaybookActionResponseInsight{
			Severity: "Critical",
			Message: fmt.Sprintf("Service %s/%s still has no endpoints: selector (%s) matches none of the %d workload pod template(s) in the namespace",
				namespace, serviceName, selStr, len(workloads)) + facts.summary(),
		})
	case len(selector) > 0 && anyMatch && addressCount == 0:
		insights = append(insights, PlaybookActionResponseInsight{
			Severity: "High",
			Message: fmt.Sprintf("Service %s/%s selector now matches a workload template but there are still 0 endpoint addresses — the workload may be scaled to zero or its pods not ready",
				namespace, serviceName) + facts.summary(),
		})
	}
	return b.String(), insights
}

// serviceAgeFacts say how old the problem is. The insight carries them because
// it is what the investigation reads: without them a Service broken for months
// reads the same as one that broke a minute ago.
type serviceAgeFacts struct {
	now          time.Time
	createdAt    time.Time
	hasCreatedAt bool
	alerts       *alertHistory // nil when the alert history was not read
}

// alertHistory is what the event store says about earlier alerts for the same
// problem (same fingerprint). High-priority events are kept 90 days, so the
// first one on record is a lower bound on how long the problem has existed.
type alertHistory struct {
	first time.Time // zero when no alert with this fingerprint is on record
}

func (f serviceAgeFacts) alertHistoryLine() string {
	switch {
	case f.alerts == nil:
		return ""
	case f.alerts.first.IsZero():
		return "No earlier alert for this Service on record"
	default:
		return fmt.Sprintf("First alert for this Service on record: %s (%s ago)",
			formatDay(f.alerts.first), humanAge(f.now, f.alerts.first))
	}
}

// summary renders the facts as sentences appended to the insight message,
// e.g. ". The Service was created Oct 25, 2023 (2 years ago). The first alert
// for it on record fired Jul 30, 2026 (2 months ago)."
func (f serviceAgeFacts) summary() string {
	var parts []string
	if f.hasCreatedAt {
		parts = append(parts, fmt.Sprintf("The Service was created %s (%s ago)",
			formatDay(f.createdAt), humanAge(f.now, f.createdAt)))
	}
	if f.alerts != nil {
		if f.alerts.first.IsZero() {
			parts = append(parts, "No earlier alert for it is on record")
		} else {
			parts = append(parts, fmt.Sprintf("The first alert for it on record fired %s (%s ago)",
				formatDay(f.alerts.first), humanAge(f.now, f.alerts.first)))
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return ". " + strings.Join(parts, ". ") + "."
}

// ownAlertFingerprint returns the event's fingerprint when the event is the
// service_no_endpoints alert for exactly this Service, else "".
func ownAlertFingerprint(ev PlaybookEvent, serviceName, namespace string) string {
	if ev.AggregationKey != "service_no_endpoints" {
		return ""
	}
	if name, ns := subjectServiceNamespace(ev); name != serviceName || ns != namespace {
		return ""
	}
	return ev.Fingerprint
}

// lookupAlertHistory is a variable so tests can replace the database read.
var lookupAlertHistory = func(accountId, fingerprint string) (alertHistory, error) {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return alertHistory{}, fmt.Errorf("service_endpoints_enricher: database: %w", err)
	}
	return queryAlertHistory(dbms.Db, accountId, fingerprint)
}

// queryAlertHistory finds the first alert on record with this fingerprint.
// Covered by the (fingerprint, cloud_account_id, ...) event indexes.
func queryAlertHistory(db *sqlx.DB, accountId, fingerprint string) (alertHistory, error) {
	var first sql.NullTime
	if err := db.QueryRowx(`SELECT min(created_at) FROM events WHERE fingerprint = $1 AND cloud_account_id = $2`,
		fingerprint, accountId).Scan(&first); err != nil {
		return alertHistory{}, fmt.Errorf("service_endpoints_enricher: query alert history: %w", err)
	}
	var hist alertHistory
	if first.Valid {
		hist.first = first.Time.UTC()
	}
	return hist, nil
}

// serviceCreatedAt reads metadata.creationTimestamp. Relay payloads may be
// snake-cased, so both spellings are accepted.
func serviceCreatedAt(svc map[string]any) (time.Time, bool) {
	meta := getMapField(svc, "metadata")
	for _, key := range []string{"creationTimestamp", "creation_timestamp"} {
		s, _ := meta[key].(string)
		if s == "" {
			continue
		}
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			return t.UTC(), true
		}
	}
	return time.Time{}, false
}

func formatDay(t time.Time) string {
	return t.UTC().Format("Jan 2, 2006")
}

// humanAge is a coarse "how long ago": hours, then days, months, years.
func humanAge(now, then time.Time) string {
	d := now.Sub(then)
	day := 24 * time.Hour
	switch {
	case d < time.Hour:
		return "less than an hour"
	case d < 2*day:
		return plural(int(d/time.Hour), "hour")
	case d < 60*day:
		return plural(int(d/day), "day")
	case d < 730*day:
		return plural(int(d/(30*day)), "month")
	default:
		return plural(int(d/(365*day)), "year")
	}
}

func plural(n int, unit string) string {
	if n == 1 {
		return "1 " + unit
	}
	return fmt.Sprintf("%d %ss", n, unit)
}

// serviceEndpointsMarkdownResponse is a markdown evidence payload with a
// top-level `data` string — the shape TextEnricherDynamicCard renders
// (`data.data` markdown body, `data.title` card title). The package's
// PlaybookActionResponseMarkdown serialises its body under `text`, which
// that card does not read; this response exists to hit the text_enricher
// render path without frontend changes.
type serviceEndpointsMarkdownResponse struct {
	Title          string                          `json:"title"`
	Data           string                          `json:"data"`
	AdditionalInfo map[string]any                  `json:"additional_info"`
	Insight        []PlaybookActionResponseInsight `json:"insight"`
}

func (m serviceEndpointsMarkdownResponse) GetFormatName() string { return "markdown" }
func (m serviceEndpointsMarkdownResponse) GetData() any          { return m.Data }
func (m serviceEndpointsMarkdownResponse) GetAdditionalInfo() map[string]any {
	return m.AdditionalInfo
}
func (m serviceEndpointsMarkdownResponse) GetInsights() []PlaybookActionResponseInsight {
	return m.Insight
}

// subjectServiceNamespace extracts (name, namespace) for a service-subject
// event. Returns empty strings when the subject is not a Service.
func subjectServiceNamespace(event PlaybookEvent) (string, string) {
	if !strings.EqualFold(event.SubjectType, "service") {
		return "", ""
	}
	return event.SubjectName, event.SubjectNamespace
}

// workloadTemplate is one workload's name + pod-template labels.
type workloadTemplate struct {
	name   string
	labels map[string]string
}

// serviceSelectorFromDict reads spec.selector off a (snake-cased) Service
// payload. Selector keys are user label names; SnakeKeysDeep leaves keys
// containing "." / "/" / "-" untouched, so real-world selectors survive.
func serviceSelectorFromDict(svc map[string]any) map[string]string {
	spec, _ := svc["spec"].(map[string]any)
	if spec == nil {
		return nil
	}
	raw, _ := spec["selector"].(map[string]any)
	return stringMap(raw)
}

// endpointsAddressCount counts subsets[].addresses[] on an Endpoints payload
// (ready addresses only — notReadyAddresses don't receive traffic).
func endpointsAddressCount(ep map[string]any) int {
	count := 0
	subsets, _ := ep["subsets"].([]any)
	for _, s := range subsets {
		sm, _ := s.(map[string]any)
		if sm == nil {
			continue
		}
		addrs := getArrayField(sm, "addresses")
		count += len(addrs)
	}
	return count
}

// workloadTemplatesFromList extracts (name, spec.template.metadata.labels)
// from a flat get_resource list of workloads, keeping only the given
// namespace. Agents before nudgebee-agent 0.1.26-rc-2 ignore the namespace on
// this call and return every workload in the cluster, so filter here too.
// Items that carry no namespace are kept.
func workloadTemplatesFromList(data any, namespace string) []workloadTemplate {
	items, _ := data.([]any)
	out := make([]workloadTemplate, 0, len(items))
	for _, it := range items {
		m, _ := it.(map[string]any)
		if m == nil {
			continue
		}
		meta, _ := m["metadata"].(map[string]any)
		name, _ := meta["name"].(string)
		if name == "" {
			continue
		}
		if ns, _ := meta["namespace"].(string); ns != "" && ns != namespace {
			continue
		}
		spec := getMapField(m, "spec")
		tmpl := getMapField(spec, "template")
		tmplMeta := getMapField(tmpl, "metadata")
		labels, _ := tmplMeta["labels"].(map[string]any)
		out = append(out, workloadTemplate{name: name, labels: stringMap(labels)})
	}
	return out
}

// labelsContain reports whether labels carries every pair in selector
// (K8s Service selectors are plain equality maps).
func labelsContain(labels, selector map[string]string) bool {
	if len(selector) == 0 {
		return false
	}
	for k, v := range selector {
		// Comma-ok: a missing key must not match a selector pair with an
		// empty-string value (labels[k] would zero-value to "" and compare
		// equal).
		val, ok := labels[k]
		if !ok || val != v {
			return false
		}
	}
	return true
}

// formatLabelMap renders a label map as sorted "k=v,k=v" for stable display.
func formatLabelMap(m map[string]string) string {
	pairs := make([]string, 0, len(m))
	for k, v := range m {
		pairs = append(pairs, k+"="+v)
	}
	sort.Strings(pairs)
	return strings.Join(pairs, ",")
}

func stringMap(raw map[string]any) map[string]string {
	if len(raw) == 0 {
		return nil
	}
	out := make(map[string]string, len(raw))
	for k, v := range raw {
		if s, ok := v.(string); ok {
			out[k] = s
		}
	}
	return out
}
