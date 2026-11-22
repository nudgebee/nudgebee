package tools

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"nudgebee/llm/config"
	"nudgebee/llm/tools/core"

	"github.com/stretchr/testify/assert"
)

// A real recommendations_apply shape: the top-level status is the
// recommendation's lifecycle, the failure is nested in resolution.
const (
	applyFailedBody  = `{"data":[],"resolution":{"id":"r1","status":"Failed"},"status":"InProgress"}`
	applySuccessBody = `{"data":[],"resolution":{"id":"r1","status":"Success"},"status":"Open"}`
)

func setReportedFailureConfig(t *testing.T, mode, enforce string) {
	t.Helper()
	oldMode, oldEnforce := config.Config.LlmServerToolReportedFailureMode, config.Config.LlmServerToolReportedFailureEnforceTools
	config.Config.LlmServerToolReportedFailureMode = mode
	config.Config.LlmServerToolReportedFailureEnforceTools = enforce
	t.Cleanup(func() {
		config.Config.LlmServerToolReportedFailureMode = oldMode
		config.Config.LlmServerToolReportedFailureEnforceTools = oldEnforce
	})
}

func okResponse(body string) core.NBToolResponse {
	return core.NBToolResponse{Data: body, Type: core.NBToolResponseTypeJson, Status: core.NBToolResponseStatusSuccess}
}

func TestWithReportedFailure_LogModeNeverChangesStatus(t *testing.T) {
	setReportedFailureConfig(t, "log", "")
	got := withReportedFailure(core.NbToolContext{}, ToolRecommendationApply, okResponse(applyFailedBody), recommendationApplyFailureRules)
	assert.Equal(t, core.NBToolResponseStatusSuccess, got.Status)
	assert.Nil(t, got.Metadata)
}

func TestWithReportedFailure_EnforcedToolIsRecordedFailed(t *testing.T) {
	setReportedFailureConfig(t, "log", "some_other_tool, "+ToolRecommendationApply)
	got := withReportedFailure(core.NbToolContext{}, ToolRecommendationApply, okResponse(applyFailedBody), recommendationApplyFailureRules)
	assert.Equal(t, core.NBToolResponseStatusError, got.Status)
	if assert.NotNil(t, got.Metadata) {
		assert.Equal(t, core.FailureReasonReportedFailed, got.Metadata.FailureReason)
	}
	assert.JSONEq(t, applyFailedBody, got.Data, "the tool's own reply stays as it was")
}

func TestWithReportedFailure_EnforcementIsPerTool(t *testing.T) {
	setReportedFailureConfig(t, "log", "some_other_tool")
	got := withReportedFailure(core.NbToolContext{}, ToolRecommendationApply, okResponse(applyFailedBody), recommendationApplyFailureRules)
	assert.Equal(t, core.NBToolResponseStatusSuccess, got.Status)
}

func TestWithReportedFailure_MalformedBodyIsFailedWhenEnforced(t *testing.T) {
	setReportedFailureConfig(t, "log", ToolRecommendationApply)
	got := withReportedFailure(core.NbToolContext{}, ToolRecommendationApply, okResponse("Bad Gateway"), core.FailureRules{ExpectJSON: true})
	assert.Equal(t, core.NBToolResponseStatusError, got.Status)
	assert.Equal(t, core.FailureReasonMalformedResponse, got.Metadata.FailureReason)
}

func TestWithReportedFailure_SuccessAndOffAreUntouched(t *testing.T) {
	setReportedFailureConfig(t, "log", ToolRecommendationApply)
	got := withReportedFailure(core.NbToolContext{}, ToolRecommendationApply, okResponse(applySuccessBody), recommendationApplyFailureRules)
	assert.Equal(t, core.NBToolResponseStatusSuccess, got.Status)
	assert.Nil(t, got.Metadata)

	setReportedFailureConfig(t, "off", ToolRecommendationApply)
	got = withReportedFailure(core.NbToolContext{}, ToolRecommendationApply, okResponse(applyFailedBody), recommendationApplyFailureRules)
	assert.Equal(t, core.NBToolResponseStatusSuccess, got.Status)
}

func TestWithReportedFailure_AnErrorStatusIsNotReclassified(t *testing.T) {
	setReportedFailureConfig(t, "log", ToolRecommendationApply)
	in := core.NBToolResponse{Data: `{"error":"x"}`, Status: core.NBToolResponseStatusError}
	assert.Equal(t, in, withReportedFailure(core.NbToolContext{}, ToolRecommendationApply, in, recommendationApplyFailureRules))
}

// The shape log is the only place a body value could reach the logs: it keeps
// key names and a short status, nothing else.
func TestResponseShape_KeepsNoBodyText(t *testing.T) {
	keys, status := responseShape([]byte(`{"resolution":{"token":"s3cret"},"data":["tenant-data"],"status":"InProgress","message":"hello"}`))
	assert.Equal(t, []string{"data", "message", "resolution", "status"}, keys)
	assert.Equal(t, "InProgress", status)

	_, status = responseShape([]byte(`{"status":"this is a long free text value that is not an enum"}`))
	assert.Empty(t, status)

	keys, status = responseShape([]byte(`[{"a":1}]`))
	assert.Empty(t, keys)
	assert.Empty(t, status)
}

func TestReportedFailureDisabled(t *testing.T) {
	for mode, want := range map[string]bool{"off": true, " OFF ": true, "log": false, "": false, "enforce": false, "typo": false} {
		setReportedFailureConfig(t, mode, "")
		assert.Equal(t, want, reportedFailureDisabled(), "mode %q", mode)
	}
}

func TestWithReportedFailure_DoesNotMutateTheToolsMetadata(t *testing.T) {
	setReportedFailureConfig(t, "log", ToolRecommendationApply)
	shared := &core.NBToolResponseMetadata{Stderr: "kept"}
	in := okResponse(applyFailedBody)
	in.Metadata = shared

	got := withReportedFailure(core.NbToolContext{}, ToolRecommendationApply, in, recommendationApplyFailureRules)

	assert.Equal(t, core.FailureReasonReportedFailed, got.Metadata.FailureReason)
	assert.Equal(t, "kept", got.Metadata.Stderr, "the tool's own fields carry over")
	assert.Empty(t, shared.FailureReason, "a metadata object the tool still holds must not change")
}

func TestToolInEnforceList(t *testing.T) {
	tests := []struct {
		name, list, tool string
		want             bool
	}{
		{"empty list enforces nothing", "", ToolRecommendationApply, false},
		{"empty list and empty name", "", "", false},
		{"blank list and empty name", "  ", "", false},
		{"stray comma does not match an empty name", "a,,b", "", false},
		{"listed with spaces", " a , " + ToolRecommendationApply, ToolRecommendationApply, true},
		{"not listed", "a,b", ToolRecommendationApply, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setReportedFailureConfig(t, "log", tt.list)
			assert.Equal(t, tt.want, toolInEnforceList(tt.tool))
		})
	}
}

func TestLogResponseShape(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))

	logResponseShape(logger, "p", "a", []byte(`{"status":"Open","secret":"s3cret"}`))
	assert.Contains(t, logs.String(), "secret")
	assert.NotContains(t, logs.String(), "s3cret", "key names only, never values")

	logs.Reset()
	big := []byte(`{"data":"` + strings.Repeat("x", maxShapeBodyBytes) + `"}`)
	logResponseShape(logger, "p", "a", big)
	assert.Contains(t, logs.String(), "size_bytes=")
	assert.Contains(t, logs.String(), "body too large")
	assert.NotContains(t, logs.String(), "keys=", "a large reply is not parsed")
}
