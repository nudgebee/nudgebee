package agents

import (
	"encoding/json"
	"log/slog"
	"nudgebee/llm/common"
	"os"
	"testing"
)

func TestMigration(t *testing.T) {
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		slog.Error("unable to connect", "error", err)
		return
	}

	rows, err := dbms.Db.Queryx(`select distinct session_id 
		from llm_conversation_history lch 
		where role = 'human' and chain_name != 'router' and session_id not in (select distinct session_id
		from llm_conversations)`,
	)
	if err != nil {
		slog.Error("unable to query", "error", err)
		return
	}
	defer func() {
		if err := rows.Close(); err != nil {
			slog.Error("failed to close rows", "err", err)
		}
	}()
	sessionIds := []string{}

	for rows.Next() {
		var session string
		err := rows.Scan(&session)
		if err != nil {
			slog.Error("unable to scan", "error", err)
		}
		sessionIds = append(sessionIds, session)
	}
	slog.Info("migrating data for", "sessions", sessionIds)

	for _, session := range sessionIds {
		rows, err = dbms.Db.Queryx("select id::text, user_id::text, account_id::text, session_id::text, chain_name, role, message, recorded_at::text, request_type from llm_conversation_history where session_id = $1 and role = 'human' and chain_name != 'router'", session)
		if err != nil {
			slog.Error("unable to query", "error", err)
			return
		}
		defer func() {
			if err := rows.Close(); err != nil {
				slog.Error("failed to close rows", "err", err)
			}
		}()

		mappedRows := []map[string]any{}

		for rows.Next() {
			m := map[string]any{}
			err := rows.MapScan(m)
			if err != nil {
				slog.Error("unable to scan row", "error", err)
			}
			mappedRows = append(mappedRows, m)
		}

		for _, m := range mappedRows {

			conversationId := m["id"]
			conversationMessageId := m["id"]
			conversationAccountId := m["account_id"]
			conversationUserId := m["user_id"]
			conversationSessionId := m["session_id"]
			tenantId := os.Getenv("TEST_TENANT")

			// add conversation/chat data
			_, err := dbms.Db.Exec(`insert into llm_conversations(id, user_id, account_id, session_id, recorded_at, status, tenant_id) 
															values($1, $2, $3, $4, $5, $6, $7) on conflict (id) do nothing;`, conversationId, conversationUserId, conversationAccountId, conversationSessionId, m["recorded_at"], "COMPLETED", tenantId)
			if err != nil {
				slog.Error("unable to insert data in conversations", "session", session, "error", err)
				continue
			}

			// get rest of the data
			rows, err := dbms.Db.Queryx("select id::text, user_id::text, account_id::text, session_id::text, chain_name, role, message, recorded_at::text, request_type from llm_conversation_history where session_id = $1 and role != 'human' and chain_name != 'router'", conversationSessionId)
			if err != nil {
				slog.Error("unable to query", "error", err)
				return
			}
			defer func() {
				if err := rows.Close(); err != nil {
					slog.Error("failed to close rows", "err", err)
				}
			}()

			response := ""
			conversationSequence := []string{}
			conversationGroup := map[string]map[string]any{}

			for rows.Next() {
				m := map[string]any{}
				err := rows.MapScan(m)
				if err != nil {
					slog.Error("unable to scan row", "error", err)
				}
				if m["request_type"] == "response" || m["request_type"] == "error" {
					response = m["message"].(string)
				} else if m["request_type"] == "tool_call" {
					messageData := map[string]any{}
					err := json.Unmarshal([]byte(m["message"].(string)), &messageData)
					if err != nil {
						slog.Error("unable to process request", "id", m["id"])
						continue
					}
					if messageData["id"] == nil {
						continue
					}
					messageData["request_id"] = m["id"]
					messageData["request_recorded_at"] = m["recorded_at"]
					conversationSequence = append(conversationSequence, messageData["id"].(string))
					conversationGroup[messageData["id"].(string)] = messageData
				} else if m["request_type"] == "tool_call_response" {
					messageData := map[string]any{}
					err := json.Unmarshal([]byte(m["message"].(string)), &messageData)
					if err != nil {
						slog.Error("unable to process response", "id", m["id"])
						continue
					}
					responseData := messageData["tool_response"]
					responseDataMap := responseData.(map[string]any)
					tool_id := responseDataMap["tool_call_id"]
					if tool_id == nil {
						continue
					}
					tool_request := conversationGroup[tool_id.(string)]
					if tool_request == nil {
						continue
					}
					tool_request["response"] = responseDataMap
				}
			}

			_, err = dbms.Db.Exec(`insert into llm_conversation_messages(id, conversation_id, message, recorded_at, response, role, account_id, user_id, message_type) 
																values($1, $2, $3, $4, $5, $6, $7, $8, $9) on conflict (id) do nothing;`,
				conversationMessageId, conversationId, m["message"], m["recorded_at"], response, "ai", conversationAccountId, conversationUserId, "generation",
			)
			if err != nil {
				slog.Error("unable to insert data into llm_conversation_messages", "session", session, "error", err)
			}

			for _, agentMessagesId := range conversationSequence {
				agentData := conversationGroup[agentMessagesId]
				functionData := agentData["function"].(map[string]any)
				toolResponse := map[string]any{
					"content": "",
				}
				if agentData["response"] != nil {
					toolResponse = agentData["response"].(map[string]any)
				}

				_, err = dbms.Db.Exec(`insert into llm_conversation_agent(id, agent_name, message_id, response, recorded_at, account_id, user_id, parent_agent_id, query, conversation_id, thought) 
								values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11) on conflict (id) do nothing;`,
					agentData["request_id"], functionData["name"], conversationMessageId, toolResponse["content"].(string), agentData["request_recorded_at"], conversationAccountId, conversationUserId, "00000000-0000-0000-0000-000000000000", functionData["arguments"], conversationId, agentData["log"])

				if err != nil {
					slog.Error("unable to insert data into llm_conversation_agent", "session", session, "agent", agentData["request_id"], "error", err)
					continue
				}

				_, err = dbms.Db.Exec(`insert into llm_conversation_tool_calls(id, tool_name, parameters, response, recorded_at, tool_id, user_id, account_id, agent_id, message_id, conversation_id) 
								values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11) on conflict (id) do nothing;`,
					agentData["request_id"], functionData["name"], functionData["arguments"], toolResponse["content"].(string), agentData["request_recorded_at"], agentMessagesId, conversationUserId, conversationAccountId, agentData["request_id"], conversationMessageId, conversationId)

				if err != nil {
					slog.Error("unable to insert data into llm_conversation_tool_calls", "session", session, "agent", agentData["request_id"], "error", err)
				}

			}
		}

	}

}
