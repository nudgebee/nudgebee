package playbooks

import (
	"errors"
	"log/slog"
	"nudgebee/services/common"
	"nudgebee/services/relay"
)

type k8sResourceAction struct{}
type k8sResourceParams struct {
	ResourceType  string   `json:"resource_type,omitempty"`
	Group         string   `json:"group,omitempty"`
	Version       string   `json:"version,omitempty"`
	Namespace     []string `json:"namespace,omitempty"`
	AllNamespaces bool     `json:"all_namespaces,omitempty"`
	Name          []string `json:"name,omitempty"`
	Owner         string   `json:"owner,omitempty"`
}

func (a *k8sResourceAction) Execute(ctx PlaybookActionContext, rawParams map[string]any) (PlaybookActionResponse, error) {
	var params k8sResourceParams
	err := common.UnmarshalMapToStruct(rawParams, &params)
	if err != nil {
		return nil, err
	}
	if params.Namespace == nil {
		params.Namespace = []string{}
	}
	if params.Name == nil {
		params.Name = []string{}
	}

	relayRequest := relay.RelayExecuteRequest{
		Body: relay.ActionExecuteBody{
			AccountID:  ctx.GetAccountId(),
			ActionName: "get_resource",
			ActionParams: map[string]any{
				"resource_type":  params.ResourceType,
				"group":          params.Group,
				"version":        params.Version,
				"namespace":      params.Namespace,
				"all_namespaces": params.AllNamespaces,
				"name":           params.Name,
				"owner":          params.Owner,
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
	data, ok := relayResponse["data"]
	if !ok {
		ctx.GetLogger().Error("relay: unable to process request", "response", slog.AnyValue(relayResponse))
		return nil, errors.New("relay: unable to execute relay query")
	}

	insight := InsightFromRelayResponse(relayResponse)
	metadata := map[string]any{
		"query-result-version": "1.0",
		"query":                rawParams,
	}
	return NewPlaybookActionResponseJson(data, additionalInfo, insight, metadata), err
}
