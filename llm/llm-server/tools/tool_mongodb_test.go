package tools

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"
)

func TestParseProxyMongoResponse(t *testing.T) {
	resp, err := parseProxyMongoResponse(map[string]any{
		"data": `{"ok":1,"connections":2}`,
	})
	require.NoError(t, err)
	assert.Contains(t, resp, "\n")
	assert.Contains(t, resp, `"connections": 2`)
}

func TestParseProxyMongoResponseMissingData(t *testing.T) {
	_, err := parseProxyMongoResponse(map[string]any{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing 'data' field")
}

func TestMongoToolInputSchema(t *testing.T) {
	schema := MongoExecuteTool{}.InputSchema()
	assert.Equal(t, core.ToolSchemaTypeObject, schema.Type)
	if _, ok := schema.Properties["instance"]; !ok {
		t.Fatalf("expected instance property")
	}
}

func TestExtractMongoInstance(t *testing.T) {
	instance := extractMongoInstance(core.NBToolCallRequest{
		Arguments: map[string]any{"instance": "prod-mongo"},
	})
	assert.Equal(t, "prod-mongo", instance)

	instance = extractMongoInstance(core.NBToolCallRequest{
		Command: `{"instance":"dev-mongo"}`,
	})
	assert.Equal(t, "dev-mongo", instance)
}

func TestMongoIdentifyConfig(t *testing.T) {
	tool := MongoExecuteTool{}
	ctx := core.NbToolContext{
		Query: "check mongo status in prod",
		Ctx:   security.NewRequestContextForSuperAdmin(),
	}

	configs := []core.ToolConfig{
		{
			Name:   "dev-mongo",
			Values: []core.ToolConfigValue{{Name: "host", Value: "dev-mongo.*"}},
			Tags:   map[string]string{"environment": "dev"},
		},
		{
			Name:   "prod-mongo",
			Values: []core.ToolConfigValue{{Name: "host", Value: "prod-mongo.*"}},
			Tags:   map[string]string{"environment": "prod"},
		},
	}

	selected, err := tool.IdentifyConfig(ctx, core.NBToolCallRequest{Arguments: map[string]any{"instance": "prod-mongo"}}, configs)
	require.NoError(t, err)
	assert.Equal(t, "prod-mongo", selected.Name)

	selected, err = tool.IdentifyConfig(ctx, core.NBToolCallRequest{}, configs)
	require.NoError(t, err)
	assert.Equal(t, "prod-mongo", selected.Name)
}
