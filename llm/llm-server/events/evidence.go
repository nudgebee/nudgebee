package events

import (
	"encoding/json"
	"strings"
	"time"
)

type Evidences struct {
	Data []any
}

type Evidence struct {
	Data           any            `json:"data,omitempty"`
	Type           string         `json:"type,omitempty"`
	Insights       []Insight      `json:"insight,omitempty"`
	Filename       string         `json:"filename,omitempty"`
	StartAt        string         `json:"start_at,omitempty"`
	AdditionalInfo map[string]any `json:"additional_info,omitempty"`
	LLMResponse    map[string]any `json:"llm_response,omitempty"`
}

type Insight struct {
	Message  string `json:"message,omitempty"`
	Severity string `json:"severity,omitempty"`
}

func (i *Insight) UnmarshalJSON(data []byte) error {
	// Try to unmarshal as an array of strings
	var ss []string
	if err := json.Unmarshal(data, &ss); err == nil {
		i.Message = strings.Join(ss, "\n")
		i.Severity = ""
		return nil
	}

	// try to unmarshal as a string
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		i.Message = s
		i.Severity = ""
		return nil
	}

	// If it's not a string or array of strings, try to unmarshal as an object.
	// Use an alias to avoid recursion.
	type Alias Insight
	var alias Alias
	if err := json.Unmarshal(data, &alias); err != nil {
		return err
	}
	*i = Insight(alias)
	return nil
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
	Data     PodData `json:"data,omitempty"`
	Type     string  `json:"type,omitempty"`
	Insights []any   `json:"insight,omitempty"`
}

type PodData struct {
	Name            string `json:"name,omitempty"`
	SourcePod       string `json:"source_pod,omitempty"`
	SourceNamespace string `json:"source_namespace,omitempty"`
	Path            string `json:"path,omitempty"`
	Status          string `json:"status,omitempty"`
}

type LogDetails struct {
	Data     string `json:"data,omitempty"`
	Type     string `json:"type,omitempty"`
	Insights []any  `json:"insight,omitempty"`
	Filename string `json:"filename,omitempty"`
}

type FailureData struct {
	Name            string `json:"name,omitempty"`
	SourcePod       string `json:"source_pod,omitempty"`
	SourceNamespace string `json:"source_namespace,omitempty"`
	Path            string `json:"path,omitempty"`
	Status          string `json:"status,omitempty"`
}
type ApiFailures struct {
	Data    FailureData `json:"data,omitempty"`
	Type    string      `json:"type,omitempty"`
	Insight []any       `json:"insight,omitempty"`
}

type Event struct {
	Id               string          `json:"id,omitempty"`
	Evidences        InvestigateData `json:"evidences,omitempty"`
	CreatedAt        string          `json:"created_at,omitempty"`
	UpdatedAt        *time.Time      `json:"updated_at,omitempty"`
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
	SubjectOwner     string          `json:"subject_owner,omitempty"`
	EndsAt           *time.Time      `json:"ends_at,omitempty"`
	StartsAt         *time.Time      `json:"starts_at,omitempty"`
	Fingerprint      string          `json:"fingerprint,omitempty"`
	CloudAccountId   string          `json:"cloud_account_id,omitempty"`
	Status           string          `json:"status,omitempty"`
	Labels           any             `json:"labels,omitempty"`
	NbStatus         string          `json:"nb_status,omitempty"`
	ComputedPriority string          `json:"computed_priority,omitempty"`
	ComputedScore    any             `json:"computed_score,omitempty"`
	ScoreFactors     any             `json:"score_factors,omitempty"`
	ScoreConfidence  any             `json:"score_confidence,omitempty"`
}

type InvestigateDataInsight struct {
	Data           any            `json:"data,omitempty"`
	Insight        []Insight      `json:"insight,omitempty"`
	Type           string         `json:"type,omitempty"`
	AdditionalInfo map[string]any `json:"additional_info,omitempty"`
	Title          string         `json:"title,omitempty"`
}

type InvestigateData struct {
	LogData string `json:"log_data,omitempty"`
	// ErrorLogData is []string on the wire in the happy path, but upstream
	// producers occasionally send heterogeneous shapes (objects, mixed arrays
	// with trace spans embedded). See UnmarshalJSON below for the tolerance rules.
	ErrorLogData     []string                 `json:"error_log_data,omitempty"`
	PodMetrics       []InvestigateDataInsight `json:"pod_metrics"`
	NodeMetrics      []InvestigateDataInsight `json:"node_metrics"`
	PodData          InvestigateDataInsight   `json:"pod_data"`
	NodeData         InvestigateDataInsight   `json:"node_data"`
	Deployment       InvestigateDataInsight   `json:"deployment"`
	NoisyNeighbours  []InvestigateDataInsight `json:"noisy_neighbours"`
	AlertLabels      InvestigateDataInsight   `json:"alert_labels"`
	ApiFailures      []InvestigateDataInsight `json:"api_failures"`
	JobInformation   InvestigateDataInsight   `json:"job_information"`
	JobEvents        InvestigateDataInsight   `json:"job_events"`
	JobPodEvents     InvestigateDataInsight   `json:"job_pod_events"`
	PodEvents        []InvestigateDataInsight `json:"pod_events"`
	NodeEvents       []InvestigateDataInsight `json:"node_events"`
	RelatedEvents    InvestigateDataInsight   `json:"related_events"`
	Markdowns        []InvestigateDataInsight `json:"markdown_data"`
	ContainerMetrics InvestigateDataInsight   `json:"container_metrics"`
	Traces           InvestigateDataInsight   `json:"traces"`
	UserActions      []InvestigateDataInsight `json:"user_actions"`
	RDBMSQueryData   []InvestigateDataInsight `json:"rdbms_query_data"`
	AlertData        InvestigateDataInsight   `json:"alert_data"`
	MetricsData      []InvestigateDataInsight `json:"metrics_queries_data"`
	ServiceMap       InvestigateDataInsight   `json:"service_map_data"`
	Others           []InvestigateDataInsight `json:"others"`
}

// UnmarshalJSON on InvestigateData exists to tolerate upstream producers
// that occasionally send error_log_data in shapes other than []string.
// Every other field flows through the default decoder; only error_log_data
// gets the fallback ladder implemented by normalizeErrorLogLines.
func (i *InvestigateData) UnmarshalJSON(data []byte) error {
	type alias InvestigateData
	aux := struct {
		ErrorLogData json.RawMessage `json:"error_log_data,omitempty"`
		*alias
	}{alias: (*alias)(i)}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	i.ErrorLogData = normalizeErrorLogLines(aux.ErrorLogData)
	return nil
}

// normalizeErrorLogLines coerces the raw JSON of error_log_data into []string,
// tolerating: []string (happy path), mixed []any (stringify non-strings via
// JSON), single string (wrap), and any other valid JSON (encode as one entry).
// Empty / null / absent input returns nil.
func normalizeErrorLogLines(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return nil
	}
	var lines []string
	if err := json.Unmarshal(raw, &lines); err == nil {
		return lines
	}
	var mixed []any
	if err := json.Unmarshal(raw, &mixed); err == nil {
		// Elements came from json.Unmarshal into any (map/slice/scalar) — every
		// element is trivially marshalable, so json.Marshal below cannot fail.
		out := make([]string, len(mixed))
		for idx, e := range mixed {
			if s, ok := e.(string); ok {
				out[idx] = s
				continue
			}
			if b, mErr := json.Marshal(e); mErr == nil {
				out[idx] = string(b)
			}
		}
		return out
	}
	var single string
	if err := json.Unmarshal(raw, &single); err == nil {
		return []string{single}
	}
	return []string{trimmed}
}
