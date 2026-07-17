package tools

import (
	"encoding/json"
	"strconv"
	"strings"
)

// This file owns the Jira field contract with the create-ticket UIs:
//
//  1. normalizeJiraFieldType maps Jira's open-ended create-meta schemas onto
//     the closed vocabulary the UIs render: string, text, number, select,
//     multiselect, array (free strings), datetime, datepicker (multicheckboxes
//     is a legacy alias of multiselect). Unrenderable types are omitted rather
//     than emitted under a name no renderer understands.
//
//  2. EncodeJiraAdditionalFields converts collected values into Jira wire
//     shapes at create time using the same meta that drove rendering. Rules
//     are tolerant/idempotent — raw scalars and already-encoded {id} shapes
//     both accepted — so persisted configs and older app versions need no
//     migration, and a stale meta can never make a payload worse.

// normalizeJiraFieldType returns the vocabulary type for a create-meta field, or
// ok=false when the field cannot be safely rendered and encoded (user pickers,
// cascading selects, sprint/team fields, ...).
func normalizeJiraFieldType(fieldKey string, schema interface{}) (string, bool) {
	// parent is object-shaped on the wire but collected as a plain issue key;
	// buildJiraIssueFields turns it into jira.Parent{Key}.
	if fieldKey == "parent" {
		return "string", true
	}
	// assignee is user-shaped; buildJiraIssueFields resolves accountID/email
	// itself, so the UI treats it as a plain select over the seeded user list.
	if fieldKey == "assignee" {
		return "select", true
	}

	schemaMap, ok := schema.(map[string]interface{})
	if !ok {
		return "", false
	}

	if custom, ok := schemaMap["custom"].(string); ok {
		if vocabType, ok := normalizeJiraCustomType(custom); ok {
			return vocabType, true
		}
		// Unknown plugin types fall through to the base schema type: shape is a
		// safer classifier than an open-ended plugin name.
	}

	typeVal, _ := schemaMap["type"].(string)
	switch typeVal {
	case "string":
		return "string", true
	case "number":
		return "number", true
	case "date":
		return "datepicker", true
	case "datetime":
		return "datetime", true
	case "option", "priority", "securitylevel":
		return "select", true
	case "array":
		items, _ := schemaMap["items"].(string)
		switch items {
		case "string":
			return "array", true
		case "option", "component", "version":
			return "multiselect", true
		default: // user, group, attachment, issuelinks, json (sprint), ...
			return "", false
		}
	default: // user, group, option-with-child, any, team, timetracking, issuelink, ...
		return "", false
	}
}

// normalizeJiraCustomType maps the suffix of a Jira custom-field type
// ("com.atlassian.jira.plugin.system.customfieldtypes:<suffix>") onto the
// vocabulary. ok=false falls through to schema-shape classification.
func normalizeJiraCustomType(custom string) (string, bool) {
	parts := strings.Split(custom, ":")
	if len(parts) < 2 {
		return "", false
	}
	switch parts[1] {
	case "textfield", "url":
		return "string", true
	case "textarea":
		return "text", true
	case "select", "radiobuttons":
		return "select", true
	case "multiselect":
		return "multiselect", true
	case "multicheckboxes":
		return "multicheckboxes", true
	case "labels":
		return "array", true
	case "float":
		return "number", true
	case "datepicker":
		return "datepicker", true
	case "datetime":
		return "datetime", true
	default: // userpicker, multiuserpicker, grouppicker, cascadingselect, gh-*, ...
		return "", false
	}
}

// FieldsForIssueType extracts one issue type's field metadata from a create-meta
// payload. The payload arrives either typed (live fetch: map with []Template) or
// as generic maps (cache hit); a JSON round-trip tolerates both.
func FieldsForIssueType(meta interface{}, issueType string) map[string]FieldInfo {
	if meta == nil || issueType == "" {
		return nil
	}
	blob, err := json.Marshal(meta)
	if err != nil {
		return nil
	}
	var parsed struct {
		Data []Template `json:"data"`
	}
	if err := json.Unmarshal(blob, &parsed); err != nil {
		return nil
	}
	for _, template := range parsed.Data {
		if template.Name == issueType {
			return template.Fields
		}
	}
	return nil
}

// jiraBasicFieldKeys back typed jira.IssueFields slots; buildJiraIssueFields owns
// their normalization, so the encoder passes them through untouched.
var jiraBasicFieldKeys = map[string]bool{
	"parent":   true,
	"priority": true,
	"assignee": true,
	"labels":   true,
}

// EncodeJiraAdditionalFields returns a copy of additionalFields with every
// meta-known field normalized to its Jira wire shape. Unknown keys and a nil
// fields map pass through unchanged (buildJiraIssueFields' hard rules still
// protect the basic-field keys).
func EncodeJiraAdditionalFields(fields map[string]FieldInfo, additionalFields map[string]interface{}) map[string]interface{} {
	if additionalFields == nil {
		return nil
	}
	out := make(map[string]interface{}, len(additionalFields))
	for key, value := range additionalFields {
		if value == nil || jiraBasicFieldKeys[key] {
			out[key] = value
			continue
		}
		fieldInfo, known := fields[key]
		if !known {
			out[key] = value
			continue
		}
		switch fieldInfo.Type {
		case "select":
			out[key] = jiraOptionRef(value)
		case "multiselect", "multicheckboxes":
			out[key] = jiraOptionRefs(value)
		case "array":
			out[key] = jiraStringList(value)
		case "number":
			out[key] = jiraNumber(value)
		default: // string, text, datetime, datepicker: already wire-ready scalars
			out[key] = value
		}
	}
	return out
}

// jiraScalarString renders a scalar as the string Jira option ids use. ok=false
// for empty strings and non-scalar values.
func jiraScalarString(v interface{}) (string, bool) {
	switch s := v.(type) {
	case string:
		return s, s != ""
	case float64:
		return strconv.FormatFloat(s, 'f', -1, 64), true
	case int:
		return strconv.Itoa(s), true
	case int64:
		return strconv.FormatInt(s, 10), true
	case json.Number:
		return s.String(), true
	}
	return "", false
}

// jiraOptionIDString extracts an option id from either a raw scalar or an
// already-encoded {"id"|"value"|"key": ...} map.
func jiraOptionIDString(v interface{}) (string, bool) {
	if s, ok := jiraScalarString(v); ok {
		return s, true
	}
	if ref, ok := v.(map[string]interface{}); ok {
		for _, k := range []string{"id", "value", "key"} {
			if s, ok := jiraScalarString(ref[k]); ok {
				return s, true
			}
		}
	}
	return "", false
}

// jiraOptionRef normalizes one select value to {"id": ...}; unclassifiable
// values pass through unchanged.
func jiraOptionRef(v interface{}) interface{} {
	if id, ok := jiraOptionIDString(v); ok {
		return map[string]interface{}{"id": id}
	}
	return v
}

// jiraOptionRefs normalizes a multi-select value to [{"id": ...}, ...],
// accepting a bare scalar, a single encoded map, or a list of either.
func jiraOptionRefs(v interface{}) interface{} {
	items, ok := v.([]interface{})
	if !ok {
		if id, found := jiraOptionIDString(v); found {
			return []interface{}{map[string]interface{}{"id": id}}
		}
		return v
	}
	out := make([]interface{}, 0, len(items))
	for _, item := range items {
		out = append(out, jiraOptionRef(item))
	}
	return out
}

// jiraStringList normalizes a free-strings value (labels) to []string,
// unwrapping legacy {"value"|"name"|"id": ...} elements.
func jiraStringList(v interface{}) interface{} {
	switch items := v.(type) {
	case []interface{}:
		out := make([]string, 0, len(items))
		for _, item := range items {
			if s, ok := jiraScalarString(item); ok {
				out = append(out, s)
				continue
			}
			if ref, ok := item.(map[string]interface{}); ok {
				for _, k := range []string{"value", "name", "id"} {
					if s, ok := jiraScalarString(ref[k]); ok {
						out = append(out, s)
						break
					}
				}
			}
		}
		return out
	case string:
		return []string{items}
	}
	return v
}

// jiraNumber parses a number field's value, tolerating numeric strings; values
// that don't parse pass through so Jira reports them rather than us guessing.
func jiraNumber(v interface{}) interface{} {
	switch n := v.(type) {
	case float64, int, int64:
		return v
	case json.Number:
		if f, err := n.Float64(); err == nil {
			return f
		}
	case string:
		if f, err := strconv.ParseFloat(strings.TrimSpace(n), 64); err == nil {
			return f
		}
	}
	return v
}
