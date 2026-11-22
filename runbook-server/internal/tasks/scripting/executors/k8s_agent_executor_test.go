package executors

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAgentScriptResult(t *testing.T) {
	out, err := agentScriptResult(`{"stdout":"half done\n","exit_code":1}`)
	assert.ErrorContains(t, err, "script exited 1: half done")
	assert.Equal(t, "half done", out)

	out, err = agentScriptResult(`{"stdout":" ok \n","exit_code":0}`)
	assert.NoError(t, err)
	assert.Equal(t, "ok", out)

	out, err = agentScriptResult(`{"stdout":" ok \n"}`)
	assert.NoError(t, err, "older agents without exit_code keep today's behaviour")
	assert.Equal(t, "ok", out)

	_, err = agentScriptResult(`not json`)
	assert.ErrorContains(t, err, "agent execution failed: not json")
}
