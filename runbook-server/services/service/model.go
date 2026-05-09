package service

import (
	"bytes"
	"fmt"
	"nudgebee/runbook/common"
	"strconv"
	"strings"
	"time"
)

type ServicesQueryRequest struct {
	Action           Action           `json:"action"`
	Input            QueryInput       `json:"input"`
	SessionVariables SessionVariables `json:"session_variables"`
}

type Action struct {
	Name string `json:"name"`
}

type QueryInput struct {
	Where QueryWhere `json:"where"`
}

type QueryWhere struct {
	AccountID         QueryCondition        `json:"account_id"`
	SpanName          QueryCondition        `json:"span_name"`
	Timestamp         QueryBetweenCondition `json:"timestamp"`
	WorkloadNamespace QueryInCondition      `json:"workload_namespace"`
	WorkloadName      QueryInCondition      `json:"workload_name"`
	StatusCode        QueryCondition        `json:"status_code"`
}

type QueryCondition struct {
	Eq  string `json:"_eq,omitempty"`
	Neq string `json:"_neq,omitempty"`
}

type QueryBetween struct {
	Gte string `json:"_gte"`
	Lte string `json:"_lte"`
}
type QueryBetweenCondition struct {
	Between QueryBetween `json:"_between"`
}

type QueryInCondition struct {
	In []string `json:"_in"`
}

type SessionVariables struct {
	UserID       string `json:"x-hasura-user-id"`
	UserTenantID string `json:"x-hasura-user-tenant-id"`
}

type ScanImageRequest struct {
	AccountId string `json:"account_id" mapstructure:"account_id" validate:"required"`
	Namespace string `json:"namespace" mapstructure:"namespace" validate:"required"`
	Workload  string `json:"workload" mapstructure:"workload" validate:"required"`
}

type ScanImageServiceRequest struct {
	Action           Action           `json:"action"`
	Input            ScanImageRequest `json:"input"`
	SessionVariables SessionVariables `json:"session_variables"`
}

type ScanCisRequest struct {
	AccountId string `json:"account_id" mapstructure:"account_id" validate:"required"`
	JobName   string `json:"job_name" mapstructure:"job_name" validate:"required"`
}

type ScanCisServiceRequest struct {
	Action           Action           `json:"action"`
	Input            ScanCisRequest   `json:"input"`
	SessionVariables SessionVariables `json:"session_variables"`
}

type SortOrder string

const (
	SortOrderAsc  SortOrder = "asc"
	SortOrderDesc SortOrder = "desc"
)

type ObservabilitySortField struct {
	ColumnName string    `json:"column_name"`
	Order      SortOrder `json:"order"`
}

type ObservabilityLogQueryRequest struct {
	AccountId         string                   `json:"account_id"`
	LogProvider       string                   `json:"log_provider"`
	LogProviderSource string                   `json:"log_provider_source"`
	Query             string                   `json:"query"`
	StartTime         int64                    `json:"start_time"`
	EndTime           int64                    `json:"end_time"`
	Limit             int                      `json:"limit"`
	Offset            int                      `json:"offset"`
	SortFields        []ObservabilitySortField `json:"sort_fields"`
	StepInterval      int                      `json:"step_interval"`
	Request           map[string]any           `json:"request"`
}

type RecommendationApplyResponse struct {
	Data []any `json:"data" mapstructure:"data"`
}

type ScanCisServiceResponse struct {
	Data []any `json:"data" mapstructure:"data"`
}

// ServiceID represents the identifier for a service.
type ServiceID struct {
	Namespace *string `json:"namespace,omitempty"`
	Kind      *string `json:"kind,omitempty"`
	Name      *string `json:"name,omitempty"`
}

// stringToPtr converts a string to a *string.
// It returns nil if the input string is empty or "None" (case-sensitive).
func stringToPtr(s string) *string {
	if s == "" || s == "None" {
		return nil
	}
	val := s // Create a new variable to take its address
	return &val
}

// UnmarshalJSON custom unmarshaler for ServiceID
func (id *ServiceID) UnmarshalJSON(data []byte) error {
	trimmedData := bytes.TrimSpace(data)

	if string(trimmedData) == "null" {
		id.Namespace, id.Kind, id.Name = nil, nil, nil
		return nil
	}

	// Try to unmarshal as a JSON object
	if bytes.HasPrefix(trimmedData, []byte("{")) && bytes.HasSuffix(trimmedData, []byte("}")) {
		var temp struct {
			Namespace *string `json:"namespace"`
			Kind      *string `json:"kind"`
			Name      *string `json:"name"`
		}
		if err := common.UnmarshalJson(trimmedData, &temp); err == nil {
			id.Namespace, id.Kind, id.Name = temp.Namespace, temp.Kind, temp.Name
			return nil
		}
		// If it looks like an object but fails to unmarshal, return that error
		return fmt.Errorf("failed to unmarshal ServiceID as JSON object: %w. Data: %s", common.UnmarshalJson(trimmedData, &temp), string(trimmedData))
	}

	// Try to unmarshal as a JSON string
	if bytes.HasPrefix(trimmedData, []byte("\"")) && bytes.HasSuffix(trimmedData, []byte("\"")) {
		var strValue string
		if err := common.UnmarshalJson(trimmedData, &strValue); err != nil {
			return fmt.Errorf("failed to unmarshal ServiceID as JSON string: %w. Data: %s", err, string(trimmedData))
		}

		parts := strings.Split(strValue, ":")
		if len(parts) == 3 {
			id.Namespace = stringToPtr(parts[0])
			id.Kind = stringToPtr(parts[1])
			id.Name = stringToPtr(parts[2])
			return nil
		}
		return fmt.Errorf("ServiceID string format error: expected 3 parts separated by colons for string '%s', got %d parts. Data: %s", strValue, len(parts), string(trimmedData))
	}

	return fmt.Errorf("invalid ServiceID format: data is not a JSON object, JSON string, or JSON null. Data: %s", string(trimmedData))
}

// MarshalJSON custom marshaller for ServiceID.
// It will always marshal to the object form for consistency.
func (id ServiceID) MarshalJSON() ([]byte, error) {
	aux := struct {
		Namespace *string `json:"namespace,omitempty"`
		Kind      *string `json:"kind,omitempty"`
		Name      *string `json:"name,omitempty"`
	}{
		Namespace: id.Namespace,
		Kind:      id.Kind,
		Name:      id.Name,
	}
	return common.MarshalJson(aux)
}

// ServiceCategory represents the category of a service.
type ServiceCategory struct {
	Category string `json:"category,omitempty"`
}

// StringOrFloat is a custom type to handle fields that can be either a string or an int in JSON.
type StringOrFloat float64

// UnmarshalJSON custom unmarshaler for StringOrFloat
func (s *StringOrFloat) UnmarshalJSON(data []byte) error {
	trimmedData := bytes.TrimSpace(data)

	if string(trimmedData) == "null" {
		*s = 0
		return nil
	}

	// Try to unmarshal as an int
	var floatVal float64
	if err := common.UnmarshalJson(trimmedData, &floatVal); err == nil {
		*s = StringOrFloat(floatVal)
		return nil
	}

	// Try to unmarshal as a string
	var strVal string
	if err := common.UnmarshalJson(trimmedData, &strVal); err == nil {
		if strVal == "" {
			*s = 0
			return nil
		}
		parsedFloat, err := strconv.ParseFloat(strVal, 64)
		if err != nil {
			return fmt.Errorf("failed to parse string '%s' as int for StringOrFloat: %w", strVal, err)
		}
		*s = StringOrFloat(parsedFloat)
		return nil
	}

	return fmt.Errorf("failed to unmarshal StringOrFloat: data is not a valid float64, string, or null. Data: %s", string(trimmedData))
}

// MarshalJSON custom marshaller for StringOrFloat.
func (s StringOrFloat) MarshalJSON() ([]byte, error) {
	return common.MarshalJson(float64(s))
}

// ServiceLabels represents the labels associated with a service.
type ServiceLabels map[string]string

// StreamDependency represents an upstream dependency of a service.
type StreamDependency struct {
	ID            ServiceID     `json:"Id,omitempty"`
	Status        int           `json:"Status,omitempty"`
	Stats         []string      `json:"Stats,omitempty"`
	Weight        StringOrFloat `json:"Weight,omitempty"`
	Latency       StringOrFloat `json:"Latency,omitempty"`
	RequestCount  StringOrFloat `json:"RequestCount,omitempty"`
	FailureCount  StringOrFloat `json:"FailureCount,omitempty"`
	Protocol      string        `json:"Protocol,omitempty"`
	BytesSent     StringOrFloat `json:"BytesSent,omitempty"`
	BytesReceived StringOrFloat `json:"BytesReceived,omitempty"`
}

// ServiceInstance represents an instance of a service.
type ServiceInstance struct {
	ID       ServiceID `json:"id,omitempty"`
	IsFailed bool      `json:"is_failed,omitempty"`
}

type ServiceDependencySourceCode struct {
	CodeRepo string `json:"CodeRepo,omitempty"`
	CiCdRepo string `json:"CiCdRepo,omitempty"`
}

// ServiceDependency is the main struct representing the service and its dependencies.
type ServiceDependency struct {
	ID                ServiceID                   `json:"Id,omitempty"`
	Category          ServiceCategory             `json:"Category,omitempty"`
	Labels            ServiceLabels               `json:"Labels,omitempty"`
	Status            any                         `json:"Status,omitempty"`     // Using 'any' as it's null in the example, can be *string or a specific type if known
	Indicators        []any                       `json:"Indicators,omitempty"` // Using 'any' as the type of elements is unknown
	Upstreams         []StreamDependency          `json:"Upstreams,omitempty"`
	Downstreams       []StreamDependency          `json:"Downstreams,omitempty"`
	Instances         []ServiceInstance           `json:"Instances,omitempty"`
	Type              []string                    `json:"Type,omitempty"`
	DesiredInstances  StringOrFloat               `json:"DesiredInstances,omitempty"`
	FailedInstances   StringOrFloat               `json:"FailedInstances,omitempty"`
	OOMKills          StringOrFloat               `json:"OOMKills,omitempty"`
	Restarts          StringOrFloat               `json:"Restarts,omitempty"`
	CPUThrottlingTime StringOrFloat               `json:"CPUThrottlingTime,omitempty"`
	VolumeSize        StringOrFloat               `json:"VolumeSize,omitempty"`
	VolumeUsed        StringOrFloat               `json:"VolumeUsed,omitempty"`
	IsHealthy         bool                        `json:"IsHealthy,omitempty"`
	HealthReason      string                      `json:"HealthReason,omitempty"`
	SourceCode        ServiceDependencySourceCode `json:"SourceCode,omitempty"`
}

type GetServiceDependencyGraphResponse struct {
	Dependency          []ServiceDependency `json:"dependency,omitempty"`
	DependencyStartTime time.Time           `json:"dependency_start_time,omitempty"`
	DependencyEndTime   time.Time           `json:"dependency_end_time,omitempty"`
}

type IntegrationConfigValue struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type Request struct {
	IntegrationName         string                   `json:"integration_name"`
	AccountIDs              []string                 `json:"account_ids"`
	IntegrationConfigName   string                   `json:"integration_config_name"`
	IntegrationConfigValues []IntegrationConfigValue `json:"integration_config_values"`
}

type Input1 struct {
	Request Request `json:"request"`
}

type IntegrationCreateConfig struct {
	Action Action `json:"action"`
	Input  Input1 `json:"input"`
}

type GitPullRequest struct {
	AccountID    string `json:"account_id"`
	Namespace    string `json:"namespace"`
	WorkloadName string `json:"workload_name"`
	Kind         string `json:"kind"`
	Patch        string `json:"patch"`
	Title        string `json:"title"`
	Description  string `json:"description"`
}

type PullRequestServiceRequest struct {
	Action           Action           `json:"action"`
	Input            GitPullRequest   `json:"input"`
	SessionVariables SessionVariables `json:"session_variables"`
}

type ProviderConfig struct {
	Name string `json:"name"`
}

type GitPushRequest struct {
	CreatedBy      string         `json:"created_by"`
	TenantID       string         `json:"tenant_id"`
	ChangeType     string         `json:"change_type"`
	AccountID      string         `json:"account_id"`
	ResolverType   string         `json:"resolver_type"`
	ResourceID     string         `json:"resource_id,omitempty"`
	ResolverID     string         `json:"resolver_id,omitempty"`
	Data           map[string]any `json:"data"`
	Provider       string         `json:"provider"`
	ProviderConfig ProviderConfig `json:"provider_config"`
	ReferenceLink  string         `json:"reference_link,omitempty"`
}

type PushRequestServiceRequest struct {
	Action Action         `json:"action"`
	Input  GitPushRequest `json:"input"`
}

type PushRequestResponse struct {
	Status     string `json:"status"`
	Resolution struct {
		ID string `json:"id"`
	} `json:"resolution"`
}

type RecommendationResolutionRequest struct {
	AccountID        string         `json:"account_id"`
	RecommendationID string         `json:"recommendation_id"`
	Data             map[string]any `json:"data"`
	Provider         string         `json:"provider"`
	ProviderConfig   ProviderConfig `json:"provider_config"`
	ResolverType     string         `json:"resolver_type"`
	ResolverID       string         `json:"resolver_id"`
}
