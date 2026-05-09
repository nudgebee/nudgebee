package core

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"strings"
	"time"
)

var AsyncRefWorkerPool *common.WorkerPool

func init() {
	AsyncRefWorkerPool = common.NewWorkerPool("async_ref_title_gen", config.Config.AsyncRefWorkerCount, 100)
}

func GenerateReferenceTitleAsync(ctx NbToolContext, ref NBToolResponseReference, query string) {
	if !config.Config.EnableLLMReferenceTitleGeneration {
		return
	}

	if ctx.ToolCallId == "" || ctx.AccountId == "" {
		return
	}

	// Create a background context to ensure execution completes even if request context is cancelled
	bgCtx := security.NewRequestContext(
		context.Background(),
		ctx.Ctx.GetSecurityContext(),
		ctx.Ctx.GetLogger(),
		ctx.Ctx.GetTracer(),
		ctx.Ctx.GetMeter(),
	)

	err := AsyncRefWorkerPool.Submit(bgCtx.GetContext(), func() {
		// Wait for the main tool execution to finish and save the result to DB
		time.Sleep(10 * time.Second)

		// 1. Generate new title using LLM
		newTitle, err := generateTitleFromLLM(bgCtx, ctx, query, ref.Text)
		if err != nil {
			slog.Error("async_ref: failed to generate title", "error", err)
			return
		}
		if newTitle == "" || newTitle == ref.Text {
			return
		}

		// 2. Update DB
		err = updateReferenceInDB(ctx, ref, newTitle)
		if err != nil {
			slog.Error("async_ref: failed to update DB", "error", err)
		} else {
			slog.Info("async_ref: successfully updated reference title", "old", ref.Text, "new", newTitle)
		}
	})

	if err != nil {
		slog.Warn("async_ref: failed to submit task to worker pool", "error", err)
	}
}

// generateTitleFromLLM uses the LLM tool to generate a title.
func generateTitleFromLLM(ctx *security.RequestContext, originalToolCtx NbToolContext, query, currentTitle string) (string, error) {
	llmTool, ok := GetNBTool(originalToolCtx.AccountId, "LLM")
	if !ok {
		return "", fmt.Errorf("LLM tool not found")
	}

	prompt := fmt.Sprintf(`You are a helpful assistant.
	Task: Generate a concise, descriptive title (max 5-7 words) for the following data query.
	Query: "%s"
	Current Title: "%s"
	
	Instructions:
	- Return ONLY the new title.
	- Do not use quotes.
	- Do not include "Title:".
	- If the query is too complex or unclear, just return the Current Title.
	`, query, currentTitle)

	request := NBToolCallRequest{
		Command: prompt,
	}

	// Create a minimal context for LLM tool
	// We don't need full history for this simple task, but we need IDs for token tracking and cost attribution
	toolCtx := NewNbToolContext(
		ctx,
		llmTool,
		originalToolCtx.AccountId,
		originalToolCtx.UserId,
		originalToolCtx.ConversationId,
		originalToolCtx.MessageId,
		originalToolCtx.ParentAgentId,
		prompt,
		nil,
		"",
		NBQueryConfig{},
		"",
	)

	resp, err := llmTool.Call(toolCtx, request)
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(resp.Data), nil
}

func updateReferenceInDB(ctx NbToolContext, targetRef NBToolResponseReference, newTitle string) error {
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return err
	}

	// Find the row
	// Key: conversation_id, message_id, tool_id (which is ctx.ToolCallId), agent_id (ctx.ParentAgentId)
	selectQuery := `SELECT "references" FROM llm_conversation_tool_calls 
		WHERE conversation_id = $1 AND message_id = $2 AND tool_id = $3 AND agent_id = $4`

	var referencesJson string
	err = dbms.Db.QueryRow(selectQuery, ctx.ConversationId, ctx.MessageId, ctx.ToolCallId, ctx.ParentAgentId).Scan(&referencesJson)
	if err != nil {
		return fmt.Errorf("failed to fetch references: %w", err)
	}

	var references []NBToolResponseReference
	err = json.Unmarshal([]byte(referencesJson), &references)
	if err != nil {
		return fmt.Errorf("failed to unmarshal references: %w", err)
	}

	updated := false
	for i, ref := range references {
		// Match by URL and old Text
		if ref.Url == targetRef.Url && ref.Text == targetRef.Text {
			references[i].Text = fmt.Sprintf("%s - %s", ref.Text, newTitle)
			updated = true
			break
		}
	}

	if !updated {
		return fmt.Errorf("reference not found in DB record")
	}

	newReferencesJson, err := json.Marshal(references)
	if err != nil {
		return fmt.Errorf("failed to marshal new references: %w", err)
	}

	updateQuery := `UPDATE llm_conversation_tool_calls 
		SET "references" = $5, updated_at = now() 
		WHERE conversation_id = $1 AND message_id = $2 AND tool_id = $3 AND agent_id = $4`

	_, err = dbms.Db.Exec(updateQuery, ctx.ConversationId, ctx.MessageId, ctx.ToolCallId, ctx.ParentAgentId, string(newReferencesJson))
	if err != nil {
		return fmt.Errorf("failed to update references in DB: %w", err)
	}

	return nil
}
