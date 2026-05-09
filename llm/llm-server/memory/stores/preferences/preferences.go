package preferences

import "time"

// Preference captures a typed user preference. One row per unique
// (tenant, user, module, key).
type Preference struct {
	ID          string    `db:"id" json:"id"`
	TenantID    string    `db:"tenant_id" json:"tenant_id"`
	UserID      string    `db:"user_id" json:"user_id"`
	AgentModule *string   `db:"agent_module" json:"agent_module,omitempty"` // nil = cross-agent
	Key         string    `db:"key" json:"key"`
	ValueJSON   []byte    `db:"value" json:"-"`
	Value       any       `db:"-" json:"value"`
	Source      string    `db:"source" json:"source"`         // "explicit" | "inferred"
	Confidence  float64   `db:"confidence" json:"confidence"` // 0..1
	CreatedAt   time.Time `db:"created_at" json:"created_at"`
	UpdatedAt   time.Time `db:"updated_at" json:"updated_at"`
}

// Known preference keys (cross-agent and module-specific).
const (
	KeyTimezone            = "timezone"
	KeyNotificationChannel = "notification_channel"
	KeyDefaultAccount      = "default_account"
	KeyDefaultCluster      = "default_cluster"
	KeyPreferredCloud      = "preferred_cloud"
	KeyPreferredObsTool    = "preferred_observability_tool"
	KeyConfirmDestructive  = "confirm_destructive"

	// Module-specific keys
	KeyFinopsCostAllocationDim = "finops.cost_allocation_dim"
	KeySREAlertThreshold       = "sre.alert_threshold"
	KeyK8sDeploymentStrategy   = "k8s.deployment_strategy"
)

// Source values.
const (
	SourceExplicit = "explicit"
	SourceInferred = "inferred"
)
