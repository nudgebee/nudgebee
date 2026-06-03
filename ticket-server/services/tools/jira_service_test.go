package tools

import (
	"reflect"
	"testing"

	jira "github.com/andygrunwald/go-jira"
	"github.com/trivago/tgo/tcontainer"
)

func TestBuildADFDocument(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []map[string]any
	}{
		{
			name: "single line",
			in:   "hello world",
			want: []map[string]any{
				{
					"type": "paragraph",
					"content": []map[string]any{
						{"type": "text", "text": "hello world"},
					},
				},
			},
		},
		{
			name: "multi line",
			in:   "a\nb\nc",
			want: []map[string]any{
				{"type": "paragraph", "content": []map[string]any{{"type": "text", "text": "a"}}},
				{"type": "paragraph", "content": []map[string]any{{"type": "text", "text": "b"}}},
				{"type": "paragraph", "content": []map[string]any{{"type": "text", "text": "c"}}},
			},
		},
		{
			name: "blank line preserved",
			in:   "a\n\nb",
			want: []map[string]any{
				{"type": "paragraph", "content": []map[string]any{{"type": "text", "text": "a"}}},
				{"type": "paragraph", "content": []map[string]any{}},
				{"type": "paragraph", "content": []map[string]any{{"type": "text", "text": "b"}}},
			},
		},
		{
			name: "crlf normalized",
			in:   "a\r\nb",
			want: []map[string]any{
				{"type": "paragraph", "content": []map[string]any{{"type": "text", "text": "a"}}},
				{"type": "paragraph", "content": []map[string]any{{"type": "text", "text": "b"}}},
			},
		},
		{
			name: "lone cr normalized",
			in:   "a\rb",
			want: []map[string]any{
				{"type": "paragraph", "content": []map[string]any{{"type": "text", "text": "a"}}},
				{"type": "paragraph", "content": []map[string]any{{"type": "text", "text": "b"}}},
			},
		},
		{
			name: "empty string",
			in:   "",
			want: []map[string]any{
				{"type": "paragraph", "content": []map[string]any{}},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := buildADFDocument(tc.in)

			if got["type"] != "doc" {
				t.Errorf("type = %v, want %q", got["type"], "doc")
			}
			if got["version"] != 1 {
				t.Errorf("version = %v, want 1", got["version"])
			}

			content, ok := got["content"].([]map[string]any)
			if !ok {
				t.Fatalf("content type = %T, want []map[string]any", got["content"])
			}
			if !reflect.DeepEqual(content, tc.want) {
				t.Errorf("content mismatch\n  got  %#v\n  want %#v", content, tc.want)
			}
		})
	}
}

// build produces a CreateMetaInfo with one project and one issue type, mirroring the
// shape that the go-jira JSON decoder yields. Inner field values are stored as plain
// map[string]interface{} (not tcontainer.MarshalMap) so the production type assertion
// `field.(map[string]interface{})` matches what JSON unmarshaling actually produces.
func TestSanitizeJiraMeta(t *testing.T) {
	build := func(fields map[string]map[string]interface{}) *jira.CreateMetaInfo {
		f := tcontainer.MarshalMap{}
		for k, v := range fields {
			f[k] = v
		}
		return &jira.CreateMetaInfo{
			Projects: []*jira.MetaProject{{
				Key: "PROJ",
				IssueTypes: []*jira.MetaIssueType{{
					Name:   "Task",
					Fields: f,
				}},
			}},
		}
	}

	extractFields := func(t *testing.T, sanitized map[string]interface{}) map[string]FieldInfo {
		t.Helper()
		templates, ok := sanitized["data"].([]Template)
		if !ok {
			t.Fatalf("expected data to be []Template, got %T", sanitized["data"])
		}
		if len(templates) != 1 {
			t.Fatalf("expected 1 template, got %d", len(templates))
		}
		return templates[0].Fields
	}

	t.Run("priority with allowedValues is included even when not required", func(t *testing.T) {
		// Regression: Jira marks priority optional but ships allowedValues.
		// Earlier behavior dropped these fields; severity dropdown rendered empty.
		meta := build(map[string]map[string]interface{}{
			"priority": {
				"required": false,
				"name":     "Priority",
				"key":      "priority",
				"schema":   map[string]interface{}{"type": "priority"},
				"allowedValues": []interface{}{
					map[string]interface{}{"id": "1", "name": "High"},
					map[string]interface{}{"id": "2", "name": "Low"},
				},
			},
		})
		fields := extractFields(t, sanitizeJiraMeta(meta, nil, nil))
		got, ok := fields["priority"]
		if !ok {
			t.Fatalf("priority field dropped despite carrying allowedValues; fields=%v", fields)
		}
		if len(got.AllowedValues) != 2 {
			t.Errorf("priority allowedValues len = %d, want 2", len(got.AllowedValues))
		}
	})

	t.Run("missing required key does not panic", func(t *testing.T) {
		// Some Jira Cloud custom field types omit `required` entirely. The pre-fix
		// code had `fieldMap["required"].(bool)` which panicked on nil.
		meta := build(map[string]map[string]interface{}{
			"customfield_10001": {
				"name":   "Story Points",
				"key":    "customfield_10001",
				"schema": map[string]interface{}{"type": "number"},
				// no `required`
			},
		})
		// Should not panic.
		_ = sanitizeJiraMeta(meta, nil, nil)
	})

	t.Run("required field without allowedValues is included", func(t *testing.T) {
		meta := build(map[string]map[string]interface{}{
			"summary": {
				"required": true,
				"name":     "Summary",
				"key":      "summary",
				"schema":   map[string]interface{}{"type": "string"},
			},
		})
		fields := extractFields(t, sanitizeJiraMeta(meta, nil, nil))
		if _, ok := fields["summary"]; !ok {
			t.Errorf("required summary field was dropped")
		}
	})

	t.Run("ignored fields are filtered even with allowedValues", func(t *testing.T) {
		meta := build(map[string]map[string]interface{}{
			"issuetype": {
				"required": true,
				"name":     "Issue Type",
				"key":      "issuetype",
				"schema":   map[string]interface{}{"type": "issuetype"},
				"allowedValues": []interface{}{
					map[string]interface{}{"id": "1", "name": "Task"},
				},
			},
		})
		fields := extractFields(t, sanitizeJiraMeta(meta, nil, nil))
		if _, ok := fields["issuetype"]; ok {
			t.Errorf("issuetype should be filtered out by ignoreFields")
		}
	})

	t.Run("optional non-select field with no allowedValues is dropped", func(t *testing.T) {
		// Optional fields without options carry no UX value — they would render as
		// empty dropdowns. The filter intentionally omits them.
		meta := build(map[string]map[string]interface{}{
			"environment": {
				"required": false,
				"name":     "Environment",
				"key":      "environment",
				"schema":   map[string]interface{}{"type": "string"},
			},
		})
		fields := extractFields(t, sanitizeJiraMeta(meta, nil, nil))
		if _, ok := fields["environment"]; ok {
			t.Errorf("optional env field should be dropped when no allowedValues present")
		}
	})

	t.Run("must-have fields included even without allowedValues", func(t *testing.T) {
		// `assignee` lives in mustFields so it ships with autoCompleteUrl alone.
		meta := build(map[string]map[string]interface{}{
			"assignee": {
				"required":        false,
				"name":            "Assignee",
				"key":             "assignee",
				"schema":          map[string]interface{}{"type": "user"},
				"autoCompleteUrl": "https://jira.example.com/rest/api/2/user/assignable/search?project=PROJ&username=",
			},
		})
		fields := extractFields(t, sanitizeJiraMeta(meta, nil, nil))
		got, ok := fields["assignee"]
		if !ok {
			t.Fatalf("assignee dropped despite mustFields membership")
		}
		if got.AutoCompleteUrl == "" {
			t.Errorf("assignee autoCompleteUrl not propagated")
		}
	})
}

func TestSanitizeJiraMeta_SeedAndGroups(t *testing.T) {
	buildPriority := func(fields map[string]map[string]interface{}) *jira.CreateMetaInfo {
		f := tcontainer.MarshalMap{}
		for k, v := range fields {
			f[k] = v
		}
		return &jira.CreateMetaInfo{
			Projects: []*jira.MetaProject{{
				Key:        "PROJ",
				IssueTypes: []*jira.MetaIssueType{{Name: "Task", Fields: f}},
			}},
		}
	}
	fieldsOf := func(t *testing.T, sanitized map[string]interface{}) map[string]FieldInfo {
		t.Helper()
		templates := sanitized["data"].([]Template)
		if len(templates) != 1 {
			t.Fatalf("expected 1 template, got %d", len(templates))
		}
		return templates[0].Fields
	}
	priorities := []jira.Priority{{ID: "1", Name: "High"}, {ID: "2", Name: "Low"}}
	users := []jira.User{{AccountID: "acc-1", DisplayName: "Ada"}, {AccountID: "acc-2", DisplayName: "Lin"}}

	t.Run("live priority allowedValues win over seed", func(t *testing.T) {
		meta := buildPriority(map[string]map[string]interface{}{
			"priority": {
				"required": false, "name": "Priority", "key": "priority",
				"schema":        map[string]interface{}{"type": "priority"},
				"allowedValues": []interface{}{map[string]interface{}{"id": "9", "name": "Custom"}},
			},
		})
		got := fieldsOf(t, sanitizeJiraMeta(meta, priorities, users))["priority"]
		if len(got.AllowedValues) != 1 {
			t.Fatalf("live allowedValues should be preserved, got %d", len(got.AllowedValues))
		}
		if got.Group != FieldGroupSeverity {
			t.Errorf("priority group = %q, want %q", got.Group, FieldGroupSeverity)
		}
	})

	t.Run("empty priority allowedValues filled from scheme", func(t *testing.T) {
		meta := buildPriority(map[string]map[string]interface{}{
			"priority": {
				"required": false, "name": "Priority", "key": "priority",
				"schema": map[string]interface{}{"type": "priority"},
			},
		})
		got := fieldsOf(t, sanitizeJiraMeta(meta, priorities, users))["priority"]
		if len(got.AllowedValues) != 2 {
			t.Fatalf("priority should be seeded from scheme, got %d", len(got.AllowedValues))
		}
		first := got.AllowedValues[0].(map[string]interface{})
		if first["value"] != "1" {
			t.Errorf("seeded priority value = %v, want priority id \"1\"", first["value"])
		}
	})

	t.Run("absent priority synthesized from scheme", func(t *testing.T) {
		meta := buildPriority(map[string]map[string]interface{}{
			"summary": {"required": true, "name": "Summary", "key": "summary", "schema": map[string]interface{}{"type": "string"}},
		})
		fields := sanitizeJiraMeta(meta, priorities, users)
		got, ok := fieldsOf(t, fields)["priority"]
		if !ok {
			t.Fatal("priority should be synthesized when absent")
		}
		if len(got.AllowedValues) != 2 || got.Group != FieldGroupSeverity {
			t.Errorf("synthesized priority malformed: vals=%d group=%q", len(got.AllowedValues), got.Group)
		}
	})

	t.Run("no scheme leaves priority unseeded", func(t *testing.T) {
		meta := buildPriority(map[string]map[string]interface{}{
			"summary": {"required": true, "name": "Summary", "key": "summary", "schema": map[string]interface{}{"type": "string"}},
		})
		if _, ok := fieldsOf(t, sanitizeJiraMeta(meta, nil, nil))["priority"]; ok {
			t.Error("priority should not be synthesized when scheme fetch failed (nil)")
		}
	})

	t.Run("assignee seeded from users but keeps autoCompleteUrl", func(t *testing.T) {
		meta := buildPriority(map[string]map[string]interface{}{
			"assignee": {
				"required": false, "name": "Assignee", "key": "assignee",
				"schema":          map[string]interface{}{"type": "user"},
				"autoCompleteUrl": "https://jira.example.com/rest/api/2/user/assignable/search?project=PROJ&username=",
			},
		})
		got := fieldsOf(t, sanitizeJiraMeta(meta, priorities, users))["assignee"]
		if len(got.AllowedValues) != 2 {
			t.Fatalf("assignee should be seeded with 2 users, got %d", len(got.AllowedValues))
		}
		if got.AutoCompleteUrl == "" {
			t.Error("assignee must keep autoCompleteUrl for live search after seeding")
		}
	})

	t.Run("assignee with no users keeps empty values and url", func(t *testing.T) {
		meta := buildPriority(map[string]map[string]interface{}{
			"assignee": {
				"required": false, "name": "Assignee", "key": "assignee",
				"schema":          map[string]interface{}{"type": "user"},
				"autoCompleteUrl": "https://jira.example.com/x?username=",
			},
		})
		got := fieldsOf(t, sanitizeJiraMeta(meta, priorities, nil))["assignee"]
		if len(got.AllowedValues) != 0 {
			t.Errorf("assignee should stay unseeded with no users, got %d", len(got.AllowedValues))
		}
		if got.AutoCompleteUrl == "" {
			t.Error("assignee must keep autoCompleteUrl to defer to live search")
		}
	})
}
