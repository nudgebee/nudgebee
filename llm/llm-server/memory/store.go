package memory

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/knowledge"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

// Store is the legacy long-term memory store: the llm_conversation_memory rows
// agents/core reads into the prompt and writes after a turn. Memory-v2 lives in
// ee/memory.
type Store interface {
	SaveLongTermMemory(accountId, conversationId, messageId, content string, memoryType MemoryType) (string, error)
	LoadLongTermMemories(accountId string, query string, limit int) ([]string, error)
	ListLongTermMemories(accountId string, conversationId string, messageIds []string, memoryType string, query string, limit int, offset int) ([]LongTermMemory, error)
	DeleteLongTermMemory(id, accountId string) error
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
}

type dao struct {
	dbManager *common.DatabaseManager
}

var (
	storeMu sync.Mutex
	store   Store
)

// GetStore returns the shared store on the metastore database, connecting if
// needed. It returns nil when the database is unavailable.
func GetStore() Store {
	storeMu.Lock()
	defer storeMu.Unlock()
	if store != nil {
		return store
	}
	dbManager, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		slog.Error("memory: unable to get database manager", "error", err)
		return nil
	}
	store = &dao{dbManager: dbManager}
	return store
}

// SetStore replaces the shared store and returns the one it replaced. Tests use
// it to install fakes.
func SetStore(s Store) (previous Store) {
	storeMu.Lock()
	defer storeMu.Unlock()
	previous, store = store, s
	return previous
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

func (chat *dao) RetrieveRelevantMemories(accountId string, queryStr string, limit int) ([]LongTermMemory, error) {
	if queryStr == "" {
		return nil, nil
	}
	results := knowledge.QueryRAG("", accountId, queryStr, "long_term_memory", limit, "", "", "", false)
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
func (chat *dao) FindSimilarMemories(accountId string, content string, limit int) ([]LongTermMemory, error) {
	if content == "" {
		return nil, nil
	}
	results := knowledge.QueryRAG("", accountId, content, "long_term_memory", limit, "", "", "", false)
	memories := parseRAGMemoryResults(results)
	return memories, nil
}

// parseRAGMemoryResults converts raw RAG query results into LongTermMemory structs.
// It is the single place that maps RAG metadata (_id, memory_type, similarity_score)
// onto the Go struct, shared by RetrieveRelevantMemories and FindSimilarMemories.
func parseRAGMemoryResults(results knowledge.RAGSearchResults) []LongTermMemory {
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
func (chat *dao) incrementMemoryUsageAsync(accountId string, memories []LongTermMemory) {
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
	if err := common.GetStatusUpdateWorkerPool().Submit(submissionCtx, func() {
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
func (chat *dao) IncrementMemoryUsage(accountId string, memories []LongTermMemory) {
	chat.incrementMemoryUsageAsync(accountId, memories)
}

// CarryOverMemoryUseCount copies use_count from the old memory row to the new one via
// the bounded status-update worker pool. This preserves access history when a memory is
// updated (insert-then-delete pattern). Failures are non-fatal.
func (chat *dao) CarryOverMemoryUseCount(oldID, newID, accountId string) {
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
	if err := common.GetStatusUpdateWorkerPool().Submit(submissionCtx, func() {
		dbCtx, dbCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer dbCancel()
		if _, err := db.ExecContext(dbCtx, query, oldID, newID, accountId); err != nil {
			slog.Warn("history: failed to carry over memory use_count", "old_id", oldID, "new_id", newID, "error", err)
		}
	}); err != nil {
		slog.Warn("history: failed to submit carry-over use_count to pool", "old_id", oldID, "new_id", newID, "error", err)
	}
}

func (chat *dao) SaveLongTermMemory(accountId, conversationId, messageId, content string, memoryType MemoryType) (string, error) {
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
	err = knowledge.AddMemoryToRAG(accountId, content, id, string(memoryType))
	if err != nil {
		// Log error but don't fail the operation as DB save was successful
		slog.Error("history: failed to save memory to RAG", "error", err, "memoryType", memoryType)
	}

	return id, nil
}

func (chat *dao) ListLongTermMemories(accountId string, conversationId string, messageIds []string, memoryType string, searchQuery string, limit int, offset int) ([]LongTermMemory, error) {
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

	if len(messageIds) > 0 {
		query += fmt.Sprintf(" AND message_id = ANY($%d)", argCounter)
		args = append(args, pq.Array(messageIds))
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

	if len(messageIds) > 1 {
		// Batched calls (useMessageAdditionalData fetches several newly-completed
		// message_ids in one call) need a per-message_id cap, not one flat LIMIT
		// across the whole batch — otherwise a message with more-recently-created
		// rows can crowd another message's rows out of the result entirely.
		// Offset doesn't have a coherent meaning against a per-message-ranked set
		// and no caller passes one alongside a multi-id batch today, so it's not
		// applied in this branch. The single-message_id / no-filter path below
		// (offset included) is left as a flat LIMIT, unchanged.
		query = fmt.Sprintf(`
			SELECT id, account_id, conversation_id, message_id, content, memory_type, use_count, last_used_at, created_at
			FROM (
				SELECT ranked.*, ROW_NUMBER() OVER (PARTITION BY ranked.message_id ORDER BY ranked.created_at DESC) AS rn
				FROM (%s) ranked
			) numbered
			WHERE rn <= $%d
			ORDER BY created_at DESC`, query, argCounter)
		args = append(args, limit)
	} else {
		query += fmt.Sprintf(" ORDER BY created_at DESC LIMIT $%d OFFSET $%d;", argCounter, argCounter+1)
		args = append(args, limit, offset)
	}

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

func (chat *dao) DeleteLongTermMemory(id, accountId string) error {
	// 1. Delete from RAG using ID
	err := knowledge.DeleteMemoryFromRAG(accountId, id)
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
func (chat *dao) purgeStaleMemories() {
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
		if ragErr := knowledge.DeleteMemoryFromRAG(c.accountId, c.id); ragErr != nil {
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

func (chat *dao) LoadLongTermMemories(accountId string, queryStr string, limit int) ([]string, error) {
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
	results := knowledge.QueryRAG("", accountId, queryStr, "long_term_memory", limit, "", "", "", false)
	var memories []string
	for _, res := range results {
		if res.Document != "" {
			memories = append(memories, res.Document)
		}
	}
	return memories, nil
}

type MemoryType string

const (
	MemoryTypeInvestigationResult MemoryType = "investigation_result"
	MemoryTypeArchitecturalFact   MemoryType = "architectural_fact"
	MemoryTypeDependencyMapping   MemoryType = "dependency_mapping"
	MemoryTypeTroubleshooting     MemoryType = "troubleshooting_guide"
	MemoryTypeConfigInsight       MemoryType = "configuration_insight"
	MemoryTypeUserPreference      MemoryType = "user_preference"
	MemoryTypePattern             MemoryType = "pattern"
	MemoryTypeWorkflow            MemoryType = "workflow"
)

// StartMemoryTTLCleanup runs a periodic job that deletes stale long-term memories.
// Two independent criteria are applied:
//   - Never-used memories older than MemoryTTLNeverUsedDays (default 90d)
//   - Memories not accessed in MemoryTTLStaleDays (default 180d)
//
// Set MemoryTTLCleanupIntervalHours = 0 to disable. Respects context cancellation for
// clean shutdown.
func StartMemoryTTLCleanup(ctx context.Context) {
	intervalHours := config.Config.MemoryTTLCleanupIntervalHours
	if intervalHours <= 0 {
		slog.Info("memory-ttl: cleanup disabled (llm_memory_ttl_cleanup_interval_hours=0)")
		return
	}

	// purgeStaleMemories is on the concrete *dao, not the interface.
	// The underlying type is always *dao in production; fail fast if not.
	store, ok := GetStore().(*dao)
	if !ok {
		slog.Error("memory-ttl: store is not *dao, cleanup disabled")
		return
	}

	interval := time.Duration(intervalHours) * time.Hour
	slog.Info("memory-ttl: starting cleanup job",
		"interval_hours", intervalHours,
		"never_used_days", config.Config.MemoryTTLNeverUsedDays,
		"stale_days", config.Config.MemoryTTLStaleDays,
	)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// Run once at startup, then on each tick.
	store.purgeStaleMemories()

	for {
		select {
		case <-ctx.Done():
			slog.Info("memory-ttl: cleanup job stopped")
			return
		case <-ticker.C:
			store.purgeStaleMemories()
		}
	}
}

func (m MemoryType) Validate() MemoryType {
	switch string(m) {
	case "user_preference":
		return MemoryTypeUserPreference
	case "pattern":
		return MemoryTypePattern
	case "workflow":
		return MemoryTypeWorkflow
	case "investigation_result":
		return MemoryTypeInvestigationResult
	case "architectural_fact":
		return MemoryTypeArchitecturalFact
	case "dependency_mapping":
		return MemoryTypeDependencyMapping
	case "troubleshooting_guide":
		return MemoryTypeTroubleshooting
	case "configuration_insight":
		return MemoryTypeConfigInsight
	default:
		return MemoryTypeInvestigationResult
	}
}
