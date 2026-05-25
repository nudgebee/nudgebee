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
	if kind == "deployment" || kind == "daemonset" || kind == "statefulset" || kind == "replicaset" {
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
	// For crash_loop, OOM, and image_pull_backoff the current container is
	// either restarting or unable to start — log_enricher hitting the live
	// container would return empty or NotReady error. Run with
	// `previous: true` so we collect the dying container's stderr instead.
	switch ctx.GetEvent().AggregationKey {
	case "report_crash_loop", "pod_oom_killer_enricher", "image_pull_backoff_reporter":
		params["previous"] = true
	}
	return a.Execute(ctx, params)
}

func (a *podLogAction) Execute(ctx PlaybookActionContext, rawParams map[string]any) (PlaybookActionResponse, error) {
	var params podLogParams
	err := common.UnmarshalMapToStruct(rawParams, &params)
	if err != nil {
		return nil, err
	}

	relayRequest := relay.RelayExecuteRequest{
		Body: relay.ActionExecuteBody{
			AccountID:  ctx.GetAccountId(),
			ActionName: "logs_enricher",
			ActionParams: map[string]any{
				"container_name": params.ContainerName,
				"name":           params.Name,
				"namespace":      params.Namespace,
				"previous":       params.Previous,
			},
			Origin: "services-server",
		},
		NoSinks: true,
		Cache:   false,
	}
	relayResponse, additionalInfo, err := relay.ExecuteAndExtractResponse(relayRequest)
	if err != nil {
		return nil, err
	}
	data, ok := relayResponse["data"].(string)
	if !ok {
		ctx.GetLogger().Error("relay: unable to process request", "response", slog.AnyValue(relayResponse))
		return nil, errors.New("relay: unable to execute relay query")
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
	return PlaybookActionResponseFile{
		AdditionalInfo: additionalInfo,
		Data:           data,
		Filename:       filename,
		Type:           typeVal,
		Insight:        insight,
	}, err
}
