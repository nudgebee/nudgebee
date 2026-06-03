package azure

import (
	"fmt"
	"nudgebee/collector/cloud/providers"
	"strconv"
	"strings"
)

// CalculateAzureThreshold determines the appropriate threshold value for an Azure alarm
// based on resource properties and threshold rules
func CalculateAzureThreshold(resource providers.Resource, template providers.AlarmTemplate) (float64, error) {
	rules := template.ThresholdRules

	// For percentage-based thresholds (memory)
	if rules.DefaultPercentage > 0 {
		return calculateAzurePercentageThreshold(resource, template)
	}

	// For instance family-based thresholds (VM CPU)
	if len(rules.ByInstanceFamily) > 0 {
		return calculateAzureVMFamilyThreshold(resource, rules)
	}

	// Default threshold
	return rules.Default, nil
}

// calculateAzurePercentageThreshold calculates threshold based on percentage of total capacity
// Used for: Available Memory Bytes (15% of total)
func calculateAzurePercentageThreshold(resource providers.Resource, template providers.AlarmTemplate) (float64, error) {
	rules := template.ThresholdRules
	metricName := template.Configuration.MetricName

	switch metricName {
	case "Available Memory Bytes":
		totalMemory, err := getAzureVMTotalMemoryBytes(resource)
		if err != nil {
			// Fall back to minimum bytes if we can't determine total memory
			if rules.MinimumBytes > 0 {
				return rules.MinimumBytes, nil
			}
			return rules.Default, nil
		}

		// Calculate percentage-based threshold
		threshold := totalMemory * rules.DefaultPercentage

		// Ensure minimum threshold is met
		if rules.MinimumBytes > 0 && threshold < rules.MinimumBytes {
			return rules.MinimumBytes, nil
		}

		return threshold, nil

	default:
		return rules.Default, nil
	}
}

// calculateAzureVMFamilyThreshold determines threshold based on Azure VM family
// Example: Standard_B2s -> family "B" -> 70% CPU threshold
func calculateAzureVMFamilyThreshold(resource providers.Resource, rules providers.ThresholdRules) (float64, error) {
	vmSize := getAzureVMSize(resource)
	if vmSize == "" {
		return rules.Default, nil
	}

	family := extractAzureVMFamily(vmSize)
	if family == "" {
		return rules.Default, nil
	}

	// Check if we have a specific threshold for this family
	if threshold, ok := rules.ByInstanceFamily[family]; ok {
		return threshold, nil
	}

	// Fall back to default
	return rules.Default, nil
}

// getAzureVMSize extracts VM size from resource metadata
func getAzureVMSize(resource providers.Resource) string {
	if len(resource.Meta) == 0 {
		return ""
	}

	// Try properties -> hardwareProfile -> vmSize
	if props, ok := resource.Meta["properties"].(map[string]interface{}); ok {
		if hwProfile, ok := props["hardwareProfile"].(map[string]interface{}); ok {
			if vmSize, ok := hwProfile["vmSize"].(string); ok {
				return vmSize
			}
		}
	}

	return ""
}

// extractAzureVMFamily extracts the family letter(s) from Azure VM size
// Examples:
//
//	"Standard_B2s"     -> "B"
//	"Standard_D4s_v3"  -> "D"
//	"Standard_DS2_v2"  -> "D"
//	"Standard_E8s_v5"  -> "E"
//	"Standard_F4s_v2"  -> "F"
//	"Standard_M32ms"   -> "M"
//	"Standard_L8s_v3"  -> "L"
//	"Standard_FX4mds"  -> "FX"
//	"Standard_Da4_v5"  -> "Da"
//	"Standard_Ea4_v5"  -> "Ea"
//	"Standard_Fs2"     -> "Fs"
//	"Standard_Ls4"     -> "Ls"
//	"Standard_Es4"     -> "Es"
//	"Standard_Ds2_v2"  -> "Ds"
func extractAzureVMFamily(vmSize string) string {
	// Remove "Standard_" prefix
	size := strings.TrimPrefix(vmSize, "Standard_")
	if size == vmSize {
		// No prefix found, try lowercase
		size = strings.TrimPrefix(vmSize, "standard_")
	}
	if size == "" {
		return ""
	}

	// Extract family letters from the beginning
	// Azure VM sizes follow pattern: <Family>[<Subfamily>]<Size>[<Feature>][_<Version>]
	// Family is 1-2 uppercase letters at the start, followed by optional lowercase letters
	family := ""
	for i, c := range size {
		if i == 0 {
			// First character must be a letter (the family)
			if c >= 'A' && c <= 'Z' {
				family += string(c)
			} else {
				return ""
			}
		} else if c >= 'A' && c <= 'Z' {
			// Additional uppercase = part of family (e.g., FX)
			family += string(c)
		} else if c >= 'a' && c <= 'z' && len(family) <= 2 {
			// Lowercase after family letter = subfamily (e.g., Da, Ea, Fs, Ls, Es, Ds)
			family += string(c)
		} else {
			// Hit a digit or other character - stop
			break
		}
	}

	return family
}

// getAzureVMTotalMemoryBytes gets total memory in bytes from Azure VM resource metadata
func getAzureVMTotalMemoryBytes(resource providers.Resource) (float64, error) {
	if len(resource.Meta) == 0 {
		return 0, fmt.Errorf("invalid resource metadata")
	}

	// Try to get memory from VM capabilities (fetched during resource collection)
	if capabilities, ok := resource.Meta["vmCapabilities"].(map[string]interface{}); ok {
		if memoryGB, ok := capabilities["MemoryGB"].(float64); ok && memoryGB > 0 {
			return memoryGB * 1024 * 1024 * 1024, nil
		}
		if memoryGB, ok := capabilities["MemoryGB"].(string); ok {
			parsed, err := strconv.ParseFloat(memoryGB, 64)
			if err == nil && parsed > 0 {
				return parsed * 1024 * 1024 * 1024, nil
			}
		}
	}

	// Fallback: Try to get memory from VM size using a static map
	vmSize := getAzureVMSize(resource)
	if vmSize != "" {
		if memory, ok := azureVMMemoryMap[vmSize]; ok {
			return memory, nil
		}
	}

	// Default to 8GB if unknown
	return 8 * 1024 * 1024 * 1024, nil
}

// azureVMMemoryMap contains approximate memory for common Azure VM sizes (in bytes)
// This is a fallback when VM capabilities are not available in resource metadata
var azureVMMemoryMap = map[string]float64{
	"Standard_B1s":    1 * 1024 * 1024 * 1024,
	"Standard_B1ms":   2 * 1024 * 1024 * 1024,
	"Standard_B2s":    4 * 1024 * 1024 * 1024,
	"Standard_B2ms":   8 * 1024 * 1024 * 1024,
	"Standard_B4ms":   16 * 1024 * 1024 * 1024,
	"Standard_D2s_v3": 8 * 1024 * 1024 * 1024,
	"Standard_D4s_v3": 16 * 1024 * 1024 * 1024,
	"Standard_D8s_v3": 32 * 1024 * 1024 * 1024,
	"Standard_E2s_v3": 16 * 1024 * 1024 * 1024,
	"Standard_E4s_v3": 32 * 1024 * 1024 * 1024,
	"Standard_E8s_v3": 64 * 1024 * 1024 * 1024,
	"Standard_F2s_v2": 4 * 1024 * 1024 * 1024,
	"Standard_F4s_v2": 8 * 1024 * 1024 * 1024,
	"Standard_F8s_v2": 16 * 1024 * 1024 * 1024,
}
