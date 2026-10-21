package core

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"nudgebee/llm/knowledge"
	llmruntime "nudgebee/llm/llms/runtime"

	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/security"

	toolcore "nudgebee/llm/tools/core"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
)

type MessageType string

const (
	MessageTypeGeneration       MessageType = "generation"
	MessageTypeResponse         MessageType = "response"
	MessageTypeError            MessageType = "error"
	MessageTypeFollowup         MessageType = "followup"
	MessageTypeFollowupResponse MessageType = "followup_response"
	MessageTypeRoute            MessageType = "route"
)

type MessageRole string

const (
	MessageRoleAI    MessageRole = "ai"
	MessageRoleHuman MessageRole = "human"
)

var ErrConversationNotFound = errors.New("history: conversation not found")
var ErrQueueFull = errors.New("history: queued message capacity reached")
var ErrQueueInactive = errors.New("history: conversation is no longer running; retry your message")

var ErrQueuedMessageNotFoundOrLocked = errors.New("history: queued message not found, already started, or permission denied")

type Conversation struct {
	TenantID         uuid.UUID                             `json:"tenant_id,omitempty" db:"tenant_id"`
	AccountID        uuid.UUID                             `json:"account_id" db:"account_id"`
	UserID           uuid.UUID                             `json:"user_id" db:"user_id"`
	SessionID        string                                `json:"session_id" db:"session_id"`
	ID               uuid.UUID                             `json:"id" db:"id"`
	Context          map[string]any                        `json:"context,omitempty" db:"context"`
	Status           ConversationStatus                    `json:"status,omitempty" db:"status"`
	Title            *string                               `json:"title,omitempty" db:"title"`
	Source           *ConversationSource                   `json:"source,omitempty" db:"source"`
	LlmProvider      *string                               `json:"llm_provider,omitempty" db:"llm_provider"`
	LlmModel         *string                               `json:"llm_model,omitempty" db:"llm_model"`
	LlmTierOverrides *llmruntime.ConversationTierOverrides `json:"llm_tier_overrides,omitempty" db:"llm_tier_overrides"`
	// Pinned config slot ({layer}:{scope}[:{name}]). Orthogonal to the three
	// above: a conversation can pin a slot with or without also pinning a
	// provider/model, so the blanket/tier mutual exclusivity doesn't apply.
	LlmConfigSource *string `json:"llm_config_source,omitempty" db:"llm_config_source"`
	// RunProfile is the identity of a governed run (see RunProfile), set once
	// when the conversation is created; nil for an ordinary conversation.
	RunProfile *RunProfile `json:"run_profile,omitempty" db:"run_profile"`
}

type ConversationMessage struct {
	ID              uuid.UUID          `json:"id" db:"id"`
	ConversationID  uuid.UUID          `json:"conversation_id" db:"conversation_id"`
	AccountID       uuid.UUID          `json:"account_id" db:"account_id"`
	Message         string             `json:"message" db:"message"`
	CreatedAt       *time.Time         `json:"created_at" db:"created_at"`
	Response        string             `json:"response" db:"response"`
	Role            string             `json:"role" db:"role"`
	UserID          uuid.UUID          `json:"user_id" db:"user_id"`
	MessageType     string             `json:"message_type" db:"message_type"`
	UpdatedAt       *time.Time         `json:"updated_at" db:"updated_at"`
	RespondedAt     *time.Time         `json:"responded_at,omitempty" db:"responded_at"`
	Status          ConversationStatus `json:"status" db:"status"`
	WorkerName      *string            `json:"worker_name" db:"worker_name"`
	AgentName       *string            `json:"agent_name" db:"agent_name"`
	ParentAgentID   uuid.UUID          `json:"parent_agent_id" db:"parent_agent_id"`
	MessageConfig   *string            `json:"message_config" db:"message_config"`
	MessageContext  *string            `json:"message_context" db:"message_context"`
	Suggestions     *string            `json:"suggestions" db:"suggestions"`
	Classification  *string            `json:"classification,omitempty" db:"classification"`
	SuccessfulTasks int                `json:"successful_tasks" db:"successful_tasks"`
	LlmProvider     *string            `json:"llm_provider,omitempty" db:"llm_provider"`
	LlmModel        *string            `json:"llm_model,omitempty" db:"llm_model"`
}

type ConversationAgent struct {
	ID                uuid.UUID            `json:"id" db:"id"`
	AgentName         string               `json:"agent_name" db:"agent_name"`
	MessageID         uuid.UUID            `json:"message_id" db:"message_id"`
	Response          *string              `json:"response" db:"response"`
	CreatedAt         *time.Time           `json:"created_at" db:"created_at"`
	AccountID         uuid.UUID            `json:"account_id" db:"account_id"`
	UserID            uuid.UUID            `json:"user_id" db:"user_id"`
	ParentAgentID     uuid.UUID            `json:"parent_agent_id" db:"parent_agent_id"`
	Query             string               `json:"query" db:"query"`
	ConversationID    uuid.UUID            `json:"conversation_id" db:"conversation_id"`
	Thought           *string              `json:"thought" db:"thought"`
	UpdatedAt         *time.Time           `json:"updated_at" db:"updated_at"`
	QueryContext      *string              `json:"query_context" db:"query_context"`
	QueryConfig       *string              `json:"query_config" db:"query_config"`
	Status            AgentExecutionStatus `json:"status" db:"status"`
	FollowupMessageID uuid.UUID            `json:"followup_message_id" db:"followup_message_id"`
	AgentStepResponse *string              `json:"agent_step_response" db:"agent_step_response"`
	State             *string              `json:"state" db:"state"`
	References        *string              `json:"references" db:"references"`
}

type ConversationMessageWithAgents struct {
	ConversationMessage
	Agents []ConversationAgent `json:"llm_conversation_agents"`
}

type ConversationWithMessages struct {
	Conversation
	Messages []ConversationMessageWithAgents `json:"llm_conversation_messages"`
}

type ConversationCritique struct {
	ID               string    `json:"id" db:"id"`
	ConversationID   string    `json:"conversation_id" db:"conversation_id"`
	MessageID        string    `json:"message_id" db:"message_id"`
	AccountID        string    `json:"account_id" db:"account_id"`
	AgentName        string    `json:"agent_name" db:"agent_name"`
	CritiqueType     string    `json:"critique_type" db:"critique_type"`
	Input            string    `json:"input" db:"input"`
	CritiquedContent string    `json:"critiqued_content" db:"critiqued_content"`
	Feedback         *string   `json:"feedback,omitempty" db:"feedback"`
	Decision         string    `json:"decision" db:"decision"`
	CreatedAt        time.Time `json:"created_at" db:"created_at"`
}

type Document struct {
	Content string `json:"document"`
}

type IConversationDao interface {
	FinalizeConversationMessage(conversationID, messageID, response string, status ConversationStatus) (bool, error)
	SaveConversationAgentCall(conversationId, messageId, accountId, userId, agentName, parentAgentId, query, thought, response, queryContext string, queryConfig toolcore.NBQueryConfig) (uuid.UUID, error)
	SaveCompletedConversationAgentCall(id uuid.UUID, conversationId, messageId, accountId, userId, agentName, parentAgentId, query, thought, response, queryContext string, queryConfig toolcore.NBQueryConfig, status AgentExecutionStatus, responseSummary string) (uuid.UUID, error)
	UpdateConversationAgentResponse(agenId, response string, agentStatus AgentExecutionStatus, state string, responseSummary string, agentStepResponse string, references string) error
	UpdateConversationAgentSummary(agentId, responseSummary string) error
	GetConversationBySession(accountID, sessionID string) (Conversation, error)
	UpdateConversationTitle(conversationId, title string) error
	UpdateConversationModel(conversationId, provider, model string) error
	UpdateConversationModelBlanket(conversationId, provider, model string) error
	UpdateConversationTierOverrides(conversationId string, overrides llmruntime.ConversationTierOverrides) error
	UpdateConversationConfigSource(conversationId, configSource string) error
	ClearConversationModelConfig(conversationId string) error
	UpdateMessageAcknowledgement(messageId, accountId, ackMessage string) error
	UpdateConversationContext(conversationId string, context map[string]any) error
	GetConversation(conversationId string) (Conversation, error)
	SaveConversation(id, sessionID, tenantId, accountID, userId, context, title string, status ConversationStatus, source ConversationSource, llmProvider, llmModel string, llmTierOverrides *llmruntime.ConversationTierOverrides, llmConfigSource string, runProfile *RunProfile) (uuid.UUID, error)
	UpdateConversationStatus(conversationId string, status ConversationStatus) error
	ListConversationMessages(status ConversationStatus, workerName string, conversationId string, deadWorker bool) ([]ConversationMessage, error)
	SaveConversationMessage(id, conversationId, accountID, userId string, role MessageRole, messageType MessageType, message, response, agentName string, parentAgentId uuid.UUID, messageConfig any, messageContext string, llmProvider, llmModel string, status ConversationStatus) (uuid.UUID, error)
	GetConversationMessage(id, accountId, conversationId string) (ConversationMessage, error)
	// GetToolCallChart returns the chart a tool call of this conversation saved
	// under ref in its metadata (#40909); nil when there is none.
	GetToolCallChart(conversationID, ref string) ([]byte, error)
	// GetConversationMessageStatus reads only a message's status. A sub-agent
	// that queries a delegated target runs under that target's account, which
	// is not the message's account (see CheckMessageTerminationStatus).
	GetConversationMessageStatus(id, conversationId string) (ConversationStatus, error)
	CleanupConversationMessage(id, accountId string) error
	UpdateConversationMessageContext(messageId string, context map[string]any) error
	UpdateConversationMessage(id, response string, status ConversationStatus) error
	UpdateConversationMessageMetadata(id string, metadata map[string]any) error
	SetMessageLiveStatus(messageId, key string, status *llmruntime.LiveStatus) error
	UpdateConversationMessageAsync(id, response string, status ConversationStatus)
	UpdateMessageCompactResponse(context *security.RequestContext, messageId, compactResponse string) error
	UpdateConversationMessageMetrics(id string, metrics string) error
	ListConversationAgents(messageId, agentId string) ([]ConversationAgent, error)
	DeleteConversationToolCalls(conversationId string) error
	DeleteConversationAgents(conversationId string) error
	DeleteConversationMessages(conversationId string) error
	RetainConversationTokenUsage(conversationId string) error
	UnlinkLateTokenUsage(conversationId string) error
	DeleteConversationSaved(conversationId string) error
	DeleteConversationReferences(conversationId string) error
	DeleteConversationMemories(conversationId string) error
	DeleteConversation(conversationId string) error
	SaveConversationMessageSuggestion(messageId string, accountId string, conversationId string, messages []ConversationSuggestionMessageResponse) error
	GetConversationAgentParentAgentIdAndPreviousState(agenId string) (string, string)
	UpdateConversationMessageConfig(id string, config toolcore.NBQueryConfig) error
	UpdateConversationMessageGitRepo(id, gitRepo string) error
	UpdateConversationMessageFollowupConfig(id string, followupRequest FollowupRequest) error
	LoadConversationMessages(accountID, conversationID, userID string, requestType MessageType, k int) ([]map[string]string, error)
	ListConversationFileRefs(accountId, conversationId string, limit int) ([]FileEvidenceRef, error)
	GetConversationAgentOutput(agentId, accountId string) (string, error)
	GetConversationToolResponse(toolId, messageId, conversationId, accountId string) (string, toolcore.NBToolResponseStatus, error)
	GetConversationToolCallChildAgentId(conversationId, agenId, toolId string) string
	UpdateConversationAgentWithFollowup(agentId, followupMessageId string) error
	FindActiveSiblingToolConfigFollowup(accountId, conversationId, userMessageId, toolName string) (uuid.UUID, error)
	ListAgentsByFollowupMessageId(accountId, followupMessageId string) ([]ConversationAgent, error)
	UpdateConversationToolResponse(toolId, messageId, conversationId, accountId, response string, status toolcore.NBToolResponseStatus) error
	GetConversationMessageByAgentId(agentId string) (string, error)
	GetAgentNameFromAgentId(agentId string) (string, error)
	// GetAgentRunAttribution returns the conversation, message and user of the
	// agent run agentId, provided it is an agentName run in accountId.
	GetAgentRunAttribution(agentId, accountId, agentName string) (conversationId, messageId, userId string, err error)
	UpdateConversationAgentThought(agentId, thought string) error
	GetConversationAgentInput(agentId, accountId string) (string, error)
	SaveConversationToolCall(conversationID, accountID, userID, messageId, agenId, toolId, toolName, toolArgs, thought, toolArgsSql, toolResult string, status toolcore.NBToolResponseStatus, toolType toolcore.NBToolType, childAgentId *string, references []toolcore.NBToolResponseReference, metadata []byte, memoryRefs []byte) error
	// FinalizeInFlightToolCalls flips any tool_calls for the given message that are
	// still 'in_progress' to 'error' with a canned response. Used by handleConversationRequest's
	// finalize-on-exit defer to prevent tool_call rows from being stranded when the
	// planner turn panics or exits before the normal callback path runs. Returns nil
	// when there are no in-flight rows for the message (idempotent).
	FinalizeInFlightToolCalls(messageId string) error
	// MarkMessageFailedOnPanic performs the three-way finalize (message + in-flight
	// tool calls + conversation status) as a single ctx-cancellable operation. Used
	// by the panic-recovery defer in handleConversationRequest so a DB hang can't
	// leak the caller's goroutine holding a DB connection indefinitely — bound the
	// caller's context and every underlying Exec is cancellable. Non-atomic across
	// the three tables (independent tables, no transaction needed); each failure
	// is logged and processing continues so a partial finalize is still better than
	// none. Safe to pass empty messageId or conversationId — that half is skipped.
	MarkMessageFailedOnPanic(ctx context.Context, messageId, conversationId, panicResponse string) error
	TerminateConversation(context *security.RequestContext, accountId, conversationId string) error
	CountWaitingSubAgents(parentAgentId, messageId string) (int, error)
	MarkInProgressConversationAsKilled() error
	GetConversationOptimizationProfile(sessionID, accountID string) (OptimizationProfile, *optSavingsIndex, error)
	SaveClaimCritiqueAudit(ctx context.Context, record *ClaimCritiqueAuditRecord) error
	GetConversationWithMessages(conversationId string, accountId string) (*ConversationWithMessages, error)
	GetLatestConversationBySessionIDWithMessages(sessionID string, accountId string) (*ConversationWithMessages, error)
	ListConversations(accountId string, userId string, queryLike string, source string, limit int, offset int) ([]Conversation, error)
	SaveCritique(critique *ConversationCritique) error
	UpdateMessageProductivityMetrics(messageId string, classification string, successfulTasks int) error
	GetSuccessfulToolCallsCountByMessage(messageId string) (int, error)
	ListToolCallTraceByMessage(messageId string, maxInputChars, maxOutputChars, maxRows int) ([]ToolCallTrace, error)
	SaveAgentReferences(accountId, conversationId, messageId, agentId string, references []AgentReference) error
	ListAgentReferences(accountId, conversationId string, messageIds []string, agentId string, limit int) ([]ConversationReference, error)
	SaveConversationContextReference(accountId, conversationId, messageId string, unifiedExtraction *LlmUnifiedExtraction) error
	ClaimNextQueuedMessage(ctx context.Context, conversationID string) (*ConversationMessage, error)
	ClaimQueuedMessageById(ctx *security.RequestContext, accountID, conversationID, messageID, userID string, isAdmin bool) (*ConversationMessage, error)
	DeleteQueuedMessage(id string, accountID string, userID string, isAdmin bool) error
	UpdateQueuedMessage(id string, accountID string, userID string, message string, isAdmin bool) error
	GetQueuedConversationMessages(conversationID string) ([]ConversationMessage, error)
}

type AgentReferenceType string

const (
	AgentReferenceTypeMemory AgentReferenceType = "memory"
	AgentReferenceTypeKB     AgentReferenceType = "knowledge_base"
)

// Reference "kind" — the metadata discriminator the UI switches on. Three
// different writers persist rows with reference_type "knowledge_base": the KB
// pre-step (documents attributed to a knowledge base), the pre-step's un-owned
// path (product docs and other collections with no llm_knowledgebases row), and
// the planner's load_skills/search_skills handler (skills loaded mid-run).
// Without this the Additional Contexts panel renders all three identically, so
// a conversation that injected no KB content can still look grounded.
const (
	AgentReferenceKindKBDocument = "kb_document"
	// Documents from collections with no llm_knowledgebases row, split by the
	// scope rag-server reports so the panel names the actual origin rather than
	// lumping NudgeBee's own docs together with a customer's own content.
	AgentReferenceKindNBDocument      = "nb_document"      // global product docs
	AgentReferenceKindAccountDocument = "account_document" // legacy per-account collection
	AgentReferenceKindTenantDocument  = "tenant_document"  // tenant-level user KB
	AgentReferenceKindSkill           = "skill"
)

const (
	AgentReferenceTypeContextState AgentReferenceType = "context_state"

	// AgentReferenceTypeChannelContext is the provenance of a watched-channel
	// context block: which channel and which retained messages a Slack answer
	// drew on. Metadata is self-contained (channel, span, per-message
	// author/preview/permalink snapshotted at write time) so the citation still
	// renders after the retention sweep deletes the source rows.
	AgentReferenceTypeChannelContext AgentReferenceType = "channel_context"

	// Memory-v2 injection reference types. Written by the memory bridge for
	// every item that made it into the composed slab; the hydration SELECT in
	// GetConversationReferences joins each of these to its owning
	// llm_memory_<layer> table to surface subject/content for the UI.
	AgentReferenceTypeMemorySoul        AgentReferenceType = "memory_soul"
	AgentReferenceTypeMemoryPreferences AgentReferenceType = "memory_preferences"
	AgentReferenceTypeMemoryPatterns    AgentReferenceType = "memory_patterns"
	AgentReferenceTypeMemoryDecisions   AgentReferenceType = "memory_decisions"
	AgentReferenceTypeMemoryCollective  AgentReferenceType = "memory_collective"
	AgentReferenceTypeMemorySession     AgentReferenceType = "memory_session"
)

// MemoryReferenceSQL is the SQL ListAgentReferences uses to show memory-v2
// reference rows: CASE arms over r.reference_type, and the LEFT JOINs they read.
type MemoryReferenceSQL struct {
	ContentCases string
	Joins        string
}

// MemoryV2ReferenceHydration defaults to the subject saved on the reference row
// because the memory-v2 layer tables are EE-only; ee/memory/corebridge sets the
// version that joins them.
var MemoryV2ReferenceHydration = MemoryReferenceSQL{
	ContentCases: `WHEN r.reference_type IN ('memory_patterns','memory_decisions','memory_collective','memory_preferences') THEN COALESCE(r.metadata->>'subject', '')`,
}

type AgentReference struct {
	Type        AgentReferenceType `json:"type"`
	ReferenceID string             `json:"reference_id"`
	Metadata    map[string]any     `json:"metadata,omitempty"`
}

type ConversationReference struct {
	ID             uuid.UUID          `json:"id" db:"id"`
	AccountID      string             `json:"account_id" db:"account_id"`
	ConversationID string             `json:"conversation_id" db:"conversation_id"`
	MessageID      string             `json:"message_id" db:"message_id"`
	AgentID        uuid.UUID          `json:"agent_id" db:"agent_id"`
	ReferenceID    string             `json:"reference_id" db:"reference_id"`
	Type           AgentReferenceType `json:"type" db:"reference_type"`
	Metadata       map[string]any     `json:"metadata,omitempty" db:"metadata"`
	Content        *string            `json:"content" db:"content"`
	CreatedAt      time.Time          `json:"created_at" db:"created_at"`
	// Used is derived at read time from EXISTS on llm_conversation_tool_calls.memory_refs
	// containing this reference's position (metadata->>'position'). Point 7
	// of the memory data-audit baseline — the audit UI shows a "used ✓"
	// chip when the LLM self-attributed at least one action to this row.
	Used bool `json:"used" db:"used"`
	// UsedByAgent is the citing agent's display name (from
	// llm_conversation_agent.agent_name via r.agent_id JOIN). Empty when
	// agent_id has no matching row — never treat as authoritative on its
	// own; always show alongside Used=true so a stale/orphan agent_name
	// can't imply the ref was actually cited.
	UsedByAgent string `json:"used_by_agent,omitempty" db:"used_by_agent"`
}

type ConversationDao struct {
	dbManager *common.DatabaseManager
}

func (chat *ConversationDao) ListAgentReferences(accountId, conversationId string, messageIds []string, agentId string, limit int) ([]ConversationReference, error) {
	// Memory-v2 hydration notes:
	//   * patterns/decisions/collective/preferences are hydrated by
	//     MemoryV2ReferenceHydration: EE joins their layer tables, OSS (which
	//     has none of them) shows the subject saved on the reference row.
	//   * soul and session don't have UUID PKs (soul = (tenant,scope,user);
	//     session = session_id), so their reference_id is a composite string
	//     and we fall back to r.metadata->>'subject' — populated by the memory
	//     bridge — for the display value. The (reference_type, content) dedup
	//     below still applies.
	//   * The inner subquery pre-computes `reference_uuid` once via a
	//     regex-guarded cast. Doing this in one place instead of six
	//     defends against Postgres constant-folding the cast on non-UUID
	//     reference_ids (which would blow up the whole query with
	//     "invalid input syntax for type uuid") and keeps the JOIN clauses
	//     readable. Indexes on the joined tables' `id` columns are still
	//     used — the planner sees a plain uuid comparison.
	query := `SELECT r.id, r.account_id, r.conversation_id, r.message_id, r.agent_id, r.reference_id, r.reference_type, r.metadata::text, r.created_at,
			    CASE
					WHEN r.reference_type = 'context_state' THEN r.metadata::text
					-- channel_context is fully self-contained: the display value is
					-- the channel name stamped at write time. Without a non-empty
					-- projection the empty-content skip below would drop the row.
					WHEN r.reference_type = 'channel_context' THEN COALESCE(NULLIF(r.metadata->>'channel_name',''), r.metadata->>'channel_id')
					WHEN r.reference_type = 'memory' THEN m.content
					-- knowledge_base: prefer the KB's stored body, but fall back to
					-- the metadata stamped at write time. Integration KBs (Confluence,
					-- ServiceNow) keep their content in the vector store so k.data is
					-- empty, and runbook references ('runbook:<hash>' ids) have no KB
					-- row at all — without the fallback both were silently dropped by
					-- the empty-content skip below and never reached the Additional
					-- Contexts panel (#34779). A PDF KB's data is base64 of the
					-- document, never a displayable body, so it takes the fallback too.
					WHEN r.reference_type = 'knowledge_base' THEN COALESCE(NULLIF(CASE WHEN k.data_format = 'pdf' THEN '' ELSE k.data END,''), NULLIF(r.metadata->>'content',''), NULLIF(r.metadata->>'description',''), r.metadata->>'name')
					` + MemoryV2ReferenceHydration.ContentCases + `
					WHEN r.reference_type IN ('memory_soul','memory_session') THEN COALESCE(NULLIF(r.metadata->>'content',''), r.metadata->>'subject', '')
				END AS content,
				-- used = point 7 audit signal. Only meaningful for memory_* rows:
				-- true when at least one llm_conversation_tool_calls row on the
				-- same message_id carries this reference's position in its
				-- memory_refs jsonb. False (or trivially false) for context_state,
				-- memory (legacy), and knowledge_base — those aren't in the
				-- <memory_index> the LLM cites from.
				CASE
					-- LIKE pattern escapes the literal underscore in 'memory_%'
					-- ('_' is a single-char wildcard in LIKE) so a hypothetical
					-- 'memoryX_foo' reference_type wouldn't match. jsonb_exists()
					-- avoids the '?' operator, which some Go SQL layers rebind as
					-- a placeholder.
					WHEN r.reference_type LIKE 'memory\_%' AND r.reference_type NOT IN ('memory') AND jsonb_exists(r.metadata, 'position') THEN
						EXISTS (
							-- Uuid casts hoisted into the outer subquery
							-- (r.conversation_uuid, r.message_uuid) so this
							-- correlated EXISTS is a plain column-to-column
							-- compare — no repeated regex CASE, planner keeps
							-- b-tree lookups on tc's (conversation_id, message_id).
							-- Compare jsonb-to-jsonb via '->' (not '->>'::int) so a
							-- non-numeric position on a legacy/malformed row
							-- returns null and fails the containment cleanly
							-- instead of throwing a cast error and 500-ing the
							-- whole list. writer stores position as a JSON
							-- number, so both sides are jsonb numbers.
							SELECT 1 FROM llm_conversation_tool_calls tc
							WHERE tc.conversation_id = r.conversation_uuid
							  AND tc.message_id = r.message_uuid
							  AND tc.memory_refs @> jsonb_build_array(jsonb_build_object('position', r.metadata->'position'))
						)
					ELSE FALSE
				END AS used,
				-- used_by_agent surfaces the citing agent's display name so the
				-- UI can render a "used by <agent>" chip next to the ✓ mark.
				-- Joined off r.agent_id — the agent the memory batch was
				-- injected into — which today is always the same as the tool
				-- call's agent (memory composes per-agent-turn). LEFT JOIN so
				-- an orphan agent_id (agent row expired / rewritten) doesn't
				-- drop the reference from the result.
				COALESCE(a.agent_name, '') AS used_by_agent
			  FROM (
				-- Pre-compute all three uuid casts once, guarded by regex so a
				-- malformed legacy text value returns NULL (JOINs fail the row
				-- cleanly) instead of raising 'invalid input syntax for type
				-- uuid' and 500-ing the whole endpoint. Reused unchanged in the
				-- LEFT JOINs below AND in the correlated EXISTS for 'used'.
				SELECT *,
					CASE WHEN reference_id ~ '^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$' THEN reference_id::uuid END AS reference_uuid,
					CASE WHEN conversation_id ~ '^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$' THEN conversation_id::uuid END AS conversation_uuid,
					CASE WHEN message_id ~ '^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$' THEN message_id::uuid END AS message_uuid
				FROM llm_conversation_references
				WHERE account_id = $1::text
			  ) r
			  LEFT JOIN llm_conversation_memory m ON (r.reference_type = 'memory' AND m.id = r.reference_uuid)
			  LEFT JOIN llm_knowledgebases k ON (r.reference_type = 'knowledge_base' AND k.id = r.reference_uuid)
			  ` + MemoryV2ReferenceHydration.Joins + `
			  LEFT JOIN llm_conversation_agent a ON a.id = r.agent_id
			  WHERE 1=1`
	args := []any{accountId}
	argCounter := 2

	if conversationId != "" {
		query += fmt.Sprintf(" AND r.conversation_id = $%d::text", argCounter)
		args = append(args, conversationId)
		argCounter++
	}
	if len(messageIds) > 0 {
		query += fmt.Sprintf(" AND r.message_id = ANY($%d::text[])", argCounter)
		args = append(args, pq.Array(messageIds))
		argCounter++
	}
	if agentId != "" {
		query += fmt.Sprintf(" AND r.agent_id = $%d::uuid", argCounter)
		args = append(args, agentId)
		argCounter++
	}

	if len(messageIds) > 1 {
		// Batched calls (useMessageAdditionalData fetches several newly-completed
		// message_ids in one call) need a per-message_id cap, not one flat LIMIT
		// across the whole batch — otherwise a message with more-recently-created
		// rows can crowd another message's rows out of the result entirely. The
		// single-message_id / no-filter path below is left as a flat LIMIT
		// (unchanged) since PARTITION BY only matters once more than one
		// message_id is in play.
		query = fmt.Sprintf(`
			SELECT id, account_id, conversation_id, message_id, agent_id, reference_id, reference_type, metadata, created_at, content, used, used_by_agent
			FROM (
				SELECT ranked.*, ROW_NUMBER() OVER (PARTITION BY ranked.message_id ORDER BY ranked.created_at DESC) AS rn
				FROM (%s) ranked
			) numbered
			WHERE rn <= $%d
			ORDER BY created_at DESC`, query, argCounter)
		args = append(args, limit)
	} else {
		query += fmt.Sprintf(" ORDER BY r.created_at DESC LIMIT $%d", argCounter)
		args = append(args, limit)
	}

	rows, err := chat.dbManager.Db.Queryx(query, args...)
	if err != nil {
		return nil, fmt.Errorf("history: failed to list agent references: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			slog.Error("conversation_dao: failed to close rows", "error", err)
		}
	}()

	references := []ConversationReference{}
	// Dedup by (reference_type, content). Rows are ordered created_at DESC, so the
	// first occurrence wins and stale repeats are dropped. Also skip rows whose
	// content is NULL/empty — these arise when the LEFT JOIN to
	// llm_conversation_memory / llm_knowledgebases fails to resolve (orphaned
	// reference rows), which previously surfaced as literal "null" entries of
	// type=memory in the UI's Additional Contexts panel.
	seen := make(map[string]struct{})
	for rows.Next() {
		var ref ConversationReference
		var metaStr sql.NullString
		var content sql.NullString
		if err := rows.Scan(&ref.ID, &ref.AccountID, &ref.ConversationID, &ref.MessageID, &ref.AgentID, &ref.ReferenceID, &ref.Type, &metaStr, &ref.CreatedAt, &content, &ref.Used, &ref.UsedByAgent); err != nil {
			return nil, fmt.Errorf("history: failed to scan agent reference: %w", err)
		}
		if !content.Valid {
			continue
		}
		trimmedContent := strings.TrimSpace(content.String)
		if trimmedContent == "" {
			continue
		}
		// Memory rows additionally pass the same insert-time predicate
		// (isAcceptableMemoryFact) so legacy junk written before that filter
		// existed — literal "null"/"none"/"n/a", template fragments, etc. —
		// stops being surfaced in the panel.
		if ref.Type == AgentReferenceTypeMemory {
			if ok, _ := isAcceptableMemoryFact(MemoryFact{Content: trimmedContent}); !ok {
				continue
			}
		}
		dedupKey := string(ref.Type) + "\x00" + trimmedContent
		if _, ok := seen[dedupKey]; ok {
			continue
		}
		seen[dedupKey] = struct{}{}
		ref.Content = &trimmedContent
		if metaStr.Valid && metaStr.String != "" {
			if err := common.UnmarshalJson([]byte(metaStr.String), &ref.Metadata); err != nil {
				slog.Warn("history: failed to unmarshal agent reference metadata", "error", err, "id", ref.ID)
			}
		}
		references = append(references, ref)
	}
	return references, nil
}

func (chat *ConversationDao) SaveAgentReferences(accountId, conversationId, messageId, agentId string, references []AgentReference) error {
	if len(references) == 0 {
		return nil
	}
	// Table: llm_conversation_references
	// Columns: id, account_id, conversation_id, message_id, agent_id, reference_id, reference_type, metadata, created_at
	// Use WHERE NOT EXISTS to skip rows that already exist for the same
	// (conversation_id, message_id, reference_id, reference_type). This prevents
	// duplicate entries when the same skill propagates through a delegation chain
	// (e.g. logs → logs_default → resource_search) and each agent tries to save it.
	//
	// $7 is cast to varchar so Postgres can unify its inferred type across the
	// bare SELECT projection (which has no FROM clause to derive types from)
	// and the WHERE comparison against reference_type (a varchar column).
	// Without the cast, the planner emits 42P08 "inconsistent types deduced
	// for parameter $7" and every reference write fails silently.
	query := `INSERT INTO llm_conversation_references (id, account_id, conversation_id, message_id, agent_id, reference_id, reference_type, metadata, created_at)
		SELECT $1, $2, $3, $4, $5, $6, $7::varchar, $8, NOW()
		WHERE NOT EXISTS (
			SELECT 1 FROM llm_conversation_references
			WHERE conversation_id = $3 AND message_id = $4 AND reference_id = $6 AND reference_type = $7::varchar
		)`

	tx, err := chat.dbManager.Db.Beginx()
	if err != nil {
		return fmt.Errorf("history: failed to begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	for _, ref := range references {
		metaJson := []byte("{}")
		if len(ref.Metadata) > 0 {
			var err error
			metaJson, err = common.MarshalJson(ref.Metadata)
			if err != nil {
				slog.Error("history: failed to marshal agent reference metadata", "error", err, "reference_id", ref.ReferenceID)
				metaJson = []byte("{}")
			}
		}
		_, err = tx.Exec(query, common.GenerateUUID(), accountId, conversationId, messageId, agentId, ref.ReferenceID, string(ref.Type), string(metaJson))
		if err != nil {
			return fmt.Errorf("history: failed to save agent reference: %w", err)
		}
	}
	return tx.Commit()
}

// SaveConversationContextReference saves the LlmUnifiedExtraction to the llm_conversation_references table for debugging
func (chat *ConversationDao) SaveConversationContextReference(accountId, conversationId, messageId string, unifiedExtraction *LlmUnifiedExtraction) error {
	if unifiedExtraction == nil {
		return nil
	}

	// Serialize the unified extraction to JSON for metadata storage
	extractionJson, err := common.MarshalJson(unifiedExtraction)
	if err != nil {
		return fmt.Errorf("history: failed to marshal unified extraction: %w", err)
	}

	// Insert a new reference record for each message (for debugging history)
	insertQuery := `INSERT INTO llm_conversation_references (id, account_id, conversation_id, message_id, agent_id, reference_id, reference_type, metadata, created_at)
	               VALUES ($1, $2, $3, $4, $5, $6, $7, $8, NOW())`
	_, err = chat.dbManager.Db.Exec(insertQuery,
		common.GenerateUUID(),
		accountId,
		conversationId,
		messageId,
		uuid.Nil.String(),     // No specific agent for context state
		common.GenerateUUID(), // Use a unique UUID for reference_id
		string(AgentReferenceTypeContextState),
		string(extractionJson),
	)
	if err != nil {
		return fmt.Errorf("history: failed to save conversation context reference: %w", err)
	}

	return nil
}

func (chat *ConversationDao) SaveConversationMessageSuggestion(messageId string, accountId string, conversationId string, messages []ConversationSuggestionMessageResponse) error {
	if messageId == "" || accountId == "" || conversationId == "" {
		return errors.New("history: messageId, accountId, and conversationId are required")
	}

	if len(messages) == 0 {
		// No suggestions to save, so we can consider this a success or a no-op.
		// Depending on desired behavior, you might want to clear existing suggestions
		// or just return nil. For now, let's assume it's a no-op.
		return nil
	}

	suggestionsJSON, err := common.MarshalJson(messages)
	if err != nil {
		return fmt.Errorf("history: failed to marshal suggestions to JSON: %w", err)
	}

	query := `
    UPDATE llm_conversation_messages
    SET suggestions = $1
    WHERE id = $2 AND account_id = $3 AND conversation_id = $4;
    `
	_, err = chat.dbManager.Db.Exec(query, string(suggestionsJSON), messageId, accountId, conversationId)
	if err != nil {
		return fmt.Errorf("history: failed to update message suggestions: %w", err)
	}
	return nil
}

var (
	conversationDao      IConversationDao
	conversationDaoMutex sync.Mutex
)

func GetConversationDao() IConversationDao {
	conversationDaoMutex.Lock()
	defer conversationDaoMutex.Unlock()

	if conversationDao != nil {
		return conversationDao
	}

	dbManager2, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		slog.Error("conversation: unable to get database manager", "error", err)
		return nil
	}

	conversationDao = &ConversationDao{
		dbManager: dbManager2,
	}

	return conversationDao
}

// NewConversationDao returns a ConversationDao backed by dbManager. It is for
// packages that run their own queries on the same database and still need a
// DAO method, such as the pricing lookup.
func NewConversationDao(dbManager *common.DatabaseManager) *ConversationDao {
	return &ConversationDao{dbManager: dbManager}
}

// PeekConversationDao returns the currently-set conversation DAO singleton,
// or nil if none has been established yet. Unlike GetConversationDao, it
// never triggers a fresh DB connection attempt — for best-effort, nice-to-have
// lookups that must not pay (or block on) a first-time connection cost just
// to check something optional.
func PeekConversationDao() IConversationDao {
	conversationDaoMutex.Lock()
	defer conversationDaoMutex.Unlock()
	return conversationDao
}

func SetConversationDao(dao IConversationDao) {
	conversationDaoMutex.Lock()
	defer conversationDaoMutex.Unlock()
	conversationDao = dao
}

func (chat *ConversationDao) UpdateConversationStatus(conversationId string, status ConversationStatus) error {
	_, err := chat.dbManager.Db.Exec(`UPDATE llm_conversations SET status = $2, updated_at = now() WHERE id = $1`, conversationId, status)
	if err != nil {
		return fmt.Errorf("history: failed to update status: %w", err)
	}
	return nil
}

// FinalizeConversationMessage serializes save-back with queued claims and
// interruption. Old turns cannot overwrite the status of a forced/new turn.
func (chat *ConversationDao) FinalizeConversationMessage(conversationID, messageID, response string, status ConversationStatus) (bool, error) {
	tx, err := chat.dbManager.Db.Beginx()
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	var conversationStatus, messageStatus ConversationStatus
	if err = tx.Get(&conversationStatus, `SELECT status FROM llm_conversations WHERE id = $1 FOR UPDATE`, conversationID); err != nil {
		return false, err
	}
	if err = tx.Get(&messageStatus, `SELECT status FROM llm_conversation_messages WHERE id = $1 AND conversation_id = $2 FOR UPDATE`, messageID, conversationID); err != nil {
		return false, err
	}
	if conversationStatus == ConversationStatusKilled || messageStatus == ConversationStatusTerminated || messageStatus == ConversationStatusKilled {
		return false, nil
	}
	_, err = tx.Exec(`UPDATE llm_conversation_messages SET response = $2, updated_at = now(), status = $3, worker_name = $4, responded_at = CASE WHEN $5::boolean THEN COALESCE(responded_at, now()) ELSE NULL END WHERE id = $1`, messageID, response, status, config.Config.ServerName, IsTerminalConversationStatus(status))
	if err != nil {
		return false, err
	}
	// Cast the shared status parameter in the predicate: otherwise PostgreSQL
	// infers text from NOT IN before resolving the enum assignment in SET.
	_, err = tx.Exec(`UPDATE llm_conversations SET status = $2, updated_at = now() WHERE id = $1 AND NOT EXISTS (
		SELECT 1 FROM llm_conversation_messages WHERE conversation_id = $1 AND id <> $3 AND status IN ('IN_PROGRESS', 'WAITING', 'WAITING_FOR_CLIENT_TOOL', 'PENDING')
 AND ($2::public.llm_conversation_status NOT IN ('WAITING', 'WAITING_FOR_CLIENT_TOOL') OR message_type = 'generation'))`, conversationID, status, messageID)
	if err != nil {
		return false, err
	}
	if err = tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

// UpdateConversationTitle updates the title of a conversation
func (chat *ConversationDao) UpdateConversationTitle(conversationId, title string) error {
	if conversationId == "" {
		return errors.New("history: conversationId is required")
	}

	_, err := chat.dbManager.Exec(`
		UPDATE llm_conversations 
		SET title = $1
		WHERE id = $2
	`, title, conversationId)

	if err != nil {
		slog.Error("history: unable to update conversation title", "error", err)
		return errors.New("history: unable to update conversation title")
	}

	return nil
}

// Clears llm_tier_overrides in the same UPDATE — blanket and tier modes are
// mutually exclusive at the row level.
func (chat *ConversationDao) UpdateConversationModelBlanket(conversationId, provider, model string) error {
	if conversationId == "" {
		return errors.New("history: conversationId is required")
	}

	query := `UPDATE llm_conversations
	          SET llm_provider = $2, llm_model = $3,
	              llm_tier_overrides = NULL,
	              updated_at = now()
	          WHERE id = $1`
	_, err := chat.dbManager.Db.Exec(query, conversationId, provider, model)
	if err != nil {
		slog.Error("history: unable to update conversation model (blanket)", "error", err)
		return fmt.Errorf("history: unable to update conversation model (blanket): %w", err)
	}
	return nil
}

// UpdateConversationConfigSource persists the pinned config slot. Deliberately
// does NOT touch llm_provider / llm_model / llm_tier_overrides: a pin selects
// which configured slot serves the request, which is independent of which model
// the user picked, so the blanket-vs-tier exclusivity above doesn't apply here.
// An empty source clears the pin.
func (chat *ConversationDao) UpdateConversationConfigSource(conversationId, configSource string) error {
	if conversationId == "" {
		return errors.New("history: conversationId is required")
	}

	query := `UPDATE llm_conversations
	          SET llm_config_source = $2,
	              updated_at = now()
	          WHERE id = $1`
	_, err := chat.dbManager.Db.Exec(query, conversationId, sql.NullString{String: configSource, Valid: configSource != ""})
	if err != nil {
		// No log here: applyConversationModelConfig warns with the request
		// context attached (trace_id, conversation_id), which this bare slog
		// call lacks — and it treats the write as best-effort, so an Error-level
		// line would alert on something already handled.
		return fmt.Errorf("history: unable to update conversation config source: %w", err)
	}
	return nil
}

// ClearConversationModelConfig drops every stored model choice at once —
// blanket, per-tier and pin. One UPDATE rather than three calls because the
// three columns are one user-visible selection: a partial clear would leave the
// conversation in a state the picker has no way to show.
func (chat *ConversationDao) ClearConversationModelConfig(conversationId string) error {
	if conversationId == "" {
		return errors.New("history: conversationId is required")
	}

	query := `UPDATE llm_conversations
	          SET llm_provider = NULL, llm_model = NULL,
	              llm_tier_overrides = NULL, llm_config_source = NULL,
	              updated_at = now()
	          WHERE id = $1`
	_, err := chat.dbManager.Db.Exec(query, conversationId)
	if err != nil {
		// See UpdateConversationConfigSource — the caller logs with context.
		return fmt.Errorf("history: unable to clear conversation model config: %w", err)
	}
	return nil
}

// Clears llm_provider and llm_model in the same UPDATE — see
// UpdateConversationModelBlanket for the mutual-exclusivity rule.
func (chat *ConversationDao) UpdateConversationTierOverrides(conversationId string, overrides llmruntime.ConversationTierOverrides) error {
	if conversationId == "" {
		return errors.New("history: conversationId is required")
	}

	query := `UPDATE llm_conversations
	          SET llm_provider = NULL, llm_model = NULL,
	              llm_tier_overrides = $2,
	              updated_at = now()
	          WHERE id = $1`
	_, err := chat.dbManager.Db.Exec(query, conversationId, overrides)
	if err != nil {
		slog.Error("history: unable to update conversation tier overrides", "error", err)
		return fmt.Errorf("history: unable to update conversation tier overrides: %w", err)
	}
	return nil
}

// Deprecated: use UpdateConversationModelBlanket directly.
func (chat *ConversationDao) UpdateConversationModel(conversationId, provider, model string) error {
	return chat.UpdateConversationModelBlanket(conversationId, provider, model)
}

const saveConversationQueryWithUser = `
    INSERT INTO llm_conversations (id, session_id, tenant_id, account_id, user_id, context, status, source, title, updated_at, llm_provider, llm_model, llm_tier_overrides, llm_config_source, run_profile)
    VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, now(), $10, $11, $12, $13, $14)
    ON CONFLICT (session_id, user_id, account_id)
    DO UPDATE SET context = EXCLUDED.context, status = EXCLUDED.status, updated_at = EXCLUDED.updated_at, llm_provider = EXCLUDED.llm_provider, llm_model = EXCLUDED.llm_model, llm_tier_overrides = EXCLUDED.llm_tier_overrides, llm_config_source = EXCLUDED.llm_config_source RETURNING llm_conversations.id;`

// run_profile is written on INSERT only: it is in neither DO UPDATE SET list,
// so no later save sets, changes or clears a run's identity.
//
// Postgres treats NULL <> NULL, so a NULL user_id needs its own conflict
// target: the partial unique index from migration V904 (GH #37367).
const saveConversationQueryNullUser = `
    INSERT INTO llm_conversations (id, session_id, tenant_id, account_id, user_id, context, status, source, title, updated_at, llm_provider, llm_model, llm_tier_overrides, llm_config_source, run_profile)
    VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, now(), $10, $11, $12, $13, $14)
    ON CONFLICT (session_id, account_id) WHERE user_id IS NULL
    DO UPDATE SET context = EXCLUDED.context, status = EXCLUDED.status, updated_at = EXCLUDED.updated_at, llm_provider = EXCLUDED.llm_provider, llm_model = EXCLUDED.llm_model, llm_tier_overrides = EXCLUDED.llm_tier_overrides, llm_config_source = EXCLUDED.llm_config_source RETURNING llm_conversations.id;`

func (chat *ConversationDao) SaveConversation(id, sessionID, tenantId, accountID, userId, context, title string, status ConversationStatus, source ConversationSource, llmProvider, llmModel string, llmTierOverrides *llmruntime.ConversationTierOverrides, llmConfigSource string, runProfile *RunProfile) (uuid.UUID, error) {
	if accountID == "" || tenantId == "" {
		return uuid.Nil, errors.New("history: accountID and tenantId are required")
	}
	t0 := time.Now()
	if id == "" {
		id = common.GenerateUUID()
	}

	if context != "" {
		context = pq.QuoteLiteral(context)
	}

	userIdSql := sql.NullString{
		String: userId,
		Valid:  userId != "" && userId != uuid.Nil.String(),
	}

	llmProviderSql := sql.NullString{
		String: llmProvider,
		Valid:  llmProvider != "",
	}

	llmModelSql := sql.NullString{
		String: llmModel,
		Valid:  llmModel != "",
	}

	llmConfigSourceSql := sql.NullString{
		String: llmConfigSource,
		Valid:  llmConfigSource != "",
	}

	var tierOverridesArg any
	if llmTierOverrides != nil {
		tierOverridesArg = *llmTierOverrides
	}

	query := saveConversationQueryWithUser
	if !userIdSql.Valid {
		query = saveConversationQueryNullUser
	}
	var lastId uuid.UUID
	var runProfileArg any
	if runProfile != nil {
		runProfileArg = *runProfile
	}
	err := chat.dbManager.Db.QueryRow(query, id, sessionID, tenantId, accountID, userIdSql, context, status, source, title, llmProviderSql, llmModelSql, tierOverridesArg, llmConfigSourceSql, runProfileArg).Scan(&lastId)
	if err != nil {
		return uuid.Nil, fmt.Errorf("history: failed to save conversation: %w", err)
	}
	slog.Info("dao: SaveConversation complete", "duration", time.Since(t0).String(), "account_id", accountID, "session_id", sessionID)
	return lastId, nil
}

func (chat *ConversationDao) SaveConversationAgentCall(conversationId, messageId, accountId, userId, agentName, parentAgentId, query, thought, response, queryContext string, queryConfig toolcore.NBQueryConfig) (uuid.UUID, error) {
	t0 := time.Now()
	if parentAgentId == "" {
		parentAgentId = uuid.Nil.String()
	}
	queryConfigString := sql.NullString{}
	if !queryConfig.IsEmpty() {
		data, err := common.MarshalJson(queryConfig)
		if err != nil {
			return uuid.Nil, fmt.Errorf("history: failed to marshal query config: %w", err)
		}
		queryConfigString.String = string(data)
		queryConfigString.Valid = true
	}
	userIdSql := sql.NullString{
		String: userId,
		Valid:  userId != "" && userId != uuid.Nil.String(),
	}

	agentQuery := `
	INSERT INTO llm_conversation_agent (conversation_id, message_id, account_id, user_id, agent_name, parent_agent_id, query, response, thought, query_context, query_config, status) 
	VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12) RETURNING llm_conversation_agent.id;`
	var lastId uuid.UUID
	err := chat.dbManager.Db.QueryRow(agentQuery, conversationId, messageId, accountId, userIdSql, agentName, parentAgentId, query, response, thought, queryContext, queryConfigString, string(AgentExecutionStatusInProgress)).Scan(&lastId)
	if err != nil {
		return uuid.Nil, fmt.Errorf("history: failed to save agent call: %w", err)
	}
	slog.Info("dao: SaveConversationAgentCall complete", "duration", time.Since(t0).String(), "agent", agentName)
	return lastId, nil
}

func (chat *ConversationDao) SaveCompletedConversationAgentCall(id uuid.UUID, conversationId, messageId, accountId, userId, agentName, parentAgentId, query, thought, response, queryContext string, queryConfig toolcore.NBQueryConfig, status AgentExecutionStatus, responseSummary string) (uuid.UUID, error) {
	t0 := time.Now()
	if parentAgentId == "" {
		parentAgentId = uuid.Nil.String()
	}
	queryConfigString := sql.NullString{}
	if !queryConfig.IsEmpty() {
		data, err := common.MarshalJson(queryConfig)
		if err != nil {
			return uuid.Nil, fmt.Errorf("history: failed to marshal query config: %w", err)
		}
		queryConfigString.String = string(data)
		queryConfigString.Valid = true
	}
	userIdSql := sql.NullString{
		String: userId,
		Valid:  userId != "" && userId != uuid.Nil.String(),
	}

	if len(responseSummary) > 250 {
		responseSummary = responseSummary[:250]
	}

	if id == uuid.Nil {
		id = uuid.New()
	}

	agentQuery := `
	INSERT INTO llm_conversation_agent (id, conversation_id, message_id, account_id, user_id, agent_name, parent_agent_id, query, response, thought, query_context, query_config, status, response_summary) 
	VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14) RETURNING llm_conversation_agent.id;`
	var lastId uuid.UUID
	err := chat.dbManager.Db.QueryRow(agentQuery, id, conversationId, messageId, accountId, userIdSql, agentName, parentAgentId, query, response, thought, queryContext, queryConfigString, string(status), responseSummary).Scan(&lastId)
	if err != nil {
		return uuid.Nil, fmt.Errorf("history: failed to save completed agent call: %w", err)
	}
	slog.Info("dao: SaveCompletedConversationAgentCall complete", "duration", time.Since(t0).String(), "agent", agentName)
	return lastId, nil
}

func (chat *ConversationDao) UpdateConversationAgentResponse(agenId, response string, agentStatus AgentExecutionStatus, state string, responseSummary string, agentStepResponse string, references string) error {
	if len(responseSummary) > 250 {
		responseSummary = responseSummary[:250]
	}

	// Termination override: if the parent message has been terminated, neutralize
	// this late writeback. Without this, a row inserted *after* TerminateConversation
	// ran (its bulk UPDATE only catches rows that existed at the time) would still
	// be in 'in_progress' and the existing guard would let the natural response
	// through. Look up message context once via the agent row, then consult the
	// termination cache (fast path) — falls through silently on lookup error so we
	// never block a normal write because of a transient read failure.
	var msgId, acctId, convId string
	if scanErr := chat.dbManager.Db.QueryRow(
		`SELECT message_id::text, account_id::text, conversation_id::text FROM llm_conversation_agent WHERE id = $1`,
		agenId,
	).Scan(&msgId, &acctId, &convId); scanErr != nil {
		slog.Debug("history: termination-check lookup failed (proceeding without override)", "agent_id", agenId, "error", scanErr)
	} else if terminated, termErr := checkMessageTerminationStatus(msgId, acctId, convId); termErr != nil {
		slog.Debug("history: termination cache lookup failed (proceeding without override)", "message_id", msgId, "error", termErr)
	} else if terminated {
		response = "Terminated by user"
		agentStatus = AgentExecutionStatusTerminated
	}

	// Guard against overwriting terminal rows. When a conversation is terminated,
	// TerminateConversation flips in-progress agent rows to 'terminated' with a
	// marker response. A still-running executor goroutine must not clobber that
	// state when it eventually finishes its blocked LLM/tool call.
	agentQuery := `UPDATE llm_conversation_agent SET response = $2, updated_at = now(), status = $3, state = $4, response_summary = $5, agent_step_response = $6, "references" = $7 WHERE id = $1 AND status NOT IN ('terminated','fail', 'success')`
	res, err := chat.dbManager.Db.Exec(agentQuery, agenId, response, agentStatus, state, responseSummary, agentStepResponse, references)
	if err != nil {
		return fmt.Errorf("history: failed to update agent call: %w", err)
	}

	// Sweep waiting ancestors. When a delegated child completes to a terminal
	// state on a later message than its own spawn (the "resume" case — user
	// answered a followup that a child sub-agent had emitted), the ancestor
	// rows that entered Waiting when the followup was raised never get flipped
	// back to a terminal state — their status ROW is frozen because the resume
	// dispatches directly to the child by followup_message_id and never
	// re-runs the ancestor. Prod evidence: conv e9826443 (2026-08-07) —
	// k8s_orchestrator_lean + delegate_agent stayed 'waiting' 22h after the
	// child (automation_builder) successfully handled 4 subsequent messages.
	//
	// The functional impact today is nil (no UI/metric consumes agent.status
	// = 'waiting'), so this is cosmetic bookkeeping. Kept the sweep best-effort
	// (log on failure, don't return err) so a transient DB blip on the sweep
	// never turns a successful primary agent completion into a caller-visible
	// failure. Only runs when THIS row's new status is a terminal
	// success/fail — no sweep for waiting→waiting or in_progress transitions.
	//
	// Gate ALSO on rows-affected > 0. The primary UPDATE has a terminal-guard
	// (`status NOT IN ('terminated','fail','success')`) that no-ops when the
	// child is already in a terminal state from another path (e.g. terminated
	// by user, or a concurrent goroutine wrote 'fail' first). In that case the
	// child's status is NOT what this call intended (agentStatus), so sweeping
	// ancestors with agentStatus would spread the wrong outcome upward — a
	// success sweep after a lost race against a 'fail' write would flag the
	// parents 'success' even though the actual child outcome was 'fail'.
	if agentStatus == AgentExecutionStatusSuccess || agentStatus == AgentExecutionStatusFail {
		// RowsAffected may error on some drivers; on error we conservatively
		// skip the sweep rather than risk propagating a stale intent.
		rowsAffected, raErr := res.RowsAffected()
		if raErr != nil {
			slog.Warn("history: RowsAffected failed on primary write — skipping ancestor sweep to avoid stale propagation",
				"agent_id", agenId, "error", raErr)
		} else if rowsAffected > 0 {
			if sweepErr := chat.markWaitingAncestorsTerminal(agenId, agentStatus); sweepErr != nil {
				slog.Warn("history: waiting-ancestors sweep failed (non-fatal — primary write already committed)",
					"agent_id", agenId, "new_status", agentStatus, "error", sweepErr)
			}
		}
	}
	return nil
}

// UpdateConversationNotebook updates the synthetic notebook agent row after it
// has already reached success. The generic agent response updater intentionally
// rejects terminal rows, so notebook replacements need this narrower contract.
// The agent-name predicate prevents this escape hatch from mutating ordinary
// completed agent calls.
func (chat *ConversationDao) UpdateConversationNotebook(agentID, response, responseSummary string) error {
	if len(responseSummary) > 250 {
		responseSummary = common.TruncateHead(responseSummary, 250)
	}
	_, err := chat.dbManager.DoInTransaction(func(tx *sqlx.Tx) (any, error) {
		var messageStatus, conversationStatus string
		if err := tx.QueryRow(
			`SELECT m.status, c.status
			 FROM llm_conversation_agent AS a
			 JOIN llm_conversation_messages AS m ON m.id = a.message_id
			 JOIN llm_conversations AS c ON c.id = m.conversation_id
			 WHERE a.id = $1 AND a.agent_name = $2
			 FOR UPDATE OF m, c`,
			agentID, notebookDummyAgent,
		).Scan(&messageStatus, &conversationStatus); err != nil {
			return nil, fmt.Errorf("history: failed to lock notebook parent state: %w", err)
		}
		if strings.EqualFold(messageStatus, string(ConversationStatusTerminated)) ||
			strings.EqualFold(conversationStatus, string(ConversationStatusKilled)) {
			return nil, errors.New("history: notebook parent is terminated")
		}

		result, err := tx.Exec(
			`UPDATE llm_conversation_agent
			 SET response = $2, response_summary = $3, updated_at = now()
			 WHERE id = $1 AND agent_name = $4 AND status = 'success'`,
			agentID, response, responseSummary, notebookDummyAgent,
		)
		if err != nil {
			return nil, fmt.Errorf("history: failed to update notebook agent: %w", err)
		}
		rowsAffected, err := result.RowsAffected()
		if err != nil {
			return nil, fmt.Errorf("history: failed to verify notebook agent update: %w", err)
		}
		if rowsAffected != 1 {
			return nil, fmt.Errorf("history: notebook agent update affected %d rows", rowsAffected)
		}
		return nil, nil
	})
	return err
}

// UpdateConversationNotebookState stores the structured plan JSON
// (update_notebook_v2, #41443) on the synthetic notebook row's state column,
// which the chat poll reads to draw the plan card. updated_at is bumped so the
// delta poll picks the change up.
func (chat *ConversationDao) UpdateConversationNotebookState(agentID, state string) error {
	result, err := chat.dbManager.Db.Exec(
		`UPDATE llm_conversation_agent
		 SET state = $2, updated_at = now()
		 WHERE id = $1 AND agent_name = $3 AND status = 'success'`,
		agentID, state, notebookDummyAgent,
	)
	if err != nil {
		return fmt.Errorf("history: failed to update notebook state: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("history: failed to verify notebook state update: %w", err)
	}
	if rowsAffected != 1 {
		return fmt.Errorf("history: notebook state update affected %d rows", rowsAffected)
	}
	return nil
}

// maxWaitingAncestorSweepHops caps how far up the parent chain the sweep walks.
// Real chains are 1–2 hops (orchestrator → delegate_agent → child); the cap is
// a runaway-loop backstop for a corrupted parent_agent_id chain. Sized to match
// the sibling walk in ResolveAgentByConversationAgentId.
const maxWaitingAncestorSweepHops = 8

// markWaitingAncestorsTerminal walks parent_agent_id up from the given agent
// and flips any ancestor rows still in 'waiting' to the same terminal status.
// Stops at the first non-waiting ancestor (already-completed rows above stay
// as-is — the walk-up is only meant to clean up rows that entered Waiting
// waiting for THIS descendant's followup). Uses a recursive CTE so we scan
// the chain in a single round trip rather than N iterative SELECTs.
//
// Best-effort: any error is returned to the caller which logs and swallows.
// The caller has already committed the primary agent-status write; a sweep
// failure never rolls that back.
func (chat *ConversationDao) markWaitingAncestorsTerminal(childAgentId string, terminalStatus AgentExecutionStatus) error {
	// Recursive CTE walks parent_agent_id up from the child, capping at
	// maxWaitingAncestorSweepHops (defense against a corrupted chain).
	// The outer UPDATE only touches rows whose CURRENT status is 'waiting'
	// — matching the "row was parked waiting for this followup" invariant
	// the fix is designed to clean up. Already-terminal ancestors
	// (success/fail/terminated) are left untouched.
	// This codebase persists top-level agents with parent_agent_id =
	// '00000000-0000-0000-0000-000000000000' (the nil UUID), NOT NULL.
	// DB audit 2026-08-08: nil-UUID=3850 rows, NULL=0 rows, real-parent=1832
	// over 7d. Both the anchor and recursive-step guards must exclude
	// nil-UUID or the CTE walks past the real root, wasting a lookup and
	// (worst case, if a stray row with id=nil-UUID exists and is Waiting)
	// flipping it to terminal by mistake.
	query := `
WITH RECURSIVE ancestors(id, hops) AS (
  SELECT parent_agent_id, 1
    FROM llm_conversation_agent
   WHERE id = $1 AND parent_agent_id IS NOT NULL
     AND parent_agent_id <> '00000000-0000-0000-0000-000000000000'
  UNION ALL
  SELECT lca.parent_agent_id, a.hops + 1
    FROM llm_conversation_agent lca
    JOIN ancestors a ON lca.id = a.id
   WHERE a.hops < $3 AND lca.parent_agent_id IS NOT NULL
     AND lca.parent_agent_id <> '00000000-0000-0000-0000-000000000000'
)
UPDATE llm_conversation_agent
   SET status = $2, updated_at = now()
 WHERE id IN (SELECT id FROM ancestors)
   AND status = 'waiting'`
	_, err := chat.dbManager.Db.Exec(query, childAgentId, string(terminalStatus), maxWaitingAncestorSweepHops)
	if err != nil {
		return fmt.Errorf("markWaitingAncestorsTerminal: %w", err)
	}
	return nil
}

func (chat *ConversationDao) UpdateConversationAgentSummary(agentId, responseSummary string) error {
	if len(responseSummary) > 250 {
		responseSummary = responseSummary[:250]
	}
	// Same terminal-row guard as UpdateConversationAgentResponse — a goroutine that
	// finishes after termination must not touch a row already marked 'fail'/'success'.
	agentQuery := `UPDATE llm_conversation_agent SET response_summary = $2 WHERE id = $1 AND status NOT IN ('terminated','fail', 'success')`
	_, err := chat.dbManager.Db.Exec(agentQuery, agentId, responseSummary)
	if err != nil {
		return fmt.Errorf("history: failed to update agent summary: %w", err)
	}
	return nil
}

func (chat *ConversationDao) UpdateConversationAgentWithFollowup(agentId, followupMessageId string) error {
	// Critical guard: without this, a late followup-write flips status from
	// 'terminated' (set by termination) back to 'waiting', which then re-opens
	// the row to a subsequent UpdateConversationAgentResponse — clobbering
	// "Terminated by user".
	agentQuery := `UPDATE llm_conversation_agent SET followup_message_id = $2, updated_at = now(), status = $3 WHERE id = $1 AND status NOT IN ('terminated','fail', 'success')`
	_, err := chat.dbManager.Db.Exec(agentQuery, agentId, followupMessageId, AgentExecutionStatusWaiting)
	if err != nil {
		return fmt.Errorf("history: failed to update agent followup call: %w", err)
	}
	return nil
}

// FindActiveSiblingToolConfigFollowup returns the message_id of any non-terminal
// tool_config followup raised by ANY sub-agent under the given user message_id,
// matching the given toolName. Returns uuid.Nil if no such followup exists.
//
// Used by GenerateFollowup to collapse parallel sibling agents' followups onto
// a single message row so the UI shows one card instead of 2-3 duplicates
// (#30515).
//
// Correlation key: `message_context::jsonb ->> 'message_id'`. SaveConversationMessage
// writes message_context = MarshalJson(NBAgentRequest), and the request struct
// carries the originating user message_id. This is the FIRST DB write in the
// followup-creation flow — visible to other goroutines before the agent-bind
// (UpdateConversationAgentWithFollowup) commits. An earlier version of this
// query joined through llm_conversation_agent.followup_message_id, but that
// JOIN had a race window: sibling B's lookup could fire AFTER sibling A's
// INSERT committed but BEFORE A's bind committed, finding the followup
// message without any agent yet bound → JOIN empty → dedup missed.
//
// Status filter excludes terminal states (COMPLETED, FAILED, KILLED,
// TERMINATED). A previously-resolved tool_config followup is fair game to
// create fresh — typically only on a stale or re-asked conversation.
func (chat *ConversationDao) FindActiveSiblingToolConfigFollowup(
	accountId, conversationId, userMessageId, toolName string,
) (uuid.UUID, error) {
	var id uuid.UUID
	err := chat.dbManager.Db.Get(&id, `
		SELECT id FROM llm_conversation_messages
		WHERE account_id = $1
		  AND conversation_id = $2
		  AND message_context::jsonb ->> 'message_id' = $3
		  AND message_type = 'followup'
		  AND status NOT IN ('COMPLETED', 'FAILED', 'KILLED', 'TERMINATED')
		  AND message_config::jsonb ->> 'followupType' = 'tool_config'
		  AND message_config::jsonb ->> 'toolName' = $4
		ORDER BY created_at ASC
		LIMIT 1
	`, accountId, conversationId, userMessageId, toolName)
	if errors.Is(err, sql.ErrNoRows) {
		return uuid.Nil, nil
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("history: find active sibling tool_config followup: %w", err)
	}
	return id, nil
}

// ListAgentsByFollowupMessageId returns every agent whose followup_message_id
// equals the given id. Used by HandleFollowupAndResumeV2 to walk all sibling
// agents bound to a single tool_config followup after the user picks an answer
// (#30515 Part 2). Without this, only the agent the UI's resume request
// targets gets unblocked; bound siblings stay in 'waiting' status and the
// parent's bubble-up never fires.
func (chat *ConversationDao) ListAgentsByFollowupMessageId(
	accountId, followupMessageId string,
) ([]ConversationAgent, error) {
	var rows []ConversationAgent
	err := chat.dbManager.Db.Select(&rows, `
		SELECT id, message_id, parent_agent_id, conversation_id, agent_name,
		       status, followup_message_id, query, state
		FROM llm_conversation_agent
		WHERE account_id = $1
		  AND followup_message_id = $2
		ORDER BY created_at ASC
	`, accountId, followupMessageId)
	if err != nil {
		return nil, fmt.Errorf("history: list agents by followup_message_id: %w", err)
	}
	return rows, nil
}

func (chat *ConversationDao) UpdateConversationAgentThought(agentId, thought string) error {
	agentQuery := `UPDATE llm_conversation_agent SET thought = $2 WHERE id = $1 AND status NOT IN ('terminated','fail', 'success')`
	_, err := chat.dbManager.Db.Exec(agentQuery, agentId, thought)
	if err != nil {
		return fmt.Errorf("history: failed to save agent thought: %w", err)
	}
	return nil
}

func (chat *ConversationDao) GetConversationMessageByAgentId(agentId string) (string, error) {
	query := `SELECT lcm.message from llm_conversation_agent lca
	JOIN llm_conversation_messages lcm ON lcm.id = lca.message_id
	WHERE lca.id = $1`
	row := chat.dbManager.Db.QueryRowx(query, agentId)
	if row == nil || row.Err() != nil {
		return "", fmt.Errorf("history: failed to get message by agent id: %w", row.Err())
	}
	var message string
	err := row.Scan(&message)
	if err != nil {
		return "", fmt.Errorf("history: failed to scan message : %w", err)
	}
	return message, nil
}

func (chat *ConversationDao) GetAgentNameFromAgentId(agentId string) (string, error) {
	query := `SELECT agent_name from llm_conversation_agent lca WHERE lca.id=$1`
	row := chat.dbManager.Db.QueryRowx(query, agentId)
	if row == nil || row.Err() != nil {
		return "", fmt.Errorf("history: failed to get agent name by agent id: %w", row.Err())
	}
	var agentName string
	err := row.Scan(&agentName)
	if err != nil {
		return "", fmt.Errorf("history: failed to scan agent name : %w", err)
	}
	return agentName, nil
}

func (chat *ConversationDao) GetAgentRunAttribution(agentId, accountId, agentName string) (string, string, string, error) {
	query := `SELECT conversation_id::text, message_id::text, COALESCE(user_id::text, '') FROM llm_conversation_agent WHERE id = $1 AND account_id = $2 AND agent_name = $3`
	var conversationId, messageId *string
	var userId string
	if err := chat.dbManager.Db.QueryRow(query, agentId, accountId, agentName).Scan(&conversationId, &messageId, &userId); err != nil {
		return "", "", "", fmt.Errorf("history: failed to get agent run attribution: %w", err)
	}
	if conversationId == nil || messageId == nil {
		return "", "", "", fmt.Errorf("history: agent run %s has no conversation or message", agentId)
	}
	return *conversationId, *messageId, userId, nil
}

func (chat *ConversationDao) GetConversationAgentParentAgentIdAndPreviousState(agenId string) (string, string) {
	agentQuery := `SELECT parent_agent_id::text, state FROM llm_conversation_agent WHERE id = $1`
	var parentAgentId, state sql.NullString
	err := chat.dbManager.Db.QueryRow(agentQuery, agenId).Scan(&parentAgentId, &state)
	if err != nil {
		return "", ""
	}
	return parentAgentId.String, state.String
}

func (chat *ConversationDao) GetConversationBySession(accountID, sessionID string) (Conversation, error) {
	if accountID == "" {
		return Conversation{}, errors.New("history: accountID is required")
	}
	query := `SELECT id, user_id, session_id, account_id, context::text, status, tenant_id::text, title, source, llm_provider, llm_model, llm_tier_overrides, llm_config_source, run_profile FROM llm_conversations
	WHERE session_id = $1 AND account_id = $2
	ORDER BY updated_at DESC LIMIT 1`
	rows, err := chat.dbManager.Db.Queryx(query, sessionID, accountID)
	if err != nil {
		return Conversation{}, fmt.Errorf("history: failed to load conversation: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			slog.Error("conversation_dao: failed to close rows", "error", err)
		}
	}()
	var conversation Conversation
	var id, sessionId, accoundId, tenantId string
	var userId *string
	var title *string
	var status ConversationStatus
	var source *ConversationSource
	var cntxt sql.NullString
	var llmProvider, llmModel, llmConfigSource *string
	var tierOverrides llmruntime.ConversationTierOverrides
	var runProfile nullRunProfile
	for rows.Next() {
		if err := rows.Scan(&id, &userId, &sessionId, &accoundId, &cntxt, &status, &tenantId, &title, &source, &llmProvider, &llmModel, &tierOverrides, &llmConfigSource, &runProfile); err != nil {
			return Conversation{}, fmt.Errorf("history: failed to scan conversation: %w", err)
		}
		var context map[string]any
		if cntxt.String != "" {
			cntxtStr := strings.Trim(cntxt.String, "'")
			if err := common.UnmarshalJson([]byte(cntxtStr), &context); err != nil {
				return Conversation{}, fmt.Errorf("history: failed to unmarshal context: %w", err)
			}
		}
		userIdUUID := uuid.Nil
		if userId != nil {
			userIdUUID = uuid.MustParse(*userId)
		}
		conversation = Conversation{
			ID:              uuid.MustParse(id),
			SessionID:       sessionId,
			AccountID:       uuid.MustParse(accoundId),
			UserID:          userIdUUID,
			Context:         context,
			Status:          status,
			TenantID:        uuid.MustParse(tenantId),
			Title:           title,
			Source:          source,
			LlmProvider:     llmProvider,
			LlmModel:        llmModel,
			LlmConfigSource: llmConfigSource,
		}
		if tierOverrides.HasAny() {
			to := tierOverrides
			conversation.LlmTierOverrides = &to
		}
		conversation.RunProfile = runProfile.profile()
	}
	if err := rows.Err(); err != nil {
		return Conversation{}, fmt.Errorf("history: failed to iterate over rows: %w", err)
	}

	return conversation, nil
}

func (chat *ConversationDao) GetConversation(conversationId string) (Conversation, error) {
	query := `SELECT id::text, user_id, session_id, account_id, context::text, status, tenant_id::text, title, source, llm_provider, llm_model, llm_tier_overrides, llm_config_source, run_profile FROM llm_conversations WHERE id = $1`
	rows, err := chat.dbManager.Db.Queryx(query, conversationId)
	if err != nil {
		return Conversation{}, fmt.Errorf("history: failed to load conversation: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			slog.Error("conversation_dao: failed to close rows", "error", err)
		}
	}()
	var conversation Conversation
	var id, sessionId, accoundId, tenantId string
	var title, userId *string
	var status ConversationStatus
	var source *ConversationSource
	var cntxt sql.NullString
	var llmProvider, llmModel, llmConfigSource *string
	var tierOverrides llmruntime.ConversationTierOverrides
	var runProfile nullRunProfile
	for rows.Next() {
		if err := rows.Scan(&id, &userId, &sessionId, &accoundId, &cntxt, &status, &tenantId, &title, &source, &llmProvider, &llmModel, &tierOverrides, &llmConfigSource, &runProfile); err != nil {
			return Conversation{}, fmt.Errorf("history: failed to scan conversation: %w", err)
		}
		var context map[string]any
		if cntxt.String != "" {
			cntxtStr := strings.Trim(cntxt.String, "'")
			if err := common.UnmarshalJson([]byte(cntxtStr), &context); err != nil {
				return Conversation{}, fmt.Errorf("history: failed to unmarshal context: %w", err)
			}
		}

		userIdUUID := uuid.Nil
		if userId != nil {
			userIdUUID = uuid.MustParse(*userId)
		}
		conversation = Conversation{
			ID:              uuid.MustParse(id),
			SessionID:       sessionId,
			AccountID:       uuid.MustParse(accoundId),
			UserID:          userIdUUID,
			Context:         context,
			Status:          status,
			TenantID:        uuid.MustParse(tenantId),
			Title:           title,
			Source:          source,
			LlmProvider:     llmProvider,
			LlmModel:        llmModel,
			LlmConfigSource: llmConfigSource,
		}
		if tierOverrides.HasAny() {
			to := tierOverrides
			conversation.LlmTierOverrides = &to
		}
		conversation.RunProfile = runProfile.profile()
	}

	if err := rows.Err(); err != nil {
		return Conversation{}, fmt.Errorf("history: failed to iterate over rows: %w", err)
	}

	if conversation.ID == uuid.Nil {
		return Conversation{}, ErrConversationNotFound
	}
	return conversation, nil
}

// CountWaitingSubAgents returns the number of sub-agents under the given parent
// (for a specific message) whose status is waiting or in_progress. Used by
// resume_v2 bubble-up to decide whether the parent can proceed.
// The parent itself is excluded via `id != $1`.
func (chat *ConversationDao) CountWaitingSubAgents(parentAgentId, messageId string) (int, error) {
	var count int
	err := chat.dbManager.Db.Get(&count,
		`SELECT COUNT(*) FROM llm_conversation_agent
		 WHERE parent_agent_id = $1 AND message_id = $2
		 AND status IN ('waiting', 'in_progress')
		 AND id != $1`,
		parentAgentId, messageId)
	if err != nil {
		return 0, fmt.Errorf("history: failed to count waiting sub-agents: %w", err)
	}
	return count, nil
}

func (chat *ConversationDao) MarkInProgressConversationAsKilled() error {
	rows, err := chat.dbManager.Db.Queryx(`select id::text from llm_conversations where status = 'IN_PROGRESS' AND updated_at < NOW() - INTERVAL '1 hour'`)
	if err != nil {
		return fmt.Errorf("history: unable to fetch conversations: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			slog.Error("conversation_dao: failed to close rows", "error", err)
		}
	}()
	conversations := []string{}
	for rows.Next() {
		var c string
		err = rows.Scan(&c)
		if err != nil {
			return fmt.Errorf("history: unable to fetch conversations: %w", err)
		}
		conversations = append(conversations, c)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("history: failed to iterate over conversations: %w", err)
	}

	if len(conversations) == 0 {
		return nil
	}

	slog.Info("history: marking following conversations as killed", "conversations", conversations)

	for _, c := range conversations {
		conversationQuery := `UPDATE llm_conversations SET status = 'KILLED', updated_at = now() WHERE id = $1`
		_, err := chat.dbManager.Db.Exec(conversationQuery, c)
		if err != nil {
			return fmt.Errorf("history: failed to sync conversation status: %w", err)
		}

		conversationMessage := `UPDATE llm_conversation_messages SET status = 'KILLED', response = 'It looks like this conversation was interrupted before I could respond. Please start a new conversation and I''ll be happy to help!', updated_at = now() WHERE status = 'IN_PROGRESS' AND conversation_id = $1`
		_, err = chat.dbManager.Db.Exec(conversationMessage, c)
		if err != nil {
			return fmt.Errorf("history: failed to sync conversationMessage status: %w", err)
		}

		conversationAgent := `UPDATE llm_conversation_agent SET status = 'fail', updated_at = now() WHERE status = 'in_progress' AND conversation_id = $1`
		_, err = chat.dbManager.Db.Exec(conversationAgent, c)
		if err != nil {
			return fmt.Errorf("history: failed to sync conversationAgent status: %w", err)
		}

		conversationTool := `UPDATE llm_conversation_tool_calls SET status = 'fail', updated_at = now() WHERE status = 'in_progress' AND conversation_id = $1`
		_, err = chat.dbManager.Db.Exec(conversationTool, c)
		if err != nil {
			return fmt.Errorf("history: failed to sync conversationTool status: %w", err)
		}

	}

	return nil
}

func (chat *ConversationDao) DeleteConversation(conversationId string) error {
	query := `DELETE FROM llm_conversations WHERE id = $1`
	_, err := chat.dbManager.Db.Exec(query, conversationId)
	if err != nil {
		return fmt.Errorf("history: failed to delete conversation: %w", err)
	}
	return nil
}

func (chat *ConversationDao) SaveConversationMessage(id, conversationId, accountID, userId string, role MessageRole, messageType MessageType, message, response, agentName string, parentAgentId uuid.UUID, messageConfig any, messageContext string, llmProvider, llmModel string, status ConversationStatus) (uuid.UUID, error) {
	t0 := time.Now()
	if id == "" {
		id = common.GenerateUUID()
	}
	messageConfigJson := []byte("{}")
	if messageConfig != nil {
		if b, err := common.MarshalJson(messageConfig); err == nil && string(b) != "null" {
			messageConfigJson = b
		}
	}

	userIdSql := sql.NullString{
		String: userId,
		Valid:  userId != "" && userId != uuid.Nil.String(),
	}

	llmProviderSql := sql.NullString{
		String: llmProvider,
		Valid:  llmProvider != "",
	}

	llmModelSql := sql.NullString{
		String: llmModel,
		Valid:  llmModel != "",
	}

	query := `INSERT INTO llm_conversation_messages (id, account_id, user_id, conversation_id, role, message, response, message_type, status, worker_name, agent_name, parent_agent_id, message_config, message_context, llm_provider, llm_model) 
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16) RETURNING llm_conversation_messages.id;`
	var lastId uuid.UUID
	var err error
	if status == ConversationStatusQueued {
		tx, beginErr := chat.dbManager.Db.Beginx()
		if beginErr != nil {
			return uuid.Nil, fmt.Errorf("history: begin queue insert: %w", beginErr)
		}
		defer func() { _ = tx.Rollback() }()
		var currentStatus ConversationStatus
		if err = tx.Get(&currentStatus, `SELECT status FROM llm_conversations WHERE id = $1 AND account_id = $2 FOR UPDATE`, conversationId, accountID); err != nil {
			return uuid.Nil, fmt.Errorf("history: lock queue conversation: %w", err)
		}
		if !QueueAcceptsStatus(currentStatus) {
			return uuid.Nil, ErrQueueInactive
		}
		var count int
		if err = tx.Get(&count, `SELECT count(*) FROM llm_conversation_messages WHERE conversation_id = $1 AND status = 'QUEUED'`, conversationId); err != nil {
			return uuid.Nil, err
		}
		limit := config.Config.MaxQueuedMessagesPerConversation
		if limit <= 0 {
			limit = 5
		}
		if count >= limit {
			return uuid.Nil, ErrQueueFull
		}
		err = tx.QueryRow(query, id, accountID, userIdSql, conversationId, role, message, response, messageType, status, config.Config.ServerName, agentName, parentAgentId, string(messageConfigJson), messageContext, llmProviderSql, llmModelSql).Scan(&lastId)
		if err == nil {
			err = tx.Commit()
		}
	} else if config.Config.QueuedMessagesEnabled && status == ConversationStatusInProgress && role == MessageRoleHuman && messageType == MessageTypeGeneration && parentAgentId == uuid.Nil {
		tx, beginErr := chat.dbManager.Db.Beginx()
		if beginErr != nil {
			return uuid.Nil, beginErr
		}
		defer func() { _ = tx.Rollback() }()
		var currentStatus ConversationStatus
		if err = tx.Get(&currentStatus, `SELECT status FROM llm_conversations WHERE id = $1 AND account_id = $2 FOR UPDATE`, conversationId, accountID); err != nil {
			return uuid.Nil, err
		}
		var busy bool
		if err = tx.Get(&busy, `SELECT EXISTS (SELECT 1 FROM llm_conversation_messages WHERE conversation_id = $1 AND status IN ('IN_PROGRESS', 'QUEUED', 'WAITING', 'WAITING_FOR_CLIENT_TOOL', 'PENDING'))`, conversationId); err != nil {
			return uuid.Nil, err
		}
		if busy {
			return uuid.Nil, errors.New("history: conversation already has active or queued messages; retry after the current turn")
		}
		err = tx.QueryRow(query, id, accountID, userIdSql, conversationId, role, message, response, messageType, status, config.Config.ServerName, agentName, parentAgentId, string(messageConfigJson), messageContext, llmProviderSql, llmModelSql).Scan(&lastId)
		if err == nil {
			err = tx.Commit()
		}
	} else {
		err = chat.dbManager.Db.QueryRow(query, id, accountID, userIdSql, conversationId, role, message, response, messageType, status, config.Config.ServerName, agentName, parentAgentId, string(messageConfigJson), messageContext, llmProviderSql, llmModelSql).Scan(&lastId)
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("history: failed to save message: %w", err)
	}
	slog.Info("dao: SaveConversationMessage complete", "duration", time.Since(t0).String(), "conversation_id", conversationId)
	return lastId, nil
}

func (chat *ConversationDao) UpdateConversationMessage(id, response string, status ConversationStatus) error {
	// Stamp responded_at — the turn's foreground answer-completion time — the first time it
	// reaches a terminal state. Uses the DB clock (now()), consistent with updated_at and
	// immune to app/DB clock drift. A non-terminal write clears it, so a retried/recovered
	// message (reset to IN_PROGRESS) re-stamps fresh instead of keeping a stale value;
	// COALESCE keeps it write-once within a single terminal run. Post-response background
	// jobs (memory/summary/suggestions) go through other DAOs and never touch it.
	isTerminal := IsTerminalConversationStatus(status)
	// Cast the shared status parameter: otherwise PostgreSQL infers text from the
	// IN literals in the WHERE clause before resolving the enum assignment in SET,
	// causing "column status is of type llm_conversation_status but expression is of type text".
	query := `UPDATE llm_conversation_messages SET response = $2, updated_at = now(), status = $3::public.llm_conversation_status, worker_name = $4, responded_at = CASE WHEN $5::boolean THEN COALESCE(responded_at, now()) ELSE NULL END WHERE id = $1 AND (status NOT IN ('TERMINATED', 'KILLED') OR $3::public.llm_conversation_status IN ('TERMINATED', 'KILLED'))`
	_, err := chat.dbManager.Db.Exec(query, id, response, string(status), config.Config.ServerName, isTerminal)
	if err != nil {
		return fmt.Errorf("history: failed to update message: %w", err)
	}
	return nil
}

// UpdateConversationMessageMetadata merges the supplied keys into the
// metadata jsonb column on the llm_conversation_messages row. The column
// is a generic per-message attachment slot — first consumer is the
// outbound egressfilter (writes under the "egressfilter" key); future
// per-message subsystems (e.g. PII tokenization) write under their own
// top-level keys. The column was added in migration V761
// (1781680308449_V761_add_metadata_to_llm_conversation_messages).
//
// We MERGE rather than overwrite (`COALESCE(metadata, '{}') || $2::jsonb`)
// because multiple subsystems can each write independently for the same
// message; a `SET metadata = $2` would let a later writer wipe an earlier
// writer's namespace. The merge is shallow (top-level keys), which matches
// the convention: each subsystem owns one top-level key and the value
// underneath is opaque to other subsystems.
//
// An empty or nil metadata is treated as a no-op so callers can call
// unconditionally without an empty-check at the call site.
// SetMessageLiveStatus sets one entry under metadata.live_status, or removes it
// when status is nil. Entries are keyed per agent so parallel sub-agents writing
// to the same message never overwrite each other.
func (chat *ConversationDao) SetMessageLiveStatus(messageId, key string, status *llmruntime.LiveStatus) error {
	if messageId == "" || key == "" {
		return nil
	}
	if status == nil {
		_, err := chat.dbManager.Db.Exec(`UPDATE llm_conversation_messages
              SET metadata = metadata #- ARRAY['live_status', $2::text], updated_at = now()
              WHERE id = $1 AND jsonb_typeof(metadata) = 'object'
                AND jsonb_typeof(metadata->'live_status') = 'object' AND metadata->'live_status' ? $2`, messageId, key)
		if err != nil {
			return fmt.Errorf("history: failed to clear live status: %w", err)
		}
		return nil
	}
	b, err := common.MarshalJson(status)
	if err != nil {
		return fmt.Errorf("history: failed to marshal live status: %w", err)
	}
	_, err = chat.dbManager.Db.Exec(`UPDATE llm_conversation_messages
              SET metadata = jsonb_set(COALESCE(NULLIF(metadata, 'null'::jsonb), '{}'::jsonb), '{live_status}',
                      CASE WHEN jsonb_typeof(metadata->'live_status') = 'object'
                           THEN metadata->'live_status'
                           ELSE '{}'::jsonb
                      END || jsonb_build_object($2::text, $3::jsonb)),
                  updated_at = now()
              WHERE id = $1`, messageId, key, string(b))
	if err != nil {
		return fmt.Errorf("history: failed to set live status: %w", err)
	}
	return nil
}

func (chat *ConversationDao) UpdateConversationMessageMetadata(id string, metadata map[string]any) error {
	if id == "" || len(metadata) == 0 {
		return nil
	}
	b, err := common.MarshalJson(metadata)
	if err != nil {
		return fmt.Errorf("history: failed to marshal message metadata: %w", err)
	}
	query := `UPDATE llm_conversation_messages
              SET metadata   = COALESCE(metadata, '{}'::jsonb) || $2::jsonb,
                  updated_at = now()
              WHERE id = $1`
	if _, err := chat.dbManager.Db.Exec(query, id, string(b)); err != nil {
		return fmt.Errorf("history: failed to update message metadata: %w", err)
	}
	return nil
}

func (chat *ConversationDao) ClaimNextQueuedMessage(ctx context.Context, conversationID string) (*ConversationMessage, error) {
	tx, err := chat.dbManager.Db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("history: begin queue claim: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var status ConversationStatus
	if err := tx.GetContext(ctx, &status, `SELECT status FROM llm_conversations WHERE id = $1 FOR UPDATE`, conversationID); err != nil {
		return nil, err
	}
	// A failed turn still advances the queue; a stopped or swept one (KILLED,
	// TERMINATED) pauses it until the user sends a queued message explicitly.
	if status != ConversationStatusCompleted && status != ConversationStatusFailed {
		return nil, nil
	}
	var msg ConversationMessage
	err = tx.GetContext(ctx, &msg, `UPDATE llm_conversation_messages SET status = 'IN_PROGRESS', updated_at = now(), worker_name = $2
		WHERE id = (SELECT id FROM llm_conversation_messages WHERE conversation_id = $1 AND status = 'QUEUED'
		AND NOT EXISTS (SELECT 1 FROM llm_conversation_messages WHERE conversation_id = $1 AND status IN ('IN_PROGRESS', 'WAITING', 'WAITING_FOR_CLIENT_TOOL', 'PENDING'))
		ORDER BY created_at, id LIMIT 1 FOR UPDATE)
		RETURNING id, conversation_id, message, created_at, response, role, account_id, user_id, message_type, updated_at, responded_at, status, worker_name, agent_name, message_config, message_context, suggestions, llm_provider, llm_model`, conversationID, config.Config.ServerName)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("history: claim queue message: %w", err)
	}
	if _, err = tx.ExecContext(ctx, `UPDATE llm_conversations SET status = 'IN_PROGRESS', updated_at = now() WHERE id = $1`, conversationID); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return &msg, nil
}

func (chat *ConversationDao) ClaimQueuedMessageById(ctx *security.RequestContext, accountID, conversationID, messageID, userID string, isAdmin bool) (*ConversationMessage, error) {
	return chat.terminateConversation(ctx, accountID, conversationID, messageID, userID, isAdmin, terminatedByUser)
}

func (chat *ConversationDao) DeleteQueuedMessage(id string, accountID string, userID string, isAdmin bool) error {
	var query string
	var res sql.Result
	var err error
	if isAdmin {
		query = `DELETE FROM llm_conversation_messages WHERE id = $1 AND status = $2 AND account_id = $3`
		res, err = chat.dbManager.Db.Exec(query, id, string(ConversationStatusQueued), accountID)
	} else {
		query = `DELETE FROM llm_conversation_messages WHERE id = $1 AND status = $2 AND user_id = $3 AND account_id = $4`
		res, err = chat.dbManager.Db.Exec(query, id, string(ConversationStatusQueued), userID, accountID)
	}
	if err != nil {
		return fmt.Errorf("history: failed to delete queued message: %w", err)
	}
	rowsAffected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("history: failed to check rows affected: %w", err)
	}
	if rowsAffected == 0 {
		return ErrQueuedMessageNotFoundOrLocked
	}
	return nil
}

func (chat *ConversationDao) UpdateQueuedMessage(id string, accountID string, userID string, message string, isAdmin bool) error {
	var query string
	var res sql.Result
	var err error
	if isAdmin {
		query = `UPDATE llm_conversation_messages SET message = $1, updated_at = now() WHERE id = $2 AND status = $3 AND account_id = $4`
		res, err = chat.dbManager.Db.Exec(query, message, id, string(ConversationStatusQueued), accountID)
	} else {
		query = `UPDATE llm_conversation_messages SET message = $1, updated_at = now() WHERE id = $2 AND status = $3 AND user_id = $4 AND account_id = $5`
		res, err = chat.dbManager.Db.Exec(query, message, id, string(ConversationStatusQueued), userID, accountID)
	}
	if err != nil {
		return fmt.Errorf("history: failed to update queued message: %w", err)
	}
	rowsAffected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("history: failed to check rows affected: %w", err)
	}
	if rowsAffected == 0 {
		return ErrQueuedMessageNotFoundOrLocked
	}
	return nil
}

func (chat *ConversationDao) GetQueuedConversationMessages(conversationID string) ([]ConversationMessage, error) {
	query := `
		SELECT id, conversation_id, message, created_at, response, role, account_id,
		       user_id, message_type, updated_at, responded_at, status, worker_name,
		       agent_name, message_config, message_context, suggestions, llm_provider, llm_model
		FROM llm_conversation_messages
		WHERE conversation_id = $1 AND status = $2
		ORDER BY created_at ASC`
	var messages []ConversationMessage
	err := chat.dbManager.Db.Select(&messages, query, conversationID, string(ConversationStatusQueued))
	if err != nil {
		return nil, fmt.Errorf("history: failed to get queued conversation messages: %w", err)
	}
	return messages, nil
}

func (chat *ConversationDao) UpdateConversationMessageAsync(id, response string, status ConversationStatus) {
	if id == "" || id == uuid.Nil.String() {
		return
	}

	// Use a background context for the submission itself with a short timeout
	submissionCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := common.GetStatusUpdateWorkerPool().Submit(submissionCtx, func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("dao: panic in UpdateConversationMessageAsync", "error", r)
			}
		}()

		// For async updates (usually progress), we use a safer query that won't overwrite terminal states.
		// This prevents a late progress update from reverting a 'COMPLETED' or 'FAILED' status.
		query := `UPDATE llm_conversation_messages 
			SET response = $2, updated_at = now(), status = $3, worker_name = $4 
			WHERE id = $1 AND status NOT IN ('COMPLETED', 'FAILED', 'KILLED', 'TERMINATED')`

		// Use a background context with a fixed timeout to prevent goroutine leaks if the DB hangs
		dbCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		_, err := chat.dbManager.Db.ExecContext(dbCtx, query, id, response, string(status), config.Config.ServerName)
		if err != nil {
			slog.Warn("dao: failed to update message asynchronously", "error", err, "id", id)
		}
	})
	if err != nil {
		// This is a serious condition indicating the worker pool queue is saturated or submission timed out.
		responseSnippet := response
		if len(responseSnippet) > 100 {
			responseSnippet = responseSnippet[:100] + "..."
		}
		slog.Error("dao: CRITICAL - failed to submit async message update",
			"error", err,
			"id", id,
			"status", status,
			"response_snippet", responseSnippet,
		)
	}
}

func (chat *ConversationDao) UpdateMessageCompactResponse(context *security.RequestContext, messageId, compactResponse string) error {
	query := `UPDATE llm_conversation_messages SET compact_response = $2, updated_at = now() WHERE id = $1`
	ctx := context.GetContext()
	_, err := chat.dbManager.Db.ExecContext(ctx, query, messageId, compactResponse)
	if err != nil {
		return fmt.Errorf("history: failed to update compact response: %w", err)
	}
	return nil
}

// termination says what an ended conversation shows and when it may be ended.
type termination struct {
	messageText   string // written to the ended messages
	stepText      string // written to the ended agent and tool-call rows
	onlyIfWaiting bool   // end it only if it is still WAITING when the row is locked
}

var terminatedByUser = termination{messageText: "Conversation terminated by user", stepText: "Terminated by user"}

// errConversationNotWaiting means an expiry found the conversation no longer
// WAITING (someone answered it) and left it alone.
var errConversationNotWaiting = errors.New("conversation is no longer waiting")

func (chat *ConversationDao) TerminateConversation(ctx *security.RequestContext, accountId, conversationId string) error {
	_, err := chat.terminateConversation(ctx, accountId, conversationId, "", "", false, terminatedByUser)
	return err
}

// ExpireWaitingConversation ends a conversation that is still WAITING, the same
// way Stop does, with note shown in place of the unanswered prompt. It returns
// false, and changes nothing, when the conversation is no longer WAITING.
func (chat *ConversationDao) ExpireWaitingConversation(ctx *security.RequestContext, accountId, conversationId, note string) (bool, error) {
	_, err := chat.terminateConversation(ctx, accountId, conversationId, "", "", false,
		termination{messageText: note, stepText: note, onlyIfWaiting: true})
	if errors.Is(err, errConversationNotWaiting) {
		return false, nil
	}
	return err == nil, err
}

func (chat *ConversationDao) terminateConversation(ctx *security.RequestContext, accountId, conversationId, messageID, userID string, isAdmin bool, how termination) (*ConversationMessage, error) {
	tx, err := chat.dbManager.Db.Beginx()
	if err != nil {
		return nil, fmt.Errorf("history: failed to begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var conversationStatus ConversationStatus
	if err = tx.Get(&conversationStatus, `SELECT status FROM llm_conversations WHERE id = $1 AND account_id = $2 FOR UPDATE`, conversationId, accountId); err != nil {
		return nil, err
	}
	if how.onlyIfWaiting && conversationStatus != ConversationStatusWaiting {
		return nil, errConversationNotWaiting
	}
	var claimed *ConversationMessage
	if messageID != "" {
		if conversationStatus == ConversationStatusKilled {
			return nil, ErrQueuedMessageNotFoundOrLocked
		}
		claimed = &ConversationMessage{}
		err = tx.Get(claimed, `SELECT id, conversation_id, message, created_at, response, role, account_id, user_id, message_type, updated_at, responded_at, status, worker_name, agent_name, message_config, message_context, suggestions, llm_provider, llm_model FROM llm_conversation_messages WHERE id = $1 AND account_id = $2 AND conversation_id = $3 AND status = 'QUEUED' AND ($4 OR user_id::text = $5) FOR UPDATE`, messageID, accountId, conversationId, isAdmin, userID)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrQueuedMessageNotFoundOrLocked
		}
		if err != nil {
			return nil, err
		}
	}

	// Update the message status to TERMINATED and capture affected message IDs so
	// we can invalidate their termination cache entries after commit.
	messageQuery := `UPDATE llm_conversation_messages SET updated_at = now(), status = $2, response = $4 WHERE conversation_id = $1 AND account_id = $3 AND status in ('IN_PROGRESS', 'WAITING', 'WAITING_FOR_CLIENT_TOOL', 'PENDING') RETURNING id`
	rows, err := tx.Queryx(messageQuery, conversationId, string(ConversationStatusTerminated), accountId, how.messageText)
	if err != nil {
		return nil, fmt.Errorf("history: failed to terminate message: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var terminatedMessageIds []string
	for rows.Next() {
		var id string
		if scanErr := rows.Scan(&id); scanErr != nil {
			return nil, fmt.Errorf("history: failed to scan terminated message id: %w", scanErr)
		}
		terminatedMessageIds = append(terminatedMessageIds, id)
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		return nil, fmt.Errorf("history: failed to iterate terminated messages: %w", rowsErr)
	}

	// Update the associated conversation status to TERMINATED
	conversationQuery := `UPDATE llm_conversations SET status = $2, updated_at = now() WHERE id = $1 AND account_id = $3`
	_, err = tx.Exec(conversationQuery, conversationId, string(ConversationStatusTerminated), accountId)
	if err != nil {
		return nil, fmt.Errorf("history: failed to terminate conversation associated with message: %w", err)
	}

	// Terminate any sub-agent and tool-call rows still in-progress (or waiting)
	// for this conversation. Without this, the executor's next iteration sees the
	// message as terminated and unwinds, but the per-step rows remain stuck. We
	// write a response marker so callers can distinguish user-termination from
	// real failures. Both tables now permit 'terminated' as a status value, which
	// matches the late-write override in SaveConversationToolCall /
	// UpdateConversationAgentResponse — keeping the bulk and late-write paths in
	// sync.
	terminatedResponse := how.stepText
	agentQuery := `UPDATE llm_conversation_agent SET status = $2, response = $5, updated_at = now() WHERE conversation_id = $1 AND account_id = $6 AND status IN ($3, $4)`
	_, err = tx.Exec(agentQuery, conversationId,
		string(AgentExecutionStatusTerminated),
		string(AgentExecutionStatusInProgress),
		string(AgentExecutionStatusWaiting),
		terminatedResponse,
		accountId)
	if err != nil {
		return nil, fmt.Errorf("history: failed to terminate in-progress agents: %w", err)
	}

	toolQuery := `UPDATE llm_conversation_tool_calls SET status = $2, response = $5, updated_at = now() WHERE conversation_id = $1 AND account_id = $6 AND status IN ($3, $4)`
	_, err = tx.Exec(toolQuery, conversationId,
		strings.ToLower(string(toolcore.NBToolResponseStatusTerminated)),
		strings.ToLower(string(toolcore.NBToolResponseStatusInProgress)),
		strings.ToLower(string(toolcore.NBToolResponseStatusWaiting)),
		terminatedResponse,
		accountId)
	if err != nil {
		return nil, fmt.Errorf("history: failed to terminate in-progress tool calls: %w", err)
	}

	if claimed != nil {
		if _, err = tx.Exec(`UPDATE llm_conversation_messages SET status = 'IN_PROGRESS', updated_at = now(), worker_name = $2 WHERE id = $1`, messageID, config.Config.ServerName); err != nil {
			return nil, err
		}
		if _, err = tx.Exec(`UPDATE llm_conversations SET status = 'IN_PROGRESS', updated_at = now() WHERE id = $1`, conversationId); err != nil {
			return nil, err
		}
		claimed.Status = ConversationStatusInProgress
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}

	// Prime the termination cache so running executors observe termination on
	// their next check instead of waiting for the cache TTL to expire.
	// Best-effort: a priming failure delays detection by one cache-miss + DB-read
	// cycle, not indefinitely — so a transient cache outage must not fail the
	// whole termination. Keep this as Warn-and-continue.
	ttl := time.Duration(config.Config.LlmServerMessageTerminatedCacheTTLMinutes) * time.Minute
	for _, id := range terminatedMessageIds {
		if cacheErr := common.CacheSet(MessageTerminationCacheNamespace, id, []byte("true"), common.CacheSetWithExpiration(ttl)); cacheErr != nil {
			slog.Warn("history: failed to prime termination cache", "error", cacheErr, "messageId", id)
		}
	}

	return claimed, nil
}

// UpdateConversationMessageConfig replaces the message's stored config, except
// that a git_repo already stored on the message is kept.
//
// The code agent records the repo it resolved mid-turn
// (UpdateConversationMessageGitRepo), but it runs as a sub-agent: the top-level
// request that rewrites the config when the turn ends never saw it and carries
// whatever repo the turn started with, or none. Letting that stale value win
// would undo the code agent's write — including a switch from repo A to repo B.
// The code agent is the only thing that resolves a repo, so the stored value is
// authoritative once set.
func (chat *ConversationDao) UpdateConversationMessageConfig(id string, config toolcore.NBQueryConfig) error {
	config = messageQueryConfig(config)
	configJson, _ := common.MarshalJson(config)

	query := `UPDATE llm_conversation_messages
		SET message_config = CASE
			WHEN COALESCE(NULLIF(message_config, '')::jsonb ->> 'git_repo', '') <> ''
			THEN jsonb_set($2::text::jsonb, '{git_repo}', to_jsonb(message_config::jsonb ->> 'git_repo'))::text
			ELSE $2::text
		END
		WHERE id = $1`
	_, err := chat.dbManager.Db.Exec(query, id, string(configJson))
	if err != nil {
		return fmt.Errorf("history: failed to update message: %w", err)
	}
	return nil
}

// UpdateConversationMessageGitRepo records the repository the code agent
// resolved on the message's stored config, leaving every other field as is. The
// next message in the conversation inherits it from there instead of detecting a
// repository again (see the inheritance in handleConversationRequest).
func (chat *ConversationDao) UpdateConversationMessageGitRepo(id, gitRepo string) error {
	query := `UPDATE llm_conversation_messages
		SET message_config = jsonb_set(COALESCE(NULLIF(message_config, ''), '{}')::jsonb, '{git_repo}', to_jsonb($2::text))::text
		WHERE id = $1`
	if _, err := chat.dbManager.Db.Exec(query, id, gitRepo); err != nil {
		return fmt.Errorf("history: failed to update message git repo: %w", err)
	}
	return nil
}

func messageQueryConfig(config toolcore.NBQueryConfig) toolcore.NBQueryConfig {
	// A delegated target belongs to its child agent row, not the shared turn.
	config.DelegatedTarget = nil
	return config
}

// UpdateConversationMessageFollowupConfig updates only the followup-specific fields in message_config.
//
// Takes the FollowupRequest rather than an already-built map on purpose: this
// overwrites a config the create path already wrote, so any field the caller
// omits is erased from the stored message. Callers hand-rolling a partial map
// is how confirmationKey went missing (silently breaking every per-action write
// confirmation) — serializing here, through the same followupMessageConfig the
// create path uses, makes that a compile error instead.
func (chat *ConversationDao) UpdateConversationMessageFollowupConfig(id string, followupRequest FollowupRequest) error {
	followupConfig := followupMessageConfig(followupRequest)
	configJson, err := common.MarshalJson(followupConfig)
	if err != nil {
		return fmt.Errorf("history: failed to marshal followup config: %w", err)
	}

	// Also update the message column with the new question text so chat history shows the correct question.
	question := followupRequest.Question
	query := `UPDATE llm_conversation_messages SET message_config = $2, message = $3, updated_at = now() WHERE id = $1`
	_, err = chat.dbManager.Db.Exec(query, id, string(configJson), question)
	if err != nil {
		return fmt.Errorf("history: failed to update message followup config: %w", err)
	}
	return nil
}

func (chat *ConversationDao) CleanupConversationMessage(id, accountId string) error {
	// Refuse to clean up when an active (non-terminal) followup message still
	// references one of the agents about to be deleted. Deleting the agent
	// row leaves the followup with a dangling parent_agent_id and the next
	// followup-resume call 400s at chains.go's ListConversationAgents lookup
	// with "api: agent not found".

	if accountId == "" {
		return fmt.Errorf("history: CleanupConversationMessage requires accountId")
	}
	var activeFollowups int
	err := chat.dbManager.Db.Get(&activeFollowups, `
		SELECT count(*)
		FROM llm_conversation_messages m
		JOIN llm_conversation_agent a
		  ON a.id = m.parent_agent_id
		 AND a.account_id = m.account_id
		WHERE a.message_id = $1
		  AND m.account_id = $2
		  AND m.message_type = $3
		  AND m.status NOT IN ($4, $5, $6, $7)`,
		id,
		accountId,
		string(MessageTypeFollowup),
		string(ConversationStatusCompleted),
		string(ConversationStatusFailed),
		string(ConversationStatusTerminated),
		string(ConversationStatusKilled))
	if err != nil {
		return fmt.Errorf("history: failed to check for active followups before cleanup: %w", err)
	}
	if activeFollowups > 0 {
		slog.Warn("history: refusing cleanup; active followup references agents from this message",
			"message_id", id, "account_id", accountId, "active_followups", activeFollowups)
		return ErrCleanupRefusedActiveFollowup
	}

	toolsQuery := `delete from  llm_conversation_tool_calls WHERE message_id = $1`
	_, err = chat.dbManager.Db.Exec(toolsQuery, id)
	if err != nil {
		return fmt.Errorf("history: failed to remove tools call: %w", err)
	}

	agentsQuery := `delete from  llm_conversation_agent WHERE message_id = $1`
	_, err = chat.dbManager.Db.Exec(agentsQuery, id)
	if err != nil {
		return fmt.Errorf("history: failed to remove agent call: %w", err)
	}

	// Clear responded_at too: this resets the message for a retry/re-run, so the prior run's
	// answer-completion time must not linger and shadow the new run's.
	messageQuery := `UPDATE llm_conversation_messages SET response = $2, updated_at = now(), status = $3, worker_name = $4, responded_at = NULL WHERE id = $1`
	_, err = chat.dbManager.Db.Exec(messageQuery, id, "", string(ConversationStatusInProgress), config.Config.ServerName)
	if err != nil {
		return fmt.Errorf("history: failed to update message: %w", err)
	}
	return nil
}

func (chat *ConversationDao) GetConversationMessageStatus(id, conversationId string) (ConversationStatus, error) {
	var status ConversationStatus
	err := chat.dbManager.Db.QueryRowx(`select status from llm_conversation_messages WHERE id = $1 and conversation_id = $2`, id, conversationId).Scan(&status)
	if err != nil {
		return "", fmt.Errorf("history: failed to read message status: %w", err)
	}
	return status, nil
}

func (chat *ConversationDao) GetToolCallChart(conversationID, ref string) ([]byte, error) {
	// conversation_id leads idx_llm_conv_tool_calls_conv_id_tool_type.
	query := `SELECT metadata->'charts'->$2::text FROM llm_conversation_tool_calls WHERE conversation_id = $1 AND metadata->'charts'->$2::text IS NOT NULL LIMIT 1`
	var chart []byte
	err := chat.dbManager.Db.QueryRow(query, conversationID, ref).Scan(&chart)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("history: failed to get tool call chart: %w", err)
	}
	return chart, nil
}

func (chat *ConversationDao) GetConversationMessage(id, accountId, conversationId string) (ConversationMessage, error) {
	query := `select id, conversation_id, message, created_at, response, role, account_id, user_id, message_type, updated_at, responded_at, status, worker_name, agent_name, message_config, message_context, suggestions, llm_provider, llm_model from llm_conversation_messages WHERE id = $1 and conversation_id = $2 and account_id = $3`
	row := chat.dbManager.Db.QueryRowx(query, id, conversationId, accountId)
	if row == nil || row.Err() != nil {
		return ConversationMessage{}, fmt.Errorf("history: failed to get message: %w", row.Err())
	}
	var message ConversationMessage
	err := row.StructScan(&message)
	if err != nil {
		return ConversationMessage{}, fmt.Errorf("history: failed to scan message: %w", err)
	}
	return message, nil
}

func (chat *ConversationDao) ListConversationMessages(status ConversationStatus, workerName string, conversationId string, deadWorker bool) ([]ConversationMessage, error) {
	query := `select id, conversation_id, message, created_at, response, role, account_id,
				user_id, message_type, updated_at, responded_at, status, worker_name, agent_name,
				message_config, message_context, suggestions, llm_provider, llm_model
			  from llm_conversation_messages WHERE true
			`
	args := []any{}
	if status != "" {
		query += fmt.Sprintf(" AND status = $%d", len(args)+1)
		args = append(args, status)
	}
	if workerName != "" {
		query += fmt.Sprintf(" AND worker_name = $%d", len(args)+1)
		args = append(args, workerName)
	}
	if conversationId != "" {
		query += fmt.Sprintf(" AND conversation_id = $%d", len(args)+1)
		args = append(args, conversationId)
	}
	if deadWorker {
		query += fmt.Sprintf(" AND worker_name not in (select worker_name from nb_workers where worker_type = $%d)", len(args)+1)
		args = append(args, config.SERVICE_NAME)
		// Only look at recently-updated messages to avoid fetching ancient stuck
		// records (3,600+ rows from months ago causing 30-60s query times). The
		// horizon is shared with the boot-time own-orphan sweep so the two recovery
		// paths cannot disagree about what counts as abandoned.
		// Bound against the database clock, not the caller's: this query runs on every
		// replica, and updated_at is written with now(), so the DB is the one clock all
		// of them agree on.
		// make_interval takes a number, so the horizon crosses into SQL as seconds and
		// never as text — no dependence on how Go formats a Duration or on how Postgres
		// parses it.
		query += fmt.Sprintf(" AND updated_at > now() - make_interval(secs => $%d)", len(args)+1)
		args = append(args, config.OrphanRecoveryHorizon.Seconds())
	}

	query += " ORDER BY created_at"

	rows, err := chat.dbManager.Db.Queryx(query, args...)
	if err != nil {
		return []ConversationMessage{}, fmt.Errorf("history: failed to get message: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			slog.Error("conversation_dao: failed to close rows", "error", err)
		}
	}()
	var messages []ConversationMessage
	for rows.Next() {
		var message ConversationMessage
		if err := rows.StructScan(&message); err != nil {
			return []ConversationMessage{}, fmt.Errorf("history: failed to scan message: %w", err)
		}
		messages = append(messages, message)
	}
	if err := rows.Err(); err != nil {
		return []ConversationMessage{}, fmt.Errorf("history: rows iteration error listing messages: %w", err)
	}
	return messages, nil
}

func (chat *ConversationDao) ListConversationAgents(messageId, agentId string) ([]ConversationAgent, error) {
	args := []any{}
	query := ""
	if agentId != "" {
		query = `select id, agent_name, message_id, response, created_at, account_id,
					user_id, parent_agent_id, query, conversation_id, thought,
					updated_at, query_context, query_config, status, followup_message_id, state, agent_step_response, "references"
			  from llm_conversation_agent WHERE id = $1
			  order by created_at
		`
		args = append(args, agentId)
	} else if messageId != "" {
		query = `select id, agent_name, message_id, response, created_at, account_id,
		user_id, parent_agent_id, query, conversation_id, thought,
		updated_at, query_context, query_config, status, followup_message_id, state, agent_step_response, "references"
		from llm_conversation_agent WHERE message_id = $1
		order by created_at
	`
		args = append(args, messageId)

	} else {
		return []ConversationAgent{}, errors.New("history: agentId or messageId is required")
	}

	rows, err := chat.dbManager.Db.Queryx(query, args...)
	if err != nil {
		return []ConversationAgent{}, fmt.Errorf("history: failed to get agent: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			slog.Error("conversation_dao: failed to close rows", "error", err)
		}
	}()
	var agents []ConversationAgent
	for rows.Next() {
		var agent ConversationAgent
		if err := rows.StructScan(&agent); err != nil {
			return []ConversationAgent{}, fmt.Errorf("history: failed to scan message: %w", err)
		}
		agents = append(agents, agent)
	}
	return agents, nil
}

func (chat *ConversationDao) GetConversationAgentInput(agentId, accountId string) (string, error) {
	querySql := `SELECT query FROM llm_conversation_agent WHERE id = $1 AND account_id = $2`
	var query string
	err := chat.dbManager.Db.QueryRow(querySql, agentId, accountId).Scan(&query)
	if err != nil {
		return "", fmt.Errorf("history: failed to get agent input: %w", err)
	}
	return query, nil
}

func (chat *ConversationDao) GetConversationAgentOutput(agentId, accountId string) (string, error) {
	querySql := `SELECT response FROM llm_conversation_agent WHERE id = $1 AND account_id = $2`
	var query string
	err := chat.dbManager.Db.QueryRow(querySql, agentId, accountId).Scan(&query)
	if err != nil {
		return "", fmt.Errorf("history: failed to get agent output: %w", err)
	}
	return query, nil
}

func (chat *ConversationDao) UpdateConversationToolResponse(toolId, messageId, conversationId, accountId, response string, status toolcore.NBToolResponseStatus) error {
	// Termination override: neutralize late writebacks for tool rows whose parent
	// message has been terminated — including rows inserted *after* termination's
	// bulk UPDATE ran (those wouldn't be caught by the late-write guard alone since
	// they're freshly 'in_progress'). The cache fast-path keeps this cheap on the
	// common non-terminated case.
	if terminated, termErr := checkMessageTerminationStatus(messageId, accountId, conversationId); termErr != nil {
		slog.Debug("history: termination cache lookup failed (proceeding without override)", "message_id", messageId, "error", termErr)
	} else if terminated {
		response = "Terminated by user"
		status = toolcore.NBToolResponseStatusTerminated
	}

	// Guard against overwriting terminal rows — see UpdateConversationAgentResponse
	// for the rationale. 'terminated' is the user-termination marker; 'success' and
	// 'error' are normal terminal states.
	query := `UPDATE llm_conversation_tool_calls SET response = $2, updated_at = now(), status = $3 WHERE tool_id = $1 AND message_id = $4 AND conversation_id = $5 AND account_id = $6 AND status NOT IN ('success', 'error', 'terminated')`
	_, err := chat.dbManager.Db.Exec(query, toolId, response, strings.ToLower(string(status)), messageId, conversationId, accountId)
	if err != nil {
		return fmt.Errorf("history: failed to update tool response: %w", err)
	}
	return nil
}

func (chat *ConversationDao) GetConversationToolResponse(toolId, messageId, conversationId, accountId string) (string, toolcore.NBToolResponseStatus, error) {
	query := `SELECT response, status FROM llm_conversation_tool_calls WHERE tool_id = $1 AND message_id = $2 AND conversation_id = $3 AND account_id = $4`
	var response string
	var status toolcore.NBToolResponseStatus
	err := chat.dbManager.Db.QueryRow(query, toolId, messageId, conversationId, accountId).Scan(&response, &status)
	if err != nil {
		return "", "", fmt.Errorf("history: failed to get tool response: %w", err)
	}
	return response, status, nil
}

func (chat *ConversationDao) SaveConversationToolCall(conversationID, accountID, userID, messageId, agenId, toolId, toolName, toolArgs, thought, toolArgsSql, toolResult string, status toolcore.NBToolResponseStatus, toolType toolcore.NBToolType, childAgentId *string, references []toolcore.NBToolResponseReference, metadata []byte, memoryRefs []byte) error {

	// Termination override — see UpdateConversationToolResponse for rationale.
	// SaveConversationToolCall is the primary persistence path (planner_callback_handler
	// calls it on tool completion via INSERT ... ON CONFLICT DO UPDATE), so without
	// this any in-flight tool that finishes after termination would clobber the
	// "Terminated by user" marker through the upsert. The DO UPDATE WHERE clause
	// below guards already-terminal rows; this override handles rows whose row-state
	// is currently 'in_progress' (so the WHERE doesn't bite) but whose parent message
	// has been marked TERMINATED.
	if terminated, termErr := checkMessageTerminationStatus(messageId, accountID, conversationID); termErr != nil {
		slog.Debug("history: termination cache lookup failed (proceeding without override)", "message_id", messageId, "error", termErr)
	} else if terminated {
		toolResult = "Terminated by user"
		status = toolcore.NBToolResponseStatusTerminated
	}

	userIDSql := sql.NullString{
		String: userID,
		Valid:  userID != "" && userID != uuid.Nil.String(),
	}

	referenceText := "[]"
	if len(references) > 0 {
		referenceTextBytes, _ := common.MarshalJson(references)
		referenceText = string(referenceTextBytes)
	}

	// metadata carries the JSON-encoded NBToolResponseMetadata blob for the
	// new V751 `metadata` JSONB column. nil ⇒ pass NULL so historical rows
	// and synthetic visibility entries (compression cards, react_critique)
	// stay unencumbered. Stringify (matches referenceText above) — passing a
	// raw []byte makes pq route through bytea, which a JSONB column rejects
	// at insert ("invalid input syntax for type json").
	var metadataParam any
	if len(metadata) > 0 {
		metadataParam = string(metadata)
	}
	// memoryRefs is the LLM's per-action memory attribution — jsonb array
	// of {position, note}. Stringify (same reason as metadataParam) — passing
	// a raw []byte routes through bytea and jsonb rejects that at insert.
	// Empty/nil defaults to '[]' via the column default; the ON CONFLICT
	// UPDATE below deliberately DOES NOT touch memory_refs so a later
	// status update (in-progress → success) doesn't clobber the attribution
	// captured at first insert.
	memoryRefsParam := "[]"
	if len(memoryRefs) > 0 {
		memoryRefsParam = string(memoryRefs)
	}

	query := `INSERT INTO public.llm_conversation_tool_calls
	(conversation_id, message_id, tool_id, tool_name, parameters, response, user_id, account_id, agent_id, thought, params_sql, status, tool_type, child_agent_id, "references", metadata, memory_refs)
	VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17)
	ON CONFLICT (conversation_id, message_id, tool_id, tool_name, agent_id)
	DO UPDATE SET response = EXCLUDED.response, updated_at = now(), status = EXCLUDED.status, tool_type = EXCLUDED.tool_type, child_agent_id = EXCLUDED.child_agent_id, "references" = EXCLUDED.references, metadata = EXCLUDED.metadata
	WHERE llm_conversation_tool_calls.status NOT IN ('success', 'error', 'terminated');`

	if childAgentId == nil || *childAgentId == "" {
		query = `INSERT INTO public.llm_conversation_tool_calls
		(conversation_id, message_id, tool_id, tool_name, parameters, response, user_id, account_id, agent_id, thought, params_sql, status, tool_type, child_agent_id, "references", metadata, memory_refs)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17)
		ON CONFLICT (conversation_id, message_id, tool_id, tool_name, agent_id)
		DO UPDATE SET response = EXCLUDED.response, updated_at = now(), status = EXCLUDED.status, tool_type = EXCLUDED.tool_type, "references" = EXCLUDED.references, metadata = EXCLUDED.metadata
		WHERE llm_conversation_tool_calls.status NOT IN ('success', 'error', 'terminated');`
	}

	_, err := chat.dbManager.Db.Exec(query, conversationID, messageId, toolId, toolName, toolArgs, toolResult, userIDSql, accountID, agenId, thought, toolArgsSql, strings.ToLower(string(status)), toolType, childAgentId, referenceText, metadataParam, memoryRefsParam)
	if err != nil {
		return fmt.Errorf("history: error inserting tool call: %w", err)
	}
	return nil
}

// FindToolCallRows returns the tool-call rows of a conversation whose tool_id is
// one of toolIDs. A tool_id is a hash of the call, so the same id can appear
// once per message and per agent; callers pick the row that fits.
func (chat *ConversationDao) FindToolCallRows(conversationID string, toolIDs []string) ([]ToolCallRowRef, error) {
	if conversationID == "" || len(toolIDs) == 0 {
		return nil, nil
	}
	var rows []ToolCallRowRef
	err := chat.dbManager.Db.Select(&rows, `
		SELECT id::text AS id, tool_id, COALESCE(agent_id::text, '') AS agent_id,
		       COALESCE(message_id::text, '') AS message_id, created_at
		FROM llm_conversation_tool_calls
		WHERE conversation_id = $1::uuid AND tool_id = ANY($2::text[])`,
		conversationID, pq.Array(toolIDs))
	if err != nil {
		return nil, fmt.Errorf("history: unable to find tool call rows: %w", err)
	}
	return rows, nil
}

// FinalizeInFlightToolCalls flips any tool_calls for the given message that are
// still 'in_progress' to 'error'. Called by handleConversationRequest's
// finalize-on-exit defer so a planner-turn panic (or any abnormal exit before
// planner_callback_handler runs) doesn't leave rows stranded — otherwise the
// only cleanup is the batch job in MarkInProgressConversationAsKilled (>=1h).
// Idempotent: no matching rows → no rows updated → returns nil.
func (chat *ConversationDao) FinalizeInFlightToolCalls(messageId string) error {
	query := `UPDATE llm_conversation_tool_calls
		SET status = 'error',
		    response = COALESCE(NULLIF(response, ''), 'Tool call did not finalize — parent turn exited abnormally.'),
		    updated_at = now()
		WHERE message_id = $1 AND status = 'in_progress'`
	_, err := chat.dbManager.Db.Exec(query, messageId)
	if err != nil {
		return fmt.Errorf("history: failed to finalize in-flight tool calls: %w", err)
	}
	return nil
}

// MarkMessageFailedOnPanic is the ctx-cancellable path used by the panic-recovery
// defer in handleConversationRequest. Uses ExecContext throughout so a caller-
// supplied ctx timeout unblocks the SQL driver on hang — no leaked goroutine,
// no held DB connection past the caller's budget. Each underlying UPDATE is
// logged on failure and processing continues (non-atomic across three tables
// on purpose — a partial finalize is still better than none). Empty messageId
// or conversationId skips that half.
func (chat *ConversationDao) MarkMessageFailedOnPanic(ctx context.Context, messageId, conversationId, panicResponse string) error {
	// Collect every UPDATE error, not just the first — the caller logs the return
	// value in a single line, so silently dropping later failures would hide real
	// signal (e.g. message update succeeded but the conversation flip failed —
	// worth knowing when triaging why a message shows FAILED but its parent
	// conversation is still IN_PROGRESS). errors.Join preserves all of them.
	var errs []error
	if messageId != "" {
		msgQuery := `UPDATE llm_conversation_messages SET updated_at = now(), status = $2, response = $3 WHERE id::text = $1`
		if _, err := chat.dbManager.Db.ExecContext(ctx, msgQuery, messageId, string(ConversationStatusFailed), panicResponse); err != nil {
			errs = append(errs, fmt.Errorf("history: MarkMessageFailedOnPanic: message update failed: %w", err))
		}
		tcQuery := `UPDATE llm_conversation_tool_calls
			SET status = 'error',
			    response = COALESCE(NULLIF(response, ''), 'Tool call did not finalize — parent turn exited abnormally.'),
			    updated_at = now()
			WHERE message_id = $1 AND status = 'in_progress'`
		if _, err := chat.dbManager.Db.ExecContext(ctx, tcQuery, messageId); err != nil {
			errs = append(errs, fmt.Errorf("history: MarkMessageFailedOnPanic: tool-call cleanup failed: %w", err))
		}
	}
	if conversationId != "" {
		convQuery := `UPDATE llm_conversations SET updated_at = now(), status = $2 WHERE id::text = $1`
		if _, err := chat.dbManager.Db.ExecContext(ctx, convQuery, conversationId, string(ConversationStatusFailed)); err != nil {
			errs = append(errs, fmt.Errorf("history: MarkMessageFailedOnPanic: conversation update failed: %w", err))
		}
	}
	return errors.Join(errs...)
}

func (chat *ConversationDao) GetConversationToolCallChildAgentId(conversationId, agenId, toolId string) string {
	query := `SELECT child_agent_id FROM llm_conversation_tool_calls WHERE conversation_id = $1 AND agent_id = $2 AND tool_id = $3`
	var childAgentId *string
	err := chat.dbManager.Db.QueryRow(query, conversationId, agenId, toolId).Scan(&childAgentId)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ""
		}
		slog.Error("history: failed to get child agent id", "error", err, "conversationId", conversationId, "agentId", agenId, "toolId", toolId)
		return ""
	}
	if childAgentId == nil {
		return ""
	}
	return *childAgentId
}

func (chat *ConversationDao) LoadConversationMessages(accountID, conversationID, userID string, requestType MessageType, k int) ([]map[string]string, error) {
	if _, err := uuid.Parse(conversationID); err != nil {
		return nil, nil
	}
	var rows *sqlx.Rows
	var err error
	var args []interface{}
	args = append(args, conversationID, accountID)

	// message_config is only needed on followup rows (it names the followup type).
	query := `SELECT role, message, response, id::text, message_type,
		CASE WHEN message_type = 'followup' THEN COALESCE(message_config, '') ELSE '' END
		FROM llm_conversation_messages
		WHERE conversation_id = $1 AND account_id = $2 AND status <> 'QUEUED'`

	if userID != "" {
		args = append(args, userID)
		query += fmt.Sprintf(" AND user_id = $%d", len(args))
	}

	if requestType != "" {
		args = append(args, string(requestType))
		query += fmt.Sprintf(" AND message_type = $%d", len(args))
	}

	args = append(args, k)
	query += fmt.Sprintf(" ORDER BY created_at DESC LIMIT $%d", len(args))

	rows, err = chat.dbManager.Db.Queryx(query, args...)
	if err != nil {
		return nil, fmt.Errorf("history: failed to load messages: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			slog.Error("conversation_dao: failed to close rows", "error", err)
		}
	}()

	var messages []map[string]string
	for rows.Next() {
		var role, message, response, id, mType, mConfig string
		if err := rows.Scan(&role, &message, &response, &id, &mType, &mConfig); err != nil {
			slog.Info("history: failed to scan row:", "error:", err)
			continue
		}
		msg := make(map[string]string)
		msg["role"] = role
		msg["content"] = message
		// Every caller feeds history to a model: a chart's points are for the reader.
		msg["response"] = toolcore.StripChartData(response)
		msg["id"] = id
		msg["message_type"] = mType
		msg["message_config"] = mConfig
		messages = append(messages, msg)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("history: failed to iterate rows: %w", err)
	}
	return messages, nil
}

func (chat *ConversationDao) DeleteConversationToolCalls(conversationId string) error {
	query := `DELETE FROM llm_conversation_tool_calls WHERE conversation_id = $1`
	_, err := chat.dbManager.Db.Exec(query, conversationId)
	if err != nil {
		return fmt.Errorf("history: failed to delete conversation tool calls: %w", err)
	}
	return nil
}

// RetainConversationTokenUsage prepares a conversation's token usage rows to
// outlive it. The rows have no foreign key to the conversation (V986), so the
// delete leaves them alone; this stamps who the spend belongs to (rows written
// before tenant_id/session_id existed have neither), clears the prompt,
// response and error text, which must go with the conversation, and unlinks
// them so budgets find them as deleted-conversation spend. Call it immediately before deleting the conversation row,
// so a usage row written by a call still finishing is cleared too.
func (chat *ConversationDao) RetainConversationTokenUsage(conversationId string) error {
	query := `UPDATE llm_conversation_token_usage t
		SET tenant_id = COALESCE(t.tenant_id, c.tenant_id),
			session_id = COALESCE(t.session_id, c.session_id),
			conversation_account_id = COALESCE(t.conversation_account_id, c.account_id),
			prompt_messages = NULL,
			response_content = NULL,
			error_message = NULL,
			conversation_id = NULL,
			message_id = NULL
		FROM llm_conversations c
		WHERE c.id = t.conversation_id AND t.conversation_id = $1`
	if _, err := chat.dbManager.Db.Exec(query, conversationId); err != nil {
		return fmt.Errorf("history: failed to retain token usage: %w", err)
	}
	return nil
}

// UnlinkLateTokenUsage clears the text of, and unlinks, usage rows written for
// a conversation after RetainConversationTokenUsage ran, by a call that was
// still finishing while the conversation was deleted. With no foreign key
// (V986) such a row would otherwise keep its text and a dangling id. Call it
// after deleting the conversation.
func (chat *ConversationDao) UnlinkLateTokenUsage(conversationId string) error {
	query := `UPDATE llm_conversation_token_usage
		SET prompt_messages = NULL,
			response_content = NULL,
			error_message = NULL,
			conversation_id = NULL,
			message_id = NULL
		WHERE conversation_id = $1`
	if _, err := chat.dbManager.Db.Exec(query, conversationId); err != nil {
		return fmt.Errorf("history: failed to unlink late token usage: %w", err)
	}
	return nil
}

func (chat *ConversationDao) DeleteConversationMessages(conversationId string) error {
	query := `DELETE FROM llm_conversation_messages WHERE conversation_id = $1`
	_, err := chat.dbManager.Db.Exec(query, conversationId)
	if err != nil {
		return fmt.Errorf("history: failed to delete conversation messages: %w", err)
	}
	return nil
}

func (chat *ConversationDao) DeleteConversationAgents(conversationId string) error {
	query := `DELETE FROM llm_conversation_agent WHERE conversation_id = $1`
	_, err := chat.dbManager.Db.Exec(query, conversationId)
	if err != nil {
		return fmt.Errorf("history: failed to delete conversation agents: %w", err)
	}
	return nil
}

func (chat *ConversationDao) DeleteConversationReferences(conversationId string) error {
	query := `DELETE FROM llm_conversation_references WHERE conversation_id = $1`
	_, err := chat.dbManager.Db.Exec(query, conversationId)
	if err != nil {
		return fmt.Errorf("history: failed to delete conversation references: %w", err)
	}
	return nil
}

func (chat *ConversationDao) DeleteConversationMemories(conversationId string) error {
	// 1. Fetch IDs to delete from RAG
	querySelect := `SELECT id::text, account_id::text FROM llm_conversation_memory WHERE conversation_id = $1`
	rows, err := chat.dbManager.Db.Queryx(querySelect, conversationId)
	if err != nil {
		return fmt.Errorf("history: failed to list conversation memories for deletion: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			slog.Warn("history: failed to close rows during conversation memory cleanup", "error", err)
		}
	}()

	type MemInfo struct {
		ID        string `db:"id"`
		AccountID string `db:"account_id"`
	}
	var mems []MemInfo
	for rows.Next() {
		var m MemInfo
		if err := rows.StructScan(&m); err != nil {
			return fmt.Errorf("history: failed to scan memory row: %w", err)
		}
		mems = append(mems, m)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("history: cursor error reading memories: %w", err)
	}

	// 2. Delete from RAG
	for _, m := range mems {
		if err := knowledge.DeleteMemoryFromRAG(m.AccountID, m.ID); err != nil {
			slog.Warn("history: failed to delete memory from RAG during conversation cleanup", "error", err, "id", m.ID)
		}
	}

	// 3. Delete from DB
	query := `DELETE FROM llm_conversation_memory WHERE conversation_id = $1`
	_, err = chat.dbManager.Db.Exec(query, conversationId)
	if err != nil {
		return fmt.Errorf("history: failed to delete conversation memories: %w", err)
	}
	return nil
}

func (chat *ConversationDao) DeleteConversationSaved(conversationId string) error {
	query := `DELETE FROM llm_conversation_saved WHERE conversation_id = $1`
	_, err := chat.dbManager.Db.Exec(query, conversationId)
	if err != nil {
		return fmt.Errorf("history: failed to delete conversation saved: %w", err)
	}
	return nil
}

func DeleteConversationBySession(sessionID string, accountId string, userId string) error {
	history := GetConversationDao()
	if history == nil {
		return fmt.Errorf("history: conversation dao is not initialized")
	}
	for {
		conversation, err := history.GetConversationBySession(accountId, sessionID)
		if err != nil {
			return err
		}

		if conversation.ID == uuid.Nil {
			break
		}

		err = DeleteConversation(conversation.ID)
		if err != nil {
			return err
		}
	}

	return nil
}

func DeleteConversation(conversationId uuid.UUID) error {
	if conversationId == uuid.Nil {
		return nil
	}
	history := GetConversationDao()
	if err := history.DeleteConversationToolCalls(conversationId.String()); err != nil {
		slog.Error("history: failed to delete tool calls", "error", err, "conversation_id", conversationId)
	}
	if err := history.DeleteConversationAgents(conversationId.String()); err != nil {
		slog.Error("history: failed to delete agents", "error", err, "conversation_id", conversationId)
	}
	if err := history.DeleteConversationMessages(conversationId.String()); err != nil {
		slog.Error("history: failed to delete messages", "error", err, "conversation_id", conversationId)
	}
	if err := history.DeleteConversationSaved(conversationId.String()); err != nil {
		slog.Error("history: failed to delete saved conversation", "error", err, "conversation_id", conversationId)
	}
	if err := history.DeleteConversationReferences(conversationId.String()); err != nil {
		slog.Error("history: failed to delete references", "error", err, "conversation_id", conversationId)
	}
	if err := history.DeleteConversationMemories(conversationId.String()); err != nil {
		slog.Error("history: failed to delete memories", "error", err, "conversation_id", conversationId)
	}
	// Spend outlives the conversation; its text does not. Runs last, just before
	// the conversation row goes (the rows are still linked to it), and a failure
	// stops the delete so no text is left behind on unlinked rows.
	if err := history.RetainConversationTokenUsage(conversationId.String()); err != nil {
		slog.Error("history: failed to retain token usage", "error", err, "conversation_id", conversationId)
		return err
	}
	if err := history.DeleteConversation(conversationId.String()); err != nil {
		slog.Error("history: failed to delete conversation", "error", err, "conversation_id", conversationId)
		return err
	}
	// The conversation is gone either way; a failure here only leaves a late
	// row's text behind, so it is logged rather than returned.
	if err := history.UnlinkLateTokenUsage(conversationId.String()); err != nil {
		slog.Error("history: failed to unlink late token usage", "error", err, "conversation_id", conversationId)
	}
	return nil
}

// UpdateConversationContext updates the context field for a conversation
func (chat *ConversationDao) UpdateConversationContext(conversationId string, context map[string]any) error {
	if len(context) == 0 {
		return nil
	}

	contextJSON, err := common.MarshalJson(context)
	if err != nil {
		return fmt.Errorf("history: failed to marshal context: %w", err)
	}

	query := `
    UPDATE llm_conversations
    SET context = $2, updated_at = now()
    WHERE id = $1;
    `

	_, err = chat.dbManager.Db.Exec(query, conversationId, contextJSON)
	if err != nil {
		return fmt.Errorf("history: failed to update context: %w", err)
	}
	return nil
}

func (chat *ConversationDao) UpdateConversationMessageContext(messageId string, context map[string]any) error {
	if len(context) == 0 {
		return nil
	}

	contextJSON, err := common.MarshalJson(context)
	if err != nil {
		return fmt.Errorf("history: failed to marshal context: %w", err)
	}

	query := `
	UPDATE llm_conversation_messages
	SET message_context = $2, updated_at = now()
	WHERE id = $1;
	`

	_, err = chat.dbManager.Db.Exec(query, messageId, contextJSON)
	if err != nil {
		return fmt.Errorf("history: failed to update message context: %w", err)
	}
	return nil
}

func (chat *ConversationDao) GetConversationContext(conversationId string) (map[string]any, error) {
	query := `SELECT context::text FROM llm_conversations WHERE id = $1`
	var context sql.NullString
	err := chat.dbManager.Db.QueryRow(query, conversationId).Scan(&context)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("history: failed to get context: %w", err)
	}
	if context.String == "" {
		return nil, nil
	}
	var contextMap map[string]any
	err = common.UnmarshalJson([]byte(context.String), &contextMap)
	if err != nil {
		return nil, fmt.Errorf("history: failed to unmarshal context: %w", err)
	}
	return contextMap, nil
}

func (chat *ConversationDao) UpdateMessageAcknowledgement(messageId, accountId, ackMessage string) error {
	query := `
	UPDATE llm_conversation_messages
	SET updated_at = now(), ack_message = $3
	WHERE id = $1 AND account_id = $2;
	`
	_, err := chat.dbManager.Db.Exec(query, messageId, accountId, ackMessage)
	if err != nil {
		return fmt.Errorf("history: failed to update message acknowledgement: %w", err)
	}
	return nil
}

func (chat *ConversationDao) UpdateConversationMessageMetrics(id string, metrics string) error {
	query := `UPDATE llm_conversation_messages SET eval_metrics = $2, updated_at = now() WHERE id = $1`
	_, err := chat.dbManager.Db.Exec(query, id, metrics)
	if err != nil {
		return fmt.Errorf("history: failed to update message metrics: %w", err)
	}
	return nil
}

// WallClockIdleGapSeconds is the longest quiet stretch still counted as part of a
// conversation's wall clock. A longer gap means the chat was left and reopened
// later (hours or days), and that idle time is not work: it would otherwise land
// in the UI's Overhead (wall - model - tool - wait) as 100h against 15m of
// activity.
const WallClockIdleGapSeconds = 30 * 60

// ConversationWallClockSQL returns a scalar subquery: the seconds between a
// conversation's first and last message activity, minus every idle gap of
// WallClockIdleGapSeconds or more between consecutive messages. convID is a SQL
// expression for the llm_conversations.id. NULL when the conversation has no
// messages, so callers pick their own fallback.
func ConversationWallClockSQL(convID string) string {
	return fmt.Sprintf(`(
		SELECT EXTRACT(EPOCH FROM (MAX(w.e) - MIN(w.s)))
			- COALESCE(SUM(CASE WHEN w.gap >= make_interval(secs => %d) THEN EXTRACT(EPOCH FROM w.gap) END), 0)
		FROM (
			SELECT q.s, q.e,
				q.s - MAX(q.e) OVER (ORDER BY q.s ROWS BETWEEN UNBOUNDED PRECEDING AND 1 PRECEDING) AS gap
			FROM (
				SELECT m.created_at AS s, COALESCE(m.responded_at, m.updated_at) AS e
				FROM llm_conversation_messages m
				WHERE m.conversation_id = %s
			) q
		) w
	)`, WallClockIdleGapSeconds, convID)
}

// WallClockActiveSeconds is the Go counterpart of ConversationWallClockSQL for
// callers that already hold the timestamps: the first-start to last-end span with
// idle gaps of WallClockIdleGapSeconds or more removed. spans are [start, end]
// pairs; a point event has start == end.
func WallClockActiveSeconds(spans [][2]time.Time) float64 {
	if len(spans) == 0 {
		return 0
	}
	sort.Slice(spans, func(i, j int) bool { return spans[i][0].Before(spans[j][0]) })
	first, maxEnd := spans[0][0], spans[0][1]
	idle := 0.0
	for _, sp := range spans[1:] {
		if gap := sp[0].Sub(maxEnd).Seconds(); gap >= WallClockIdleGapSeconds {
			idle += gap
		}
		if sp[1].After(maxEnd) {
			maxEnd = sp[1]
		}
	}
	return math.Max(0, maxEnd.Sub(first).Seconds()-idle)
}

func (chat *ConversationDao) GetLatestConversationBySessionID(sessionID string, accountId string) (Conversation, error) {
	query := `SELECT id::text, user_id, session_id, account_id, context::text, status, tenant_id::text, title, source, run_profile FROM llm_conversations WHERE session_id = $1 AND account_id = $2 ORDER BY updated_at DESC LIMIT 1`
	rows, err := chat.dbManager.Db.Queryx(query, sessionID, accountId)
	if err != nil {
		return Conversation{}, fmt.Errorf("history: failed to load conversation: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			slog.Error("conversation_dao: failed to close rows", "error", err)
		}
	}()
	var conversation Conversation
	var id, sessionId, accoundId, tenantId string
	var title, userId *string
	var status ConversationStatus
	var source *ConversationSource
	var cntxt sql.NullString
	var runProfile nullRunProfile
	for rows.Next() {
		if err := rows.Scan(&id, &userId, &sessionId, &accoundId, &cntxt, &status, &tenantId, &title, &source, &runProfile); err != nil {
			return Conversation{}, fmt.Errorf("history: failed to scan conversation: %w", err)
		}
		var context map[string]any
		if cntxt.String != "" {
			cntxtStr := strings.Trim(cntxt.String, "'")
			if err := common.UnmarshalJson([]byte(cntxtStr), &context); err != nil {
				return Conversation{}, fmt.Errorf("history: failed to unmarshal context: %w", err)
			}
		}

		userIdUUID := uuid.Nil
		if userId != nil {
			userIdUUID = uuid.MustParse(*userId)
		}
		conversation = Conversation{
			ID:         uuid.MustParse(id),
			SessionID:  sessionId,
			AccountID:  uuid.MustParse(accoundId),
			UserID:     userIdUUID,
			Context:    context,
			Status:     status,
			TenantID:   uuid.MustParse(tenantId),
			Title:      title,
			Source:     source,
			RunProfile: runProfile.profile(),
		}
	}

	if err := rows.Err(); err != nil {
		return Conversation{}, fmt.Errorf("history: failed to iterate over rows: %w", err)
	}

	if conversation.ID == uuid.Nil {
		return Conversation{}, ErrConversationNotFound
	}
	return conversation, nil
}

func (chat *ConversationDao) GetLatestConversationBySessionIDWithMessages(sessionID string, accountId string) (*ConversationWithMessages, error) {
	conversation, err := chat.GetLatestConversationBySessionID(sessionID, accountId)
	if err != nil {
		return nil, err
	}
	return chat.getConversationWithMessages(conversation, accountId)
}

func (chat *ConversationDao) GetConversationWithMessages(conversationId string, accountId string) (*ConversationWithMessages, error) {
	conversation, err := chat.GetConversation(conversationId)
	if err != nil {
		return nil, err
	}
	return chat.getConversationWithMessages(conversation, accountId)
}

func (chat *ConversationDao) getConversationWithMessages(conversation Conversation, accountId string) (*ConversationWithMessages, error) {
	if conversation.AccountID.String() != accountId {
		return nil, ErrConversationNotFound
	}

	messages, err := chat.ListConversationMessages("", "", conversation.ID.String(), false)
	if err != nil {
		return nil, err
	}

	messagesWithAgents := []ConversationMessageWithAgents{}
	for _, msg := range messages {
		agents, err := chat.ListConversationAgents(msg.ID.String(), "")
		if err != nil {
			slog.Error("failed to get agents for message", "message_id", msg.ID.String(), "error", err)
			agents = []ConversationAgent{}
		}
		messagesWithAgents = append(messagesWithAgents, ConversationMessageWithAgents{
			ConversationMessage: msg,
			Agents:              agents,
		})
	}

	return &ConversationWithMessages{
		Conversation: conversation,
		Messages:     messagesWithAgents,
	}, nil
}

func (chat *ConversationDao) ListConversations(accountId string, userId string, queryLike string, source string, limit int, offset int) ([]Conversation, error) {
	query := `SELECT id::text, user_id, session_id, account_id, status, tenant_id::text, title, source FROM llm_conversations WHERE account_id = $1`
	args := []any{accountId}
	argCounter := 2

	if userId != "" {
		query += fmt.Sprintf(" AND user_id = $%d", argCounter)
		args = append(args, userId)
		argCounter++
	}

	if source != "" {
		query += fmt.Sprintf(" AND source = $%d", argCounter)
		args = append(args, source)
		argCounter++
	}

	if queryLike != "" {
		query += fmt.Sprintf(" AND id IN (SELECT conversation_id FROM llm_conversation_messages WHERE message ILIKE $%d)", argCounter)
		args = append(args, "%"+queryLike+"%")
		argCounter++
	}

	query += fmt.Sprintf(" ORDER BY updated_at DESC LIMIT $%d OFFSET $%d", argCounter, argCounter+1)
	args = append(args, limit, offset)

	rows, err := chat.dbManager.Db.Queryx(query, args...)
	if err != nil {
		return nil, fmt.Errorf("history: failed to list conversations: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			slog.Error("conversation_dao: failed to close rows", "error", err)
		}
	}()

	var conversations []Conversation
	for rows.Next() {
		var conversation Conversation
		var id, sessionId, accoundId, tenantId string
		var title, dbUserId *string
		var status ConversationStatus
		var dbSource *ConversationSource

		if err := rows.Scan(&id, &dbUserId, &sessionId, &accoundId, &status, &tenantId, &title, &dbSource); err != nil {
			return nil, fmt.Errorf("history: failed to scan conversation: %w", err)
		}

		userIdUUID := uuid.Nil
		if dbUserId != nil {
			parsedId, err := uuid.Parse(*dbUserId)
			if err != nil {
				slog.Warn("history: failed to parse user_id, setting to Nil", "user_id", *dbUserId, "error", err)
			} else {
				userIdUUID = parsedId
			}
		}

		tenantIdUUID, err := uuid.Parse(tenantId)
		if err != nil {
			slog.Warn("history: failed to parse tenant_id, setting to Nil", "tenant_id", tenantId, "error", err)
		}

		conversation = Conversation{
			ID:        uuid.MustParse(id),
			SessionID: sessionId,
			AccountID: uuid.MustParse(accoundId),
			UserID:    userIdUUID,
			Context:   nil,
			Status:    status,
			TenantID:  tenantIdUUID,
			Title:     title,
			Source:    dbSource,
		}
		conversations = append(conversations, conversation)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("history: failed to iterate over rows: %w", err)
	}

	return conversations, nil
}

func (chat *ConversationDao) SaveCritique(critique *ConversationCritique) error {

	query := `
	INSERT INTO llm_conversation_agent_critiques (conversation_id, message_id, account_id, agent_name, critiqued_content, input, critique_type, feedback, decision) 
	VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9);`
	_, err := chat.dbManager.Db.Exec(query, critique.ConversationID, critique.MessageID, critique.AccountID, critique.AgentName, critique.CritiquedContent, critique.Input, critique.CritiqueType, critique.Feedback, critique.Decision)
	if err != nil {
		return fmt.Errorf("history: failed to save critique: %w", err)
	}
	return nil
}

func (chat *ConversationDao) UpdateMessageProductivityMetrics(messageId string, classification string, successfulTasks int) error {
	query := `
	UPDATE llm_conversation_messages 
	SET classification = $2, 
	    successful_tasks = $3, 
	    updated_at = now()
	WHERE id = $1;`
	_, err := chat.dbManager.Db.Exec(query, messageId, classification, successfulTasks)
	if err != nil {
		return fmt.Errorf("history: failed to update message productivity metrics: %w", err)
	}
	return nil
}

func (chat *ConversationDao) GetSuccessfulToolCallsCountByMessage(messageId string) (int, error) {
	query := `
	SELECT count(*) 
	FROM llm_conversation_tool_calls 
	WHERE message_id = $1 AND status = 'success' AND metadata->>'parent_tool_call_id' IS NULL;`
	var count int
	err := chat.dbManager.Db.Get(&count, query, messageId)
	if err != nil {
		return 0, fmt.Errorf("history: failed to count successful tool calls for message: %w", err)
	}
	return count, nil
}

// ToolCallTrace is one tool call as the post-hoc answer-confidence grader sees
// it: what ran, with which input, how it ended, and a bounded slice of what it
// returned. The grader needs the output, not just the tool name — a single
// shell tool can supply logs, config and events in one answer, and a name-only
// manifest cannot tell that apart from four repeats of the same lookup.
//
// The output goes to a second LLM call through the same egress-filtered,
// PII-scrubbed model wrapper (llm_config.go) as the investigation that
// produced it, so it reaches no provider that has not already seen it.
type ToolCallTrace struct {
	ToolName   string `db:"tool_name"`
	Parameters string `db:"parameters"`
	Status     string `db:"status"`
	Response   string `db:"response"`
}

// ListToolCallTraceByMessage returns up to maxRows of a message's tool calls,
// oldest first. Parameters are cut to maxInputChars and a response longer than
// maxOutputChars keeps its head and tail halves (the command's framing and its
// final lines); both carry an explicit truncation marker, charged to the budget
// so neither value exceeds the cap it was given.
//
// Both bounds matter and neither substitutes for the other: the per-row cuts
// stop one multi-MB log dump crossing the wire, maxRows stops a runaway
// investigation (the record holder ran 18,685 tool calls) doing the same with
// volume. Bounded by construction rather than by what the caller's gate happens
// to exclude — the manifest this replaced was bounded twice, and grading is a
// background task nobody is waiting on, so a quiet cap beats an OOM.
//
// When the cap binds, the NEWEST calls are kept: they are the most distilled
// and sit closest to the conclusion being graded, the same reasoning
// buildSubAgentEvidence uses. The caller marks the trace as abridged.
//
// Ordering is by created_at, which is the transaction start, so tool calls
// issued in parallel tie and fall back to id. Stable across reads; not
// guaranteed to be the true execution order for concurrent calls.
func (chat *ConversationDao) ListToolCallTraceByMessage(messageId string, maxInputChars, maxOutputChars, maxRows int) ([]ToolCallTrace, error) {
	query := `
	SELECT tool_name, parameters, status, response FROM (
		SELECT tool_name,
		       CASE WHEN length(COALESCE(parameters, '')) > $2
		            -- 20 = length(' [... truncated ...]'). A clipped inline script or
		            -- YAML payload would otherwise read as the complete input, and the
		            -- prompt only warns the grader that outputs are cut.
		            THEN LEFT(parameters, GREATEST($2 - 20, 0)) || ' [... truncated ...]'
		            ELSE COALESCE(parameters, '')
		       END AS parameters,
		       COALESCE(status, '') AS status,
		       CASE WHEN length(COALESCE(response, '')) > $3
		            -- 21 = length(' [... truncated ...] ')
		            THEN LEFT(response, GREATEST(($3 - 21) / 2, 0)) || ' [... truncated ...] ' || RIGHT(response, GREATEST(($3 - 21) / 2, 0))
		            ELSE COALESCE(response, '')
		       END AS response,
		       created_at, id
		FROM llm_conversation_tool_calls
		WHERE message_id = $1 AND COALESCE(tool_name, '') <> ''
		ORDER BY created_at DESC, id DESC
		LIMIT $4
	) t
	ORDER BY created_at ASC, id ASC;`
	calls := []ToolCallTrace{}
	if err := chat.dbManager.Db.Select(&calls, query, messageId, maxInputChars, maxOutputChars, maxRows); err != nil {
		return nil, fmt.Errorf("history: failed to list tool call trace for message: %w", err)
	}
	return calls, nil
}
