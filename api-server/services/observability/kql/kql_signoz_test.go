package kql

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestKqlSignozConverter_Translate_Where(t *testing.T) {
	kqlQuery := `search "error"`
	ast, err := Parse(kqlQuery)
	assert.NoError(t, err)

	converter := &KqlSignozConverter{}
	jsonQuery, err := converter.Translate(*ast)
	assert.NoError(t, err)

	var actualMap, expectedMap map[string]interface{}
	expected := `{"queryType":"list","filters":[{"key":"body","op":"contains","value":"error"}],"aggregate":{"functions":[],"groupBy":[]}}`

	// Unmarshal and then marshal again to normalize JSON string for comparison
	err = json.Unmarshal([]byte(jsonQuery), &actualMap)
	assert.NoError(t, err)
	err = json.Unmarshal([]byte(expected), &expectedMap)
	assert.NoError(t, err)

	assert.Equal(t, expectedMap, actualMap)
}

func TestKqlSignozConverter_Translate_SummarizeCount(t *testing.T) {
	kqlQuery := `logs | summarize count() by serviceName`
	ast, err := Parse(kqlQuery)
	assert.NoError(t, err)

	converter := &KqlSignozConverter{}
	jsonQuery, err := converter.Translate(*ast)
	assert.NoError(t, err)

	var actualMap, expectedMap map[string]interface{}
	expected := `{"queryType":"aggregate","filters":[{"key":"serviceName","op":"=","value":"logs"}],"aggregate":{"functions":[{"name":"count","key":"body"}],"groupBy":["serviceName"]}}`

	// Unmarshal and then marshal again to normalize JSON string for comparison
	err = json.Unmarshal([]byte(jsonQuery), &actualMap)
	assert.NoError(t, err)
	err = json.Unmarshal([]byte(expected), &expectedMap)
	assert.NoError(t, err)

	assert.Equal(t, expectedMap, actualMap)
}

func TestKqlSignozConverter_Translate_AndCondition(t *testing.T) {
	kqlQuery := `logs | where level == "error" and service == "auth"`
	ast, err := Parse(kqlQuery)
	assert.NoError(t, err)

	converter := &KqlSignozConverter{}
	jsonQuery, err := converter.Translate(*ast)
	assert.NoError(t, err)

	var actualMap, expectedMap map[string]interface{}
	expected := `{"queryType":"list","filters":[{"key":"serviceName","op":"=","value":"logs"},{"logicalOp":"AND","items":[{"key":"level","op":"=","value":"error"},{"key":"service","op":"=","value":"auth"}]}],"aggregate":{"functions":[],"groupBy":[]}}`

	// Unmarshal and then marshal again to normalize JSON string for comparison
	err = json.Unmarshal([]byte(jsonQuery), &actualMap)
	assert.NoError(t, err)
	err = json.Unmarshal([]byte(expected), &expectedMap)
	assert.NoError(t, err)

	assert.Equal(t, expectedMap, actualMap)
}

func TestKqlSignozConverter_Translate_OrCondition(t *testing.T) {
	kqlQuery := `logs | where level == "error" or level == "warn"`
	ast, err := Parse(kqlQuery)
	assert.NoError(t, err)

	converter := &KqlSignozConverter{}
	jsonQuery, err := converter.Translate(*ast)
	assert.NoError(t, err)

	var actualMap, expectedMap map[string]interface{}
	expected := `{"queryType":"list","filters":[{"key":"serviceName","op":"=","value":"logs"},{"logicalOp":"OR","items":[{"key":"level","op":"=","value":"error"},{"key":"level","op":"=","value":"warn"}]}],"aggregate":{"functions":[],"groupBy":[]}}`

	// Unmarshal and then marshal again to normalize JSON string for comparison
	err = json.Unmarshal([]byte(jsonQuery), &actualMap)
	assert.NoError(t, err)
	err = json.Unmarshal([]byte(expected), &expectedMap)
	assert.NoError(t, err)

	assert.Equal(t, expectedMap, actualMap)
}

func TestKqlSignozConverter_Translate_ComplexCondition(t *testing.T) {
	kqlQuery := `logs | where (level == "error" and service == "auth") or (level == "warn" and service == "payment")`
	ast, err := Parse(kqlQuery)
	assert.NoError(t, err)

	converter := &KqlSignozConverter{}
	jsonQuery, err := converter.Translate(*ast)
	assert.NoError(t, err)

	var actualMap, expectedMap map[string]interface{}
	expected := `{"queryType":"list","filters":[{"key":"serviceName","op":"=","value":"logs"},{"logicalOp":"OR","items":[{"logicalOp":"AND","items":[{"key":"level","op":"=","value":"error"},{"key":"service","op":"=","value":"auth"}]},{"logicalOp":"AND","items":[{"key":"level","op":"=","value":"warn"},{"key":"service","op":"=","value":"payment"}]}]}],"aggregate":{"functions":[],"groupBy":[]}}`

	// Unmarshal and then marshal again to normalize JSON string for comparison
	err = json.Unmarshal([]byte(jsonQuery), &actualMap)
	assert.NoError(t, err)
	err = json.Unmarshal([]byte(expected), &expectedMap)
	assert.NoError(t, err)

	assert.Equal(t, expectedMap, actualMap)
}

func TestKqlSignozConverter_Translate_StartsWith(t *testing.T) {
	kqlQuery := `logs | where message startswith "Error"`
	ast, err := Parse(kqlQuery)
	assert.NoError(t, err)

	converter := &KqlSignozConverter{}
	jsonQuery, err := converter.Translate(*ast)
	assert.NoError(t, err)

	var actualMap, expectedMap map[string]interface{}
	expected := `{"queryType":"list","filters":[{"key":"serviceName","op":"=","value":"logs"},{"key":"message","op":"regex","value":"^Error"}],"aggregate":{"functions":[],"groupBy":[]}}`

	err = json.Unmarshal([]byte(jsonQuery), &actualMap)
	assert.NoError(t, err)
	err = json.Unmarshal([]byte(expected), &expectedMap)
	assert.NoError(t, err)

	assert.Equal(t, expectedMap, actualMap)
}

func TestKqlSignozConverter_Translate_NotStartsWith(t *testing.T) {
	kqlQuery := `logs | where message !startswith "Error"`
	ast, err := Parse(kqlQuery)
	assert.NoError(t, err)

	converter := &KqlSignozConverter{}
	jsonQuery, err := converter.Translate(*ast)
	assert.NoError(t, err)

	var actualMap, expectedMap map[string]interface{}
	expected := `{"queryType":"list","filters":[{"key":"serviceName","op":"=","value":"logs"},{"key":"message","op":"notRegex","value":"^Error"}],"aggregate":{"functions":[],"groupBy":[]}}`

	err = json.Unmarshal([]byte(jsonQuery), &actualMap)
	assert.NoError(t, err)
	err = json.Unmarshal([]byte(expected), &expectedMap)
	assert.NoError(t, err)

	assert.Equal(t, expectedMap, actualMap)
}

func TestKqlSignozConverter_Translate_EndsWith(t *testing.T) {
	kqlQuery := `logs | where message endswith "completed"`
	ast, err := Parse(kqlQuery)
	assert.NoError(t, err)

	converter := &KqlSignozConverter{}
	jsonQuery, err := converter.Translate(*ast)
	assert.NoError(t, err)

	var actualMap, expectedMap map[string]interface{}
	expected := `{"queryType":"list","filters":[{"key":"serviceName","op":"=","value":"logs"},{"key":"message","op":"regex","value":"completed$"}],"aggregate":{"functions":[],"groupBy":[]}}`

	err = json.Unmarshal([]byte(jsonQuery), &actualMap)
	assert.NoError(t, err)
	err = json.Unmarshal([]byte(expected), &expectedMap)
	assert.NoError(t, err)

	assert.Equal(t, expectedMap, actualMap)
}

func TestKqlSignozConverter_Translate_NotEndsWith(t *testing.T) {
	kqlQuery := `logs | where message !endswith "completed"`
	ast, err := Parse(kqlQuery)
	assert.NoError(t, err)

	converter := &KqlSignozConverter{}
	jsonQuery, err := converter.Translate(*ast)
	assert.NoError(t, err)

	var actualMap, expectedMap map[string]interface{}
	expected := `{"queryType":"list","filters":[{"key":"serviceName","op":"=","value":"logs"},{"key":"message","op":"notRegex","value":"completed$"}],"aggregate":{"functions":[],"groupBy":[]}}`

	err = json.Unmarshal([]byte(jsonQuery), &actualMap)
	assert.NoError(t, err)
	err = json.Unmarshal([]byte(expected), &expectedMap)
	assert.NoError(t, err)

	assert.Equal(t, expectedMap, actualMap)
}

func TestKqlSignozConverter_Translate_ContainsCS(t *testing.T) {
	kqlQuery := `logs | where message contains_cs "SensitiveData"`
	ast, err := Parse(kqlQuery)
	assert.NoError(t, err)

	converter := &KqlSignozConverter{}
	jsonQuery, err := converter.Translate(*ast)
	assert.NoError(t, err)

	var actualMap, expectedMap map[string]interface{}
	expected := `{"queryType":"list","filters":[{"key":"serviceName","op":"=","value":"logs"},{"key":"message","op":"regex","value":"SensitiveData"}],"aggregate":{"functions":[],"groupBy":[]}}`

	err = json.Unmarshal([]byte(jsonQuery), &actualMap)
	assert.NoError(t, err)
	err = json.Unmarshal([]byte(expected), &expectedMap)
	assert.NoError(t, err)

	assert.Equal(t, expectedMap, actualMap)
}
