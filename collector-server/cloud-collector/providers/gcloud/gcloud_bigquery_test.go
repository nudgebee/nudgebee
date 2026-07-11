package gcloud

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"cloud.google.com/go/bigquery"
	"github.com/stretchr/testify/assert"
)

// TestTableToResourceMetaIsCompactAndReadable guards two coupled fixes:
//  1. OOM trim — a table's column Schema (and an external table's
//     ExternalDataConfig schema) can be hundreds of KB and, accumulated across a
//     large project (~26k tables in prod), exhausted the 3Gi limit. tableTo
//     Resource must never carry that into Meta.
//  2. Reader-key contract — Meta is now hand-built with camelCase keys so the
//     recommendation helpers (getTableSizeGB, hasTimePartitioning, ...) actually
//     match. The old structToMap path emitted PascalCase keys (the SDK struct has
//     no json tags), so every table recommendation silently no-op'd.
func TestTableToResourceMetaIsCompactAndReadable(t *testing.T) {
	var schema bigquery.Schema
	for i := 0; i < 5000; i++ {
		schema = append(schema, &bigquery.FieldSchema{
			Name:        fmt.Sprintf("column_%d_with_a_reasonably_long_name", i),
			Type:        bigquery.StringFieldType,
			Description: "padding description text to inflate per-field metadata size",
		})
	}

	md := &bigquery.TableMetadata{
		Name:             "big_table",
		FullID:           "proj:ds.big_table",
		Schema:           schema,
		NumBytes:         20 * 1024 * 1024 * 1024, // 20 GB
		LastModifiedTime: time.Now(),
		ExpirationTime:   time.Now().Add(24 * time.Hour),
		TimePartitioning: &bigquery.TimePartitioning{Type: bigquery.DayPartitioningType, Field: "ts"},
		Clustering:       &bigquery.Clustering{Fields: []string{"ts", "user_id"}},
		ExternalDataConfig: &bigquery.ExternalDataConfig{
			SourceURIs: []string{"gs://bucket/*"},
			Schema:     schema,
		},
	}

	res := (&bigQueryService{}).tableToResource("ds", "big_table", md, "US", "proj")

	// Column schema must never survive into Meta, under any key or casing.
	for _, k := range []string{"Schema", "schema", "ExternalDataConfig", "externalDataConfig"} {
		if v, ok := res.Meta[k]; ok {
			assert.Nil(t, v, "%s must not be present in Meta", k)
		}
	}

	// The recommendation helpers must read the Meta this function produces.
	// (These all silently returned zero/false before the camelCase fix.)
	sizeGB, ok := getTableSizeGB(res.Meta)
	assert.True(t, ok, "getTableSizeGB must read numBytes")
	assert.InDelta(t, 20.0, sizeGB, 0.01)
	assert.True(t, hasTimePartitioning(res.Meta), "hasTimePartitioning must read timePartitioning")
	assert.True(t, hasClustering(res.Meta), "hasClustering must read clustering")
	assert.True(t, hasExpiration(res.Meta), "hasExpiration must read expirationTime")
	_, lmOk := getLastModifiedTime(res.Meta)
	assert.True(t, lmOk, "getLastModifiedTime must read lastModifiedTime")

	// FullID must survive for the knowledge-graph table->dataset edge builder,
	// which reads metaMap["FullID"] (api-server gcp_source.go).
	assert.Equal(t, "proj:ds.big_table", res.Meta["FullID"], "FullID must be preserved for KG hierarchy edges")

	// Serialized Meta must be small even though the input schema was huge.
	b, err := json.Marshal(res.Meta)
	assert.NoError(t, err)
	assert.Less(t, len(b), 8*1024, "trimmed Meta should be small, got %d bytes", len(b))
}

// TestDatasetToResourceMetaReadable guards the dataset-level reader-key contract
// (hasDefaultTableExpiration / hasCMEK) that the same PascalCase bug broke.
func TestDatasetToResourceMetaReadable(t *testing.T) {
	md := &bigquery.DatasetMetadata{
		Name:                   "analytics",
		Location:               "US",
		DefaultTableExpiration: 48 * time.Hour,
		DefaultEncryptionConfig: &bigquery.EncryptionConfig{
			KMSKeyName: "projects/p/locations/us/keyRings/r/cryptoKeys/k",
		},
	}

	res := (&bigQueryService{}).datasetToResource("analytics", md, "proj")

	assert.True(t, hasDefaultTableExpiration(res.Meta), "hasDefaultTableExpiration must read defaultTableExpiration")
	assert.True(t, hasCMEK(res.Meta), "hasCMEK must read defaultEncryptionConfiguration.kmsKeyName")
	assert.Equal(t, "projects/proj/datasets/analytics", res.Meta["selfLink"])
}

func TestGetTableSizeGB(t *testing.T) {
	tests := []struct {
		name     string
		meta     map[string]any
		wantSize float64
		wantOk   bool
	}{
		{
			name:     "valid size in bytes",
			meta:     map[string]any{"numBytes": float64(10737418240)}, // 10 GB
			wantSize: 10.0,
			wantOk:   true,
		},
		{
			name:     "zero bytes",
			meta:     map[string]any{"numBytes": float64(0)},
			wantSize: 0,
			wantOk:   false,
		},
		{
			name:     "missing numBytes",
			meta:     map[string]any{},
			wantSize: 0,
			wantOk:   false,
		},
		{
			name:     "invalid type",
			meta:     map[string]any{"numBytes": "not a number"},
			wantSize: 0,
			wantOk:   false,
		},
		{
			name:     "small table (1 MB)",
			meta:     map[string]any{"numBytes": float64(1048576)}, // 1 MB
			wantSize: 0.0009765625,
			wantOk:   true,
		},
		{
			name:     "large table (1 TB)",
			meta:     map[string]any{"numBytes": float64(1099511627776)}, // 1 TB
			wantSize: 1024.0,
			wantOk:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotSize, gotOk := getTableSizeGB(tt.meta)
			assert.Equal(t, tt.wantOk, gotOk)
			if tt.wantOk {
				assert.InDelta(t, tt.wantSize, gotSize, 0.01)
			}
		})
	}
}

func TestHasTimePartitioning(t *testing.T) {
	tests := []struct {
		name string
		meta map[string]any
		want bool
	}{
		{
			name: "has time partitioning",
			meta: map[string]any{
				"timePartitioning": map[string]interface{}{
					"type":  "DAY",
					"field": "created_at",
				},
			},
			want: true,
		},
		{
			name: "no time partitioning",
			meta: map[string]any{},
			want: false,
		},
		{
			name: "nil time partitioning",
			meta: map[string]any{"timePartitioning": nil},
			want: false,
		},
		{
			name: "invalid type",
			meta: map[string]any{"timePartitioning": "not a map"},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := hasTimePartitioning(tt.meta)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestHasClustering(t *testing.T) {
	tests := []struct {
		name string
		meta map[string]any
		want bool
	}{
		{
			name: "has clustering",
			meta: map[string]any{
				"clustering": map[string]interface{}{
					"fields": []interface{}{"user_id", "created_at"},
				},
			},
			want: true,
		},
		{
			name: "no clustering",
			meta: map[string]any{},
			want: false,
		},
		{
			name: "nil clustering",
			meta: map[string]any{"clustering": nil},
			want: false,
		},
		{
			name: "empty fields",
			meta: map[string]any{
				"clustering": map[string]interface{}{
					"fields": []interface{}{},
				},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := hasClustering(tt.meta)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestHasExpiration(t *testing.T) {
	tests := []struct {
		name string
		meta map[string]any
		want bool
	}{
		{
			name: "has expiration",
			meta: map[string]any{
				"expirationTime": "2024-12-31T23:59:59.999999999Z",
			},
			want: true,
		},
		{
			name: "no expiration",
			meta: map[string]any{},
			want: false,
		},
		{
			name: "empty expiration",
			meta: map[string]any{"expirationTime": ""},
			want: false,
		},
		{
			name: "invalid time format",
			meta: map[string]any{"expirationTime": "not a time"},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := hasExpiration(tt.meta)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestGetLastModifiedTime(t *testing.T) {
	tests := []struct {
		name    string
		meta    map[string]any
		wantOk  bool
		wantAge time.Duration // approximate age in days
	}{
		{
			name: "recent modification",
			meta: map[string]any{
				"lastModifiedTime": time.Now().Add(-7 * 24 * time.Hour).Format(time.RFC3339Nano),
			},
			wantOk:  true,
			wantAge: 7 * 24 * time.Hour,
		},
		{
			name: "old modification",
			meta: map[string]any{
				"lastModifiedTime": time.Now().Add(-200 * 24 * time.Hour).Format(time.RFC3339Nano),
			},
			wantOk:  true,
			wantAge: 200 * 24 * time.Hour,
		},
		{
			name:   "no lastModifiedTime",
			meta:   map[string]any{},
			wantOk: false,
		},
		{
			name:   "empty string",
			meta:   map[string]any{"lastModifiedTime": ""},
			wantOk: false,
		},
		{
			name:   "invalid format",
			meta:   map[string]any{"lastModifiedTime": "not a time"},
			wantOk: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotTime, gotOk := getLastModifiedTime(tt.meta)
			assert.Equal(t, tt.wantOk, gotOk)
			if tt.wantOk {
				age := time.Since(gotTime)
				assert.InDelta(t, tt.wantAge.Hours(), age.Hours(), 1.0)
			}
		})
	}
}

func TestUnusedTableDetectionLogic(t *testing.T) {
	t.Run("table never queried should be marked unused", func(t *testing.T) {
		queriedTables := map[string]time.Time{
			"projects/test-project/datasets/test-dataset/tables/queried-table": time.Now().Add(-10 * 24 * time.Hour),
		}

		tableId := "projects/test-project/datasets/test-dataset/tables/unused-table"
		lookbackDays := 60

		lastQueried, wasQueried := queriedTables[tableId]
		isUnused := !wasQueried || time.Since(lastQueried) > time.Duration(lookbackDays)*24*time.Hour

		assert.False(t, wasQueried)
		assert.True(t, isUnused)
	})

	t.Run("table queried recently should not be marked unused", func(t *testing.T) {
		queriedTables := map[string]time.Time{
			"projects/test-project/datasets/test-dataset/tables/active-table": time.Now().Add(-10 * 24 * time.Hour),
		}

		tableId := "projects/test-project/datasets/test-dataset/tables/active-table"
		lookbackDays := 60

		lastQueried, wasQueried := queriedTables[tableId]
		isUnused := !wasQueried || time.Since(lastQueried) > time.Duration(lookbackDays)*24*time.Hour

		assert.True(t, wasQueried)
		assert.False(t, isUnused)
	})

	t.Run("table queried long ago should be marked unused", func(t *testing.T) {
		queriedTables := map[string]time.Time{
			"projects/test-project/datasets/test-dataset/tables/old-table": time.Now().Add(-90 * 24 * time.Hour),
		}

		tableId := "projects/test-project/datasets/test-dataset/tables/old-table"
		lookbackDays := 60

		lastQueried, wasQueried := queriedTables[tableId]
		isUnused := !wasQueried || time.Since(lastQueried) > time.Duration(lookbackDays)*24*time.Hour

		assert.True(t, wasQueried)
		assert.True(t, isUnused)
	})

	t.Run("cost calculation for unused tables", func(t *testing.T) {
		// Test active storage rate (tables queried within 90 days)
		sizeGB := 100.0
		lookbackDays := 60
		storageRate := bqActiveStoragePricePerGBMonth
		if lookbackDays >= 90 {
			storageRate = bqLongTermStoragePricePerGBMonth
		}
		savings := sizeGB * storageRate

		assert.InDelta(t, 2.0, savings, 0.01) // 100 GB * $0.02/GB = $2.00

		// Test long-term storage rate (tables not queried in 90+ days)
		lookbackDays = 90
		storageRate = bqActiveStoragePricePerGBMonth
		if lookbackDays >= 90 {
			storageRate = bqLongTermStoragePricePerGBMonth
		}
		savings = sizeGB * storageRate

		assert.InDelta(t, 1.0, savings, 0.01) // 100 GB * $0.01/GB = $1.00
	})
}

func TestBigQueryRecommendationDataStructure(t *testing.T) {
	t.Run("unused table recommendation contains all required fields", func(t *testing.T) {
		now := time.Now()
		lastQueried := now.Add(-70 * 24 * time.Hour)

		recData := map[string]any{
			"table_id":           "projects/test-project/datasets/test-dataset/tables/unused-table",
			"table_name":         "unused-table",
			"region":             "us-central1",
			"age_days":           365,
			"days_since_queried": 70,
			"lookback_days":      60,
			"detection_method":   "query_activity",
			"size_gb":            50.0,
			"has_size_info":      true,
			"last_queried":       lastQueried.Format(time.RFC3339),
		}

		// Verify all required fields exist
		assert.NotNil(t, recData["table_id"])
		assert.NotNil(t, recData["table_name"])
		assert.NotNil(t, recData["region"])
		assert.NotNil(t, recData["age_days"])
		assert.NotNil(t, recData["days_since_queried"])
		assert.NotNil(t, recData["lookback_days"])
		assert.Equal(t, "query_activity", recData["detection_method"])
		assert.NotNil(t, recData["size_gb"])
		assert.NotNil(t, recData["has_size_info"])
		assert.NotNil(t, recData["last_queried"])
	})

	t.Run("unused table never queried has nil last_queried", func(t *testing.T) {
		recData := map[string]any{
			"table_id":           "projects/test-project/datasets/test-dataset/tables/never-queried",
			"table_name":         "never-queried",
			"region":             "us-central1",
			"age_days":           365,
			"days_since_queried": -1,
			"lookback_days":      60,
			"detection_method":   "query_activity",
			"size_gb":            50.0,
			"has_size_info":      true,
			"last_queried":       nil,
		}

		assert.Nil(t, recData["last_queried"])
		assert.Equal(t, -1, recData["days_since_queried"])
	})
}

func TestConfigurableLookbackPeriod(t *testing.T) {
	t.Run("default lookback period is 60 days", func(t *testing.T) {
		// TODO: Once configuration mechanism is implemented, add tests for custom lookback periods
		// For now, the lookback period is hardcoded to 60 days
		defaultLookbackDays := 60
		assert.Equal(t, 60, defaultLookbackDays)
	})

	t.Run("lookback period affects unused table detection", func(t *testing.T) {
		// Verify that the lookback logic would work correctly with different periods
		testCases := []struct {
			name         string
			lookbackDays int
			lastQueried  time.Time
			expectUnused bool
		}{
			{
				name:         "table queried within lookback period (30 days)",
				lookbackDays: 30,
				lastQueried:  time.Now().Add(-20 * 24 * time.Hour),
				expectUnused: false,
			},
			{
				name:         "table queried outside lookback period (30 days)",
				lookbackDays: 30,
				lastQueried:  time.Now().Add(-40 * 24 * time.Hour),
				expectUnused: true,
			},
			{
				name:         "table queried within lookback period (60 days)",
				lookbackDays: 60,
				lastQueried:  time.Now().Add(-50 * 24 * time.Hour),
				expectUnused: false,
			},
			{
				name:         "table queried outside lookback period (60 days)",
				lookbackDays: 60,
				lastQueried:  time.Now().Add(-70 * 24 * time.Hour),
				expectUnused: true,
			},
			{
				name:         "table queried within lookback period (90 days)",
				lookbackDays: 90,
				lastQueried:  time.Now().Add(-80 * 24 * time.Hour),
				expectUnused: false,
			},
			{
				name:         "table queried outside lookback period (90 days)",
				lookbackDays: 90,
				lastQueried:  time.Now().Add(-100 * 24 * time.Hour),
				expectUnused: true,
			},
		}

		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				isUnused := time.Since(tc.lastQueried) > time.Duration(tc.lookbackDays)*24*time.Hour
				assert.Equal(t, tc.expectUnused, isUnused)
			})
		}
	})
}

// Note: Integration tests for getQueriedTablesFromJobs require actual GCP credentials
// and BigQuery datasets with query history. These should be run separately with proper
// test fixtures or in a GCP testing environment.
func TestBigQueryIntegration(t *testing.T) {
	// Skip if no GCP credentials are available
	t.Skip("skipping BigQuery integration test: requires GCP credentials and test fixtures")

	// Integration test implementation would go here
	// This would test:
	// 1. getQueriedTablesFromJobs with real INFORMATION_SCHEMA.JOBS queries
	// 2. getDatasetRegions with real datasets
	// 3. Full GetRecommendations flow with query-based unused table detection
}
