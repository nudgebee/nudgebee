package main

import (
	"testing"
)

func TestIssueDelete(t *testing.T) {
	issueID := "I_kwDOIJg5ds6MN8al"
	err := closeIssue(issueID)
	if err != nil {
		t.Errorf("Error in DELETING issues: %v", err)
	}
}

func TestListProjectIssues(t *testing.T) {
	err := ProcessProjectIssues()
	if err != nil {
		t.Errorf("Error in processing project issues: %v", err)
	}
}

func TestGetIssueIDs(t *testing.T) {
	issueIDs, err := getIssueIDs(sourceProjectID)
	if err != nil {
		t.Errorf("Error in getting issue IDs: %v", err)
	}
	if len(issueIDs) == 0 {
		t.Errorf("No issue IDs found")
	}
}
