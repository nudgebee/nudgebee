package notification

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"nudgebee/runbook/common"
	"nudgebee/runbook/config"
	"nudgebee/runbook/services/security"
	"slices"

	"github.com/google/uuid"
)

func validatePlatform(platform string) error {
	validPlatforms := []string{"slack", "ms_teams", "google_chat"}
	if !slices.Contains(validPlatforms, platform) {
		return fmt.Errorf("unsupported platform %s", platform)
	}
	return nil
}

type SendImNotificationRequest struct {
	Platform   string         `json:"platform" validate:"required"`
	Channel    string         `json:"channel" validate:"required"`
	ThreadId   string         `json:"thread_id"`
	TeamId     string         `json:"team_id"`
	Body       string         `json:"body"`
	AccountID  string         `json:"account_id" validate:"required"`
	Parameters map[string]any `json:"parameters"`
}

type SendImNotificationResponse struct {
	Platform  string `json:"platform"`
	TeamId    string `json:"team_id"`
	ChannelId string `json:"channel_id"`
	MessageTs string `json:"message_ts"`
	Status    string `json:"status"`
	Reason    string `json:"reason"`
}

func SendNotification(ctx *security.RequestContext, request SendImNotificationRequest) (SendImNotificationResponse, error) {
	if err := validatePlatform(request.Platform); err != nil {
		return SendImNotificationResponse{}, err
	}

	if ctx.GetSecurityContext().GetTenantId() == "" {
		return SendImNotificationResponse{}, errors.New("notifications: tenantId is required")
	}

	if err := common.ValidateStruct(request); err != nil {
		return SendImNotificationResponse{}, err
	}

	headers := map[string]string{
		"tenant":       ctx.GetSecurityContext().GetTenantId(),
		"Content-Type": "application/json",
	}

	requestParams := map[string]any{
		"id": request.Channel,
	}
	if request.TeamId != "" {
		requestParams["team_id"] = request.TeamId
	}

	params := make(map[string]any)
	if request.Body != "" {
		params["message"] = request.Body
	}
	for k, v := range request.Parameters {
		params[k] = v
	}

	requestBody := map[string]any{
		"kind":      "notification",
		"type":      "generic",
		"tenant_id": ctx.GetSecurityContext().GetTenantId(),
		"channels": map[string]any{
			request.Platform: []map[string]any{
				requestParams,
			},
		},
		"parameters": params,
		"source":     "runbook",
	}

	if request.ThreadId != "" {
		requestBody = map[string]any{
			"kind":       "notification",
			"type":       "generic",
			"tenant_id":  ctx.GetSecurityContext().GetTenantId(),
			"source":     "runbook",
			"parameters": params,
			"thread": map[string]any{
				"platform":   request.Platform,
				"channel_id": request.Channel,
				"team_id":    request.TeamId,
				"message_ts": request.ThreadId,
			},
		}
	}

	resp, err := common.HttpPost(config.Config.NotificationServerUrl+"/api/send_message", common.HttpWithHeaders(headers), common.HttpWithJsonBody(requestBody))
	if err != nil {
		ctx.GetLogger().Error("notifications: unable to send notifications", "error", err)
		return SendImNotificationResponse{}, errors.New("notifications: unable to send request")
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		ctx.GetLogger().Error("notifications: unable to read body", "error", err)
	}

	if resp.StatusCode != 200 && resp.StatusCode != 201 {
		return SendImNotificationResponse{}, fmt.Errorf("notifications: unable to process request - %s", string(body))
	}

	responses := []SendImNotificationResponse{}
	err = json.Unmarshal(body, &responses)
	if err != nil {
		return SendImNotificationResponse{}, err
	}

	if len(responses) == 0 {
		return SendImNotificationResponse{}, errors.New("notifications: unable to parse response")
	}

	return responses[0], nil
}

// SendDirectMessageRequest represents a request to send a direct message to a user
type SendDirectMessageRequest struct {
	Platform  string `json:"platform" validate:"required"`
	UserID    string `json:"user_id" validate:"required"`
	Body      string `json:"body" validate:"required"`
	TeamID    string `json:"team_id"`
	AccountID string `json:"account_id" validate:"required"`
}

// SendDirectMessageResponse represents the response from sending a direct message
type SendDirectMessageResponse struct {
	Platform  string `json:"platform"`
	UserID    string `json:"user_id"`
	ChannelID string `json:"channel_id"`
	MessageTs string `json:"message_ts"`
}

// SendDirectMessage sends a direct message to a user
func SendDirectMessage(ctx *security.RequestContext, request SendDirectMessageRequest) (SendDirectMessageResponse, error) {
	if err := validatePlatform(request.Platform); err != nil {
		return SendDirectMessageResponse{}, err
	}

	if ctx.GetSecurityContext().GetTenantId() == "" {
		return SendDirectMessageResponse{}, errors.New("notifications: tenantId is required")
	}

	if err := common.ValidateStruct(request); err != nil {
		return SendDirectMessageResponse{}, err
	}

	headers := map[string]string{
		"tenant":       ctx.GetSecurityContext().GetTenantId(),
		"Content-Type": "application/json",
	}

	requestBody := map[string]any{
		"platform": request.Platform,
		"user_id":  request.UserID,
		"text":     request.Body,
	}

	if request.TeamID != "" {
		requestBody["team_id"] = request.TeamID
	}

	resp, err := common.HttpPost(config.Config.NotificationServerUrl+"/api/users/message", common.HttpWithHeaders(headers), common.HttpWithJsonBody(requestBody))
	if err != nil {
		ctx.GetLogger().Error("notifications: unable to send direct message", "error", err)
		return SendDirectMessageResponse{}, errors.New("notifications: unable to send request")
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		ctx.GetLogger().Error("notifications: unable to read body", "error", err)
		return SendDirectMessageResponse{}, err
	}

	if resp.StatusCode != 200 && resp.StatusCode != 201 {
		return SendDirectMessageResponse{}, fmt.Errorf("notifications: unable to send direct message - %s", string(body))
	}

	var response struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
		Data    struct {
			UserID    string `json:"user_id"`
			ChannelID string `json:"channel_id"`
			MessageTs string `json:"message_ts"`
			Platform  string `json:"platform"`
		} `json:"data"`
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	err = json.Unmarshal(body, &response)
	if err != nil {
		return SendDirectMessageResponse{}, err
	}

	if !response.Success {
		return SendDirectMessageResponse{}, fmt.Errorf("notifications: %s", response.Error.Message)
	}

	return SendDirectMessageResponse{
		Platform:  response.Data.Platform,
		UserID:    response.Data.UserID,
		ChannelID: response.Data.ChannelID,
		MessageTs: response.Data.MessageTs,
	}, nil
}

type JoinChannelRequest struct {
	Platform  string `json:"platform" validate:"required"`
	ChannelID string `json:"channel_id" validate:"required"`
	AccountID string `json:"account_id" validate:"required"`
	TeamID    string `json:"team_id,omitempty"`
	Text      string `json:"text,omitempty"`
}

// GetThreadMessagesRequest represents a request to read thread messages
type GetThreadMessagesRequest struct {
	ChannelID string `json:"channel_id" validate:"required"`
	ThreadTs  string `json:"thread_ts" validate:"required"`
	TeamID    string `json:"team_id,omitempty"`
}

// ReactionInfo represents a reaction on a message
type ReactionInfo struct {
	Name  string   `json:"name"`
	Count int      `json:"count"`
	Users []string `json:"users"`
}

// UserInfo represents user information
type UserInfo struct {
	ID          string `json:"id"`
	Name        string `json:"name,omitempty"`
	RealName    string `json:"real_name,omitempty"`
	DisplayName string `json:"display_name,omitempty"`
	IsBot       bool   `json:"is_bot"`
}

// ThreadMessage represents a single message in a thread
type ThreadMessage struct {
	Ts         string         `json:"ts"`
	Text       string         `json:"text"`
	UserID     string         `json:"user_id,omitempty"`
	User       *UserInfo      `json:"user,omitempty"`
	Reactions  []ReactionInfo `json:"reactions"`
	IsParent   bool           `json:"is_parent"`
	ReplyCount int            `json:"reply_count,omitempty"`
}

// GetThreadMessagesResponse represents the response from reading thread messages
type GetThreadMessagesResponse struct {
	Success   bool            `json:"success"`
	ChannelID string          `json:"channel_id"`
	ThreadTs  string          `json:"thread_ts"`
	Messages  []ThreadMessage `json:"messages"`
	HasMore   bool            `json:"has_more"`
	Error     string          `json:"error,omitempty"`
}

// GetThreadMessages reads messages and reactions from a thread
func GetThreadMessages(ctx *security.RequestContext, request GetThreadMessagesRequest) (GetThreadMessagesResponse, error) {
	if ctx.GetSecurityContext().GetTenantId() == "" {
		return GetThreadMessagesResponse{}, errors.New("notifications: tenantId is required")
	}

	if err := common.ValidateStruct(request); err != nil {
		return GetThreadMessagesResponse{}, err
	}

	headers := map[string]string{
		"tenant":       ctx.GetSecurityContext().GetTenantId(),
		"Content-Type": "application/json",
	}

	requestBody := map[string]any{
		"tenant_id":  ctx.GetSecurityContext().GetTenantId(),
		"channel_id": request.ChannelID,
		"thread_ts":  request.ThreadTs,
	}

	if request.TeamID != "" {
		requestBody["team_id"] = request.TeamID
	}

	resp, err := common.HttpPost(config.Config.NotificationServerUrl+"/api/threads/messages", common.HttpWithHeaders(headers), common.HttpWithJsonBody(requestBody))
	if err != nil {
		ctx.GetLogger().Error("notifications: unable to get thread messages", "error", err)
		return GetThreadMessagesResponse{}, errors.New("notifications: unable to send request")
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		ctx.GetLogger().Error("notifications: unable to read body", "error", err)
		return GetThreadMessagesResponse{}, err
	}

	if resp.StatusCode != 200 && resp.StatusCode != 201 {
		return GetThreadMessagesResponse{}, fmt.Errorf("notifications: unable to get thread messages - %s", string(body))
	}

	var response GetThreadMessagesResponse
	err = json.Unmarshal(body, &response)
	if err != nil {
		return GetThreadMessagesResponse{}, err
	}

	return response, nil
}

// SendEmailRequest represents a request to send an email.
// Either Body or Template must be provided.
type SendEmailRequest struct {
	Recipients     []string       `json:"recipients" validate:"required"`
	Subject        string         `json:"subject" validate:"required"`
	Body           string         `json:"body,omitempty"`
	BodyFormat     string         `json:"body_format,omitempty"`
	Template       string         `json:"template,omitempty"`
	TemplateParams map[string]any `json:"template_params,omitempty"`
	ReplyTo        string         `json:"reply_to,omitempty"`
}

// SendEmailResponse represents the response from sending an email
type SendEmailResponse struct {
	Success bool     `json:"success"`
	SentTo  []string `json:"sent_to,omitempty"`
	Error   string   `json:"error,omitempty"`
}

// SendEmail sends an email to one or more recipients
func SendEmail(ctx *security.RequestContext, request SendEmailRequest) (SendEmailResponse, error) {
	if err := common.ValidateStruct(request); err != nil {
		return SendEmailResponse{Success: false, Error: err.Error()}, err
	}

	if len(request.Recipients) == 0 {
		return SendEmailResponse{Success: false, Error: "recipients is required"}, errors.New("notifications: recipients is required")
	}

	if request.Body == "" && request.Template == "" {
		return SendEmailResponse{Success: false, Error: "body or template is required"}, errors.New("notifications: body or template is required")
	}

	headers := map[string]string{
		"Content-Type": "application/json",
	}

	// Add tenant header if available
	if ctx.GetSecurityContext().GetTenantId() != "" {
		headers["tenant"] = ctx.GetSecurityContext().GetTenantId()
	}

	requestBody := map[string]any{
		"recipients": request.Recipients,
		"subject":    request.Subject,
	}

	if request.Body != "" {
		requestBody["body"] = request.Body
	}

	if request.BodyFormat != "" {
		requestBody["body_format"] = request.BodyFormat
	}

	if request.Template != "" {
		requestBody["template"] = request.Template
	}

	if request.TemplateParams != nil {
		requestBody["template_params"] = request.TemplateParams
	}

	if request.ReplyTo != "" {
		requestBody["reply_to"] = request.ReplyTo
	}

	resp, err := common.HttpPost(config.Config.NotificationServerUrl+"/api/send_email", common.HttpWithHeaders(headers), common.HttpWithJsonBody(requestBody))
	if err != nil {
		ctx.GetLogger().Error("notifications: unable to send email", "error", err)
		return SendEmailResponse{Success: false, Error: "unable to send request"}, errors.New("notifications: unable to send email")
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		ctx.GetLogger().Error("notifications: unable to read body", "error", err)
		return SendEmailResponse{Success: false, Error: "unable to read response"}, err
	}

	if resp.StatusCode != 200 && resp.StatusCode != 201 {
		return SendEmailResponse{Success: false, Error: string(body)}, fmt.Errorf("notifications: unable to send email - %s", string(body))
	}

	var response SendEmailResponse
	err = json.Unmarshal(body, &response)
	if err != nil {
		return SendEmailResponse{Success: false, Error: "unable to parse response"}, err
	}

	return response, nil
}

// AddReactionRequest represents a request to add a reaction to a message
type AddReactionRequest struct {
	Platform  string `json:"platform" validate:"required"`
	ChannelID string `json:"channel_id" validate:"required"`
	MessageID string `json:"message_id" validate:"required"`
	Emoji     string `json:"emoji" validate:"required"`
	TeamID    string `json:"team_id,omitempty"`
}

// AddReactionResponse represents the response from adding a reaction
type AddReactionResponse struct {
	Success   bool   `json:"success"`
	Provider  string `json:"provider"`
	ChannelID string `json:"channel_id,omitempty"`
	MessageID string `json:"message_id,omitempty"`
	Reaction  string `json:"reaction,omitempty"`
	Error     string `json:"error,omitempty"`
}

// AddReaction adds a reaction to a message on any supported platform
func AddReaction(ctx *security.RequestContext, request AddReactionRequest) (AddReactionResponse, error) {
	if err := validatePlatform(request.Platform); err != nil {
		return AddReactionResponse{Success: false, Error: err.Error()}, err
	}

	if ctx.GetSecurityContext().GetTenantId() == "" {
		return AddReactionResponse{Success: false, Error: "tenantId is required"}, errors.New("notifications: tenantId is required")
	}

	if err := common.ValidateStruct(request); err != nil {
		return AddReactionResponse{Success: false, Error: err.Error()}, err
	}

	headers := map[string]string{
		"tenant":       ctx.GetSecurityContext().GetTenantId(),
		"Content-Type": "application/json",
	}

	requestBody := map[string]any{
		"platform":   request.Platform,
		"tenant_id":  ctx.GetSecurityContext().GetTenantId(),
		"channel_id": request.ChannelID,
		"message_id": request.MessageID,
		"emoji":      request.Emoji,
	}

	if request.TeamID != "" {
		requestBody["team_id"] = request.TeamID
	}

	resp, err := common.HttpPost(config.Config.NotificationServerUrl+"/api/reactions/add", common.HttpWithHeaders(headers), common.HttpWithJsonBody(requestBody))
	if err != nil {
		ctx.GetLogger().Error("notifications: unable to add reaction", "error", err)
		return AddReactionResponse{Success: false, Error: "unable to send request"}, errors.New("notifications: unable to add reaction")
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		ctx.GetLogger().Error("notifications: unable to read body", "error", err)
		return AddReactionResponse{Success: false, Error: "unable to read response"}, err
	}

	if resp.StatusCode != 200 && resp.StatusCode != 201 {
		return AddReactionResponse{Success: false, Error: string(body)}, fmt.Errorf("notifications: unable to add reaction - %s", string(body))
	}

	var response AddReactionResponse
	err = json.Unmarshal(body, &response)
	if err != nil {
		return AddReactionResponse{Success: false, Error: "unable to parse response"}, err
	}

	return response, nil
}

func JoinChannel(ctx *security.RequestContext, request JoinChannelRequest) (map[string]any, error) {
	if err := validatePlatform(request.Platform); err != nil {
		return nil, err
	}

	if ctx.GetSecurityContext().GetTenantId() == "" {
		return nil, errors.New("notifications: tenantId is required")
	}

	if err := common.ValidateStruct(request); err != nil {
		return nil, err
	}

	headers := map[string]string{
		"tenant":       ctx.GetSecurityContext().GetTenantId(),
		"Content-Type": "application/json",
	}

	requestBody := map[string]any{
		"platform":   request.Platform,
		"account_id": request.AccountID,
		"tenant_id":  ctx.GetSecurityContext().GetTenantId(),
		"channel_id": request.ChannelID,
		"session_id": uuid.NewString(),
	}

	if request.TeamID != "" {
		requestBody["team_id"] = request.TeamID
	}
	if request.Text != "" {
		requestBody["text"] = request.Text
	}

	resp, err := common.HttpPost(config.Config.NotificationServerUrl+"/api/channels/join", common.HttpWithHeaders(headers), common.HttpWithJsonBody(requestBody))
	if err != nil {
		ctx.GetLogger().Error("notifications: unable to join channel", "error", err)
		return nil, errors.New("notifications: unable to join channel request")
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		ctx.GetLogger().Error("notifications: unable to read body", "error", err)
		return nil, err
	}

	if resp.StatusCode != 200 && resp.StatusCode != 201 {
		return nil, fmt.Errorf("notifications: unable to join channel - %s", string(body))
	}

	var response map[string]any
	err = json.Unmarshal(body, &response)
	if err != nil {
		return nil, err
	}

	return response, nil
}
