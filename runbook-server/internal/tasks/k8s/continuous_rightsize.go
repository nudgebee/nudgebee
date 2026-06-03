package k8s

import (
	"fmt"
	"nudgebee/runbook/common"
	"nudgebee/runbook/internal/tasks/types"
	"nudgebee/runbook/services/relay"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"k8s.io/apimachinery/pkg/api/resource"
)

type ContinuousRightsizeTask struct{}

func (t *ContinuousRightsizeTask) GetName() string {
	return "k8s.continuous_rightsize"
}

func (t *ContinuousRightsizeTask) GetDescription() string {
	return "Continuously monitor and auto-adjust resources for Kubernetes workloads."
}

func (t *ContinuousRightsizeTask) GetDisplayName() string {
	return "Continuous Rightsize"
}

func (t *ContinuousRightsizeTask) Execute(taskCtx types.TaskContext, params map[string]any) (any, error) {
	taskCtx.GetLogger().Debug("Executing ContinuousRightsizeTask", "params", params)

	if paramsStr, err := common.MarshalJson(params); err == nil {
		taskCtx.GetLogger().Info("params", "params", paramsStr)
	}

	accountID := taskCtx.GetAccountID()
	if id, ok := params["account_id"].(string); ok && id != "" {
		accountID = id
	}

	// AutoOptimize Generator pushes a pre-built action_params via task.Meta.
	// Manual workflow triggers go through the form fields below and we
	// construct action_params ourselves.
	actionParams, ok := params["action_params"].(map[string]any)
	if !ok {
		var err error
		actionParams, err = buildContinuousRightsizeActionParams(taskCtx, accountID, params)
		if err != nil {
			return nil, err
		}
	}

	// Prepare Relay Body
	body := relay.ActionExecuteBody{
		AccountID:    accountID,
		ActionName:   "continuous_rightsizing",
		ActionParams: actionParams,
		Origin:       "runbook-server",
		Timeout:      10 * time.Minute, // Give analysis time
	}

	// Execute Relay
	// We need to use ExecuteRelay directly because this is a custom agent action, not a standard CLI tool
	resp, err := relay.ExecuteRelay(body)
	if err != nil {
		return nil, fmt.Errorf("failed to execute relay action: %w", err)
	}

	// The agent reports in-band failures via the response body (success:false
	// and/or status_code>=400) while the HTTP call itself returns 200, so
	// ExecuteRelay forwards the body verbatim. Without this inspection the
	// task ends up COMPLETED with a failure payload and no surfaced error.
	if err := checkContinuousRightsizeAgentResp(taskCtx, resp); err != nil {
		return nil, err
	}

	return resp, nil
}

// checkContinuousRightsizeAgentResp surfaces in-band failures from the
// continuous_rightsizing agent action. Returns nil when the response looks
// healthy, otherwise an error mentioning the agent status_code and
// request_id so operators can correlate with agent-side logs.
func checkContinuousRightsizeAgentResp(taskCtx types.TaskContext, resp map[string]any) error {
	if resp == nil {
		return nil
	}

	statusCode, _ := readFloat(resp, "status_code")
	if statusCode == 0 {
		// Some agent paths nest the action result under "result" or "data".
		if r, ok := resp["result"].(map[string]any); ok {
			if v, _ := readFloat(r, "status_code"); v != 0 {
				statusCode = v
			}
		}
	}

	requestID := readStringOr(resp, "request_id", "")
	innerData, _ := resp["data"].(map[string]any)
	if innerData != nil {
		if requestID == "" {
			requestID = readStringOr(innerData, "request_id", "")
		}
	}

	successFlag := true
	if v, ok := readBool(resp, "success"); ok {
		successFlag = v
	} else if innerData != nil {
		if v, ok := readBool(innerData, "success"); ok {
			successFlag = v
		}
	}

	failed := !successFlag || (statusCode != 0 && statusCode >= 400)
	if !failed {
		return nil
	}

	msg := readStringOr(resp, "msg", "")
	if msg == "" && innerData != nil {
		msg = readStringOr(innerData, "msg", "")
		if msg == "" {
			msg = readStringOr(innerData, "error", "")
		}
	}
	if msg == "" {
		msg = "agent reported failure with no message"
	}

	taskCtx.GetLogger().Error("continuous_rightsizing agent failure", "status_code", statusCode, "request_id", requestID, "response", resp)

	if requestID != "" {
		return fmt.Errorf("continuous_rightsizing agent failed (status %d, request %s): %s", int(statusCode), requestID, msg)
	}
	return fmt.Errorf("continuous_rightsizing agent failed (status %d): %s", int(statusCode), msg)
}

func readBool(m map[string]any, key string) (bool, bool) {
	if m == nil {
		return false, false
	}
	if v, ok := m[key].(bool); ok {
		return v, true
	}
	return false, false
}

// buildContinuousRightsizeActionParams shapes the flat form-field params into
// the {settings, applications} payload the agent expects. Mirrors the
// AutoOptimize Generator (services/optimizer/generator_continuous_rightsize.go).
func buildContinuousRightsizeActionParams(taskCtx types.TaskContext, accountID string, params map[string]any) (map[string]any, error) {
	namespace, _ := params["namespace"].(string)
	name, _ := params["name"].(string)
	kind, _ := params["kind"].(string)
	if namespace == "" || name == "" || kind == "" {
		return nil, fmt.Errorf("namespace, name, and kind are required")
	}

	cpuCfg, _ := params["cpu"].(map[string]any)
	memCfg, _ := params["memory"].(map[string]any)

	cpuMinFloor, _ := readFloat(cpuCfg, "min_request")

	memMinFloor := int64(0)
	if memMinStr, _ := readString(memCfg, "min_request"); memMinStr != "" {
		q, err := resource.ParseQuantity(memMinStr)
		if err != nil {
			return nil, fmt.Errorf("invalid memory.min_request %q: %w", memMinStr, err)
		}
		memMinFloor = q.Value() / (1024 * 1024)
	}

	cpuPct := percentileFromAlgorithm(readStringOr(cpuCfg, "algorithm", ""))
	memPct := percentileFromAlgorithm(readStringOr(memCfg, "algorithm", ""))

	cpuMinChange, _ := readFloat(cpuCfg, "min_change_pct")
	memMinChange, _ := readFloat(memCfg, "min_change_pct")

	// change_threshold is a global field on the agent settings; use the
	// lower of the two min-change percentages so neither resource is
	// thresholded out by a stricter setting on the other.
	threshold := cpuMinChange
	if memMinChange > 0 && (threshold == 0 || memMinChange < threshold) {
		threshold = memMinChange
	}

	oomFactor, _ := params["oom_kill_increase_factor"].(float64)
	duration, _ := params["analysis_duration_hour"].(float64)
	recommendOnly, _ := params["recommend_only"].(bool)

	identifier := fmt.Sprintf("%s/%s/%s/%s", taskCtx.GetTenantID(), accountID, taskCtx.GetWorkflowID(), uuid.New().String())

	// Match the AutoOptimize Generator's payload exactly so the agent gets
	// a known-good shape — 9 settings keys, no extras. The buffer / min /
	// max change form fields are accepted on the input but not yet plumbed
	// here; the agent currently rejects unknown settings keys with an empty
	// 500 (pydantic extra='forbid'). Re-add the new keys only after the
	// agent learns about them.
	settings := map[string]any{
		"default_min_cpu":                cpuMinFloor,
		"default_min_memory":             memMinFloor,
		"oom_kill_increase_factor":       oomFactor,
		"change_threshold":               int(threshold),
		"cpu_analysis_percentile":        cpuPct,
		"memory_analysis_percentile":     memPct,
		"default_analysis_duration_hour": int(duration),
		"recommend_only":                 recommendOnly,
		"identifier":                     identifier,
	}

	applications := []any{
		map[string]string{
			"name":      name,
			"namespace": namespace,
			"kind":      kind,
		},
	}

	return map[string]any{
		"settings":     settings,
		"applications": applications,
	}, nil
}

// percentileFromAlgorithm maps the form-field algorithm value to an integer
// percentile. "NB Algo" (Nudgebee heuristic) → 0 signals the agent to use
// its default algorithm; "P95" / "P99" / etc. forward the percentile.
func percentileFromAlgorithm(algo string) int {
	if algo == "" || strings.EqualFold(algo, "NB Algo") {
		return 0
	}
	if strings.HasPrefix(algo, "P") {
		if v, err := strconv.Atoi(strings.TrimPrefix(algo, "P")); err == nil {
			return v
		}
	}
	return 0
}

func readFloat(m map[string]any, key string) (float64, bool) {
	if m == nil {
		return 0, false
	}
	switch v := m[key].(type) {
	case float64:
		return v, true
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	case string:
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f, true
		}
	}
	return 0, false
}

func readString(m map[string]any, key string) (string, bool) {
	if m == nil {
		return "", false
	}
	if s, ok := m[key].(string); ok {
		return s, true
	}
	return "", false
}

func readStringOr(m map[string]any, key, def string) string {
	if s, ok := readString(m, key); ok && s != "" {
		return s
	}
	return def
}

func (t *ContinuousRightsizeTask) InputSchema() *types.Schema {
	cpuAlgoOptions := []string{"NB Algo", "P99", "P97", "P95"}
	memAlgoOptions := []string{"NB Algo"}
	cpuBufferOptions := []string{"0", "5", "10", "15"}
	memBufferOptions := []string{"0", "5", "10", "15", "20"}

	return &types.Schema{
		Properties: map[string]types.Property{
			"account_id": {
				Type:     types.PropertyTypeAccount,
				Required: true,
				Title:    "Account",
				Order:    1,
			},
			"namespace": {
				Type:      types.PropertyTypeString,
				Required:  true,
				Title:     "Namespace",
				Order:     2,
				DependsOn: []string{"account_id"},
			},
			"kind": {
				Type:      types.PropertyTypeString,
				Required:  true,
				Title:     "Kind",
				Options:   []string{"Deployment", "StatefulSet", "DaemonSet", "ReplicaSet", "Rollout"},
				Order:     3,
				DependsOn: []string{"account_id"},
			},
			"name": {
				Type:      types.PropertyTypeString,
				Required:  true,
				Title:     "Name",
				Order:     4,
				DependsOn: []string{"account_id", "namespace", "kind"},
			},
			"analysis_duration_hour": {
				Type:        types.PropertyTypeNumber,
				Title:       "Analysis Duration (hours)",
				Description: "Lookback window the agent uses when sampling resource usage.",
				Default:     24,
				Order:       5,
			},
			"cpu": {
				Type:        types.PropertyTypeObject,
				Title:       "CPU",
				Description: "How CPU is sized: which algorithm the analysis uses, headroom buffer, change-trigger thresholds, and the floor for the CPU request.",
				Order:       6,
				Schema: &types.Schema{
					Properties: map[string]types.Property{
						"algorithm": {
							Type:        types.PropertyTypeString,
							Title:       "Recommended (Based on)",
							Description: "Algorithm used to compute the CPU recommendation.",
							Options:     cpuAlgoOptions,
							Default:     "NB Algo",
							Order:       1,
						},
						"buffer_pct": {
							Type:        types.PropertyTypeString,
							Title:       "Add Buffer",
							Description: "Headroom added on top of the recommendation.",
							Options:     cpuBufferOptions,
							Default:     "0",
							Order:       2,
						},
						"min_change_pct": {
							Type:        types.PropertyTypeNumber,
							Title:       "Minimum Change (%)",
							Description: "Do not trigger a CPU change when the percent difference is below this value.",
							Default:     10,
							Order:       3,
						},
						"max_change_pct": {
							Type:        types.PropertyTypeNumber,
							Title:       "Maximum Change (%)",
							Description: "Do not trigger a CPU change when the percent difference is above this value.",
							Default:     100,
							Order:       4,
						},
						"min_request": {
							Type:        types.PropertyTypeNumber,
							Title:       "Min CPU Floor (cores)",
							Description: "Minimum CPU request the recommendation can produce, in cores (e.g. 0.01 = 10m).",
							Default:     0.01,
							Order:       5,
						},
					},
				},
			},
			"memory": {
				Type:        types.PropertyTypeObject,
				Title:       "Memory",
				Description: "How memory is sized: algorithm, buffer, change-trigger thresholds, and the memory request floor.",
				Order:       7,
				Schema: &types.Schema{
					Properties: map[string]types.Property{
						"algorithm": {
							Type:        types.PropertyTypeString,
							Title:       "Recommended (Based on)",
							Description: "Algorithm used to compute the memory recommendation.",
							Options:     memAlgoOptions,
							Default:     "NB Algo",
							Order:       1,
						},
						"buffer_pct": {
							Type:        types.PropertyTypeString,
							Title:       "Add Buffer",
							Description: "Headroom added on top of the recommendation.",
							Options:     memBufferOptions,
							Default:     "0",
							Order:       2,
						},
						"min_change_pct": {
							Type:        types.PropertyTypeNumber,
							Title:       "Minimum Change (%)",
							Description: "Do not trigger a memory change when the percent difference is below this value.",
							Default:     10,
							Order:       3,
						},
						"max_change_pct": {
							Type:        types.PropertyTypeNumber,
							Title:       "Maximum Change (%)",
							Description: "Do not trigger a memory change when the percent difference is above this value.",
							Default:     100,
							Order:       4,
						},
						"min_request": {
							Type:        types.PropertyTypeString,
							Title:       "Min Memory Floor",
							Description: "Minimum memory request as a Kubernetes quantity (e.g. '100Mi', '256Mi').",
							Default:     "100Mi",
							Order:       5,
						},
					},
				},
			},
			"oom_kill_increase_factor": {
				Type:        types.PropertyTypeNumber,
				Title:       "OOM Kill Increase Factor",
				Description: "Multiplier applied when an OOMKill is observed (e.g. 1.5 = +50%).",
				Default:     1.5,
				Order:       8,
			},
			"recommend_only": {
				Type:        types.PropertyTypeBoolean,
				Title:       "Recommend Only",
				Description: "If true, surface the recommendation without applying any change.",
				Default:     false,
				Order:       9,
			},
		},
	}
}

func (t *ContinuousRightsizeTask) OutputSchema() *types.Schema {
	return &types.Schema{
		Properties: map[string]types.Property{
			"data": {Type: types.PropertyTypeObject},
		},
	}
}
