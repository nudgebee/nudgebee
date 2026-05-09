package core

import (
	"context"
	"io"
	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"time"

	"github.com/google/uuid"
)

func sendReplyToNotificationServer(ctx *security.RequestContext, agentRequest NBAgentRequest, response NBAgentResponse, err error) {
	if response.AgentName == "router" {
		return
	}

	notificationUrl := config.Config.NotificationServerUrl + "/llm/response"
	c, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	notificationRequest := map[string]any{
		"conversation_id": response.SessionId,
		"session_id":      response.SessionId,
		"tenant_id":       ctx.GetSecurityContext().GetTenantId(),
	}

	switch {
	case err != nil:
		notificationRequest["type"] = "error"
		notificationRequest["response"] = err.Error()

	case response.Status == ConversationStatusWaiting || response.Status == ConversationStatusWaitingForClientTool:
		notificationRequest["type"] = "follow-up"
		followupData := map[string]any{
			"question": response.FollowupRequest.Question,
		}
		if response.Status == ConversationStatusWaitingForClientTool {
			followupData["question"] = "Waiting for client tool execution"
			if len(response.Response) > 0 {
				followupData["question"] = response.Response[0]
			}
		}
		if len(response.FollowupRequest.FollowupOptions) > 0 {
			followupData["followupOptions"] = response.FollowupRequest.FollowupOptions
		}
		if response.FollowupRequest.AgentName != "" {
			followupData["agent_name"] = response.FollowupRequest.AgentName
		}
		if response.FollowupRequest.AgentId != uuid.Nil {
			followupData["agent_id"] = response.FollowupRequest.AgentId.String()
		}
		if response.MessageId != "" {
			followupData["message_id"] = response.MessageId
		}
		jsonBytes, err := common.MarshalJson(followupData)
		if err != nil {
			ctx.GetLogger().Error("notifications: failed to marshal followup request", "error", err)
			notificationRequest["response"] = response.FollowupRequest.Question
		} else {
			notificationRequest["response"] = string(jsonBytes)
		}

	default:
		notificationRequest["type"] = "final"
		if len(response.Response) > 0 {
			response := response.Response[0]
			response = convertMarkdownToSlackMarkdown(response)
			notificationRequest["response"] = response
		} else {
			notificationRequest["response"] = ""
		}
	}

	ctx.GetLogger().Info("notifications: sending notification request", "request_type", notificationRequest["type"], "message_id", response.MessageId, "agent_name", response.AgentName)
	resp, err := common.HttpPost(notificationUrl, common.HttpWithJsonBody(notificationRequest), common.HttpWithContext(c))
	if err != nil {
		ctx.GetLogger().Error("notifications: unable to send webhook notification", "error", err)
		return
	}
	defer func() {
		err := resp.Body.Close()
		if err != nil {
			ctx.GetLogger().Error("notifications: unable to close response body", "error", err)
		}
	}()
	if resp.StatusCode != 200 {
		data, err := io.ReadAll(resp.Body)
		if err != nil {
			ctx.GetLogger().Info("notifications: unable to read notification server resp", "error", err, "statuscode", resp.StatusCode)
		} else {
			ctx.GetLogger().Debug("notifications: notification server response", "statuscode", resp.StatusCode, "data", string(data), "message_id", response.MessageId, "request_type", notificationRequest["type"])
		}
	}
}
