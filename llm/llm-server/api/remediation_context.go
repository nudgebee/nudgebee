package api

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"nudgebee/llm/common"
	"nudgebee/llm/security"
	"nudgebee/llm/tools"
)

// The investigation is prose; incident age, current configuration and past attempts are rows we
// already store. Stated here as facts. Best-effort: a failed lookup drops its section, never the run.

const (
	// factsBudget bounds the whole block. The investigation is the primary evidence and these facts
	// must not crowd it out of the context window.
	factsBudget = 4000
	// A resource's stored config is the provider's full description - dozens of fields, some of them
	// whole policy documents. Only short scalars are worth stating, and only a handful of them.
	maxConfigFields  = 16
	maxConfigValue   = 120
	maxLinkedConfigs = 2
	maxAttempts      = 5
	maxContainers    = 6
	maxEnvNames      = 12
	maxConditions    = 4
)

// configSectionHeading names the section that gets trimmed first when the block is over budget.
const configSectionHeading = "## Current configuration (from inventory, may lag the live resource)"

// arnPattern finds identifiers of other resources inside a resource's own configuration - the
// dead-letter queue named by a queue's redrive policy, for example. Those neighbours are part of
// the blast radius of any command against the subject, so their config is worth stating too.
var arnPattern = regexp.MustCompile(`arn:aws:[a-z0-9-]+:[a-z0-9-]*:\d{12}:[A-Za-z0-9/:_.+=@-]+`)

type eventFacts struct {
	Title          string
	AggregationKey string
	SubjectType    string
	SubjectName    string
	SubjectNS      string
	Region         string
	Fingerprint    string
	StartsAt       time.Time
	EndsAt         time.Time
	Labels         map[string]string
	Owner          string // the workload a pod belongs to; commands change it, not the pod
	OwnerKind      string
}

// remediationNow is the moment the facts describe. Production reads the clock; the audit replay sets
// it to an incident's own time, so firings, recoveries and attempts that came later stay out of it.
var remediationNow = func() time.Time { return time.Now().UTC() }

// buildRemediationFacts renders the structured facts as markdown sections appended to the
// investigation. Returns "" when nothing could be read.
// Facts only; reasoning from them is the prompt's job, so it generalises past any rule.
func buildRemediationFacts(ctx *security.RequestContext, accountId, eventId string) string {
	if strings.TrimSpace(eventId) == "" || strings.TrimSpace(accountId) == "" {
		return ""
	}
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return ""
	}

	ev := loadEventFacts(ctx, dbms, accountId, eventId)
	if ev == nil {
		return ""
	}

	now := remediationNow()
	config, ownership := configSection(ctx, dbms, accountId, ev)
	sections := []string{
		incidentSection(ctx, dbms, accountId, eventId, ev, now),
		targetSection(ctx, dbms, accountId, ev),
		conclusionSection(loadChangePlan(ctx, dbms, accountId, eventId)),
		ownership,
		config,
		attemptsSection(ctx, dbms, accountId, eventId, ev.SubjectNS, now),
	}
	return withinBudget(nonEmpty(sections))
}

// withinBudget fits the sections to factsBudget by trimming the configuration section, which is the
// elastic one. Cutting the joined tail instead dropped what was already attempted — the section that
// stops a command being proposed again after it failed.
func withinBudget(sections []string) string {
	out := strings.Join(sections, "\n\n")
	if len(out) <= factsBudget {
		return out
	}

	const trimmed = "\n  [configuration truncated]"
	for i, section := range sections {
		if !strings.HasPrefix(section, configSectionHeading) {
			continue
		}
		keep := len(section) - (len(out) - factsBudget) - len(trimmed)
		if keep < len(configSectionHeading) {
			keep = len(configSectionHeading)
		}
		sections[i] = common.TruncateHead(section, keep) + trimmed
		out = strings.Join(sections, "\n\n")
		break
	}
	if len(out) > factsBudget {
		// Nothing elastic left: the remaining sections are already the short, load-bearing ones.
		out = common.TruncateHead(out, factsBudget) + "\n[facts truncated]"
	}
	return out
}

func nonEmpty(in []string) []string {
	out := in[:0]
	for _, s := range in {
		if strings.TrimSpace(s) != "" {
			out = append(out, s)
		}
	}
	return out
}

// Selected columns only: the events row also carries `evidences`, which runs to megabytes and has
// caused out-of-memory failures when selected wholesale elsewhere in this package.
func loadEventFacts(ctx *security.RequestContext, dbms *common.DatabaseManager, accountId, eventId string) *eventFacts {
	var (
		ev       eventFacts
		startsAt sql.NullTime
		endsAt   sql.NullTime
		labelsIn []byte
	)
	row := dbms.Db.QueryRowx(
		`SELECT COALESCE(title,''), COALESCE(aggregation_key,''), COALESCE(subject_type,''),
		        COALESCE(subject_name,''), COALESCE(subject_namespace,''), COALESCE(subject_node,''),
		        COALESCE(fingerprint,''), starts_at, ends_at, COALESCE(labels,'{}'::jsonb),
		        COALESCE(subject_owner,''), COALESCE(subject_owner_kind,'')
		   FROM events WHERE id = $1 AND cloud_account_id = $2`, eventId, accountId)
	if err := row.Scan(&ev.Title, &ev.AggregationKey, &ev.SubjectType, &ev.SubjectName, &ev.SubjectNS,
		&ev.Region, &ev.Fingerprint, &startsAt, &endsAt, &labelsIn, &ev.Owner, &ev.OwnerKind); err != nil {
		ctx.GetLogger().Warn("remediation facts: event lookup failed", "error", err)
		return nil
	}
	// Nullable, and a rejected scan would drop every fact for this incident, not just its age.
	ev.StartsAt, ev.EndsAt = startsAt.Time, endsAt.Time
	ev.Labels = decodeLabels(labelsIn)
	return &ev
}

// Labels are written by several ingest paths with mixed value types, so anything that is not a
// string is rendered rather than dropped - a number threshold is still a fact.
func decodeLabels(in []byte) map[string]string {
	raw := map[string]any{}
	if err := json.Unmarshal(in, &raw); err != nil {
		return nil
	}
	out := make(map[string]string, len(raw))
	for k, v := range raw {
		if s := scalarString(v); s != "" {
			out[k] = s
		}
	}
	return out
}

func scalarString(v any) string {
	switch t := v.(type) {
	case string:
		return strings.TrimSpace(t)
	case float64:
		return strings.TrimSuffix(strings.TrimRight(fmt.Sprintf("%.4f", t), "0"), ".")
	case bool:
		return fmt.Sprintf("%t", t)
	default:
		return ""
	}
}

// incidentSection states when the incident started and how long ago that was, and says outright
// that the stored status is not evidence of recovery: cloud alarm events are written with equal
// start and end times, so they are stored CLOSED/RESOLVED at ingest while the alarm still fires.
func incidentSection(ctx *security.RequestContext, dbms *common.DatabaseManager, accountId, eventId string, ev *eventFacts, now time.Time) string {
	lines := []string{"## Incident"}
	if name := firstNonEmpty(ev.AggregationKey, ev.Title); name != "" {
		lines = append(lines, fmt.Sprintf("Alert: %s", name))
	}
	if !ev.StartsAt.IsZero() {
		lines = append(lines, fmt.Sprintf("Started: %s (%s ago). Time now: %s.",
			ev.StartsAt.UTC().Format(time.RFC3339), humanDuration(now.Sub(ev.StartsAt.UTC())), now.Format(time.RFC3339)))
	}
	if state := ev.Labels["aws_event_state"]; state != "" {
		line := fmt.Sprintf("Alarm state when this event was delivered: %s", state)
		if reason := ev.Labels["aws_event_state_reason"]; reason != "" {
			line += fmt.Sprintf(" (%s)", truncate(reason, 200))
		}
		lines = append(lines, line+".")
	}
	if occ := occurrenceLine(ctx, dbms, accountId, eventId, now); occ != "" {
		lines = append(lines, occ)
	}
	if last := lastFiredLine(ctx, dbms, accountId, eventId, ev, now); last != "" {
		lines = append(lines, last)
	}
	// Recorded evidence only: status is set at ingest, and a same-finding_id re-fire records no new
	// firing, so "has not fired since" told the model an active alert was over.
	lines = append(lines, "The event's stored status is not evidence that the incident ended, and a missing "+
		"firing record is not evidence that the alert stopped: re-fires of the same alert are not always recorded. "+
		"Judge whether the problem is still present from the recorded firings, the observed state below, and the "+
		"investigation's own observations.")
	return strings.Join(lines, "\n")
}

func occurrenceLine(ctx *security.RequestContext, dbms *common.DatabaseManager, accountId, eventId string, now time.Time) string {
	var (
		occurrence int
		firstSeen  sql.NullTime
	)
	err := dbms.Db.QueryRowx(
		// Fingerprints are not unique per account (539 on dev are shared) -> without the predicate on d,
		// a small account is told the busiest one's count: 520 against a true 4.
		`SELECT d.occurrence_number, d.absolute_first_seen_at
		   FROM event_duplicates d JOIN events e ON e.fingerprint = d.fingerprint
		  WHERE e.id = $1 AND e.cloud_account_id = $2 AND d.cloud_account_id = e.cloud_account_id
		    AND d.created_at <= $3
		  ORDER BY d.occurrence_number DESC LIMIT 1`, eventId, accountId, now).Scan(&occurrence, &firstSeen)
	if err != nil || occurrence <= 1 || !firstSeen.Valid {
		return ""
	}
	return fmt.Sprintf("This alert has at least %d recorded firings, first seen %s. A fix that only restores service "+
		"has been tried before or the cause recurs.", occurrence, firstSeen.Time.UTC().Format(time.RFC3339))
}

// lastFiredLine states the latest RECORDED firing of this alert. Prose describes the incident as it was
// investigated, sometimes days earlier. It is a lower bound: a later re-fire may not be recorded.
func lastFiredLine(ctx *security.RequestContext, dbms *common.DatabaseManager, accountId, eventId string, ev *eventFacts, now time.Time) string {
	lastFired := ev.StartsAt
	var recurrence sql.NullTime
	err := dbms.Db.QueryRowx(
		`SELECT MAX(d.created_at)
		   FROM event_duplicates d JOIN events e ON e.fingerprint = d.fingerprint
		  WHERE e.id = $1 AND e.cloud_account_id = $2 AND d.cloud_account_id = e.cloud_account_id
		    AND d.created_at <= $3`,
		eventId, accountId, now).Scan(&recurrence)
	if err == nil && recurrence.Valid && recurrence.Time.After(lastFired) {
		lastFired = recurrence.Time
	}
	if lastFired.IsZero() {
		return ""
	}

	line := fmt.Sprintf("Latest recorded firing: %s (%s ago).", lastFired.UTC().Format(time.RFC3339), humanDuration(now.Sub(lastFired.UTC())))
	if ev.EndsAt.After(ev.StartsAt) && !ev.EndsAt.After(now) {
		line += fmt.Sprintf(" The source recorded an end at %s.", ev.EndsAt.UTC().Format(time.RFC3339))
	}
	return line
}

// targetSection names what the command has to act on. The investigation mentions these in prose,
// inconsistently; a command needs them exactly.
func targetSection(ctx *security.RequestContext, dbms *common.DatabaseManager, accountId string, ev *eventFacts) string {
	lines := []string{"## Target"}
	if ev.SubjectName != "" {
		subject := ev.SubjectName
		if ev.SubjectType != "" {
			subject = fmt.Sprintf("%s (type: %s)", subject, ev.SubjectType)
		}
		if ev.SubjectNS != "" {
			subject += fmt.Sprintf(", namespace/service: %s", ev.SubjectNS)
		}
		lines = append(lines, "Resource: "+subject)
	}
	if metric := ev.Labels["aws_event_metric_name"]; metric != "" {
		line := "Metric: " + metric
		if ns := ev.Labels["aws_event_metric_namespace"]; ns != "" {
			line += " in " + ns
		}
		if stat := ev.Labels["aws_event_metric_statistic"]; stat != "" {
			line += fmt.Sprintf(" (%s)", stat)
		}
		if th := ev.Labels["aws_event_threshold"]; th != "" {
			line += fmt.Sprintf(", alarm threshold %s", th)
		}
		lines = append(lines, line)
	}
	if dims := ev.Labels["aws_event_alarm_dimensions"]; dims != "" {
		lines = append(lines, "Alarm dimensions: "+truncate(dims, 200))
	}
	if env := accountEnvironment(dbms, accountId); env != "" {
		lines = append(lines, fmt.Sprintf("Account environment: %s.", env))
	}
	if len(lines) == 1 {
		return ""
	}
	return strings.Join(lines, "\n")
}

func accountEnvironment(dbms *common.DatabaseManager, accountId string) string {
	var env string
	if err := dbms.Db.QueryRowx(`SELECT COALESCE(account_env,'') FROM cloud_accounts WHERE id = $1`, accountId).Scan(&env); err != nil {
		return ""
	}
	return env
}

type inventoryResource struct {
	Name     string
	Type     string
	Arn      string
	LastSeen time.Time
	Meta     map[string]any
}

// configSection states the target's stored configuration and the resources it points at: what a
// rollback restores, and side paths like a queue's dead-letter target the investigation never saw.
// For a pod it adds the owning workload: pod rows carry no limits or deploy annotations, and commands
// change the owner. Ownership is returned apart so budget trimming of config never drops it.
func configSection(ctx *security.RequestContext, dbms *common.DatabaseManager, accountId string, ev *eventFacts) (config, ownership string) {
	region, required := regionScope(tools.GetCloudProviderForAccount(accountId), ev.Region)
	if required && region == "" {
		// A cloud resource is identified by its region; without one, any row we returned would be a
		// guess the plan would then build commands from.
		return "", ""
	}
	subject := loadResource(dbms, accountId, ev.SubjectName, region, ev.SubjectType, ev.SubjectNS)
	var owner *inventoryResource
	if ev.Owner != "" && !strings.EqualFold(ev.Owner, ev.SubjectName) && ev.SubjectNS != "" {
		owner = loadResource(dbms, accountId, ev.Owner, region, ev.OwnerKind, ev.SubjectNS)
	}
	if subject == nil && owner == nil {
		return "", ""
	}
	blocks := []string{}
	workload := owner
	if workload == nil {
		workload = subject
	}
	if subject != nil {
		blocks = append(blocks, renderResource(subject))
		// A resource's config quotes its own identifier too; rendering the subject twice would spend
		// both neighbour slots on it.
		for _, arn := range linkedArns(subject.Meta, subject.Arn, maxLinkedConfigs) {
			if linked := loadResourceByArn(dbms, accountId, arn); linked != nil {
				blocks = append(blocks, renderResource(linked))
			}
		}
	}
	if owner != nil {
		blocks = append(blocks, "Owning workload (what a command changes): "+renderResource(owner))
	}
	if by := managedBy(workload.Meta); by != "" {
		ownership = fmt.Sprintf("## Deployment ownership\n%s %s is managed by %s: its spec is re-applied from that source on every deploy.",
			workload.Type, workload.Name, by)
	}
	return configSectionHeading + "\n" + strings.Join(blocks, "\n"), ownership
}

// conclusionSection states the investigation's stored change plan, which is lifted out of its text and
// never reached a plan before.
func conclusionSection(plan *codeChangePlan) string {
	if plan == nil {
		return ""
	}
	lines := []string{"## Investigation conclusion"}
	switch plan.Kind {
	case "source":
		lines = append(lines, "Where the root cause is: application source code.")
	case "deployment":
		lines = append(lines, "Where the root cause is: deployment configuration.")
	}
	if len(plan.Files) > 0 {
		lines = append(lines, "Files: "+truncate(strings.Join(plan.Files, ", "), 300))
	}
	if plan.Change != "" {
		lines = append(lines, "Required change: "+truncate(plan.Change, 600))
	}
	if plan.TargetValue != "" {
		lines = append(lines, "Target value: "+truncate(plan.TargetValue, 120))
	}
	return strings.Join(lines, "\n")
}

// loadChangePlan reads the investigation's stored change plan for this event. nil when the
// investigation concluded none, or stored something else in its analysis column.
func loadChangePlan(ctx *security.RequestContext, dbms *common.DatabaseManager, accountId, eventId string) *codeChangePlan {
	var analysis string
	err := dbms.Db.QueryRowx(
		`SELECT COALESCE(a.analysis::text,'') FROM event_log_analysis a
		   JOIN events e ON e.id = a.event_id AND e.cloud_account_id = $2
		  WHERE a.event_id = $1 AND a.analysis_type = 'investigation'
		  ORDER BY a.updated_at DESC LIMIT 1`, eventId, accountId).Scan(&analysis)
	if err != nil {
		return nil
	}
	return unmarshalCodeChangePlan(analysis)
}

// managedBy names what re-applies a workload on deploy, from the labels and annotations Helm and
// Argo CD stamp on the objects they own. "" when none is present.
func managedBy(meta map[string]any) string {
	config, _ := meta["config"].(map[string]any)
	labels, _ := config["labels"].(map[string]any)
	annotations, _ := config["annotations"].(map[string]any)
	get := func(m map[string]any, k string) string { return scalarString(m[k]) }
	if app := firstNonEmpty(get(labels, "argocd.argoproj.io/instance"), get(annotations, "argocd.argoproj.io/tracking-id")); app != "" {
		return "Argo CD application " + truncate(app, 80)
	}
	release := get(annotations, "meta.helm.sh/release-name")
	if release == "" && !strings.EqualFold(get(labels, "app.kubernetes.io/managed-by"), "helm") && meta["is_helm_release"] != true {
		return ""
	}
	out := "Helm"
	if release != "" {
		out += " release " + release
	}
	if values := get(annotations, "ci.nudgebee.com/helm.values.filePath"); values != "" {
		out += " (values file " + values + ")"
	}
	return out
}

// regionScope reports which region to match on, and whether a region is required at all.
//
// subject_node is a region only on a cloud account; on k8s it is a node name against rows stored as
// 'global', so matching on it dropped this section for 31% of a dev account's events. A cloud event
// that carries no region is a different case: 74% of dev's Azure events have none, and one account
// holds the same name in 17 regions, so widening the search there attaches another region's
// configuration to the plan.
func regionScope(provider, subjectNode string) (region string, required bool) {
	if provider == "" || strings.EqualFold(provider, "k8s") {
		return "", false
	}
	return subjectNode, true
}

// A name alone picks the wrong row: dev has 17 Deployments called llm-server, and this returned a
// dead namespace's copy (rev 2, 0/0) for a workload at rev 1810, 2/2. Namespace scopes it. #38594
func loadResource(dbms *common.DatabaseManager, accountId, name, region, subjectType, namespace string) *inventoryResource {
	if strings.TrimSpace(name) == "" {
		return nil
	}
	// No fallback once the event names a namespace: answering with another namespace's workload is
	// the bug this scoping exists to stop. Cloud events name none, and keep the unscoped lookup.
	return queryResource(dbms, accountId, name, region, subjectType, strings.TrimSpace(namespace))
}

// queryResource returns the best row for this name, optionally restricted to one namespace.
func queryResource(dbms *common.DatabaseManager, accountId, name, region, subjectType, namespace string) *inventoryResource {
	return scanResource(dbms.Db.QueryRowx(
		`SELECT COALESCE(name,''), COALESCE(type,''), COALESCE(arn,''), last_seen, COALESCE(meta,'{}'::jsonb)
		   FROM cloud_resourses
		  WHERE account = $1 AND name = $2 AND ($3 = '' OR region = $3)
		    AND ($5 = '' OR meta->>'namespace' = $5)
		  ORDER BY (lower(COALESCE(type,'')) = lower($4)) DESC, is_active DESC NULLS LAST, last_seen DESC
		  LIMIT 1`, accountId, name, region, subjectType, namespace))
}

func loadResourceByArn(dbms *common.DatabaseManager, accountId, arn string) *inventoryResource {
	return scanResource(dbms.Db.QueryRowx(
		`SELECT COALESCE(name,''), COALESCE(type,''), COALESCE(arn,''), last_seen, COALESCE(meta,'{}'::jsonb)
		   FROM cloud_resourses WHERE account = $1 AND arn = $2
		  ORDER BY is_active DESC NULLS LAST, last_seen DESC LIMIT 1`, accountId, arn))
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanResource(row rowScanner) *inventoryResource {
	var (
		res      inventoryResource
		lastSeen sql.NullTime
		metaIn   []byte
	)
	if err := row.Scan(&res.Name, &res.Type, &res.Arn, &lastSeen, &metaIn); err != nil {
		return nil
	}
	res.LastSeen = lastSeen.Time
	if err := json.Unmarshal(metaIn, &res.Meta); err != nil {
		res.Meta = nil
	}
	return &res
}

// linkedArns pulls identifiers of OTHER resources out of a config blob, in a stable order. A
// resource's config quotes its own identifier as well, and that one is skipped.
func linkedArns(meta map[string]any, selfArn string, limit int) []string {
	if len(meta) == 0 {
		return nil
	}
	raw, err := json.Marshal(meta)
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	if selfArn != "" {
		seen[selfArn] = true
	}
	out := []string{}
	for _, arn := range arnPattern.FindAllString(string(raw), -1) {
		if seen[arn] {
			continue
		}
		seen[arn] = true
		out = append(out, arn)
		if len(out) == limit {
			break
		}
	}
	sort.Strings(out)
	return out
}

// renderResource prints the short scalar fields of a config blob. Long values are whole policy
// documents or code hashes: not useful to a planner and expensive in context.
func renderResource(res *inventoryResource) string {
	keys := make([]string, 0, len(res.Meta))
	for k, v := range res.Meta {
		s := scalarString(v)
		if s == "" || len(s) > maxConfigValue {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	omitted := 0
	if len(keys) > maxConfigFields {
		omitted = len(keys) - maxConfigFields
		keys = keys[:maxConfigFields]
	}
	pairs := make([]string, 0, len(keys))
	for _, k := range keys {
		pairs = append(pairs, fmt.Sprintf("%s=%s", k, scalarString(res.Meta[k])))
	}
	// An unknown collection time is left unsaid; the zero value would date the row to year 1. The age
	// is stated because "ready" a week ago and "ready" a minute ago support different decisions.
	header := fmt.Sprintf("%s %s", res.Type, res.Name)
	if !res.LastSeen.IsZero() {
		header += fmt.Sprintf(" (observed %s, %s ago)",
			res.LastSeen.UTC().Format(time.RFC3339), humanDuration(time.Since(res.LastSeen.UTC())))
	}
	if len(pairs) == 0 {
		return header
	}
	line := header + ": " + strings.Join(pairs, ", ")
	if omitted > 0 {
		line += fmt.Sprintf(" (+%d more fields)", omitted)
	}
	return strings.Join(append([]string{line}, workloadConfigLines(res.Meta)...), "\n")
}

// workloadConfigLines states what a command has to get right about a Kubernetes workload: which
// container to address, what it is configured with, and what state it is in right now. #38594
func workloadConfigLines(meta map[string]any) []string {
	config, _ := meta["config"].(map[string]any)
	statusInfo, _ := meta["status_info"].(map[string]any)

	var lines []string
	for _, container := range containerLines(config) {
		lines = append(lines, "  "+container)
	}
	if revision := revisionOf(config); revision != "" {
		lines = append(lines, "  rollout revision: "+revision)
	}
	if replicas := scalarString(statusInfo["replicas"]); replicas != "" {
		lines = append(lines, "  desired replicas: "+replicas)
	}
	if conditions := conditionSummaries(statusInfo); len(conditions) > 0 {
		lines = append(lines, "  conditions: "+strings.Join(conditions, ", "))
	}
	return lines
}

// containerLines describes each container the way a command addresses one: by name, with the image
// and resources a fix would change, and the settings that explain a restart loop.
func containerLines(config map[string]any) []string {
	raw, ok := config["containers"].([]any)
	if !ok {
		return nil
	}
	lines := make([]string, 0, len(raw))
	for _, entry := range raw {
		container, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		name := scalarString(container["name"])
		if name == "" {
			continue
		}
		parts := []string{"container " + name + ":"}
		if image := scalarString(container["image"]); image != "" {
			parts = append(parts, "image="+truncate(image, maxConfigValue))
		}
		parts = append(parts, resourceSummaries(container)...)
		if probes := probeNames(container); probes != "" {
			parts = append(parts, "probes="+probes)
		}
		// Names only: an env value can be a credential, and the name alone is what tells the model an
		// application-level setting exists to change.
		if names := envNames(container); names != "" {
			parts = append(parts, "env="+names)
		}
		lines = append(lines, strings.Join(parts, " "))
		if len(lines) == maxContainers {
			break
		}
	}
	return lines
}

// resourceSummaries states what the container asks for and is capped at, which is the value most
// remediations change and the one a rollback has to restore.
func resourceSummaries(container map[string]any) []string {
	resources, ok := container["resources"].(map[string]any)
	if !ok {
		return nil
	}
	summaries := []string{}
	for _, kind := range []string{"requests", "limits"} {
		amounts, ok := resources[kind].(map[string]any)
		if !ok || len(amounts) == 0 {
			continue
		}
		pairs := make([]string, 0, len(amounts))
		for _, resource := range []string{"cpu", "memory"} {
			if amount := scalarString(amounts[resource]); amount != "" {
				pairs = append(pairs, resource+":"+amount)
			}
		}
		if len(pairs) > 0 {
			summaries = append(summaries, kind+"="+strings.Join(pairs, ","))
		}
	}
	return summaries
}

// probeNames reports which probes are configured. A failing probe restarts a healthy container, so
// "no probes" and "both probes" are different incidents.
func probeNames(container map[string]any) string {
	configured := []string{}
	for _, probe := range []string{"liveness", "readiness", "startup"} {
		if _, ok := container[probe+"_probe"].(map[string]any); ok {
			configured = append(configured, probe)
		}
	}
	return strings.Join(configured, ",")
}

// envNames lists the environment variable names, never their values.
func envNames(container map[string]any) string {
	raw, ok := container["env"].([]any)
	if !ok {
		return ""
	}
	names := make([]string, 0, len(raw))
	for _, entry := range raw {
		variable, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		if name := scalarString(variable["name"]); name != "" {
			names = append(names, name)
		}
		if len(names) == maxEnvNames {
			break
		}
	}
	return strings.Join(names, ",")
}

// revisionOf reports the rollout revision. Revision 1 means nothing was ever rolled out over it, so
// there is no earlier state for `rollout undo` to return to.
func revisionOf(config map[string]any) string {
	annotations, ok := config["annotations"].(map[string]any)
	if !ok {
		return ""
	}
	return scalarString(annotations["deployment.kubernetes.io/revision"])
}

// conditionSummaries states why the workload is in the state it is in — unschedulable, unavailable,
// progressing — which decides whether a command is needed at all.
func conditionSummaries(statusInfo map[string]any) []string {
	raw, ok := statusInfo["conditions"].([]any)
	if !ok {
		return nil
	}
	summaries := make([]string, 0, len(raw))
	for _, entry := range raw {
		condition, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		name := scalarString(condition["type"])
		if name == "" {
			continue
		}
		summary := name + "=" + scalarString(condition["status"])
		if reason := scalarString(condition["reason"]); reason != "" {
			summary += " (" + reason + ")"
		}
		summaries = append(summaries, summary)
		if len(summaries) == maxConditions {
			break
		}
	}
	return summaries
}

// attempt is one earlier try at fixing this alert: a command run from a remediation plan, or a fix
// pull request raised from code analysis.
type attempt struct {
	Kind      string // CommandExecution or PullRequest
	Ref       string // the command, or the pull request URL
	Status    string
	Lifecycle string // pull requests only: created, merged or closed
	Branch    string // pull requests only; the name says what the fix changed
	Data      []byte
	At        time.Time
	SameEvent bool
	// FiredAfter counts recorded firings after the attempt: the outcome a value read-back can't see.
	FiredAfter int
	LastFired  sql.NullTime
}

// attemptsSection lists what was already tried for this alert, here and on earlier firings. Without
// it a regenerate proposes the failed command again, or claims no fix exists while a PR sits open.
func attemptsSection(ctx *security.RequestContext, dbms *common.DatabaseManager, accountId, eventId, subjectNS string, now time.Time) string {
	rows, err := dbms.Db.Queryx(
		`SELECT er.type, COALESCE(er.type_reference_id,''), COALESCE(er.status,''),
		        COALESCE(er.pr_lifecycle_state,''), COALESCE(er.data->>'pr_branch', er.data->>'branch', ''),
		        COALESCE(er.data,'{}'::jsonb), er.created_at, (er.event_id = e.id) AS same_event,
		        fired.n, fired.last
		   FROM event_resolution er
		   JOIN events other ON other.id = er.event_id
		   JOIN events e ON e.id = $1 AND e.cloud_account_id = $2
		  CROSS JOIN LATERAL (
		        SELECT count(*) AS n, MAX(d.created_at) AS last FROM event_duplicates d
		         WHERE d.fingerprint = e.fingerprint AND d.cloud_account_id = $2 AND d.created_at > er.created_at AND d.created_at <= $4
		  ) fired
		  WHERE er.type IN ('CommandExecution', 'PullRequest')
		    AND other.cloud_account_id = $2
		    AND other.fingerprint = e.fingerprint
		    AND er.created_at <= $4
		  ORDER BY er.created_at DESC LIMIT $3`, eventId, accountId, 4*maxAttempts, now) // headroom: attemptInScope filters after
	if err != nil {
		ctx.GetLogger().Warn("remediation facts: attempt lookup failed", "error", err)
		return ""
	}
	defer func() { _ = rows.Close() }()

	lines := []string{}
	for rows.Next() && len(lines) < maxAttempts {
		var a attempt
		if err := rows.Scan(&a.Kind, &a.Ref, &a.Status, &a.Lifecycle, &a.Branch, &a.Data, &a.At, &a.SameEvent, &a.FiredAfter, &a.LastFired); err != nil {
			continue
		}
		if !attemptInScope(a, subjectNS) {
			continue
		}
		lines = append(lines, renderAttempt(a))
	}
	if err := rows.Err(); err != nil {
		ctx.GetLogger().Warn("remediation facts: attempt rows ended early", "error", err)
	}
	if len(lines) == 0 {
		return ""
	}
	return "## Remediation already attempted\n" + strings.Join(lines, "\n") +
		"\nDo not repeat an attempt that failed; address why it failed or propose a different action. " +
		"A merged fix followed by this firing either did not address this cause or fixed a different one: " +
		"check what it changed before relying on it. An open fix pull request that addresses this cause is " +
		"the fix to recommend, so name it."
}

// attemptInScope drops kubectl attempts in another namespace: a demo/cart patch recorded on a
// benchmark-server alert steered the next plan into patching cart.
func attemptInScope(a attempt, subjectNS string) bool {
	if a.Kind != "CommandExecution" || subjectNS == "" || !strings.HasPrefix(strings.TrimSpace(a.Ref), "kubectl ") {
		return true
	}
	ns := tools.KubectlNamespace(a.Ref)
	return ns == "" || ns == subjectNS
}

func renderAttempt(a attempt) string {
	scope := "an earlier firing of this alert"
	if a.SameEvent {
		scope = "this incident"
	}
	at := a.At.UTC().Format(time.RFC3339)
	if a.Kind == "PullRequest" {
		state := strings.ToLower(firstNonEmpty(a.Lifecycle, a.Status, "unknown"))
		if state == "created" {
			state = "open"
		}
		line := fmt.Sprintf("- %s on %s: fix pull request %s", at, scope, a.Ref)
		if a.Branch != "" {
			line += fmt.Sprintf(" (branch %s)", truncate(a.Branch, 120))
		}
		line += " -> " + state
		if state == "merged" {
			// ponytail: no merge SHA is stored for fix PRs, so "deployed" cannot be checked against the
			// running image; firings after the merge are the evidence. Store the SHA to check it exactly.
			line += " (whether the merge is deployed is not recorded)"
		}
		return line + firedAfterNote(a)
	}
	line := fmt.Sprintf("- %s on %s: ran %s -> %s", at, scope, truncate(a.Ref, 200), strings.ToLower(firstNonEmpty(a.Status, "unknown")))
	if v := verifyOutcome(a.Data); v != "" {
		line += ", " + v
	}
	return line + firedAfterNote(a)
}

// firedAfterNote states a recorded firing after an attempt; with none it says nothing, since re-fires
// aren't always recorded.
func firedAfterNote(a attempt) string {
	if a.FiredAfter == 0 {
		return ""
	}
	note := fmt.Sprintf("; a firing of this alert was recorded after it (%d recorded)", a.FiredAfter)
	if a.LastFired.Valid {
		note += ", last at " + a.LastFired.Time.UTC().Format(time.RFC3339)
	}
	return note
}

func verifyOutcome(data []byte) string {
	var parsed struct {
		Verify *struct {
			Passed *bool  `json:"passed"`
			Output string `json:"output"`
		} `json:"verify"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil || parsed.Verify == nil {
		return ""
	}
	switch {
	case parsed.Verify.Passed == nil:
		return "verification inconclusive"
	case *parsed.Verify.Passed:
		return "verification passed"
	default:
		return "verification FAILED: " + truncate(strings.TrimSpace(parsed.Verify.Output), 120)
	}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// Rune-safe: a byte slice here cut operator-supplied text (an alarm reason, a branch name) mid
// character and put invalid UTF-8 into the prompt.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return common.TruncateHead(s, n) + "..."
}

func humanDuration(d time.Duration) string {
	if d < time.Minute {
		return "under a minute"
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
}
