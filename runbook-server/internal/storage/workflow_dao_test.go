package storage

import (
	"context"
	"testing"

	"nudgebee/runbook/internal/model"

	"github.com/stretchr/testify/assert"
)

func TestBuildOptimizationFilter_Empty(t *testing.T) {
	assert.Equal(t, "", buildOptimizationFilter(""))
}

func TestBuildOptimizationFilter_InvalidJSON(t *testing.T) {
	assert.Equal(t, "", buildOptimizationFilter("{invalid"))
}

func TestBuildOptimizationFilter_EmptyParams(t *testing.T) {
	assert.Equal(t, "", buildOptimizationFilter("{}"))
}

func TestBuildOptimizationFilter_SingleCategory(t *testing.T) {
	input := `{"categories":["PodRightSizing"]}`
	expected := "{{ event.category in ['PodRightSizing'] }}"
	assert.Equal(t, expected, buildOptimizationFilter(input))
}

func TestBuildOptimizationFilter_MultipleCategories(t *testing.T) {
	input := `{"categories":["PodRightSizing","Security"]}`
	expected := "{{ event.category in ['PodRightSizing', 'Security'] }}"
	assert.Equal(t, expected, buildOptimizationFilter(input))
}

func TestBuildOptimizationFilter_SingleRuleName(t *testing.T) {
	input := `{"rule_names":["vertical_rightsize"]}`
	expected := "{{ event.rule_name in ['vertical_rightsize'] }}"
	assert.Equal(t, expected, buildOptimizationFilter(input))
}

func TestBuildOptimizationFilter_SingleCluster(t *testing.T) {
	input := `{"clusters":["a2a30b02-0f67-42e5-a2ab-c658230fd798"]}`
	expected := "{{ event.cloud_account_id in ['a2a30b02-0f67-42e5-a2ab-c658230fd798'] }}"
	assert.Equal(t, expected, buildOptimizationFilter(input))
}

func TestBuildOptimizationFilter_MultipleClusters(t *testing.T) {
	input := `{"clusters":["id-1","id-2"]}`
	expected := "{{ event.cloud_account_id in ['id-1', 'id-2'] }}"
	assert.Equal(t, expected, buildOptimizationFilter(input))
}

func TestBuildOptimizationFilter_CombinedParams(t *testing.T) {
	input := `{"categories":["PodRightSizing"],"rule_names":["vertical_rightsize"]}`
	expected := "{{ event.category in ['PodRightSizing'] and event.rule_name in ['vertical_rightsize'] }}"
	assert.Equal(t, expected, buildOptimizationFilter(input))
}

func TestBuildOptimizationFilter_AllThreeParams(t *testing.T) {
	input := `{"categories":["PodRightSizing"],"rule_names":["vertical_rightsize"],"clusters":["acct-1"]}`
	expected := "{{ event.category in ['PodRightSizing'] and event.rule_name in ['vertical_rightsize'] and event.cloud_account_id in ['acct-1'] }}"
	assert.Equal(t, expected, buildOptimizationFilter(input))
}

func TestBuildOptimizationFilter_ExplicitFilter(t *testing.T) {
	input := `{"filter":"{{ event.severity == 'high' }}"}`
	expected := "{{ (event.severity == 'high') }}"
	assert.Equal(t, expected, buildOptimizationFilter(input))
}

func TestBuildOptimizationFilter_CombinedWithExplicitFilter(t *testing.T) {
	input := `{"categories":["PodRightSizing"],"filter":"{{ event.severity == 'high' }}"}`
	expected := "{{ event.category in ['PodRightSizing'] and (event.severity == 'high') }}"
	assert.Equal(t, expected, buildOptimizationFilter(input))
}

func TestBuildOptimizationFilter_ExplicitFilterWithOrPrecedence(t *testing.T) {
	input := `{"categories":["PodRightSizing"],"filter":"{{ event.severity == 'high' or event.severity == 'critical' }}"}`
	expected := "{{ event.category in ['PodRightSizing'] and (event.severity == 'high' or event.severity == 'critical') }}"
	assert.Equal(t, expected, buildOptimizationFilter(input))
}

func TestBuildOptimizationFilter_EmptyArrayIgnored(t *testing.T) {
	input := `{"categories":[],"rule_names":["vertical_rightsize"]}`
	expected := "{{ event.rule_name in ['vertical_rightsize'] }}"
	assert.Equal(t, expected, buildOptimizationFilter(input))
}

func TestBuildOptimizationFilter_EmptyFilterString(t *testing.T) {
	input := `{"filter":""}`
	assert.Equal(t, "", buildOptimizationFilter(input))
}

func TestBuildOptimizationFilter_EscapesSingleQuotes(t *testing.T) {
	input := `{"categories":["it's a test","normal"]}`
	expected := "{{ event.category in ['it\\'s a test', 'normal'] }}"
	assert.Equal(t, expected, buildOptimizationFilter(input))
}

// List and CountWorkflows now take a set of accounts (the Automations listing is
// tenant-level with an account filter). An empty set must fail loudly: it would
// otherwise become `account_id = ANY('{}')`, which matches nothing but still
// costs a scan, and would report "no automations" for what is really a bug in
// the caller's scope resolution.
func TestWorkflowDaoList_RejectsEmptyScope(t *testing.T) {
	dao := &WorkflowDao{}

	_, _, err := dao.List(context.Background(), "t1", nil, model.ListWorkflowRequest{})
	assert.Error(t, err)

	_, _, err = dao.List(context.Background(), "", []string{"acct-1"}, model.ListWorkflowRequest{})
	assert.Error(t, err)
}

func TestWorkflowDaoCountWorkflows_RejectsEmptyScope(t *testing.T) {
	dao := &WorkflowDao{}

	_, err := dao.CountWorkflows(context.Background(), "t1", nil, "", "")
	assert.Error(t, err)

	_, err = dao.CountWorkflows(context.Background(), "", []string{"acct-1"}, "", "")
	assert.Error(t, err)
}

// A dry-run execution stamps "dry-run-<uuid>" into the nb_workflow_id search
// attribute, so the ids reaching GetWorkflowNames are not all UUIDs. Passing one
// through to `id = ANY($3::uuid[])` failed the whole statement with "invalid
// input syntax for type uuid", which blanked the automation name on every row of
// the executions dashboard. They must be dropped before the query runs — the nil
// db here is the assertion: reaching QueryContext would panic.
func TestWorkflowDaoGetWorkflowNames_DropsNonUUIDIDs(t *testing.T) {
	dao := &WorkflowDao{}

	names, err := dao.GetWorkflowNames(context.Background(), "t1", []string{"acct-1"},
		[]string{"dry-run-98091125-f8fb-4681-ad83-b86f7be4f370", "inline-group-1", ""})

	assert.NoError(t, err)
	assert.Empty(t, names)
}
