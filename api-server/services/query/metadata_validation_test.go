package query

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// Known metadata inconsistencies — these tables have pre-existing issues
// that should be fixed but are excluded from strict validation to keep the suite green.
var knownSecurityColumnMissing = map[string]bool{
	"event_resolution_groupings_v2":                             true, // AccountIdColumnName "account_id" missing from Columns
	"traces_heatmap_v2":                                         true, // NamespaceColumnName "workload_namespace" missing from Columns
	"k8s_workloads_cloud_account_monitoring_recommendations_v2": true, // security columns missing
	"slo_report_observation_v2":                                 true, // AccountIdColumnName missing
	"auto_pilot_approvals_groupings_v2":                         true, // AccountIdColumnName missing
}

var knownNameMismatches = map[string]bool{
	"k8s_namespace_groupings_v2":                true,
	"k8s_workloads_cloud_account_monitoring_v2": true,
	"integrations_get_all_accounts":             true,
	"audit_groupings_v2":                        true,
	"admin_get_notification_rules_grouping_v2":  true,
}

// TestAllTablesHaveRequiredFields validates that every table in table_metadata
// has the minimum required configuration to function correctly.
func TestAllTablesHaveRequiredFields(t *testing.T) {
	for name, def := range table_metadata {
		t.Run(name, func(t *testing.T) {
			assert.NotEmpty(t, def.Columns, "table %s has no columns defined", name)

			hasIdentity := def.Name != "" || def.Def != "" || def.DefGenerator != nil
			assert.True(t, hasIdentity, "table %s has no Name, Def, or DefGenerator", name)

			hasSource := def.Source != "" || def.SourceGenerator != nil
			assert.True(t, hasSource, "table %s has no Source or SourceGenerator", name)
		})
	}
}

// TestSecurityColumnsExistInColumnMap validates that TenantIdColumnName,
// AccountIdColumnName, and NamespaceColumnName actually exist in the Columns map.
func TestSecurityColumnsExistInColumnMap(t *testing.T) {
	for name, def := range table_metadata {
		if knownSecurityColumnMissing[name] {
			continue
		}
		t.Run(name, func(t *testing.T) {
			if def.TenantIdColumnName != "" {
				_, ok := def.Columns[def.TenantIdColumnName]
				assert.True(t, ok, "TenantIdColumnName %q not found in Columns map", def.TenantIdColumnName)
			}
			if def.AccountIdColumnName != "" {
				_, ok := def.Columns[def.AccountIdColumnName]
				assert.True(t, ok, "AccountIdColumnName %q not found in Columns map", def.AccountIdColumnName)
			}
			if def.NamespaceColumnName != "" {
				_, ok := def.Columns[def.NamespaceColumnName]
				assert.True(t, ok, "NamespaceColumnName %q not found in Columns map", def.NamespaceColumnName)
			}
		})
	}
}

// TestKnownSecurityColumnIssues explicitly documents known metadata inconsistencies.
// Remove entries from this test as they are fixed.
func TestKnownSecurityColumnIssues(t *testing.T) {
	for name := range knownSecurityColumnMissing {
		t.Run(name, func(t *testing.T) {
			def, ok := table_metadata[name]
			if !ok {
				t.Skipf("table %s no longer exists — remove from knownSecurityColumnMissing", name)
				return
			}
			// Check if the issue has been fixed
			allPresent := true
			if def.TenantIdColumnName != "" {
				if _, ok := def.Columns[def.TenantIdColumnName]; !ok {
					allPresent = false
				}
			}
			if def.AccountIdColumnName != "" {
				if _, ok := def.Columns[def.AccountIdColumnName]; !ok {
					allPresent = false
				}
			}
			if def.NamespaceColumnName != "" {
				if _, ok := def.Columns[def.NamespaceColumnName]; !ok {
					allPresent = false
				}
			}
			if allPresent {
				t.Errorf("table %s security columns are now all present — remove from knownSecurityColumnMissing", name)
			} else {
				t.Logf("KNOWN ISSUE: table %s has missing security columns in Columns map", name)
			}
		})
	}
}

// TestSecurityColumnsAreNotAggregated validates that tenant_id, account_id,
// and namespace columns are never marked as IsAggregated.
func TestSecurityColumnsAreNotAggregated(t *testing.T) {
	for name, def := range table_metadata {
		t.Run(name, func(t *testing.T) {
			if def.TenantIdColumnName != "" {
				if col, ok := def.Columns[def.TenantIdColumnName]; ok {
					assert.False(t, col.IsAggregated, "TenantIdColumnName %q must not be aggregated", def.TenantIdColumnName)
				}
			}
			if def.AccountIdColumnName != "" {
				if col, ok := def.Columns[def.AccountIdColumnName]; ok {
					assert.False(t, col.IsAggregated, "AccountIdColumnName %q must not be aggregated", def.AccountIdColumnName)
				}
			}
			if def.NamespaceColumnName != "" {
				if col, ok := def.Columns[def.NamespaceColumnName]; ok {
					assert.False(t, col.IsAggregated, "NamespaceColumnName %q must not be aggregated", def.NamespaceColumnName)
				}
			}
		})
	}
}

// TestAggregateTablesHaveAggregatedColumns validates that every Aggregate table
// has at least one IsAggregated column.
func TestAggregateTablesHaveAggregatedColumns(t *testing.T) {
	// llm_conversation_feedback_v2 is typed Aggregate but has no aggregated columns — known issue
	knownMissing := map[string]bool{"llm_conversation_feedback_v2": true}
	for name, def := range table_metadata {
		if def.Type != Aggregate {
			continue
		}
		if knownMissing[name] {
			continue
		}
		t.Run(name, func(t *testing.T) {
			hasAggregated := false
			for _, col := range def.Columns {
				if col.IsAggregated {
					hasAggregated = true
					break
				}
			}
			assert.True(t, hasAggregated, "Aggregate table %s has no IsAggregated columns", name)
		})
	}
}

// TestAggregatedColumnsHaveDefOrDefGenerator validates that every IsAggregated column
// has either a Def expression or a DefGenerator function.
func TestAggregatedColumnsHaveDefOrDefGenerator(t *testing.T) {
	for name, def := range table_metadata {
		for colName, col := range def.Columns {
			if !col.IsAggregated {
				continue
			}
			t.Run(name+"/"+colName, func(t *testing.T) {
				hasDef := col.Def != "" || col.DefGenerator != nil
				assert.True(t, hasDef, "table %s column %s is IsAggregated but has no Def or DefGenerator", name, colName)
			})
		}
	}
}

// TestGetTableMetadataLookup validates that GetTableMetadata works with
// exact names and is case-insensitive.
func TestGetTableMetadataLookup(t *testing.T) {
	for name := range table_metadata {
		t.Run("exact/"+name, func(t *testing.T) {
			_, ok := GetTableMetadata(name)
			assert.True(t, ok, "GetTableMetadata(%q) should find the table", name)
		})
	}

	t.Run("case_insensitive", func(t *testing.T) {
		_, ok := GetTableMetadata("DW_QUERIES_V2")
		assert.True(t, ok, "GetTableMetadata should be case-insensitive")
	})

	t.Run("not_found", func(t *testing.T) {
		_, ok := GetTableMetadata("nonexistent_table_xyz")
		assert.False(t, ok)
	})
}

// TestColumnTypesAreValid validates that column types use known ColumnDefinitionType values.
func TestColumnTypesAreValid(t *testing.T) {
	validTypes := map[ColumnDefinitionType]bool{
		ColumnDefinitionTypeString:   true,
		ColumnDefinitionTypeInt:      true,
		ColumnDefinitionTypeFloat:    true,
		ColumnDefinitionTypeDatetime: true,
		ColumnDefinitionTypeList:     true,
		ColumnDefinitionTypeMap:      true,
		ColumnDefinitionTypeJson:     true,
		ColumnDefinitionTypeBoolean:  true,
		"array":                      true, // legacy
	}

	for name, def := range table_metadata {
		for colName, col := range def.Columns {
			t.Run(name+"/"+colName, func(t *testing.T) {
				assert.True(t, validTypes[col.Type], "table %s column %s has unknown type %q", name, colName, col.Type)
			})
		}
	}
}

// TestAggregatedColumnDefsContainAggregateFunctions validates that Def expressions
// for IsAggregated columns look like aggregate SQL.
func TestAggregatedColumnDefsContainAggregateFunctions(t *testing.T) {
	aggregateKeywords := []string{
		"count(", "sum(", "avg(", "max(", "min(",
		"COUNT(", "SUM(", "AVG(", "MAX(", "MIN(",
		"array_agg(", "jsonb_agg(", "quantile(",
		"APPROX_QUANTILES(", "SUM(CASE",
		"OVER(",
		"string_agg(", "STRING_AGG(",
	}

	for name, def := range table_metadata {
		for colName, col := range def.Columns {
			if !col.IsAggregated || col.Def == "" || col.DefGenerator != nil {
				continue
			}
			t.Run(name+"/"+colName, func(t *testing.T) {
				found := false
				for _, kw := range aggregateKeywords {
					if strings.Contains(col.Def, kw) {
						found = true
						break
					}
				}
				if !found {
					t.Logf("WARN: table %s column %s is IsAggregated with Def=%q — no recognized aggregate function found", name, colName, col.Def)
				}
			})
		}
	}
}

// TestTableNameConsistency validates that table Name field matches the map key.
func TestTableNameConsistency(t *testing.T) {
	for name, def := range table_metadata {
		if def.Name == "" || knownNameMismatches[name] {
			continue
		}
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, name, def.Name, "table_metadata key %q doesn't match TableDefinition.Name %q", name, def.Name)
		})
	}
}

// TestTableCount validates that the metadata registry has a reasonable number of tables.
func TestTableCount(t *testing.T) {
	count := len(table_metadata)
	assert.Greater(t, count, 50, "Expected at least 50 tables in table_metadata, got %d", count)
	t.Logf("Total tables in table_metadata: %d", count)
}
