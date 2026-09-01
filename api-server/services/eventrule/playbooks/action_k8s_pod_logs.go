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
	// Resolve namespace: SubjectNamespace is canonical, Labels is fallback
	namespace := ctx.GetEvent().SubjectNamespace
	if namespace == "" {
		namespace = ctx.GetEvent().Labels["namespace"]
	}
	return ctx.GetEvent().SubjectName != "" && namespace != ""
}

func (a *podLogAction) AutoExecute(ctx PlaybookActionContext) (PlaybookActionResponse, error) {
	// Resolve namespace: SubjectNamespace is canonical, Labels is fallback
	namespace := ctx.GetEvent().SubjectNamespace
	if namespace == "" && ctx.GetEvent().Labels != nil {
		namespace = ctx.GetEvent().Labels["namespace"]
	}
	params := map[string]any{
		"name":      ctx.GetEvent().SubjectName,
		"namespace": namespace,
	}
	// The subject container of these classes has already exited by the time
	// enrichment runs — hitting the live container returns the replacement's
	// startup banner, a NotReady error, or nothing. Ask for the dying
	// container's stderr instead.
	if terminatedContainerAggKeys[ctx.GetEvent().AggregationKey] {
		params["previous"] = true
	}
	return a.Execute(ctx, params)
}

// terminatedContainerAggKeys is the set of event classes whose subject
// container is already dead when enrichment runs.
//
// It is not a list of "things that crash". job_failure, KubeJobFailed,
// KubePodCrashLooping and KubeContainerWaiting are all terminated-container
// cases that were reading the live container: a finished Job has no live
// container at all, so those events carried the replacement pod's output or
// none. Guessing wrong is cheap — Execute retries against the live container
// when the previous one's logs have already been garbage collected.
var terminatedContainerAggKeys = map[string]bool{
	"report_crash_loop":           true,
	"pod_oom_killer_enricher":     true,
	"image_pull_backoff_reporter": true,
	"job_failure":                 true,
	"KubeJobFailed":               true,
	"KubePodCrashLooping":         true,
	"KubeContainerWaiting":        true,
}

func (a *podLogAction) Execute(ctx PlaybookActionContext, rawParams map[string]any) (PlaybookActionResponse, error) {
	var params podLogParams
	err := common.UnmarshalMapToStruct(rawParams, &params)
	if err != nil {
		return nil, err
	}

	relayResponse, additionalInfo, err := a.fetch(ctx, params, params.Previous)
	if err != nil {
		return nil, err
	}
	data, ok := relayResponse["data"].(string)
	if !ok {
		ctx.GetLogger().Error("relay: unable to process request", "response", slog.AnyValue(relayResponse))
		return nil, errors.New("relay: unable to execute relay query")
	}

	// A previous container's logs live on the node that ran it and are garbage
	// collected with it, so `previous: true` can answer "unable to retrieve
	// container logs for containerd://…" — a successful response carrying no
	// logs. Storing that string is worse than storing nothing: it reads as the
	// workload's output, and because this action belongs to
	// eventrule.logActions it also marks the log category collected, so the
	// account's configured log source is never asked. Retry live instead.
	usedPrevious := params.Previous
	if params.Previous && !usableAgentLogOutput(data) {
		ctx.GetLogger().Info("k8s_pod_log_enricher: previous container logs unavailable, retrying live container",
			"pod", params.Name, "namespace", params.Namespace)
		if liveResponse, liveInfo, liveErr := a.fetch(ctx, params, false); liveErr == nil {
			if liveData, ok := liveResponse["data"].(string); ok && usableAgentLogOutput(liveData) {
				relayResponse, additionalInfo, data, usedPrevious = liveResponse, liveInfo, liveData, false
			}
		}
	}

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
	if params.TailLines > 0 {
		additionalInfo["tail_lines"] = params.TailLines
	}

	return PlaybookActionResponseFile{
		AdditionalInfo: additionalInfo,
		Data:           data,
		Filename:       filename,
		Type:           typeVal,
		Insight:        insight,
	}, nil
}

// fetch runs one logs_enricher round-trip for the given container instance.
func (a *podLogAction) fetch(ctx PlaybookActionContext, params podLogParams, previous bool) (map[string]any, map[string]any, error) {
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
	return relay.ExecuteAndExtractResponse(relayRequest)
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
