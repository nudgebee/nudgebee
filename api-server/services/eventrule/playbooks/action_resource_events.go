package playbooks

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

// resource_events_enricher fetches K8s Events for the subject resource — and,
// for a workload, the ReplicaSets / Jobs / Pods it owns — and renders them as a
// kubectl-style table (LastSeen / Type / Reason / Object / Message).
//
// Fires for any pod / workload / node subject the trigger engine produced.
// Skipped when the agent already provided this block.
type resourceEventsAction struct{}

var resourceEventsAggKeys = map[string]bool{
	"pod_oom_killer_enricher":     true,
	"report_crash_loop":           true,
	"image_pull_backoff_reporter": true,
	"node_not_ready":              true,
}

func (a *resourceEventsAction) CanAutoExecute(ctx PlaybookActionContext) bool {
	if !resourceEventsAggKeys[ctx.GetEvent().AggregationKey] {
		// Also fire for any pod/node-subject event from a trigger.
		st := strings.ToLower(ctx.GetEvent().SubjectType)
		if st != "pod" && st != "node" && st != "deployment" && st != "statefulset" && st != "daemonset" && st != "job" && st != "rollout" {
			return false
		}
	}
	return ctx.GetEvent().SubjectName != ""
}

func (a *resourceEventsAction) AutoExecute(ctx PlaybookActionContext) (PlaybookActionResponse, error) {
	subjectKind := strings.ToLower(ctx.GetEvent().SubjectType)
	if subjectKind == "" {
		subjectKind = "pod"
	}
	return a.Execute(ctx, map[string]any{
		"name":      ctx.GetEvent().SubjectName,
		"namespace": ctx.GetEvent().SubjectNamespace,
		"kind":      subjectKind,
		// Only for the failure classes: the extra lookup below costs a relay
		// round-trip, and only these have a node-recorded counterpart.
		"node_name": nodeNameForNodeScopedEvents(ctx),
	})
}

func (a *resourceEventsAction) Execute(ctx PlaybookActionContext, rawParams map[string]any) (PlaybookActionResponse, error) {
	name, _ := rawParams["name"].(string)
	namespace, _ := rawParams["namespace"].(string)
	kind, _ := rawParams["kind"].(string)
	nodeName, _ := rawParams["node_name"].(string)
	if name == "" {
		return nil, errors.New("resource_events_enricher: name required")
	}
	if kind == "" {
		kind = "Pod"
	}
	kind = titleASCII(kind)

	// The agent's get_resource doesn't honor field_selector (kube/Handlers
	// only accepts resource_type/group/version/namespace/name). Pull the
	// namespace's events and filter client-side on the involvedObject (see
	// filterEventsForSubject). Namespace scope keeps the round-trip bounded
	// even for clusters with thousands of cluster-wide events.
	params := map[string]any{
		"resource_type":  "events",
		"group":          "",
		"version":        "v1",
		"namespace":      []string{namespace},
		"all_namespaces": namespace == "",
		"name":           []string{},
	}
	data, additionalInfo, err := getResourceViaRelay(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("resource_events_enricher: %w", err)
	}

	matched := filterEventsForSubject(data, kind, name, namespace)

	// Node Events are recorded in the "default" namespace, so the namespace
	// fetch above cannot see them however the filter is written. A second,
	// reason-filtered lookup is what puts the actual kill in the table.
	if nodeName != "" && namespace != "default" {
		nodeData, _, nodeErr := getResourceViaRelay(ctx, map[string]any{
			"resource_type":  "events",
			"group":          "",
			"version":        "v1",
			"namespace":      []string{"default"},
			"all_namespaces": false,
			"name":           []string{},
		})
		if nodeErr != nil {
			ctx.GetLogger().Info("resource_events_enricher: node event lookup failed", "node", nodeName, "error", nodeErr)
		} else {
			matched = append(matched, nodeScopedFailureEvents(nodeData, nodeName)...)
		}
	} else {
		matched = append(matched, nodeScopedFailureEvents(data, nodeName)...)
	}
	rows, headers, insight := resourceEventsCard(matched, kind, name, ctx.GetEvent().AlertStart(), time.Now())

	if additionalInfo == nil {
		additionalInfo = map[string]any{}
	}
	additionalInfo["title"] = fmt.Sprintf("Recent %s Events", kind)
	additionalInfo["action_name"] = "resource_events_enricher"
	additionalInfo["actual_action_name"] = "resource_events_enricher"
	additionalInfo["subject_name"] = name
	additionalInfo["subject_namespace"] = namespace

	return PlaybookActionResponseTable{
		Rows:           rows,
		Headers:        headers,
		AdditionalInfo: additionalInfo,
		Insight:        insight,
	}, nil
}

// nodeNameForNodeScopedEvents returns the node to pull node-recorded failure
// Events for, and "" when this event class has none worth a second round-trip.
func nodeNameForNodeScopedEvents(ctx PlaybookActionContext) string {
	if !nodeRecordedFailureAggKeys[ctx.GetEvent().AggregationKey] {
		return ""
	}
	return SubjectNodeName(ctx.GetEvent())
}

// nodeRecordedFailureAggKeys are the classes Kubernetes records against the Node
// as well as the Pod, and so the only ones worth the extra default-namespace
// lookup. An ImagePullBackOff or a crash loop has no node-side counterpart, so
// asking would spend a relay round-trip per event to find nothing.
var nodeRecordedFailureAggKeys = map[string]bool{
	"pod_oom_killer_enricher": true,
	"node_not_ready":          true,
}

// nodeScopedFailureReasons are the Event reasons Kubernetes records against the
// Node object rather than the Pod. The kubelet's OOM watcher emits OOMKilling /
// SystemOOM on the Node, so a Pod-scoped filter never sees the very kill the
// event is about: across 176 OOMKilled events on dev, not one "Recent Pod
// Events" table mentioned an OOM.
var nodeScopedFailureReasons = map[string]bool{
	"OOMKilling":            true,
	"SystemOOM":             true,
	"Evicted":               true,
	"NodeNotReady":          true,
	"NodeHasMemoryPressure": true,
	"NodeHasDiskPressure":   true,
}

// nodeScopedFailureEvents returns the failure Events recorded against the node
// the subject ran on. Restricted to nodeScopedFailureReasons because a busy
// node emits a great many Events that have nothing to do with this subject.
func nodeScopedFailureEvents(data any, nodeName string) []any {
	if nodeName == "" {
		return nil
	}
	arr, ok := data.([]any)
	if !ok {
		return nil
	}
	out := []any{}
	for _, item := range arr {
		ev, ok := item.(map[string]any)
		if !ok {
			continue
		}
		io := getMapField(ev, "involved_object", "involvedObject")
		if io == nil {
			continue
		}
		if k, _ := io["kind"].(string); !strings.EqualFold(k, "Node") {
			continue
		}
		if n, _ := io["name"].(string); n != nodeName {
			continue
		}
		if reason, _ := ev["reason"].(string); !nodeScopedFailureReasons[reason] {
			continue
		}
		out = append(out, ev)
	}
	return out
}

// filterEventsByInvolvedObject narrows a list of Events to those whose
// involvedObject matches (kind, name, namespace). The agent returns events
// at the namespace (or cluster) level — we trim client-side because the
// agent's get_resource doesn't support field_selector.
func filterEventsByInvolvedObject(data any, kind, name, namespace string) []any {
	arr, ok := data.([]any)
	if !ok {
		return nil
	}
	out := []any{}
	for _, item := range arr {
		ev, ok := item.(map[string]any)
		if !ok {
			continue
		}
		io := getMapField(ev, "involved_object", "involvedObject")
		if io == nil {
			continue
		}
		if name != "" {
			if n, _ := io["name"].(string); n != name {
				continue
			}
		}
		if namespace != "" {
			if ns, _ := io["namespace"].(string); ns != namespace {
				continue
			}
		}
		if kind != "" && !strings.EqualFold(kind, "Any") {
			if k, _ := io["kind"].(string); !strings.EqualFold(k, kind) {
				continue
			}
		}
		out = append(out, ev)
	}
	return out
}

// k8sGeneratedChars is the alphabet Kubernetes draws generated name parts from
// (k8s.io/apimachinery/pkg/util/rand): both the 5-character suffix of a
// generateName object and the pod-template-hash a Deployment names its
// ReplicaSets with.
const k8sGeneratedChars = `[bcdfghjklmnpqrstvwxz2456789]`

// ownedObject is one kind of object a workload's controllers create, and the
// name they give it.
type ownedObject struct {
	kind string
	name *regexp.Regexp
}

// ownedObjects returns the objects a workload's controllers create. Kubernetes
// records the Events that explain a workload failure (FailedScheduling, image
// pull errors, BackOff) on these, not on the workload: keeping only the
// workload's own Events left almost every Deployment and StatefulSet card
// empty.
//
// An Event's involvedObject carries the object's own uid, not its owner's, so
// ownership is read from the exact name each controller generates. The patterns
// are anchored and shape-specific so that workload "api" does not swallow the
// objects of a sibling "api-worker". The pod-template-hash is the decimal fnv32
// hash re-encoded one character per digit (1-10 characters); requiring at least
// 6 misses 1 hash in ~40,000 but stops a Job "api-v2" passing for "api"'s pods.
func ownedObjects(kind, name string) []ownedObject {
	suffix := k8sGeneratedChars + `{5}`
	owned := func(kind, rest string) ownedObject {
		return ownedObject{kind: kind, name: regexp.MustCompile(`^` + regexp.QuoteMeta(name) + `-` + rest + `$`)}
	}
	switch strings.ToLower(kind) {
	case "deployment", "rollout":
		hash := k8sGeneratedChars + `{6,10}`
		return []ownedObject{owned("ReplicaSet", hash), owned("Pod", hash+`-`+suffix)}
	case "statefulset":
		return []ownedObject{owned("Pod", `\d+`)}
	case "daemonset", "replicaset":
		return []ownedObject{owned("Pod", suffix)}
	case "job":
		// Indexed Jobs name their pods <job>-<index>-<suffix>.
		return []ownedObject{owned("Pod", `(\d+-)?`+suffix)}
	case "cronjob":
		// Each Job is named after its scheduled time in minutes.
		return []ownedObject{owned("Job", `\d+`), owned("Pod", `\d+-(\d+-)?`+suffix)}
	}
	return nil
}

// filterEventsForSubject keeps the Events about the subject itself and, for a
// workload, about the objects its controllers created (see ownedObjects).
func filterEventsForSubject(data any, kind, name, namespace string) []any {
	owned := ownedObjects(kind, name)
	out := []any{}
	for _, item := range filterEventsByInvolvedObject(data, "", "", namespace) {
		io := getMapField(item.(map[string]any), "involved_object", "involvedObject")
		ioKind, _ := io["kind"].(string)
		ioName, _ := io["name"].(string)
		if ioName == name && strings.EqualFold(ioKind, kind) {
			out = append(out, item)
			continue
		}
		for _, o := range owned {
			if strings.EqualFold(ioKind, o.kind) && o.name.MatchString(ioName) {
				out = append(out, item)
				break
			}
		}
	}
	return out
}

// maxResourceEventRows bounds the card: a crash-looping workload with many
// replicas repeats the same few Events per pod.
const maxResourceEventRows = 50

// eventTTL is kube-apiserver's default --event-ttl, after which Events are
// garbage collected.
const eventTTL = time.Hour

// resourceEventsCard renders the matched Events, Warnings first and newest
// first, capped at maxResourceEventRows, and says why when there are none.
func resourceEventsCard(events []any, kind, name string, alertStart *time.Time, now time.Time) ([][]any, []string, []PlaybookActionResponseInsight) {
	insight := []PlaybookActionResponseInsight{}
	shown := mostRelevantEvents(events, maxResourceEventRows)
	rows, headers := eventListToTable(shown)
	if len(shown) < len(events) {
		insight = append(insight, PlaybookActionResponseInsight{
			Message:  fmt.Sprintf("Showing %d of %d Events, Warnings first and newest first.", len(shown), len(events)),
			Severity: "info",
		})
	}
	if len(rows) == 0 {
		insight = append(insight, PlaybookActionResponseInsight{
			Message:  noEventsMessage(kind, name, alertStart, now),
			Severity: "info",
		})
	}
	return rows, headers, insight
}

// noEventsMessage explains an empty card. Blaming the event TTL is only right
// when the alert is older than it: most empty cards are for alerts minutes old,
// and an empty table there means nothing was recorded.
func noEventsMessage(kind, name string, alertStart *time.Time, now time.Time) string {
	subject := fmt.Sprintf("%s/%s", kind, name)
	if owned := ownedObjects(kind, name); len(owned) > 0 {
		kinds := make([]string, len(owned))
		for i, o := range owned {
			kinds[i] = o.kind + "s"
		}
		subject += " or its " + strings.Join(kinds, " and ")
	}
	switch {
	case alertStart == nil || alertStart.IsZero():
		return fmt.Sprintf("No Kubernetes Events found for %s.", subject)
	case now.Sub(*alertStart) > eventTTL:
		return fmt.Sprintf("No Kubernetes Events found for %s. The alert started %s ago, longer than the cluster's event TTL (1h by default), so Events from around the alert have likely been garbage collected.", subject, formatDuration(now.Sub(*alertStart)))
	default:
		return fmt.Sprintf("No Kubernetes Events were recorded for %s.", subject)
	}
}

// mostRelevantEvents orders Warnings before Normal Events, newest first within
// each, and keeps at most limit of them.
func mostRelevantEvents(events []any, limit int) []any {
	sorted := append([]any(nil), events...)
	sort.SliceStable(sorted, func(i, j int) bool {
		a, _ := sorted[i].(map[string]any)
		b, _ := sorted[j].(map[string]any)
		aType, _ := a["type"].(string)
		bType, _ := b["type"].(string)
		if aWarn, bWarn := aType == "Warning", bType == "Warning"; aWarn != bWarn {
			return aWarn
		}
		return eventLastSeenTime(a).After(eventLastSeenTime(b))
	})
	if len(sorted) > limit {
		sorted = sorted[:limit]
	}
	return sorted
}

func eventLastSeen(item map[string]any) string {
	if last := firstStringField(item, "last_timestamp", "lastTimestamp"); last != "" {
		return last
	}
	return firstStringField(item, "event_time", "eventTime")
}

// eventLastSeenTime is the zero time when the Event carries no parseable
// timestamp, which sorts it last.
func eventLastSeenTime(item map[string]any) time.Time {
	t, _ := time.Parse(time.RFC3339Nano, eventLastSeen(item))
	return t
}

func eventListToTable(data any) ([][]any, []string) {
	headers := []string{"LastSeen", "Type", "Reason", "Object", "Message"}
	rows := [][]any{}
	walk := func(item map[string]any) {
		last := eventLastSeen(item)
		eventType, _ := item["type"].(string)
		reason, _ := item["reason"].(string)
		obj := ""
		if io, ok := item["involved_object"].(map[string]any); ok {
			obj = involvedObjectLabel(io)
		} else if io, ok := item["involvedObject"].(map[string]any); ok {
			obj = involvedObjectLabel(io)
		}
		msg, _ := item["message"].(string)
		rows = append(rows, []any{last, eventType, reason, obj, msg})
	}
	switch d := data.(type) {
	case []any:
		for _, item := range d {
			if m, ok := item.(map[string]any); ok {
				walk(m)
			}
		}
	case map[string]any:
		walk(d)
	}
	return rows, headers
}

func involvedObjectLabel(io map[string]any) string {
	kind, _ := io["kind"].(string)
	name, _ := io["name"].(string)
	return fmt.Sprintf("%s/%s", kind, name)
}

func firstStringField(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k].(string); ok && v != "" {
			return v
		}
	}
	return ""
}

// titleASCII uppercases the first byte if it's a lowercase ASCII letter.
// Replaces strings.Title for the narrow case of canonicalising a K8s kind
// string (pod -> Pod, deployment -> Deployment) — those are all ASCII so
// we don't need the deprecated Unicode-aware strings.Title.
func titleASCII(s string) string {
	if s == "" {
		return s
	}
	c := s[0]
	if c >= 'a' && c <= 'z' {
		return string(c-('a'-'A')) + s[1:]
	}
	return s
}
