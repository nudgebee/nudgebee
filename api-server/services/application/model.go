package application

import (
	"nudgebee/services/relay"
	"nudgebee/services/traces"
	"time"

	"github.com/google/uuid"
)

type ApplicationDeploymentInsight struct {
	AccountId              string                         `json:"account_id" validate:"required" mapstructure:"account_id"`
	Name                   string                         `json:"name" validate:"required" mapstructure:"name"`
	Namespace              string                         `json:"namespace" validate:"required" mapstructure:"namespace"`
	LastDeploymentDateTime string                         `json:"last_deployment_date_time" mapstructure:"last_deployment_date_time"`
	PreviousStats          relay.ApplicationStatsResponse `json:"previous_stats" mapstructure:"previous_stats"`
	CurrentStats           relay.ApplicationStatsResponse `json:"current_stats" mapstructure:"current_stats"`
}

type ApplicationRequest struct {
	Name      string `json:"name" validate:"required" mapstructure:"name"`
	Namespace string `json:"namespace" validate:"required" mapstructure:"namespace"`
	Kind      string `json:"kind" validate:"required" mapstructure:"kind"`
}

type ApplicationMetricsRequest struct {
	AccountId    string               `json:"account_id" validate:"required" mapstructure:"account_id"`
	Applications []ApplicationRequest `json:"applications" validate:"required" mapstructure:"applications"`
	StartAt      time.Time            `json:"start_at" validate:"optional" mapstructure:"start_at"`
	EndAt        time.Time            `json:"end_at" validate:"optional" mapstructure:"end_at"`
}

type ApplicationStatsResponse struct {
	Name      string                         `json:"name" validate:"required" mapstructure:"name"`
	Namespace string                         `json:"namespace" validate:"required" mapstructure:"namespace"`
	Metrics   relay.ApplicationStatsResponse `json:"metrics" mapstructure:"metrics"`
}

type ApplicationDeploymentCompareRequest struct {
	AccountId    string               `json:"account_id" validate:"required" mapstructure:"account_id"`
	Applications []ApplicationRequest `json:"applications" validate:"required" mapstructure:"applications"`
}

type ApplicationCategory string

const (
	ApplicationCategoryDatabase ApplicationCategory = "database"
	ApplicationCategoryCache    ApplicationCategory = "cache"
	ApplicationCategoryQueue    ApplicationCategory = "queue"
)

type Application struct {
	Id            uuid.UUID                 `json:"id"`
	Arn           string                    `json:"arn"`
	Name          string                    `json:"name"`
	K8sNamespace  string                    `json:"k8s_namespace"`
	K8sKind       string                    `json:"k8s_kind"`
	ResourceId    uuid.UUID                 `json:"resource_id"`
	Labels        map[string]string         `json:"labels"`
	CreatedAt     *time.Time                `json:"created_at"`
	LastSeenAt    *time.Time                `json:"last_seen_at"`
	ReadyPods     *int                      `json:"ready_pods"`
	TotalPods     *int                      `json:"total_pods"`
	Attributes    []ApplicationAttributes   `json:"attributes"`
	Relationships []ApplicationRelationship `json:"relationships"`
}

type ApplicationAttributes struct {
	Id         uuid.UUID         `json:"id" db:"id"`
	Name       string            `json:"name" db:"name"`
	Value      string            `json:"value" db:"value"`
	Labels     map[string]string `json:"labels" db:"ltrfabels"`
	CreatedAt  time.Time         `json:"created_at" db:"created_at"`
	LastSeenAt time.Time         `json:"last_seen_at" db:"last_seen_at"`
}

type ApplicationRelationshipType string

const (
	ApplicationRelationshipTypeConfig     ApplicationRelationshipType = "config"
	ApplicationRelationshipTypeSecret     ApplicationRelationshipType = "secret"
	ApplicationRelationshipTypeChild      ApplicationRelationshipType = "child"
	ApplicationRelationshipTypeParent     ApplicationRelationshipType = "parent"
	ApplicationRelationshipTypeNetworkSrc ApplicationRelationshipType = "network_src"
	ApplicationRelationshipTypeNetworkDst ApplicationRelationshipType = "network_dst"
)

type ApplicationRelationship struct {
	Id            uuid.UUID                   `json:"id" db:"id"`
	Relationship  ApplicationRelationshipType `json:"relationship" db:"relationship"`
	SourceId      uuid.UUID                   `json:"source_id" db:"source_id"`
	DestinationId uuid.UUID                   `json:"destination_id" db:"destiation_id"`
	Labels        map[string]string           `json:"labels" db:"labels"`
	CreatedAt     time.Time                   `json:"created_at" db:"created_at"`
	LastSeenAt    time.Time                   `json:"last_seen_at" db:"last_seen_at"`
}

type ApplicationProfileConvertRequest struct {
	AccountId      string `json:"account_id" validate:"required" mapstructure:"account_id"`
	Profile        string `json:"base64_profile" validate:"required" mapstructure:"base64_profile"`
	ResponseFormat string `json:"response_format" validate:"required" mapstructure:"response_format"`
}
type ApplicationProfileConvertResponse struct {
	SvgProfile string `json:"svg_profile" validate:"required" mapstructure:"svg_profile"`
}

type ApplicationProfileRequest struct {
	AccountId           string `json:"account_id" validate:"required" mapstructure:"account_id"`
	Namespace           string `json:"namespace" validate:"required" mapstructure:"namespace"`
	PodName             string `json:"pod_name" validate:"required" mapstructure:"pod_name"`
	ProfileType         string `json:"profile_type" validate:"optional" mapstructure:"profile_type"`
	ApplicationLanguage string `json:"application_language" validate:"optional" mapstructure:"application_language"`
	ProfileDuration     int    `json:"profile_duration" validate:"optional" mapstructure:"profile_duration"`
	OutputType          string `json:"output_type" validate:"optional" mapstructure:"output_type"`
	ProfileTool         string `json:"profile_tool" validate:"optional" mapstructure:"profile_tool"`
}

type ApplicationProfileResponse struct {
	ProfileTaskId string                 `json:"profile_task_id" validate:"required" mapstructure:"profile_task_id"`
	AccountId     string                 `json:"account_id" validate:"required" mapstructure:"account_id"`
	Status        string                 `json:"status" validate:"required" mapstructure:"status"`
	Profile       map[string]interface{} `json:"base64_profile" validate:"optional" mapstructure:"base64_profile"`
	ErrorMessage  string                 `json:"message" validate:"optional" mapstructure:"error_message"`
}

type GetApplicationProfileRequest struct {
	AccountId     string `json:"account_id" validate:"required" mapstructure:"account_id"`
	ProfileTaskId string `json:"profile_id" validate:"required" mapstructure:"profile_id"`
}

type ProfilingTool string

const (
	AsyncProfiler ProfilingTool = "async-profiler"
	Jcmd          ProfilingTool = "jcmd"
	Pyspy         ProfilingTool = "pyspy"
	Bpf           ProfilingTool = "bpf"
	Perf          ProfilingTool = "perf"
	Rbspy         ProfilingTool = "rbspy"
	FakeTool      ProfilingTool = "fake"
	Austin        ProfilingTool = "austin"
	Pprof         ProfilingTool = "pprof"
)

func (t ProfilingTool) IsValid() bool {
	switch t {
	case AsyncProfiler, Jcmd, Pyspy, Bpf, Perf, Rbspy, FakeTool, Austin, Pprof:
		return true
	default:
		return false
	}
}

// ProfileType represents the type of profiling to perform.
type ProfileType string

const (
	Memory ProfileType = "memory"
	CPU    ProfileType = "cpu"
)

// ProgrammingLanguage represents supported programming languages.
type ProgrammingLanguage string

const (
	Java          ProgrammingLanguage = "java"
	Python        ProgrammingLanguage = "python"
	GoLang        ProgrammingLanguage = "go"
	Node          ProgrammingLanguage = "node"
	Rust          ProgrammingLanguage = "rust"
	Clang         ProgrammingLanguage = "clang"
	ClangPlusPlus ProgrammingLanguage = "c++"
	Ruby          ProgrammingLanguage = "ruby"
	Unknown       ProgrammingLanguage = "unknown"
)

type TraceServiceMapRequest struct {
	AccountID         string               `json:"account_id" validate:"required"`
	WorkloadName      string               `json:"workload_name,omitempty"`
	WorkloadNamespace string               `json:"workload_namespace,omitempty"`
	StartTime         time.Time            `json:"start_time,omitempty"`
	EndTime           time.Time            `json:"end_time,omitempty"`
	LabelFilter       []traces.LabelFilter `json:"label_filter,omitempty"`
}
