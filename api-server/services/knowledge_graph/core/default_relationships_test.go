package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDefaultRelationships(t *testing.T) {
	// Create a temporary test file
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test_relationships.json")

	testData := []CrossAccountRelationship{
		{
			Name:           "test_relationship",
			Enabled:        true,
			SourceType:     "k8s",
			TargetType:     "k8s",
			SourceNodeType: NodeTypeService,
			TargetNodeType: NodeTypeWorkload,
			MatchingRules: []MatchingRule{
				{
					SourceProperty: "properties.service_name",
					TargetProperty: "properties.labels.dd_service_name",
					MatchType:      MatchTypeExact,
					CaseSensitive:  false,
				},
			},
			RelationshipType: RelationshipRunsOn,
			Bidirectional:    false,
			CrossAccount:     true,
		},
	}

	// Write test data to file
	data, err := json.MarshalIndent(testData, "", "  ")
	if err != nil {
		t.Fatalf("Failed to marshal test data: %v", err)
	}

	err = os.WriteFile(testFile, data, 0644)
	if err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	// Test loading
	relationships, err := LoadDefaultRelationships(testFile)
	if err != nil {
		t.Fatalf("LoadDefaultRelationships failed: %v", err)
	}

	if len(relationships) != 1 {
		t.Errorf("Expected 1 relationship, got %d", len(relationships))
	}

	if relationships[0].Name != "test_relationship" {
		t.Errorf("Expected name 'test_relationship', got '%s'", relationships[0].Name)
	}
}

func TestLoadDefaultRelationships_InvalidFile(t *testing.T) {
	_, err := LoadDefaultRelationships("/nonexistent/file.json")
	if err == nil {
		t.Error("Expected error for nonexistent file, got nil")
	}
}

func TestLoadDefaultRelationships_InvalidJSON(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "invalid.json")

	err := os.WriteFile(testFile, []byte("not valid json"), 0644)
	if err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	_, err = LoadDefaultRelationships(testFile)
	if err == nil {
		t.Error("Expected error for invalid JSON, got nil")
	}
}

func TestLoadDefaultRelationships_MissingRequiredFields(t *testing.T) {
	tmpDir := t.TempDir()

	tests := []struct {
		name         string
		relationship CrossAccountRelationship
		expectError  bool
		errorMsg     string
	}{
		{
			name: "missing name",
			relationship: CrossAccountRelationship{
				SourceType:     "k8s",
				TargetType:     "k8s",
				SourceNodeType: NodeTypeService,
				TargetNodeType: NodeTypeWorkload,
				MatchingRules: []MatchingRule{
					{
						SourceProperty: "prop1",
						TargetProperty: "prop2",
						MatchType:      MatchTypeExact,
					},
				},
			},
			expectError: true,
			errorMsg:    "missing name",
		},
		{
			name: "missing source_type",
			relationship: CrossAccountRelationship{
				Name:           "test",
				TargetType:     "k8s",
				SourceNodeType: NodeTypeService,
				TargetNodeType: NodeTypeWorkload,
				MatchingRules: []MatchingRule{
					{
						SourceProperty: "prop1",
						TargetProperty: "prop2",
						MatchType:      MatchTypeExact,
					},
				},
			},
			expectError: true,
			errorMsg:    "missing source_type",
		},
		{
			name: "missing matching_rules",
			relationship: CrossAccountRelationship{
				Name:           "test",
				SourceType:     "k8s",
				TargetType:     "k8s",
				SourceNodeType: NodeTypeService,
				TargetNodeType: NodeTypeWorkload,
				MatchingRules:  []MatchingRule{},
			},
			expectError: true,
			errorMsg:    "no matching rules",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testFile := filepath.Join(tmpDir, tt.name+".json")
			data, _ := json.Marshal([]CrossAccountRelationship{tt.relationship})
			err := os.WriteFile(testFile, data, 0644)
			if err != nil {
				t.Fatalf("Failed to write test file: %v", err)
			}

			_, err = LoadDefaultRelationships(testFile)
			if tt.expectError && err == nil {
				t.Errorf("Expected error containing '%s', got nil", tt.errorMsg)
			}
			if !tt.expectError && err != nil {
				t.Errorf("Expected no error, got %v", err)
			}
		})
	}
}

func TestMergeRelationships(t *testing.T) {
	defaults := []CrossAccountRelationship{
		{
			Name:           "default1",
			Enabled:        true,
			SourceType:     "k8s",
			TargetType:     "k8s",
			SourceNodeType: NodeTypeService,
			TargetNodeType: NodeTypeWorkload,
			MatchingRules: []MatchingRule{
				{
					SourceProperty: "prop1",
					TargetProperty: "prop2",
					MatchType:      MatchTypeExact,
				},
			},
			RelationshipType: RelationshipRunsOn,
		},
		{
			Name:           "default2",
			Enabled:        true,
			SourceType:     "k8s",
			TargetType:     "aws",
			SourceNodeType: NodeTypeWorkload,
			TargetNodeType: NodeTypeDatabase,
			MatchingRules: []MatchingRule{
				{
					SourceProperty: "prop3",
					TargetProperty: "prop4",
					MatchType:      MatchTypeContains,
				},
			},
			RelationshipType: RelationshipCalls,
		},
	}

	apiProvided := []CrossAccountRelationship{
		{
			Name:           "default1", // Override default1
			Enabled:        false,      // Disable it
			SourceType:     "k8s",
			TargetType:     "k8s",
			SourceNodeType: NodeTypeService,
			TargetNodeType: NodeTypeWorkload,
			MatchingRules: []MatchingRule{
				{
					SourceProperty: "different_prop",
					TargetProperty: "another_prop",
					MatchType:      MatchTypeExact,
				},
			},
			RelationshipType: RelationshipCalls,
		},
		{
			Name:           "api_only", // New relationship from API
			Enabled:        true,
			SourceType:     "aws",
			TargetType:     "k8s",
			SourceNodeType: NodeTypeLoadBalancer,
			TargetNodeType: NodeTypeService,
			MatchingRules: []MatchingRule{
				{
					SourceProperty: "lb_name",
					TargetProperty: "svc_name",
					MatchType:      MatchTypeExact,
				},
			},
			RelationshipType: RelationshipRoutesTo,
		},
	}

	merged := MergeRelationships(defaults, apiProvided)

	// Should have 2 relationships (default1 is disabled, default2 is kept, api_only is added)
	if len(merged) != 2 {
		t.Errorf("Expected 2 merged relationships, got %d", len(merged))
	}

	// Check that api_only exists
	foundApiOnly := false
	for _, rel := range merged {
		if rel.Name == "api_only" {
			foundApiOnly = true
		}
		if rel.Name == "default1" {
			t.Error("default1 should be excluded (disabled)")
		}
	}

	if !foundApiOnly {
		t.Error("api_only relationship not found in merged result")
	}

	// Check that default2 exists and is unchanged
	foundDefault2 := false
	for _, rel := range merged {
		if rel.Name == "default2" {
			foundDefault2 = true
			if rel.SourceType != "k8s" {
				t.Error("default2 was modified incorrectly")
			}
		}
	}

	if !foundDefault2 {
		t.Error("default2 relationship not found in merged result")
	}
}

func TestMergeRelationships_EmptyDefaults(t *testing.T) {
	apiProvided := []CrossAccountRelationship{
		{
			Name:           "api1",
			Enabled:        true,
			SourceType:     "k8s",
			TargetType:     "k8s",
			SourceNodeType: NodeTypeService,
			TargetNodeType: NodeTypeWorkload,
			MatchingRules: []MatchingRule{
				{
					SourceProperty: "prop1",
					TargetProperty: "prop2",
					MatchType:      MatchTypeExact,
				},
			},
			RelationshipType: RelationshipRunsOn,
		},
	}

	merged := MergeRelationships([]CrossAccountRelationship{}, apiProvided)

	if len(merged) != 1 {
		t.Errorf("Expected 1 merged relationship, got %d", len(merged))
	}
}

func TestMergeRelationships_EmptyAPI(t *testing.T) {
	defaults := []CrossAccountRelationship{
		{
			Name:           "default1",
			Enabled:        true,
			SourceType:     "k8s",
			TargetType:     "k8s",
			SourceNodeType: NodeTypeService,
			TargetNodeType: NodeTypeWorkload,
			MatchingRules: []MatchingRule{
				{
					SourceProperty: "prop1",
					TargetProperty: "prop2",
					MatchType:      MatchTypeExact,
				},
			},
			RelationshipType: RelationshipRunsOn,
		},
	}

	merged := MergeRelationships(defaults, []CrossAccountRelationship{})

	if len(merged) != 1 {
		t.Errorf("Expected 1 merged relationship, got %d", len(merged))
	}
}
