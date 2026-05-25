package autopilot

import (
	"errors"
	"io"
	"log/slog"
	"nudgebee/services/common"
	"nudgebee/services/config"
	"nudgebee/services/security"
)

func CreateAutoOptimizeRule(ctx *security.RequestContext, autopilotRequest AutoOptimizeRequest, tenantId string) (AutoOptimizeResponse, error) {
	ctx.GetLogger().Info("autopilot: autopilot request sent", "request", slog.AnyValue(autopilotRequest))

	resp, err := common.HttpPost(config.Config.AutoPilotUrl+"/autopilot", common.HttpWithJsonBody(map[string]any{
		"input": map[string]any{
			"arg1": autopilotRequest,
		},
		"session_variables": map[string]any{
			"user_id":   "00000000-0000-0000-0000-000000000000",
			"tenant_id": tenantId,
		},
	}), common.HttpWithHeaders(map[string]string{
		config.Config.ServiceApiServerTokenHeader: config.Config.ServiceApiServerToken,
		"Content-Type": "application/json",
	}))

	if err != nil {
		ctx.GetLogger().Error("autopilot: error sending autopilot request", "error", err)
		return AutoOptimizeResponse{}, err
	}

	defer func() {
		err := resp.Body.Close()
		if err != nil {
			ctx.GetLogger().Error("Error closing response body", "error", err)
		}
	}()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		ctx.GetLogger().Error("autopilot: error reading response", "error", err)
		return AutoOptimizeResponse{}, err
	}

	ctx.GetLogger().Info("autopilot: autopilot response received", "response", string(respBody))

	if resp.StatusCode != 200 {
		ctx.GetLogger().Error("autopilot: error sending autopilot request", "status_code", resp.StatusCode, "body", string(respBody))
		return AutoOptimizeResponse{}, errors.New("autopilot: error sending autopilot request")
	}

	data := AutoOptimizeResponse{}
	err = common.UnmarshalJson(respBody, &data)
	if err != nil {
		ctx.GetLogger().Error("autopilot: error unmarshalling response", "error", err, "body", string(respBody))
		return AutoOptimizeResponse{}, err
	}

	return data, err
}

func UpdateAutoOptimizeRule(ctx *security.RequestContext, autopilotRequest AutoOptimizeRequest, tenantId string) (map[string]any, error) {
	resp, err := common.HttpPost(config.Config.AutoPilotUrl+"/autopilot/update", common.HttpWithJsonBody(map[string]any{
		"input": map[string]any{
			"arg1": autopilotRequest,
		},
		"session_variables": map[string]any{
			"user_id":   "00000000-0000-0000-0000-000000000000",
			"tenant_id": tenantId,
		},
	}), common.HttpWithHeaders(map[string]string{
		config.Config.ServiceApiServerTokenHeader: config.Config.ServiceApiServerToken,
		"Content-Type": "application/json",
	}))

	if err != nil {
		ctx.GetLogger().Error("autopilot: error sending autopilot request", "error", err)
		return nil, err
	}

	defer func() {
		err := resp.Body.Close()
		if err != nil {
			ctx.GetLogger().Error("Error closing response body", "error", err)
		}
	}()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		ctx.GetLogger().Error("autopilot: error reading response", "error", err)
		return nil, err
	}

	ctx.GetLogger().Info("autopilot: autopilot response received", "response", string(respBody))

	if resp.StatusCode != 200 {
		ctx.GetLogger().Error("autopilot: error sending autopilot request", "status_code", resp.StatusCode)
		return nil, errors.New("autopilot: error sending autopilot request")
	}

	data := map[string]any{}
	err = common.UnmarshalJson(respBody, &data)
	if err != nil {
		ctx.GetLogger().Error("autopilot: error unmarshalling response", "error", err, "body", string(respBody))
		return nil, err
	}
	return data, nil
}

func DeleteAutoOptimizeRule(ctx *security.RequestContext, autopilotRequest AutoOptimizeRequest) error {
	return errors.ErrUnsupported
}
