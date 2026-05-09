package cloudfoundry

import "time"

// CF API v3 response models

// cfPagination represents the pagination structure in CF API v3 responses.
type cfPagination struct {
	TotalResults int `json:"total_results"`
	TotalPages   int `json:"total_pages"`
	First        struct {
		Href string `json:"href"`
	} `json:"first"`
	Last struct {
		Href string `json:"href"`
	} `json:"last"`
	Next *struct {
		Href string `json:"href"`
	} `json:"next"`
	Previous *struct {
		Href string `json:"href"`
	} `json:"previous"`
}

// cfInfo represents the response from GET /v3/info (used for UAA discovery).
type cfInfo struct {
	Build  string `json:"build"`
	CLIVer struct {
		Minimum     string `json:"minimum"`
		Recommended string `json:"recommended"`
	} `json:"cli_version"`
	Links struct {
		Self struct {
			Href string `json:"href"`
		} `json:"self"`
		UAA struct {
			Href string `json:"href"`
		} `json:"uaa"`
		Login struct {
			Href string `json:"href"`
		} `json:"login"`
		LogCache struct {
			Href string `json:"href"`
		} `json:"log_cache"`
	} `json:"links"`
}

// cfApp represents a Cloud Foundry application from GET /v3/apps.
type cfApp struct {
	GUID      string             `json:"guid"`
	Name      string             `json:"name"`
	State     string             `json:"state"` // STARTED, STOPPED
	CreatedAt time.Time          `json:"created_at"`
	UpdatedAt time.Time          `json:"updated_at"`
	Lifecycle cfLifecycle        `json:"lifecycle"`
	Metadata  cfMetadata         `json:"metadata"`
	Relations cfAppRelationships `json:"relationships"`
	Links     map[string]cfLink  `json:"links"`
}

type cfLifecycle struct {
	Type string          `json:"type"` // buildpack, docker, kpack
	Data cfLifecycleData `json:"data"`
}

type cfLifecycleData struct {
	Buildpacks []string `json:"buildpacks"`
	Stack      string   `json:"stack"`
}

type cfMetadata struct {
	Labels      map[string]string `json:"labels"`
	Annotations map[string]string `json:"annotations"`
}

type cfAppRelationships struct {
	Space cfRelationship `json:"space"`
}

type cfRelationship struct {
	Data struct {
		GUID string `json:"guid"`
	} `json:"data"`
}

type cfLink struct {
	Href   string `json:"href"`
	Method string `json:"method,omitempty"`
}

// cfSpace represents a Cloud Foundry space from GET /v3/spaces.
type cfSpace struct {
	GUID      string               `json:"guid"`
	Name      string               `json:"name"`
	CreatedAt time.Time            `json:"created_at"`
	UpdatedAt time.Time            `json:"updated_at"`
	Metadata  cfMetadata           `json:"metadata"`
	Relations cfSpaceRelationships `json:"relationships"`
}

type cfSpaceRelationships struct {
	Organization cfRelationship `json:"organization"`
}

// cfOrg represents a Cloud Foundry organization from GET /v3/organizations.
type cfOrg struct {
	GUID      string     `json:"guid"`
	Name      string     `json:"name"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	Metadata  cfMetadata `json:"metadata"`
	Suspended bool       `json:"suspended"`
}

// cfRoute represents a Cloud Foundry route from GET /v3/routes.
type cfRoute struct {
	GUID         string               `json:"guid"`
	Host         string               `json:"host"`
	Path         string               `json:"path"`
	URL          string               `json:"url"`
	Protocol     string               `json:"protocol"`
	CreatedAt    time.Time            `json:"created_at"`
	UpdatedAt    time.Time            `json:"updated_at"`
	Metadata     cfMetadata           `json:"metadata"`
	Destinations []cfRouteDestination `json:"destinations"`
	Relations    struct {
		Space  cfRelationship `json:"space"`
		Domain cfRelationship `json:"domain"`
	} `json:"relationships"`
}

type cfRouteDestination struct {
	GUID string `json:"guid"`
	App  struct {
		GUID    string `json:"guid"`
		Process struct {
			Type string `json:"type"`
		} `json:"process"`
	} `json:"app"`
	Port     int    `json:"port"`
	Protocol string `json:"protocol"`
}

// cfServiceInstance represents a managed or user-provided service instance from GET /v3/service_instances.
type cfServiceInstance struct {
	GUID            string     `json:"guid"`
	Name            string     `json:"name"`
	Type            string     `json:"type"` // managed, user-provided
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
	Metadata        cfMetadata `json:"metadata"`
	Tags            []string   `json:"tags"`
	MaintenanceInfo struct {
		Version string `json:"version"`
	} `json:"maintenance_info"`
	UpgradeAvailable bool   `json:"upgrade_available"`
	DashboardURL     string `json:"dashboard_url"`
	LastOperation    struct {
		Type        string    `json:"type"`  // create, update, delete
		State       string    `json:"state"` // succeeded, in progress, failed
		Description string    `json:"description"`
		CreatedAt   time.Time `json:"created_at"`
		UpdatedAt   time.Time `json:"updated_at"`
	} `json:"last_operation"`
	Relations struct {
		Space       cfRelationship `json:"space"`
		ServicePlan cfRelationship `json:"service_plan"`
	} `json:"relationships"`
}

// cfProcess represents a CF process (web, worker, etc.) from GET /v3/processes.
type cfProcess struct {
	GUID       string `json:"guid"`
	Type       string `json:"type"` // web, worker, etc.
	Instances  int    `json:"instances"`
	MemoryInMB int    `json:"memory_in_mb"`
	DiskInMB   int    `json:"disk_in_mb"`
	Command    string `json:"command"`
	Relations  struct {
		App cfRelationship `json:"app"`
	} `json:"relationships"`
	HealthCheck struct {
		Type string `json:"type"` // http, port, process
		Data struct {
			Timeout           int    `json:"timeout"`
			InvocationTimeout int    `json:"invocation_timeout"`
			Endpoint          string `json:"endpoint"`
		} `json:"data"`
	} `json:"health_check"`
}

// cfProcessStats represents instance stats from GET /v3/processes/:guid/stats.
type cfProcessStats struct {
	Resources []cfProcessStatsResource `json:"resources"`
}

type cfProcessStatsResource struct {
	Type      string         `json:"type"`
	Index     int            `json:"index"`
	State     string         `json:"state"` // RUNNING, CRASHED, STARTING, DOWN
	Usage     cfProcessUsage `json:"usage"`
	Host      string         `json:"host"`
	Uptime    int64          `json:"uptime"`
	MemQuota  int64          `json:"mem_quota"`
	DiskQuota int64          `json:"disk_quota"`
	FdsQuota  int            `json:"fds_quota"`
}

type cfProcessUsage struct {
	Time string  `json:"time"`
	CPU  float64 `json:"cpu"`
	Mem  int64   `json:"mem"`
	Disk int64   `json:"disk"`
}

// cfAuditEvent represents an audit event from GET /v3/audit_events.
type cfAuditEvent struct {
	GUID      string    `json:"guid"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Type      string    `json:"type"` // e.g., "audit.app.start", "audit.app.stop"
	Actor     struct {
		GUID string `json:"guid"`
		Type string `json:"type"`
		Name string `json:"name"`
	} `json:"actor"`
	Target struct {
		GUID string `json:"guid"`
		Type string `json:"type"`
		Name string `json:"name"`
	} `json:"target"`
	Space struct {
		GUID string `json:"guid"`
	} `json:"space"`
	Organization struct {
		GUID string `json:"guid"`
	} `json:"organization"`
	Data map[string]any `json:"data"`
}

// cfBuild represents a CF build from GET /v3/builds.
type cfBuild struct {
	GUID      string      `json:"guid"`
	State     string      `json:"state"` // STAGING, STAGED, FAILED
	CreatedAt time.Time   `json:"created_at"`
	UpdatedAt time.Time   `json:"updated_at"`
	Error     *string     `json:"error"`
	Lifecycle cfLifecycle `json:"lifecycle"`
	Package   struct {
		GUID string `json:"guid"`
	} `json:"package"`
	Droplet *struct {
		GUID string `json:"guid"`
	} `json:"droplet"`
	CreatedBy struct {
		GUID  string `json:"guid"`
		Name  string `json:"name"`
		Email string `json:"email"`
	} `json:"created_by"`
	Relations struct {
		App cfRelationship `json:"app"`
	} `json:"relationships"`
	Metadata cfMetadata `json:"metadata"`
}

// cfDeployment represents a CF deployment from GET /v3/deployments.
type cfDeployment struct {
	GUID      string    `json:"guid"`
	State     string    `json:"state"`    // DEPLOYING, DEPLOYED, CANCELING, CANCELED
	Strategy  string    `json:"strategy"` // rolling, canary
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Status    struct {
		Value  string `json:"value"`  // ACTIVE, FINALIZED
		Reason string `json:"reason"` // DEPLOYING, DEPLOYED, CANCELED, SUPERSEDED
	} `json:"status"`
	Droplet struct {
		GUID string `json:"guid"`
	} `json:"droplet"`
	PreviousDroplet struct {
		GUID string `json:"guid"`
	} `json:"previous_droplet"`
	NewProcesses []struct {
		GUID string `json:"guid"`
		Type string `json:"type"`
	} `json:"new_processes"`
	Relations struct {
		App cfRelationship `json:"app"`
	} `json:"relationships"`
	Metadata cfMetadata `json:"metadata"`
}

// cfTask represents a CF task from GET /v3/tasks.
type cfTask struct {
	GUID       string    `json:"guid"`
	Name       string    `json:"name"`
	Command    string    `json:"command"`
	State      string    `json:"state"` // RUNNING, SUCCEEDED, FAILED, CANCELING
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
	MemoryInMB int       `json:"memory_in_mb"`
	DiskInMB   int       `json:"disk_in_mb"`
	SequenceID int       `json:"sequence_id"`
	Result     struct {
		FailureReason string `json:"failure_reason"`
	} `json:"result"`
	Relations struct {
		App cfRelationship `json:"app"`
	} `json:"relationships"`
	Metadata cfMetadata `json:"metadata"`
}

// cfServiceBinding represents a service credential binding from GET /v3/service_credential_bindings.
type cfServiceBinding struct {
	GUID          string    `json:"guid"`
	Name          string    `json:"name"`
	Type          string    `json:"type"` // app, key
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
	LastOperation struct {
		Type        string    `json:"type"`
		State       string    `json:"state"`
		Description string    `json:"description"`
		CreatedAt   time.Time `json:"created_at"`
		UpdatedAt   time.Time `json:"updated_at"`
	} `json:"last_operation"`
	Relations struct {
		App             cfRelationship `json:"app"`
		ServiceInstance cfRelationship `json:"service_instance"`
	} `json:"relationships"`
	Metadata cfMetadata `json:"metadata"`
}

// logCacheResponse is the top-level response from Log Cache /api/v1/read.
type logCacheResponse struct {
	Envelopes struct {
		Batch []logCacheEnvelope `json:"batch"`
	} `json:"envelopes"`
}

type logCacheEnvelope struct {
	Timestamp  string            `json:"timestamp"` // nanoseconds as string
	SourceID   string            `json:"source_id"`
	InstanceID string            `json:"instance_id"`
	Tags       map[string]string `json:"tags"`
	Log        *logCacheLog      `json:"log"`
}

type logCacheLog struct {
	Payload string `json:"payload"` // base64-encoded
	Type    string `json:"type"`    // OUT or ERR
}

// uaaTokenResponse represents the OAuth2 token response from UAA.
type uaaTokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int    `json:"expires_in"`
	Scope       string `json:"scope"`
	JTI         string `json:"jti"`
}

// cfAccountData stores Cloud Foundry-specific configuration in Account.Data JSON.
type cfAccountData struct {
	CFAPIURL    string `json:"cf_api_url"`
	SkipSSL     bool   `json:"skip_ssl"`
	LogCacheURL string `json:"log_cache_url"` // optional, auto-discovered from /v3/info or CF API URL pattern
}
