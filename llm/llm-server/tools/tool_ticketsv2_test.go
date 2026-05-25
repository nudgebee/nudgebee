package tools

import (
	"fmt"
	"nudgebee/llm/common"
	"strings"
	"testing"

	"nudgebee/llm/tools/core"

	"github.com/stretchr/testify/assert"
)

// --- TicketV2OperationRequest.Validate tests ---

func TestTicketV2OperationRequest_Validate(t *testing.T) {
	tests := []struct {
		name    string
		req     TicketV2OperationRequest
		wantErr bool
		errMsg  string
	}{
		{
			name:    "get_create_meta valid",
			req:     TicketV2OperationRequest{OperationType: "get_create_meta"},
			wantErr: false,
		},
		{
			name:    "get_create_meta with project_key",
			req:     TicketV2OperationRequest{OperationType: "get_create_meta", ProjectKey: "PROJ"},
			wantErr: false,
		},
		{
			name:    "create_ticket valid with title",
			req:     TicketV2OperationRequest{OperationType: "create_ticket", Title: "Bug: OOM in prod"},
			wantErr: false,
		},
		{
			name:    "create_ticket missing title",
			req:     TicketV2OperationRequest{OperationType: "create_ticket"},
			wantErr: true,
			errMsg:  "title is required",
		},
		{
			name:    "add_comment valid",
			req:     TicketV2OperationRequest{OperationType: "add_comment", TicketID: "PROJ-123", CommentText: "Fixed it"},
			wantErr: false,
		},
		{
			name:    "add_comment missing ticket_id",
			req:     TicketV2OperationRequest{OperationType: "add_comment", CommentText: "Fixed it"},
			wantErr: true,
			errMsg:  "ticket_id and comment_text are required",
		},
		{
			name:    "add_comment missing comment_text",
			req:     TicketV2OperationRequest{OperationType: "add_comment", TicketID: "PROJ-123"},
			wantErr: true,
			errMsg:  "ticket_id and comment_text are required",
		},
		{
			name:    "get_comments valid",
			req:     TicketV2OperationRequest{OperationType: "get_comments", TicketID: "PROJ-123"},
			wantErr: false,
		},
		{
			name:    "get_comments missing ticket_id",
			req:     TicketV2OperationRequest{OperationType: "get_comments"},
			wantErr: true,
			errMsg:  "ticket_id is required",
		},
		{
			name:    "get_ticket valid",
			req:     TicketV2OperationRequest{OperationType: "get_ticket", TicketID: "PROJ-456"},
			wantErr: false,
		},
		{
			name:    "get_ticket missing ticket_id",
			req:     TicketV2OperationRequest{OperationType: "get_ticket"},
			wantErr: true,
			errMsg:  "ticket_id is required",
		},
		{
			name:    "list_tickets valid with project_key",
			req:     TicketV2OperationRequest{OperationType: "list_tickets", ProjectKey: "PROJ"},
			wantErr: false,
		},
		{
			name:    "list_tickets valid without project_key (can come from config)",
			req:     TicketV2OperationRequest{OperationType: "list_tickets"},
			wantErr: false,
		},
		{
			name:    "list_tickets with all filters",
			req:     TicketV2OperationRequest{OperationType: "list_tickets", ProjectKey: "PROJ", Status: "open", Priority: "High", Assignee: "alice", Limit: 10, Offset: 20},
			wantErr: false,
		},
		{
			name:    "invalid operation_type",
			req:     TicketV2OperationRequest{OperationType: "delete_ticket"},
			wantErr: true,
			errMsg:  "invalid operation_type",
		},
		{
			name:    "empty operation_type",
			req:     TicketV2OperationRequest{},
			wantErr: true,
			errMsg:  "invalid operation_type",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.req.Validate()
			if tt.wantErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// --- IdentifyConfig tests ---

func TestTicketMasterV2_IdentifyConfig(t *testing.T) {
	tool := TicketMasterV2{}

	jiraConfig := core.ToolConfig{
		Name: "My Jira",
		Values: []core.ToolConfigValue{
			{Name: "id", Value: "intg-jira-001"},
			{Name: "type", Value: "jira"},
		},
	}
	githubConfig := core.ToolConfig{
		Name: "My GitHub",
		Values: []core.ToolConfigValue{
			{Name: "id", Value: "intg-github-001"},
			{Name: "type", Value: "github"},
		},
	}
	gitlabConfig := core.ToolConfig{
		Name: "My GitLab",
		Values: []core.ToolConfigValue{
			{Name: "id", Value: "intg-gitlab-001"},
			{Name: "type", Value: "gitlab"},
		},
	}

	t.Run("single config auto-selects", func(t *testing.T) {
		ctx := core.NbToolContext{Query: "create a ticket"}
		input := core.NBToolCallRequest{Command: `{"operation_type": "get_create_meta"}`}
		result, err := tool.IdentifyConfig(ctx, input, []core.ToolConfig{jiraConfig})
		assert.NoError(t, err)
		assert.Equal(t, "My Jira", result.Name)
	})

	t.Run("match by integration_id", func(t *testing.T) {
		ctx := core.NbToolContext{Query: "create a ticket"}
		input := core.NBToolCallRequest{Command: `{"operation_type": "create_ticket", "integration_id": "intg-github-001", "title": "Test"}`}
		configs := []core.ToolConfig{jiraConfig, githubConfig, gitlabConfig}
		result, err := tool.IdentifyConfig(ctx, input, configs)
		assert.NoError(t, err)
		assert.Equal(t, "My GitHub", result.Name)
	})

	t.Run("match by platform type", func(t *testing.T) {
		ctx := core.NbToolContext{Query: "create a ticket"}
		input := core.NBToolCallRequest{Command: `{"operation_type": "create_ticket", "platform": "gitlab", "title": "Test"}`}
		configs := []core.ToolConfig{jiraConfig, githubConfig, gitlabConfig}
		result, err := tool.IdentifyConfig(ctx, input, configs)
		assert.NoError(t, err)
		assert.Equal(t, "My GitLab", result.Name)
	})

	t.Run("match by source field (fallback to platform)", func(t *testing.T) {
		ctx := core.NbToolContext{Query: "get comments on ticket"}
		input := core.NBToolCallRequest{Command: `{"operation_type": "get_comments", "ticket_id": "PROJ-1", "source": "jira"}`}
		configs := []core.ToolConfig{jiraConfig, githubConfig}
		result, err := tool.IdentifyConfig(ctx, input, configs)
		assert.NoError(t, err)
		assert.Equal(t, "My Jira", result.Name)
	})

	t.Run("match by config name in user query", func(t *testing.T) {
		ctx := core.NbToolContext{Query: "create a ticket in My GitHub"}
		input := core.NBToolCallRequest{Command: `{"operation_type": "create_ticket", "title": "Test"}`}
		configs := []core.ToolConfig{jiraConfig, githubConfig}
		result, err := tool.IdentifyConfig(ctx, input, configs)
		assert.NoError(t, err)
		assert.Equal(t, "My GitHub", result.Name)
	})

	t.Run("no match returns empty config", func(t *testing.T) {
		ctx := core.NbToolContext{Query: "create a ticket"}
		input := core.NBToolCallRequest{Command: `{"operation_type": "create_ticket", "title": "Test"}`}
		configs := []core.ToolConfig{jiraConfig, githubConfig}
		result, err := tool.IdentifyConfig(ctx, input, configs)
		assert.NoError(t, err)
		assert.Empty(t, result.Name)
	})

	t.Run("invalid json returns empty config", func(t *testing.T) {
		ctx := core.NbToolContext{Query: "test"}
		input := core.NBToolCallRequest{Command: `not json`}
		configs := []core.ToolConfig{jiraConfig}
		result, err := tool.IdentifyConfig(ctx, input, configs)
		assert.NoError(t, err)
		assert.Empty(t, result.Name)
	})

	t.Run("get_create_meta with multiple configs falls through", func(t *testing.T) {
		ctx := core.NbToolContext{Query: "create a ticket"}
		input := core.NBToolCallRequest{Command: `{"operation_type": "get_create_meta"}`}
		configs := []core.ToolConfig{jiraConfig, githubConfig}
		result, err := tool.IdentifyConfig(ctx, input, configs)
		assert.NoError(t, err)
		// Should not auto-select when multiple configs exist and no disambiguation
		assert.Empty(t, result.Name)
	})
}

// --- checkRequiredFields tests ---

func TestCheckRequiredFields(t *testing.T) {
	issueTypes := []ticketServerIssueType{
		{
			Name: "Bug",
			Fields: map[string]ticketServerFieldInfo{
				"summary":     {Key: "summary", Name: "Summary", Type: "string", Required: true},
				"description": {Key: "description", Name: "Description", Type: "string", Required: false},
				"priority":    {Key: "priority", Name: "Priority", Type: "select", Required: true, AllowedValues: []any{map[string]any{"name": "High"}, map[string]any{"name": "Medium"}, map[string]any{"name": "Low"}}},
				"assignee":    {Key: "assignee", Name: "Assignee", Type: "string", Required: true},
			},
		},
		{
			Name: "Task",
			Fields: map[string]ticketServerFieldInfo{
				"summary": {Key: "summary", Name: "Summary", Type: "string", Required: true},
			},
		},
	}

	t.Run("all required fields provided", func(t *testing.T) {
		req := TicketV2OperationRequest{
			Title:    "OOM Bug",
			Severity: "High",
			Assignee: "alice@test.com",
		}
		result := checkRequiredFields(issueTypes, req)
		assert.Empty(t, result)
	})

	t.Run("missing required fields", func(t *testing.T) {
		req := TicketV2OperationRequest{
			Title: "OOM Bug",
			// Missing priority and assignee
		}
		result := checkRequiredFields(issueTypes, req)
		assert.NotEmpty(t, result)
		assert.Contains(t, result, "missing")
		assert.Contains(t, result, "assignee")
		assert.Contains(t, result, "priority")
	})

	t.Run("fields provided via additional_fields", func(t *testing.T) {
		req := TicketV2OperationRequest{
			Title:    "OOM Bug",
			Severity: "High",
			AdditionalFields: map[string]any{
				"assignee": "bob@test.com",
			},
		}
		result := checkRequiredFields(issueTypes, req)
		assert.Empty(t, result)
	})

	t.Run("matching issue type by name", func(t *testing.T) {
		req := TicketV2OperationRequest{
			Title:      "Simple task",
			TicketType: "Task",
		}
		// Task type only requires summary
		result := checkRequiredFields(issueTypes, req)
		assert.Empty(t, result)
	})

	t.Run("multiple issue types listed when missing fields", func(t *testing.T) {
		req := TicketV2OperationRequest{
			// Missing title/summary
		}
		result := checkRequiredFields(issueTypes, req)
		assert.NotEmpty(t, result)
		assert.Contains(t, result, "Available issue types")
		assert.Contains(t, result, "Bug")
		assert.Contains(t, result, "Task")
	})
}

// --- formatAdditionalFields tests ---

func TestFormatAdditionalFields(t *testing.T) {
	fieldTypes := map[string]string{
		"customfield_team":     "select",
		"customfield_tags":     "multicheckboxes",
		"customfield_priority": "priority",
		"customfield_date":     "datetime",
		"customfield_text":     "string",
		"customfield_multi":    "multiselect",
	}

	t.Run("nil fields returns nil", func(t *testing.T) {
		result := formatAdditionalFields(nil, fieldTypes)
		assert.Nil(t, result)
	})

	t.Run("select field - plain string wrapped", func(t *testing.T) {
		fields := map[string]any{"customfield_team": "Backend"}
		result := formatAdditionalFields(fields, fieldTypes)
		assert.Equal(t, map[string]any{"value": "Backend"}, result["customfield_team"])
	})

	t.Run("select field - already structured with name", func(t *testing.T) {
		fields := map[string]any{"customfield_team": map[string]any{"name": "Backend"}}
		result := formatAdditionalFields(fields, fieldTypes)
		assert.Equal(t, map[string]any{"name": "Backend"}, result["customfield_team"])
	})

	t.Run("select field - already structured with value", func(t *testing.T) {
		fields := map[string]any{"customfield_team": map[string]any{"value": "Backend"}}
		result := formatAdditionalFields(fields, fieldTypes)
		assert.Equal(t, map[string]any{"value": "Backend"}, result["customfield_team"])
	})

	t.Run("select field - already structured with id", func(t *testing.T) {
		fields := map[string]any{"customfield_team": map[string]any{"id": "123"}}
		result := formatAdditionalFields(fields, fieldTypes)
		assert.Equal(t, map[string]any{"id": "123"}, result["customfield_team"])
	})

	t.Run("multicheckboxes field - array of strings", func(t *testing.T) {
		fields := map[string]any{"customfield_tags": []any{"frontend", "urgent"}}
		result := formatAdditionalFields(fields, fieldTypes)
		expected := []any{map[string]any{"value": "frontend"}, map[string]any{"value": "urgent"}}
		assert.Equal(t, expected, result["customfield_tags"])
	})

	t.Run("multicheckboxes field - single string wrapped in array", func(t *testing.T) {
		fields := map[string]any{"customfield_tags": "frontend"}
		result := formatAdditionalFields(fields, fieldTypes)
		expected := []any{map[string]any{"value": "frontend"}}
		assert.Equal(t, expected, result["customfield_tags"])
	})

	t.Run("multiselect field - array of structured values", func(t *testing.T) {
		fields := map[string]any{"customfield_multi": []any{map[string]any{"value": "A"}, map[string]any{"value": "B"}}}
		result := formatAdditionalFields(fields, fieldTypes)
		expected := []any{map[string]any{"value": "A"}, map[string]any{"value": "B"}}
		assert.Equal(t, expected, result["customfield_multi"])
	})

	t.Run("priority field - flattened from object", func(t *testing.T) {
		fields := map[string]any{"customfield_priority": map[string]any{"name": "High"}}
		result := formatAdditionalFields(fields, fieldTypes)
		assert.Equal(t, "High", result["customfield_priority"])
	})

	t.Run("datetime field - plain value passthrough", func(t *testing.T) {
		fields := map[string]any{"customfield_date": "2026-02-18T10:00:00Z"}
		result := formatAdditionalFields(fields, fieldTypes)
		assert.Equal(t, "2026-02-18T10:00:00Z", result["customfield_date"])
	})

	t.Run("string field - plain value passthrough", func(t *testing.T) {
		fields := map[string]any{"customfield_text": "some text"}
		result := formatAdditionalFields(fields, fieldTypes)
		assert.Equal(t, "some text", result["customfield_text"])
	})

	t.Run("unknown field type - scalar flattening from value key", func(t *testing.T) {
		fields := map[string]any{"unknown_field": map[string]any{"value": "test"}}
		result := formatAdditionalFields(fields, nil)
		assert.Equal(t, "test", result["unknown_field"])
	})
}

// --- toSelectValue tests ---

func TestToSelectValue(t *testing.T) {
	t.Run("string wrapped as value", func(t *testing.T) {
		result := toSelectValue("Backend")
		assert.Equal(t, map[string]any{"value": "Backend"}, result)
	})

	t.Run("map with id preserved", func(t *testing.T) {
		input := map[string]any{"id": "123"}
		result := toSelectValue(input)
		assert.Equal(t, input, result)
	})

	t.Run("map with value preserved", func(t *testing.T) {
		input := map[string]any{"value": "Backend"}
		result := toSelectValue(input)
		assert.Equal(t, input, result)
	})

	t.Run("map with name preserved", func(t *testing.T) {
		input := map[string]any{"name": "High"}
		result := toSelectValue(input)
		assert.Equal(t, input, result)
	})

	t.Run("map without recognized keys returned as-is", func(t *testing.T) {
		input := map[string]any{"label": "Something"}
		result := toSelectValue(input)
		assert.Equal(t, input, result)
	})

	t.Run("non-string non-map returned as-is", func(t *testing.T) {
		result := toSelectValue(42)
		assert.Equal(t, 42, result)
	})
}

// --- toMultiSelectValue tests ---

func TestToMultiSelectValue(t *testing.T) {
	t.Run("array of strings", func(t *testing.T) {
		result := toMultiSelectValue([]any{"A", "B", "C"})
		expected := []any{
			map[string]any{"value": "A"},
			map[string]any{"value": "B"},
			map[string]any{"value": "C"},
		}
		assert.Equal(t, expected, result)
	})

	t.Run("array of maps preserved", func(t *testing.T) {
		input := []any{map[string]any{"value": "A"}, map[string]any{"id": "2"}}
		result := toMultiSelectValue(input)
		expected := []any{map[string]any{"value": "A"}, map[string]any{"id": "2"}}
		assert.Equal(t, expected, result)
	})

	t.Run("single string wrapped in array", func(t *testing.T) {
		result := toMultiSelectValue("SingleVal")
		expected := []any{map[string]any{"value": "SingleVal"}}
		assert.Equal(t, expected, result)
	})

	t.Run("empty array", func(t *testing.T) {
		result := toMultiSelectValue([]any{})
		assert.Equal(t, []any{}, result)
	})
}

// --- toScalarValue tests ---

func TestToScalarValue(t *testing.T) {
	t.Run("map with name extracted", func(t *testing.T) {
		result := toScalarValue(map[string]any{"name": "High"})
		assert.Equal(t, "High", result)
	})

	t.Run("map with value extracted", func(t *testing.T) {
		result := toScalarValue(map[string]any{"value": "Medium"})
		assert.Equal(t, "Medium", result)
	})

	t.Run("map with name takes precedence over value", func(t *testing.T) {
		result := toScalarValue(map[string]any{"name": "ByName", "value": "ByValue"})
		assert.Equal(t, "ByName", result)
	})

	t.Run("plain string returned as-is", func(t *testing.T) {
		result := toScalarValue("plain")
		assert.Equal(t, "plain", result)
	})

	t.Run("number returned as-is", func(t *testing.T) {
		result := toScalarValue(42)
		assert.Equal(t, 42, result)
	})

	t.Run("map without name or value returned as-is", func(t *testing.T) {
		input := map[string]any{"other": "key"}
		result := toScalarValue(input)
		assert.Equal(t, input, result)
	})
}

// --- buildFieldTypeMap tests ---

func TestBuildFieldTypeMap(t *testing.T) {
	t.Run("builds map from create-meta response", func(t *testing.T) {
		meta := ticketServerCreateMetaResponse{}
		meta.Data.TicketsGetCreateMeta = []ticketServerIssueType{
			{
				Name: "Bug",
				Fields: map[string]ticketServerFieldInfo{
					"summary":  {Key: "summary", Type: "string"},
					"priority": {Key: "priority", Type: "select"},
					"labels":   {Key: "labels", Type: "multicheckboxes"},
				},
			},
		}
		result := buildFieldTypeMap(meta)
		assert.Equal(t, "string", result["summary"])
		assert.Equal(t, "select", result["priority"])
		assert.Equal(t, "multicheckboxes", result["labels"])
	})

	t.Run("merges fields from multiple issue types", func(t *testing.T) {
		meta := ticketServerCreateMetaResponse{}
		meta.Data.TicketsGetCreateMeta = []ticketServerIssueType{
			{
				Name: "Bug",
				Fields: map[string]ticketServerFieldInfo{
					"summary": {Key: "summary", Type: "string"},
				},
			},
			{
				Name: "Task",
				Fields: map[string]ticketServerFieldInfo{
					"story_points": {Key: "story_points", Type: "number"},
				},
			},
		}
		result := buildFieldTypeMap(meta)
		assert.Equal(t, "string", result["summary"])
		assert.Equal(t, "number", result["story_points"])
	})

	t.Run("returns nil for error response", func(t *testing.T) {
		meta := ticketServerCreateMetaResponse{Error: "something went wrong"}
		result := buildFieldTypeMap(meta)
		assert.Nil(t, result)
	})

	t.Run("returns nil for empty issue types", func(t *testing.T) {
		meta := ticketServerCreateMetaResponse{}
		result := buildFieldTypeMap(meta)
		assert.Nil(t, result)
	})
}

// --- create-meta response parsing tests (Jira vs GitHub format) ---

func TestCreateMetaResponseParsing(t *testing.T) {
	t.Run("GitHub format - nested under tickets_get_create_meta", func(t *testing.T) {
		body := []byte(`{"data":{"tickets_get_create_meta":[{"name":"GitHub Issue","fields":{"summary":{"key":"summary","name":"Summary","required":true,"type":"string"}}}]}}`)

		var result ticketServerCreateMetaResponse
		err := common.UnmarshalJson(body, &result)
		assert.NoError(t, err)
		assert.Len(t, result.Data.TicketsGetCreateMeta, 1)
		assert.Equal(t, "GitHub Issue", result.Data.TicketsGetCreateMeta[0].Name)
	})

	t.Run("Jira format - direct array under data", func(t *testing.T) {
		body := []byte(`{"data":[{"name":"Task","fields":{"summary":{"key":"summary","name":"Summary","required":true,"type":"string"},"priority":{"key":"priority","name":"Priority","required":true,"type":"select","allowedValues":[{"name":"High"},{"name":"Medium"},{"name":"Low"}]}}},{"name":"Epic","fields":{"summary":{"key":"summary","name":"Summary","required":true,"type":"string"}}}]}`)

		// Standard parse errors because "data" is an array, not an object
		var result ticketServerCreateMetaResponse
		err := common.UnmarshalJson(body, &result)
		assert.Error(t, err, "standard format should fail on Jira array-shaped data")

		// Jira fallback parse should work
		var jiraResult ticketServerCreateMetaResponseJira
		err = common.UnmarshalJson(body, &jiraResult)
		assert.NoError(t, err)
		assert.Len(t, jiraResult.Data, 2)
		assert.Equal(t, "Task", jiraResult.Data[0].Name)
		assert.Equal(t, "Epic", jiraResult.Data[1].Name)

		// Verify fields are parsed correctly
		taskFields := jiraResult.Data[0].Fields
		assert.True(t, taskFields["summary"].Required)
		assert.Equal(t, "select", taskFields["priority"].Type)
		assert.Len(t, taskFields["priority"].AllowedValues, 3)
	})

	t.Run("Jira format with custom fields", func(t *testing.T) {
		body := []byte(`{"data":[{"name":"Task","fields":{"summary":{"key":"summary","name":"Summary","required":true,"type":"string"},"customfield_10034":{"key":"customfield_10034","name":"nudgebee_teams","required":true,"type":"select","allowedValues":[{"id":"10020","value":"Infra"},{"id":"10021","value":"UI"},{"id":"10022","value":"Backend"}]},"customfield_10035":{"key":"customfield_10035","name":"Options","required":true,"type":"multicheckboxes","allowedValues":[{"id":"10026","value":"A"},{"id":"10027","value":"B"},{"id":"10028","value":"C"}]},"customfield_10036":{"key":"customfield_10036","name":"Found On","required":true,"type":"datetime"},"customfield_10037":{"key":"customfield_10037","name":"Seen Date","required":true,"type":"datepicker"}}}]}`)

		var jiraResult ticketServerCreateMetaResponseJira
		err := common.UnmarshalJson(body, &jiraResult)
		assert.NoError(t, err)
		assert.Len(t, jiraResult.Data, 1)

		fields := jiraResult.Data[0].Fields
		assert.Equal(t, "select", fields["customfield_10034"].Type)
		assert.Equal(t, "multicheckboxes", fields["customfield_10035"].Type)
		assert.Equal(t, "datetime", fields["customfield_10036"].Type)
		assert.Equal(t, "datepicker", fields["customfield_10037"].Type)
		assert.True(t, fields["customfield_10034"].Required)
		assert.Len(t, fields["customfield_10034"].AllowedValues, 3)
		assert.Len(t, fields["customfield_10035"].AllowedValues, 3)
	})

	t.Run("error response parsed correctly", func(t *testing.T) {
		body := []byte(`{"error":"integration not found"}`)

		var result ticketServerCreateMetaResponse
		err := common.UnmarshalJson(body, &result)
		assert.NoError(t, err)
		assert.Equal(t, "integration not found", result.Error)
		assert.Empty(t, result.Data.TicketsGetCreateMeta)
	})
}

// --- getIntegrationFromToolConfig tests ---

func TestGetIntegrationFromToolConfig(t *testing.T) {
	t.Run("extracts id and type", func(t *testing.T) {
		cfg := core.ToolConfig{
			Values: []core.ToolConfigValue{
				{Name: "id", Value: "intg-001"},
				{Name: "type", Value: "jira"},
			},
		}
		id, typ := getIntegrationFromToolConfig(cfg)
		assert.Equal(t, "intg-001", id)
		assert.Equal(t, "jira", typ)
	})

	t.Run("empty config returns empty strings", func(t *testing.T) {
		cfg := core.ToolConfig{}
		id, typ := getIntegrationFromToolConfig(cfg)
		assert.Empty(t, id)
		assert.Empty(t, typ)
	})

	t.Run("partial config - only id", func(t *testing.T) {
		cfg := core.ToolConfig{
			Values: []core.ToolConfigValue{
				{Name: "id", Value: "intg-001"},
			},
		}
		id, typ := getIntegrationFromToolConfig(cfg)
		assert.Equal(t, "intg-001", id)
		assert.Empty(t, typ)
	})

	t.Run("partial config - only type", func(t *testing.T) {
		cfg := core.ToolConfig{
			Values: []core.ToolConfigValue{
				{Name: "type", Value: "github"},
			},
		}
		id, typ := getIntegrationFromToolConfig(cfg)
		assert.Empty(t, id)
		assert.Equal(t, "github", typ)
	})
}

// --- resolveProjectFromConfig tests ---

func TestResolveProjectFromConfig(t *testing.T) {
	t.Run("no projects in config returns empty", func(t *testing.T) {
		cfg := core.ToolConfig{
			Values: []core.ToolConfigValue{
				{Name: "id", Value: "intg-001"},
				{Name: "type", Value: "github"},
			},
		}
		result, err := resolveProjectFromConfig(cfg)
		assert.NoError(t, err)
		assert.Empty(t, result)
	})

	t.Run("empty projects JSON returns empty", func(t *testing.T) {
		cfg := core.ToolConfig{
			Values: []core.ToolConfigValue{
				{Name: "projects", Value: ""},
			},
		}
		result, err := resolveProjectFromConfig(cfg)
		assert.NoError(t, err)
		assert.Empty(t, result)
	})

	t.Run("empty projects array returns empty", func(t *testing.T) {
		cfg := core.ToolConfig{
			Values: []core.ToolConfigValue{
				{Name: "projects", Value: "[]"},
			},
		}
		result, err := resolveProjectFromConfig(cfg)
		assert.NoError(t, err)
		assert.Empty(t, result)
	})

	t.Run("single project auto-selects by key", func(t *testing.T) {
		cfg := core.ToolConfig{
			Values: []core.ToolConfigValue{
				{Name: "projects", Value: `[{"key": "nudgebee/api-server", "name": "api-server"}]`},
			},
		}
		result, err := resolveProjectFromConfig(cfg)
		assert.NoError(t, err)
		assert.Equal(t, "nudgebee/api-server", result)
	})

	t.Run("single project auto-selects by name when no key", func(t *testing.T) {
		cfg := core.ToolConfig{
			Values: []core.ToolConfigValue{
				{Name: "projects", Value: `[{"name": "my-project"}]`},
			},
		}
		result, err := resolveProjectFromConfig(cfg)
		assert.NoError(t, err)
		assert.Equal(t, "my-project", result)
	})

	t.Run("multiple projects returns error with options", func(t *testing.T) {
		cfg := core.ToolConfig{
			Values: []core.ToolConfigValue{
				{Name: "projects", Value: `[{"key": "nudgebee/api-server"}, {"key": "nudgebee/app"}, {"key": "nudgebee/llm-server"}]`},
			},
		}
		result, err := resolveProjectFromConfig(cfg)
		assert.Error(t, err)
		assert.Empty(t, result)
		assert.Contains(t, err.Error(), "multiple projects/repositories are configured")
		assert.Contains(t, err.Error(), "nudgebee/api-server")
		assert.Contains(t, err.Error(), "nudgebee/app")
		assert.Contains(t, err.Error(), "nudgebee/llm-server")
	})

	t.Run("multiple projects with name fallback", func(t *testing.T) {
		cfg := core.ToolConfig{
			Values: []core.ToolConfigValue{
				{Name: "projects", Value: `[{"name": "proj-a"}, {"name": "proj-b"}]`},
			},
		}
		result, err := resolveProjectFromConfig(cfg)
		assert.Error(t, err)
		assert.Empty(t, result)
		assert.Contains(t, err.Error(), "proj-a")
		assert.Contains(t, err.Error(), "proj-b")
	})

	t.Run("invalid JSON returns empty without error", func(t *testing.T) {
		cfg := core.ToolConfig{
			Values: []core.ToolConfigValue{
				{Name: "projects", Value: `not-json`},
			},
		}
		result, err := resolveProjectFromConfig(cfg)
		assert.NoError(t, err)
		assert.Empty(t, result)
	})
}

// --- multipleIntegrationsError tests ---

func TestMultipleIntegrationsError(t *testing.T) {
	t.Run("error message lists all integrations", func(t *testing.T) {
		err := &multipleIntegrationsError{
			platform: "jira",
			matches: []ticketIntegration{
				{ID: "intg-001", Type: "jira", Name: "Jira Cloud"},
				{ID: "intg-002", Type: "jira", Name: "Jira Datacenter"},
			},
		}
		msg := err.Error()
		assert.Contains(t, msg, "Multiple jira integrations found")
		assert.Contains(t, msg, "Jira Cloud")
		assert.Contains(t, msg, "intg-001")
		assert.Contains(t, msg, "Jira Datacenter")
		assert.Contains(t, msg, "intg-002")
	})
}

// --- TicketMasterV2 tool metadata tests ---

func TestTicketMasterV2_Metadata(t *testing.T) {
	tool := TicketMasterV2{}

	t.Run("Name", func(t *testing.T) {
		assert.Equal(t, "ticket_master_v2", tool.Name())
	})

	t.Run("Type", func(t *testing.T) {
		assert.Equal(t, core.NBToolTypeTool, tool.GetType())
	})

	t.Run("Description mentions all platforms", func(t *testing.T) {
		desc := tool.Description()
		assert.Contains(t, desc, "Jira")
		assert.Contains(t, desc, "GitHub")
		assert.Contains(t, desc, "GitLab")
		assert.Contains(t, desc, "ServiceNow")
		assert.Contains(t, desc, "PagerDuty")
		assert.Contains(t, desc, "ZenDuty")
	})

	t.Run("InputSchema has command property", func(t *testing.T) {
		schema := tool.InputSchema()
		assert.Equal(t, core.ToolSchemaTypeObject, schema.Type)
		assert.Contains(t, schema.Properties, "command")
		assert.Contains(t, schema.Required, "command")
	})

	t.Run("InputSchema command mentions all operations", func(t *testing.T) {
		schema := tool.InputSchema()
		cmdDesc := schema.Properties["command"].Description
		assert.Contains(t, cmdDesc, "get_create_meta")
		assert.Contains(t, cmdDesc, "create_ticket")
		assert.Contains(t, cmdDesc, "add_comment")
		assert.Contains(t, cmdDesc, "get_comments")
		assert.Contains(t, cmdDesc, "get_ticket")
		assert.Contains(t, cmdDesc, "list_tickets")
	})
}

// --- writeFieldInfo tests ---

func TestWriteFieldInfo(t *testing.T) {
	t.Run("basic field info", func(t *testing.T) {
		var sb strings.Builder
		f := ticketServerFieldInfo{Key: "summary", Name: "Summary", Type: "string"}
		writeFieldInfo(&sb, f)
		result := sb.String()
		assert.Contains(t, result, "`summary`")
		assert.Contains(t, result, "Summary")
		assert.Contains(t, result, "string")
	})

	t.Run("field with allowed values", func(t *testing.T) {
		var sb strings.Builder
		f := ticketServerFieldInfo{
			Key:  "priority",
			Name: "Priority",
			Type: "select",
			AllowedValues: []any{
				map[string]any{"name": "High"},
				map[string]any{"name": "Medium"},
				map[string]any{"name": "Low"},
			},
		}
		writeFieldInfo(&sb, f)
		result := sb.String()
		assert.Contains(t, result, "High")
		assert.Contains(t, result, "Medium")
		assert.Contains(t, result, "Low")
		assert.Contains(t, result, "allowed:")
	})

	t.Run("field with many allowed values truncated", func(t *testing.T) {
		var sb strings.Builder
		var vals []any
		for i := 0; i < 15; i++ {
			vals = append(vals, map[string]any{"name": fmt.Sprintf("Val%d", i)})
		}
		f := ticketServerFieldInfo{
			Key:           "component",
			Name:          "Component",
			Type:          "select",
			AllowedValues: vals,
		}
		writeFieldInfo(&sb, f)
		result := sb.String()
		assert.Contains(t, result, "+5 more")
	})

	t.Run("field with value-keyed allowed values", func(t *testing.T) {
		var sb strings.Builder
		f := ticketServerFieldInfo{
			Key:  "custom_field",
			Name: "Custom",
			Type: "select",
			AllowedValues: []any{
				map[string]any{"value": "Option1"},
				map[string]any{"value": "Option2"},
			},
		}
		writeFieldInfo(&sb, f)
		result := sb.String()
		assert.Contains(t, result, "Option1")
		assert.Contains(t, result, "Option2")
	})
}

// --- ticketServerListRequest / ticketServerListResponse type tests ---

func TestTicketServerListTypes(t *testing.T) {
	t.Run("list request serializes correctly", func(t *testing.T) {
		req := ticketServerListRequest{
			IntegrationID: "intg-001",
			AccountID:     "acc-001",
			Params: ticketServerListParams{
				ProjectKey: "PROJ",
				Status:     "open",
				Priority:   "High",
				Assignee:   "alice",
				Limit:      10,
				Offset:     20,
				SortBy:     "created_at",
				SortOrder:  "desc",
			},
		}
		data, err := common.MarshalJson(req)
		assert.NoError(t, err)
		assert.Contains(t, string(data), `"integration_id":"intg-001"`)
		assert.Contains(t, string(data), `"project_key":"PROJ"`)
		assert.Contains(t, string(data), `"status":"open"`)
		assert.Contains(t, string(data), `"priority":"High"`)
	})

	t.Run("list response deserializes correctly", func(t *testing.T) {
		body := []byte(`{"tickets":[{"ticket_id":"PROJ-1","title":"Bug fix","status":"open","severity":"High","assignee":"alice","platform":"jira","url":"https://jira.example.com/PROJ-1","created_at":"2026-02-19T10:00:00Z"}],"total":1,"limit":20,"offset":0}`)
		var result ticketServerListResponse
		err := common.UnmarshalJson(body, &result)
		assert.NoError(t, err)
		assert.Len(t, result.Tickets, 1)
		assert.Equal(t, "PROJ-1", result.Tickets[0].TicketID)
		assert.Equal(t, "Bug fix", result.Tickets[0].Title)
		assert.Equal(t, "open", result.Tickets[0].Status)
		assert.Equal(t, "High", result.Tickets[0].Severity)
		assert.Equal(t, 1, result.Total)
		assert.Equal(t, 20, result.Limit)
		assert.Equal(t, 0, result.Offset)
	})

	t.Run("list response with error", func(t *testing.T) {
		body := []byte(`{"error":"integration not found","tickets":[],"total":0,"limit":0,"offset":0}`)
		var result ticketServerListResponse
		err := common.UnmarshalJson(body, &result)
		assert.NoError(t, err)
		assert.Equal(t, "integration not found", result.Error)
		assert.Empty(t, result.Tickets)
	})

	t.Run("list response with empty tickets", func(t *testing.T) {
		body := []byte(`{"tickets":[],"total":0,"limit":20,"offset":0}`)
		var result ticketServerListResponse
		err := common.UnmarshalJson(body, &result)
		assert.NoError(t, err)
		assert.Empty(t, result.Tickets)
		assert.Equal(t, 0, result.Total)
	})
}
