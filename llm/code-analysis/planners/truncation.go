package planners

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/tmc/langchaingo/llms"
)

// maxObservationChars is the most one tool result may occupy in the
// conversation. formatObservation trims to it, keeping head and tail and stating
// the gap, and the conversation append applies it again to whatever did not come
// through formatObservation. It is one number on purpose: when the first trimmed
// to 30,000 and the second cut at 15,000, the second dropped the tail the first
// had kept — the end of a test run, a tool's own "narrow your search" note — and
// left only "[truncated]".
const maxObservationChars = 15000

// observationNoteReserve is what formatObservation leaves free under
// maxObservationChars for the notes the planner puts around a result after
// formatting it: cachedResultNote in front, repeatCallWarning behind. Without
// it a result trimmed to the limit went over it again once a note was added,
// and was trimmed a second time — through the note.
const observationNoteReserve = 500

const repeatCallWarning = "\n\nWARNING: This is the 2nd identical call. A 3rd will be blocked. Try a different approach."

func cachedResultNote(action string, firstStep int) string {
	return fmt.Sprintf("[cached: identical %s call already executed at step %d this run; result unchanged]\n", action, firstStep)
}

// proportionalTruncate keeps the head and tail of content, omitting the middle.
// Head gets 70% of maxLen, tail gets 30%. A marker is inserted showing how much was omitted.
func proportionalTruncate(content string, maxLen int) string {
	if len(content) <= maxLen {
		return content
	}
	if maxLen <= 0 {
		return ""
	}

	headSize := int(float64(maxLen) * 0.7)
	tailSize := maxLen - headSize
	// Reserve space for the omission marker
	markerSpace := 60
	if headSize+tailSize+markerSpace > maxLen {
		headSize = headSize - markerSpace/2
		tailSize = tailSize - markerSpace/2
	}
	if headSize < 0 {
		headSize = 0
	}
	if tailSize < 0 {
		tailSize = 0
	}

	omitted := len(content) - headSize - tailSize
	marker := fmt.Sprintf("\n\n[... %d chars omitted ...]\n\n", omitted)

	head := content[:headSize]
	tail := content[len(content)-tailSize:]
	return head + marker + tail
}

// lineBasedTruncate keeps the first 70% and last 30% of lines, omitting the middle.
// A marker is inserted showing how many lines were omitted.
func lineBasedTruncate(content string, maxLines int) string {
	lines := strings.Split(content, "\n")
	if len(lines) <= maxLines {
		return content
	}
	if maxLines <= 0 {
		return ""
	}

	headLines := int(float64(maxLines) * 0.7)
	tailLines := maxLines - headLines
	if headLines < 1 {
		headLines = 1
	}
	if tailLines < 1 {
		tailLines = 1
	}

	omitted := len(lines) - headLines - tailLines
	if omitted <= 0 {
		return content
	}

	head := strings.Join(lines[:headLines], "\n")
	tail := strings.Join(lines[len(lines)-tailLines:], "\n")
	marker := fmt.Sprintf("\n[... %d lines omitted ...]\n", omitted)

	return head + marker + tail
}

// compactJSONArgs keeps short scalar values from a JSON args string, dropping large
// strings, arrays, and nested objects. Returns valid JSON. Falls back to "{}" on error
// or if the result exceeds maxLen.
func compactJSONArgs(argsJSON string, maxLen int) string {
	if len(argsJSON) <= maxLen {
		return argsJSON
	}

	var parsed map[string]any
	if err := json.Unmarshal([]byte(argsJSON), &parsed); err != nil {
		return "{}"
	}

	compact := make(map[string]any)
	for k, v := range parsed {
		switch val := v.(type) {
		case string:
			if len(val) <= 100 {
				compact[k] = val
			} else {
				compact[k] = val[:80] + "..."
			}
		case float64, bool:
			compact[k] = val
			// Drop arrays, nested objects, and nil
		}
	}

	result, err := json.Marshal(compact)
	if err != nil || len(result) > maxLen {
		return "{}"
	}
	return string(result)
}

// estimateMessageTokens estimates the token count of a conversation by summing
// all text content characters and dividing by 4 (rough chars-per-token heuristic).
func estimateMessageTokens(messages []llms.MessageContent) int {
	totalChars := 0
	for _, msg := range messages {
		for _, part := range msg.Parts {
			switch p := part.(type) {
			case llms.TextContent:
				totalChars += len(p.Text)
			case llms.ToolCall:
				if p.FunctionCall != nil {
					totalChars += len(p.FunctionCall.Name)
					totalChars += len(p.FunctionCall.Arguments)
				}
			case llms.ToolCallResponse:
				totalChars += len(p.Name)
				totalChars += len(p.Content)
			}
		}
	}
	return totalChars / 4
}
