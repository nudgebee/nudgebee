package aws

import (
	"fmt"
	"nudgebee/collector/cloud/providers"
	"strconv"
	"strings"
)

// CalculateThreshold determines the appropriate threshold value for an alarm
// based on resource properties and threshold rules
func CalculateThreshold(resource providers.Resource, template providers.AlarmTemplate) (float64, error) {
	rules := template.ThresholdRules

	// For percentage-based thresholds (memory, storage)
	if rules.DefaultPercentage > 0 {
		return calculatePercentageThreshold(resource, template)
	}

	// For instance family-based thresholds (EC2 CPU)
	if len(rules.ByInstanceFamily) > 0 {
		return calculateInstanceFamilyThreshold(resource, rules)
	}

	// For instance class-based thresholds (RDS CPU)
	if len(rules.ByInstanceClass) > 0 {
		return calculateInstanceClassThreshold(resource, rules)
	}

	// For storage type-based thresholds (RDS latency)
	if len(rules.ByStorageType) > 0 {
		return calculateStorageTypeThreshold(resource, rules)
	}

	// For memory size-based thresholds
	if len(rules.ByMemorySize) > 0 {
		return calculateMemorySizeThreshold(resource, rules)
	}

	// Default threshold - accept any value including 0.0
	// Note: 0.0 is a valid threshold (e.g., for UnhealthyHostCount where any unhealthy host is a problem)
	// We rely on the YAML template explicitly setting a default value if needed
	return rules.Default, nil
}

// calculatePercentageThreshold calculates threshold based on percentage of total capacity
// Used for: FreeableMemory (15% of total), FreeStorageSpace (15% of allocated)
func calculatePercentageThreshold(resource providers.Resource, template providers.AlarmTemplate) (float64, error) {
	rules := template.ThresholdRules
	metricName := template.Configuration.MetricName

	var totalCapacity float64
	var err error

	switch metricName {
	case "FreeableMemory":
		totalCapacity, err = getTotalMemoryBytes(resource)
		if err != nil {
			// Fall back to minimum bytes if we can't determine total memory
			if rules.MinimumBytes > 0 {
				return rules.MinimumBytes, nil
			}
			return rules.Default, nil
		}

	case "FreeStorageSpace":
		totalCapacity, err = getAllocatedStorageBytes(resource)
		if err != nil {
			// Fall back to minimum bytes if we can't determine storage
			if rules.MinimumBytes > 0 {
				return rules.MinimumBytes, nil
			}
			return rules.Default, nil
		}

	case "DatabaseConnections":
		totalCapacity, err = getMaxConnections(resource)
		if err != nil {
			// Fall back to default if we can't determine max_connections
			// Default threshold (e.g., 100 connections) will be used
			return rules.Default, nil
		}

	default:
		return rules.Default, nil
	}

	// Calculate percentage-based threshold
	threshold := totalCapacity * rules.DefaultPercentage

	// Ensure minimum threshold is met
	if rules.MinimumBytes > 0 && threshold < rules.MinimumBytes {
		return rules.MinimumBytes, nil
	}

	return threshold, nil
}

// calculateInstanceFamilyThreshold determines threshold based on EC2 instance family
// Example: t3.large -> family "t3" -> 70% CPU threshold
func calculateInstanceFamilyThreshold(resource providers.Resource, rules providers.ThresholdRules) (float64, error) {
	instanceType := getInstanceType(resource)
	if instanceType == "" {
		return rules.Default, nil
	}

	family := extractInstanceFamily(instanceType)
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

// calculateInstanceClassThreshold determines threshold based on RDS instance class
// Example: db.t3.medium -> class "db.t3" -> 65% CPU threshold
func calculateInstanceClassThreshold(resource providers.Resource, rules providers.ThresholdRules) (float64, error) {
	instanceClass := getDBInstanceClass(resource)
	if instanceClass == "" {
		return rules.Default, nil
	}

	// Extract base class (db.t3, db.r5, etc.)
	class := extractDBInstanceClass(instanceClass)
	if class == "" {
		return rules.Default, nil
	}

	// Check if we have a specific threshold for this class
	if threshold, ok := rules.ByInstanceClass[class]; ok {
		return threshold, nil
	}

	// Fall back to default
	return rules.Default, nil
}

// calculateStorageTypeThreshold determines threshold based on storage type
// Example: gp2 -> 20ms latency, io2 -> 8ms latency
func calculateStorageTypeThreshold(resource providers.Resource, rules providers.ThresholdRules) (float64, error) {
	storageType := getStorageType(resource)
	if storageType == "" {
		return rules.Default, nil
	}

	// Check if we have a specific threshold for this storage type
	if threshold, ok := rules.ByStorageType[storageType]; ok {
		return threshold, nil
	}

	// Fall back to default
	return rules.Default, nil
}

// calculateMemorySizeThreshold determines threshold based on total memory
// Example: <4GB -> 20%, >32GB -> 10%
func calculateMemorySizeThreshold(resource providers.Resource, rules providers.ThresholdRules) (float64, error) {
	memoryGB, err := getTotalMemoryGB(resource)
	if err != nil {
		return rules.Default, nil
	}

	// Convert memory size to string for map lookup
	memorySizeKey := getMemorySizeKey(memoryGB)

	// Check if we have a specific threshold for this memory size
	if threshold, ok := rules.ByMemorySize[memorySizeKey]; ok {
		return threshold, nil
	}

	// Fall back to default
	return rules.Default, nil
}

// Helper functions to extract resource properties

// getInstanceType extracts EC2 instance type from resource
func getInstanceType(resource providers.Resource) string {
	if len(resource.Meta) == 0 {
		return ""
	}

	if instanceType, ok := resource.Meta["InstanceType"].(string); ok {
		return instanceType
	}

	return ""
}

// extractInstanceFamily extracts family from instance type
// Example: "t3.large" -> "t3", "c5.xlarge" -> "c5"
func extractInstanceFamily(instanceType string) string {
	parts := strings.Split(instanceType, ".")
	if len(parts) > 0 {
		return parts[0]
	}
	return ""
}

// getDBInstanceClass extracts RDS instance class from resource
func getDBInstanceClass(resource providers.Resource) string {
	if len(resource.Meta) == 0 {
		return ""
	}

	if instanceClass, ok := resource.Meta["DBInstanceClass"].(string); ok {
		return instanceClass
	}

	return ""
}

// extractDBInstanceClass extracts base class from DB instance class
// Example: "db.t3.medium" -> "db.t3", "db.r5.large" -> "db.r5"
func extractDBInstanceClass(instanceClass string) string {
	parts := strings.Split(instanceClass, ".")
	if len(parts) >= 2 {
		return fmt.Sprintf("%s.%s", parts[0], parts[1])
	}
	return ""
}

// getStorageType extracts storage type from resource (RDS or ElastiCache)
func getStorageType(resource providers.Resource) string {
	if len(resource.Meta) == 0 {
		return ""
	}

	// RDS storage type
	if storageType, ok := resource.Meta["StorageType"].(string); ok {
		return storageType
	}

	// ElastiCache cache node type (different field)
	if cacheNodeType, ok := resource.Meta["CacheNodeType"].(string); ok {
		return cacheNodeType
	}

	return ""
}

// getTotalMemoryBytes gets total memory in bytes from resource metadata
// Priority: AWS Pricing API data -> Hardcoded maps -> Error
func getTotalMemoryBytes(resource providers.Resource) (float64, error) {
	if len(resource.Meta) == 0 {
		return 0, fmt.Errorf("invalid raw data format")
	}

	// PRIORITY 1: Try to get memory from AWS Pricing API data (InstanceTypeDetails)
	// This works for ALL instance types, not just hardcoded ones
	if memoryBytes, err := getMemoryFromInstanceTypeDetails(resource); err == nil {
		return memoryBytes, nil
	}

	// PRIORITY 2: Fallback to hardcoded maps if pricing data unavailable
	// RDS: AllocatedMemory or calculate from instance class
	if dbInstanceClass, ok := resource.Meta["DBInstanceClass"].(string); ok {
		return getMemoryFromDBInstanceClass(dbInstanceClass)
	}

	// ElastiCache: Calculate from cache node type
	if cacheNodeType, ok := resource.Meta["CacheNodeType"].(string); ok {
		return getMemoryFromCacheNodeType(cacheNodeType)
	}

	// EC2: Not typically used for EC2, but could extract from instance type
	if instanceType, ok := resource.Meta["InstanceType"].(string); ok {
		return getMemoryFromInstanceType(instanceType)
	}

	return 0, fmt.Errorf("unable to determine total memory")
}

// getTotalMemoryGB gets total memory in GB from resource
func getTotalMemoryGB(resource providers.Resource) (float64, error) {
	memoryBytes, err := getTotalMemoryBytes(resource)
	if err != nil {
		return 0, err
	}
	return memoryBytes / (1024 * 1024 * 1024), nil
}

// getAllocatedStorageBytes gets allocated storage in bytes from resource
func getAllocatedStorageBytes(resource providers.Resource) (float64, error) {
	if len(resource.Meta) == 0 {
		return 0, fmt.Errorf("invalid raw data format")
	}

	// RDS: AllocatedStorage in GB
	if allocatedStorage, ok := resource.Meta["AllocatedStorage"]; ok {
		var storageGB float64

		switch v := allocatedStorage.(type) {
		case int:
			storageGB = float64(v)
		case int32:
			storageGB = float64(v)
		case int64:
			storageGB = float64(v)
		case float64:
			storageGB = v
		case string:
			parsed, err := strconv.ParseFloat(v, 64)
			if err != nil {
				return 0, fmt.Errorf("invalid AllocatedStorage value: %s", v)
			}
			storageGB = parsed
		default:
			return 0, fmt.Errorf("unsupported AllocatedStorage type")
		}

		// Convert GB to bytes
		return storageGB * 1024 * 1024 * 1024, nil
	}

	return 0, fmt.Errorf("unable to determine allocated storage")
}

// getMaxConnections gets the maximum number of database connections from resource
// For RDS, this is typically stored in parameter groups or calculated from instance memory
func getMaxConnections(resource providers.Resource) (float64, error) {
	if len(resource.Meta) == 0 {
		return 0, fmt.Errorf("invalid raw data format")
	}

	// PRIORITY 1: Try to get max_connections from resource metadata (if explicitly set)
	if maxConns, ok := resource.Meta["MaxConnections"]; ok {
		switch v := maxConns.(type) {
		case int:
			return float64(v), nil
		case int32:
			return float64(v), nil
		case int64:
			return float64(v), nil
		case float64:
			return v, nil
		case string:
			parsed, err := strconv.ParseFloat(v, 64)
			if err != nil {
				return 0, fmt.Errorf("invalid MaxConnections value: %s", v)
			}
			return parsed, nil
		}
	}

	// PRIORITY 2: Calculate from instance memory using AWS default formula
	// RDS default max_connections = {DBInstanceClassMemory/12582880}
	// See: https://docs.aws.amazon.com/AmazonRDS/latest/UserGuide/CHAP_Limits.html
	memoryBytes, err := getTotalMemoryBytes(resource)
	if err != nil {
		return 0, fmt.Errorf("unable to determine max_connections: %w", err)
	}

	// AWS formula: max_connections = memory_bytes / 12582880
	maxConnections := memoryBytes / 12582880

	return maxConnections, nil
}

// Memory lookup tables (approximations - in production, use AWS API or metadata)
// These are approximate values for common instance types

// getMemoryFromInstanceTypeDetails extracts memory from AWS Pricing API data
// stored in resource.Meta["InstanceTypeDetails"]["product"]["attributes"]["memory"]
func getMemoryFromInstanceTypeDetails(resource providers.Resource) (float64, error) {
	instanceTypeDetails, ok := resource.Meta["InstanceTypeDetails"]
	if !ok {
		return 0, fmt.Errorf("InstanceTypeDetails not found in resource metadata")
	}

	// Navigate: InstanceTypeDetails -> product -> attributes -> memory
	details, ok := instanceTypeDetails.(map[string]any)
	if !ok {
		return 0, fmt.Errorf("InstanceTypeDetails is not a map")
	}

	product, ok := details["product"].(map[string]any)
	if !ok {
		return 0, fmt.Errorf("product not found in InstanceTypeDetails")
	}

	attributes, ok := product["attributes"].(map[string]any)
	if !ok {
		return 0, fmt.Errorf("attributes not found in product")
	}

	memoryStr, ok := attributes["memory"].(string)
	if !ok || memoryStr == "" {
		return 0, fmt.Errorf("memory not found in attributes")
	}

	// Parse memory string (e.g., "8 GiB" -> 8 GB -> bytes)
	return parseMemoryString(memoryStr)
}

// parseMemoryString parses AWS memory format strings
// Supports: "8 GiB", "16 GiB", "128 GiB", etc.
// Returns: memory in bytes
func parseMemoryString(memoryStr string) (float64, error) {
	// Format: "8 GiB" or "16 GiB"
	parts := strings.Fields(memoryStr)
	if len(parts) < 1 {
		return 0, fmt.Errorf("invalid memory format: %s", memoryStr)
	}

	// Extract numeric value
	valueStr := parts[0]
	value, err := strconv.ParseFloat(valueStr, 64)
	if err != nil {
		return 0, fmt.Errorf("failed to parse memory value '%s': %w", valueStr, err)
	}

	// Convert GiB to bytes (AWS uses GiB notation)
	// 1 GiB = 1024 * 1024 * 1024 bytes
	return value * 1024 * 1024 * 1024, nil
}

func getMemoryFromDBInstanceClass(instanceClass string) (float64, error) {
	// FALLBACK: Hardcoded memory mapping for common RDS instance classes (in bytes)
	// This is only used when AWS Pricing API data (InstanceTypeDetails) is unavailable
	// Primary data source: resource.Meta["InstanceTypeDetails"]["product"]["attributes"]["memory"]
	memoryMap := map[string]float64{
		"db.t3.micro":   1 * 1024 * 1024 * 1024,   // 1 GB
		"db.t3.small":   2 * 1024 * 1024 * 1024,   // 2 GB
		"db.t3.medium":  4 * 1024 * 1024 * 1024,   // 4 GB
		"db.t3.large":   8 * 1024 * 1024 * 1024,   // 8 GB
		"db.t3.xlarge":  16 * 1024 * 1024 * 1024,  // 16 GB
		"db.t3.2xlarge": 32 * 1024 * 1024 * 1024,  // 32 GB
		"db.r5.large":   16 * 1024 * 1024 * 1024,  // 16 GB
		"db.r5.xlarge":  32 * 1024 * 1024 * 1024,  // 32 GB
		"db.r5.2xlarge": 64 * 1024 * 1024 * 1024,  // 64 GB
		"db.r5.4xlarge": 128 * 1024 * 1024 * 1024, // 128 GB
		"db.m5.large":   8 * 1024 * 1024 * 1024,   // 8 GB
		"db.m5.xlarge":  16 * 1024 * 1024 * 1024,  // 16 GB
		"db.m5.2xlarge": 32 * 1024 * 1024 * 1024,  // 32 GB
	}

	if memory, ok := memoryMap[instanceClass]; ok {
		return memory, nil
	}

	// Default to 8GB if unknown
	return 8 * 1024 * 1024 * 1024, nil
}

func getMemoryFromCacheNodeType(cacheNodeType string) (float64, error) {
	// FALLBACK: Hardcoded memory mapping for ElastiCache node types (in bytes)
	// This is only used when AWS Pricing API data (InstanceTypeDetails) is unavailable
	// Primary data source: resource.Meta["InstanceTypeDetails"]["product"]["attributes"]["memory"]
	memoryMap := map[string]float64{
		"cache.t3.micro":   0.5 * 1024 * 1024 * 1024,   // 0.5 GB
		"cache.t3.small":   1.37 * 1024 * 1024 * 1024,  // 1.37 GB
		"cache.t3.medium":  3.09 * 1024 * 1024 * 1024,  // 3.09 GB
		"cache.r5.large":   13.07 * 1024 * 1024 * 1024, // 13.07 GB
		"cache.r5.xlarge":  26.32 * 1024 * 1024 * 1024, // 26.32 GB
		"cache.r5.2xlarge": 52.82 * 1024 * 1024 * 1024, // 52.82 GB
		"cache.m5.large":   6.38 * 1024 * 1024 * 1024,  // 6.38 GB
		"cache.m5.xlarge":  12.93 * 1024 * 1024 * 1024, // 12.93 GB
	}

	if memory, ok := memoryMap[cacheNodeType]; ok {
		return memory, nil
	}

	// Default to 4GB if unknown
	return 4 * 1024 * 1024 * 1024, nil
}

func getMemoryFromInstanceType(instanceType string) (float64, error) {
	// FALLBACK: Hardcoded memory mapping for EC2 instance types (in bytes)
	// This is only used when AWS Pricing API data (InstanceTypeDetails) is unavailable
	// Primary data source: resource.Meta["InstanceTypeDetails"]["product"]["attributes"]["memory"]
	memoryMap := map[string]float64{
		"t3.micro":   1 * 1024 * 1024 * 1024,  // 1 GB
		"t3.small":   2 * 1024 * 1024 * 1024,  // 2 GB
		"t3.medium":  4 * 1024 * 1024 * 1024,  // 4 GB
		"t3.large":   8 * 1024 * 1024 * 1024,  // 8 GB
		"t3.xlarge":  16 * 1024 * 1024 * 1024, // 16 GB
		"t3.2xlarge": 32 * 1024 * 1024 * 1024, // 32 GB
		"m5.large":   8 * 1024 * 1024 * 1024,  // 8 GB
		"m5.xlarge":  16 * 1024 * 1024 * 1024, // 16 GB
		"m5.2xlarge": 32 * 1024 * 1024 * 1024, // 32 GB
		"c5.large":   4 * 1024 * 1024 * 1024,  // 4 GB
		"c5.xlarge":  8 * 1024 * 1024 * 1024,  // 8 GB
		"r5.large":   16 * 1024 * 1024 * 1024, // 16 GB
		"r5.xlarge":  32 * 1024 * 1024 * 1024, // 32 GB
	}

	if memory, ok := memoryMap[instanceType]; ok {
		return memory, nil
	}

	// Default to 8GB if unknown
	return 8 * 1024 * 1024 * 1024, nil
}

// getMemorySizeKey converts memory GB to a lookup key
// Example: 2GB -> "<4gb", 16GB -> "8-32gb", 64GB -> ">32gb"
func getMemorySizeKey(memoryGB float64) string {
	if memoryGB < 4 {
		return "<4gb"
	} else if memoryGB <= 8 {
		return "4-8gb"
	} else if memoryGB <= 16 {
		return "8-16gb"
	} else if memoryGB <= 32 {
		return "16-32gb"
	}
	return ">32gb"
}
