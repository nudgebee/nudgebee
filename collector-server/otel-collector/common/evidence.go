package common

import "time"

type Evidences struct {
	Data []interface{}
}

type Evidence struct {
	Data     interface{} `json:"data,omitempty"`
	Type     string      `json:"type,omitempty"`
	Insights []Insight   `json:"insight,omitempty"`
	Filename string      `json:"filename,omitempty"`
	StartAt  string      `json:"start_at,omitempty"`
}

type Insight struct {
	Message  string `json:"message,omitempty"`
	Severity string `json:"severity,omitempty"`
}

type DeploymentChange struct {
	Data     DiffData  `json:"data,omitempty"`
	Type     string    `json:"type,omitempty"`
	Insights []Insight `json:"insight,omitempty"`
	StartAt  time.Time `json:"start_at,omitempty"`
}

type DiffData struct {
	New              string   `json:"new,omitempty"`
	Old              string   `json:"old,omitempty"`
	NumAdditions     int      `json:"num_additions,omitempty"`
	NumDeletions     int      `json:"num_deletions,omitempty"`
	ResourceName     string   `json:"resource_name,omitempty"`
	UpdatedPaths     []string `json:"updated_paths,omitempty"`
	NumModifications int      `json:"num_modifications,omitempty"`
}

type PodDetails struct {
	Data     PodData       `json:"data,omitempty"`
	Type     string        `json:"type,omitempty"`
	Insights []interface{} `json:"insight,omitempty"`
}

type PodData struct {
	Name            string `json:"name,omitempty"`
	SourcePod       string `json:"source_pod,omitempty"`
	SourceNamespace string `json:"source_namespace,omitempty"`
	Path            string `json:"path,omitempty"`
	Status          string `json:"status,omitempty"`
}

type LogDetails struct {
	Data     string        `json:"data,omitempty"`
	Type     string        `json:"type,omitempty"`
	Insights []interface{} `json:"insight,omitempty"`
	Filename string        `json:"filename,omitempty"`
}

type FailureData struct {
	Name            string `json:"name,omitempty"`
	SourcePod       string `json:"source_pod,omitempty"`
	SourceNamespace string `json:"source_namespace,omitempty"`
	Path            string `json:"path,omitempty"`
	Status          string `json:"status,omitempty"`
}
type ApiFailures struct {
	Data    FailureData   `json:"data,omitempty"`
	Type    string        `json:"type,omitempty"`
	Insight []interface{} `json:"insight,omitempty"`
}

type Event struct {
	Id               string          `json:"id,omitempty"`
	Evidences        InvestigateData `json:"evidences,omitempty"`
	CreatedAt        string          `json:"created_at,omitempty"`
	UpdatedAt        string          `json:"updated_at,omitempty"`
	FindingId        string          `json:"finding_id,omitempty"`
	Title            string          `json:"title,omitempty"`
	Description      string          `json:"description,omitempty"`
	Source           string          `json:"source,omitempty"`
	AggregationKey   string          `json:"aggregation_key,omitempty"`
	Failure          string          `json:"failure,omitempty"`
	FindingType      string          `json:"finding_type,omitempty"`
	Category         string          `json:"category,omitempty"`
	Priority         string          `json:"priority,omitempty"`
	SubjectType      string          `json:"subject_type,omitempty"`
	SubjectName      string          `json:"subject_name,omitempty"`
	SubjectNamespace string          `json:"subject_namespace,omitempty"`
	SubjectNode      string          `json:"subject_node,omitempty"`
	EndsAt           string          `json:"ends_at,omitempty"`
	StartsAt         string          `json:"starts_at,omitempty"`
	Fingerprint      string          `json:"fingerprint,omitempty"`
	CloudAccountId   string          `json:"cloud_account_id,omitempty"`
	Status           string          `json:"status,omitempty"`
}

type InvestigateData struct {
	LogData          string
	ErrorLogData     []string
	PodMetrics       any
	NodeMetrics      any
	PodData          map[string]any
	NodeData         any
	Deployment       map[string]any
	NoisyNeighbours  any
	AlertLabels      []map[string]any
	ApiFailures      any
	JobInformation   []map[string]any
	JobEvents        []map[string]any
	JobPodEvents     []map[string]any
	PodEvents        []any
	NodeEvents       []any
	RelatedEvents    []map[string]any
	MarkdownData     []any
	ContainerMetrics any
}
