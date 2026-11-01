package core

import (
	"io"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
)

// The planner hands the container tool the raw JSON input as Command when it
// has no string "command" key; that JSON must not be run as a shell command.
func TestResolveContainerCommand(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	config := map[string]any{"image": "alpine:latest", "command": []any{`/bin/sh -c "df -h | awk '$5+0 > 85 {print}'"`}}
	configured := `/bin/sh -c "df -h | awk '$5+0 > 85 {print}'"`

	tests := []struct {
		name   string
		input  NBToolCallRequest
		config map[string]any
		want   string
	}{
		{name: "empty JSON object uses configured command", input: NBToolCallRequest{Command: "{}"}, config: config, want: configured},
		{name: "padded empty JSON object uses configured command", input: NBToolCallRequest{Command: " {} \n"}, config: config, want: configured},
		{name: "JSON with empty string command uses configured command", input: NBToolCallRequest{Command: `{"command":""}`, Arguments: map[string]any{"command": ""}}, config: config, want: configured},
		{name: "JSON with only image override uses configured command", input: NBToolCallRequest{Command: `{"image":"busybox"}`, Arguments: map[string]any{"image": "busybox"}}, config: config, want: configured},
		{name: "JSON with array command override", input: NBToolCallRequest{Command: `{"command":["df","-h"]}`, Arguments: map[string]any{"command": []any{"df", "-h"}}}, config: config, want: "df -h"},
		{name: "JSON with empty array command uses configured command", input: NBToolCallRequest{Command: `{"command":[]}`}, config: config, want: configured},
		{name: "string command from planner", input: NBToolCallRequest{Command: "uname -a"}, config: config, want: "uname -a"},
		{name: "shell group command is not mistaken for JSON", input: NBToolCallRequest{Command: "{ df -h; }"}, config: config, want: "{ df -h; }"},
		{name: "empty JSON object with no configured command", input: NBToolCallRequest{Command: "{}"}, config: map[string]any{"image": "alpine:latest", "command": []any{}}, want: ""},
		{name: "configured command as []string", input: NBToolCallRequest{}, config: map[string]any{"command": []string{"printenv"}}, want: "printenv"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveContainerCommand(log, normalizeContainerCallRequest(tt.input), tt.config)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestNormalizeContainerCallRequest_ImageOverride(t *testing.T) {
	got := normalizeContainerCallRequest(NBToolCallRequest{Command: `{"image":"busybox","env_vars":{"A":"1"}}`, Context: "ctx"})
	assert.Equal(t, "", got.Command)
	assert.Equal(t, "busybox", got.Arguments["image"])
	assert.Equal(t, map[string]any{"A": "1"}, got.Arguments["env_vars"])
	assert.Equal(t, "ctx", got.Context)
}
