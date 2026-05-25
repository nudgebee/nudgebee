package models

import (
	"time"
)

type ConfigValue struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type TicketConfigurations struct {
	ID            string        `json:"id,omitempty" db:"id"`
	URL           string        `json:"url,omitempty" db:"url"`
	AuthType      string        `json:"auth_type,omitempty" db:"auth_type"`
	Tool          string        `json:"tool,omitempty" db:"tool"`
	Name          string        `json:"name,omitempty" db:"name"`
	Username      string        `json:"username,omitempty" db:"username"`
	Password      string        `json:"password,omitempty" db:"password"`
	CreatedBy     *string       `json:"created_by,omitempty" db:"created_by"`
	UpdatedBy     *string       `json:"updated_by,omitempty" db:"updated_by"`
	Tenant        string        `json:"tenant,omitempty" db:"tenant"`
	Status        string        `json:"status,omitempty" db:"status"`
	LastConnected *time.Time    `json:"last_connected,omitempty" db:"last_connected"`
	Projects      []Project     `json:"projects,omitempty" db:"-"`
	Priorities    []Priority    `json:"priorities,omitempty" db:"-"`
	Users         interface{}   `json:"users,omitempty" db:"-"`
	IsActive      bool          `json:"is_active,omitempty" db:"-"`
	ConfigValues  []ConfigValue `json:"config_values,omitempty" db:"-"`
}

type Project struct {
	Name string `json:"name"`
	Key  string `json:"key"`
}

type Priority struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type User struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

type ConfigurationInsertResponse struct {
	Data struct {
		InsertTicketsOne struct {
			ID string `json:"id"`
		} `json:"insert_jira_configurations_one"`
	} `json:"data"`
}

// TicketIntegrationCreateResponse is the simplified response for the new mutation
type TicketIntegrationCreateResponse struct {
	ID string `json:"id"`
}

type ConfigurationUpdateResponse struct {
	Data struct {
		UpdateJiraConfigurationsByPk struct {
			ID string `json:"id"`
		} `json:"update_jira_configurations_by_pk"`
	} `json:"data"`
}

type ConfigurationRequest struct {
	Action           Action             `json:"action"`
	Input            ConfigurationInput `json:"input"`
	RequestQuery     string             `json:"request_query"`
	SessionVariables SessionVariables   `json:"session_variables"`
}

type ConfigurationInput struct {
	Object TicketConfigurations `json:"object"`
}

type IssueTemplateMetaRequest struct {
	Action           Action               `json:"action"`
	Input            TemplateRequestInput `json:"input"`
	RequestQuery     string               `json:"request_query"`
	SessionVariables SessionVariables     `json:"session_variables"`
}

type TemplateRequestInput struct {
	IntegrationId string `json:"integration_id"`
	ProjectKey    string `json:"project_key"`
}

type FieldValuesRequest struct {
	Action           Action                  `json:"action"`
	Input            FieldValuesRequestInput `json:"input"`
	RequestQuery     string                  `json:"request_query"`
	SessionVariables SessionVariables        `json:"session_variables"`
}

type FieldValuesRequestInput struct {
	IntegrationId string `json:"integration_id"`
	URL           string `json:"url"`
	KEY           string `json:"key"`
	SearchTerm    string `json:"search_term"`
}

type FieldValue struct {
	ID    string `json:"id,omitempty"`
	Name  string `json:"name,omitempty"`
	Value string `json:"value,omitempty"`
}

type FieldValueResponse struct {
	Values []FieldValue `json:"values"`
}

// TestConnectionRequest is the Hasura action request for testing a ticketing integration connection
type TestConnectionRequest struct {
	Action           Action              `json:"action"`
	Input            TestConnectionInput `json:"input"`
	RequestQuery     string              `json:"request_query"`
	SessionVariables SessionVariables    `json:"session_variables"`
}

type TestConnectionInput struct {
	IntegrationID string `json:"integration_id"`
}

// TestConnectionByConfigRequest is the Hasura action request for testing
// ticketing credentials before they have been persisted. Shares the same
// configuration shape as AddTicketConfiguration so the Add/Edit modal can
// send identical payloads to both endpoints.
type TestConnectionByConfigRequest struct {
	Action           Action             `json:"action"`
	Input            ConfigurationInput `json:"input"`
	RequestQuery     string             `json:"request_query"`
	SessionVariables SessionVariables   `json:"session_variables"`
}

// TestConnectionResponse is the response for the test connection action
type TestConnectionResponse struct {
	Success       bool   `json:"success"`
	Message       string `json:"message"`
	Tool          string `json:"tool"`
	ProjectsCount int    `json:"projects_count"`
	Error         string `json:"error,omitempty"`
}

type SuggestionsResponse struct {
	Suggestions []struct {
		Label string `json:"label"`
		HTML  string `json:"html"`
	} `json:"suggestions"`
}
