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

// Who a new automation sends a Slack direct message to.
//
// notifications.dm needs the person's Slack member id (U012AB3CD). The builder has no user data,
// so it wrote the name from the request ("Utsav Lanjewar"), the automation was reported as built,
// and every run failed with "User not found". The clarifier now names the person in a typed
// question, and code settles it against the workspace's members: exactly one match is recorded
// without asking; no match, several, or a list that cannot be read is asked about. The member id
// is what ends up in the saved automation, and validate refuses a DM step that names a person.

// clarifyKindChatUser marks a clarifying question that asks who gets a Slack direct message.
const clarifyKindChatUser = "chat_user"

// chatUserSuggestions is how many real members are offered when the name is not settled.
const chatUserSuggestions = 8

// chatUserConfigPlaceholder is what a DM step uses when nobody was named: it saves the automation
// INACTIVE until the config is set, rather than running with a guess.
const chatUserConfigPlaceholder = "{{ Configs.slack_user_id }}"

// chatUser is one member of the tenant's Slack workspace.
type chatUser struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	RealName    string `json:"real_name"`
	DisplayName string `json:"display_name"`
	Email       string `json:"email"`
}

// label is how a member is offered as an option: the name people know them by, and the
// workspace-unique username that tells two people of the same name apart.
func (u chatUser) label() string {
	shown := u.RealName
	if shown == "" {
		shown = u.DisplayName
	}
	if shown == "" || strings.EqualFold(shown, u.Name) {
		return "@" + u.Name
	}
	if u.Name == "" {
		return shown
	}
	return fmt.Sprintf("%s (@%s)", shown, u.Name)
}

// normalizeUserName is the form person names are compared in: no leading '@', lower case.
func normalizeUserName(name string) string {
	return strings.ToLower(strings.TrimPrefix(strings.TrimSpace(name), "@"))
}

// slackMemberIdShape is what a Slack member id looks like: U or W, then upper-case letters and
// digits. looksLikeSlackMemberId also wants a digit, so "UTSAV" does not pass for an id.
var slackMemberIdShape = regexp.MustCompile(`^[UW][A-Z0-9]{6,}$`)

func looksLikeSlackMemberId(value string) bool {
	value = strings.TrimSpace(value)
	return slackMemberIdShape.MatchString(value) && strings.ContainsAny(value, "0123456789")
}

// listSlackUsers asks the notification service for the members of the tenant's Slack workspace.
// ok is false when the list could not be read.
func listSlackUsers(ctx *security.RequestContext) (users []chatUser, ok bool) {
	if config.Config.NotificationServerUrl == "" {
		return nil, false
	}
	// The headers go first: HttpWithHeaders replaces the header set.
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

	resp, err := common.HttpPost(strings.TrimRight(config.Config.NotificationServerUrl, "/")+"/api/users/list", options...)
	if err != nil {
		ctx.GetLogger().Warn("workflow_builder: could not list Slack users", "error", err)
		return nil, false
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil || resp.StatusCode != http.StatusOK {
		ctx.GetLogger().Warn("workflow_builder: could not list Slack users", "status", resp.StatusCode, "error", err)
		return nil, false
	}

	// {"data": [...]}, empty when no workspace is connected; an object without "data" on failure.
	var listed struct {
		Data *[]chatUser `json:"data"`
	}
	if err := json.Unmarshal(body, &listed); err != nil || listed.Data == nil {
		ctx.GetLogger().Warn("workflow_builder: Slack user list was not readable", "error", err)
		return nil, false
	}
	return *listed.Data, true
}

// matchChatUsers returns every member the text names exactly, case-insensitively: by member id,
// display name, real name, username, email, or the label it was offered under.
func matchChatUsers(users []chatUser, text string) []chatUser {
	typed := normalizeUserName(text)
	if typed == "" {
		return nil
	}
	var matched []chatUser
	for _, user := range users {
		for _, field := range []string{user.ID, user.DisplayName, user.RealName, user.Name, user.Email, user.label()} {
			if field != "" && normalizeUserName(field) == typed {
				matched = append(matched, user)
				break
			}
		}
	}
	return matched
}

// suggestChatUsers returns up to limit labels of members whose names contain the text, or whose
// names it contains, in label order.
func suggestChatUsers(users []chatUser, text string, limit int) []string {
	typed := normalizeUserName(text)
	if typed == "" {
		return nil
	}
	var labels []string
	for _, user := range users {
		for _, field := range []string{user.DisplayName, user.RealName, user.Name} {
			candidate := normalizeUserName(field)
			if candidate != "" && (strings.Contains(candidate, typed) || strings.Contains(typed, candidate)) {
				labels = append(labels, user.label())
				break
			}
		}
	}
	sort.Strings(labels)
	if len(labels) > limit {
		labels = labels[:limit]
	}
	return labels
}

func userLabels(users []chatUser, limit int) []string {
	labels := make([]string, 0, len(users))
	for _, user := range users {
		labels = append(labels, user.label())
	}
	sort.Strings(labels)
	if len(labels) > limit {
		labels = labels[:limit]
	}
	return labels
}

func (a *WorkflowBuilderAgent) slackUsers(ctx *security.RequestContext) ([]chatUser, bool) {
	if a.chatUserLister != nil {
		return a.chatUserLister(ctx)
	}
	return listSlackUsers(ctx)
}

// recordChatUser remembers the member for the build: every name it was called by maps to its id,
// and the answer recorded for the plan carries the id.
func (a *WorkflowBuilderAgent) recordChatUser(user chatUser, namedAs ...string) string {
	if a.state.ChatUsers == nil {
		a.state.ChatUsers = map[string]string{}
	}
	for _, name := range append(namedAs, user.DisplayName, user.RealName, user.Name) {
		if key := normalizeUserName(name); key != "" && key != normalizeUserName(user.ID) {
			a.state.ChatUsers[key] = user.ID
		}
	}
	return fmt.Sprintf("%s — Slack member ID %s (use this ID as user_id)", user.label(), user.ID)
}

// memberIdAnswer is what to record for an answer that could not be checked against the
// workspace: a member id is kept, anything else is no answer, so the DM uses the placeholder.
func memberIdAnswer(answer string) string {
	if looksLikeSlackMemberId(answer) {
		return fmt.Sprintf("Slack member ID %s (use this ID as user_id)", strings.TrimSpace(answer))
	}
	return ""
}

// settleChatUserQuestions looks up the person each chat-user question names, before anything is
// asked. Exactly one match is recorded as the answer and the question is not shown; otherwise the
// question is reworded to say what was found, with the candidates as options.
func (a *WorkflowBuilderAgent) settleChatUserQuestions(ctx *security.RequestContext) {
	var users []chatUser
	listed, looked := false, false
	for i, question := range a.state.ClarifyingQuestions {
		if question.Kind != clarifyKindChatUser {
			continue
		}
		if !looked {
			users, listed = a.slackUsers(ctx)
			listed = listed && len(users) > 0
			looked = true
		}
		recipient := strings.TrimSpace(question.Recipient)
		switch {
		case !listed:
			// No list means no check: ask for the id rather than trust a name.
			who := "the person"
			if recipient != "" {
				who = fmt.Sprintf("%q", recipient)
			}
			a.state.ClarifyingQuestions[i].Question = fmt.Sprintf("I couldn't look up %s in your Slack workspace. "+
				"Type their Slack member ID (for example U012AB3CD) — it is in their Slack profile under \"Copy member ID\".", who)
			a.state.ClarifyingQuestions[i].Options = []string{"Skip"}
		case recipient == "":
			// Nobody named: the model's question stands, and the typed answer is checked.
		default:
			matched := matchChatUsers(users, recipient)
			switch len(matched) {
			case 1:
				a.state.ClarifyingAnswers[i] = a.recordChatUser(matched[0], recipient)
			case 0:
				a.state.ClarifyingQuestions[i].Question = fmt.Sprintf("I couldn't find a Slack user named %q. Who should get the direct message?", recipient)
				a.state.ClarifyingQuestions[i].Options = append(suggestChatUsers(users, recipient, chatUserSuggestions), "Skip")
			default:
				a.state.ClarifyingQuestions[i].Question = fmt.Sprintf("More than one Slack user matches %q. Which one should get the direct message?", recipient)
				a.state.ClarifyingQuestions[i].Options = append(userLabels(matched, chatUserSuggestions), "Skip")
			}
		}
	}
}

// resolveChatUserAnswer settles the user's answer to a chat-user question. It returns the answer
// to record, or a question to put again when the answer does not name exactly one member. Each
// question is asked again once; after that, and when the list cannot be read, only a member id
// is kept.
func (a *WorkflowBuilderAgent) resolveChatUserAnswer(ctx *security.RequestContext, request core.NBAgentRequest, answer string) (string, *core.NBAgentResponse) {
	users, ok := a.slackUsers(ctx)
	if !ok || len(users) == 0 {
		return memberIdAnswer(answer), nil
	}

	matched := matchChatUsers(users, answer)
	if len(matched) == 1 {
		return a.recordChatUser(matched[0], answer, a.state.ClarifyingQuestions[a.state.ClarifyingIndex].Recipient), nil
	}

	if a.state.ChatUserRetriedAt == a.state.ClarifyingIndex+1 {
		return memberIdAnswer(answer), nil
	}
	a.state.ChatUserRetriedAt = a.state.ClarifyingIndex + 1

	retry := ClarifyingQuestion{Kind: clarifyKindChatUser}
	if len(matched) == 0 {
		retry.Question = fmt.Sprintf("I couldn't find a Slack user named %q. Who should get the direct message?", strings.TrimSpace(answer))
		retry.Options = append(suggestChatUsers(users, answer, chatUserSuggestions), "Skip")
	} else {
		retry.Question = fmt.Sprintf("More than one Slack user matches %q. Which one should get the direct message?", strings.TrimSpace(answer))
		retry.Options = append(userLabels(matched, chatUserSuggestions), "Skip")
	}
	response, err := a.buildClarificationResponse(request, retry)
	if err != nil {
		return memberIdAnswer(answer), nil
	}
	return "", &response
}

// visitSlackDmSteps calls visit with the params of every Slack DM step, and returns the automation's
// inputs by id. A DM step with no provider is Slack's: it is the only one the task offers.
func visitSlackDmSteps(workflow map[string]interface{}, visit func(task, params map[string]interface{})) map[string]map[string]interface{} {
	definition, _ := workflow["definition"].(map[string]interface{})
	tasks, _ := definition["tasks"].([]interface{})
	walkTaskMaps(tasks, func(task map[string]interface{}) {
		if taskType, _ := task["type"].(string); taskType != "notifications.dm" {
			return
		}
		params, _ := task["params"].(map[string]interface{})
		if provider, _ := params["provider"].(string); provider != "" && provider != "slack" {
			return
		}
		if params != nil {
			visit(task, params)
		}
	})
	inputs := map[string]map[string]interface{}{}
	rawInputs, _ := definition["inputs"].([]interface{})
	for _, raw := range rawInputs {
		if input, ok := raw.(map[string]interface{}); ok {
			if id, _ := input["id"].(string); id != "" {
				inputs[id] = input
			}
		}
	}
	return inputs
}

// rewriteChatUsers puts the member id in place of a person's name in every Slack DM step's
// user_id, and in the default of an input that user_id only refers to. Only names in users are
// touched; any other template is left as written. It returns the number of values replaced.
func rewriteChatUsers(workflow map[string]interface{}, users map[string]string) int {
	if len(users) == 0 {
		return 0
	}
	replaced := 0
	referenced := map[string]bool{}
	inputs := visitSlackDmSteps(workflow, func(_, params map[string]interface{}) {
		value, _ := params["user_id"].(string)
		if ref := channelInputRef.FindStringSubmatch(strings.TrimSpace(value)); ref != nil {
			referenced[ref[1]+ref[2]] = true
			return
		}
		if value == "" || strings.Contains(value, "{{") {
			return
		}
		if id, ok := users[normalizeUserName(value)]; ok && id != value {
			params["user_id"] = id
			replaced++
		}
	})
	for id := range referenced {
		value, _ := inputs[id]["default"].(string)
		if value == "" || strings.Contains(value, "{{") {
			continue
		}
		if member, ok := users[normalizeUserName(value)]; ok && member != value {
			inputs[id]["default"] = member
			replaced++
		}
	}
	return replaced
}

// rewriteChatUsersJSON is rewriteChatUsers for a definition held as JSON. Anything that cannot be
// read is returned unchanged.
func rewriteChatUsersJSON(workflowJSON string, users map[string]string) string {
	if len(users) == 0 {
		return workflowJSON
	}
	var workflow map[string]interface{}
	if err := json.Unmarshal([]byte(workflowJSON), &workflow); err != nil {
		return workflowJSON
	}
	if rewriteChatUsers(workflow, users) == 0 {
		return workflowJSON
	}
	rewritten, err := json.Marshal(workflow)
	if err != nil {
		return workflowJSON
	}
	return string(rewritten)
}

// templateDefaultLiteral finds a quoted literal handed to a template's default filter.
var templateDefaultLiteral = regexp.MustCompile(`default\(\s*['"]([^'"]*)['"]`)

// chatUserIssue is a Slack DM step whose recipient is a name, not a member id.
type chatUserIssue struct {
	TaskID string
	Value  string
}

// lintChatUserIds lists the Slack DM steps that would send to a name: a plain user_id, the
// default of the input it refers to, or a template's default literal that is not a member id.
func lintChatUserIds(workflow map[string]interface{}) []chatUserIssue {
	var issues []chatUserIssue
	refs := map[string][]string{}
	inputs := visitSlackDmSteps(workflow, func(task, params map[string]interface{}) {
		taskID, _ := task["id"].(string)
		value, _ := params["user_id"].(string)
		value = strings.TrimSpace(value)
		switch {
		case value == "":
		case channelInputRef.MatchString(value):
			ref := channelInputRef.FindStringSubmatch(value)
			refs[ref[1]+ref[2]] = append(refs[ref[1]+ref[2]], taskID)
		case strings.Contains(value, "{{"):
			for _, literal := range templateDefaultLiteral.FindAllStringSubmatch(value, -1) {
				if literal[1] != "" && !looksLikeSlackMemberId(literal[1]) {
					issues = append(issues, chatUserIssue{TaskID: taskID, Value: literal[1]})
				}
			}
		case !looksLikeSlackMemberId(value):
			issues = append(issues, chatUserIssue{TaskID: taskID, Value: value})
		}
	})
	for id, taskIDs := range refs {
		value, _ := inputs[id]["default"].(string)
		if value == "" || strings.Contains(value, "{{") || looksLikeSlackMemberId(value) {
			continue
		}
		for _, taskID := range taskIDs {
			issues = append(issues, chatUserIssue{TaskID: taskID, Value: value})
		}
	}
	sort.Slice(issues, func(i, j int) bool { return issues[i].TaskID < issues[j].TaskID })
	return issues
}

// settleChatUserIds puts member ids in place of names in the working automation's Slack DM
// steps: names from the clarifying answers first, then any name that matches exactly one member.
// It returns the steps that still name a person.
func (a *WorkflowBuilderAgent) settleChatUserIds(ctx *security.RequestContext) []chatUserIssue {
	rewriteChatUsers(a.state.WorkingWorkflow, a.state.ChatUsers)
	issues := lintChatUserIds(a.state.WorkingWorkflow)
	if len(issues) == 0 {
		return nil
	}
	users, ok := a.slackUsers(ctx)
	if !ok || len(users) == 0 {
		return issues
	}
	for _, issue := range issues {
		if matched := matchChatUsers(users, issue.Value); len(matched) == 1 {
			a.recordChatUser(matched[0], issue.Value)
		}
	}
	rewriteChatUsers(a.state.WorkingWorkflow, a.state.ChatUsers)
	return lintChatUserIds(a.state.WorkingWorkflow)
}

// chatUserLintMessage is the validate failure for DM steps that still name a person.
func chatUserLintMessage(issues []chatUserIssue) string {
	lines := make([]string, 0, len(issues))
	for _, issue := range issues {
		lines = append(lines, fmt.Sprintf("- task %q: user_id %q", issue.TaskID, issue.Value))
	}
	return fmt.Sprintf("Validation FAILED: these Slack direct-message steps name a person instead of their Slack member ID, "+
		"and would fail at run time with \"User not found\":\n%s\n\nSet user_id to the member ID from CLARIFICATION ANSWERS "+
		"(it looks like U012AB3CD). If none was given, use %s — never a name, and never a name as a template default. "+
		"Fix it with modify_task, then call validate again.", strings.Join(lines, "\n"), chatUserConfigPlaceholder)
}
