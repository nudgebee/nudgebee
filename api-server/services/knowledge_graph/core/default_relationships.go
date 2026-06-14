package core

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
)

// LoadDefaultRelationships loads default cross-account relationship rules from a JSON file
// These relationships are shared across all tenants
func LoadDefaultRelationships(filePath string) ([]CrossAccountRelationship, error) {
	// Read the JSON file
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read default relationships file: %w", err)
	}

	// Parse JSON into CrossAccountRelationship slice
	var relationships []CrossAccountRelationship
	if err := json.Unmarshal(data, &relationships); err != nil {
		return nil, fmt.Errorf("failed to parse default relationships JSON: %w", err)
	}

	// Validate that all relationships have required fields
	for i, rel := range relationships {
		if rel.Name == "" {
			return nil, fmt.Errorf("relationship at index %d missing name", i)
		}
		if rel.SourceType == "" {
			return nil, fmt.Errorf("relationship '%s' missing source_type", rel.Name)
		}
		if rel.TargetType == "" {
			return nil, fmt.Errorf("relationship '%s' missing target_type", rel.Name)
		}
		if len(rel.MatchingRules) == 0 {
			return nil, fmt.Errorf("relationship '%s' has no matching rules", rel.Name)
		}
	}

	return relationships, nil
}

// MergeRelationships merges default relationships (from file) with API-provided relationships
// Strategy:
// - Start with all default relationships
// - Add API-provided relationships
// - If API provides a relationship with the same name, API version overrides the default
func MergeRelationships(defaults, apiProvided []CrossAccountRelationship) []CrossAccountRelationship {
	// Create a map starting with defaults
	mergedMap := make(map[string]CrossAccountRelationship)
	for _, rel := range defaults {
		mergedMap[rel.Name] = rel
	}

	// Override/add with API-provided relationships
	for _, rel := range apiProvided {
		mergedMap[rel.Name] = rel
	}

	// Convert back to slice
	func MergeRelationships(defaults, apiProvided []CrossAccountRelationship) []CrossAccountRelationship {
	// Create a map starting with defaults
	mergedMap := make(map[string]CrossAccountRelationship)
	for _, rel := range defaults {
		mergedMap[rel.Name] = rel
	}

	// Override/add with API-provided relationships
	for _, rel := range apiProvided {
		mergedMap[rel.Name] = rel
	}

	// Collect and sort keys
	keys := make([]string, 0, len(mergedMap))
	for k := range mergedMap {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	// Convert back to slice in deterministic order
	merged := make([]CrossAccountRelationship, 0, len(mergedMap))
	for _, k := range keys {
		rel := mergedMap[k]

		// Only include enabled relationships
		if rel.Enabled {
			merged = append(merged, rel)
		}
	}

	return merged
}