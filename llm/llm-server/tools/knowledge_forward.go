package tools

import (
	"fmt"

	"nudgebee/llm/knowledge"

	"nudgebee/llm/config"
	"nudgebee/llm/tools/core"
)

// KnowledgeFileReference identifies a file in the execution workspace, not the
// analysis checkout. The receiver resolves Path using ConversationID.
type KnowledgeFileReference struct {
	ConversationID string                            `json:"conversation_id"`
	Path           string                            `json:"path"`
	Title          string                            `json:"title"`
	Source         string                            `json:"source"`
	Version        string                            `json:"version"`
	SHA256         string                            `json:"sha256"`
	Bytes          int64                             `json:"bytes"`
	Purpose        knowledge.KnowledgeContentPurpose `json:"purpose"`
}

// PrepareKnowledgeForAnalysis reuses exact loading, validation and transfer
// limits. Large content stays in the workspace; only small content is inline.
func PrepareKnowledgeForAnalysis(ctx core.NbToolContext, candidate knowledge.KnowledgeCandidate) (string, *KnowledgeFileReference, error) {
	if !config.Config.LlmServerKnowledgeWorkspaceEnabled {
		return "", nil, fmt.Errorf("workspace knowledge is disabled")
	}
	response, err := (LoadSkillsTool{}).callWorkspaceKnowledge(ctx, core.NBToolCallRequest{Arguments: map[string]any{"skill_name": candidate.ID}}, candidate.ID)
	if err != nil {
		return "", nil, err
	}
	if response.Status != core.NBToolResponseStatusSuccess {
		return "", nil, fmt.Errorf("%v", response.Data)
	}
	loaded, ok := knowledge.LoadKnowledgeCandidate(ctx.AccountId, ctx.ConversationId, ctx.MessageId, candidate.ID)
	if !ok {
		return "", nil, fmt.Errorf("knowledge candidate expired during preparation")
	}
	if loaded.Path == "" {
		if loaded.Content == "" || loaded.ExcerptOnly {
			return "", nil, fmt.Errorf("complete knowledge content is unavailable")
		}
		return fmt.Sprintf("%s\nTitle: %s\nSource: %s\n%s", knowledge.KnowledgePurposeGuidance(loaded.Purpose), loaded.Title, loaded.ReferenceID, loaded.Content), nil, nil
	}
	source := loaded.URL
	if source == "" {
		source = loaded.ReferenceID
	}
	return "", &KnowledgeFileReference{ConversationID: ctx.ConversationId, Path: loaded.Path, Title: loaded.Title, Source: source, Version: loaded.Version, SHA256: loaded.FileSHA256, Bytes: loaded.Bytes, Purpose: loaded.Purpose}, nil
}
