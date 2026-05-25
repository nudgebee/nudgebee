package core

import (
	"github.com/tmc/langchaingo/llms"
	"github.com/tmc/langchaingo/prompts"
)

// LiteralSystemMessage is a prompts.MessageFormatter that emits a system
// message verbatim, without running the content through Go's text/template
// engine. Use this for system content that has already been rendered (e.g.
// the agent system prompt produced by GetPromptTemplate.Format() in
// executor.go) so that any literal `{{ ... }}` in the data is not
// re-interpreted as a template action — see issue #30120 where a literal
// `{{ Configs.key_name }}` in the WorkflowAgent's instruction was parsed as
// an undefined function call during the chat-prompt second render.
type LiteralSystemMessage struct {
	Content string
}

var _ prompts.MessageFormatter = LiteralSystemMessage{}

func (m LiteralSystemMessage) FormatMessages(_ map[string]any) ([]llms.ChatMessage, error) {
	return []llms.ChatMessage{llms.SystemChatMessage{Content: m.Content}}, nil
}

func (m LiteralSystemMessage) GetInputVariables() []string {
	return nil
}
