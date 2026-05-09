package k8s_upgrade

import (
	"time"
)

type ClusterHealthCheckType string

const (
	PreFlightCheck  ClusterHealthCheckType = "pre_flight"
	PostFlightCheck ClusterHealthCheckType = "post_flight"
)

type HealthCheckStatus string

const (
	StatusHealthy  HealthCheckStatus = "healthy"
	StatusWarning  HealthCheckStatus = "warning"
	StatusCritical HealthCheckStatus = "critical"
	StatusUnknown  HealthCheckStatus = "unknown"
)

type AccountDetails struct {
	Type        string `db:"type" json:"type"`
	Status      string `db:"status" json:"status"`
	K8sVersion  string `db:"k8s_version" json:"k8s_version"`
	K8sProvider string `db:"k8s_provider" json:"k8s_provider"`
}

type ClusterHealthCheck struct {
	ID               string                 `json:"id"`
	TenantID         string                 `json:"tenant_id"`
	AccountID        string                 `json:"account_id"`
	CheckType        ClusterHealthCheckType `json:"check_type"`
	CheckName        string                 `json:"check_name"`
	Status           HealthCheckStatus      `json:"status"`
	Summary          string                 `json:"summary"`
	Details          map[string]interface{} `json:"details"`
	Recommendations  []string               `json:"recommendations,omitempty"`
	CreatedAt        time.Time              `json:"created_at"`
	CompletedAt      *time.Time             `json:"completed_at,omitempty"`
	EstimatedSavings *float64               `json:"estimated_savings,omitempty"`
}

type ClusterHealthSummary struct {
	TenantID      string                 `json:"tenant_id"`
	AccountID     string                 `json:"account_id"`
	CheckType     ClusterHealthCheckType `json:"check_type"`
	OverallScore  int                    `json:"overall_score"` // 0-100 scale
	TotalChecks   int                    `json:"total_checks"`
	HealthyCount  int                    `json:"healthy_count"`
	WarningCount  int                    `json:"warning_count"`
	CriticalCount int                    `json:"critical_count"`
	Checks        []ClusterHealthCheck   `json:"checks"`
	CreatedAt     time.Time              `json:"created_at"`
}

type NodeHealthCheck struct {
	NodeName         string            `json:"node_name"`
	Ready            bool              `json:"ready"`
	CPUUsage         float64           `json:"cpu_usage"`
	MemoryUsage      float64           `json:"memory_usage"`
	DiskUsage        float64           `json:"disk_usage"`
	PodCount         int               `json:"pod_count"`
	Conditions       []NodeCondition   `json:"conditions"`
	Taints           []NodeTaint       `json:"taints"`
	KubeletVersion   string            `json:"kubelet_version"`
	ContainerRuntime string            `json:"container_runtime"`
	Status           HealthCheckStatus `json:"status"`
}

type NodeCondition struct {
	Type    string `json:"type"`
	Status  string `json:"status"`
	Reason  string `json:"reason,omitempty"`
	Message string `json:"message,omitempty"`
}

type NodeTaint struct {
	Key    string `json:"key"`
	Value  string `json:"value"`
	Effect string `json:"effect"`
}

type WorkloadHealthCheck struct {
	Namespace       string            `json:"namespace"`
	WorkloadType    string            `json:"workload_type"`
	WorkloadName    string            `json:"workload_name"`
	DesiredReplicas int               `json:"desired_replicas"`
	ReadyReplicas   int               `json:"ready_replicas"`
	RestartCount    int               `json:"restart_count"`
	CPURequests     string            `json:"cpu_requests"`
	MemoryRequests  string            `json:"memory_requests"`
	CPULimits       string            `json:"cpu_limits"`
	MemoryLimits    string            `json:"memory_limits"`
	Status          HealthCheckStatus `json:"status"`
}

type ServiceHealthCheck struct {
	Namespace     string            `json:"namespace"`
	ServiceName   string            `json:"service_name"`
	ServiceType   string            `json:"service_type"`
	ClusterIP     string            `json:"cluster_ip"`
	ExternalIP    string            `json:"external_ip,omitempty"`
	Ports         []ServicePort     `json:"ports"`
	EndpointCount int               `json:"endpoint_count"`
	Status        HealthCheckStatus `json:"status"`
}

type ServicePort struct {
	Name       string `json:"name"`
	Protocol   string `json:"protocol"`
	Port       int    `json:"port"`
	TargetPort string `json:"target_port"`
}

type HealthCheckRequest struct {
	AccountID      string   `json:"account_id" validate:"required"`
	ResourceType   string   `json:"resource_type" validate:"required"`
	CurrentVersion string   `json:"current_version,omitempty"`
	TargetVersion  string   `json:"target_version,omitempty"`
	Namespaces     []string `json:"namespaces,omitempty"`
}

type ListClusterHealthChecksRequest struct {
	AccountID string `json:"account_id" validate:"required"`
	Limit     *int   `json:"limit,omitempty"`
}

type DeleteUpgradePlanRequest struct {
	AccountID string `json:"account_id" validate:"required"`
	PlanID    string `json:"plan_id" validate:"required"`
}

type PreFlightCheckRequest struct {
	AccountID string `json:"account_id" validate:"required"`
	PlanID    string `json:"plan_id" validate:"required"`
}

type PostFlightCheckRequest struct {
	AccountID string `json:"account_id" validate:"required"`
	PlanID    string `json:"plan_id" validate:"required"`
}

type RecommendationRecord struct {
	ID                   string    `db:"id"`
	TenantID             string    `db:"tenant_id"`
	CloudAccountID       string    `db:"cloud_account_id"`
	AccountObjectID      *string   `db:"account_object_id"`
	Category             string    `db:"category"`
	RuleName             string    `db:"rule_name"`
	Recommendation       string    `db:"recommendation"`
	RecommendationAction string    `db:"recommendation_action"`
	Status               string    `db:"status"`
	Severity             string    `db:"severity"`
	EstimatedSavings     *float64  `db:"estimated_savings"`
	Note                 *string   `db:"note"`
	CreatedAt            time.Time `db:"created_at"`
	UpdatedAt            time.Time `db:"updated_at"`
}

type UpgradePlanTemplate struct {
	TenantID       string `json:"tenant_id"`
	AccountID      string `json:"account_id"`
	CurrentVersion string `json:"current_version"`
	TargetVersion  string `json:"target_version"`
	K8sProvider    string `json:"k8s_provider"`
	Owner          string `json:"owner,omitempty"`
	Steps          []Step `json:"steps"`
}

type UpgradePlan struct {
	ID              string    `json:"id" db:"id"`
	CreatedAt       time.Time `json:"created_at,omitempty" db:"created_at"`
	UpdatedAt       time.Time `json:"updated_at,omitempty" db:"updated_at"`
	CreatedBy       string    `json:"created_by,omitempty" db:"created_by"`
	UpdatedBy       string    `json:"updated_by,omitempty" db:"updated_by,omitempty"`
	CurrentVersion  string    `json:"current_version" db:"current_version"`
	TargetVersion   string    `json:"target_version" db:"target_version"`
	Owner           string    `json:"owner,omitempty" db:"owner"`
	K8sProvider     string    `json:"k8s_provider" db:"k8s_provider"`
	AccountID       string    `json:"account_id" db:"account_id"`
	TenantID        string    `json:"tenant_id" db:"tenant_id"`
	Status          string    `json:"status" db:"status"`
	Steps           []Step    `json:"steps" db:"-"`
	ProgressPercent int       `json:"progress_percent,omitempty"`
}

type Step struct {
	ID          string `json:"id" db:"id"`
	Sequence    int    `json:"sequence"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Status      string `json:"status"`
	Tasks       []Task `json:"tasks"`
}

type Task struct {
	ID           string `json:"id" db:"id"`
	StepID       string `json:"step_id,omitempty" db:"step_id"`
	Sequence     int    `json:"sequence"`
	Title        string `json:"title"`
	Description  string `json:"description"`
	Status       string `json:"status"`
	Action       string `json:"action,omitempty"`
	ResourceType string `json:"resource_type,omitempty"`
	Owner        string `json:"owner,omitempty"`
	IsRequired   bool   `json:"is_required"`
}

type TaskUpsertRequest struct {
	AccountID   string `json:"account_id" validate:"required"`
	TaskID      string `json:"task_id"`
	StepID      string `json:"step_id"`
	PlanID      string `json:"plan_id"`
	Sequence    int    `json:"sequence"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Status      string `json:"status"`
	Owner       string `json:"owner,omitempty"`
	IsRequired  bool   `json:"is_required"`
	Comment     string `json:"comment,omitempty"`
}

type UpgradePlanAudit struct {
	ID        string `json:"id" db:"id"`
	TenantID  string `json:"tenant_id" db:"tenant_id"`
	AccountID string `json:"account_id" db:"account_id"`
	PlanID    string `json:"plan_id" db:"plan_id"`
	StepID    string `json:"step_id" db:"step_id"`
	TaskID    string `json:"task_id" db:"task_id"`

	CreatedAt  time.Time `json:"created_at" db:"created_at"`
	Field      string    `json:"field" db:"field"`
	Action     string    `json:"action" db:"action"`
	OldValue   string    `json:"old_value,omitempty" db:"old_value"`
	NewValue   string    `json:"new_value" db:"new_value"`
	ActionedBy string    `json:"actioned_by" db:"actioned_by"`
	Comment    string    `json:"comment,omitempty" db:"comments"`
}

type ReleaseNotesData struct {
	Releases []ReleaseInfo `json:"releases"`
}

type ReleaseInfo struct {
	Version     string `json:"version"`
	Description string `json:"description"`
}

type HealthCheck struct {
	AccountID         string                   `json:"account_id"`
	Nodes             []NodeHealth             `json:"nodes"`
	Workloads         []WorkloadHealth         `json:"workloads"`
	Services          []ServiceHealth          `json:"services"`
	PersistentVolumes []PersistentVolumeInfo   `json:"persistentVolumes"`
	LoadBalancers     []LoadBalancerInfo       `json:"load_balancers"`
	NodeGroups        []NodeGroups             `json:"node_groups"`
	APIDeprecations   *APIDeprecationResult    `json:"api_deprecations,omitempty"`
	HelmCompatibility *HelmCompatibilityResult `json:"helm_compatibility,omitempty"`
	AddOnVersions     *AddOnVersionResult      `json:"add_on_versions,omitempty"`
	DaemonSets        []DaemonSetHealth        `json:"daemonsets,omitempty"`
	Jobs              []JobHealth              `json:"jobs,omitempty"`
	CRDs              []CRDInfo                `json:"crds,omitempty"`
	Ingresses         []IngressInfo            `json:"ingresses,omitempty"`
	NetworkPolicies   []NetworkPolicyInfo      `json:"network_policies,omitempty"`
}

type NodeHealth struct {
	Name       string        `json:"name"`
	Version    string        `json:"version"`
	PoolName   string        `json:"poolName,omitempty"`
	Conditions []interface{} `json:"conditions"`
}

type WorkloadHealth struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	Type      string `json:"type"`
	Replicas  int    `json:"replicas"`
	Available int    `json:"available"`
}

type ServiceHealth struct {
	Name      string            `json:"name"`
	Namespace string            `json:"namespace"`
	Selector  map[string]string `json:"selector"`
	Type      string            `json:"type"`
	Status    string            `json:"status"`
}

type PersistentVolumeInfo struct {
	Name   string `json:"name"`
	Claim  string `json:"claim,omitempty"`
	Status string `json:"status"`
}

type LoadBalancerInfo struct {
	ServiceName      string      `json:"service_name"`
	Namespace        string      `json:"namespace"`
	Type             string      `json:"type"`
	HostName         string      `json:"hostname"`
	LoadBalancerName string      `json:"load_balancer_name"`
	CheckError       string      `json:"check_error"`
	Instances        interface{} `json:"instances,omitempty"`
}

type NodeGroups struct {
	Name              string      `json:"name"`
	Status            string      `json:"status"`
	InstanceType      string      `json:"instance_type"`
	AmiType           string      `json:"ami_type"`
	CapacityType      string      `json:"capacity_type"`
	MinSize           int         `json:"min_size"`
	MaxSize           int         `json:"max_size"`
	DesiredSize       int         `json:"desired_size"`
	DiskSize          int         `json:"disk_size"`
	KubernetesVersion string      `json:"kubernetes_version"`
	ReleaseVersion    string      `json:"release_version"`
	RemoteAccess      bool        `json:"remote_access"`
	Subnets           []string    `json:"subnets"`
	Tags              interface{} `json:"tags,omitempty"`
	LaunchTemplate    interface{} `json:"launch_template,omitempty"`
	TaintsAndLabels   interface{} `json:"taints_and_labels,omitempty"`
	Nodes             []NodeInfo  `json:"nodes,omitempty"`
	CheckError        string      `json:"check_error,omitempty"`
}

type NodeInfo struct {
	Name             string `json:"name"`
	InstanceID       string `json:"instance_id"`
	Status           string `json:"status"`
	InstanceType     string `json:"instance_type,omitempty"`
	AvailabilityZone string `json:"availability_zone,omitempty"`
	KubeletVersion   string `json:"kubelet_version,omitempty"`
	Ready            bool   `json:"ready"`
}

type ExecuteCommandRequest struct {
	AccountID   string `json:"account_id" validate:"required"`
	PlanID      string `json:"plan_id" validate:"required"`
	StepID      string `json:"step_id" validate:"required"`
	TaskID      string `json:"task_id" validate:"required"`
	Command     string `json:"command" validate:"required"`
	CommandType string `json:"command_type" validate:"required"`
}

type ExecuteCommandResponse struct {
	Success bool   `json:"success"`
	Output  string `json:"output,omitempty"`
	Error   string `json:"error,omitempty"`
}

// --- Phase 1: Server-side check types (moved from agent) ---

// APIDeprecation represents a single Kubernetes API deprecation/deletion entry
type APIDeprecation struct {
	Kind              string `json:"kind"`
	Group             string `json:"group"`
	Version           string `json:"version"`
	DeprecatedVersion string `json:"deprecated_version,omitempty"`
	DeletedVersion    string `json:"deleted_version,omitempty"`
	ReplacementGroup  string `json:"replacement_group,omitempty"`
	ReplacementVer    string `json:"replacement_version,omitempty"`
	ReplacementKind   string `json:"replacement_kind,omitempty"`
	Description       string `json:"description,omitempty"`
}

// APIDeprecationRegistry holds the per-version deprecation data fetched from the registry
type APIDeprecationRegistry map[string]struct {
	Deleted    []APIDeprecation `json:"deleted"`
	Deprecated []APIDeprecation `json:"deprecated"`
}

// APIDeprecationResult represents the result of an API deprecation check
type APIDeprecationResult struct {
	TargetVersion    string                  `json:"target_version"`
	DeletedAPIs      []APIDeprecationFinding `json:"deleted_apis"`
	DeprecatedAPIs   []APIDeprecationFinding `json:"deprecated_apis"`
	TotalDeleted     int                     `json:"total_deleted"`
	TotalDeprecated  int                     `json:"total_deprecated"`
	ResourcesScanned int                     `json:"resources_scanned"`
}

// APIDeprecationFinding is a deprecated/deleted API that is actually in use in the cluster
type APIDeprecationFinding struct {
	APIDeprecation
	InUse bool `json:"in_use"`
}

// HelmRelease represents a decoded Helm release from a Kubernetes secret
type HelmRelease struct {
	Name         string `json:"name"`
	Namespace    string `json:"namespace"`
	ChartName    string `json:"chart_name"`
	ChartVersion string `json:"chart_version"`
	AppVersion   string `json:"app_version,omitempty"`
	KubeVersion  string `json:"kube_version,omitempty"`
	Status       string `json:"status"`
}

// HelmCompatibilityResult represents the result of a Helm compatibility check
type HelmCompatibilityResult struct {
	TargetVersion string                     `json:"target_version"`
	TotalReleases int                        `json:"total_releases"`
	Compatible    int                        `json:"compatible"`
	Incompatible  int                        `json:"incompatible"`
	Unknown       int                        `json:"unknown"`
	Releases      []HelmReleaseCompatibility `json:"releases"`
}

// HelmReleaseCompatibility is the compatibility result for a single Helm release
type HelmReleaseCompatibility struct {
	HelmRelease
	Compatible string `json:"compatible"` // "yes", "no", "unknown"
	Reason     string `json:"reason,omitempty"`
}

// AddOnInfo represents a cluster add-on discovered via kubectl
type AddOnInfo struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	Type      string `json:"type"` // Deployment, DaemonSet
	Image     string `json:"image"`
	Version   string `json:"version"`
	Ready     bool   `json:"ready"`
}

// AddOnVersionResult represents the result of an add-on version scan
type AddOnVersionResult struct {
	TargetVersion string      `json:"target_version"`
	AddOns        []AddOnInfo `json:"add_ons"`
	TotalAddOns   int         `json:"total_add_ons"`
}

// DaemonSetHealth represents health status for a DaemonSet workload
type DaemonSetHealth struct {
	Name             string `json:"name"`
	Namespace        string `json:"namespace"`
	DesiredScheduled int    `json:"desired_scheduled"`
	CurrentScheduled int    `json:"current_scheduled"`
	Ready            int    `json:"ready"`
	Available        int    `json:"available"`
}

// JobHealth represents health status for a Job workload
type JobHealth struct {
	Name        string `json:"name"`
	Namespace   string `json:"namespace"`
	Active      int    `json:"active"`
	Succeeded   int    `json:"succeeded"`
	Failed      int    `json:"failed"`
	Completions int    `json:"completions"`
}

// CRDInfo represents a Custom Resource Definition in the cluster (M2)
type CRDInfo struct {
	Name       string `json:"name"`
	Group      string `json:"group"`
	Version    string `json:"version"`
	Scope      string `json:"scope"`     // Namespaced or Cluster
	Served     bool   `json:"served"`    // actively served version
	Stored     bool   `json:"stored"`    // storage version
	Instances  int    `json:"instances"` // number of CR instances
	CheckError string `json:"check_error,omitempty"`
}

// IngressInfo represents an Ingress resource health check (M5)
type IngressInfo struct {
	Name      string   `json:"name"`
	Namespace string   `json:"namespace"`
	Class     string   `json:"class,omitempty"`
	Hosts     []string `json:"hosts,omitempty"`
	TLS       bool     `json:"tls"`
	Status    string   `json:"status"` // "Healthy" or "Unhealthy"
	Address   string   `json:"address,omitempty"`
}

// NetworkPolicyInfo represents a NetworkPolicy health check (M5)
type NetworkPolicyInfo struct {
	Name        string `json:"name"`
	Namespace   string `json:"namespace"`
	PodSelector string `json:"pod_selector"`
	PolicyTypes string `json:"policy_types"` // "Ingress", "Egress", or "Ingress,Egress"
}
