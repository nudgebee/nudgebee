package tools

import (
	"strings"
	"testing"

	"nudgebee/llm/tools/core"
)

func TestProposeRecommendationApplyTool_Metadata(t *testing.T) {
	tool := ProposeRecommendationApplyTool{}
	if tool.Name() != ToolProposeRecommendationApply {
		t.Errorf("Name() = %q, want %q", tool.Name(), ToolProposeRecommendationApply)
	}
	if tool.GetType() != core.NBToolTypeTool {
		t.Errorf("GetType() = %q, want tool", tool.GetType())
	}
	// Read-only hand-off: it produces a link and must NOT engage the executor's
	// write-confirmation gate (the apply is confirmed in the UI).
	rt, err := tool.InferToolRequestType(nil, "", "")
	if err != nil {
		t.Fatalf("InferToolRequestType error: %v", err)
	}
	if rt != core.ToolRequestTypeRead {
		t.Errorf("InferToolRequestType = %q, want %q (read-only hand-off, not a write)", rt, core.ToolRequestTypeRead)
	}
}

func TestProposeRecommendationApplyTool_Guards(t *testing.T) {
	tool := ProposeRecommendationApplyTool{}
	tests := []struct {
		name    string
		ctx     core.NbToolContext
		args    map[string]any
		wantErr string
	}{
		{
			name:    "missing recommendation_id",
			ctx:     core.NbToolContext{AccountId: "acc"},
			args:    map[string]any{},
			wantErr: "recommendation_id is required",
		},
		{
			name:    "missing account",
			ctx:     core.NbToolContext{},
			args:    map[string]any{"recommendation_id": "rec-1"},
			wantErr: "account_id is required",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tool.Call(tt.ctx, core.NBToolCallRequest{Arguments: tt.args})
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %q, want containing %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestProposeRecommendationApplyTool_LinkAndReference(t *testing.T) {
	tool := ProposeRecommendationApplyTool{}
	resp, err := tool.Call(
		core.NbToolContext{AccountId: "acc-1"},
		core.NBToolCallRequest{Arguments: map[string]any{"recommendation_id": "rec-1"}},
	)
	if err != nil {
		t.Fatalf("Call error: %v", err)
	}
	for _, want := range []string{"/optimise?id=rec-1", "accountId=acc-1", "#recommendations"} {
		if !strings.Contains(resp.Data, want) {
			t.Errorf("response data missing %q: %s", want, resp.Data)
		}
	}
	if len(resp.References) != 1 || !strings.Contains(resp.References[0].Url, "id=rec-1") {
		t.Errorf("expected a single link reference to the recommendation, got %+v", resp.References)
	}
}

func TestBuildRecommendationApplyLink_EscapesParams(t *testing.T) {
	link := buildRecommendationApplyLink("rec/with space", "acc-1")
	if strings.Contains(link, " ") || strings.Contains(link, "rec/with") {
		t.Errorf("link did not escape the recommendation id: %s", link)
	}
}
