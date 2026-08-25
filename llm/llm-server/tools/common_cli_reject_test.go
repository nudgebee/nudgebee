package tools

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAwsCliToolDescription_CleanFormat(t *testing.T) {
	desc := AwsCliTool{}.Description()
	assert.NotContains(t, strings.ToLower(desc), "use tools like 'jq' to parse",
		"the old advice contradicts the single-command rule above it")
	assert.NotContains(t, strings.ToLower(desc), "for pipes, loops, 'jq'",
		"pipes and jq are valid in workspace mode")
}
