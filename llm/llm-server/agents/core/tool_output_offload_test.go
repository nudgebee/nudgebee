package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"nudgebee/llm/config"
	"nudgebee/llm/security"
	toolcore "nudgebee/llm/tools/core"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type savedToolOutput struct {
	accountId, conversationId, filename, content string
}

// stubToolOutputSave replaces the workspace save for one test and records
// every call; err is returned from each save.
func stubToolOutputSave(t *testing.T, err error) *[]savedToolOutput {
	t.Helper()
	var saved []savedToolOutput
	orig := saveToolOutputFile
	saveToolOutputFile = func(_ *security.RequestContext, accountId, conversationId, filename, content string) error {
		saved = append(saved, savedToolOutput{accountId, conversationId, filename, content})
		return err
	}
	origEnabled := config.Config.LlmServerSaveTruncatedToolOutput
	config.Config.LlmServerSaveTruncatedToolOutput = true
	t.Cleanup(func() {
		saveToolOutputFile = orig
		config.Config.LlmServerSaveTruncatedToolOutput = origEnabled
	})
	return &saved
}

func TestSaveTruncatedToolOutput(t *testing.T) {
	full := `{"series":[` + strings.Repeat(`{"pod":"a","v":1},`, 10) + `{"pod":"b","v":2}]}`

	t.Run("saves the full output and points an agent with shell at the file", func(t *testing.T) {
		saved := stubToolOutputSave(t, nil)
		note, ref := saveTruncatedToolOutput(nil, "acct", "conv", "prometheus_execute", full, nil, true)
		require.Len(t, *saved, 1)
		got := (*saved)[0]
		assert.Equal(t, "acct", got.accountId)
		assert.Equal(t, "conv", got.conversationId)
		assert.Equal(t, full, got.content)
		assert.True(t, strings.HasPrefix(got.filename, "tool_prometheus_execute_"))
		assert.True(t, strings.HasSuffix(got.filename, ".json"), "one JSON document gets a .json name")
		require.NotNil(t, ref)
		assert.Equal(t, "file", ref.Type)
		assert.Equal(t, got.filename, ref.Url)
		assert.Contains(t, note, got.filename)
		assert.Contains(t, note, "read it with shell_execute")
	})

	t.Run("an agent without shell is told to pass the file name up", func(t *testing.T) {
		stubToolOutputSave(t, nil)
		note, ref := saveTruncatedToolOutput(nil, "acct", "conv", "prometheus_execute", full, nil, false)
		require.NotNil(t, ref)
		assert.Contains(t, note, "you cannot read files")
		assert.Contains(t, note, "name this file in your answer")
	})

	t.Run("plain text gets a .txt name and the tool name is made file-safe", func(t *testing.T) {
		saved := stubToolOutputSave(t, nil)
		_, ref := saveTruncatedToolOutput(nil, "acct", "conv", "mcp/Some Tool", "line one\nline two", nil, true)
		require.NotNil(t, ref)
		assert.True(t, strings.HasPrefix((*saved)[0].filename, "tool_mcp_some_tool_"))
		assert.True(t, strings.HasSuffix((*saved)[0].filename, ".txt"))
	})

	t.Run("the same output always gets the same file and note", func(t *testing.T) {
		stubToolOutputSave(t, nil)
		note1, ref1 := saveTruncatedToolOutput(nil, "acct", "conv", "shell_execute", full, nil, true)
		note2, ref2 := saveTruncatedToolOutput(nil, "acct", "conv", "shell_execute", full, nil, true)
		require.NotNil(t, ref1)
		require.NotNil(t, ref2)
		assert.Equal(t, ref1.Url, ref2.Url)
		assert.Equal(t, note1, note2, "identical notes keep the no-progress check working")
		_, ref3 := saveTruncatedToolOutput(nil, "acct", "conv", "shell_execute", full+" ", nil, true)
		assert.NotEqual(t, ref1.Url, ref3.Url)
	})

	t.Run("a tool that returned its own file is not saved twice", func(t *testing.T) {
		saved := stubToolOutputSave(t, nil)
		refs := []toolcore.NBToolResponseReference{{Type: "file", Url: "logs_loki_1.txt"}}
		note, ref := saveTruncatedToolOutput(nil, "acct", "conv", "fetch_logs_direct", full, refs, true)
		assert.Empty(t, *saved)
		assert.Empty(t, note)
		assert.Nil(t, ref)
	})

	t.Run("switched off saves nothing and adds nothing", func(t *testing.T) {
		saved := stubToolOutputSave(t, nil)
		config.Config.LlmServerSaveTruncatedToolOutput = false
		note, ref := saveTruncatedToolOutput(nil, "acct", "conv", "shell_execute", full, nil, true)
		assert.Empty(t, *saved)
		assert.Empty(t, note)
		assert.Nil(t, ref)
	})

	t.Run("a failed save says the output could not be saved", func(t *testing.T) {
		stubToolOutputSave(t, errors.New("workspace pod not found"))
		note, ref := saveTruncatedToolOutput(nil, "acct", "conv", "shell_execute", full, nil, true)
		assert.Nil(t, ref)
		assert.Contains(t, note, "could not be saved")
		assert.Contains(t, note, "narrow the query")
	})

	t.Run("no conversation means no save, and the note says so", func(t *testing.T) {
		saved := stubToolOutputSave(t, nil)
		note, ref := saveTruncatedToolOutput(nil, "acct", "", "shell_execute", full, nil, true)
		assert.Empty(t, *saved)
		assert.Nil(t, ref)
		assert.Contains(t, note, "not saved: no workspace")
	})

	t.Run("an output over the file cap saves only its first part, and says so", func(t *testing.T) {
		saved := stubToolOutputSave(t, nil)
		big := strings.Repeat("x", maxSavedToolOutputBytes+10)
		note, ref := saveTruncatedToolOutput(nil, "acct", "conv", "shell_execute", big, nil, true)
		require.Len(t, *saved, 1)
		assert.Len(t, (*saved)[0].content, maxSavedToolOutputBytes)
		require.NotNil(t, ref)
		assert.Contains(t, note, fmt.Sprintf("first %d bytes", maxSavedToolOutputBytes))
		assert.Contains(t, note, fmt.Sprintf("(%d bytes)", len(big)))
	})

	t.Run("the note starts with the file name", func(t *testing.T) {
		saved := stubToolOutputSave(t, nil)
		note, _ := saveTruncatedToolOutput(nil, "acct", "conv", "shell_execute", full, nil, false)
		assert.True(t, strings.HasPrefix(note, fmt.Sprintf("[workspace file %q", (*saved)[0].filename)), note)
	})
}

// TestDoActionSavesTruncatedToolOutput runs the real tool step: an output over
// LlmServerMaxToolOutputLen is cut, its full text is saved, and the step
// carries the file reference and the note naming the file.
func TestDoActionSavesTruncatedToolOutput(t *testing.T) {
	origOut := config.Config.LlmServerMaxToolOutputLen
	config.Config.LlmServerMaxToolOutputLen = 4096
	t.Cleanup(func() { config.Config.LlmServerMaxToolOutputLen = origOut })

	request := NBAgentRequest{AccountId: "cccccccc-cccc-cccc-cccc-cccccccccccc", ConversationId: "conv-1", MessageId: "msg-1"}
	shell := &MockContextCapturingTool{NameVal: toolcore.ToolExecuteShellCommand, ReturnStatus: toolcore.NBToolResponseStatusSuccess}

	run := func(t *testing.T, output string, status toolcore.NBToolResponseStatus, withShell bool) NBAgentPlannerToolActionStep {
		t.Helper()
		tool := &MockContextCapturingTool{NameVal: "big_tool", ReturnOutput: output, ReturnStatus: status}
		tools := []toolcore.NBTool{tool}
		if withShell {
			tools = append(tools, shell)
		}
		executor := newPlannerExecutor(security.NewRequestContextForSuperAdmin(), &mockNBAgentPlanner{}, &MockAgent{SupportedTools: tools}, request, 3)
		executor.toolCallbackHandler = nil // no DB: the step itself is what this test reads
		step, _, err := executor.doAction(getNameToTool(tools), NBAgentPlannerToolAction{ToolID: "t1", Tool: "big_tool", ToolInput: "{}"}, "")
		require.NoError(t, err)
		return step
	}

	t.Run("a cut output is saved in full and named in the step", func(t *testing.T) {
		saved := stubToolOutputSave(t, nil)
		full := "HEAD-" + strings.Repeat("m", 20000) + "-TAIL"
		step := run(t, full, toolcore.NBToolResponseStatusSuccess, true)

		require.Len(t, *saved, 1)
		assert.Equal(t, full, (*saved)[0].content, "the file holds the uncut output")
		assert.Equal(t, "conv-1", (*saved)[0].conversationId)
		assert.Less(t, len(step.Observation), len(full))
		assert.True(t, strings.HasPrefix(step.Observation, "HEAD-"), "the stored observation keeps the tool's own output shape")
		assert.NotContains(t, step.Observation, "workspace file")
		require.NotNil(t, step.Metadata)
		assert.True(t, strings.HasPrefix(step.Metadata.SavedOutputNote, fmt.Sprintf("[workspace file %q", (*saved)[0].filename)))
		assert.Contains(t, step.Metadata.SavedOutputNote, "read it with shell_execute")
		rendered := renderObservationWithMetadata(step.Observation, step.Metadata)
		assert.True(t, strings.HasPrefix(rendered, step.Metadata.SavedOutputNote+"\n"), "the prompt shows the note first")
		require.Len(t, step.References, 1)
		assert.Equal(t, "file", step.References[0].Type)
		assert.Equal(t, (*saved)[0].filename, step.References[0].Url)
	})

	t.Run("an output just over the cap is still marked truncated", func(t *testing.T) {
		stubToolOutputSave(t, nil)
		tool := &MockContextCapturingTool{NameVal: "big_tool", ReturnOutput: strings.Repeat("m", 4100), ReturnStatus: toolcore.NBToolResponseStatusSuccess}
		tools := []toolcore.NBTool{tool, shell}
		executor := newPlannerExecutor(security.NewRequestContextForSuperAdmin(), &mockNBAgentPlanner{}, &MockAgent{SupportedTools: tools}, request, 3)
		executor.toolCallbackHandler = nil
		step, _, err := executor.doAction(getNameToTool(tools), NBAgentPlannerToolAction{ToolID: "t1", Tool: "big_tool", ToolInput: "{}"}, "")
		require.NoError(t, err)
		assert.NotEmpty(t, step.Metadata.SavedOutputNote)
		require.NotNil(t, step.Metadata)
		assert.True(t, step.Metadata.Truncated)
		assert.Equal(t, 4100, step.Metadata.OriginalLen)
	})

	t.Run("without shell the note asks to pass the file up", func(t *testing.T) {
		stubToolOutputSave(t, nil)
		step := run(t, strings.Repeat("m", 20000), toolcore.NBToolResponseStatusSuccess, false)
		require.NotNil(t, step.Metadata)
		assert.Contains(t, step.Metadata.SavedOutputNote, "you cannot read files")
	})

	t.Run("an output under the cap is not saved", func(t *testing.T) {
		saved := stubToolOutputSave(t, nil)
		step := run(t, "small output", toolcore.NBToolResponseStatusSuccess, true)
		assert.Empty(t, *saved)
		assert.Empty(t, step.References)
		assert.NotContains(t, step.Observation, "workspace file")
		if step.Metadata != nil {
			assert.Empty(t, step.Metadata.SavedOutputNote)
		}
	})

	t.Run("a cut error output is not saved", func(t *testing.T) {
		saved := stubToolOutputSave(t, nil)
		run(t, strings.Repeat("e", 40000), toolcore.NBToolResponseStatusError, true)
		assert.Empty(t, *saved)
	})
}

func TestFileReferences(t *testing.T) {
	refs := []toolcore.NBToolResponseReference{
		{Type: "file", Url: "tool_x_1.json"},
		{Type: "link", Url: "https://example.com"},
		{Type: "file", Url: ""},
		{Type: "file", Url: "logs_loki_2.txt"},
	}
	got := FileReferences(refs)
	require.Len(t, got, 2)
	assert.Equal(t, "tool_x_1.json", got[0].Url)
	assert.Equal(t, "logs_loki_2.txt", got[1].Url)
	assert.Nil(t, FileReferences(nil))
}

func TestUnwrapStdoutEnvelope(t *testing.T) {
	podsJSON := "{\n  \"items\": [\n    {\"metadata\": {\"name\": \"a\"}}\n  ]\n}\n"
	env, err := json.Marshal(map[string]string{"stdout": podsJSON})
	require.NoError(t, err)

	cases := []struct {
		name, in, want string
	}{
		{"shell result is unwrapped to what the command printed", string(env), podsJSON},
		{"no_matches result is unwrapped", `{"no_matches":true,"stdout":""}`, ""},
		{"plain text is unchanged", "line one\nline two", "line one\nline two"},
		{"other JSON is unchanged", `{"series":[1,2,3]}`, `{"series":[1,2,3]}`},
		{"an object with extra keys is unchanged", `{"stdout":"x","items":[]}`, `{"stdout":"x","items":[]}`},
		{"a non-string stdout is unchanged", `{"stdout":42}`, `{"stdout":42}`},
		{"broken JSON is unchanged", `{"stdout":"x`, `{"stdout":"x`},
		{"an empty stderr is dropped", `{"stderr":"","stdout":"out"}`, "out"},
		{"a non-empty stderr keeps the whole result", `{"stderr":"warning: x","stdout":"out"}`, `{"stderr":"warning: x","stdout":"out"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, unwrapStdoutEnvelope(tc.in))
		})
	}
}

// The live case: a shell read of `kubectl get pods -A -o json` is saved as the
// command's JSON, so `jq '.items[]'` works on the file.
func TestSaveTruncatedToolOutputSavesShellStdout(t *testing.T) {
	saved := stubToolOutputSave(t, nil)
	podsJSON := "{\n  \"items\": [\n    {\"metadata\": {\"name\": \"a\"}, \"status\": {\"phase\": \"Pending\"}}\n  ]\n}\n"
	env, err := json.Marshal(map[string]string{"stdout": podsJSON})
	require.NoError(t, err)

	_, ref := saveTruncatedToolOutput(nil, "acct", "conv", "shell_execute", string(env), nil, true)
	require.NotNil(t, ref)
	require.Len(t, *saved, 1)
	assert.Equal(t, podsJSON, (*saved)[0].content)
	assert.True(t, strings.HasSuffix((*saved)[0].filename, ".json"))
	var parsed map[string]any
	require.NoError(t, json.Unmarshal([]byte((*saved)[0].content), &parsed), "the saved file is the command's own JSON")
}

// A command output the workspace had already cut at 1 MiB is saved, but the
// note says the file is incomplete and points at a redirect.
func TestSaveTruncatedToolOutputFlagsWorkspaceCap(t *testing.T) {
	saved := stubToolOutputSave(t, nil)
	env, err := json.Marshal(map[string]string{"stdout": "{\"items\": [" + workspaceOutputCapMarker})
	require.NoError(t, err)

	note, ref := saveTruncatedToolOutput(nil, "acct", "conv", "shell_execute", string(env), nil, true)
	require.NotNil(t, ref)
	require.Len(t, *saved, 1)
	assert.True(t, strings.HasPrefix(note, fmt.Sprintf("[workspace file %q", (*saved)[0].filename)))
	assert.Contains(t, note, "file is incomplete")
	assert.Contains(t, note, "command > out.json")
}

func TestSaveToolOutputFileNilContext(t *testing.T) {
	err := saveToolOutputFile(nil, "acct", "conv", "f.txt", "x")
	require.Error(t, err)
}

// The note is added only when the prompt is built, first, on both planners'
// render paths; steps without one render exactly as before.
func TestSavedOutputNoteRendering(t *testing.T) {
	meta := &toolcore.NBToolResponseMetadata{Truncated: true, OriginalLen: 9, SavedOutputNote: `[workspace file "tool_x_1.txt" holds the full output]`}
	obs := `{"stdout":"cut"}`

	t.Run("ReAct3 and compression render", func(t *testing.T) {
		got := renderObservationWithMetadata(obs, meta)
		assert.True(t, strings.HasPrefix(got, meta.SavedOutputNote+"\n"+obs))
		assert.Equal(t, obs+formatToolMetadataFooter(&toolcore.NBToolResponseMetadata{}), renderObservationWithMetadata(obs, &toolcore.NBToolResponseMetadata{}))
	})

	t.Run("ReAct4 render", func(t *testing.T) {
		planner := &NBReActPlanner4{}
		step := &NBAgentPlannerToolActionStep{Action: NBAgentPlannerToolAction{Tool: "shell_execute"}, Observation: obs, Metadata: meta}
		got := planner.renderObservation(step, 0, 1, false, 4096)
		assert.True(t, strings.HasPrefix(got, meta.SavedOutputNote+"\n"), got)
		assert.Contains(t, got, obs)

		plain := &NBAgentPlannerToolActionStep{Action: NBAgentPlannerToolAction{Tool: "shell_execute"}, Observation: obs}
		assert.Equal(t, obs, planner.renderObservation(plain, 0, 1, false, 4096))
	})
}

// A run that pauses and resumes keeps each step's saved-output note: executor
// state round-trips step metadata.
func TestExecutorStateKeepsSavedOutputNote(t *testing.T) {
	ctx := security.NewRequestContextForSuperAdmin()
	request := NBAgentRequest{AccountId: "cccccccc-cccc-cccc-cccc-cccccccccccc", ConversationId: "conv-1", MessageId: "msg-1"}
	note := `[workspace file "tool_shell_execute_abc.txt" holds the full output of this cut output (9 bytes); read it with shell_execute (grep, jq, python) instead of re-running this tool]`

	before := newPlannerExecutor(ctx, &mockNBAgentPlanner{}, &MockAgent{}, request, 3)
	before.steps = []NBAgentPlannerToolActionStep{{
		Action:      NBAgentPlannerToolAction{ToolID: "t1", Tool: "shell_execute", ToolInput: "{}"},
		Observation: `{"stdout":"cut"}`,
		Status:      ToolStatusSuccess,
		References:  []toolcore.NBToolResponseReference{{Type: "file", Url: "tool_shell_execute_abc.txt"}},
		Metadata:    &toolcore.NBToolResponseMetadata{Truncated: true, OriginalLen: 9, SavedOutputNote: note},
	}}
	state, err := before.Marshal()
	require.NoError(t, err)

	after := newPlannerExecutor(ctx, &mockNBAgentPlanner{}, &MockAgent{}, request, 3)
	require.NoError(t, after.Unmarshal(state))
	require.Len(t, after.steps, 1)
	require.NotNil(t, after.steps[0].Metadata)
	assert.Equal(t, note, after.steps[0].Metadata.SavedOutputNote)
	assert.True(t, after.steps[0].Metadata.Truncated)
	assert.Equal(t, 9, after.steps[0].Metadata.OriginalLen)
	assert.True(t, strings.HasPrefix(renderObservationWithMetadata(after.steps[0].Observation, after.steps[0].Metadata), note))
}

func TestRestoreStepMetadata(t *testing.T) {
	assert.Nil(t, restoreStepMetadata(nil))
	assert.Nil(t, restoreStepMetadata("not an object"))
	got := restoreStepMetadata(map[string]any{"truncated": true, "original_len": float64(5), "saved_output_note": "n"})
	require.NotNil(t, got)
	assert.Equal(t, "n", got.SavedOutputNote)
	assert.Equal(t, 5, got.OriginalLen)
}

// When the cut output was saved, the truncation marker points at the file
// instead of telling the model to re-query; otherwise it keeps "narrow the query".
func TestTruncationNotePointsAtSavedFile(t *testing.T) {
	saved := &toolcore.NBToolResponseMetadata{Truncated: true, OriginalLen: 200000, SavedOutputNote: `[workspace file "tool_x_1.txt" holds the full output of this cut output (200000 bytes); read it with shell_execute (grep, jq, python) instead of re-running this tool]`}
	notSaved := &toolcore.NBToolResponseMetadata{Truncated: true, OriginalLen: 200000, SavedOutputNote: "[output cut from 200000 bytes and could not be saved to the workspace; narrow the query to see the part that was cut]"}
	noNote := &toolcore.NBToolResponseMetadata{Truncated: true, OriginalLen: 200000}

	assert.Equal(t, "truncated: output cut from 200000 bytes, full output in the workspace file named above", truncationNote(saved))
	assert.Equal(t, "truncated: output cut from 200000 bytes, narrow the query for full detail", truncationNote(notSaved))
	assert.Equal(t, "truncated: output cut from 200000 bytes, narrow the query for full detail", truncationNote(noNote))

	o := &NBReActPlanner4{}
	got := o.renderObservation(&NBAgentPlannerToolActionStep{Action: NBAgentPlannerToolAction{Tool: "shell_execute"}, Observation: "partial output", Metadata: saved}, 0, 1, false, 4096)
	assert.Equal(t, saved.SavedOutputNote+"\npartial output\n[truncated: output cut from 200000 bytes, full output in the workspace file named above]", got)
	assert.NotContains(t, renderObservationWithMetadata("partial output", saved), "narrow the query")
}
