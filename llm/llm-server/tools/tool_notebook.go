package tools

import (
	"strings"

	core "nudgebee/llm/tools/core"
)

// NotebookToolName is the registered name for the update_notebook tool
// (canonical value in tools/core, aliased here for local readability).
//
// update_notebook is the ReAct4 (provider-native tool calling) replacement for
// ReAct3's inline `<update_notebook>` XML tag. It is a CONTROL tool, not an
// integration: the ReAct4 planner advertises it in the native tool list so the
// model can record investigation state. The planner dispatches the call like any
// other tool and derives the current notebook from the resulting step, so Call
// below simply validates and echoes the content back as the observation. See
// docs/planner_react_4.md and the `think` tool for the same control-tool shape.
const NotebookToolName = core.NotebookToolName

// notebookContentArg is the schema field carrying the full notebook body.
const notebookContentArg = "content"

const notebookEmptyRejectionMsg = "update_notebook requires a non-empty 'content' field holding the FULL notebook body " +
	"(hypotheses with [SUPPORTED]/[REFUTED] markers, evidence, and resolved status). " +
	"The content replaces the notebook wholesale — include everything you want to keep, not just the delta."

func init() {
	core.RegisterNBToolFactory(NotebookToolName, func(accountId string) (core.NBTool, error) {
		return &notebookTool{}, nil
	})
}

type notebookTool struct{}

func (t *notebookTool) Name() string             { return NotebookToolName }
func (t *notebookTool) GetType() core.NBToolType { return core.NBToolTypeTool }

func (t *notebookTool) Description() string {
	return "Record or update your investigation notebook — the durable state of the analysis. " +
		"USE to maintain the hypothesis tree (candidate root causes with [SUPPORTED]/[REFUTED] markers), " +
		"the evidence that closes each sub-question, and what remains open. " +
		"The 'content' field REPLACES the notebook wholesale, so include the full body you want to keep — not just the change. " +
		"Update it as evidence arrives; it is carried into every subsequent step and persisted across turns. " +
		"This does not answer the user — emit the final answer directly when the investigation is complete."
}

func (t *notebookTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.ToolSchemaProperty{
			notebookContentArg: {
				Type:        core.ToolSchemaTypeString,
				Description: "The full notebook body to store: hypothesis tree, evidence, resolved status, and open questions. Replaces the previous notebook.",
			},
		},
		Required: []string{notebookContentArg},
	}
}

// Call is the fallback path for update_notebook. The ReAct4 planner normally
// intercepts the call and writes the content into planner state before this
// runs, so a direct execution here just validates the content and echoes it
// back as the observation. Content may arrive either in Arguments["content"]
// (native tool call) or on the top-level Command.
func (t *notebookTool) Call(_ core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {
	content := input.Command
	if input.Arguments != nil {
		if c, ok := input.Arguments[notebookContentArg].(string); ok && c != "" {
			content = c
		}
	}

	if strings.TrimSpace(content) == "" {
		return core.NBToolResponse{
			Data:   notebookEmptyRejectionMsg,
			Status: core.NBToolResponseStatusError,
		}, nil
	}

	return core.NBToolResponse{
		Data:   content,
		Status: core.NBToolResponseStatusSuccess,
	}, nil
}
