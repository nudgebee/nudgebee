package playbooks

import (
	"errors"
	"log/slog"
	"nudgebee/services/common"
	"nudgebee/services/relay"
)

type k8sKubectlAction struct{}
type k8sKubectlParams struct {
	Command   string `json:"command,omitempty"`
	AccountId string `json:"account_id,omitempty"`
}

func (a *k8sKubectlAction) Execute(ctx PlaybookActionContext, rawParams map[string]any) (PlaybookActionResponse, error) {
	var params k8sKubectlParams
	err := common.UnmarshalMapToStruct(rawParams, &params)
	if err != nil {
		return nil, err
	}

	if params.Command == "" {
		return nil, errors.New("command is required")
	}

	if params.AccountId == "" {
		params.AccountId = ctx.GetAccountId()
	}

	if params.AccountId == "" {
		return nil, errors.New("account_id is required")
	}

	relayRequest := relay.RelayExecuteRequest{
		Body: relay.ActionExecuteBody{
			AccountID:  params.AccountId,
			ActionName: "kubectl_command_executor",
			ActionParams: map[string]any{
				"command": params.Command,
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
