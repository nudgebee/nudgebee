package core

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

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

type Conversation struct {
	TenantID    uuid.UUID           `json:"tenant_id,omitempty" db:"tenant_id"`
	AccountID   uuid.UUID           `json:"account_id" db:"account_id"`
	UserID      uuid.UUID           `json:"user_id" db:"user_id"`
	SessionID   string              `json:"session_id" db:"session_id"`
	ID          uuid.UUID           `json:"id" db:"id"`
	Context     map[string]any      `json:"context,omitempty" db:"context"`
	Status      ConversationStatus  `json:"status,omitempty" db:"status"`
	Title       *string             `json:"title,omitempty" db:"title"`
	Source      *ConversationSource `json:"source,omitempty" db:"source"`
	LlmProvider *string             `json:"llm_provider,omitempty" db:"llm_provider"`
	LlmModel    *string             `json:"llm_model,omitempty" db:"llm_model"`
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

type LongTermMemory struct {
	ID             uuid.UUID  `json:"id" db:"id"`
	AccountID      uuid.UUID  `json:"account_id" db:"account_id"`
	ConversationID *uuid.UUID `json:"conversation_id,omitempty" db:"conversation_id"`
	MessageID      *uuid.UUID `json:"message_id,omitempty" db:"message_id"`
	Content        string     `json:"content" db:"content"`
	MemoryType     MemoryType `json:"memory_type" db:"memory_type"`
	UseCount       int        `json:"use_count" db:"use_count"`
	LastUsedAt     *time.Time `json:"last_used_at,omitempty" db:"last_used_at"`
	CreatedAt      time.Time  `json:"created_at" db:"created_at"`
	// SimilarityScore is populated only when the memory is returned from a RAG search;
	// it is not persisted.
	SimilarityScore float32 `json:"similarity_score,omitempty" db:"-"`
}

type IConversationDao interface {
	SaveConversationAgentCall(conversationId, messageId, accountId, userId, agentName, parentAgentId, query, thought, response, queryContext string, queryConfig toolcore.NBQueryConfig) (uuid.UUID, error)
	SaveCompletedConversationAgentCall(id uuid.UUID, conversationId, messageId, accountId, userId, agentName, parentAgentId, query, thought, response, queryContext string, queryConfig toolcore.NBQueryConfig, status AgentExecutionStatus, responseSummary string) (uuid.UUID, error)
	UpdateConversationAgentResponse(agenId, response string, agentStatus AgentExecutionStatus, state string, responseSummary string, agentStepResponse string, references string) error
	UpdateConversationAgentSummary(agentId, responseSummary string) error
	GetConversationBySession(accountID, sessionID string) (Conversation, error)
	UpdateConversationTitle(conversationId, title string) error
	UpdateConversationModel(conversationId, provider, model string) error
	UpdateMessageAcknowledgement(messageId, accountId, ackMessage string) error
	UpdateConversationContext(conversationId string, context map[string]any) error
	GetConversation(conversationId string) (Conversation, error)
	SaveConversation(id, sessionID, tenantId, accountID, userId, context, title string, status ConversationStatus, source ConversationSource, llmProvider, llmModel string) (uuid.UUID, error)
	UpdateConversationStatus(conversationId string, status ConversationStatus) error
	ListConversationMessages(status ConversationStatus, workerName string, conversationId string, deadWorker bool) ([]ConversationMessage, error)
	SaveConversationMessage(id, conversationId, accountID, userId string, role MessageRole, messageType MessageType, message, response, agentName string, parentAgentId uuid.UUID, messageConfig any, messageContext string, llmProvider, llmModel string) (uuid.UUID, error)
	GetConversationMessage(id, accountId, conversationId string) (ConversationMessage, error)
	CleanupConversationMessage(id, accountId string) error
	UpdateConversationMessageContext(messageId string, context map[string]any) error
	UpdateConversationMessage(id, response string, status ConversationStatus) error
	UpdateConversationMessageAsync(id, response string, status ConversationStatus)
	UpdateMessageCompactResponse(context *security.RequestContext, messageId, compactResponse string) error
	UpdateConversationMessageMetrics(id string, metrics string) error
	ListConversationAgents(messageId, agentId string) ([]ConversationAgent, error)
	DeleteConversationToolCalls(conversationId string) error
	DeleteConversationAgents(conversationId string) error
	DeleteConversationMessages(conversationId string) error
	DeleteConversationSaved(conversationId string) error
	DeleteConversationReferences(conversationId string) error
	DeleteConversationMemories(conversationId string) error
	DeleteConversation(conversationId string) error
	SaveConversationMessageSuggestion(messageId string, accountId string, conversationId string, messages []ConversationSuggestionMessageResponse) error
	GetConversationAgentParentAgentIdAndPreviousState(agenId string) (string, string)
	UpdateConversationMessageConfig(id string, config toolcore.NBQueryConfig) error
	UpdateConversationMessageFollowupConfig(id string, followupConfig map[string]any) error
	LoadConversationMessages(accountID, conversationID, userID string, requestType MessageType, k int) ([]map[string]string, error)
	GetConversationAgentOutput(agentId, accountId string) (string, error)
	GetConversationToolResponse(toolId, messageId, conversationId, accountId string) (string, toolcore.NBToolResponseStatus, error)
	GetConversationToolCallChildAgentId(conversationId, agenId, toolId string) string
	UpdateConversationAgentWithFollowup(agentId, followupMessageId string) error
	FindActiveSiblingToolConfigFollowup(accountId, conversationId, userMessageId, toolName string) (uuid.UUID, error)
	ListAgentsByFollowupMessageId(accountId, followupMessageId string) ([]ConversationAgent, error)
	UpdateConversationToolResponse(toolId, messageId, conversationId, accountId, response string, status toolcore.NBToolResponseStatus) error
	GetConversationMessageByAgentId(agentId string) (string, error)
	GetAgentNameFromAgentId(agentId string) (string, error)
	UpdateConversationAgentThought(agentId, thought string) error
	GetConversationAgentInput(agentId, accountId string) (string, error)
	SaveConversationToolCall(conversationID, accountID, userID, messageId, agenId, toolId, toolName, toolArgs, thought, toolArgsSql, toolResult string, status toolcore.NBToolResponseStatus, toolType toolcore.NBToolType, childAgentId *string, references []toolcore.NBToolResponseReference) error
	TerminateConversation(context *security.RequestContext, accountId, conversationId string) error
	CountWaitingSubAgents(parentAgentId, messageId string) (int, error)
	MarkInProgressConversationAsKilled() error
	GetConversationTokenUsage(conversationId string) ([]TokenMetrics, error)
	GetConversationCost(provider string, model string, nonCachedInputTokens, cachedInputTokens, cacheCreationTokens, outputTokens, thinkingTokens int) (float64, error)
	GetConversationCosts(models []string) (map[string]modelPricing, error)
	GetConversationTokenUsageDetailed(conversationId string) ([]TokenUsageDetailedRecord, error)
	GetConversationLifecycleStorageCost(conversationId string, tenantId string) (float64, error)
	GetConversationToolCallsStats(conversationId string) (ToolCallsStats, error)
	GetConversationTimeBreakdown(conversationId string) (TimeBreakdown, error)
	GetConversationTimeAggregates(filter ConversationTimeAggregatesFilter) (ConversationTimeAggregates, error)
	InsertTokenUsage(record *TokenUsageRecord) error
	InsertCacheLifecycle(ctx context.Context, record *CacheLifecycleRecord) error
	SetCacheLifecycleInvalidated(ctx context.Context, cacheName string) error
	GetConversationWithMessages(conversationId string, accountId string) (*ConversationWithMessages, error)
	GetLatestConversationBySessionIDWithMessages(sessionID string, accountId string) (*ConversationWithMessages, error)
	ListConversations(accountId string, userId string, queryLike string, source string, limit int, offset int) ([]Conversation, error)
	SaveCritique(critique *ConversationCritique) error
	SaveLongTermMemory(accountId, conversationId, messageId, content string, memoryType MemoryType) (string, error)
	LoadLongTermMemories(accountId string, query string, limit int) ([]string, error)
	ListLongTermMemories(accountId string, conversationId string, messageId string, memoryType string, query string, limit int, offset int) ([]LongTermMemory, error)
	DeleteLongTermMemory(id, accountId string) error
	UpdateMessageProductivityMetrics(messageId string, classification string, successfulTasks int) error
	GetSuccessfulToolCallsCountByMessage(messageId string) (int, error)
	RetrieveRelevantMemories(accountId string, query string, limit int) ([]LongTermMemory, error)
	// FindSimilarMemories performs a RAG similarity search using content as the query and
	// populates SimilarityScore on each result. Unlike RetrieveRelevantMemories it does NOT
	// update use_count — it is intended for deduplication probes at save time.
	FindSimilarMemories(accountId string, content string, limit int) ([]LongTermMemory, error)
	// CarryOverMemoryUseCount copies the use_count from an old memory to a newly inserted one.
	// Called after a memory update (insert-then-delete) so highly-used memories don't lose
	// their access history when their content is clarified or extended.
	CarryOverMemoryUseCount(oldID, newID, accountId string)
	// IncrementMemoryUsage bumps use_count and last_used_at for the given memories.
	// Used when memories are retrieved via FindSimilarMemories (no auto-bump) but later
	// determined to actually be surfaced to the agent.
	IncrementMemoryUsage(accountId string, memories []LongTermMemory)
	SaveAgentReferences(accountId, conversationId, messageId, agentId string, references []AgentReference) error
	ListAgentReferences(accountId, conversationId, messageId, agentId string, limit int) ([]ConversationReference, error)
	SaveConversationContextReference(accountId, conversationId, messageId string, unifiedExtraction *LlmUnifiedExtraction) error
}

type AgentReferenceType string

const (
	AgentReferenceTypeMemory       AgentReferenceType = "memory"
	AgentReferenceTypeKB           AgentReferenceType = "knowledge_base"
	AgentReferenceTypeContextState AgentReferenceType = "context_state"
)

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
}

type ConversationDao struct {
	dbManager *common.DatabaseManager
}

// TokenUsageRecord represents a single LLM API call token usage
type TokenUsageRecord struct {
	ConversationID      string
	MessageID           string
	AgentID             *string // Nullable
	AgentName           string
	AccountID           string
	UserID              string // Nullable
	LLMProvider         string
	LLMModel            string
	InputTokens         int
	OutputTokens        int
	CachedInputTokens   int
	CacheCreationTokens int
	IsCacheHit          bool
	CacheHitRate        *float64 // Nullable
	RetryAttempt        int
	FallbackFromModel   *string  // Nullable - immediate previous model
	FallbackChain       *string  // Nullable - JSON array of all fallback models
	LatencySeconds      *float64 // Nullable
	RequestStatus       string   // 'success' or 'failure'
	ErrorMessage        *string  // Nullable
	ContentLength       *int     // Nullable
	StopReason          *string  // Nullable
	CacheTTLMinutes     *int     // Nullable - cache TTL in minutes at time of request
	PromptMessages      *string  // Nullable - serialized prompt messages JSON (when LLM trace is enabled)
	ResponseContent     *string  // Nullable - full LLM response text (when LLM trace is enabled)
	ThinkingTokens      *int     // Nullable - Gemini 2.5+ hidden chain-of-thought tokens (usage.ThoughtsTokenCount). NULL when 0/unavailable.
	// Streaming-latency breakdown. ttft_ms / itl_ms_avg / tokens_per_second
	// are NULL on non-streaming or legacy rows. was_streaming is FALSE on new
	// non-streaming rows and NULL only on legacy rows pre-V722.
	TTFTMs          *int64   // Time-to-first-token in ms.
	ITLMsAvg        *float64 // Average inter-token latency in ms ((latency_ms - ttft_ms) / output_tokens). float to preserve sub-ms precision on fast models.
	TokensPerSecond *float64 // Steady-state output throughput.
	WasStreaming    *bool    // Whether the call streamed (≥1 chunk observed).
}

func (chat *ConversationDao) ListAgentReferences(accountId, conversationId, messageId, agentId string, limit int) ([]ConversationReference, error) {
	query := `SELECT r.id, r.account_id, r.conversation_id, r.message_id, r.agent_id, r.reference_id, r.reference_type, r.metadata::text, r.created_at,
			    CASE
					WHEN r.reference_type = 'context_state' THEN r.metadata::text
					WHEN r.reference_type = 'memory' THEN m.content
					WHEN r.reference_type = 'knowledge_base' THEN k.data
				END AS content
			  FROM llm_conversation_references r
			  LEFT JOIN llm_conversation_memory m ON (r.reference_id::uuid = m.id AND r.reference_type = 'memory')
			  LEFT JOIN llm_knowledgebases k ON (r.reference_id::uuid = k.id AND r.reference_type = 'knowledge_base')
			  WHERE r.account_id = $1::text`
	args := []any{accountId}
	argCounter := 2

	if conversationId != "" {
		query += fmt.Sprintf(" AND r.conversation_id = $%d::text", argCounter)
		args = append(args, conversationId)
		argCounter++
	}
	if messageId != "" {
		query += fmt.Sprintf(" AND r.message_id = $%d::text", argCounter)
		args = append(args, messageId)
		argCounter++
	}
	if agentId != "" {
		query += fmt.Sprintf(" AND r.agent_id = $%d::uuid", argCounter)
		args = append(args, agentId)
		argCounter++
	}

	query += fmt.Sprintf(" ORDER BY r.created_at DESC LIMIT $%d", argCounter)
	args = append(args, limit)

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
		if err := rows.Scan(&ref.ID, &ref.AccountID, &ref.ConversationID, &ref.MessageID, &ref.AgentID, &ref.ReferenceID, &ref.Type, &metaStr, &ref.CreatedAt, &content); err != nil {
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
			if ok, _ := isAcceptableMemoryFact(trimmedContent); !ok {
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

func (chat *ConversationDao) RetrieveRelevantMemories(accountId string, queryStr string, limit int) ([]LongTermMemory, error) {
	if queryStr == "" {
		return nil, nil
	}
	results := toolcore.QueryRAG("", accountId, queryStr, "long_term_memory", limit, "", "", "", false)
	memories := parseRAGMemoryResults(results)
	slog.Info("LTM fetched memories", "count", len(memories))

	// Increment use_count and refresh last_used_at for every surfaced memory.
	// Uses the bounded status-update worker pool to prevent goroutine/connection spikes
	// under high load. Variables are passed explicitly to the closure to avoid capture bugs.
	// Failures are non-fatal — the query result is still returned.
	chat.incrementMemoryUsageAsync(accountId, memories)

	return memories, nil
}

// FindSimilarMemories queries the RAG index with content as the search key and returns
// results with SimilarityScore populated. It intentionally does NOT update use_count
// because it is used as a pre-save deduplication probe, not as a user-facing retrieval.
func (chat *ConversationDao) FindSimilarMemories(accountId string, content string, limit int) ([]LongTermMemory, error) {
	if content == "" {
		return nil, nil
	}
	results := toolcore.QueryRAG("", accountId, content, "long_term_memory", limit, "", "", "", false)
	memories := parseRAGMemoryResults(results)
	return memories, nil
}

// parseRAGMemoryResults converts raw RAG query results into LongTermMemory structs.
// It is the single place that maps RAG metadata (_id, memory_type, similarity_score)
// onto the Go struct, shared by RetrieveRelevantMemories and FindSimilarMemories.
func parseRAGMemoryResults(results toolcore.RAGSearchResults) []LongTermMemory {
	var memories []LongTermMemory
	for _, res := range results {
		if res.Document == "" {
			continue
		}
		memId := ""
		if val, ok := res.Metadata["_id"]; ok {
			if idStr, ok := val.(string); ok {
				memId = idStr
			}
		}
		if memId == "" {
			continue
		}
		parsedUUID, err := uuid.Parse(memId)
		if err != nil {
			slog.Warn("history: skipping memory with invalid UUID in RAG metadata", "id", memId, "error", err)
			continue
		}
		memType := MemoryTypeInvestigationResult
		if val, ok := res.Metadata["memory_type"]; ok {
			if typeStr, ok := val.(string); ok && typeStr != "" {
				memType = MemoryType(typeStr)
			}
		}
		memories = append(memories, LongTermMemory{
			ID:              parsedUUID,
			Content:         res.Document,
			MemoryType:      memType,
			SimilarityScore: res.SimilarityScore,
		})
	}
	return memories
}

// incrementMemoryUsageAsync bumps use_count and last_used_at for the given memories
// via the bounded status-update worker pool. Failures are non-fatal.
func (chat *ConversationDao) incrementMemoryUsageAsync(accountId string, memories []LongTermMemory) {
	if len(memories) == 0 {
		return
	}
	ids := make([]string, 0, len(memories))
	for _, m := range memories {
		ids = append(ids, m.ID.String())
	}
	updateQuery := `
		UPDATE llm_conversation_memory
		SET use_count    = use_count + 1,
		    last_used_at = now()
		WHERE account_id = $1
		  AND id = ANY($2::uuid[])`
	accId := accountId
	db := chat.dbManager.Db
	submissionCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := getStatusUpdateWorkerPool().Submit(submissionCtx, func() {
		dbCtx, dbCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer dbCancel()
		if _, err := db.ExecContext(dbCtx, updateQuery, accId, pq.Array(ids)); err != nil {
			slog.Warn("history: failed to update memory usage stats", "error", err)
		}
	}); err != nil {
		slog.Warn("history: failed to submit memory usage stat update to pool", "error", err)
	}
}

// IncrementMemoryUsage bumps use_count and last_used_at for a specific set of memories.
// Used when memories are retrieved via FindSimilarMemories (no auto-bump) but later
// determined to actually be surfaced to the agent.
func (chat *ConversationDao) IncrementMemoryUsage(accountId string, memories []LongTermMemory) {
	chat.incrementMemoryUsageAsync(accountId, memories)
}

// CarryOverMemoryUseCount copies use_count from the old memory row to the new one via
// the bounded status-update worker pool. This preserves access history when a memory is
// updated (insert-then-delete pattern). Failures are non-fatal.
func (chat *ConversationDao) CarryOverMemoryUseCount(oldID, newID, accountId string) {
	query := `
		UPDATE llm_conversation_memory AS dst
		SET use_count = src.use_count
		FROM llm_conversation_memory AS src
		WHERE dst.id       = $2::uuid
		  AND src.id       = $1::uuid
		  AND dst.account_id = $3
		  AND src.account_id = $3`
	db := chat.dbManager.Db
	submissionCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := getStatusUpdateWorkerPool().Submit(submissionCtx, func() {
		dbCtx, dbCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer dbCancel()
		if _, err := db.ExecContext(dbCtx, query, oldID, newID, accountId); err != nil {
			slog.Warn("history: failed to carry over memory use_count", "old_id", oldID, "new_id", newID, "error", err)
		}
	}); err != nil {
		slog.Warn("history: failed to submit carry-over use_count to pool", "old_id", oldID, "new_id", newID, "error", err)
	}
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

func SetConversationDao(dao IConversationDao) {
	conversationDao = dao
}

func (chat *ConversationDao) UpdateConversationStatus(conversationId string, status ConversationStatus) error {
	query := `
    update llm_conversations
	set status = $2, updated_at = now()
	where id = $1;
	`
	_, err := chat.dbManager.Db.Exec(query, conversationId, status)
	if err != nil {
		return fmt.Errorf("history: failed to update status: %w", err)
	}
	return nil
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

// UpdateConversationModel updates the LLM model configuration for a conversation
func (chat *ConversationDao) UpdateConversationModel(conversationId, provider, model string) error {
	if conversationId == "" {
		return errors.New("history: conversationId is required")
	}

	query := `UPDATE llm_conversations SET llm_provider = $2, llm_model = $3, updated_at = now() WHERE id = $1`
	_, err := chat.dbManager.Db.Exec(query, conversationId, provider, model)
	if err != nil {
		slog.Error("history: unable to update conversation model", "error", err)
		return fmt.Errorf("history: unable to update conversation model: %w", err)
	}
	return nil
}

func (chat *ConversationDao) SaveConversation(id, sessionID, tenantId, accountID, userId, context, title string, status ConversationStatus, source ConversationSource, llmProvider, llmModel string) (uuid.UUID, error) {
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

	query := `
    INSERT INTO llm_conversations (id, session_id, tenant_id, account_id, user_id, context, status, source, title, updated_at, llm_provider, llm_model) 
    VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, now(), $10, $11) 
    ON CONFLICT (session_id, user_id, account_id) 
    DO UPDATE SET context = EXCLUDED.context, status = EXCLUDED.status, updated_at = EXCLUDED.updated_at, llm_provider = EXCLUDED.llm_provider, llm_model = EXCLUDED.llm_model RETURNING llm_conversations.id;`
	var lastId uuid.UUID
	err := chat.dbManager.Db.QueryRow(query, id, sessionID, tenantId, accountID, userIdSql, context, status, source, title, llmProviderSql, llmModelSql).Scan(&lastId)
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
	_, err := chat.dbManager.Db.Exec(agentQuery, agenId, response, agentStatus, state, responseSummary, agentStepResponse, references)
	if err != nil {
		return fmt.Errorf("history: failed to update agent call: %w", err)
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
	query := `SELECT id, user_id, session_id, account_id, context::text, status, tenant_id::text, title, source, llm_provider, llm_model FROM llm_conversations 
	WHERE session_id = $1 AND account_id = $2`
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
	var llmProvider, llmModel *string
	for rows.Next() {
		if err := rows.Scan(&id, &userId, &sessionId, &accoundId, &cntxt, &status, &tenantId, &title, &source, &llmProvider, &llmModel); err != nil {
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
			ID:          uuid.MustParse(id),
			SessionID:   sessionId,
			AccountID:   uuid.MustParse(accoundId),
			UserID:      userIdUUID,
			Context:     context,
			Status:      status,
			TenantID:    uuid.MustParse(tenantId),
			Title:       title,
			Source:      source,
			LlmProvider: llmProvider,
			LlmModel:    llmModel,
		}
	}
	if err := rows.Err(); err != nil {
		return Conversation{}, fmt.Errorf("history: failed to iterate over rows: %w", err)
	}

	return conversation, nil
}

func (chat *ConversationDao) GetConversation(conversationId string) (Conversation, error) {
	query := `SELECT id::text, user_id, session_id, account_id, context::text, status, tenant_id::text, title, source, llm_provider, llm_model FROM llm_conversations WHERE id = $1`
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
	var llmProvider, llmModel *string
	for rows.Next() {
		if err := rows.Scan(&id, &userId, &sessionId, &accoundId, &cntxt, &status, &tenantId, &title, &source, &llmProvider, &llmModel); err != nil {
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
			ID:          uuid.MustParse(id),
			SessionID:   sessionId,
			AccountID:   uuid.MustParse(accoundId),
			UserID:      userIdUUID,
			Context:     context,
			Status:      status,
			TenantID:    uuid.MustParse(tenantId),
			Title:       title,
			Source:      source,
			LlmProvider: llmProvider,
			LlmModel:    llmModel,
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

func (chat *ConversationDao) SaveConversationMessage(id, conversationId, accountID, userId string, role MessageRole, messageType MessageType, message, response, agentName string, parentAgentId uuid.UUID, messageConfig any, messageContext string, llmProvider, llmModel string) (uuid.UUID, error) {
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
	err := chat.dbManager.Db.QueryRow(query, id, accountID, userIdSql, conversationId, role, message, response, messageType, ConversationStatusInProgress, config.Config.ServerName, agentName, parentAgentId, string(messageConfigJson), messageContext, llmProviderSql, llmModelSql).Scan(&lastId)
	if err != nil {
		return uuid.Nil, fmt.Errorf("history: failed to save message: %w", err)
	}
	slog.Info("dao: SaveConversationMessage complete", "duration", time.Since(t0).String(), "conversation_id", conversationId)
	return lastId, nil
}

func (chat *ConversationDao) UpdateConversationMessage(id, response string, status ConversationStatus) error {
	query := `UPDATE llm_conversation_messages SET response = $2, updated_at = now(), status = $3, worker_name = $4 WHERE id = $1`
	_, err := chat.dbManager.Db.Exec(query, id, response, string(status), config.Config.ServerName)
	if err != nil {
		return fmt.Errorf("history: failed to update message: %w", err)
	}
	return nil
}

var (
	conversationStatusUpdateWorkerPool *common.WorkerPool
	onceStatusUpdateWorkerPool         sync.Once
)

func getStatusUpdateWorkerPool() *common.WorkerPool {
	onceStatusUpdateWorkerPool.Do(func() {
		// Dedicated pool for fast DB I/O (status updates).
		// We use a high queue size (500) as these are non-blocking, quick operations.
		conversationStatusUpdateWorkerPool = common.NewWorkerPool("conversation_status_updates", config.Config.AsyncPlanExecutionWorkerCount, 500)
	})
	return conversationStatusUpdateWorkerPool
}

func (chat *ConversationDao) UpdateConversationMessageAsync(id, response string, status ConversationStatus) {
	if id == "" || id == uuid.Nil.String() {
		return
	}

	// Use a background context for the submission itself with a short timeout
	submissionCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := getStatusUpdateWorkerPool().Submit(submissionCtx, func() {
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

func (chat *ConversationDao) TerminateConversation(context *security.RequestContext, accountId, conversationId string) error {
	tx, err := chat.dbManager.Db.Beginx()
	if err != nil {
		return fmt.Errorf("history: failed to begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Update the message status to TERMINATED and capture affected message IDs so
	// we can invalidate their termination cache entries after commit.
	messageQuery := `UPDATE llm_conversation_messages SET updated_at = now(), status = $2, response = $4 WHERE conversation_id = $1 AND account_id = $3 AND status in ('IN_PROGRESS', 'WAITING', 'PENDING') RETURNING id`
	rows, err := tx.Queryx(messageQuery, conversationId, string(ConversationStatusTerminated), accountId, "Conversation terminated by user")
	if err != nil {
		return fmt.Errorf("history: failed to terminate message: %w", err)
	}
	var terminatedMessageIds []string
	for rows.Next() {
		var id string
		if scanErr := rows.Scan(&id); scanErr != nil {
			_ = rows.Close()
			return fmt.Errorf("history: failed to scan terminated message id: %w", scanErr)
		}
		terminatedMessageIds = append(terminatedMessageIds, id)
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		_ = rows.Close()
		return fmt.Errorf("history: failed to iterate terminated messages: %w", rowsErr)
	}
	_ = rows.Close()

	// Update the associated conversation status to TERMINATED
	conversationQuery := `UPDATE llm_conversations SET status = $2, updated_at = now() WHERE id = $1 AND account_id = $3`
	_, err = tx.Exec(conversationQuery, conversationId, string(ConversationStatusTerminated), accountId)
	if err != nil {
		return fmt.Errorf("history: failed to terminate conversation associated with message: %w", err)
	}

	// Terminate any sub-agent and tool-call rows still in-progress (or waiting)
	// for this conversation. Without this, the executor's next iteration sees the
	// message as terminated and unwinds, but the per-step rows remain stuck. We
	// write a response marker so callers can distinguish user-termination from
	// real failures. Both tables now permit 'terminated' as a status value, which
	// matches the late-write override in SaveConversationToolCall /
	// UpdateConversationAgentResponse — keeping the bulk and late-write paths in
	// sync.
	const terminatedResponse = "Terminated by user"
	agentQuery := `UPDATE llm_conversation_agent SET status = $2, response = $5, updated_at = now() WHERE conversation_id = $1 AND account_id = $6 AND status IN ($3, $4)`
	_, err = tx.Exec(agentQuery, conversationId,
		string(AgentExecutionStatusTerminated),
		string(AgentExecutionStatusInProgress),
		string(AgentExecutionStatusWaiting),
		terminatedResponse,
		accountId)
	if err != nil {
		return fmt.Errorf("history: failed to terminate in-progress agents: %w", err)
	}

	toolQuery := `UPDATE llm_conversation_tool_calls SET status = $2, response = $5, updated_at = now() WHERE conversation_id = $1 AND account_id = $6 AND status IN ($3, $4)`
	_, err = tx.Exec(toolQuery, conversationId,
		strings.ToLower(string(toolcore.NBToolResponseStatusTerminated)),
		strings.ToLower(string(toolcore.NBToolResponseStatusInProgress)),
		strings.ToLower(string(toolcore.NBToolResponseStatusWaiting)),
		terminatedResponse,
		accountId)
	if err != nil {
		return fmt.Errorf("history: failed to terminate in-progress tool calls: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return err
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

	return nil
}

func (chat *ConversationDao) UpdateConversationMessageConfig(id string, config toolcore.NBQueryConfig) error {
	configJson, _ := common.MarshalJson(config)

	query := `UPDATE llm_conversation_messages SET message_config = $2 WHERE id = $1`
	_, err := chat.dbManager.Db.Exec(query, id, string(configJson))
	if err != nil {
		return fmt.Errorf("history: failed to update message: %w", err)
	}
	return nil
}

// UpdateConversationMessageFollowupConfig updates only the followup-specific fields in message_config.
func (chat *ConversationDao) UpdateConversationMessageFollowupConfig(id string, followupConfig map[string]any) error {
	configJson, err := common.MarshalJson(followupConfig)
	if err != nil {
		return fmt.Errorf("history: failed to marshal followup config: %w", err)
	}

	// Also update the message column with the new question text so chat history shows the correct question.
	question, _ := followupConfig["question"].(string)
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

	messageQuery := `UPDATE llm_conversation_messages SET response = $2, updated_at = now(), status = $3, worker_name = $4 WHERE id = $1`
	_, err = chat.dbManager.Db.Exec(messageQuery, id, "", string(ConversationStatusInProgress), config.Config.ServerName)
	if err != nil {
		return fmt.Errorf("history: failed to update message: %w", err)
	}
	return nil
}

func (chat *ConversationDao) GetConversationMessage(id, accountId, conversationId string) (ConversationMessage, error) {
	query := `select id, conversation_id, message, created_at, response, role, account_id, user_id, message_type, updated_at, status, worker_name, agent_name, message_config, message_context, suggestions, llm_provider, llm_model from llm_conversation_messages WHERE id = $1 and conversation_id = $2 and account_id = $3`
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
				user_id, message_type, updated_at, status, worker_name, agent_name,
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
		// Only look at messages updated within the last 48 hours to avoid fetching
		// ancient stuck records (3,600+ rows from months ago causing 30-60s query times)
		query += " AND updated_at > now() - interval '48 hours'"
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

func (chat *ConversationDao) SaveConversationToolCall(conversationID, accountID, userID, messageId, agenId, toolId, toolName, toolArgs, thought, toolArgsSql, toolResult string, status toolcore.NBToolResponseStatus, toolType toolcore.NBToolType, childAgentId *string, references []toolcore.NBToolResponseReference) error {

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

	query := `INSERT INTO public.llm_conversation_tool_calls
	(conversation_id, message_id, tool_id, tool_name, parameters, response, user_id, account_id, agent_id, thought, params_sql, status, tool_type, child_agent_id, "references")
	VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
	ON CONFLICT (conversation_id, message_id, tool_id, tool_name, agent_id)
	DO UPDATE SET response = EXCLUDED.response, updated_at = now(), status = EXCLUDED.status, tool_type = EXCLUDED.tool_type, child_agent_id = EXCLUDED.child_agent_id, "references" = EXCLUDED.references
	WHERE llm_conversation_tool_calls.status NOT IN ('success', 'error', 'terminated');`

	if childAgentId == nil || *childAgentId == "" {
		query = `INSERT INTO public.llm_conversation_tool_calls
		(conversation_id, message_id, tool_id, tool_name, parameters, response, user_id, account_id, agent_id, thought, params_sql, status, tool_type, child_agent_id, "references")
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
		ON CONFLICT (conversation_id, message_id, tool_id, tool_name, agent_id)
		DO UPDATE SET response = EXCLUDED.response, updated_at = now(), status = EXCLUDED.status, tool_type = EXCLUDED.tool_type, "references" = EXCLUDED.references
		WHERE llm_conversation_tool_calls.status NOT IN ('success', 'error', 'terminated');`
	}

	_, err := chat.dbManager.Db.Exec(query, conversationID, messageId, toolId, toolName, toolArgs, toolResult, userIDSql, accountID, agenId, thought, toolArgsSql, strings.ToLower(string(status)), toolType, childAgentId, referenceText)
	if err != nil {
		return fmt.Errorf("history: error inserting tool call: %w", err)
	}
	return nil
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
	var rows *sqlx.Rows
	var err error
	var args []interface{}
	args = append(args, conversationID, accountID)

	query := `SELECT role, message, response, id::text, message_type FROM llm_conversation_messages 
		WHERE conversation_id = $1 AND account_id = $2`

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
		var role, message, response, id, mType string
		if err := rows.Scan(&role, &message, &response, &id, &mType); err != nil {
			slog.Info("history: failed to scan row:", "error:", err)
			continue
		}
		msg := make(map[string]string)
		msg["role"] = role
		msg["content"] = message
		msg["response"] = response
		msg["id"] = id
		msg["message_type"] = mType
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

	type MemInfo struct {
		ID        string `db:"id"`
		AccountID string `db:"account_id"`
	}
	var mems []MemInfo
	for rows.Next() {
		var m MemInfo
		if err := rows.StructScan(&m); err != nil {
			_ = rows.Close()
			return fmt.Errorf("history: failed to scan memory row: %w", err)
		}
		mems = append(mems, m)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("history: cursor error reading memories: %w", err)
	}
	if err := rows.Close(); err != nil {
		slog.Warn("history: failed to close rows during conversation memory cleanup", "error", err)
	}

	// 2. Delete from RAG
	for _, m := range mems {
		if err := toolcore.DeleteMemoryFromRAG(m.AccountID, m.ID); err != nil {
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
	if err := history.DeleteConversation(conversationId.String()); err != nil {
		slog.Error("history: failed to delete conversation", "error", err, "conversation_id", conversationId)
		return err
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

type TokenMetrics struct {
	AgentId              string          `json:"id"`
	AgentName            string          `json:"agent_name"`              // Human-readable agent name
	InputTokens          int             `json:"input_tokens"`            // Total input (cached + non-cached, includes cache_creation)
	OutputTokens         int             `json:"output_tokens"`           // Total output
	CachedInputTokens    int             `json:"cached_input_tokens"`     // Tokens served from cache
	NonCachedInputTokens int             `json:"non_cached_input_tokens"` // New input tokens (full input rate)
	CacheCreationTokens  int             `json:"cache_creation_tokens"`   // Tokens written to cache (Anthropic/Vertex)
	ThinkingTokens       int             `json:"thinking_tokens"`         // Hidden chain-of-thought (Gemini 2.5+)
	CacheHitRate         sql.NullFloat64 `json:"cache_hit_rate"`          // Cache hit rate percentage (0-100)
	Cost                 float64         `json:"cost"`
	ModelProviderName    sql.NullString  `json:"model_provider_name"`
	ModelName            sql.NullString  `json:"model_name"`
	MessageId            string          `json:"message_id"`
	ConversationId       string          `json:"conversation_id"`
}

// modelPricing captures all rate dimensions billed by an LLM provider.
// Standard rates apply when total prompt ≤ ContextThresholdTokens (or when
// the threshold is NULL → no tiering). LongCtx rates apply above that.
//
// Storage rate is per-token-hour and only Vertex/Gemini bills storage; for
// other providers the column is NULL → storage cost = 0 in the formula
// (which is correct, since they don't bill it).
//
// CacheCostPerMillionTokensPerHour is the legacy column kept for backward
// compatibility with old data. It will be dropped after backfill into
// CostPerMillionCachedStoragePerHour.
type modelPricing struct {
	CostPerMillionInput  float64
	CostPerMillionOutput float64

	CostPerMillionCachedInput          sql.NullFloat64 // discounted read rate
	CostPerMillionCacheCreation        sql.NullFloat64 // cache write rate; NULL → use input rate
	CostPerMillionCachedStoragePerHour sql.NullFloat64 // storage rate; NULL → no storage cost
	ContextThresholdTokens             sql.NullInt64   // long-ctx threshold (e.g. 200K for Gemini)

	CostPerMillionInputLongCtx         sql.NullFloat64
	CostPerMillionOutputLongCtx        sql.NullFloat64
	CostPerMillionCachedInputLongCtx   sql.NullFloat64
	CostPerMillionCacheCreationLongCtx sql.NullFloat64

	// Legacy — to be removed after backfill into CostPerMillionCachedStoragePerHour.
	CacheCostPerMillionTokensPerHour sql.NullFloat64
}

// effectiveInputRate returns the input rate for the given total prompt size,
// applying the long-ctx tier when the threshold is configured AND exceeded
// AND a long-ctx rate is populated.
func (p *modelPricing) effectiveInputRate(totalPrompt int) float64 {
	if p.useLongCtx(totalPrompt) {
		return p.CostPerMillionInputLongCtx.Float64
	}
	return p.CostPerMillionInput
}

// effectiveOutputRate is the rate billed for output tokens AND thinking tokens.
func (p *modelPricing) effectiveOutputRate(totalPrompt int) float64 {
	if p.useLongCtx(totalPrompt) {
		return p.CostPerMillionOutputLongCtx.Float64
	}
	return p.CostPerMillionOutput
}

// effectiveCachedInputRate is the rate billed for tokens served from cache.
// NULL means we don't know — fall back to input rate (overestimates slightly,
// conservative for cost reporting).
func (p *modelPricing) effectiveCachedInputRate(totalPrompt int) float64 {
	if p.useLongCtx(totalPrompt) {
		return nullableFloatOr(p.CostPerMillionCachedInputLongCtx, p.CostPerMillionInputLongCtx.Float64)
	}
	return nullableFloatOr(p.CostPerMillionCachedInput, p.CostPerMillionInput)
}

// effectiveCacheCreationRate is the rate billed when tokens are written into
// the cache for the first time. NULL → fall back to the input rate (matches
// Vertex/Gemini); Anthropic populates this column with 1.25x input.
func (p *modelPricing) effectiveCacheCreationRate(totalPrompt int) float64 {
	if p.useLongCtx(totalPrompt) {
		return nullableFloatOr(p.CostPerMillionCacheCreationLongCtx, p.CostPerMillionInputLongCtx.Float64)
	}
	return nullableFloatOr(p.CostPerMillionCacheCreation, p.CostPerMillionInput)
}

func (p *modelPricing) useLongCtx(totalPrompt int) bool {
	return p.ContextThresholdTokens.Valid &&
		int64(totalPrompt) > p.ContextThresholdTokens.Int64 &&
		p.CostPerMillionInputLongCtx.Valid
}

func nullableFloatOr(v sql.NullFloat64, fallback float64) float64 {
	if v.Valid {
		return v.Float64
	}
	return fallback
}

// CalculateTotalCost computes the per-call cost in USD for a single LLM API
// call using the redesigned formula. Storage cost is NOT included here —
// it lives in llm_cache_lifecycle and is computed separately on read.
//
// Formula:
//
//	cost =   non_cached_input  × input_rate
//	       + cached_input      × cached_input_rate
//	       + cache_creation    × cache_creation_rate
//	       + (output + thinking) × output_rate
//
// All four rates are tier-aware: when total prompt size exceeds
// pricing.ContextThresholdTokens AND a long-ctx variant is populated, the
// long-ctx rate is used for every component of this call.
//
// Returns 0 if pricing is nil so callers don't have to nil-check.
func CalculateTotalCost(p *modelPricing, nonCachedInputTokens, cachedInputTokens, cacheCreationTokens, outputTokens, thinkingTokens int) float64 {
	if p == nil {
		return 0
	}

	totalPrompt := nonCachedInputTokens + cachedInputTokens + cacheCreationTokens

	inputRate := p.effectiveInputRate(totalPrompt)
	outputRate := p.effectiveOutputRate(totalPrompt)
	cachedRate := p.effectiveCachedInputRate(totalPrompt)
	creationRate := p.effectiveCacheCreationRate(totalPrompt)

	const million = 1_000_000.0

	return (float64(nonCachedInputTokens)/million)*inputRate +
		(float64(cachedInputTokens)/million)*cachedRate +
		(float64(cacheCreationTokens)/million)*creationRate +
		(float64(outputTokens+thinkingTokens)/million)*outputRate
}

func (chat *ConversationDao) fetchModelPricing() (map[string]modelPricing, error) {
	return chat.GetConversationCosts(nil)
}

func (chat *ConversationDao) GetConversationCosts(models []string) (map[string]modelPricing, error) {
	pricingMap := make(map[string]modelPricing)

	// V726 onwards: pricing has explicit cached_input / creation / storage rates
	// plus tiered (long_ctx) variants for Gemini-style threshold pricing.
	// Old cache_cost_per_million_tokens_per_hour is kept until backfill so
	// reading it here is a temporary belt-and-braces measure.
	query := `SELECT provider_name, model_name,
	    cost_per_million_input_tokens,
	    cost_per_million_output_tokens,
	    cache_cost_per_million_tokens_per_hour,
	    cost_per_million_cached_input_tokens,
	    cost_per_million_cache_creation_tokens,
	    cost_per_million_cached_storage_per_hour,
	    context_threshold_tokens,
	    cost_per_million_input_tokens_long_ctx,
	    cost_per_million_output_tokens_long_ctx,
	    cost_per_million_cached_input_tokens_long_ctx,
	    cost_per_million_cache_creation_tokens_long_ctx
	FROM llm_model_pricing`

	var args []any
	if len(models) > 0 {
		query += ` WHERE model_name IN (?)`
		var err error
		query, args, err = sqlx.In(query, models)
		if err != nil {
			return nil, err
		}
		query = chat.dbManager.Db.Rebind(query)
	}

	rows, err := chat.dbManager.Db.Queryx(query, args...)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := rows.Close(); err != nil {
			slog.Error("Failed to close rows", "error", err)
		}
	}()

	for rows.Next() {
		var (
			provider, model      string
			input, output        float64
			legacyCacheStorage   sql.NullFloat64
			cachedInput          sql.NullFloat64
			cacheCreation        sql.NullFloat64
			cachedStoragePerHour sql.NullFloat64
			ctxThreshold         sql.NullInt64
			inputLong            sql.NullFloat64
			outputLong           sql.NullFloat64
			cachedInputLong      sql.NullFloat64
			cacheCreationLong    sql.NullFloat64
		)
		if err := rows.Scan(
			&provider, &model,
			&input, &output,
			&legacyCacheStorage,
			&cachedInput,
			&cacheCreation,
			&cachedStoragePerHour,
			&ctxThreshold,
			&inputLong,
			&outputLong,
			&cachedInputLong,
			&cacheCreationLong,
		); err != nil {
			continue
		}

		// Until backfill drops the legacy column, prefer the new explicit
		// storage column; if NULL there but populated in the legacy column,
		// fall through to that. Same dollar value for now.
		storage := cachedStoragePerHour
		if !storage.Valid && legacyCacheStorage.Valid {
			storage = legacyCacheStorage
		}

		pricingMap[provider+":"+model] = modelPricing{
			CostPerMillionInput:                input,
			CostPerMillionOutput:               output,
			CostPerMillionCachedInput:          cachedInput,
			CostPerMillionCacheCreation:        cacheCreation,
			CostPerMillionCachedStoragePerHour: storage,
			ContextThresholdTokens:             ctxThreshold,
			CostPerMillionInputLongCtx:         inputLong,
			CostPerMillionOutputLongCtx:        outputLong,
			CostPerMillionCachedInputLongCtx:   cachedInputLong,
			CostPerMillionCacheCreationLongCtx: cacheCreationLong,
			CacheCostPerMillionTokensPerHour:   legacyCacheStorage,
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return pricingMap, nil
}

func (chat *ConversationDao) GetConversationTokenUsage(conversationId string) ([]TokenMetrics, error) {

	// Fetch Pricing Map upfront to avoid N+1 queries
	pricingMap, err := chat.fetchModelPricing()
	if err != nil {
		slog.Error("Failed to fetch model pricing", "error", err)
		// Proceed with empty map, costs will be 0
		pricingMap = make(map[string]modelPricing)
	}

	// Query the new llm_conversation_token_usage table
	// Since the new table has per-API-call granularity (multiple rows per agent),
	// we need to SUM tokens for each agent across all API calls
	// Group by agent_name instead of agent_id to avoid duplicate agent names in UI
	// when same agent executes multiple times (retries, fallbacks, multiple LLM calls)
	// NOTE: AgentId field removed from query - UI should use AgentName as identifier
	query := `
		SELECT
			COALESCE(t.agent_name, '') as AgentName,
			SUM(t.input_tokens) as InputTokens,
			SUM(t.output_tokens) as OutputTokens,
			SUM(COALESCE(t.cached_input_tokens, 0)) as CachedInputTokens,
			SUM(t.input_tokens - COALESCE(t.cached_input_tokens, 0)) as NonCachedInputTokens,
			SUM(COALESCE(t.cache_creation_tokens, 0)) as CacheCreationTokens,
			SUM(COALESCE(t.thinking_tokens, 0)) as ThinkingTokens,
			CASE
				WHEN SUM(t.input_tokens) > 0
				THEN (SUM(COALESCE(t.cached_input_tokens, 0))::float8 / SUM(t.input_tokens)::float8) * 100
				ELSE NULL
			END as CacheHitRate,
			t.llm_model as ModelName,
			t.llm_provider as ModelProviderName,
			t.message_id::text as MessageId,
			t.conversation_id::text as ConversationId
		FROM llm_conversation_token_usage t
		INNER JOIN llm_conversations c ON c.id = t.conversation_id
		WHERE c.session_id = $1
		GROUP BY t.agent_name, t.llm_model, t.llm_provider, t.message_id, t.conversation_id;`

	rows, err := chat.dbManager.Db.Queryx(query, conversationId)

	if err != nil {
		slog.Error("Error executing query", "error", err)
		return nil, err
	}
	defer func() {
		if err := rows.Close(); err != nil {
			slog.Error("Failed to close rows", "error", err)
		}
	}()
	agents := []TokenMetrics{}
	for rows.Next() {
		var individualAgentStat TokenMetrics
		err := rows.StructScan(&individualAgentStat)
		if err != nil {
			slog.Error("Scan error", "error", err)
			return nil, err
		}

		// Initialize cost to 0, will calculate dynamically
		individualAgentStat.Cost = 0

		if individualAgentStat.ModelProviderName.Valid && individualAgentStat.ModelName.Valid {
			key := individualAgentStat.ModelProviderName.String + ":" + individualAgentStat.ModelName.String
			if pricing, ok := pricingMap[key]; ok {
				individualAgentStat.Cost = CalculateTotalCost(
					&pricing,
					individualAgentStat.NonCachedInputTokens,
					individualAgentStat.CachedInputTokens,
					individualAgentStat.CacheCreationTokens,
					individualAgentStat.OutputTokens,
					individualAgentStat.ThinkingTokens,
				)
			}
		}
		agents = append(agents, individualAgentStat)
	}
	return agents, nil
}

// GetConversationCost computes the per-call cost in USD for a single LLM
// invocation by looking up the (provider, model) row in llm_model_pricing
// and applying CalculateTotalCost. Returns -1 with an error if pricing
// cannot be found for the given (provider, model).
func (chat *ConversationDao) GetConversationCost(
	provider string,
	model string,
	nonCachedInputTokens int,
	cachedInputTokens int,
	cacheCreationTokens int,
	outputTokens int,
	thinkingTokens int,
) (float64, error) {
	pricingMap, err := chat.GetConversationCosts([]string{model})
	if err != nil {
		return -1, err
	}
	pricing, ok := pricingMap[provider+":"+model]
	if !ok {
		return -1, fmt.Errorf("found no pricing data for model %s and provider %s", model, provider)
	}
	return CalculateTotalCost(
		&pricing,
		nonCachedInputTokens,
		cachedInputTokens,
		cacheCreationTokens,
		outputTokens,
		thinkingTokens,
	), nil
}

// TokenUsageDetailedRecord represents a single API call record with all details
type TokenUsageDetailedRecord struct {
	ConversationID      string
	MessageID           string
	AgentID             sql.NullString
	AgentName           string
	LLMProvider         string
	LLMModel            string
	InputTokens         int
	OutputTokens        int
	CachedInputTokens   int
	CacheCreationTokens int
	ThinkingTokens      int
	RequestStatus       string
	LatencySeconds      sql.NullFloat64
}

// GetConversationTokenUsageDetailed returns all individual token usage records for detailed analysis
func (chat *ConversationDao) GetConversationTokenUsageDetailed(conversationId string) ([]TokenUsageDetailedRecord, error) {
	query := `
		SELECT
			t.conversation_id::text as ConversationID,
			t.message_id::text as MessageID,
			t.agent_id::text as AgentID,
			t.agent_name as AgentName,
			t.llm_provider as LLMProvider,
			t.llm_model as LLMModel,
			t.input_tokens as InputTokens,
			t.output_tokens as OutputTokens,
			COALESCE(t.cached_input_tokens, 0) as CachedInputTokens,
			COALESCE(t.cache_creation_tokens, 0) as CacheCreationTokens,
			COALESCE(t.thinking_tokens, 0) as ThinkingTokens,
			t.request_status as RequestStatus,
			t.latency_seconds as LatencySeconds
		FROM llm_conversation_token_usage t
		INNER JOIN llm_conversations c ON c.id = t.conversation_id
		WHERE c.session_id = $1
		ORDER BY t.created_at ASC;`

	rows, err := chat.dbManager.Db.Queryx(query, conversationId)
	if err != nil {
		slog.Error("Error executing detailed token usage query", "error", err)
		return nil, err
	}
	defer func() {
		if err := rows.Close(); err != nil {
			slog.Error("Failed to close rows", "error", err)
		}
	}()

	records := []TokenUsageDetailedRecord{}
	for rows.Next() {
		var record TokenUsageDetailedRecord
		err := rows.StructScan(&record)
		if err != nil {
			slog.Error("Scan error", "error", err)
			return nil, err
		}
		records = append(records, record)
	}
	return records, nil
}

// ToolCallsStats represents tool call statistics for a conversation
type ToolCallsStats struct {
	TotalToolCalls      int
	SuccessfulToolCalls int
	FailedToolCalls     int
	InProgressToolCalls int
}

type TimeBreakdown struct {
	WallTimeSeconds        float64
	AgentActiveTimeSeconds float64
	ToolTimeSeconds        float64
}

// ConversationTimeAggregatesFilter selects which conversations to roll up.
// AccountIDs scopes the rollup to the caller's accessible accounts (matches
// RPC RLS behavior for the existing GraphQL widgets); empty means "no
// accessible accounts" and short-circuits to a zero result. Empty Sources
// means "any source"; EventScoped narrows to conversations whose title
// contains a UUID, matching the auto-investigation tab in the UI.
// ExcludedTitles filters out infrastructure conversations like the
// "Event details retrieval by ID" lookup that never represents real work.
type ConversationTimeAggregatesFilter struct {
	AccountIDs     []string
	StartDate      time.Time
	EndDate        time.Time
	Sources        []string
	ExcludedTitles []string
	EventScoped    bool
}

// ConversationTimeAggregates rolls up TimeBreakdown numbers across many
// conversations for dashboard widgets. CompletedCount drives per-investigation
// averaging; TotalCount is the unfiltered total in the same window so the UI
// can show progress vs. completion separately.
type ConversationTimeAggregates struct {
	CompletedCount              int     `json:"completed_count"`
	TotalCount                  int     `json:"total_count"`
	TotalWallTimeSeconds        float64 `json:"total_wall_time_seconds"`
	TotalAgentActiveTimeSeconds float64 `json:"total_agent_active_time_seconds"`
	TotalToolTimeSeconds        float64 `json:"total_tool_time_seconds"`
}

// GetConversationLifecycleStorageCost returns the total per-hour storage cost
// in USD accrued by conversation-scoped caches for the given session.
// Account/tenant/global-scoped caches are intentionally excluded — their
// storage cost belongs to the account/tenant budget rollup, not to any
// individual conversation. Storage time is bounded by invalidated_at if set,
// else min(now, expires_at) — matches the formula used by budget/usage.go.
//
// tenantId is required for defense-in-depth: session_id is unique per
// conversation but a tenant filter ensures cross-tenant isolation even if
// a session_id collision were ever to occur.
func (chat *ConversationDao) GetConversationLifecycleStorageCost(conversationId string, tenantId string) (float64, error) {
	if strings.TrimSpace(tenantId) == "" {
		return 0, fmt.Errorf("GetConversationLifecycleStorageCost: tenantId is required")
	}
	query := `
		SELECT COALESCE(SUM(
			(cl.cached_tokens / 1000000.0)
			* COALESCE(p.cost_per_million_cached_storage_per_hour, 0)
			* GREATEST(0, EXTRACT(EPOCH FROM (
				COALESCE(cl.invalidated_at, LEAST(now(), cl.expires_at)) - cl.created_at
			))) / 3600
		), 0) AS storage_cost
		FROM llm_cache_lifecycle cl
		INNER JOIN llm_conversations c ON c.id = cl.conversation_id
		LEFT JOIN llm_model_pricing p
			ON p.model_name = cl.llm_model AND p.provider_name = cl.llm_provider
		WHERE c.session_id = $1
		  AND c.tenant_id = $2
		  AND cl.scope = 'conversation';`

	var cost sql.NullFloat64
	if err := chat.dbManager.Db.Get(&cost, query, conversationId, tenantId); err != nil {
		return 0, fmt.Errorf("GetConversationLifecycleStorageCost: %w", err)
	}
	if !cost.Valid {
		return 0, nil
	}
	return cost.Float64, nil
}

// GetConversationToolCallsStats returns tool call statistics for a conversation
func (chat *ConversationDao) GetConversationToolCallsStats(conversationId string) (ToolCallsStats, error) {
	query := `
		SELECT
			COUNT(*) as total,
			COUNT(*) FILTER (WHERE tc.status = 'success') as successful,
			COUNT(*) FILTER (WHERE tc.status = 'fail') as failed,
			COUNT(*) FILTER (WHERE tc.status = 'in_progress') as in_progress
		FROM llm_conversation_tool_calls tc
		INNER JOIN llm_conversations c ON c.id = tc.conversation_id
		WHERE c.session_id = $1;`

	var stats struct {
		Total      int `db:"total"`
		Successful int `db:"successful"`
		Failed     int `db:"failed"`
		InProgress int `db:"in_progress"`
	}

	err := chat.dbManager.Db.Get(&stats, query, conversationId)
	if err != nil {
		slog.Error("Error getting tool calls stats", "error", err)
		return ToolCallsStats{}, err
	}

	return ToolCallsStats{
		TotalToolCalls:      stats.Total,
		SuccessfulToolCalls: stats.Successful,
		FailedToolCalls:     stats.Failed,
		InProgressToolCalls: stats.InProgress,
	}, nil
}

// GetConversationTimeBreakdown calculates time breakdown for a conversation
func (chat *ConversationDao) GetConversationTimeBreakdown(conversationId string) (TimeBreakdown, error) {
	query := `
		WITH conversation_ids AS (
			SELECT id FROM llm_conversations WHERE session_id = $1
		),
		wall_time AS (
			SELECT
				COALESCE(SUM(EXTRACT(EPOCH FROM (updated_at - created_at))), 0) as seconds
			FROM llm_conversations
			WHERE session_id = $1
		),
		agent_time AS (
			SELECT
				COALESCE(SUM(EXTRACT(EPOCH FROM (updated_at - created_at))), 0) as seconds
			FROM llm_conversation_agent
			WHERE conversation_id IN (SELECT id FROM conversation_ids)
				AND updated_at IS NOT NULL
				AND created_at IS NOT NULL
				AND (parent_agent_id IS NULL OR parent_agent_id = '00000000-0000-0000-0000-000000000000')
		),
		tool_time AS (
			SELECT
				COALESCE(SUM(EXTRACT(EPOCH FROM (updated_at - created_at))), 0) as seconds
			FROM llm_conversation_tool_calls
			WHERE conversation_id IN (SELECT id FROM conversation_ids)
				AND tool_type = 'tool'
				AND updated_at IS NOT NULL
				AND created_at IS NOT NULL
		)
		SELECT
			COALESCE((SELECT seconds FROM wall_time), 0) as wall_time,
			COALESCE((SELECT seconds FROM agent_time), 0) as agent_time,
			COALESCE((SELECT seconds FROM tool_time), 0) as tool_time;`

	var result struct {
		WallTime  float64 `db:"wall_time"`
		AgentTime float64 `db:"agent_time"`
		ToolTime  float64 `db:"tool_time"`
	}

	err := chat.dbManager.Db.Get(&result, query, conversationId)
	if err != nil {
		slog.Error("Error getting conversation time breakdown", "error", err)
		return TimeBreakdown{}, err
	}

	return TimeBreakdown{
		WallTimeSeconds:        result.WallTime,
		AgentActiveTimeSeconds: result.AgentTime,
		ToolTimeSeconds:        result.ToolTime,
	}, nil
}

// GetConversationTimeAggregates rolls up wall / agent-active / tool time over a
// date range across one or more accounts. Designed for dashboard widgets that
// previously computed (updated_at - created_at) averages on the frontend —
// moving the math here gives a single source of truth so future refinements
// (excluding user-input wait, complexity-aware baselines) only land in one
// place.
//
// Window semantics match the existing groupings_v2 / list_v2 frontend queries:
// filter by updated_at, exclude infrastructure titles, and optionally narrow to
// auto-investigations (title contains a UUID).
//
// All three time totals (wall / agent / tool) are scoped to COMPLETED
// conversations so that any future caller doing `time / completed_count`
// gets a true average over finished work — never inflated by in-progress
// rows. Use TotalCount when you need the raw window size including
// in-progress conversations.
func (chat *ConversationDao) GetConversationTimeAggregates(filter ConversationTimeAggregatesFilter) (ConversationTimeAggregates, error) {
	if len(filter.AccountIDs) == 0 {
		// No accessible accounts means no rows; return zero rather than running
		// an unbounded query.
		return ConversationTimeAggregates{}, nil
	}
	if filter.EndDate.Before(filter.StartDate) {
		return ConversationTimeAggregates{}, fmt.Errorf("GetConversationTimeAggregates: end_date must be >= start_date")
	}

	args := []any{pq.Array(filter.AccountIDs), filter.StartDate, filter.EndDate}
	argCounter := 4

	sourceClause := ""
	if len(filter.Sources) > 0 {
		sourceClause = fmt.Sprintf(" AND c.source = ANY($%d)", argCounter)
		args = append(args, pq.Array(filter.Sources))
		argCounter++
	}

	excludedTitleClause := ""
	if len(filter.ExcludedTitles) > 0 {
		excludedTitleClause = fmt.Sprintf(" AND (c.title IS NULL OR c.title <> ALL($%d))", argCounter)
		args = append(args, pq.Array(filter.ExcludedTitles))
	}

	eventScopedClause := ""
	if filter.EventScoped {
		eventScopedClause = ` AND c.title ~ '[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}'`
	}

	// All three time CTEs scope to COMPLETED conversations to keep the
	// numerator/denominator semantics consistent: any caller doing
	// `wall|agent|tool / completed_count` gets a true average over finished
	// work rather than one inflated by in-progress rows.
	//
	// account_id is cast to `uuid[]` on the parameter side (not the column
	// side) so the existing index on llm_conversations.account_id can serve
	// the lookup.
	query := fmt.Sprintf(`
		WITH filtered_conversations AS (
			SELECT c.id, c.status, c.created_at, c.updated_at
			FROM llm_conversations c
			WHERE c.account_id = ANY($1::uuid[])
				AND c.updated_at >= $2
				AND c.updated_at <= $3
				AND EXISTS (
					SELECT 1 FROM llm_conversation_messages m
					WHERE m.conversation_id = c.id
						AND m.message_type = 'generation'
						AND m.role = 'human'
				)%s%s%s
		),
		completed_conversations AS (
			SELECT id, created_at, updated_at
			FROM filtered_conversations
			WHERE status = 'COMPLETED'
		),
		wall_time AS (
			SELECT COALESCE(SUM(EXTRACT(EPOCH FROM (updated_at - created_at))), 0) AS seconds
			FROM completed_conversations
		),
		agent_time AS (
			SELECT COALESCE(SUM(EXTRACT(EPOCH FROM (a.updated_at - a.created_at))), 0) AS seconds
			FROM llm_conversation_agent a
			WHERE a.conversation_id IN (SELECT id FROM completed_conversations)
				AND a.updated_at IS NOT NULL
				AND a.created_at IS NOT NULL
				AND (a.parent_agent_id IS NULL OR a.parent_agent_id = '00000000-0000-0000-0000-000000000000')
		),
		tool_time AS (
			SELECT COALESCE(SUM(EXTRACT(EPOCH FROM (tc.updated_at - tc.created_at))), 0) AS seconds
			FROM llm_conversation_tool_calls tc
			WHERE tc.conversation_id IN (SELECT id FROM completed_conversations)
				AND tc.tool_type = 'tool'
				AND tc.updated_at IS NOT NULL
				AND tc.created_at IS NOT NULL
		),
		counts AS (
			SELECT
				(SELECT COUNT(*) FROM completed_conversations) AS completed_count,
				(SELECT COUNT(*) FROM filtered_conversations) AS total_count
		)
		SELECT
			(SELECT completed_count FROM counts) AS completed_count,
			(SELECT total_count FROM counts) AS total_count,
			(SELECT seconds FROM wall_time) AS wall_time,
			(SELECT seconds FROM agent_time) AS agent_time,
			(SELECT seconds FROM tool_time) AS tool_time;`,
		sourceClause, excludedTitleClause, eventScopedClause)

	var result struct {
		CompletedCount int     `db:"completed_count"`
		TotalCount     int     `db:"total_count"`
		WallTime       float64 `db:"wall_time"`
		AgentTime      float64 `db:"agent_time"`
		ToolTime       float64 `db:"tool_time"`
	}

	if err := chat.dbManager.Db.Get(&result, query, args...); err != nil {
		slog.Error("GetConversationTimeAggregates: query failed", "error", err, "account_ids", filter.AccountIDs)
		return ConversationTimeAggregates{}, fmt.Errorf("GetConversationTimeAggregates: %w", err)
	}

	return ConversationTimeAggregates{
		CompletedCount:              result.CompletedCount,
		TotalCount:                  result.TotalCount,
		TotalWallTimeSeconds:        result.WallTime,
		TotalAgentActiveTimeSeconds: result.AgentTime,
		TotalToolTimeSeconds:        result.ToolTime,
	}, nil
}

func (chat *ConversationDao) InsertTokenUsage(record *TokenUsageRecord) error {
	if record == nil {
		return fmt.Errorf("token usage record is nil")
	}

	query := `
		INSERT INTO llm_conversation_token_usage (
			conversation_id, message_id, agent_id, agent_name, account_id, user_id,
			llm_provider, llm_model,
			input_tokens, output_tokens, cached_input_tokens, cache_creation_tokens,
			is_cache_hit, cache_hit_rate,
			retry_attempt, fallback_from_model, fallback_chain,
			latency_seconds, request_status, error_message,
			content_length, stop_reason, cache_ttl_minutes,
			prompt_messages, response_content,
			thinking_tokens,
			ttft_ms, itl_ms_avg, tokens_per_second, was_streaming
		) VALUES (
			$1, $2, $3, $4, $5, $6,
			$7, $8,
			$9, $10, $11, $12,
			$13, $14,
			$15, $16, $17,
			$18, $19, $20,
			$21, $22, $23,
			$24, $25,
			$26,
			$27, $28, $29, $30
		)
	`

	_, err := chat.dbManager.Db.Exec(query,
		record.ConversationID, record.MessageID, record.AgentID, record.AgentName,
		record.AccountID, record.UserID,
		record.LLMProvider, record.LLMModel,
		record.InputTokens, record.OutputTokens, record.CachedInputTokens, record.CacheCreationTokens,
		record.IsCacheHit, record.CacheHitRate,
		record.RetryAttempt, record.FallbackFromModel, record.FallbackChain,
		record.LatencySeconds, record.RequestStatus, record.ErrorMessage,
		record.ContentLength, record.StopReason, record.CacheTTLMinutes,
		record.PromptMessages, record.ResponseContent,
		record.ThinkingTokens,
		record.TTFTMs, record.ITLMsAvg, record.TokensPerSecond, record.WasStreaming,
	)

	if err != nil {
		// If the insert failed due to a foreign key constraint on agent_id (agent record doesn't exist),
		// retry with agent_id = NULL to avoid losing token usage data
		if record.AgentID != nil && strings.Contains(err.Error(), "llm_conversation_token_usage_agent_fk") {
			slog.Warn("InsertTokenUsage: agent_id FK violation, retrying with NULL agent_id", "agent_id", *record.AgentID, "agent_name", record.AgentName)
			record.AgentID = nil
			_, retryErr := chat.dbManager.Db.Exec(query,
				record.ConversationID, record.MessageID, record.AgentID, record.AgentName,
				record.AccountID, record.UserID,
				record.LLMProvider, record.LLMModel,
				record.InputTokens, record.OutputTokens, record.CachedInputTokens, record.CacheCreationTokens,
				record.IsCacheHit, record.CacheHitRate,
				record.RetryAttempt, record.FallbackFromModel, record.FallbackChain,
				record.LatencySeconds, record.RequestStatus, record.ErrorMessage,
				record.ContentLength, record.StopReason, record.CacheTTLMinutes,
				record.PromptMessages, record.ResponseContent,
				record.ThinkingTokens,
				record.TTFTMs, record.ITLMsAvg, record.TokensPerSecond, record.WasStreaming,
			)
			if retryErr != nil {
				return fmt.Errorf("failed to insert token usage (retry without agent_id): %w", retryErr)
			}
			return nil
		}
		return fmt.Errorf("failed to insert token usage: %w", err)
	}

	return nil
}

func (chat *ConversationDao) GetLatestConversationBySessionID(sessionID string, accountId string) (Conversation, error) {
	query := `SELECT id::text, user_id, session_id, account_id, context::text, status, tenant_id::text, title, source FROM llm_conversations WHERE session_id = $1 AND account_id = $2 ORDER BY updated_at DESC LIMIT 1`
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
	for rows.Next() {
		if err := rows.Scan(&id, &userId, &sessionId, &accoundId, &cntxt, &status, &tenantId, &title, &source); err != nil {
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
			ID:        uuid.MustParse(id),
			SessionID: sessionId,
			AccountID: uuid.MustParse(accoundId),
			UserID:    userIdUUID,
			Context:   context,
			Status:    status,
			TenantID:  uuid.MustParse(tenantId),
			Title:     title,
			Source:    source,
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

func (chat *ConversationDao) SaveLongTermMemory(accountId, conversationId, messageId, content string, memoryType MemoryType) (string, error) {
	convIdSql := sql.NullString{
		String: conversationId,
		Valid:  conversationId != "" && conversationId != uuid.Nil.String(),
	}
	msgIdSql := sql.NullString{
		String: messageId,
		Valid:  messageId != "" && messageId != uuid.Nil.String(),
	}

	query := `
	INSERT INTO llm_conversation_memory (account_id, conversation_id, message_id, content, memory_type, created_at)
	VALUES ($1, $2, $3, $4, $5, now()) RETURNING id;`
	var id string
	err := chat.dbManager.Db.QueryRow(query, accountId, convIdSql, msgIdSql, content, string(memoryType)).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("history: failed to save long-term memory: %w", err)
	}

	// Also save to RAG for semantic search with memory type for better retrieval
	err = toolcore.AddMemoryToRAG(accountId, content, id, string(memoryType))
	if err != nil {
		// Log error but don't fail the operation as DB save was successful
		slog.Error("history: failed to save memory to RAG", "error", err, "memoryType", memoryType)
	}

	return id, nil
}

func (chat *ConversationDao) ListLongTermMemories(accountId string, conversationId string, messageId string, memoryType string, searchQuery string, limit int, offset int) ([]LongTermMemory, error) {
	query := `
	SELECT id, account_id, conversation_id, message_id, content, memory_type, use_count, last_used_at, created_at
	FROM llm_conversation_memory
	WHERE account_id = $1 `

	args := []interface{}{accountId}
	argCounter := 2

	if conversationId != "" {
		query += fmt.Sprintf(" AND conversation_id = $%d", argCounter)
		args = append(args, conversationId)
		argCounter++
	}

	if messageId != "" {
		query += fmt.Sprintf(" AND message_id = $%d", argCounter)
		args = append(args, messageId)
		argCounter++
	}

	if memoryType != "" {
		query += fmt.Sprintf(" AND memory_type = $%d", argCounter)
		args = append(args, memoryType)
		argCounter++
	}

	if searchQuery != "" {
		query += fmt.Sprintf(" AND content ILIKE $%d", argCounter)
		args = append(args, "%"+searchQuery+"%")
		argCounter++
	}

	query += fmt.Sprintf(" ORDER BY created_at DESC LIMIT $%d OFFSET $%d;", argCounter, argCounter+1)
	args = append(args, limit, offset)

	rows, err := chat.dbManager.Db.Queryx(query, args...)
	if err != nil {
		return nil, fmt.Errorf("history: failed to list long-term memories: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			slog.Error("conversation_dao: failed to close rows", "error", err)
		}
	}()

	var memories []LongTermMemory
	for rows.Next() {
		var memory LongTermMemory
		if err := rows.StructScan(&memory); err != nil {
			return nil, fmt.Errorf("history: failed to scan long-term memory struct: %w", err)
		}
		memories = append(memories, memory)
	}
	return memories, nil
}

func (chat *ConversationDao) DeleteLongTermMemory(id, accountId string) error {
	// 1. Delete from RAG using ID
	err := toolcore.DeleteMemoryFromRAG(accountId, id)
	if err != nil {
		// Log error but proceed to delete from DB to maintain consistency (or user choice)
		// Usually better to ensure it's gone from at least one place, or retry.
		// For now, we log and proceed.
		slog.Error("history: failed to delete memory from RAG", "error", err)
	}

	// 2. Delete from DB
	query := `DELETE FROM llm_conversation_memory WHERE id = $1 AND account_id = $2`
	result, err := chat.dbManager.Db.Exec(query, id, accountId)
	if err != nil {
		return fmt.Errorf("history: failed to delete long-term memory: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("history: failed to get rows affected: %w", err)
	}
	if rowsAffected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// purgeStaleMemories deletes long-term memories that satisfy either TTL criterion.
// It fetches affected IDs first so each can be removed from RAG before the DB row
// is deleted, keeping both stores in sync.
func (chat *ConversationDao) purgeStaleMemories() {
	neverUsedDays := config.Config.MemoryTTLNeverUsedDays
	staleDays := config.Config.MemoryTTLStaleDays

	query := `
		SELECT id, account_id FROM llm_conversation_memory
		WHERE (use_count = 0    AND created_at  < now() - ($1 || ' days')::interval)
		   OR (use_count > 0    AND last_used_at < now() - ($2 || ' days')::interval)`

	queryCtx, queryCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer queryCancel()
	rows, err := chat.dbManager.Db.QueryContext(queryCtx, query, neverUsedDays, staleDays)
	if err != nil {
		slog.Error("memory-ttl: failed to query stale memories", "error", err)
		return
	}
	defer func() {
		if err := rows.Close(); err != nil {
			slog.Warn("memory-ttl: failed to close rows", "error", err)
		}
	}()

	type row struct{ id, accountId string }
	var candidates []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.accountId); err != nil {
			slog.Warn("memory-ttl: failed to scan row", "error", err)
			continue
		}
		candidates = append(candidates, r)
	}
	if err := rows.Err(); err != nil {
		slog.Error("memory-ttl: row iteration error", "error", err)
		return
	}

	if len(candidates) == 0 {
		slog.Info("memory-ttl: no stale memories found")
		return
	}

	// Remove each entry from RAG first so both stores stay in sync.
	// RAG failures are non-fatal — the DB delete proceeds regardless so the
	// DB remains authoritative and the orphaned RAG entry won't be retrieved.
	idsToDelete := make([]string, 0, len(candidates))
	for _, c := range candidates {
		if ragErr := toolcore.DeleteMemoryFromRAG(c.accountId, c.id); ragErr != nil {
			slog.Warn("memory-ttl: failed to delete from RAG, proceeding with DB delete", "id", c.id, "error", ragErr)
		}
		idsToDelete = append(idsToDelete, c.id)
	}

	// Batch-delete from DB in a single query to avoid N+1 round-trips.
	deleted := 0
	deleteCtx, deleteCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer deleteCancel()
	res, dbErr := chat.dbManager.Db.ExecContext(deleteCtx,
		`DELETE FROM llm_conversation_memory WHERE id = ANY($1::uuid[])`,
		pq.Array(idsToDelete),
	)
	if dbErr != nil {
		slog.Error("memory-ttl: failed to batch delete from DB", "error", dbErr)
	} else if rows, err := res.RowsAffected(); err == nil {
		deleted = int(rows)
	}

	slog.Info("memory-ttl: purge complete", "deleted", deleted, "candidates", len(candidates),
		"never_used_threshold_days", neverUsedDays, "stale_threshold_days", staleDays)
}

func (chat *ConversationDao) LoadLongTermMemories(accountId string, queryStr string, limit int) ([]string, error) {
	// If query is empty, use DB to list latest memories (fallback/maintenance)
	if queryStr == "" {
		query := `
		SELECT content FROM llm_conversation_memory 
		WHERE account_id = $1 
		ORDER BY created_at DESC LIMIT $2;`
		rows, err := chat.dbManager.Db.Queryx(query, accountId, limit)
		if err != nil {
			return nil, fmt.Errorf("history: failed to load long-term memories from DB: %w", err)
		}
		defer func() {
			if err := rows.Close(); err != nil {
				slog.Error("conversation_dao: failed to close rows", "error", err)
			}
		}()

		var memories []string
		for rows.Next() {
			var content string
			if err := rows.Scan(&content); err != nil {
				slog.Error("conversation_dao: failed to scan long-term memory row", "error", err)
				continue
			}
			memories = append(memories, content)
		}
		return memories, nil
	}

	// Use RAG for semantic search
	results := toolcore.QueryRAG("", accountId, queryStr, "long_term_memory", limit, "", "", "", false)
	var memories []string
	for _, res := range results {
		if res.Document != "" {
			memories = append(memories, res.Document)
		}
	}
	return memories, nil
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
	WHERE message_id = $1 AND status = 'success';`
	var count int
	err := chat.dbManager.Db.Get(&count, query, messageId)
	if err != nil {
		return 0, fmt.Errorf("history: failed to count successful tool calls for message: %w", err)
	}
	return count, nil
}
