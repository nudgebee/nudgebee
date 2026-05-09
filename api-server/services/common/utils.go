package common

import (
	"fmt"
	"log/slog"
	"nudgebee/services/internal/database/models"
	"strings"
)

func CheckNeighboringWorkloadHealth(evidence map[string]any, config map[string]string) ([]map[string]string, error) {
	var insights []map[string]string

	if evidence["data"] == nil {
		return insights, nil
	}

	// Step 1: Extract and unmarshal "data"
	raw, ok := evidence["data"].([]any)
	if !ok {
		slog.Warn("evidence does not contain a valid 'data' array", "evidence", slog.AnyValue(evidence))
		return nil, nil
	}

	rawData := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		m, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("invalid item in data array: not a map")
		}
		rawData = append(rawData, m)
	}

	upstreamServicesSet := make(map[string]bool)
	for _, workload := range rawData {
		// Safely extract "Id" map
		idRaw, ok := workload["Id"]
		if !ok {
			continue // Skip if "Id" key is missing
		}
		id, ok := idRaw.(map[string]any)
		if !ok {
			continue // Skip if "Id" isn't a map
		}

		// Safely extract "name"
		nameRaw, ok := id["name"]
		if !ok {
			continue
		}
		name, ok := nameRaw.(string)
		if !ok || name == "" {
			continue
		}

		// Safely extract "namespace"
		nsRaw, ok := id["namespace"]
		if !ok {
			continue
		}
		ns, ok := nsRaw.(string)
		if !ok {
			continue
		}

		// Skip based on namespace and name
		if ns == config["Namespace"] && name == config["WorkloadName"] {
			upstreamLinkRaw, ok := workload["Upstreams"]
			if !ok {
				continue // Skip if "Upstreams" key is missing
			}
			upstreamLinks, ok := upstreamLinkRaw.([]interface{})
			if !ok {
				continue
			}
			for _, linkRaw := range upstreamLinks {
				link, ok := linkRaw.(map[string]interface{})
				if !ok {
					continue
				}
				// Now safely check for the "Id" key
				upstreamLinkIdRaw, exists := link["Id"]
				if !exists {
					continue // Skip if "Id" key is missing
				}
				upstreamLinkId, ok := upstreamLinkIdRaw.(string)
				if !ok {
					continue // Skip if "Id" in upstream link isn't a string
				}
				upstreamServicesSet[upstreamLinkId] = true
			}
			break
		}
	}

	// Step 2: Loop through and validate workloads
	for _, workload := range rawData {
		// Safely extract "Id" map
		idRaw, ok := workload["Id"]
		if !ok {
			continue // Skip if "Id" key is missing
		}
		id, ok := idRaw.(map[string]any)
		if !ok {
			continue // Skip if "Id" isn't a map
		}

		// Safely extract "name"
		nameRaw, ok := id["name"]
		if !ok {
			continue
		}
		name, ok := nameRaw.(string)
		if !ok || name == "" {
			continue
		}

		// Safely extract "namespace"
		nsRaw, ok := id["namespace"]
		if !ok {
			continue
		}
		ns, ok := nsRaw.(string)
		if !ok {
			continue
		}

		// Skip based on namespace and name
		if ns == "" || ns == "external" || (ns == config["Namespace"] && name == config["WorkloadName"]) {
			continue
		}

		// Safely check "IsHealthy"
		isHealthy, ok := workload["IsHealthy"].(bool)
		if !ok || isHealthy {
			continue
		}

		// Optionally get health reason as string, fallback to "unknown" if missing or wrong type
		reason := "unknown"
		if r, ok := workload["HealthReason"].(string); ok {
			reason = r
		}
		workloadTypeRaw, ok := id["kind"]
		if !ok {
			continue
		}
		workloadType, ok := workloadTypeRaw.(string)
		if !ok {
			continue
		}
		expectedUpstreamId := fmt.Sprintf("%s:%s:%s", ns, workloadType, name)

		var message string
		var severity string

		if upstreamServicesSet[expectedUpstreamId] {
			message = fmt.Sprintf("Critical upstream service %s in %s is unhealthy, reason: %s. This may directly impact the current workload.", name, ns, reason)
			severity = "Critical"
		} else {
			message = fmt.Sprintf("Found %s in %s as unhealthy, reason: %s", name, ns, reason)
			severity = "Info" // Lower severity for non-upstream services
		}

		// Add insight
		insights = append(insights, map[string]string{
			"message":  message,
			"severity": severity,
		})
	}

	return insights, nil
}

func IsEmptyStringPointer(s *string) bool {
	return s == nil || *s == ""
}

func MapFlatten(prefix string, in map[string]interface{}, out map[string]string) {
	for k, v := range in {
		// Build new key with prefix
		var newKey string
		if prefix == "" {
			newKey = k
		} else {
			newKey = prefix + "." + k
		}

		switch val := v.(type) {
		case map[string]interface{}:
			// Recursively flatten nested objects
			MapFlatten(newKey, val, out)
		case string:
			// Direct string value
			out[newKey] = val
		default:
			// Convert non-string values to string
			out[newKey] = fmt.Sprintf("%v", val)
		}
	}
}

func JsonToStringMap(j *models.Json) map[string]string {
	if j == nil || j.IsArray() {
		return nil
	}

	obj, ok := j.Object().(map[string]any)
	if !ok {
		return nil
	}

	out := make(map[string]string, len(obj))
	for k, v := range obj {
		switch val := v.(type) {
		case string:
			out[k] = val
		default:
			out[k] = fmt.Sprint(val)
		}
	}

	return out
}

func StrVal(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func GetString(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	if s, ok := m[key].(string); ok {
		return s
	}
	return ""
}

// ConvertPythonDictToJSON converts Python dict format (single quotes, True/False/None) to valid JSON.
// This is needed because the relay/pod execution pipeline sometimes converts JSON to Python dict format.
// Pure Go implementation that handles:
// - Single quotes → double quotes
// - True → true, False → false, None → null
// - Properly escapes double quotes inside single-quoted strings
func ConvertPythonDictToJSON(pythonDict string) (string, error) {
	var sb strings.Builder
	inString := false
	stringChar := byte(0)
	i := 0

	for i < len(pythonDict) {
		c := pythonDict[i]

		if !inString {
			// Not inside a string
			if c == '\'' {
				// Start of string with single quote - convert to double quote
				sb.WriteByte('"')
				inString = true
				stringChar = '\''
			} else if c == '"' {
				// Start of string with double quote
				sb.WriteByte('"')
				inString = true
				stringChar = '"'
			} else if c == 'T' && i+4 <= len(pythonDict) && pythonDict[i:i+4] == "True" {
				// Check if it's a standalone True (not part of a word)
				if (i == 0 || !isAlphaNumeric(pythonDict[i-1])) && (i+4 >= len(pythonDict) || !isAlphaNumeric(pythonDict[i+4])) {
					sb.WriteString("true")
					i += 3
				} else {
					sb.WriteByte(c)
				}
			} else if c == 'F' && i+5 <= len(pythonDict) && pythonDict[i:i+5] == "False" {
				if (i == 0 || !isAlphaNumeric(pythonDict[i-1])) && (i+5 >= len(pythonDict) || !isAlphaNumeric(pythonDict[i+5])) {
					sb.WriteString("false")
					i += 4
				} else {
					sb.WriteByte(c)
				}
			} else if c == 'N' && i+4 <= len(pythonDict) && pythonDict[i:i+4] == "None" {
				if (i == 0 || !isAlphaNumeric(pythonDict[i-1])) && (i+4 >= len(pythonDict) || !isAlphaNumeric(pythonDict[i+4])) {
					sb.WriteString("null")
					i += 3
				} else {
					sb.WriteByte(c)
				}
			} else {
				sb.WriteByte(c)
			}
		} else {
			// Inside a string
			if c == '\\' && i+1 < len(pythonDict) {
				// Escape sequence - preserve it
				sb.WriteByte(c)
				i++
				sb.WriteByte(pythonDict[i])
			} else if c == stringChar {
				// End of string
				sb.WriteByte('"')
				inString = false
			} else if c == '"' && stringChar == '\'' {
				// Double quote inside single-quoted string - must escape for JSON
				sb.WriteString("\\\"")
			} else {
				sb.WriteByte(c)
			}
		}
		i++
	}

	return sb.String(), nil
}

// isAlphaNumeric checks if a byte is alphanumeric or underscore
func isAlphaNumeric(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_'
}
