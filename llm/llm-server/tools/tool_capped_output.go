package tools

import (
	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"
)

// CappedOutputTool caps another tool's result text at MaxChars, appending a
// truncation note that ends with Hint (what the caller should do to get a
// smaller answer). It caps the result itself, so it holds on every planner:
// react_4 never calls an agent's UpdateToolResponseForPlanner, so a cap there
// only held on react_3. The executor records and meters the call to this
// wrapper as it would the wrapped tool, under the same name.
type CappedOutputTool struct {
	core.NBTool
	MaxChars int
	Hint     string
}

func (c CappedOutputTool) Call(ctx core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {
	response, err := c.NBTool.Call(ctx, input)
	if c.MaxChars > 0 && len(response.Data) > c.MaxChars {
		response.Data = response.Data[:c.MaxChars] + "\n... (truncated - " + c.Hint + ")"
	}
	return response, err
}

// InferToolRequestType forwards the wrapped tool's read/write classification.
// Embedding the NBTool interface hides every method outside it, so without this
// a ReadOnlyTool behind the wrapper is unclassified and each call logs "no
// classifier decided" (#39880 C7). A wrapped tool without a static classifier
// answers "" (undecided). An LLM-prompt classifier is not forwarded, so wrap
// read tools only.
func (c CappedOutputTool) InferToolRequestType(ctx *security.RequestContext, toolName, input string) (core.ToolRequestType, error) {
	if inner, ok := c.NBTool.(core.ToolRequestInference); ok {
		return inner.InferToolRequestType(ctx, toolName, input)
	}
	return "", nil
}
