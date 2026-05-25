package collective

import "time"

// Entry captures a piece of tenant-scoped shared knowledge.
// Cross-user within a tenant; module-tagged for routing.
type Entry struct {
	ID            string    `db:"id" json:"id"`
	TenantID      string    `db:"tenant_id" json:"tenant_id"`
	AgentModule   *string   `db:"agent_module" json:"agent_module,omitempty"`
	EntryKind     string    `db:"entry_kind" json:"entry_kind"`
	Subject       string    `db:"subject" json:"subject"`
	Body          string    `db:"body" json:"body"`
	MetaJSON      []byte    `db:"metadata" json:"-"`
	Metadata      any       `db:"-" json:"metadata,omitempty"`
	SourceEventID *string   `db:"source_event_id" json:"source_event_id,omitempty"`
	Confidence    float64   `db:"confidence" json:"confidence"`
	CuratedBy     *string   `db:"curated_by" json:"curated_by,omitempty"`
	CreatedAt     time.Time `db:"created_at" json:"created_at"`
	UpdatedAt     time.Time `db:"updated_at" json:"updated_at"`
}

// Entry kinds — maps to retired memory_types for backfill.
const (
	KindArchitecturalFact = "architectural_fact"
	KindConfigInsight     = "configuration_insight"
	KindDependencyMapping = "dependency_mapping"
	KindTroubleshooting   = "troubleshooting"
	KindRunbookIndex      = "runbook_index"
)
