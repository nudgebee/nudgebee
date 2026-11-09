package agents

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"nudgebee/llm/agents/core"
	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
)

// Where a new automation posts to Slack.
//
// The builder used to be told not to ask: it had no channel data, so any options it offered were
// invented names. It wrote {{ Configs.slack_channel }} instead, and on an account without that
// config the automation was saved INACTIVE and never ran. Now the question is asked, but the
// model supplies only the question. The user types the channel, and everything that must be true
// about the answer is settled here in code: the name is checked against the workspace's channels,
// suggestions come from that list, and the channel's id is what ends up in the saved automation.

// clarifyKindChatChannel marks a clarifying question that asks which Slack channel to post to.
const clarifyKindChatChannel = "chat_channel"

// chatChannelSuggestions is how many real channels are offered when the typed name is not found.
const chatChannelSuggestions = 8

// chatChannel is one channel of the tenant's Slack workspace.
type chatChannel struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// normalizeQuestionKind reads a question's kind as the model may have spelled it: any case, with
// spaces or hyphens for the underscore.
func normalizeQuestionKind(kind string) string {
	return strings.NewReplacer(" ", "_", "-", "_").Replace(strings.ToLower(strings.TrimSpace(kind)))
}

// normalizeChannelName is the form channel names are compared in: no leading '#', lower case.
func normalizeChannelName(name string) string {
	return strings.ToLower(strings.TrimPrefix(strings.TrimSpace(name), "#"))
}

// listSlackChannels asks the notification service for the channels of the tenant's Slack
// workspace. ok is false when the list could not be read; an answer is then accepted as typed.
func listSlackChannels(ctx *security.RequestContext) (channels []chatChannel, ok bool) {
	if config.Config.NotificationServerUrl == "" {
		return nil, false
	}
	// The headers go first: HttpWithHeaders replaces the header set, and the JSON body option
	// that follows adds its Content-Type to it.
	headers := map[string]string{}
	if config.Config.NotificationServerToken != "" {
		headers["X-ACTION-TOKEN"] = config.Config.NotificationServerToken
	}
	options := []common.HttpOption{
		common.HttpWithHeaders(headers),
		common.HttpWithJsonBody(map[string]any{
			"input":             map[string]any{"platform": "slack"},
			"session_variables": map[string]any{"tenant_id": ctx.GetSecurityContext().GetTenantId()},
		}),
		common.HttpWithContext(ctx.GetContext()),
		common.HttpWithTimeout(20 * time.Second),
	}

	resp, err := common.HttpPost(strings.TrimRight(config.Config.NotificationServerUrl, "/")+"/api/channels/list", options...)
	if err != nil {
		ctx.GetLogger().Warn("workflow_builder: could not list Slack channels", "error", err)
		return nil, false
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil || resp.StatusCode != http.StatusOK {
		ctx.GetLogger().Warn("workflow_builder: could not list Slack channels", "status", resp.StatusCode, "error", err)
		return nil, false
	}

	// The service answers {"data": [...]} — with an empty list when no workspace is connected —
	// and an object without "data" when it failed.
	var listed struct {
		Data *[]chatChannel `json:"data"`
	}
	if err := json.Unmarshal(body, &listed); err != nil || listed.Data == nil {
		ctx.GetLogger().Warn("workflow_builder: Slack channel list was not readable", "error", err)
		return nil, false
	}
	return *listed.Data, true
}

// matchChatChannel finds the channel an answer names, by id or by name.
func matchChatChannel(channels []chatChannel, answer string) (chatChannel, bool) {
	typed := strings.TrimSpace(answer)
	name := normalizeChannelName(answer)
	for _, channel := range channels {
		if channel.ID != "" && strings.EqualFold(channel.ID, typed) {
			return channel, true
		}
	}
	for _, channel := range channels {
		if channel.Name != "" && normalizeChannelName(channel.Name) == name {
			return channel, true
		}
	}
	return chatChannel{}, false
}

// suggestChatChannels returns up to limit real channels whose name contains the answer, or is
// contained in it, as "#name" labels in name order.
func suggestChatChannels(channels []chatChannel, answer string, limit int) []string {
	name := normalizeChannelName(answer)
	if name == "" {
		return nil
	}
	var labels []string
	for _, channel := range channels {
		candidate := normalizeChannelName(channel.Name)
		if candidate == "" {
			continue
		}
		if strings.Contains(candidate, name) || strings.Contains(name, candidate) {
			labels = append(labels, "#"+channel.Name)
		}
	}
	sort.Strings(labels)
	if len(labels) > limit {
		labels = labels[:limit]
	}
	return labels
}

// channelNameShape is what a Slack channel name or id looks like: one word of letters, digits,
// hyphens, underscores and dots, with an optional leading '#'.
var channelNameShape = regexp.MustCompile(`^#?[A-Za-z0-9][A-Za-z0-9._-]{0,79}$`)

// unverifiedChannelAnswer is what to record for an answer that could not be matched to a channel.
// One that reads as a channel name is kept as typed: the user named it, a private channel the app
// is not in is missing from the list, and a name is still a value the automation can run with.
// Anything else — a sentence, "whatever you think" — is not a channel, and is recorded as no
// answer, which leaves the builder where it was before the question was asked.
func unverifiedChannelAnswer(answer string) string {
	if channelNameShape.MatchString(strings.TrimSpace(answer)) {
		return strings.TrimSpace(answer)
	}
	return ""
}

// resolveChatChannelAnswer settles the user's answer to a chat-channel question. It returns the
// answer to record, or a question to put to the user again when the workspace has no channel of
// that name. Each question is asked again once; after that, and whenever the channel list cannot
// be read, the answer is handled by unverifiedChannelAnswer.
func (a *WorkflowBuilderAgent) resolveChatChannelAnswer(ctx *security.RequestContext, request core.NBAgentRequest, answer string) (string, *core.NBAgentResponse) {
	lister := a.chatChannelLister
	if lister == nil {
		lister = listSlackChannels
	}
	channels, ok := lister(ctx)
	if !ok || len(channels) == 0 {
		return unverifiedChannelAnswer(answer), nil
	}

	if channel, found := matchChatChannel(channels, answer); found {
		if a.state.ChatChannels == nil {
			a.state.ChatChannels = map[string]string{}
		}
		a.state.ChatChannels[normalizeChannelName(channel.Name)] = channel.ID
		return "#" + channel.Name, nil
	}

	// One retry per question, remembered by the question's position.
	if a.state.ChatChannelRetriedAt == a.state.ClarifyingIndex+1 {
		return unverifiedChannelAnswer(answer), nil
	}
	a.state.ChatChannelRetriedAt = a.state.ClarifyingIndex + 1

	retry := ClarifyingQuestion{
		Kind: clarifyKindChatChannel,
		Question: fmt.Sprintf("I couldn't find a Slack channel named %q that the Nudgebee app can see. "+
			"Type the channel name again — for a private channel, add the Nudgebee app to it first.", strings.TrimSpace(answer)),
		Options: append(suggestChatChannels(channels, answer, chatChannelSuggestions), "Skip"),
	}
	response, err := a.buildClarificationResponse(request, retry)
	if err != nil {
		return unverifiedChannelAnswer(answer), nil
	}
	return "", &response
}

// chatChannelParams are the task parameters that hold a Slack channel.
var chatChannelParams = []string{"channel", "im_channel", "channel_id"}

// channelInputRef matches a channel parameter that is nothing but a reference to an automation
// input, in either spelling: {{ Inputs.slack_channel }} or {{ Inputs['slack_channel'] }}.
var channelInputRef = regexp.MustCompile(`^\{\{\s*Inputs(?:\.([A-Za-z0-9_]+)|\[['"]([A-Za-z0-9_-]+)['"]\])\s*\}\}$`)

// rewriteChatChannels replaces a channel the user named with that channel's id: in every task
// parameter that holds a Slack channel, and in the default of an automation input that such a
// parameter refers to, which is where the channel lands when the plan makes it an input. Only
// names recorded from the user's own answers are touched: a template, an id, or any other name is
// left as written. It returns the number of values replaced.
func rewriteChatChannels(workflow map[string]interface{}, channels map[string]string) int {
	if len(channels) == 0 {
		return 0
	}
	definition, _ := workflow["definition"].(map[string]interface{})
	tasks, _ := definition["tasks"].([]interface{})
	replaced := 0
	channelInputs := map[string]bool{}
	walkTaskMaps(tasks, func(task map[string]interface{}) {
		params, _ := task["params"].(map[string]interface{})
		if provider, _ := params["provider"].(string); provider != "" && provider != "slack" {
			return
		}
		for _, key := range chatChannelParams {
			value, _ := params[key].(string)
			if value == "" {
				continue
			}
			if ref := channelInputRef.FindStringSubmatch(strings.TrimSpace(value)); ref != nil {
				channelInputs[ref[1]+ref[2]] = true
				continue
			}
			if strings.Contains(value, "{{") {
				continue
			}
			if id, ok := channels[normalizeChannelName(value)]; ok && id != "" && id != value {
				params[key] = id
				replaced++
			}
		}
	})

	inputs, _ := definition["inputs"].([]interface{})
	for _, raw := range inputs {
		input, _ := raw.(map[string]interface{})
		inputId, _ := input["id"].(string)
		value, _ := input["default"].(string)
		if !channelInputs[inputId] || value == "" || strings.Contains(value, "{{") {
			continue
		}
		if id, ok := channels[normalizeChannelName(value)]; ok && id != "" && id != value {
			input["default"] = id
			replaced++
		}
	}
	return replaced
}

// rewriteChatChannelsJSON is rewriteChatChannels for a definition held as JSON. Anything that
// cannot be read is returned unchanged.
func rewriteChatChannelsJSON(workflowJSON string, channels map[string]string) string {
	if len(channels) == 0 {
		return workflowJSON
	}
	var workflow map[string]interface{}
	if err := json.Unmarshal([]byte(workflowJSON), &workflow); err != nil {
		return workflowJSON
	}
	if rewriteChatChannels(workflow, channels) == 0 {
		return workflowJSON
	}
	rewritten, err := json.Marshal(workflow)
	if err != nil {
		return workflowJSON
	}
	return string(rewritten)
}

// keepChatChannelQuestions cuts questions to limit without dropping a chat-channel question: it
// is the one answer that decides whether the automation can run at all, and the model lists it
// after the others.
func keepChatChannelQuestions(questions []ClarifyingQuestion, limit int) []ClarifyingQuestion {
	if len(questions) <= limit {
		return questions
	}
	kept := append([]ClarifyingQuestion(nil), questions[:limit]...)
	for _, dropped := range questions[limit:] {
		if dropped.Kind != clarifyKindChatChannel {
			continue
		}
		// Give up the last question that is not itself about a channel.
		for i := len(kept) - 1; i >= 0; i-- {
			if kept[i].Kind != clarifyKindChatChannel {
				kept = append(append(kept[:i:i], kept[i+1:]...), dropped)
				break
			}
		}
	}
	return kept
}
