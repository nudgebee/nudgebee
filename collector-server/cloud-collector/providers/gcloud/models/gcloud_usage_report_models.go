package models

import (
	"time"

	"cloud.google.com/go/bigquery"
)

// BillingConfig represents the configuration for GCP billing export
type BillingConfig struct {
	ProjectID string `json:"billing_project_id"`
	DatasetID string `json:"billing_dataset_id"`
	TableID   string `json:"billing_table_id"`
}

// LabelEntry represents a single label key-value pair in BigQuery repeated records
type LabelEntry struct {
	Key   bigquery.NullString `bigquery:"key"`   // Can be NULL
	Value bigquery.NullString `bigquery:"value"` // Can be NULL
}

// BigQueryBillingRow represents a row from the GCP billing export table
type BigQueryBillingRow struct {
	ServiceName    string               `bigquery:"service_name"`
	SKUDescription string               `bigquery:"sku_description"`
	UsageStartTime time.Time            `bigquery:"usage_start_time"`
	UsageEndTime   time.Time            `bigquery:"usage_end_time"`
	ProjectID      bigquery.NullString  `bigquery:"project_id"`    // Can be NULL for taxes, adjustments, shared costs
	Region         bigquery.NullString  `bigquery:"region"`        // Can be NULL
	ResourceName   bigquery.NullString  `bigquery:"resource_name"` // Can be NULL
	Cost           float64              `bigquery:"cost"`
	CreditAmount   bigquery.NullFloat64 `bigquery:"credit_amount"` // SUM of credits array, NULL when no credits
	Currency       string               `bigquery:"currency"`
	CostType       string               `bigquery:"cost_type"`
	Labels         []LabelEntry         `bigquery:"labels"`
	SystemLabels   []LabelEntry         `bigquery:"system_labels"`
	UsageAmount    float64              `bigquery:"usage_amount"`
	UsageUnit      string               `bigquery:"usage_unit"`
}
