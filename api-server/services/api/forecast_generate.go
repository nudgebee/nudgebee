package api

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	"nudgebee/services/common"
	"nudgebee/services/forecast"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// forecastGenerateTimeout keeps an on-demand forecast under the app's nginx 120s cap,
// so a slow metrics backend surfaces as this action's error rather than a gateway 504.
const forecastGenerateTimeout = 90 * time.Second

// forecastGenerateRequest is the input of forecast_generate. Metric names a forecast
// preset (forecast.Presets); the threshold defaults to the preset's.
type forecastGenerateRequest struct {
	AccountId string  `json:"account_id" mapstructure:"account_id"`
	Namespace string  `json:"workload_namespace" mapstructure:"workload_namespace"`
	Name      string  `json:"workload_name" mapstructure:"workload_name"`
	Kind      string  `json:"kind" mapstructure:"kind"`
	Metric    string  `json:"metric" mapstructure:"metric"`
	Container string  `json:"container" mapstructure:"container"`
	Threshold float64 `json:"threshold" mapstructure:"threshold"`
	EndTime   int64   `json:"end_time" mapstructure:"end_time"`
}

// toForecastRequest checks the input and turns it into the engine's request.
func (r forecastGenerateRequest) toForecastRequest() (forecast.Request, error) {
	if r.AccountId == "" || r.Namespace == "" || r.Name == "" {
		return forecast.Request{}, fmt.Errorf("account_id, workload_namespace and workload_name are required")
	}
	metric := strings.ToLower(strings.TrimSpace(r.Metric))
	if !forecast.IsCondition(metric) {
		return forecast.Request{}, fmt.Errorf("metric must be one of %s", strings.Join(forecast.ConditionNames(), ", "))
	}
	if r.Threshold < 0 || r.Threshold > 100 {
		return forecast.Request{}, fmt.Errorf("threshold must be a percentage between 0 and 100")
	}
	container := strings.TrimSpace(r.Container)
	if p, _ := forecast.PresetFor(forecast.Condition(metric)); p.ContainerScoped && container == "" {
		return forecast.Request{}, fmt.Errorf("%s needs a container", metric)
	}
	req := forecast.Request{
		AccountId: r.AccountId, Namespace: r.Namespace, Name: r.Name, Kind: r.Kind, Container: container,
		Condition: forecast.Condition(metric), ThresholdPct: r.Threshold,
	}
	if r.EndTime > 0 {
		req.EndTime = time.UnixMilli(r.EndTime)
	}
	return req, nil
}

// handleForecastGenerate runs one metric forecast on demand (the SLO dialog's Check
// now, and Nubi's metric_forecast tool). It creates no event.
func handleForecastGenerate(actionPayload *ActionRequest, c *gin.Context, tracer *trace.Tracer, meter *metric.Meter, logger *slog.Logger) {
	input := actionPayload.Input
	if r, ok := input["request"].(map[string]any); ok {
		input = r
	}
	var body forecastGenerateRequest
	if err := common.UnmarshalMapToStruct(input, &body); err != nil {
		c.JSON(400, common.ErrorActionBadRequest(err.Error()))
		return
	}
	req, err := body.toForecastRequest()
	if err != nil {
		c.JSON(400, common.ErrorActionBadRequest(err.Error()))
		return
	}
	ctx, err := buildContextFromPayload(c, actionPayload, tracer, meter, logger)
	if err != nil {
		c.JSON(400, common.ErrorActionBadRequest(err.Error()))
		return
	}
	if !ctx.GetSecurityContext().CanReadAccountData(req.AccountId, "metrics") {
		c.JSON(403, common.ErrorActionForbidden("access denied for account: "+req.AccountId))
		return
	}
	res, err := runObservabilityActionWithTimeout(ctx, actionPayload.Action.Name, forecastGenerateTimeout, func() (forecast.Result, error) {
		return forecast.Evaluate(ctx, req), nil
	})
	if err != nil {
		c.JSON(500, common.ErrorActionBadRequest(err.Error()))
		return
	}
	c.JSON(200, map[string]any{"data": res})
}
