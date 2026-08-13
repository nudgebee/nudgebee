package tools

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"nudgebee/tickets-server/models"

	jira "github.com/andygrunwald/go-jira"
)

func customSchema(suffix, baseType string) map[string]interface{} {
	return map[string]interface{}{
		"custom": "com.atlassian.jira.plugin.system.customfieldtypes:" + suffix,
		"type":   baseType,
	}
}

func TestNormalizeJiraFieldType(t *testing.T) {
	cases := []struct {
		name     string
		fieldKey string
		schema   interface{}
		want     string
		wantOK   bool
	}{
		{name: "parent carve-out", fieldKey: "parent", schema: nil, want: "string", wantOK: true},
		{name: "assignee carve-out", fieldKey: "assignee", schema: map[string]interface{}{"type": "user"}, want: "select", wantOK: true},
		{name: "system string", fieldKey: "environment", schema: map[string]interface{}{"type": "string"}, want: "string", wantOK: true},
		{name: "system priority", fieldKey: "priority", schema: map[string]interface{}{"type": "priority"}, want: "select", wantOK: true},
		{name: "duedate is a datepicker", fieldKey: "duedate", schema: map[string]interface{}{"type": "date", "system": "duedate"}, want: "datepicker", wantOK: true},
		{name: "labels array of string", fieldKey: "labels", schema: map[string]interface{}{"type": "array", "items": "string"}, want: "array", wantOK: true},
		{name: "components array of component", fieldKey: "components", schema: map[string]interface{}{"type": "array", "items": "component"}, want: "multiselect", wantOK: true},
		{name: "fixVersions array of version", fieldKey: "fixVersions", schema: map[string]interface{}{"type": "array", "items": "version"}, want: "multiselect", wantOK: true},
		{name: "custom textfield", fieldKey: "customfield_1", schema: customSchema("textfield", "string"), want: "string", wantOK: true},
		{name: "custom textarea", fieldKey: "customfield_2", schema: customSchema("textarea", "string"), want: "text", wantOK: true},
		{name: "custom select", fieldKey: "customfield_3", schema: customSchema("select", "option"), want: "select", wantOK: true},
		{name: "custom radiobuttons", fieldKey: "customfield_4", schema: customSchema("radiobuttons", "option"), want: "select", wantOK: true},
		{name: "custom multiselect", fieldKey: "customfield_5", schema: customSchema("multiselect", "array"), want: "multiselect", wantOK: true},
		{name: "custom multicheckboxes keeps legacy name", fieldKey: "customfield_6", schema: customSchema("multicheckboxes", "array"), want: "multicheckboxes", wantOK: true},
		{name: "custom labels", fieldKey: "customfield_7", schema: customSchema("labels", "array"), want: "array", wantOK: true},
		{name: "custom float", fieldKey: "customfield_8", schema: customSchema("float", "number"), want: "number", wantOK: true},
		{name: "custom datepicker", fieldKey: "customfield_9", schema: customSchema("datepicker", "date"), want: "datepicker", wantOK: true},
		{name: "unknown custom falls back to schema shape", fieldKey: "customfield_10", schema: customSchema("some-plugin-widget", "string"), want: "string", wantOK: true},
		{name: "user picker omitted", fieldKey: "customfield_11", schema: customSchema("userpicker", "user"), wantOK: false},
		{name: "multi user picker omitted", fieldKey: "customfield_12", schema: map[string]interface{}{"custom": "com.atlassian.jira.plugin.system.customfieldtypes:multiuserpicker", "type": "array", "items": "user"}, wantOK: false},
		{name: "cascading select omitted", fieldKey: "customfield_13", schema: customSchema("cascadingselect", "option-with-child"), wantOK: false},
		{name: "sprint omitted", fieldKey: "customfield_14", schema: map[string]interface{}{"custom": "com.pyxis.greenhopper.jira:gh-sprint", "type": "array", "items": "json"}, wantOK: false},
		{name: "epic link omitted", fieldKey: "customfield_15", schema: map[string]interface{}{"custom": "com.pyxis.greenhopper.jira:gh-epic-link", "type": "any"}, wantOK: false},
		{name: "nil schema omitted", fieldKey: "customfield_16", schema: nil, wantOK: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := normalizeJiraFieldType(tc.fieldKey, tc.schema)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if ok && got != tc.want {
				t.Errorf("type = %q, want %q", got, tc.want)
			}
		})
	}
}

// metaFixture builds a create-meta payload and returns it in BOTH shapes the
// runtime sees: typed (live fetch) and generic maps (Redis cache hit).
func metaFixture(t *testing.T) (typed interface{}, cached interface{}) {
	t.Helper()
	typed = map[string]interface{}{
		"data": []Template{
			{
				Name: "Task",
				Fields: map[string]FieldInfo{
					"customfield_100": {Key: "customfield_100", Name: "Team", Type: "select"},
					"customfield_101": {Key: "customfield_101", Name: "Options", Type: "multiselect"},
					"customfield_102": {Key: "customfield_102", Name: "Score", Type: "number"},
					"labels":          {Key: "labels", Name: "Labels", Type: "array"},
					"priority":        {Key: "priority", Name: "Priority", Type: "select", Group: FieldGroupSeverity},
				},
			},
		},
	}
	blob, err := json.Marshal(typed)
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	if err := json.Unmarshal(blob, &cached); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}
	return typed, cached
}

func TestFieldsForIssueType_BothMetaShapes(t *testing.T) {
	typed, cached := metaFixture(t)
	for name, meta := range map[string]interface{}{"typed": typed, "cached": cached} {
		t.Run(name, func(t *testing.T) {
			fields := FieldsForIssueType(meta, "Task")
			if fields == nil {
				t.Fatal("fields = nil, want Task fields")
			}
			if fields["customfield_100"].Type != "select" {
				t.Errorf("customfield_100 type = %q, want select", fields["customfield_100"].Type)
			}
		})
	}
	if got := FieldsForIssueType(typed, "Story"); got != nil {
		t.Errorf("unknown issue type: got %v, want nil", got)
	}
	if got := FieldsForIssueType(nil, "Task"); got != nil {
		t.Errorf("nil meta: got %v, want nil", got)
	}
}

func TestEncodeJiraAdditionalFields(t *testing.T) {
	fields := map[string]FieldInfo{
		"customfield_100": {Key: "customfield_100", Type: "select"},
		"customfield_101": {Key: "customfield_101", Type: "multiselect"},
		"customfield_102": {Key: "customfield_102", Type: "number"},
		"customfield_103": {Key: "customfield_103", Type: "multicheckboxes"},
		"customfield_104": {Key: "customfield_104", Type: "array"},
	}

	cases := []struct {
		name string
		in   map[string]interface{}
		want map[string]interface{}
	}{
		{
			name: "raw select value gets wrapped",
			in:   map[string]interface{}{"customfield_100": "10023"},
			want: map[string]interface{}{"customfield_100": map[string]interface{}{"id": "10023"}},
		},
		{
			name: "already-encoded select value is idempotent",
			in:   map[string]interface{}{"customfield_100": map[string]interface{}{"id": "10023"}},
			want: map[string]interface{}{"customfield_100": map[string]interface{}{"id": "10023"}},
		},
		{
			name: "multiselect raw ids get wrapped",
			in:   map[string]interface{}{"customfield_101": []interface{}{"1", "2"}},
			want: map[string]interface{}{"customfield_101": []interface{}{
				map[string]interface{}{"id": "1"}, map[string]interface{}{"id": "2"},
			}},
		},
		{
			name: "multicheckboxes legacy {id} elements are idempotent",
			in:   map[string]interface{}{"customfield_103": []interface{}{map[string]interface{}{"id": "7"}}},
			want: map[string]interface{}{"customfield_103": []interface{}{map[string]interface{}{"id": "7"}}},
		},
		{
			name: "custom labels field unwraps legacy maps to bare strings",
			in:   map[string]interface{}{"customfield_104": []interface{}{"alpha", map[string]interface{}{"value": "beta"}}},
			want: map[string]interface{}{"customfield_104": []interface{}{"alpha", "beta"}},
		},
		{
			name: "system labels is a basic key and passes through untouched",
			in:   map[string]interface{}{"labels": []interface{}{"alpha"}},
			want: map[string]interface{}{"labels": []interface{}{"alpha"}},
		},
		{
			name: "number parses numeric strings",
			in:   map[string]interface{}{"customfield_102": "1.5"},
			want: map[string]interface{}{"customfield_102": 1.5},
		},
		{
			name: "unknown keys pass through",
			in:   map[string]interface{}{"customfield_999": "free text"},
			want: map[string]interface{}{"customfield_999": "free text"},
		},
		{
			name: "basic-field keys pass through for downstream rules",
			in:   map[string]interface{}{"priority": "3", "parent": "KAN-5"},
			want: map[string]interface{}{"priority": "3", "parent": "KAN-5"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := EncodeJiraAdditionalFields(fields, tc.in)
			gotJSON, _ := json.Marshal(got)
			wantJSON, _ := json.Marshal(tc.want)
			if string(gotJSON) != string(wantJSON) {
				t.Errorf("encoded = %s, want %s", gotJSON, wantJSON)
			}
		})
	}

	t.Run("nil meta passes everything through", func(t *testing.T) {
		in := map[string]interface{}{"customfield_100": "10023"}
		got := EncodeJiraAdditionalFields(nil, in)
		if got["customfield_100"] != "10023" {
			t.Errorf("got %v, want raw passthrough", got["customfield_100"])
		}
	})
}

// wireJSON marshals the exact payload go-jira would send, so assertions cover
// Unknowns hoisting, not just struct state.
func wireJSON(t *testing.T, fields *jira.IssueFields) string {
	t.Helper()
	blob, err := json.Marshal(jira.Issue{Fields: fields})
	if err != nil {
		t.Fatalf("marshal issue: %v", err)
	}
	return string(blob)
}

func noAssignee(string) *jira.User { return nil }

func acceptParent(string) error { return nil }

func TestBuildJiraIssueFields_WireShapes(t *testing.T) {
	baseTicket := func(issueType string) models.Ticket {
		return models.Ticket{
			Title:       "title",
			Description: "desc",
			TicketType:  issueType,
			ProjectKey:  "KAN",
			Source:      "ui",
		}
	}

	t.Run("priority in both severity and additional_fields marshals as one object", func(t *testing.T) {
		ticket := baseTicket("Task")
		ticket.Severity = "3"
		fields, err := buildJiraIssueFields(ticket, map[string]interface{}{"priority": "3"}, noAssignee, acceptParent)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		wire := wireJSON(t, fields)
		if !strings.Contains(wire, `"priority":{"id":"3"}`) {
			t.Errorf("wire payload missing priority object: %s", wire)
		}
		if strings.Contains(wire, `"priority":"3"`) {
			t.Errorf("raw priority string leaked into wire payload: %s", wire)
		}
	})

	t.Run("legacy additional_fields priority without severity becomes the typed slot", func(t *testing.T) {
		fields, err := buildJiraIssueFields(baseTicket("Task"), map[string]interface{}{"priority": map[string]interface{}{"id": "2"}}, noAssignee, acceptParent)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if wire := wireJSON(t, fields); !strings.Contains(wire, `"priority":{"id":"2"}`) {
			t.Errorf("wire payload missing priority object: %s", wire)
		}
	})

	t.Run("parent encodes as object for hyphenated Sub-task type", func(t *testing.T) {
		fields, err := buildJiraIssueFields(baseTicket("Sub-task"), map[string]interface{}{"parent": "KAN-5"}, noAssignee, acceptParent)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		wire := wireJSON(t, fields)
		if !strings.Contains(wire, `"parent":{"key":"KAN-5"}`) {
			t.Errorf("wire payload missing parent object: %s", wire)
		}
		if strings.Contains(wire, `"parent":"KAN-5"`) {
			t.Errorf("raw parent string leaked into wire payload: %s", wire)
		}
	})

	t.Run("parent validation failure aborts the build", func(t *testing.T) {
		reject := func(key string) error { return fmt.Errorf("parent issue %s is a sub-task and cannot be a parent", key) }
		if _, err := buildJiraIssueFields(baseTicket("Subtask"), map[string]interface{}{"parent": "KAN-9"}, noAssignee, reject); err == nil {
			t.Fatal("expected parent validation error, got nil")
		}
	})

	t.Run("empty parent value is skipped, not fatal", func(t *testing.T) {
		fields, err := buildJiraIssueFields(baseTicket("Subtask"), map[string]interface{}{"parent": ""}, noAssignee, acceptParent)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if fields.Parent != nil {
			t.Errorf("parent = %+v, want nil", fields.Parent)
		}
	})

	t.Run("labels merge with defaults and assignee never reaches Unknowns", func(t *testing.T) {
		af := map[string]interface{}{
			"labels":   []interface{}{"custom-label"},
			"assignee": "5b10ac8d82e05b22cc7d4ef5",
		}
		fields, err := buildJiraIssueFields(baseTicket("Task"), af, noAssignee, acceptParent)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		wire := wireJSON(t, fields)
		if !strings.Contains(wire, `"labels":["nudgebee","ui","custom-label"]`) {
			t.Errorf("wire payload labels wrong: %s", wire)
		}
		if strings.Contains(wire, `"assignee":"5b10ac8d82e05b22cc7d4ef5"`) {
			t.Errorf("raw assignee string leaked into wire payload: %s", wire)
		}
	})

	t.Run("assignee inside additional_fields resolves through the resolver", func(t *testing.T) {
		resolver := func(input string) *jira.User {
			if input == "" {
				return nil
			}
			return &jira.User{AccountID: input}
		}
		fields, err := buildJiraIssueFields(baseTicket("Task"), map[string]interface{}{"assignee": "557058:abc"}, resolver, acceptParent)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if fields.Assignee == nil || fields.Assignee.AccountID != "557058:abc" {
			t.Errorf("assignee = %+v, want AccountID 557058:abc", fields.Assignee)
		}
	})

	t.Run("encoded custom fields survive into Unknowns unchanged", func(t *testing.T) {
		af := map[string]interface{}{"customfield_100": map[string]interface{}{"id": "10023"}}
		fields, err := buildJiraIssueFields(baseTicket("Task"), af, noAssignee, acceptParent)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if wire := wireJSON(t, fields); !strings.Contains(wire, `"customfield_100":{"id":"10023"}`) {
			t.Errorf("wire payload missing custom field: %s", wire)
		}
	})
}

func TestJiraCreateError(t *testing.T) {
	jerr := &jira.Error{
		HTTPError:     fmt.Errorf("request failed. Status code: 400"),
		ErrorMessages: []string{"Field 'priority' is required"},
		Errors:        map[string]string{"components": "Components is not valid", "assignee": "User does not exist"},
	}
	got := jiraCreateError(jerr).Error()
	want := "jira rejected the create request: Field 'priority' is required; assignee: User does not exist; components: Components is not valid"
	if got != want {
		t.Errorf("error = %q, want %q", got, want)
	}

	plain := fmt.Errorf("dial tcp: timeout")
	if got := jiraCreateError(plain); got != plain {
		t.Errorf("non-jira error should pass through, got %v", got)
	}

	empty := &jira.Error{HTTPError: fmt.Errorf("request failed. Status code: 400")}
	if got := jiraCreateError(empty); got != empty {
		t.Errorf("detail-less jira error should pass through, got %v", got)
	}
}
