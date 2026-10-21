package core

import (
	"errors"

	llmruntime "nudgebee/llm/llms/runtime"
	"nudgebee/llm/security"
)

// runtimeConversation is the conversation side of the LLM runtime (see
// llmruntime.ConversationPort).
type runtimeConversation struct{}

func (runtimeConversation) GetAgentNameFromAgentId(agentId string) (string, error) {
	return GetConversationDao().GetAgentNameFromAgentId(agentId)
}

func (runtimeConversation) UpdateConversationAgentThought(agentId, thought string) error {
	dao := GetConversationDao()
	if dao == nil {
		return nil
	}
	return dao.UpdateConversationAgentThought(agentId, thought)
}

func (runtimeConversation) SetMessageLiveStatus(messageId, key string, status *llmruntime.LiveStatus) error {
	return GetConversationDao().SetMessageLiveStatus(messageId, key, status)
}

func (runtimeConversation) GetConversationOverride(conversationId string) (string, string, llmruntime.ConversationTierOverrides, string, error) {
	return GetConversationOverride(conversationId)
}

func (runtimeConversation) AddRunTokens(ctx *security.RequestContext, tokens int) {
	policyFor(ctx).AddTokens(ctx, tokens)
}

func (runtimeConversation) PeekAgentNameFromAgentId(agentId string) (string, error) {
	dao := PeekConversationDao()
	if dao == nil {
		return "", errNoConversationDao
	}
	return dao.GetAgentNameFromAgentId(agentId)
}

var errNoConversationDao = errors.New("conversation DAO is not open")

func init() {
	llmruntime.SetConversationPort(runtimeConversation{})
}
