package k8s

import (
	"fmt"
	"math"

	"k8s.io/apimachinery/pkg/api/resource"
)

var cpuAlgoMapping = map[string]string{
	"P99": "cpu_percentile_99",
	"P97": "cpu_percentile_97",
	"P95": "cpu_percentile_95",
	"P92": "cpu_percentile_92",
}

const (
	DefaultMinCPU    = "10m"
	DefaultMinMemory = "100Mi"
)

type RightsizeRules struct {
	CPU       map[string]any
	Memory    map[string]any
	Direction string
}

func (r *RightsizeRules) ApplyCPURules(containerName string, recommendation map[string]any, allocated map[string]any) (newRequest, newLimit *string, errors []string) {
	requestVal, limitVal := r.applyCPUAlgo(recommendation, allocated)

	// apply_buffer
	requestVal, limitVal = r.applyCPUBuffer(requestVal, limitVal)

	// check_cpu_min_change
	requestVal, limitVal, err := r.checkCPUMinChange(requestVal, limitVal, allocated, containerName)
	if err != nil {
		errors = append(errors, fmt.Sprintf("[CPU Min Change] %v", err))
	}

	// check_direction
	if requestVal != nil || limitVal != nil {
		var errDir error
		requestVal, limitVal, errDir = r.checkCPUDirection(requestVal, limitVal, allocated, containerName)
		if errDir != nil {
			// If direction check fails (e.g. trying to scale down when direction is up), we just skip the change silently or log?
			// Typically we just filter it out. The user asked for "up", so we only provide "up".
			// Python implementation typically filters silently or adds a reason.
			// Let's add it to errors for visibility in the result description if all changes are skipped.
			errors = append(errors, fmt.Sprintf("[CPU Direction] %v", errDir))
		}
	}

	if requestVal != nil || limitVal != nil {
		// check_cpu_max_change
		requestVal, limitVal, err = r.checkCPUMaxChange(requestVal, limitVal, allocated, containerName)
		if err != nil {
			errors = append(errors, fmt.Sprintf("[CPU Max Change] %v", err))
		}
	}

	if requestVal != nil || limitVal != nil {
		// check_max_cpu
		var errs []string
		requestVal, limitVal, errs = r.checkCPUMaxThreshold(requestVal, limitVal, containerName)
		if len(errs) > 0 {
			errors = append(errors, errs...)
		}

		// check_cpu_threshold (Min)
		requestVal, limitVal, errs = r.checkCPUMinThreshold(requestVal, limitVal, containerName)
		if len(errs) > 0 {
			errors = append(errors, errs...)
		}

		// check_with_allocated
		requestVal, limitVal, errs = r.checkCPUWithAllocated(requestVal, limitVal, allocated, containerName)
		if len(errs) > 0 {
			errors = append(errors, errs...)
		}
	}

	return r.toString(requestVal), r.toString(limitVal), errors
}

func (r *RightsizeRules) applyCPUAlgo(recommendation map[string]any, allocated map[string]any) (request, limit *float64) {
	algo, _ := r.CPU["algo"].(string)
	if algoKey, ok := cpuAlgoMapping[algo]; ok {
		if addInfo, ok := recommendation["add_info"].(map[string]any); ok {
			if val := r.parseToFloat(addInfo[algoKey]); val != nil {
				return val, nil // CPU limit is typically nil for P99/P95
			}
		}
	}

	// Fallback to recommended
	if recommended, ok := recommendation["recommended"].(map[string]any); ok {
		req := r.parseToFloat(recommended["request"])
		lim := r.parseToFloat(recommended["limit"])
		return req, lim
	}

	// Manual scale fallback: use allocated as base, then scale by change_pct
	// in the requested direction (e.g. up + 20% on a 100m request → 120m).
	// When the container has neither request nor limit set, bootstrap both
	// from cpu.min so scale-up still produces a patch. We require BOTH to be
	// missing — bootstrapping just one side from min could produce
	// request > limit (e.g. existing 500m request + bootstrapped 100m limit).
	req := r.parseToFloat(allocated["request"])
	lim := r.parseToFloat(allocated["limit"])
	if req == nil && lim == nil {
		if minBase := r.parseToFloat(r.CPU["min"]); minBase != nil {
			v := *minBase
			req = &v
			lim = &v
		}
	}
	return r.scaleByChangePct(req, lim, r.CPU)
}

func (r *RightsizeRules) applyCPUBuffer(request, limit *float64) (*float64, *float64) {
	bufferPct, _ := r.CPU["buffer_pct"].(float64)
	if bufferPct <= 0 {
		return request, limit
	}

	if request != nil {
		newReq := *request * (1 + bufferPct/100.0)
		request = &newReq
	}
	if limit != nil {
		newLim := *limit * (1 + bufferPct/100.0)
		limit = &newLim
	}
	return request, limit
}

func (r *RightsizeRules) checkCPUMinChange(request, limit *float64, allocated map[string]any, containerName string) (*float64, *float64, error) {
	trigger, _ := r.CPU["trigger"].(map[string]any)
	if trigger == nil {
		return request, limit, nil
	}
	minPct, _ := trigger["change_pct"].(float64)
	if minPct <= 0 {
		return request, limit, nil
	}

	allocReq := r.parseToFloat(allocated["request"])
	allocLim := r.parseToFloat(allocated["limit"])

	maxChange := 0.0
	if request != nil && allocReq != nil && *allocReq != 0 {
		maxChange = math.Max(maxChange, math.Abs(*request-*allocReq) / *allocReq)
	}
	if limit != nil && allocLim != nil && *allocLim != 0 {
		maxChange = math.Max(maxChange, math.Abs(*limit-*allocLim) / *allocLim)
	}

	if maxChange < (minPct / 100.0) {
		return nil, nil, fmt.Errorf("[Container: %s] CPU change skipped: The recommended change (%.2f%%) is below the minimum threshold of %.2f%%", containerName, maxChange*100, minPct)
	}
	return request, limit, nil
}

func (r *RightsizeRules) checkCPUDirection(request, limit *float64, allocated map[string]any, containerName string) (*float64, *float64, error) {
	if r.Direction == "" {
		return request, limit, nil
	}

	allocReq := r.parseToFloat(allocated["request"])
	allocLim := r.parseToFloat(allocated["limit"])

	switch r.Direction {
	case "up":
		if request != nil && allocReq != nil && *request < *allocReq {
			request = nil // Block downsize
		}
		if limit != nil && allocLim != nil && *limit < *allocLim {
			limit = nil // Block downsize
		}
	case "down":
		if request != nil && allocReq != nil && *request > *allocReq {
			request = nil // Block upsize
		}
		if limit != nil && allocLim != nil && *limit > *allocLim {
			limit = nil // Block upsize
		}
	}

	if request == nil && limit == nil {
		return nil, nil, fmt.Errorf("[Container: %s] CPU change skipped: Change does not match direction '%s'", containerName, r.Direction)
	}

	return request, limit, nil
}

func (r *RightsizeRules) checkCPUMaxChange(request, limit *float64, allocated map[string]any, containerName string) (*float64, *float64, error) {
	trigger, _ := r.CPU["trigger"].(map[string]any)
	if trigger == nil {
		return request, limit, nil
	}
	maxPct, _ := trigger["max_change_pct"].(float64)
	if maxPct <= 0 {
		return request, limit, nil
	}

	allocReq := r.parseToFloat(allocated["request"])
	allocLim := r.parseToFloat(allocated["limit"])

	maxChange := 0.0
	if request != nil && allocReq != nil && *allocReq != 0 {
		maxChange = math.Max(maxChange, math.Abs(*request-*allocReq) / *allocReq)
	}
	if limit != nil && allocLim != nil && *allocLim != 0 {
		maxChange = math.Max(maxChange, math.Abs(*limit-*allocLim) / *allocLim)
	}

	if maxChange > (maxPct / 100.0) {
		return nil, nil, fmt.Errorf("[Container: %s] CPU change rejected: The recommended change (%.2f%%) exceeds the maximum allowed threshold of %.2f%%", containerName, maxChange*100, maxPct)
	}
	return request, limit, nil
}

func (r *RightsizeRules) checkCPUMaxThreshold(request, limit *float64, containerName string) (*float64, *float64, []string) {
	var errors []string
	maxVal := r.parseToFloat(r.CPU["max_cpu"])
	if maxVal == nil {
		return request, limit, nil
	}

	if request != nil && *request > *maxVal {
		errors = append(errors, fmt.Sprintf("[Container: %s] CPU request exceeds maximum threshold: Requested %.2f cores is above the maximum allowed %.2f cores. Request has been capped.", containerName, *request, *maxVal))
		request = nil
	}
	if limit != nil && *limit > *maxVal {
		errors = append(errors, fmt.Sprintf("[Container: %s] CPU limit exceeds maximum threshold: Requested %.2f cores is above the maximum allowed %.2f cores. Limit has been capped.", containerName, *limit, *maxVal))
		limit = nil
	}
	return request, limit, errors
}

func (r *RightsizeRules) checkCPUMinThreshold(request, limit *float64, containerName string) (*float64, *float64, []string) {
	var errors []string
	minVal := r.parseToFloat(r.CPU["min_cpu"])
	if minVal == nil {
		minVal = r.parseToFloat(DefaultMinCPU)
	}

	if minVal != nil {
		if request != nil && *request < *minVal {
			errors = append(errors, fmt.Sprintf("[Container: %s] CPU request below minimum threshold: Requested %.2f cores is below the minimum required %.2f cores. Request has been set to minimum.", containerName, *request, *minVal))
			request = minVal
		}
		if limit != nil && *limit < *minVal {
			errors = append(errors, fmt.Sprintf("[Container: %s] CPU limit below minimum threshold: Requested %.2f cores is below the minimum required %.2f cores. Limit has been set to minimum.", containerName, *limit, *minVal))
			limit = minVal
		}
	}
	return request, limit, errors
}

func (r *RightsizeRules) checkCPUWithAllocated(request, limit *float64, allocated map[string]any, containerName string) (*float64, *float64, []string) {
	var errors []string
	allocReq := r.parseToFloat(allocated["request"])
	allocLim := r.parseToFloat(allocated["limit"])

	if request != nil && allocReq != nil && *request == *allocReq {
		errors = append(errors, fmt.Sprintf("[Container: %s] CPU request unchanged: The recommended CPU request matches the current allocated value. No change needed.", containerName))
		request = nil
	}
	if limit != nil && allocLim != nil && *limit == *allocLim {
		errors = append(errors, fmt.Sprintf("[Container: %s] CPU limit unchanged: The recommended CPU limit matches the current allocated value. No change needed.", containerName))
		limit = nil
	}
	return request, limit, errors
}

// Memory Rules

func (r *RightsizeRules) ApplyMemoryRules(containerName string, recommendation map[string]any, allocated map[string]any) (newRequest, newLimit *string, errors []string) {
	requestVal, limitVal := r.applyMemoryBase(recommendation, allocated)

	// apply_mem_buffer
	requestVal, limitVal = r.applyMemoryBuffer(requestVal, limitVal, recommendation)

	// apply_mem_minimum_change
	requestVal, limitVal, err := r.checkMemoryMinChange(requestVal, limitVal, allocated, containerName)
	if err != nil {
		errors = append(errors, fmt.Sprintf("[Memory Min Change] %v", err))
	}

	// check_direction
	if requestVal != nil || limitVal != nil {
		var errDir error
		requestVal, limitVal, errDir = r.checkMemoryDirection(requestVal, limitVal, allocated, containerName)
		if errDir != nil {
			errors = append(errors, fmt.Sprintf("[Memory Direction] %v", errDir))
		}
	}

	if requestVal != nil || limitVal != nil {
		// apply_mem_maxmimum_change
		requestVal, limitVal, err = r.checkMemoryMaxChange(requestVal, limitVal, allocated, containerName)
		if err != nil {
			errors = append(errors, fmt.Sprintf("[Memory Max Change] %v", err))
		}
	}

	if requestVal != nil || limitVal != nil {
		// check_mem_threshold (Max and Min)
		var errs []string
		requestVal, limitVal, errs = r.checkMemoryThresholds(requestVal, limitVal, containerName)
		if len(errs) > 0 {
			errors = append(errors, errs...)
		}

		// check_with_allocated
		requestVal, limitVal, errs = r.checkMemoryWithAllocated(requestVal, limitVal, allocated, containerName)
		if len(errs) > 0 {
			errors = append(errors, errs...)
		}
	}

	return r.toStringBytes(requestVal), r.toStringBytes(limitVal), errors
}

func (r *RightsizeRules) checkMemoryDirection(request, limit *float64, allocated map[string]any, containerName string) (*float64, *float64, error) {
	if r.Direction == "" {
		return request, limit, nil
	}

	allocReq := r.parseToFloatBytes(allocated["request"])
	allocLim := r.parseToFloatBytes(allocated["limit"])

	switch r.Direction {
	case "up":
		if request != nil && allocReq != nil && *request < *allocReq {
			request = nil
		}
		if limit != nil && allocLim != nil && *limit < *allocLim {
			limit = nil
		}
	case "down":
		if request != nil && allocReq != nil && *request > *allocReq {
			request = nil
		}
		if limit != nil && allocLim != nil && *limit > *allocLim {
			limit = nil
		}
	}

	if request == nil && limit == nil {
		return nil, nil, fmt.Errorf("[Container: %s] Memory change skipped: Change does not match direction '%s'", containerName, r.Direction)
	}

	return request, limit, nil
}

func (r *RightsizeRules) applyMemoryBase(recommendation map[string]any, allocated map[string]any) (request, limit *float64) {
	if recommended, ok := recommendation["recommended"].(map[string]any); ok {
		req := r.parseToFloatBytes(recommended["request"])
		lim := r.parseToFloatBytes(recommended["limit"])
		return req, lim
	}

	// Manual scale fallback: use allocated as base, then scale by change_pct
	// in the requested direction (e.g. up + 20% on a 1Gi request → 1.2Gi).
	// When the container has neither request nor limit set, bootstrap both
	// from memory.min so scale-up still produces a patch. We require BOTH
	// to be missing — bootstrapping just one side from min could produce
	// request > limit when only one was set on the container.
	req := r.parseToFloatBytes(allocated["request"])
	lim := r.parseToFloatBytes(allocated["limit"])
	if req == nil && lim == nil {
		if minBase := r.parseToFloatBytes(r.Memory["min"]); minBase != nil {
			v := *minBase
			req = &v
			lim = &v
		}
	}
	return r.scaleByChangePct(req, lim, r.Memory)
}

// scaleByChangePct multiplies request and limit by the user-provided
// change_pct in the requested direction. No-op when direction is empty
// or change_pct is zero/missing — callers in those cases get back the
// allocated values unchanged, which is the prior behavior.
func (r *RightsizeRules) scaleByChangePct(request, limit *float64, config map[string]any) (*float64, *float64) {
	if r.Direction == "" || config == nil {
		return request, limit
	}
	// parseToFloat handles both float64 (JSON unmarshal) and int (other
	// callers), so callers passing change_pct: 20 vs 20.0 behave the same.
	pctPtr := r.parseToFloat(config["change_pct"])
	if pctPtr == nil || *pctPtr <= 0 {
		return request, limit
	}
	pct := *pctPtr
	factor := 1.0 + pct/100.0
	if r.Direction == "down" {
		factor = 1.0 - pct/100.0
	}
	if factor <= 0 {
		// A 100%+ down scale would produce zero/negative — leave values
		// alone and let downstream threshold checks surface the issue.
		return request, limit
	}
	if request != nil {
		v := *request * factor
		request = &v
	}
	if limit != nil {
		v := *limit * factor
		limit = &v
	}
	return request, limit
}

func (r *RightsizeRules) applyMemoryBuffer(request, limit *float64, recommendation map[string]any) (*float64, *float64) {
	bufferPct, _ := r.Memory["buffer_pct"].(float64)
	if bufferPct <= 0 {
		return request, limit
	}

	addInfo, _ := recommendation["add_info"].(map[string]any)
	if addInfo == nil {
		return request, limit
	}

	// Python: request = recommendation["add_info"]["actual_recommended_request"]
	if request != nil {
		if base := r.parseToFloatBytes(addInfo["actual_recommended_request"]); base != nil {
			newReq := *base * (1 + bufferPct/100.0)
			request = &newReq
		}
	}
	if limit != nil {
		if base := r.parseToFloatBytes(addInfo["actual_recommended_limit"]); base != nil {
			newLim := *base * (1 + bufferPct/100.0)
			limit = &newLim
		}
	}
	return request, limit
}

func (r *RightsizeRules) checkMemoryMinChange(request, limit *float64, allocated map[string]any, containerName string) (*float64, *float64, error) {
	trigger, _ := r.Memory["trigger"].(map[string]any)
	if trigger == nil {
		return request, limit, nil
	}
	minPct, _ := trigger["change_pct"].(float64)
	if minPct <= 0 {
		return request, limit, nil
	}

	allocReq := r.parseToFloatBytes(allocated["request"])
	allocLim := r.parseToFloatBytes(allocated["limit"])

	maxChange := 0.0
	if request != nil && allocReq != nil && *allocReq != 0 {
		maxChange = math.Max(maxChange, math.Abs(*request-*allocReq) / *allocReq)
	}
	if limit != nil && allocLim != nil && *allocLim != 0 {
		maxChange = math.Max(maxChange, math.Abs(*limit-*allocLim) / *allocLim)
	}

	if maxChange < (minPct / 100.0) {
		return nil, nil, fmt.Errorf("[Container: %s] Memory change skipped: The recommended change (%.2f%%) is below the minimum threshold of %.2f%%", containerName, maxChange*100, minPct)
	}
	return request, limit, nil
}

func (r *RightsizeRules) checkMemoryMaxChange(request, limit *float64, allocated map[string]any, containerName string) (*float64, *float64, error) {
	trigger, _ := r.Memory["trigger"].(map[string]any)
	if trigger == nil {
		return request, limit, nil
	}
	maxPct, _ := trigger["max_change_pct"].(float64)
	if maxPct <= 0 {
		return request, limit, nil
	}

	allocReq := r.parseToFloatBytes(allocated["request"])
	allocLim := r.parseToFloatBytes(allocated["limit"])

	maxChange := 0.0
	if request != nil && allocReq != nil && *allocReq != 0 {
		maxChange = math.Max(maxChange, math.Abs(*request-*allocReq) / *allocReq)
	}
	if limit != nil && allocLim != nil && *allocLim != 0 {
		maxChange = math.Max(maxChange, math.Abs(*limit-*allocLim) / *allocLim)
	}

	if maxChange > (maxPct / 100.0) {
		return nil, nil, fmt.Errorf("[Container: %s] Memory change rejected: The recommended change (%.2f%%) exceeds the maximum allowed threshold of %.2f%%", containerName, maxChange*100, maxPct)
	}
	return request, limit, nil
}

func (r *RightsizeRules) checkMemoryThresholds(request, limit *float64, containerName string) (*float64, *float64, []string) {
	var errors []string
	maxVal := r.parseToFloatBytes(r.Memory["max_memory"])
	minVal := r.parseToFloatBytes(r.Memory["min_memory"])
	if minVal == nil {
		minVal = r.parseToFloatBytes(DefaultMinMemory)
	}

	if maxVal != nil {
		if request != nil && *request > *maxVal {
			errors = append(errors, fmt.Sprintf("[Container: %s] Memory request exceeds maximum threshold: Requested %v is above the maximum allowed %v. Request has been capped.", containerName, r.toStringBytes(request), r.toStringBytes(maxVal)))
			request = nil
		}
		if limit != nil && *limit > *maxVal {
			errors = append(errors, fmt.Sprintf("[Container: %s] Memory limit exceeds maximum threshold: Requested %v is above the maximum allowed %v. Limit has been capped.", containerName, r.toStringBytes(limit), r.toStringBytes(maxVal)))
			limit = nil
		}
	}

	if minVal != nil {
		if request != nil && *request < *minVal {
			errors = append(errors, fmt.Sprintf("[Container: %s] Memory request below minimum threshold: Requested %v is below the minimum required %v. Request has been set to minimum.", containerName, r.toStringBytes(request), r.toStringBytes(minVal)))
			request = minVal
		}
		if limit != nil && *limit < *minVal {
			errors = append(errors, fmt.Sprintf("[Container: %s] Memory limit below minimum threshold: Requested %v is below the minimum required %v. Limit has been set to minimum.", containerName, r.toStringBytes(limit), r.toStringBytes(minVal)))
			limit = minVal
		}
	}

	return request, limit, errors
}

func (r *RightsizeRules) checkMemoryWithAllocated(request, limit *float64, allocated map[string]any, containerName string) (*float64, *float64, []string) {
	var errors []string
	allocReq := r.parseToFloatBytes(allocated["request"])
	allocLim := r.parseToFloatBytes(allocated["limit"])

	if request != nil && allocReq != nil && *request == *allocReq {
		errors = append(errors, fmt.Sprintf("[Container: %s] Memory request unchanged: The recommended memory request matches the current allocated value. No change needed.", containerName))
		request = nil
	}
	if limit != nil && allocLim != nil && *limit == *allocLim {
		errors = append(errors, fmt.Sprintf("[Container: %s] Memory limit unchanged: The recommended memory limit matches the current allocated value. No change needed.", containerName))
		limit = nil
	}
	return request, limit, errors
}

// Helpers

func (r *RightsizeRules) parseToFloat(val any) *float64 {
	if val == nil {
		return nil
	}
	switch v := val.(type) {
	case float64:
		return &v
	case int64:
		f := float64(v)
		return &f
	case int:
		f := float64(v)
		return &f
	case string:
		q, err := resource.ParseQuantity(v)
		if err == nil {
			f := float64(q.MilliValue()) / 1000.0
			return &f
		}
	}
	return nil
}

func (r *RightsizeRules) parseToFloatBytes(val any) *float64 {
	if val == nil {
		return nil
	}
	switch v := val.(type) {
	case float64:
		return &v
	case int64:
		f := float64(v)
		return &f
	case int:
		f := float64(v)
		return &f
	case string:
		q, err := resource.ParseQuantity(v)
		if err == nil {
			f := float64(q.Value())
			return &f
		}
	}
	return nil
}

func (r *RightsizeRules) toString(val *float64) *string {
	if val == nil {
		return nil
	}
	// Convert cores back to string (e.g., 0.1 -> 100m)
	milli := int64(math.Round(*val * 1000.0))
	q := resource.NewMilliQuantity(milli, resource.DecimalSI)
	s := q.String()
	return &s
}

func (r *RightsizeRules) toStringBytes(val *float64) *string {
	if val == nil {
		return nil
	}
	q := resource.NewQuantity(int64(*val), resource.BinarySI)
	s := q.String()
	return &s
}
