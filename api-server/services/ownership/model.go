package ownership

import (
	"database/sql"
	"time"
)

// Resource types that can be owned. resource_key conventions:
//
//	workload       -> cloud_resource_id (uuid)
//	namespace      -> "<cloud_account_id>/<namespace>"
//	cluster        -> "<cloud_account_id>"   (resource_type 'cloud_account')
//	cloud_account  -> "<cloud_account_id>"
//	service        -> KG unique_key
//	cloud_resource -> cloud_resourses.id (uuid)
const (
	ResourceTypeWorkload      = "workload"
	ResourceTypeNamespace     = "namespace"
	ResourceTypeCloudAccount  = "cloud_account" // also the "cluster" key
	ResourceTypeService       = "service"
	ResourceTypeCloudResource = "cloud_resource"
)

const (
	OwnerTypeUser  = "user"
	OwnerTypeGroup = "group"
)

const (
	MatchScopeLabel     = "label"
	MatchScopeNamespace = "namespace"
	// MatchScopeWorkload pins specific workloads by name within one account+namespace.
	// Encoding (no extra columns): cloud_account_id = account (required),
	// match_value = namespace (required), match_key = comma-joined workload names
	// (k8s names never contain commas, so the join is lossless).
	MatchScopeWorkload = "workload"

	// Cloud-domain scopes (match a row in cloud_resourses). Encodings:
	//	cloud_tag      -> match_key = tag key, match_value = tag value
	//	cloud_type     -> match_value = type OR service_name
	//	cloud_region   -> match_value = region
	//	cloud_resource -> match_key = comma-joined cloud_resourses.id set (account required)
	MatchScopeCloudTag      = "cloud_tag"
	MatchScopeCloudType     = "cloud_type"
	MatchScopeCloudRegion   = "cloud_region"
	MatchScopeCloudResource = "cloud_resource"
)

// resource_domain partitions rules so each resolver only sees its own rows.
const (
	ResourceDomainK8s   = "k8s"
	ResourceDomainCloud = "cloud"
)

// source / via on a resolved owner.
const (
	SourceManual = "manual"
	SourceRule   = "rule"

	ViaSelf      = "self"
	ViaNamespace = "namespace"
	ViaCluster   = "cluster"
)

// ResourceOwnerRow is a stored manual owner assignment.
type ResourceOwnerRow struct {
	Id             string         `db:"id"`
	TenantId       string         `db:"tenant_id"`
	ResourceType   string         `db:"resource_type"`
	ResourceKey    string         `db:"resource_key"`
	CloudAccountId sql.NullString `db:"cloud_account_id"`
	OwnerType      string         `db:"owner_type"`
	OwnerId        string         `db:"owner_id"`
	CreatedBy      sql.NullString `db:"created_by"`
	UpdatedBy      sql.NullString `db:"updated_by"`
	CreatedAt      time.Time      `db:"created_at"`
	UpdatedAt      time.Time      `db:"updated_at"`
}

// OwnershipRuleRow is a stored, lazily-evaluated rule.
type OwnershipRuleRow struct {
	Id             string         `db:"id"`
	TenantId       string         `db:"tenant_id"`
	Name           string         `db:"name"`
	ResourceDomain string         `db:"resource_domain"` // 'k8s' | 'cloud'
	MatchScope     string         `db:"match_scope"`
	MatchKey       sql.NullString `db:"match_key"`
	MatchValue     string         `db:"match_value"`
	CloudAccountId sql.NullString `db:"cloud_account_id"`
	OwnerType      string         `db:"owner_type"`
	OwnerId        string         `db:"owner_id"`
	Priority       int            `db:"priority"`
	Enabled        bool           `db:"enabled"`
	CreatedBy      sql.NullString `db:"created_by"`
	UpdatedBy      sql.NullString `db:"updated_by"`
	CreatedAt      time.Time      `db:"created_at"`
	UpdatedAt      time.Time      `db:"updated_at"`
}

// workloadMeta is the minimal workload shape the resolver needs.
type workloadMeta struct {
	CloudAccountId string
	Namespace      string
	Name           string
	Labels         map[string]string
}

// ---- RPC request/response types ----

// ResourceRef identifies a resource to resolve / assign.
type ResourceRef struct {
	ResourceType string `json:"resource_type" mapstructure:"resource_type"`
	ResourceKey  string `json:"resource_key" mapstructure:"resource_key"`
}

type AssignOwnerRequest struct {
	ResourceType   string `json:"resource_type" mapstructure:"resource_type" validate:"required"`
	ResourceKey    string `json:"resource_key" mapstructure:"resource_key" validate:"required"`
	OwnerType      string `json:"owner_type" mapstructure:"owner_type" validate:"required"`
	OwnerId        string `json:"owner_id" mapstructure:"owner_id" validate:"required"`
	CloudAccountId string `json:"cloud_account_id" mapstructure:"cloud_account_id"`
}

type DeleteOwnerRequest struct {
	ResourceType string `json:"resource_type" mapstructure:"resource_type" validate:"required"`
	ResourceKey  string `json:"resource_key" mapstructure:"resource_key" validate:"required"`
}

type GetOwnerRequest struct {
	ResourceType string `json:"resource_type" mapstructure:"resource_type" validate:"required"`
	ResourceKey  string `json:"resource_key" mapstructure:"resource_key" validate:"required"`
}

type ResolveRequest struct {
	Resources []ResourceRef `json:"resources" mapstructure:"resources"`
}

type ListOwnersRequest struct {
	OwnerType      string `json:"owner_type" mapstructure:"owner_type"`
	OwnerId        string `json:"owner_id" mapstructure:"owner_id"`
	ResourceType   string `json:"resource_type" mapstructure:"resource_type"`
	CloudAccountId string `json:"cloud_account_id" mapstructure:"cloud_account_id"`
}

// OwnerResult is the resolved effective owner of a resource.
type OwnerResult struct {
	ResourceType string `json:"resource_type"`
	ResourceKey  string `json:"resource_key"`
	Found        bool   `json:"found"`
	OwnerType    string `json:"owner_type,omitempty"`
	OwnerId      string `json:"owner_id,omitempty"`
	OwnerName    string `json:"owner_name,omitempty"` // resolved display name (user display_name/username, or group name)
	Source       string `json:"source,omitempty"`     // manual | rule
	Via          string `json:"via,omitempty"`        // self | namespace | cluster
}

// OwnerDto is a stored owner row for listing.
type OwnerDto struct {
	Id             string `json:"id"`
	ResourceType   string `json:"resource_type"`
	ResourceKey    string `json:"resource_key"`
	CloudAccountId string `json:"cloud_account_id,omitempty"`
	OwnerType      string `json:"owner_type"`
	OwnerId        string `json:"owner_id"`
}

type StatusResponse struct {
	Status string `json:"status"`
	Count  int    `json:"count,omitempty"`
	Id     string `json:"id,omitempty"`
}

// ---- rules ----

type UpsertRuleRequest struct {
	Id             string `json:"id" mapstructure:"id"` // empty = create
	Name           string `json:"name" mapstructure:"name" validate:"required"`
	ResourceDomain string `json:"resource_domain" mapstructure:"resource_domain"` // empty ⇒ 'k8s'
	MatchScope     string `json:"match_scope" mapstructure:"match_scope" validate:"required"`
	MatchKey       string `json:"match_key" mapstructure:"match_key"`
	// match_value is required for most scopes but not cloud_resource (which uses
	// match_key only) — enforced per-scope in validateRuleRequest, not here.
	MatchValue     string `json:"match_value" mapstructure:"match_value"`
	CloudAccountId string `json:"cloud_account_id" mapstructure:"cloud_account_id"`
	OwnerType      string `json:"owner_type" mapstructure:"owner_type" validate:"required"`
	OwnerId        string `json:"owner_id" mapstructure:"owner_id" validate:"required"`
	Priority       *int   `json:"priority" mapstructure:"priority"` // nil ⇒ default 100
	Enabled        *bool  `json:"enabled" mapstructure:"enabled"`
}

type DeleteRuleRequest struct {
	Id string `json:"id" mapstructure:"id" validate:"required"`
}

type RuleDto struct {
	Id             string `json:"id"`
	Name           string `json:"name"`
	ResourceDomain string `json:"resource_domain"`
	MatchScope     string `json:"match_scope"`
	MatchKey       string `json:"match_key,omitempty"`
	MatchValue     string `json:"match_value"`
	CloudAccountId string `json:"cloud_account_id,omitempty"`
	OwnerType      string `json:"owner_type"`
	OwnerId        string `json:"owner_id"`
	Priority       int    `json:"priority"`
	Enabled        bool   `json:"enabled"`
}

func toOwnerDto(r ResourceOwnerRow) OwnerDto {
	return OwnerDto{
		Id:             r.Id,
		ResourceType:   r.ResourceType,
		ResourceKey:    r.ResourceKey,
		CloudAccountId: r.CloudAccountId.String,
		OwnerType:      r.OwnerType,
		OwnerId:        r.OwnerId,
	}
}

func toRuleDto(r OwnershipRuleRow) RuleDto {
	return RuleDto{
		Id:             r.Id,
		Name:           r.Name,
		ResourceDomain: r.ResourceDomain,
		MatchScope:     r.MatchScope,
		MatchKey:       r.MatchKey.String,
		MatchValue:     r.MatchValue,
		CloudAccountId: r.CloudAccountId.String,
		OwnerType:      r.OwnerType,
		OwnerId:        r.OwnerId,
		Priority:       r.Priority,
		Enabled:        r.Enabled,
	}
}
