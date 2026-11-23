package tools

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"
)

func init() {
	core.RegisterNBToolFactory(ToolIMMessageSend, func(accountId string) (core.NBTool, error) {
		return IMMessageSendTool{}, nil
	})
}

const ToolIMMessageSend = "im_message_send"

// imMessageMaxBytes keeps a message under what every platform takes in one
// post (Teams cuts a generic card at 20 KB), so nothing is cut without the
// person knowing.
const imMessageMaxBytes = 12_000

// imMessageSource is the notification source of a message a person sends from
// Nubi: notifications-server delivers it only to the channel named, past every
// notification rule.
const imMessageSource = "nubi"

// The conversation sources a person is behind. Slack/Teams chat
// (InstantNotification) counts: the chat user is matched to their Nudgebee
// login before the request arrives.
var imMessageAttendedSources = map[string]bool{
	"UserInvestigation":    true,
	"UserInvestigationCli": true,
	"InstantNotification":  true,
}

var imMessagePlatforms = []string{"slack", "ms_teams", "google_chat"}

var imMessagePlatformNames = map[string]string{
	"slack":       "Slack",
	"ms_teams":    "Microsoft Teams",
	"google_chat": "Google Chat",
}

// IMMessageSendTool posts a message a person asked for to a Slack, Microsoft
// Teams or Google Chat channel, after they approve it.
type IMMessageSendTool struct{}

func (t IMMessageSendTool) Name() string             { return ToolIMMessageSend }
func (t IMMessageSendTool) GetType() core.NBToolType { return core.NBToolTypeTool }

func (t IMMessageSendTool) Description() string {
	return "Sends (posts / shares) a message to a Slack, Microsoft Teams or Google Chat channel when the user asks, e.g. \"send this summary to #ops\". " +
		"The user approves the exact message before it is posted, and it is signed as sent by them via Nubi. " +
		"Call it yourself, never through delegate_agent: the user approves the message in this conversation. " +
		"Inputs: channel (required — channel name like ops or #ops, or its id), message (required — markdown headings and bullets; write links as plain URLs; no tables or code blocks), " +
		"platform (optional: slack, ms_teams, google_chat — set it when the user names the app), team (optional — the Microsoft Teams team name, when channel names repeat across teams). " +
		"When the channel is not found or matches more than one, nothing is sent and the candidates are returned: ask the user which one."
}

func (t IMMessageSendTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.ToolSchemaProperty{
			"channel":  {Type: core.ToolSchemaTypeString, Description: "Channel name (ops or #ops) or channel id."},
			"message":  {Type: core.ToolSchemaTypeString, Description: "The message to post, in markdown: headings and bullets, links as plain URLs."},
			"platform": {Type: core.ToolSchemaTypeString, Description: "slack, ms_teams or google_chat. Leave empty to look in every connected app."},
			"team":     {Type: core.ToolSchemaTypeString, Description: "Microsoft Teams only: the team's name or id."},
		},
		Required: []string{"channel", "message"},
	}
}

// InferToolRequestType is static: every call posts a message.
func (t IMMessageSendTool) InferToolRequestType(_ *security.RequestContext, _, _ string) (core.ToolRequestType, error) {
	return core.ToolRequestTypeCreate, nil
}

// ConfirmationKey ties the approval to this exact destination and message.
func (t IMMessageSendTool) ConfirmationKey(toolInput string) string {
	return perActionConfirmationKey(ToolIMMessageSend, toolInput)
}

// ConfirmationQuestion shows where the message goes and the message itself.
func (t IMMessageSendTool) ConfirmationQuestion(toolInput string) string {
	args := confirmationArgs(toolInput)
	channel, _ := args["channel"].(string)
	message, _ := args["message"].(string)
	if strings.TrimSpace(channel) == "" || strings.TrimSpace(message) == "" {
		return ""
	}
	where := "#" + strings.TrimPrefix(strings.TrimSpace(channel), "#")
	if team, _ := args["team"].(string); strings.TrimSpace(team) != "" {
		where = strings.TrimSpace(team) + " / " + where
	}
	if platform, _ := args["platform"].(string); imMessagePlatformNames[platform] != "" {
		where += " on " + imMessagePlatformNames[platform]
	}
	return fmt.Sprintf("Post this message to %s?\n\n%s", where, message)
}

func (t IMMessageSendTool) Call(nbCtx core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {
	if err := imMessageAttended(nbCtx); err != nil {
		return errNBLLMToolResponse(err), nil
	}
	channel := strings.TrimSpace(stringArg(input, "channel"))
	message := strings.TrimSpace(stringArg(input, "message"))
	if channel == "" || message == "" {
		return errNBLLMToolResponse(errors.New("channel and message are required")), nil
	}
	if len(message) > imMessageMaxBytes {
		return errNBLLMToolResponse(fmt.Errorf("the message is %d characters; shorten it to under %d and ask the user to approve the shorter one", len(message), imMessageMaxBytes)), nil
	}
	platforms := imMessagePlatforms
	if platform := strings.TrimSpace(stringArg(input, "platform")); platform != "" {
		if imMessagePlatformNames[platform] == "" {
			return errNBLLMToolResponse(fmt.Errorf("platform must be one of slack, ms_teams, google_chat, not %q", platform)), nil
		}
		platforms = []string{platform}
	}

	destinations, err := listChatDestinations(nbCtx.Ctx, platforms)
	if err != nil {
		return errNBLLMToolResponse(err), nil
	}
	if len(destinations) == 0 {
		return errNBLLMToolResponse(errors.New("no Slack, Microsoft Teams or Google Chat channel is available: connect one under Integrations, and for Teams add the Nudgebee app to the team")), nil
	}
	destination, candidates := resolveChatDestination(destinations, channel, strings.TrimSpace(stringArg(input, "team")))
	if destination == nil {
		return errNBLLMToolResponse(destinationNotFoundError(channel, candidates)), nil
	}

	text := messageWithSignature(message, nbCtx.Ctx.GetSecurityContext().GetDisplayName(), chatConversationURL(nbCtx.AccountId, nbCtx.SessionId), destination.Platform)
	results, err := sendChatMessage(nbCtx.Ctx, *destination, text, nbCtx.AccountId)
	if err != nil {
		nbCtx.Ctx.GetLogger().Error("im_message_send: send failed", "error", err, "platform", destination.Platform)
		return errNBLLMToolResponse(err), nil
	}
	return chatSendResponse(*destination, results), nil
}

// imMessageAttended refuses a run nobody is watching (an automation, an alert
// investigation, a Bee) and one without a signed-in person, whatever approval
// rule would let it through: the message goes to people outside Nubi.
func imMessageAttended(nbCtx core.NbToolContext) error {
	if nbCtx.Source == "" {
		// A sub-agent's call carries no conversation source.
		return fmt.Errorf("nothing was sent: %s must be called directly in the conversation, not by a sub-agent, so the user can approve the message", ToolIMMessageSend)
	}
	if !imMessageAttendedSources[nbCtx.Source] {
		return errors.New("messages can only be sent to a chat channel from a conversation with a person in it, not from an automation or an automatic investigation")
	}
	userId := ""
	if nbCtx.Ctx != nil && nbCtx.Ctx.GetSecurityContext() != nil {
		userId = strings.TrimSpace(nbCtx.Ctx.GetSecurityContext().GetUserId())
	}
	if userId == "" || userId == security.GetSystemUserId() {
		return errors.New("messages can only be sent by a signed-in Nudgebee user; if you are chatting from Slack or Teams, use the email of your Nudgebee login there")
	}
	return nil
}

// chatDestination is one channel a message can go to. TeamID and TeamName are
// set for Microsoft Teams only.
type chatDestination struct {
	Platform    string `json:"platform"`
	TeamID      string `json:"team_id,omitempty"`
	TeamName    string `json:"team,omitempty"`
	ChannelID   string `json:"channel_id"`
	ChannelName string `json:"channel"`
}

func (d chatDestination) label() string {
	label := "#" + d.ChannelName
	if d.TeamName != "" {
		label = d.TeamName + " / " + label
	}
	return label + " (" + imMessagePlatformNames[d.Platform] + ")"
}

type chatChannelEntry struct {
	ID       string             `json:"id"`
	Name     string             `json:"name"`
	Channels []chatChannelEntry `json:"channels"`
}

// listChatDestinations reads the channels of each platform from
// notifications-server. A Teams entry is a team with its channels; only teams
// the Nudgebee app is in are listed.
func listChatDestinations(ctx *security.RequestContext, platforms []string) ([]chatDestination, error) {
	if config.Config.NotificationServerUrl == "" {
		return nil, errors.New("the notification service is not configured")
	}
	var destinations []chatDestination
	var failed []string
	for _, platform := range platforms {
		entries, err := listChatChannels(ctx, platform)
		if err != nil {
			ctx.GetLogger().Warn("im_message_send: could not list channels", "platform", platform, "error", err)
			failed = append(failed, imMessagePlatformNames[platform])
			continue
		}
		destinations = append(destinations, chatDestinationsOf(platform, entries)...)
	}
	if len(destinations) == 0 && len(failed) > 0 {
		return nil, fmt.Errorf("could not read the channels of %s; try again shortly", strings.Join(failed, ", "))
	}
	return destinations, nil
}

func chatDestinationsOf(platform string, entries []chatChannelEntry) []chatDestination {
	var destinations []chatDestination
	for _, entry := range entries {
		if platform != "ms_teams" {
			destinations = append(destinations, chatDestination{Platform: platform, ChannelID: entry.ID, ChannelName: entry.Name})
			continue
		}
		for _, channel := range entry.Channels {
			destinations = append(destinations, chatDestination{
				Platform: platform, TeamID: entry.ID, TeamName: entry.Name, ChannelID: channel.ID, ChannelName: channel.Name,
			})
		}
	}
	return destinations
}

func listChatChannels(ctx *security.RequestContext, platform string) ([]chatChannelEntry, error) {
	resp, err := common.HttpPost(strings.TrimRight(config.Config.NotificationServerUrl, "/")+"/api/channels/list",
		common.HttpWithHeaders(notificationServerHeaders(ctx)),
		common.HttpWithJsonBody(map[string]any{
			"input":             map[string]any{"platform": platform},
			"session_variables": map[string]any{"tenant_id": ctx.GetSecurityContext().GetTenantId()},
		}),
		common.HttpWithContext(ctx.GetContext()),
		common.HttpWithTimeout(20*time.Second),
	)
	if err != nil {
		return nil, fmt.Errorf("listChatChannels: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("listChatChannels: read body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("listChatChannels: status %d", resp.StatusCode)
	}
	// {"data": [...]}, with an empty list when the app is not connected; an
	// object without "data" when listing failed.
	var listed struct {
		Data *[]chatChannelEntry `json:"data"`
	}
	if err := json.Unmarshal(body, &listed); err != nil {
		return nil, fmt.Errorf("listChatChannels: decode: %w", err)
	}
	if listed.Data == nil {
		return nil, errors.New("listChatChannels: no channel list in the answer")
	}
	return *listed.Data, nil
}

// notificationServerHeaders are the headers notifications-server's action
// routes check. HttpWithHeaders replaces the header set, so it goes before the
// JSON body option, which adds its Content-Type.
func notificationServerHeaders(ctx *security.RequestContext) map[string]string {
	headers := map[string]string{"tenant": ctx.GetSecurityContext().GetTenantId()}
	if config.Config.NotificationServerToken != "" {
		headers["X-ACTION-TOKEN"] = config.Config.NotificationServerToken
	}
	return headers
}

func normalizeChatName(name string) string {
	return strings.ToLower(strings.TrimPrefix(strings.TrimSpace(name), "#"))
}

// resolveChatDestination finds the one destination channel names, by id first,
// then by name within team when one is given. Without exactly one match it
// returns nil and the destinations worth offering instead.
func resolveChatDestination(destinations []chatDestination, channel, team string) (*chatDestination, []chatDestination) {
	for i, d := range destinations {
		if d.ChannelID != "" && strings.EqualFold(d.ChannelID, strings.TrimSpace(channel)) {
			return &destinations[i], nil
		}
	}
	name := normalizeChatName(channel)
	teamName := normalizeChatName(team)
	inTeam := func(d chatDestination) bool {
		return teamName == "" || normalizeChatName(d.TeamName) == teamName || strings.EqualFold(d.TeamID, strings.TrimSpace(team))
	}
	var exact, similar []chatDestination
	for _, d := range destinations {
		if !inTeam(d) {
			continue
		}
		candidate := normalizeChatName(d.ChannelName)
		switch {
		case candidate == name:
			exact = append(exact, d)
		case candidate != "" && name != "" && (strings.Contains(candidate, name) || strings.Contains(name, candidate)):
			similar = append(similar, d)
		}
	}
	if len(exact) == 1 {
		return &exact[0], nil
	}
	if len(exact) > 1 {
		return nil, exact
	}
	sort.Slice(similar, func(i, j int) bool { return similar[i].label() < similar[j].label() })
	if len(similar) > 10 {
		similar = similar[:10]
	}
	return nil, similar
}

func destinationNotFoundError(channel string, candidates []chatDestination) error {
	if len(candidates) == 0 {
		return fmt.Errorf("nothing was sent: no channel named %q was found in the connected chat apps; ask the user for the exact channel name", channel)
	}
	labels := make([]string, 0, len(candidates))
	for _, c := range candidates {
		labels = append(labels, c.label())
	}
	return fmt.Errorf("nothing was sent: %q matches more than one channel or none exactly; ask the user which one they mean: %s", channel, strings.Join(labels, "; "))
}

// signatureNameMarkup is the markup a display name must not carry into the
// signature: it would turn the name into a link or break the italics. Replaced
// by spaces, not escaped, because Slack's markup has no escape character.
var signatureNameMarkup = strings.NewReplacer("[", " ", "]", " ", "(", " ", ")", " ", "<", " ", ">", " ", "|", " ", "_", " ", "*", " ", "`", " ", "~", " ")

// messageWithSignature adds who sent the message, and a link to the
// conversation it came from when there is one. The link is written the way the
// platform renders it: Slack and Google Chat take <url|text>, Teams markdown.
func messageWithSignature(message, displayName, conversationURL, platform string) string {
	signature := "_Sent via Nubi_"
	if name := strings.Join(strings.Fields(signatureNameMarkup.Replace(displayName)), " "); name != "" {
		signature = "_Sent by " + name + " via Nubi_"
	}
	if conversationURL != "" {
		if platform == "ms_teams" {
			signature += " · [conversation](" + conversationURL + ")"
		} else {
			signature += " · <" + conversationURL + "|conversation>"
		}
	}
	return message + "\n\n" + signature
}

// chatConversationURL is the app page of the conversation, or "" when the
// app's address is not an absolute http(s) URL.
func chatConversationURL(accountId, sessionId string) string {
	base := strings.TrimRight(config.Config.BaseUrl, "/")
	if accountId == "" || sessionId == "" || (!strings.HasPrefix(base, "http://") && !strings.HasPrefix(base, "https://")) {
		return ""
	}
	return base + "/ask-nudgebee?" + url.Values{"accountId": {accountId}, "session_id": {sessionId}}.Encode()
}

type chatSendResult struct {
	Platform string `json:"platform"`
	Status   string `json:"status"`
	Reason   string `json:"reason"`
}

func sendChatMessage(ctx *security.RequestContext, destination chatDestination, message, accountId string) ([]chatSendResult, error) {
	channel := map[string]any{"id": destination.ChannelID}
	if destination.TeamID != "" {
		channel["team_id"] = destination.TeamID
	}
	resp, err := common.HttpPost(strings.TrimRight(config.Config.NotificationServerUrl, "/")+"/api/messages/send",
		common.HttpWithHeaders(notificationServerHeaders(ctx)),
		common.HttpWithJsonBody(map[string]any{
			"kind":       "notification",
			"type":       "generic",
			"tenant_id":  ctx.GetSecurityContext().GetTenantId(),
			"source":     imMessageSource,
			"parameters": map[string]any{"message": message, "account_id": accountId},
			"channels":   map[string]any{destination.Platform: []map[string]any{channel}},
		}),
		common.HttpWithContext(ctx.GetContext()),
		common.HttpWithTimeout(30*time.Second),
	)
	if err != nil {
		return nil, fmt.Errorf("sendChatMessage: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("sendChatMessage: read body: %w", err)
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("sendChatMessage: the notification service answered %d", resp.StatusCode)
	}
	var results []chatSendResult
	if err := json.Unmarshal(body, &results); err != nil {
		return nil, fmt.Errorf("sendChatMessage: decode: %w", err)
	}
	return results, nil
}

// chatSendResponse says in plain words whether the message reached the channel.
func chatSendResponse(destination chatDestination, results []chatSendResult) core.NBToolResponse {
	for _, r := range results {
		if r.Status == "success" {
			data, _ := json.Marshal(map[string]any{"status": "sent", "destination": destination.label()})
			return core.NBToolResponse{Data: string(data), Type: core.NBToolResponseTypeJson, Status: core.NBToolResponseStatusSuccess}
		}
	}
	reason := "the notification service did not send it"
	for _, r := range results {
		if strings.TrimSpace(r.Reason) != "" {
			reason = r.Reason
			break
		}
	}
	return errNBLLMToolResponse(fmt.Errorf("the message was not sent to %s: %s", destination.label(), reason))
}
