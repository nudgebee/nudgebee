package aws

import "strings"

// getGravitonInstanceType returns the Graviton equivalent instance type with the given prefix
// prefix: "db." for RDS, "cache." for ElastiCache
// instanceType: full instance type like "db.r5.large" or "cache.r6i.large"
func getGravitonInstanceType(instanceType, prefix string) string {
	// Extract instance family (e.g., "r6i" from "db.r6i.large" or "cache.r6i.large")
	parts := strings.Split(instanceType, ".")
	if len(parts) < 3 {
		return ""
	}

	familyType := parts[1] // e.g., "r6i", "m5", "t3"

	// Check if already Graviton (ends with 'g')
	if strings.HasSuffix(familyType, "g") {
		return "" // Already Graviton
	}

	// Determine the instance family letter (r, m, t, c, x, etc.)
	if len(familyType) == 0 {
		return ""
	}
	familyLetter := string(familyType[0])

	// Map to latest Graviton generation based on family
	// AWS Graviton naming: 7th gen for r/m/c, 4th gen for t, 2nd gen for x
	var gravitonFamily string
	switch familyLetter {
	case "r":
		gravitonFamily = prefix + "r7g"
	case "m":
		gravitonFamily = prefix + "m7g"
	case "t":
		gravitonFamily = prefix + "t4g"
	case "c":
		gravitonFamily = prefix + "c7g"
	case "x":
		gravitonFamily = prefix + "x2g"
	default:
		// Unknown family, no Graviton equivalent
		return ""
	}

	// Reconstruct full instance type with Graviton family
	// e.g., "db.r5.large" → "db.r7g.large" or "cache.r6i.large" → "cache.r7g.large"
	gravitonType := gravitonFamily
	for i := 2; i < len(parts); i++ {
		gravitonType += "." + parts[i]
	}

	return gravitonType
}
