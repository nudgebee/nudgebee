package api

import (
	"errors"
	"fmt"
	"runtime/debug"
	"strings"
	"sync"

	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"
	"nudgebee/llm/security/egressfilter"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"
)

// eventBriefSource is one read the direct step-1 brief is written from.
type eventBriefSource struct {
	tool toolcore.NBTool
	// args are added to event_id.
	args map[string]any
	// maxBytes caps the output in the prompt; 0 leaves it whole.
	maxBytes int
}

// eventBriefSources are the reads the direct brief is written from, in prompt
// order. The events agent spent ~5.5 LLM calls of ~35K input tokens each
// deciding to call the first three every time (#41579). The metric summary is
// there for "the actual measured value": get_event_by_id inlines metric series
// cut mid-way, and the events agent fetched this summary itself when it needed
// the number (dev event dcdd4751, 10-19).
var eventBriefSources = []eventBriefSource{
	{tool: tools.GetEventByIdTool{}},
	{tool: tools.TriageExplanationTool{}},
	{tool: tools.IncidentAssemblyTool{}},
	{tool: tools.GetEventEvidenceTool{}, args: map[string]any{"evidence_type": "metrics_data", "query_mode": "summary"}, maxBytes: 12000},
}

// eventBriefGuidance must not let the brief call data absent: the brief is fed
// to the investigation prompt and the final report, and the reads here are a
// subset of the stored evidence.
// eventBriefNotLoaded marks a read that failed, so the brief can tell "not
// checked" apart from "checked and found nothing".
const eventBriefNotLoaded = "NOT LOADED"

const eventBriefGuidance = "You write on-call briefs for alert events. Use only the tool outputs you are given and " +
	"never guess. When an output is an error or lacks a fact, leave that fact out rather than writing that it is " +
	"unavailable, missing or unknown. Follow the requested format exactly."

// buildDirectEventBriefQuery runs the brief's reads and returns the whole
// prompt for the LLM agent: guidance, the five-section instruction, then each
// read's output.
func buildDirectEventBriefQuery(ctx *security.RequestContext, request EventAnalysisRequest, eventId string, parsedLabels map[string]any) string {
	return composeDirectEventBriefQuery(eventId, fetchEventBriefSources(ctx, request, eventId, parsedLabels))
}

// composeDirectEventBriefQuery tags each output with its tool name so the
// instruction's "from the <tool> output" phrases resolve.
func composeDirectEventBriefQuery(eventId string, outputs []string) string {
	var b strings.Builder
	b.WriteString(eventBriefGuidance + "\n\n")
	b.WriteString(buildEventBriefQuery(eventId, true))
	b.WriteString("\n\nTool outputs for this event:\n")
	for i, source := range eventBriefSources {
		name := source.tool.Name()
		b.WriteString("\n<" + name + ">\n" + outputs[i] + "\n</" + name + ">\n")
	}
	return b.String()
}

// fetchEventBriefSources runs the reads in parallel. A failed read becomes a
// NOT LOADED line with its error rather than failing the pipeline step.
func fetchEventBriefSources(ctx *security.RequestContext, request EventAnalysisRequest, eventId string, parsedLabels map[string]any) []string {
	outputs := make([]string, len(eventBriefSources))
	var wg sync.WaitGroup
	for i, source := range eventBriefSources {
		wg.Add(1)
		go func(i int, source eventBriefSource) {
			defer wg.Done()
			// A panic in a bare goroutine would take the whole server down.
			defer func() {
				if r := recover(); r != nil {
					ctx.GetLogger().Error("analyzer: event brief read panicked", "tool", source.tool.Name(), "event_id", eventId, "panic", r, "stack", string(debug.Stack()))
					outputs[i] = eventBriefNotLoaded + ": tool call failed"
				}
			}()
			args := map[string]any{"event_id": eventId}
			for k, v := range source.args {
				args[k] = v
			}
			data, err := callEventReadTool(ctx, request, parsedLabels, source.tool, args)
			switch {
			case err != nil:
				ctx.GetLogger().Warn("analyzer: event brief read failed", "tool", source.tool.Name(), "event_id", eventId, "error", err)
				outputs[i] = eventBriefNotLoaded + ": " + err.Error()
			case source.maxBytes > 0:
				outputs[i] = core.TruncateHead(data, source.maxBytes)
			default:
				outputs[i] = data
			}
		}(i, source)
	}
	wg.Wait()
	return outputs
}

// callEventReadTool runs one read-only event tool for the pipeline, before any
// conversation message exists, so there is no tool-call row. Output gets the
// same redaction a planner's tool call gets.
func callEventReadTool(ctx *security.RequestContext, request EventAnalysisRequest, parsedLabels map[string]any, tool toolcore.NBTool, args map[string]any) (string, error) {
	toolCtx := toolcore.NewNbToolContext(ctx, tool, request.AccountId, request.UserId, "", "", "", "", nil, "", toolcore.NBQueryConfig{Labels: parsedLabels, EventId: request.EventId}, "")
	resp, err := tool.Call(toolCtx, toolcore.NBToolCallRequest{Arguments: args})
	if err != nil {
		return "", fmt.Errorf("%s: %w", tool.Name(), err)
	}
	if resp.Status == toolcore.NBToolResponseStatusError {
		return "", fmt.Errorf("%s: %s", tool.Name(), core.TruncateHead(resp.Data, 500))
	}
	if strings.TrimSpace(resp.Data) == "" {
		return "", errors.New(tool.Name() + ": no data returned")
	}
	return egressfilter.RedactToolOutput(ctx, "", tool.Name(), resp.Data), nil
}
