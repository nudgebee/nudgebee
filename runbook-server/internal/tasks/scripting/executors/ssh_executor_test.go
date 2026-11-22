package executors

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSSHExecutorTargetConfigs(t *testing.T) {
	assert.Equal(t, map[string]any{}, sshTargetConfigs(ExecutionConfig{}))
	assert.Equal(t,
		map[string]any{"host": "192.168.0.10", "port": 2222},
		sshTargetConfigs(ExecutionConfig{Host: "192.168.0.10", Port: 2222}))
}

func TestSSHScriptResult(t *testing.T) {
	out, err := sshScriptResult(`{"stdout":"partial","stderr":"boom","exit_code":2}`)
	assert.ErrorContains(t, err, "script exited 2")
	assert.Equal(t, "partial\nboom", out)

	out, err = sshScriptResult(`{"stdout":"ok","exit_code":0}`)
	assert.NoError(t, err)
	assert.Equal(t, "ok", out)

	out, err = sshScriptResult(`{"stdout":"ok","stderr":"note"}`)
	assert.NoError(t, err, "older agents without exit_code keep today's behaviour")
	assert.Equal(t, "ok\nnote", out)

	out, err = sshScriptResult(`not json`)
	assert.NoError(t, err)
	assert.Equal(t, "not json", out)
}
