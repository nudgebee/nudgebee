package dashboard

import (
	"time"
)

// PanelDatasource names the family a panel's data comes from. `metrics`, `logs`
// and `traces` route to the corresponding observability provider interface for
// the account; `nudgebee` routes to the internal query engine (events,
// recommendations, spend, tickets) and needs no external provider at all.
//
// `redis`, `rabbitmq` and `postgresql` are a different shape: they run a
// READ-ONLY command against the account's integration through the relay and
// tabulate the output. They return a snapshot, not a series, so they render as a
// table and ignore the dashboard's time range. See integration_query.go.
//
// Each value doubles as the INTEGRATION TYPE looked up for the account, so it
// must match the name the integration registers (`postgresql`, not `postgres`).
const (
	DatasourceMetrics  = "metrics"
	DatasourceLogs     = "logs"
	DatasourceTraces   = "traces"
	DatasourceNudgebee = "nudgebee"
	DatasourceRedis    = "redis"
	DatasourceRabbitMQ = "rabbitmq"
	DatasourcePostgres = "postgresql"
)

// IsCommandDatasource reports whether a panel's data comes from running a
// command through the relay rather than from an observability provider.
func IsCommandDatasource(datasource string) bool {
	return datasource == DatasourceRedis || datasource == DatasourceRabbitMQ || datasource == DatasourcePostgres
}

// Panel visualisation types supported by the renderer. Kept deliberately small —
// every entry here must have a matching branch in the frontend panel registry,
// so adding one is a two-sided change.
const (
	VizTimeseries = "timeseries"
	VizStat       = "stat"
	VizGauge      = "gauge"
	VizTable      = "table"
	VizBar        = "bar"
	VizText       = "text"
)

const (
	StatusActive   = "active"
	StatusArchived = "archived"
)

// PanelTarget is one query inside a panel. Exactly one of Expr (raw expression,
// e.g. PromQL) or Query (internal query-engine request) is meaningful, selected
// by the owning panel's Datasource.
type PanelTarget struct {
	RefId string `json:"ref_id"`
	// Expr is the raw provider expression for metrics/logs/traces panels.
	Expr string `json:"expr,omitempty"`
	// Query is the internal query-engine request for `nudgebee` panels. Held
	// untyped so the query package stays the single owner of its own shape and
	// this package never has to track its evolution.
	//
	// A map rather than json.RawMessage: the action layer decodes with
	// mapstructure, which sees a nested JSON object as a map and cannot assign
	// it to a []byte ("source data must be an array or slice, got map").
	Query map[string]any `json:"query,omitempty"`
	// LegendFormat names the series in the chart legend, e.g. "{{route}}".
	LegendFormat string `json:"legend_format,omitempty"`
	// TimeColumn is the column a `nudgebee` panel applies the dashboard's time
	// range to. A row query has no inherent time axis, so an empty value means
	// the panel ignores the time picker.
	TimeColumn string `json:"time_column,omitempty"`
	Hide       bool   `json:"hide,omitempty"`
}

// GridPos mirrors the Grafana panel model so imported dashboards need no
// coordinate translation. w is in 12ths of the dashboard width.
type GridPos struct {
	X int `json:"x"`
	Y int `json:"y"`
	W int `json:"w"`
	H int `json:"h"`
}

type Panel struct {
	Id          int    `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	Type        string `json:"type"`
	Datasource  string `json:"datasource"`
	// A panel names the accounts it queries — not the dashboard — because every
	// observability backend resolves its provider integration from an account id
	// and rejects an empty one, and one dashboard is expected to compare several
	// accounts side by side.
	//
	// Exactly one of AccountType / AccountIds must be set on a non-text panel;
	// see ValidateDefinition. They are mutually exclusive rather than merged so
	// the panel has one unambiguous answer to "which accounts is this?".
	//
	// AccountType selects EVERY account of one cloud provider (K8S / AWS / GCP /
	// AZURE). It is resolved at RENDER time against the accounts the viewer can
	// see, so the same panel widens as accounts are added and never shows a
	// viewer an account they lack access to.
	AccountType string `json:"account_type,omitempty"`
	// AccountIds selects specific accounts, pinned at authoring time.
	AccountIds []string `json:"account_ids,omitempty"`
	// LegacyAccountId is the single-account field this panel model replaced.
	// Definitions written before the change still carry it; upgradeDefinition
	// folds it into AccountIds on read and clears it, so `omitempty` drops it the
	// next time the dashboard is written. Never set this.
	LegacyAccountId string        `json:"account_id,omitempty"`
	GridPos         GridPos       `json:"grid_pos"`
	Targets         []PanelTarget `json:"targets,omitempty"`
	Unit            string        `json:"unit,omitempty"`
	// Content backs the `text` panel type.
	Content string `json:"content,omitempty"`
	// Options carries per-visualisation settings the renderer understands.
	Options map[string]any `json:"options,omitempty"`
}

// Definition is the whole panel document. Stored as one JSONB blob because
// panels are always read and written together and nothing queries them by
// inner field.
type Definition struct {
	Panels []Panel `json:"panels"`
	// TimeFrom is a relative default range for the dashboard, e.g. "now-6h".
	TimeFrom string `json:"time_from,omitempty"`
	Refresh  string `json:"refresh,omitempty"`
}

type Dashboard struct {
	Id            string     `json:"id"`
	TenantId      string     `json:"tenant_id"`
	Slug          string     `json:"slug"`
	Title         string     `json:"title"`
	Description   string     `json:"description,omitempty"`
	Definition    Definition `json:"definition"`
	SchemaVersion int        `json:"schema_version"`
	Tags          []string   `json:"tags"`
	Status        string     `json:"status"`
	IsBuiltin     bool       `json:"is_builtin"`
	CreatedBy     *string    `json:"created_by,omitempty"`
	UpdatedBy     *string    `json:"updated_by,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
	// Version is the highest revision number in dashboard_versions. Populated on
	// Get, omitted on List to keep the listing a single query.
	Version int `json:"version,omitempty"`
}

type Binding struct {
	Id          string         `json:"id,omitempty"`
	DashboardId string         `json:"dashboard_id,omitempty"`
	ScopeType   string         `json:"scope_type"`
	MatchKind   string         `json:"match_kind"`
	MatchValue  map[string]any `json:"match_value"`
	Priority    int            `json:"priority"`
}

type Version struct {
	Version    int        `json:"version"`
	Message    string     `json:"message,omitempty"`
	CreatedBy  *string    `json:"created_by,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	Definition Definition `json:"definition,omitempty"`
}

// ---- request shapes ----

type ListRequest struct {
	Search string `json:"search,omitempty"`
	Limit  int    `json:"limit,omitempty"`
	Offset int    `json:"offset,omitempty"`
}

// Ids are compared against uuid COLUMNS, so a non-uuid string reaches Postgres
// as `invalid input syntax for type uuid` — a 500 from the driver rather than a
// 400 naming the bad field. `uuid` rather than the repo's more common `uuid4`:
// every id here is gen_random_uuid() today, but rejecting a valid non-v4 id
// would be a worse failure than accepting one.
type GetRequest struct {
	Id string `json:"id" validate:"required,uuid"`
}

type SaveRequest struct {
	// Id empty => create, set => update.
	Id          string     `json:"id,omitempty" validate:"omitempty,uuid"`
	Title       string     `json:"title" validate:"required"`
	Description string     `json:"description,omitempty"`
	Definition  Definition `json:"definition"`
	Tags        []string   `json:"tags,omitempty"`
	Status      string     `json:"status,omitempty"`
	Bindings    []Binding  `json:"bindings,omitempty"`
	// Message is an optional changelog line stored on the new version row.
	Message string `json:"message,omitempty"`
}

type DeleteRequest struct {
	Id string `json:"id" validate:"required,uuid"`
}

// ExecuteQueryRequest runs one command-datasource panel for one account. The
// panel is not passed by id: an unsaved panel in the editor has none, and the
// command is re-validated here regardless of where it came from.
type ExecuteQueryRequest struct {
	AccountId  string `json:"account_id" validate:"required"`
	Datasource string `json:"datasource" validate:"required"`
	Command    string `json:"command" validate:"required"`
}

// QueryResult is the tabulated output of a command datasource. Strings, not
// typed values: `redis-cli INFO` and `rabbitmqadmin list` both emit text, and
// guessing types would misread version strings and ids as numbers.
type QueryResult struct {
	Columns []string   `json:"columns"`
	Rows    [][]string `json:"rows"`
	// Truncated is set when Rows was capped, so the panel can say so rather than
	// silently showing a prefix of the answer.
	Truncated bool `json:"truncated,omitempty"`
}

// EntityQueryRequest runs one `nudgebee` panel against the internal query
// engine. The account and time filters are separate from the stored query
// because they belong to the panel's scope and the dashboard's time picker, not
// to the query the author wrote.
type EntityQueryRequest struct {
	AccountIds []string `json:"account_ids" validate:"required"`
	// Datasource decides which tables the query may name — a `traces` panel
	// cannot read events and vice versa.
	Datasource string `json:"datasource" validate:"required"`
	// Query is a query-engine QueryRequest, exactly as stored on the panel
	// target — and a map for the same mapstructure reason as PanelTarget.Query.
	Query map[string]any `json:"query" validate:"required"`
	// TimeColumn names the column the dashboard's range applies to (events have
	// several: starts_at, created_at, ends_at). Empty means no time filter.
	TimeColumn string `json:"time_column,omitempty"`
	StartTime  int64  `json:"start_time,omitempty"`
	EndTime    int64  `json:"end_time,omitempty"`
}

// ResolveRequest asks "which dashboards auto-attach to this entity". Backs the
// contextual (Class A) surface on workload / pod / node detail pages.
type ResolveRequest struct {
	AccountId string `json:"account_id" validate:"required"`
	ScopeType string `json:"scope_type" validate:"required"`
	Name      string `json:"name,omitempty"`
	Namespace string `json:"namespace,omitempty"`
	AppType   string `json:"app_type,omitempty"`
}
