package integrations

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	neturl "net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"nudgebee/services/common"
	"nudgebee/services/event"
	"nudgebee/services/eventrule"
	"nudgebee/services/integrations/core"
	"nudgebee/services/security"
)

func init() {
	core.RegisterIntegration(OpenObserveWebhook{})
}

const IntegrationOpenObserveWebhook = "openobserve_webhook"

// openObserveEventType is the EventType stamped on every event produced by this
// integration. Unlike Grafana (where the alert name is the event type), a
// single constant keeps the inbox groupable by source — OpenObserve alert names
// are free-form and carry no taxonomy.
const openObserveEventType = "openobserve_alert"

// openObserveMaxLabelValueLen caps how much of any single payload field is kept
// as an investigation label. OpenObserve templates can inline whole result rows
// (`{rows}`), and those belong in evidence, not in the label table.
const openObserveMaxLabelValueLen = 512

// openObserveMaxLabels caps how many payload fields become investigation labels.
// The endpoint takes an arbitrary user-authored body, so an unbounded copy would
// let one wide template blow up every event it produces.
const openObserveMaxLabels = 200

// openObserveBulkFields are payload keys excluded from the label map because
// they carry record dumps rather than identifying context. They are still
// surfaced as evidence (see buildOpenObserveRowsEvidence).
var openObserveBulkFields = map[string]bool{
	"rows": true,
}

// openObservePlaceholderPattern matches an OpenObserve template variable that
// was never substituted — e.g. a template referencing `{k8s_pod_name}` on an
// alert whose stream has no such field. OpenObserve leaves the literal braces
// in the delivered body, so without this guard every optional field would
// become a bogus label value like "{k8s_pod_name}".
var openObservePlaceholderPattern = regexp.MustCompile(`^\{[A-Za-z0-9_.\-]*\}$`)

type OpenObserveWebhook struct{}

func (m OpenObserveWebhook) Name() string {
	return IntegrationOpenObserveWebhook
}

func (m OpenObserveWebhook) Category() core.IntegrationCategory {
	return core.IntegrationCategoryIncidentWebhook
}

func (m OpenObserveWebhook) ConfigSchema() core.IntegrationSchema {
	return core.IntegrationSchema{
		Type:     core.ToolSchemaTypeObject,
		Required: []string{},
		Properties: map[string]core.IntegrationSchemaProperty{
			core.IntegrationConfigName: {
				Type:        core.ToolSchemaTypeString,
				Description: "Name of OpenObserve Webhook",
				Default:     "",
				Priority:    100,
			},
			core.AccountId: {
				Type:             core.ToolSchemaTypeArray,
				Description:      "Select Account",
				Default:          "",
				AutoGenerateFunc: "listAccounts",
				Priority:         95,
			},
			"token": {
				Type:     core.ToolSchemaTypeString,
				Default:  "",
				Priority: 70,
			},
		},
	}
}

func (m OpenObserveWebhook) ValidateConfig(sc *security.SecurityContext, config []core.IntegrationConfigValue, accountId string) []error {
	return []error{}
}

func (m OpenObserveWebhook) MergeEventWebhooks(sc *security.RequestContext, previous core.EventIncomingWebhook, new core.EventIncomingWebhook) (core.EventIncomingWebhook, error) {
	return new, nil
}

// ---------------------------------------------------------------------------
// ProcessEventWebook — main integration entry point
// ---------------------------------------------------------------------------

// ProcessEventWebook parses an OpenObserve alert notification.
//
// OpenObserve has no fixed webhook schema: the delivered body is whatever the
// user's alert *template* (Management → Templates) renders, with `{variable}`
// placeholders substituted from the alert and the matching stream row. The
// setup dialog ships a canonical NudgeBee template, but the parser deliberately
// does not assume it — it flattens whatever JSON object arrives, drops
// unsubstituted placeholders, and resolves each field it needs through an alias
// list. A user who reuses an existing Slack/PagerDuty template still gets a
// usable event as long as the alert name is present somewhere.
func (m OpenObserveWebhook) ProcessEventWebook(sc *security.RequestContext, settings []core.IntegrationConfigValue, accountId, webhookPayloadString string) ([]core.EventIncomingWebhook, error) {
	var raw map[string]any
	if err := common.UnmarshalJson([]byte(webhookPayloadString), &raw); err != nil {
		return nil, fmt.Errorf("openobserve_webhook: failed to parse payload: %w", err)
	}
	if len(raw) == 0 {
		return nil, fmt.Errorf("openobserve_webhook: empty payload")
	}

	webhookEvent, fields, err := mapOpenObserveAlertToEvent(accountId, raw)
	if err != nil {
		return nil, err
	}

	// The mapper leaves EventUrl empty unless {alert_url} was already absolute.
	// Resolve the rest here: OpenObserve renders {alert_url} as a site-relative
	// path, which only becomes usable once joined to the instance base URL, and a
	// template omitting it entirely gets a synthesized deep link. Both need the
	// account's OpenObserve integration, hence a DB lookup outside the pure mapper.
	if webhookEvent.EventUrl == "" {
		if url := buildOpenObserveAlertURL(sc, accountId, fields["alert_url"],
			fields["org_name"], fields["stream_name"], fields["stream_type"]); url != "" {
			webhookEvent.EventUrl = url
			webhookEvent.Investigation.SourceUrl = url
		}
	}

	// Upsert an event rule so the alert shows up in rule management. Skipped for
	// resolutions, which must not resurrect a rule the user disabled.
	if webhookEvent.Investigation.Status != event.EventStatusResolved {
		createOpenObserveEventRule(sc, accountId, webhookEvent.Investigation.RuleName,
			webhookEvent.EventTitle, webhookEvent.EventDescription,
			eventRuleSeverity(webhookEvent.Investigation.Severity), fields)
	}

	return []core.EventIncomingWebhook{*webhookEvent}, nil
}

// mapOpenObserveAlertToEvent is the pure alert → event mapping. It is the single
// source of truth for how an OpenObserve notification becomes a NudgeBee event
// (fingerprint, subject, severity, timestamps, evidence) and performs no I/O, so
// both the webhook path and tests exercise identical logic. The returned field
// map is the flattened payload, for callers that need to enrich further.
func mapOpenObserveAlertToEvent(accountId string, raw map[string]any) (*core.EventIncomingWebhook, map[string]string, error) {
	fields := map[string]string{}
	flattenOpenObservePayload("", raw, fields)

	alertName := firstOpenObserveValue(fields, "alert_name", "alertname", "alert", "name", "title")
	if alertName == "" {
		// Without an alert name there is nothing stable to key the event on, and
		// the resulting inbox row would be unactionable. Surfacing this as an
		// error routes the body to event_incoming_webhooks as unprocessed so the
		// user can inspect what their template actually sent.
		return nil, fields, fmt.Errorf("openobserve_webhook: missing alert name in payload (expected one of alert_name/alertname/name/title)")
	}

	orgName := firstOpenObserveValue(fields, "org_name", "organization", "org_id", "org")
	streamName := firstOpenObserveValue(fields, "stream_name", "stream")
	streamType := firstOpenObserveValue(fields, "stream_type")

	status := mapOpenObserveStatus(firstOpenObserveValue(fields, "status", "alert_status", "event_status", "state"))
	severityRaw := firstOpenObserveValue(fields, "severity", "alert_severity", "level", "priority")
	if severityRaw == "" {
		// OpenObserve's own sample templates use {alert_type} in the "Severity"
		// slot even though it renders the trigger mode (scheduled/realtime).
		// Honour it only when it actually spells a severity word.
		if candidate := fields["alert_type"]; isOpenObserveSeverityWord(candidate) {
			severityRaw = candidate
		}
	}
	priority := mapOpenObserveSeverity(severityRaw)

	createdAt := parseOpenObserveTime(firstOpenObserveValue(fields,
		"alert_start_time", "alert_trigger_time", "start_time", "timestamp", "_timestamp"))
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	endsAt := parseOpenObserveTime(firstOpenObserveValue(fields, "alert_end_time", "end_time"))

	subjectKind, subjectName, candidates := extractOpenObserveSubject(fields)

	// A scheduled alert spanning several namespaces renders k8s_namespace_name as
	// a comma-joined aggregate. Only a single value names the event's namespace;
	// an aggregate is kept as a label (and as tags) but must not masquerade as one
	// namespace, or namespace filtering and workload lookup both miss.
	namespaceRaw := firstOpenObserveValue(fields,
		"k8s_namespace_name", "kubernetes_namespace_name", "namespace", "namespace_name")
	namespaces := splitOpenObserveMultiValue(namespaceRaw)
	namespace := singleOpenObserveValue(namespaceRaw)

	labels := buildOpenObserveLabels(fields)
	labels["alertname"] = alertName
	if severityRaw != "" {
		labels["severity"] = severityRaw
	}
	if orgName != "" {
		labels["org_name"] = orgName
	}
	if streamName != "" {
		labels["stream_name"] = streamName
	}
	if namespace != "" {
		labels["namespace"] = namespace
	} else if len(namespaces) > 1 {
		// Ambiguous by design — surface the full set so the investigation still
		// shows every namespace the alert spanned.
		labels["namespaces"] = strings.Join(namespaces, ",")
	}
	if subjectName != "" && labels["service"] == "" {
		labels["service"] = subjectName
	}
	if len(candidates) > 0 {
		// Hand the full candidate list to core.MatchWorkloadAndEnrich so it can
		// fall back through pod → workload → service when EventSubjectName misses.
		labels["nb_workload_candidates"] = strings.Join(candidates, ",")
	}

	// Write the alias-resolved identity back under canonical keys so callers
	// (and the event-rule upsert) read one spelling regardless of which alias
	// the user's template happened to use.
	if orgName != "" {
		fields["org_name"] = orgName
	}
	if streamName != "" {
		fields["stream_name"] = streamName
	}
	if streamType != "" {
		fields["stream_type"] = streamType
	}

	// Invariant: EventUrl is absolute or empty, never a bare path.
	//
	// OpenObserve renders {alert_url} as a site-relative path
	// ("/web/short/3ae9dca2…?org_identifier=default"), which a client would
	// resolve against NudgeBee's own origin and 404 on. The raw value stays in
	// the labels either way; ProcessEventWebook joins it onto the account's
	// OpenObserve base URL, and falls back to a synthesized deep link when the
	// template carried no link at all.
	eventURL := firstOpenObserveValue(fields, "alert_url", "url", "link", "source_url")
	if !isAbsoluteOpenObserveURL(eventURL) {
		eventURL = ""
	}

	// The fingerprint must be stable across re-fires of the same alert so the
	// event pipeline dedupes them (and so a later resolution can find the event
	// it closes), and distinct per affected workload so two pods breaching the
	// same rule do not collapse into one event. OpenObserve sends no identifier
	// of its own, so we derive one.
	//
	// Only single-valued namespace/subject reach the hash. Aggregated stream
	// fields are deliberately excluded: their value is the set of rows matched in
	// the current evaluation window, which changes between fires — feeding one in
	// yields a new fingerprint every time, producing a fresh event per fire and
	// leaving every one of them permanently open.
	fingerprint := openObserveFingerprint(orgName, streamName, alertName, namespace, subjectName)

	title := alertName
	if streamName != "" {
		title = fmt.Sprintf("%s (stream: %s)", alertName, streamName)
	}
	description := buildOpenObserveDescription(alertName, orgName, streamName, fields)

	evidences := []event.EventEvidence{buildOpenObserveAlertEvidence(alertName, orgName, streamName, streamType, fields)}
	if rowsEvidence := buildOpenObserveRowsEvidence(raw); rowsEvidence != nil {
		evidences = append(evidences, *rowsEvidence)
	}

	tags := buildOpenObserveTags(orgName, streamName, namespaces, fields)

	webhookEvent := core.EventIncomingWebhook{
		WebhookId:             fingerprint,
		EventType:             openObserveEventType,
		EventId:               fingerprint,
		EventUrl:              eventURL,
		EventStatus:           string(status),
		EventPriority:         string(priority),
		EventCreatedAt:        createdAt,
		EventEndsAt:           endsAt,
		EventTitle:            title,
		EventDescription:      description,
		EventTags:             tags,
		EventSubjectKind:      subjectKind,
		EventSubjectName:      subjectName,
		EventSubjectNamespace: namespace,
		AccountId:             accountId,
		Investigation: core.EventIncomingWebhookInvestigation{
			RuleName:    alertName,
			RuleId:      alertName,
			RuleType:    IntegrationOpenObserveWebhook,
			Fingerprint: fingerprint,
			Status:      status,
			Severity:    priority,
			SourceUrl:   eventURL,
			Labels:      labels,
			Evidences:   evidences,
		},
	}

	return &webhookEvent, fields, nil
}

// eventRuleSeverity maps an event priority onto the only two values the
// event_rules.severity column accepts (FK → event_rule_severity: "critical",
// "warning").
//
// The raw payload severity must never be forwarded here: OpenObserve renders
// whatever the stream carries — the k8s_events stream, for instance, sends a
// numeric `severity: "0"` — and anything outside those two values fails the
// foreign key, so the rule upsert is lost and the alert never appears in rule
// management.
func eventRuleSeverity(priority event.EventPriority) string {
	if priority == event.EventPriorityHigh {
		return "critical"
	}
	return "warning"
}

// createOpenObserveEventRule fire-and-forgets the event-rule upsert on a
// detached context. The inbound request context is cancelled as soon as the
// webhook handler responds, so reusing it would abort the insert mid-flight.
func createOpenObserveEventRule(sc *security.RequestContext, accountId, alertName, title, description, severityLabel string, fields map[string]string) {
	expr := buildOpenObserveCondition(fields)
	// Read everything off the request context up front: the handler has already
	// responded by the time this goroutine runs, so nothing derived from sc
	// should be resolved inside it. Registering recover() first also means a
	// panic anywhere in the body — including context setup — is logged rather
	// than taking the process down.
	secCtx := sc.GetSecurityContext()
	logger := sc.GetLogger()
	tracer := sc.GetTracer()
	meter := sc.GetMeter()
	go func() {
		defer func() {
			if r := recover(); r != nil {
				logger.Error("openobserve_webhook: panic in CreateEventRule goroutine", "panic", r)
			}
		}()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		detachedSc := security.NewRequestContext(ctx, secCtx, logger, tracer, meter)
		eventReq := eventrule.EventConfig{
			Annotations: struct {
				Description string `json:"description"`
				Summary     string `json:"summary"`
				Runbook     string `json:"runbook"`
			}{
				Description: description,
				Summary:     title,
				Runbook:     "",
			},
			Expr: expr,
			Labels: struct {
				Severity string `json:"severity"`
			}{Severity: severityLabel},
			Alert:         alertName,
			Duration:      "0",
			AccountID:     accountId,
			Source:        IntegrationOpenObserveWebhook,
			Category:      "alert",
			Severity:      severityLabel,
			Enabled:       true,
			TriggerParams: []map[string]any{},
			ActionParams:  []map[string]any{},
		}
		if _, err := eventrule.CreateEventRule(detachedSc, eventReq); err != nil {
			detachedSc.GetLogger().Error("openobserve_webhook: CreateEventRule failed", "error", err, "alert", alertName)
		}
	}()
}

// ---------------------------------------------------------------------------
// Payload flattening
// ---------------------------------------------------------------------------

// flattenOpenObservePayload walks an arbitrary decoded JSON body and collects
// every scalar leaf into out, keyed by its underscore-joined path. Values that
// are unsubstituted template placeholders or JSON nulls are dropped so they
// never reach labels. Nested arrays of scalars are joined with commas; arrays of
// objects are indexed (`rows_0_message`).
func flattenOpenObservePayload(prefix string, value any, out map[string]string) {
	switch v := value.(type) {
	case map[string]any:
		for k, nested := range v {
			flattenOpenObservePayload(joinOpenObserveKey(prefix, normalizeOpenObserveKey(k)), nested, out)
		}
	case []any:
		scalars := make([]string, 0, len(v))
		allScalar := true
		for _, item := range v {
			switch item.(type) {
			case map[string]any, []any:
				allScalar = false
			default:
				if s := cleanOpenObserveValue(scalarToString(item)); s != "" {
					scalars = append(scalars, s)
				}
			}
		}
		if allScalar {
			if len(scalars) > 0 && prefix != "" {
				out[prefix] = strings.Join(scalars, ",")
			}
			return
		}
		for i, item := range v {
			flattenOpenObservePayload(joinOpenObserveKey(prefix, strconv.Itoa(i)), item, out)
		}
	default:
		if prefix == "" {
			return
		}
		if s := cleanOpenObserveValue(scalarToString(v)); s != "" {
			out[prefix] = s
		}
	}
}

func joinOpenObserveKey(prefix, key string) string {
	if prefix == "" {
		return key
	}
	return prefix + "_" + key
}

// normalizeOpenObserveKey lowercases a payload key and converts dots, dashes and
// spaces to underscores, so `k8s.pod.name`, `k8s-pod-name` and `k8s_pod_name`
// all resolve through the same alias lookup.
func normalizeOpenObserveKey(key string) string {
	key = strings.TrimSpace(strings.ToLower(key))
	replacer := strings.NewReplacer(".", "_", "-", "_", " ", "_", "/", "_")
	return replacer.Replace(key)
}

func scalarToString(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case bool:
		return strconv.FormatBool(t)
	case float64:
		// Render integral floats without the ".0" JSON decoding introduces, so
		// epoch timestamps survive the round trip intact.
		if t == float64(int64(t)) {
			return strconv.FormatInt(int64(t), 10)
		}
		return strconv.FormatFloat(t, 'f', -1, 64)
	case json.Number:
		return t.String()
	default:
		return fmt.Sprintf("%v", t)
	}
}

// cleanOpenObserveValue trims a rendered template value and rejects the ones
// that carry no information: unsubstituted `{placeholder}` variables and the
// string spellings of null that OpenObserve emits for absent stream fields.
func cleanOpenObserveValue(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if openObservePlaceholderPattern.MatchString(s) {
		return ""
	}
	switch strings.ToLower(s) {
	case "null", "undefined", "<nil>", "none":
		return ""
	}
	return s
}

// firstOpenObserveValue returns the first non-empty value among the given keys.
func firstOpenObserveValue(fields map[string]string, keys ...string) string {
	for _, k := range keys {
		if v := fields[k]; v != "" {
			return v
		}
	}
	return ""
}

// splitOpenObserveMultiValue splits a rendered stream-field variable into its
// individual values.
//
// OpenObserve documents that "all of the stream fields are variables", but a
// scheduled alert matches many rows, so it renders each stream field as the
// comma-joined set of distinct values across every matched row. A field named
// `k8s_namespace_name` therefore arrives as
// "traefik, nudgebee, elasticsearch, kube-system" whenever the alert spans more
// than one namespace — an aggregate, not an identity.
//
// Callers use the length to decide: exactly one value identifies the subject;
// more than one means the field cannot name a single subject and must not be
// treated as one (see mapOpenObserveAlertToEvent's fingerprint note).
func splitOpenObserveMultiValue(v string) []string {
	if v == "" {
		return nil
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// singleOpenObserveValue returns the value only when the field carries exactly
// one, and "" when it is empty or an aggregate of several.
func singleOpenObserveValue(v string) string {
	if values := splitOpenObserveMultiValue(v); len(values) == 1 {
		return values[0]
	}
	return ""
}

// buildOpenObserveLabels copies the flattened payload into the investigation
// label map, skipping record dumps and truncating oversized values.
//
// The endpoint accepts an arbitrary user-authored body, so both the per-value
// size and the label count are bounded: without a cap, one template that inlines
// a wide record would persist thousands of labels onto every event it fires and
// render an unusable alert-labels table. Keys are sorted before truncation so
// the retained subset is deterministic across deliveries; the identifying labels
// (alert name, severity, stream, subject) are re-applied by the caller
// afterwards and therefore survive regardless.
func buildOpenObserveLabels(fields map[string]string) map[string]string {
	keys := make([]string, 0, len(fields))
	for k := range fields {
		if openObserveBulkFields[k] || strings.HasPrefix(k, "rows_") {
			continue
		}
		// `nb_` is NudgeBee's reserved label namespace and carries control
		// semantics downstream (nb_skip_workload_match disables subject matching,
		// nb_workload_candidates steers it). The payload is user-authored, so a
		// template that happens to render one of those keys must not be able to
		// steer core enrichment — drop them and let this parser set its own.
		if strings.HasPrefix(k, "nb_") {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)

	truncated := false
	if len(keys) > openObserveMaxLabels {
		keys = keys[:openObserveMaxLabels]
		truncated = true
	}

	labels := make(map[string]string, len(keys)+8)
	for _, k := range keys {
		labels[k] = truncateOpenObserveLabelValue(fields[k])
	}
	if truncated {
		labels["nb_labels_truncated"] = "true"
	}
	return labels
}

// truncateOpenObserveLabelValue caps a label value at openObserveMaxLabelValueLen
// runes. Ranging over the string yields the byte offset of each rune boundary,
// so the cut never splits a multi-byte character and the result is still valid
// UTF-8 when it reaches Postgres — without allocating a []rune copy of a value
// that is usually already short enough to return untouched.
func truncateOpenObserveLabelValue(v string) string {
	runeCount := 0
	for i := range v {
		if runeCount == openObserveMaxLabelValueLen {
			return v[:i] + "…"
		}
		runeCount++
	}
	return v
}

// ---------------------------------------------------------------------------
// Field mapping
// ---------------------------------------------------------------------------

// openObserveSubjectAliases maps a NudgeBee subject kind to the payload keys
// that can carry its name, in match priority order. Pod wins over workload
// because a pod-scoped alert should investigate that pod; core enrichment walks
// up to the owning workload on its own.
var openObserveSubjectAliases = []struct {
	kind    string
	aliases []string
}{
	{"pod", []string{"k8s_pod_name", "kubernetes_pod_name", "pod_name", "pod"}},
	{"deployment", []string{"k8s_deployment_name", "deployment_name", "deployment"}},
	{"statefulset", []string{"k8s_statefulset_name", "statefulset_name", "statefulset"}},
	{"daemonset", []string{"k8s_daemonset_name", "daemonset_name", "daemonset"}},
	{"replicaset", []string{"k8s_replicaset_name", "replicaset_name", "replicaset"}},
	{"cronjob", []string{"k8s_cronjob_name", "cronjob_name", "cronjob"}},
	{"job", []string{"k8s_job_name", "job_name", "job"}},
	{"node", []string{"k8s_node_name", "node_name", "node"}},
}

// extractOpenObserveSubject resolves the alert's K8s subject and the ordered
// candidate list handed to core.MatchWorkloadAndEnrich. The service name is a
// candidate but never the subject kind — OpenTelemetry's service.name is not a
// Kubernetes object, so claiming a kind for it would produce a wrong subject
// type on the event.
func extractOpenObserveSubject(fields map[string]string) (kind, name string, candidates []string) {
	seen := map[string]bool{}
	addCandidate := func(v string) {
		if v == "" || seen[v] {
			return
		}
		seen[v] = true
		candidates = append(candidates, v)
	}

	// A workload field can arrive aggregated across every matched row
	// ("api-0, api-1, worker-2"). Each value is a legitimate match candidate, but
	// only an unambiguous single value may name the subject — picking one
	// arbitrarily would both mislabel the event and destabilise the fingerprint.
	for _, entry := range openObserveSubjectAliases {
		values := splitOpenObserveMultiValue(firstOpenObserveValue(fields, entry.aliases...))
		if len(values) == 0 {
			continue
		}
		if name == "" && len(values) == 1 {
			kind, name = entry.kind, values[0]
		}
		for _, v := range values {
			addCandidate(v)
		}
	}

	serviceValues := splitOpenObserveMultiValue(firstOpenObserveValue(fields,
		"service_name", "k8s_container_name", "container_name", "app", "app_name", "service"))
	if name == "" && len(serviceValues) == 1 {
		name = serviceValues[0]
	}
	for _, v := range serviceValues {
		addCandidate(v)
	}

	return kind, name, candidates
}

// mapOpenObserveStatus maps an optional status field onto an EventStatus.
//
// OpenObserve has no resolve callback: a scheduled alert simply stops firing,
// and nothing is delivered when the condition clears. Every delivery is
// therefore treated as firing unless the user's template explicitly renders a
// resolution status.
func mapOpenObserveStatus(status string) event.EventStatus {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "resolved", "ok", "recovered", "cleared", "normal":
		return event.EventStatusResolved
	case "closed":
		return event.EventStatusClosed
	default:
		return event.EventStatusFiring
	}
}

// isOpenObserveSeverityWord reports whether a value spells a severity rather
// than something else (e.g. OpenObserve's "scheduled" / "realtime" alert types).
func isOpenObserveSeverityWord(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "critical", "fatal", "emergency", "error", "high", "major", "p1", "p2",
		"warning", "warn", "medium", "moderate", "p3",
		"low", "minor", "p4", "p5",
		"info", "informational", "debug":
		return true
	}
	return false
}

// mapOpenObserveSeverity maps a severity string to an EventPriority. An alert
// that fires without any severity hint defaults to medium: OpenObserve alerts
// are user-authored threshold breaches, so silently filing them at the bottom
// of the inbox would hide real incidents.
func mapOpenObserveSeverity(severity string) event.EventPriority {
	switch strings.ToLower(strings.TrimSpace(severity)) {
	case "critical", "fatal", "emergency", "error", "high", "major", "p1", "p2":
		return event.EventPriorityHigh
	case "warning", "warn", "medium", "moderate", "p3":
		return event.EventPriorityMedium
	case "low", "minor", "p4", "p5":
		return event.EventPriorityLow
	case "info", "informational", "notice":
		return event.EventPriorityInfo
	case "debug", "trace":
		return event.EventPriorityDebug
	default:
		return event.EventPriorityMedium
	}
}

// parseOpenObserveTime accepts every timestamp spelling an OpenObserve template
// can render: RFC 3339 (with or without offset/fraction), the space-separated
// SQL-ish form, and bare epoch integers. OpenObserve stores `_timestamp` in
// microseconds, but templates may surface seconds, milliseconds or nanoseconds
// depending on the field, so the unit is inferred from magnitude.
// Returns the zero time when the input is empty or unrecognised.
func parseOpenObserveTime(raw string) time.Time {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}
	}

	if n, err := strconv.ParseInt(raw, 10, 64); err == nil {
		return epochToTime(n)
	}

	for _, layout := range []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05.999999999 -0700 MST",
		"2006-01-02 15:04:05 -0700",
		"2006-01-02 15:04:05",
		"2006-01-02T15:04:05Z0700",
	} {
		if t, err := time.Parse(layout, raw); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

// epochToTime infers the unit of a bare epoch integer from its magnitude.
// Boundaries are placed at year ~2001 in each unit, which cleanly separates
// seconds / milliseconds / microseconds / nanoseconds for any timestamp this
// decade. Non-positive input yields the zero time.
func epochToTime(n int64) time.Time {
	switch {
	case n <= 0:
		return time.Time{}
	case n >= 1e18: // nanoseconds
		return time.Unix(0, n).UTC()
	case n >= 1e15: // microseconds
		return time.UnixMicro(n).UTC()
	case n >= 1e12: // milliseconds
		return time.UnixMilli(n).UTC()
	default: // seconds
		return time.Unix(n, 0).UTC()
	}
}

// openObserveFingerprint derives a stable, collision-resistant event key from
// the alert's identity. Empty parts are retained as empty segments so that
// "alert X, no namespace" and "alert X, namespace X" cannot hash alike.
func openObserveFingerprint(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:16])
}

// ---------------------------------------------------------------------------
// Presentation helpers
// ---------------------------------------------------------------------------

// buildOpenObserveCondition renders the alert's trigger condition (operator,
// threshold, evaluation period) as a human-readable expression for the event
// rule. Returns an empty string when the template carried no condition fields.
func buildOpenObserveCondition(fields map[string]string) string {
	operator := fields["alert_operator"]
	threshold := fields["alert_threshold"]
	if operator == "" && threshold == "" {
		return ""
	}
	expr := strings.TrimSpace(operator + " " + threshold)
	if period := fields["alert_period"]; period != "" {
		expr = fmt.Sprintf("%s over %s", expr, period)
	}
	return expr
}

func buildOpenObserveDescription(alertName, orgName, streamName string, fields map[string]string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "OpenObserve alert %q fired", alertName)
	if streamName != "" {
		fmt.Fprintf(&b, " on stream %q", streamName)
	}
	if orgName != "" {
		fmt.Fprintf(&b, " in organization %q", orgName)
	}
	b.WriteString(".")
	if condition := buildOpenObserveCondition(fields); condition != "" {
		fmt.Fprintf(&b, " Condition: %s.", condition)
	}
	if count := fields["alert_count"]; count != "" {
		fmt.Fprintf(&b, " Matching records: %s.", count)
	}
	if agg := fields["alert_agg_value"]; agg != "" {
		fmt.Fprintf(&b, " Aggregated value: %s.", agg)
	}
	return b.String()
}

// buildOpenObserveAlertEvidence renders the alert's identity and trigger
// condition as a table so the investigation shows what fired without the reader
// having to scan the raw label dump.
func buildOpenObserveAlertEvidence(alertName, orgName, streamName, streamType string, fields map[string]string) event.EventEvidence {
	rows := [][]any{{"Alert", alertName}}
	appendRow := func(label, value string) {
		if value != "" {
			rows = append(rows, []any{label, value})
		}
	}
	appendRow("Organization", orgName)
	appendRow("Stream", streamName)
	appendRow("Stream type", streamType)
	appendRow("Alert type", fields["alert_type"])
	appendRow("Condition", buildOpenObserveCondition(fields))
	appendRow("Matching records", fields["alert_count"])
	appendRow("Aggregated value", fields["alert_agg_value"])
	appendRow("Evaluation period", fields["alert_period"])
	appendRow("Start time", fields["alert_start_time"])
	appendRow("End time", fields["alert_end_time"])

	return event.EventEvidence{
		Type:    "table",
		Insight: []event.EventEvidenceInsight{},
		Data: map[string]any{
			"column_renderers": map[string]any{},
			"headers":          []string{"field", "value"},
			"rows":             rows,
			"table_name":       "*OpenObserve alert*",
		},
		AdditionalInfo: map[string]any{
			"action_name":            "openobserve_alert",
			"actual_action_name":     "openobserve_alert",
			"action_title":           "OpenObserve Alert",
			"conditional_expression": "",
		},
	}
}

// buildOpenObserveRowsEvidence surfaces the `{rows}` variable — the sample of
// matching stream records OpenObserve renders from the alert's row template —
// as its own evidence block. Returns nil when the template omitted it.
func buildOpenObserveRowsEvidence(raw map[string]any) *event.EventEvidence {
	value, ok := raw["rows"]
	if !ok {
		return nil
	}

	switch v := value.(type) {
	case string:
		if cleanOpenObserveValue(v) == "" {
			return nil
		}
		return &event.EventEvidence{
			Type:    "markdown",
			Insight: []event.EventEvidenceInsight{},
			Data: map[string]any{
				"name": "OpenObserve Matching Records",
				"data": v,
			},
			AdditionalInfo: openObserveRowsAdditionalInfo(),
		}
	case []any:
		if len(v) == 0 {
			return nil
		}
		return &event.EventEvidence{
			Type: "json",
			Insight: []event.EventEvidenceInsight{{
				Message:  fmt.Sprintf("Alert matched %d sample record(s)", len(v)),
				Severity: "info",
			}},
			Data: map[string]any{
				"name": "OpenObserve Matching Records",
				"data": v,
			},
			AdditionalInfo: openObserveRowsAdditionalInfo(),
		}
	default:
		return nil
	}
}

func openObserveRowsAdditionalInfo() map[string]any {
	return map[string]any{
		"action_name":            "openobserve_rows",
		"actual_action_name":     "openobserve_rows",
		"action_title":           "OpenObserve Matching Records",
		"conditional_expression": "",
	}
}

// buildOpenObserveTags collects the coarse routing dimensions of the alert.
// Every namespace the alert spanned is tagged, so an aggregated alert stays
// filterable even though no single namespace can own the event. Sorted so the
// persisted tag list is deterministic across deliveries.
func buildOpenObserveTags(orgName, streamName string, namespaces []string, fields map[string]string) []string {
	seen := map[string]bool{}
	var tags []string
	values := []string{orgName, streamName}
	values = append(values, namespaces...)
	values = append(values,
		// k8s_cluster is the spelling OpenObserve's k8s_events stream uses; the
		// other three cover OTel/Prometheus-style collectors.
		firstOpenObserveValue(fields, "k8s_cluster_name", "k8s_cluster", "cluster_name", "cluster"),
		fields["environment"],
		fields["env"],
	)
	for _, v := range values {
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		tags = append(tags, v)
	}
	sort.Strings(tags)
	return tags
}

// isAbsoluteOpenObserveURL reports whether a rendered link can stand on its own.
// Anything else — most importantly OpenObserve's site-relative "/web/short/…"
// form — needs the instance base URL prepended before it is usable.
func isAbsoluteOpenObserveURL(raw string) bool {
	raw = strings.TrimSpace(raw)
	return strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://")
}

// absoluteOpenObserveURL joins a site-relative path onto the instance base URL.
// Returns "" when either side is missing, so callers never emit a broken link.
func absoluteOpenObserveURL(baseURL, path string) string {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	path = strings.TrimSpace(path)
	if baseURL == "" || path == "" {
		return ""
	}
	if isAbsoluteOpenObserveURL(path) {
		return path
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return baseURL + path
}

// resolveOpenObserveEventURL turns whatever the template rendered into a usable
// absolute link, preferring the alert's own link over a synthesized one:
//
//  1. absolute {alert_url}          → used as-is
//  2. relative {alert_url}          → joined onto the instance base URL
//  3. no {alert_url}, base known    → synthesized alerts-list deep link
//  4. nothing usable                → "" (better than a link that 404s)
func resolveOpenObserveEventURL(rawAlertURL, baseURL, orgName, streamName, streamType, cfgOrgID string) string {
	if isAbsoluteOpenObserveURL(rawAlertURL) {
		return strings.TrimSpace(rawAlertURL)
	}
	if rawAlertURL != "" {
		if abs := absoluteOpenObserveURL(baseURL, rawAlertURL); abs != "" {
			return abs
		}
		return ""
	}
	return synthesizeOpenObserveAlertsURL(baseURL, orgName, streamName, streamType, cfgOrgID)
}

// synthesizeOpenObserveAlertsURL builds an alerts-list deep link from the
// instance base URL. Returns "" when the base URL is unknown.
func synthesizeOpenObserveAlertsURL(baseURL, orgName, streamName, streamType, cfgOrgID string) string {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		return ""
	}
	org := orgName
	if org == "" {
		org = cfgOrgID
	}
	if org == "" {
		return baseURL + "/web/alerts"
	}
	url := fmt.Sprintf("%s/web/alerts?org_identifier=%s", baseURL, neturl.QueryEscape(org))
	if streamName != "" {
		url += "&stream_name=" + neturl.QueryEscape(streamName)
	}
	if streamType != "" {
		url += "&stream_type=" + neturl.QueryEscape(streamType)
	}
	return url
}

// buildOpenObserveAlertURL resolves the event's link against the account's
// OpenObserve observability integration, which it reads purely for the instance
// base URL. Degrades to an empty string when no such integration is configured.
func buildOpenObserveAlertURL(sc *security.RequestContext, accountId, rawAlertURL, orgName, streamName, streamType string) string {
	if isAbsoluteOpenObserveURL(rawAlertURL) {
		return strings.TrimSpace(rawAlertURL)
	}
	if accountId == "" {
		return ""
	}
	cfg, err := GetOpenObserveConfigs(sc, accountId)
	if err != nil || cfg.URL == "" {
		sc.GetLogger().Debug("openobserve_webhook: no OpenObserve integration for alert URL, leaving event URL empty",
			"account_id", accountId, "error", err)
		return ""
	}
	return resolveOpenObserveEventURL(rawAlertURL, cfg.URL, orgName, streamName, streamType, cfg.OrgID)
}
