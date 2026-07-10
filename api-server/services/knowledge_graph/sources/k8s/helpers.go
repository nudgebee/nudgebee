package k8s

import (
	"encoding/json"
	"fmt"
	"net"
	"strings"
)

// parseRelayDataArray extracts and parses the data array from a relay response,
// handling potentially nested JSON structures.
// Returns the parsed array and any error encountered during navigation or parsing.
func (s *K8sSource) parseRelayDataArray(response map[string]interface{}, resourceType string) ([]interface{}, error) {
	// Navigate: response -> data -> findings -> evidence -> data (JSON string) -> array
	data, ok := response["data"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("response.data is not a map")
	}

	// Treat a missing / null `findings` as "no findings". The relay returns
	// this shape when the requested resource type does not exist in the
	// cluster — e.g. Karpenter CRDs on a GKE cluster, which currently
	// surface as a noisy WARN with full stack trace on every build:
	//
	//   level=WARN msg="failed to parse Karpenter CRD response"
	//   resource_type=nodepools error="response.data.findings is not an array"
	//
	// Distinguish only the truly-malformed case (key present, non-null,
	// non-array) from the benign empty-cluster case.
	findingsRaw, present := data["findings"]
	if !present || findingsRaw == nil {
		s.logger.Info("no findings in response (key absent or null)",
			"resource_type", resourceType)
		return []interface{}{}, nil
	}
	findings, ok := findingsRaw.([]interface{})
	if !ok {
		return nil, fmt.Errorf("response.data.findings is not an array")
	}

	if len(findings) == 0 {
		s.logger.Info("no findings in response", "resource_type", resourceType)
		return []interface{}{}, nil
	}

	// Get first finding
	firstFinding, ok := findings[0].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("first finding is not a map")
	}

	// Evidence is an array containing maps
	evidenceArray, ok := firstFinding["evidence"].([]interface{})
	if !ok {
		return nil, fmt.Errorf("finding.evidence is not an array")
	}

	if len(evidenceArray) == 0 {
		return nil, fmt.Errorf("finding.evidence array is empty")
	}

	// Get the first evidence item
	evidenceItem, ok := evidenceArray[0].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("evidence item is not a map")
	}

	dataStr, ok := evidenceItem["data"].(string)
	if !ok {
		return nil, fmt.Errorf("evidence.data is not a string")
	}

	// Parse the data JSON string
	var dataArray []interface{}
	if err := json.Unmarshal([]byte(dataStr), &dataArray); err != nil {
		return nil, fmt.Errorf("failed to unmarshal evidence.data: %w", err)
	}

	// Handle nested structure: dataArray might contain a wrapper with type:"json" and data field
	var actualDataArray []interface{}
	if len(dataArray) > 0 {
		if wrapperMap, ok := dataArray[0].(map[string]interface{}); ok {
			if dataType, exists := wrapperMap["type"]; exists && dataType == "json" {
				// Extract nested data field
				if nestedDataStr, ok := wrapperMap["data"].(string); ok {
					if err := json.Unmarshal([]byte(nestedDataStr), &actualDataArray); err != nil {
						return nil, fmt.Errorf("failed to unmarshal nested evidence.data: %w", err)
					}
				} else {
					return nil, fmt.Errorf("nested data field is not a string")
				}
			} else {
				// No wrapper, use dataArray as-is
				actualDataArray = dataArray
			}
		} else {
			// Not a wrapper map, use dataArray as-is
			actualDataArray = dataArray
		}
	}

	return actualDataArray, nil
}

// parseCPUValue parses Kubernetes CPU resource values into cores
// Examples: "500m" -> 0.5, "2" -> 2.0, "1000m" -> 1.0
func (s *K8sSource) parseCPUValue(value string) float64 {
	if value == "" {
		return 0
	}

	// Handle millicores (e.g., "500m")
	if strings.HasSuffix(value, "m") {
		cpuStr := strings.TrimSuffix(value, "m")
		var cpuVal float64
		if _, err := fmt.Sscanf(cpuStr, "%f", &cpuVal); err == nil {
			return cpuVal / 1000.0 // Convert millicores to cores
		}
	}

	// Handle plain number (cores, e.g., "2")
	var cpuVal float64
	if _, err := fmt.Sscanf(value, "%f", &cpuVal); err == nil {
		return cpuVal
	}

	return 0
}

// parseMemoryValue parses Kubernetes memory resource values into bytes
// Examples: "512Mi" -> 536870912, "1Gi" -> 1073741824, "100M" -> 100000000
func (s *K8sSource) parseMemoryValue(value string) float64 {
	if value == "" {
		return 0
	}

	var memVal float64
	var unit string

	// Try to parse with unit
	if n, err := fmt.Sscanf(value, "%f%s", &memVal, &unit); err == nil && n == 2 {
		switch unit {
		case "Ki":
			return memVal * 1024
		case "Mi":
			return memVal * 1024 * 1024
		case "Gi":
			return memVal * 1024 * 1024 * 1024
		case "Ti":
			return memVal * 1024 * 1024 * 1024 * 1024
		case "Pi":
			return memVal * 1024 * 1024 * 1024 * 1024 * 1024
		case "K", "k":
			return memVal * 1000
		case "M":
			return memVal * 1000 * 1000
		case "G":
			return memVal * 1000 * 1000 * 1000
		case "T":
			return memVal * 1000 * 1000 * 1000 * 1000
		case "P":
			return memVal * 1000 * 1000 * 1000 * 1000 * 1000
		}
	}

	// Try parsing as plain number (bytes)
	if _, err := fmt.Sscanf(value, "%f", &memVal); err == nil {
		return memVal
	}

	return 0
}

// isRFC1918IPv4 reports whether the given dotted-quad string is a private
// IPv4 address (10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16). Used to recover
// a Node's InternalIP from the collector's flat addresses list when the
// (type,address) shape is no longer available.
func isRFC1918IPv4(ip string) bool {
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return false
	}
	v4 := parsed.To4()
	if v4 == nil {
		return false
	}
	switch {
	case v4[0] == 10:
		return true
	case v4[0] == 172 && v4[1] >= 16 && v4[1] <= 31:
		return true
	case v4[0] == 192 && v4[1] == 168:
		return true
	}
	return false
}

// formatBytesToHumanReadable converts bytes to human-readable format using binary units (Ki, Mi, Gi, Ti)
// Examples: 536870912 -> "512Mi", 1073741824 -> "1Gi", 1536 -> "1.5Ki"
func (s *K8sSource) formatBytesToHumanReadable(bytes float64) string {
	if bytes == 0 {
		return "0"
	}

	const (
		Ki = 1024
		Mi = 1024 * 1024
		Gi = 1024 * 1024 * 1024
		Ti = 1024 * 1024 * 1024 * 1024
		Pi = 1024 * 1024 * 1024 * 1024 * 1024
	)

	// Select appropriate unit
	switch {
	case bytes >= Pi:
		return fmt.Sprintf("%.2fPi", bytes/Pi)
	case bytes >= Ti:
		return fmt.Sprintf("%.2fTi", bytes/Ti)
	case bytes >= Gi:
		return fmt.Sprintf("%.2fGi", bytes/Gi)
	case bytes >= Mi:
		return fmt.Sprintf("%.2fMi", bytes/Mi)
	case bytes >= Ki:
		return fmt.Sprintf("%.2fKi", bytes/Ki)
	default:
		return fmt.Sprintf("%.0f", bytes)
	}
}

// parseResourceValue determines resource type and parses accordingly
// CPU values (with 'm' suffix or small numbers) -> cores
// Memory values (with size units) -> bytes
func (s *K8sSource) parseResourceValue(value string) float64 {
	if value == "" {
		return 0
	}

	// CPU resource (millicores or cores)
	if strings.HasSuffix(value, "m") {
		return s.parseCPUValue(value)
	}

	// Memory resource (has unit suffix)
	if strings.ContainsAny(value, "KMGTPikKMGTP") {
		return s.parseMemoryValue(value)
	}

	// Plain number - assume CPU cores
	var numVal float64
	if _, err := fmt.Sscanf(value, "%f", &numVal); err == nil {
		return numVal
	}

	return 0
}
