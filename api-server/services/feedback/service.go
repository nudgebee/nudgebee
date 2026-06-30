package feedback

import (
	"fmt"
	"nudgebee/services/internal/database"
	"nudgebee/services/security"
	"time"

	"github.com/jmoiron/sqlx"
)

func CreateConversationAiFeedback(context *security.RequestContext, feedbackRequest ConversationFeedbackRequest) (map[string]bool, error) {
	data := make(map[string]bool)
	if context.GetSecurityContext().GetUserId() == "" || context.GetSecurityContext().GetTenantId() == "" {
		return data, fmt.Errorf("unauthorized")
	}

	if !context.GetSecurityContext().HasAccountAccess(feedbackRequest.CloudAccountId, security.SecurityAccessTypeCreate) {
		return data, fmt.Errorf("unauthorized")
	}

	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return data, err
	}

	tenantId := context.GetSecurityContext().GetTenantId()
	userId := context.GetSecurityContext().GetUserId()

	// Feedback is singular per (response, user): a user's thumbs-up/down on a
	// given response (session_id) for a module replaces any prior feedback
	// rather than appending. The table started out append-only (plain INSERT),
	// which let a single response accumulate conflicting rows (e.g. a Yes and a
	// No). The conversation widget reads only the latest row while the admin
	// feedback tab lists all of them, so the two surfaces disagreed and the
	// icon could show the opposite of the recorded feedback (issue #32906).
	// Deleting prior rows for the key before inserting also collapses any
	// pre-existing duplicates the moment the user re-submits.
	deleteQuery := `DELETE FROM llm_conversation_feedback
		WHERE session_id = $1 AND module = $2 AND user_id = $3 AND cloud_account_id = $4 AND tenant_id = $5`
	insertQuery := `INSERT INTO llm_conversation_feedback (session_id,
		module,
		question,
		llm_response,
		user_corrected_response,
		useful,
		additional_notes,
		conversation_id,
		tenant_id,
		cloud_account_id,
		user_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`

	_, err = dbms.DoInTransaction(func(tx *sqlx.Tx) (any, error) {
		if _, err := tx.Exec(deleteQuery, feedbackRequest.SessionId, feedbackRequest.Module, userId,
			feedbackRequest.CloudAccountId, tenantId); err != nil {
			return nil, err
		}
		if _, err := tx.Exec(insertQuery, feedbackRequest.SessionId, feedbackRequest.Module, feedbackRequest.Question,
			feedbackRequest.LlmResponse, feedbackRequest.UserCorrectedResponse, feedbackRequest.Useful, feedbackRequest.AdditionalNotes,
			feedbackRequest.ConversationId, tenantId, feedbackRequest.CloudAccountId, userId); err != nil {
			return nil, err
		}
		return nil, nil
	})
	if err != nil {
		return data, err
	}

	data["success"] = true
	return data, nil
}

func SaveConversation(context *security.RequestContext, saveConversationRequest SaveOrDeleteConversationRequest) (map[string]bool, error) {
	data := make(map[string]bool)
	if context.GetSecurityContext().GetUserId() == "" || context.GetSecurityContext().GetTenantId() == "" {
		return data, fmt.Errorf("unauthorized")
	}

	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return data, err
	}
	query := `INSERT INTO llm_conversation_saved (conversation_id, 
		user_id, 
		created_at) 
		VALUES ($1, $2, $3)`
	_, err = dbms.Db.Exec(query, saveConversationRequest.ConversationId, context.GetSecurityContext().GetUserId(), time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return data, err
	}

	data["success"] = true
	return data, nil
}

func DeleteSavedConversation(context *security.RequestContext, deleteConversationRequest SaveOrDeleteConversationRequest) (map[string]bool, error) {
	data := make(map[string]bool)
	if context.GetSecurityContext().GetUserId() == "" || context.GetSecurityContext().GetTenantId() == "" {
		return data, fmt.Errorf("unauthorized")
	}

	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return data, err
	}
	query := `DELETE FROM llm_conversation_saved WHERE conversation_id = $1 and user_id = $2`
	_, err = dbms.Db.Exec(query, deleteConversationRequest.ConversationId, context.GetSecurityContext().GetUserId())
	if err != nil {
		return data, err
	}

	data["success"] = true
	return data, nil
}

func DeleteConversationByConversationId(context *security.RequestContext, deleteConversationRequest DeleteConversationRequest) (map[string]bool, error) {
	data := make(map[string]bool)
	data["success"] = false
	if context.GetSecurityContext().GetUserId() == "" || context.GetSecurityContext().GetTenantId() == "" {
		return data, fmt.Errorf("unauthorized")
	}

	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return data, err
	}

	// Get the conversation
	llmConversation := LLMConversations{}
	selectQuery := `SELECT id, user_id, status FROM llm_conversations WHERE id = $1`
	err = dbms.Db.Get(&llmConversation, selectQuery, deleteConversationRequest.ConversationId)
	if err != nil {
		context.GetLogger().Error("Error fetching conversation", "error", err)
		return data, err
	}

	if llmConversation.UserId != context.GetSecurityContext().GetUserId() {
		return data, fmt.Errorf("not authorized to delete conversation of other user")
	} else if llmConversation.Status == "IN_PROGRESS" || llmConversation.Status == "PENDING" {
		return data, fmt.Errorf("feedback: not authorized to delete In Progress or Pending conversations")
	}

	// Delete from tables with foreign key constraints first
	tables := []string{
		"llm_conversation_tool_calls",
		"llm_conversation_agent",
		"llm_conversation_messages",
		"llm_conversation_saved",
	}

	for _, table := range tables {
		query := fmt.Sprintf("DELETE FROM %s WHERE conversation_id = $1", table)
		_, err = dbms.Db.Exec(query, deleteConversationRequest.ConversationId)
		if err != nil {
			context.GetLogger().Error(fmt.Sprintf("Error deleting from %s", table), "error", err)
			return data, err
		}
	}

	// Finally delete the conversation itself
	query := `DELETE FROM llm_conversations WHERE id = $1`
	result, err := dbms.Db.Exec(query, deleteConversationRequest.ConversationId)
	if err != nil {
		context.GetLogger().Error("Error deleting llm_conversation", "error", err)
		return data, err
	}

	// Check if any rows were affected by the delete
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return data, err
	}

	if rowsAffected > 0 {
		data["success"] = true
	}

	return data, nil
}
