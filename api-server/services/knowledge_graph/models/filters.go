package models

import (
	"time"

	"github.com/google/uuid"
)

// KnowledgeGraphTenantFilter represents a knowledge graph filter configuration for a tenant
type KnowledgeGraphTenantFilter struct {
	ID                uuid.UUID              `json:"id" db:"id"`
	TenantID          uuid.UUID              `json:"tenant_id" db:"tenant_id"`
	FilterName        string                 `json:"filter_name" db:"filter_name"`
	AccountIDs        []string               `json:"account_ids" db:"account_ids"`
	Sources           []string               `json:"sources" db:"sources"`
	FlowSources       []string               `json:"flow_sources" db:"flow_sources"`
	Filters           map[string]interface{} `json:"filters" db:"filters"`
	IsDefault         bool                   `json:"is_default" db:"is_default"`
	Enabled           bool                   `json:"enabled" db:"enabled"`
	CreatedAt         time.Time              `json:"created_at" db:"created_at"`
	LastSyncVersion   int64                  `json:"last_sync_version" db:"last_sync_version"`
	LastSyncTimestamp *time.Time             `json:"last_sync_time,omitempty" db:"last_sync_time"`
}

// ToSlices converts fields to string slices for easier use (now a no-op since they're already slices)
func (f *KnowledgeGraphTenantFilter) ToSlices() (accountIDs []string, sources []string, flowSources []string) {
	return f.AccountIDs, f.Sources, f.FlowSources
}
