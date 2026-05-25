package storage

import (
	"testing"

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
