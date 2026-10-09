package playbooks

import (
	"errors"
	"log/slog"
	"nudgebee/services/common"
	"nudgebee/services/relay"
	"strings"
)

type podLogAction struct{}
type podLogParams struct {
	Name                  string              `json:"name,omitempty"`
	Namespace             string              `json:"namespace,omitempty"`
	ContainerName         string              `json:"container_name,omitempty"`
	WarnOnMissingLabels   bool                `json:"warn_on_missing_labels,omitempty"`
	RegexReplacerPatterns []map[string]string `json:"regex_replacer_patterns,omitempty"`
	RegexReplacementStyle string              `json:"regex_replacement_style,omitempty"`
	Previous              bool                `json:"previous,omitempty"`
	FilterRegex           string              `json:"filter_regex,omitempty"`
	TailLines             int                 `json:"tail_lines,omitempty"`
	SinceTime             int                 `json:"since_time,omitempty"`
}

func (a *podLogAction) CanAutoExecute(ctx PlaybookActionContext) bool {
	if ctx.GetEvent().Labels == nil {
		return false
	}
	// Resolve kind from Labels["kind"] or SubjectType (agent events use SubjectType)
	kind := strings.ToLower(ctx.GetEvent().Labels["kind"])
	if kind == "" {
		kind = strings.ToLower(ctx.GetEvent().SubjectType)
	}
	// Skip for workload types — logs_enricher expects a pod name, not a workload name.
	if kind == "deployment" || kind == "daemonset" || kind == "statefulset" || kind == "replicaset" || kind == "rollout" {
		return false
	}
	// A Job is not a pod either, and its pods already have a reader: job_pod_enricher
	// picks the failed pod and reads its current container. Asked for the Job by
	// name, this action can only fail — a finished Job's pod has no previous
	// container, and one already cleaned up has no pod.
	if kind == "job" || kind == "cronjob" {
		return false
	}
	// Nor is a node. The namespace label on its alert is the exporter's, so this
	// action would be asking for a pod called after the node in that namespace.
	if kind == "node" {
		return false
	}
	if IsNeverStartedContainerAggKey(ctx.GetEvent().AggregationKey) {
		return false
	}
	// Resolve namespace: SubjectNamespace is canonical, Labels is fallback
	namespace := ctx.GetEvent().SubjectNamespace
	if namespace == "" {
		namespace = ctx.GetEvent().Labels["namespace"]
	}
	return ctx.GetEvent().SubjectName != "" && namespace != ""
}

func (a *podLogAction) AutoExecute(ctx PlaybookActionContext) (PlaybookActionResponse, error) {
	return a.Execute(ctx, podLogAutoParams(ctx.GetEvent()))
}

// podLogAutoParams is what an event asks the agent for. Split out from
// AutoExecute because every branch in it — which container instance, which
// window — is a decision about whether the stored evidence describes the
// incident, and Execute's own path needs a live relay to reach.
func podLogAutoParams(event PlaybookEvent) map[string]any {
	// Resolve namespace: SubjectNamespace is canonical, Labels is fallback
	namespace := event.SubjectNamespace
	if namespace == "" && event.Labels != nil {
		namespace = event.Labels["namespace"]
	}
	params := map[string]any{
		"name":      event.SubjectName,
		"namespace": namespace,
	}
	// The subject container of these classes has already exited by the time
	// enrichment runs — hitting the live container returns the replacement's
	// startup banner, a NotReady error, or nothing. Ask for the dying
	// container's stderr instead.
	if terminatedContainerAggKeys[event.AggregationKey] {
		params["previous"] = true
	}
	// Anchor the read to the event's own window instead of taking whatever tail
	// the agent defaults to. Unanchored, the payload is a slice of the
	// container's whole lifetime: measured on dev over the week to 2026-09-23,
	// 54 of the 239 timestamped pod-log evidences ended more than five minutes
	// before their event and the worst ended 68 days before — a quiet
	// container's startup banner, stored as if it were the incident's output.
	// ResolveQueryWindow anchors on the termination when the cluster gave us
	// one, so a crashed container gets the hour before it died rather than the
	// hour before now. Execute drops the window again if it returns nothing.
	start, _ := event.ResolveQueryWindow(0)
	params["since_time"] = int(start.Unix())
	return params
}

// terminatedContainerAggKeys is the set of event classes whose subject
// container is already dead when enrichment runs.
//
// It is not a list of "things that crash": KubePodCrashLooping and
// KubeContainerWaiting are terminated-container cases that were reading the
// live container. Guessing wrong is cheap — Execute retries against the live
// container when the previous one has no logs to give.
//
// Two groups that used to be here are not. image_pull_backoff_reporter's
// container never started, so it has no previous instance to read
// (IsNeverStartedContainerAggKey). job_failure and KubeJobFailed are about a
// Job, which CanAutoExecute leaves to job_pod_enricher.
var terminatedContainerAggKeys = map[string]bool{
	"report_crash_loop":       true,
	"pod_oom_killer_enricher": true,
	"KubePodCrashLooping":     true,
	"KubeContainerWaiting":    true,
}

func (a *podLogAction) Execute(ctx PlaybookActionContext, rawParams map[string]any) (PlaybookActionResponse, error) {
	var params podLogParams
	err := common.UnmarshalMapToStruct(rawParams, &params)
	if err != nil {
		return nil, err
	}

	current, err := a.fetch(ctx, params, params.Previous)
	if err != nil {
		return nil, err
	}

	// A previous container's logs live on the node that ran it and are garbage
	// collected with it, and a container on its first run has no previous
	// instance at all. Either way `previous: true` comes back with no logs — as
	// "unable to retrieve container logs for containerd://…" in a successful
	// response, or as "previous terminated container … not found" in a failed
	// one. Storing that string is worse than storing nothing: it reads as the
	// workload's output, and because this action belongs to eventrule.logActions
	// it also marks the log category collected, so the account's configured log
	// source is never asked. Retry live instead.
	usedPrevious := params.Previous
	if usedPrevious && !current.usable() && !current.noPods {
		ctx.GetLogger().Info("k8s_pod_log_enricher: previous container logs unavailable, retrying live container",
			"pod", params.Name, "namespace", params.Namespace)
		// With no previous instance the live container is the only one there is,
		// so it becomes the subject whatever it answers. A previous instance that
		// was merely quiet in the window stays the subject unless the live one has
		// something to say.
		if live, liveErr := a.fetch(ctx, params, false); liveErr == nil && (live.usable() || current.refused) {
			current, usedPrevious = live, false
		}
	}

	// A window that matched nothing is not the same as a container with nothing
	// to say: the container may simply have been quiet for the last hour, and on
	// an account with no configured log source the agent is the only thing that
	// can answer at all. Drop the window and take the tail, marked so nobody
	// reads it as the incident's output. Not when the cluster refused: a container
	// that never started has no tail either.
	outsideWindow := false
	if params.SinceTime > 0 && !current.usable() && !current.refused {
		unwindowed := params
		unwindowed.SinceTime = 0
		ctx.GetLogger().Info("k8s_pod_log_enricher: no logs inside the event window, falling back to the container tail",
			"pod", params.Name, "namespace", params.Namespace, "since_time", params.SinceTime)
		if tail, tailErr := a.fetch(ctx, unwindowed, usedPrevious); tailErr == nil && tail.usable() {
			current, outsideWindow = tail, true
		}
	}

	// The retries above rescue the previous-container and empty-window cases;
	// what is left here is a container that genuinely returned nothing. Storing it produces a log card
	// that decodes to an empty gzip — 258 of 1179 agent log evidences on dev over
	// the week to 2026-09-23 — and, because this action belongs to
	// eventrule.logActions, suppresses the account's configured log source for
	// that event. Nil is the honest answer: no evidence, category still open.
	if !current.usable() {
		ctx.GetLogger().Info("k8s_pod_log_enricher: no usable container logs, leaving log collection to the configured source",
			"pod", params.Name, "namespace", params.Namespace, "previous", usedPrevious, "reason", current.reason)
		return nil, nil
	}
	relayResponse, additionalInfo, data := current.response, current.additionalInfo, current.data

	filename, ok := relayResponse["filename"].(string)
	if !ok {
		ctx.GetLogger().Error("relay: unable to process request", "response", slog.AnyValue(relayResponse))
		return nil, errors.New("relay: unable to execute relay query")
	}
	typeVal, ok := relayResponse["type"].(string)
	if !ok {
		ctx.GetLogger().Error("relay: unable to process request", "response", slog.AnyValue(relayResponse))
		return nil, errors.New("relay: unable to execute relay query")
	}
	insight := InsightFromRelayResponse(relayResponse)
	logDigest := summarizeAgentLogPayload(data)
	insight = append(insight, logDigest.Insights...)

	// Record which container instance was read and over what window. Without
	// this, stored evidence does not say whether a payload is the dying
	// container or its replacement, and no audit of past events can tell.
	if additionalInfo == nil {
		additionalInfo = map[string]any{}
	}
	additionalInfo["previous_container"] = usedPrevious
	if params.Previous != usedPrevious {
		additionalInfo["previous_container_unavailable"] = true
	}
	if params.SinceTime > 0 {
		additionalInfo["since_time"] = params.SinceTime
	}
	// Says plainly that what follows is not from the window we asked for, so a
	// reader (and the investigation prompt) can discount it rather than treat a
	// week-old line as evidence about this incident.
	if outsideWindow {
		additionalInfo["outside_event_window"] = true
	}
	if params.TailLines > 0 {
		additionalInfo["tail_lines"] = params.TailLines
	}
	// Same key the provider log actions write, so the investigation tools read one
	// shape regardless of which source the logs came from.
	if logDigest.Summary != nil {
		additionalInfo["log_summary"] = logDigest.Summary
	}
	// The log card shows these instead of the raw file when the file is too long to read.
	if len(logDigest.Groups) > 0 {
		additionalInfo["log_groups"] = logDigest.Groups
		additionalInfo["log_total_lines"] = logDigest.TotalLines
	}
	// What window this payload is evidence of. The agent answers with a tail of the
	// container's lifetime, not a window around the event, so without this a card
	// holding last month's startup banner looks exactly like one holding the crash.
	// Empty until the agent reads logs with timestamps on.
	if logDigest.FirstSeen != "" {
		additionalInfo["log_window_start"] = logDigest.FirstSeen
		additionalInfo["log_window_end"] = logDigest.LastSeen
	}

	return PlaybookActionResponseFile{
		AdditionalInfo: additionalInfo,
		Data:           data,
		Filename:       filename,
		Type:           typeVal,
		Insight:        insight,
	}, nil
}

// executeLogsEnricherFn is an indirection so tests can script the agent's answers.
var executeLogsEnricherFn = relay.ExecuteAndExtractResponse

// podLogFetch is one logs_enricher answer.
type podLogFetch struct {
	response       map[string]any
	additionalInfo map[string]any
	data           string
	// refused is the cluster saying this container instance has no logs to give:
	// it never started, it has no previous instance, its logs were collected with
	// it. Asking again over a different window cannot change that; asking for a
	// different instance can.
	refused bool
	// noPods is the refusal that covers every instance: the subject has no pod,
	// so there is no live container to try in place of the previous one.
	noPods bool
	// reason is what the cluster said when it refused, for the log line.
	reason string
}

func (f podLogFetch) usable() bool {
	return !f.refused && usableAgentLogOutput(f.data)
}

// fetch runs one logs_enricher round-trip for the given container instance.
func (a *podLogAction) fetch(ctx PlaybookActionContext, params podLogParams, previous bool) (podLogFetch, error) {
	actionParams := map[string]any{
		"container_name": params.ContainerName,
		"name":           params.Name,
		"namespace":      params.Namespace,
		"previous":       previous,
	}
	// Forwarded only when a caller set them. podLogParams has declared these
	// since it was written and never sent them, so the agent has always
	// answered with its own default tail over the container's whole lifetime.
	if params.SinceTime > 0 {
		actionParams["since_time"] = params.SinceTime
	}
	if params.TailLines > 0 {
		actionParams["tail_lines"] = params.TailLines
	}
	if params.FilterRegex != "" {
		actionParams["filter_regex"] = params.FilterRegex
	}
	relayRequest := relay.RelayExecuteRequest{
		Body: relay.ActionExecuteBody{
			AccountID:    ctx.GetAccountId(),
			ActionName:   "logs_enricher",
			ActionParams: actionParams,
			Origin:       "services-server",
		},
		NoSinks: true,
		Cache:   false,
	}
	response, additionalInfo, err := executeLogsEnricherFn(relayRequest)
	if err != nil {
		// The agent reports the kubelet's "no logs here" answers as a failed action,
		// so they arrive as an error rather than as output. They are still an
		// answer, and Execute's fallbacks are written for it: returned as an error,
		// "previous terminated container … not found" ended the run before the
		// live-container retry could start.
		if ClusterHasNoLogs(err.Error()) {
			return podLogFetch{refused: true, noPods: noPodsAnswerPattern.MatchString(err.Error()), reason: err.Error()}, nil
		}
		return podLogFetch{}, err
	}
	data, ok := response["data"].(string)
	if !ok {
		ctx.GetLogger().Error("relay: unable to process request", "response", slog.AnyValue(response))
		return podLogFetch{}, errors.New("relay: unable to execute relay query")
	}
	fetched := podLogFetch{response: response, additionalInfo: additionalInfo, data: data}
	// Some of these answers come back as the body of a successful read instead:
	// the kubelet serves "unable to retrieve container logs for …" as log content.
	if decoded, decodeErr := DecodeAgentPayload(data); decodeErr == nil && IsLogRetrievalFailure(decoded) {
		fetched.refused, fetched.reason = true, strings.TrimSpace(decoded)
	}
	return fetched, nil
}

// maxPodLogInsights is how many log patterns become card highlights.
const maxPodLogInsights = 3

// summarizeAgentLogPayload groups the lines of an agent log payload.
// An undecodable payload yields nothing rather than an error: the logs are still
// stored and shown, they just carry no summary.
func summarizeAgentLogPayload(payload string) LogLinesDigest {
	decoded, err := DecodeAgentPayload(payload)
	if err != nil || IsLogRetrievalFailure(decoded) {
		return LogLinesDigest{}
	}
	return LogLinesSummary(decoded, maxPodLogInsights)
}

// usableAgentLogOutput reports whether an agent log payload carries actual log
// lines, as opposed to being empty or one of the kubelet's "no logs here"
// answers. Undecodable payloads count as usable — unreadable is not the same
// as absent, and the old behaviour was to keep them.
func usableAgentLogOutput(payload string) bool {
	decoded, err := DecodeAgentPayload(payload)
	if err != nil {
		return true
	}
	if strings.TrimSpace(decoded) == "" {
		return false
	}
	return !IsLogRetrievalFailure(decoded)
}
