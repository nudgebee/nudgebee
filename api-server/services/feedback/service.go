package feedback

import (
	"fmt"
	"nudgebee/services/internal/database"
	"nudgebee/services/security"
	"time"
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
	query := `INSERT INTO llm_conversation_feedback (session_id, 
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
	_, err = dbms.Db.Exec(query, feedbackRequest.SessionId, feedbackRequest.Module, feedbackRequest.Question,
		feedbackRequest.LlmResponse, feedbackRequest.UserCorrectedResponse, feedbackRequest.Useful, feedbackRequest.AdditionalNotes,
		feedbackRequest.ConversationId, context.GetSecurityContext().GetTenantId(), feedbackRequest.CloudAccountId, context.GetSecurityContext().GetUserId())
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
	_, err = dbms.Db.Exec(query, saveConversationRequest.ConversationId, context.GetSecurityContext().GetUserId(), time.Now().Format(time.RFC3339))
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
