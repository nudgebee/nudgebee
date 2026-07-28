package models

import (
	"reflect"
	"testing"
)

func TestUpdateFieldsAssigneeList(t *testing.T) {
	cases := []struct {
		name   string
		fields UpdateFields
		want   []string
	}{
		{"none", UpdateFields{}, []string{}},
		{"legacy single", UpdateFields{Assignee: "alice"}, []string{"alice"}},
		{"legacy single trimmed", UpdateFields{Assignee: "  alice  "}, []string{"alice"}},
		{"multi", UpdateFields{Assignees: []string{"alice", "bob"}}, []string{"alice", "bob"}},
		{"multi trims and drops empties", UpdateFields{Assignees: []string{" alice ", "", "  ", "bob"}}, []string{"alice", "bob"}},
		{"assignees take precedence over assignee", UpdateFields{Assignee: "carol", Assignees: []string{"alice"}}, []string{"alice"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.fields.AssigneeList()
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("AssigneeList() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestUpdateFieldsHasAssignee(t *testing.T) {
	cases := []struct {
		name   string
		fields UpdateFields
		want   bool
	}{
		{"none", UpdateFields{}, false},
		{"whitespace only", UpdateFields{Assignee: "   "}, false},
		{"empty slice", UpdateFields{Assignees: []string{"", "  "}}, false},
		{"legacy single", UpdateFields{Assignee: "alice"}, true},
		{"multi", UpdateFields{Assignees: []string{"alice"}}, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.fields.HasAssignee(); got != tc.want {
				t.Errorf("HasAssignee() = %v, want %v", got, tc.want)
			}
		})
	}
}
