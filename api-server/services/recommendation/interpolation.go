package recommendation

import (
	"nudgebee/services/internal/database/models"
	"regexp"
)

var templateVarRegex = regexp.MustCompile(`\{\{(\w+(?:\.\w+)*)\}\}`)

// Hoisted from BuildVariableMap — regexp.MustCompile costs ~1-5µs + heap allocs per call.
var resourceGroupRegex = regexp.MustCompile(`(?i)/resourceGroups/([^/]+)/`)

// InterpolateMitigationsJson interpolates template variables in a Json array of mitigations.
func InterpolateMitigationsJson(mitigations models.Json, vars map[string]string) models.Json {
	if !mitigations.IsArray() {
		return mitigations
	}
	interpolated := InterpolateMitigations(mitigations.Array(), vars)
	return models.NewJsonArray(interpolated)
}

func InterpolateMitigations(mitigations []any, vars map[string]string) []any {
	result := make([]any, len(mitigations))
	for i, m := range mitigations {
		s, ok := m.(string)
		if !ok {
			result[i] = m
			continue
		}
		result[i] = templateVarRegex.ReplaceAllStringFunc(s, func(match string) string {
			key := match[2 : len(match)-2]
			if val, ok := vars[key]; ok && val != "" {
				return val
			}
			return match
		})
	}
	return result
}

func BuildVariableMap(recommendationData map[string]any, resourceId string, resourceRegion string) map[string]string {
	vars := map[string]string{}
	if resourceRegion != "" {
		vars["region"] = resourceRegion
		vars["resource_region"] = resourceRegion
	}
	if resourceId != "" {
		if m := resourceGroupRegex.FindStringSubmatch(resourceId); len(m) > 1 {
			vars["resource_group"] = m[1]
		}
	}
	for k, v := range recommendationData {
		if s, ok := v.(string); ok {
			vars[k] = s
			vars["recommendation."+k] = s
		}
	}
	return vars
}
