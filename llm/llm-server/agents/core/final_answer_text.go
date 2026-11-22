package core

import (
	"strings"

	"nudgebee/llm/common"
)

// eventSummaryHeading opens every event investigation answer: the event prompts
// tell the model to start with it, so text before it is the model's own notes.
const eventSummaryHeading = "### 📝 Event Summary"

// splitFinalAnswer separates the answer a reader should see from working text
// the model wrote around it (#41809). It removes a react_3-style <final_answer>
// wrapper (closed or not), a leading <thought> block, and anything before the
// event summary heading. notes holds what was removed, empty when nothing was.
func splitFinalAnswer(output string) (answer, notes string) {
	answer = strings.TrimSpace(output)
	var removed []string
	if strings.Contains(answer, "<final_answer>") {
		thought := xmlBlockContent(answer, "thought")
		content := xmlBlockContent(answer, "content")
		if content == "" {
			content = strings.TrimSpace(stripXMLBlocks(xmlBlockContent(answer, "final_answer"), "thought"))
		}
		if content != "" {
			answer = content
			removed = append(removed, thought)
		}
	} else if strings.HasPrefix(answer, "<thought>") {
		if end := strings.Index(answer, "</thought>"); end >= 0 {
			removed = append(removed, strings.TrimSpace(answer[len("<thought>"):end]))
			answer = strings.TrimSpace(answer[end+len("</thought>"):])
		}
	}
	// Only a heading that starts a line, after prose without headings of its
	// own: a real section before it, or the heading quoted mid-sentence, stays.
	if i := strings.Index(answer, "\n"+eventSummaryHeading); i >= 0 && !hasMarkdownHeading(answer[:i]) {
		removed = append(removed, strings.TrimSpace(answer[:i]))
		answer = answer[i+1:]
	}
	if answer == "" {
		// Nothing but notes (e.g. a cut-off reply): keep the text as before
		// rather than return an empty answer.
		return strings.TrimSpace(output), ""
	}
	var kept []string
	for _, r := range removed {
		if r != "" {
			kept = append(kept, r)
		}
	}
	return answer, strings.Join(kept, "\n\n")
}

func hasMarkdownHeading(text string) bool {
	for line := range strings.SplitSeq(text, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			return true
		}
	}
	return false
}

// xmlBlockContent returns a tag's content like common.XmlExtractTagContent, but
// also when the closing tag is missing (the model sometimes stops before closing
// its wrapper): the content then runs to the end, without the wrapper's own
// closing tags.
func xmlBlockContent(text, tag string) string {
	if content := common.XmlExtractTagContent(text, tag); content != "" {
		return content
	}
	_, rest, found := strings.Cut(text, "<"+tag+">")
	if !found {
		return ""
	}
	// Closed after this opening tag (XmlExtractTagContent misses it when a stray
	// closing tag comes first).
	if end := strings.Index(rest, "</"+tag+">"); end >= 0 {
		return common.XmlExtractCDATA(strings.TrimSpace(rest[:end]))
	}
	for _, closing := range []string{"</content>", "</final_answer>"} {
		rest = strings.ReplaceAll(rest, closing, "")
	}
	return common.XmlExtractCDATA(strings.TrimSpace(rest))
}
