package core

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"nudgebee/runbook/config"
	"nudgebee/runbook/internal/tasks/types"
	"nudgebee/runbook/services/notification"
	"strings"
	"time"

	"go.temporal.io/sdk/activity"
)

// ApprovalTask implements a task that pauses workflow execution until an external approval is received.
type ApprovalTask struct{}

// GetName returns the unique name of the task.
func (t *ApprovalTask) GetName() string {
	return "core.approval"
}

// GetDescription returns a brief description of the task.
func (t *ApprovalTask) GetDescription() string {
	return "Pause and wait for a team member to approve before continuing."
}

// GetDisplayName returns a human-readable name for the task.
func (t *ApprovalTask) GetDisplayName() string {
	return "Approval"
}

// Execute runs the core logic of the task.
func (t *ApprovalTask) Execute(taskCtx types.TaskContext, params map[string]any) (any, error) {
	taskCtx.GetLogger().Debug("Executing ApprovalTask", "params", params)

	if !activity.IsActivity(taskCtx.GetContext()) {
		return nil, fmt.Errorf("core.approval task is not allowed to execute directly. It must be part of a workflow")
	}

	// Get activity's identity to signal the workflow
	activityInfo := activity.GetInfo(taskCtx.GetContext())
	taskToken := activityInfo.TaskToken

	// Create a compound token that includes metadata for validation
	compoundToken := fmt.Sprintf("%s:%s:%s:%s:%s",
		hex.EncodeToString(taskToken),
		taskCtx.GetAccountID(),
		activityInfo.WorkflowExecution.ID,
		taskCtx.GetWorkflowRunID(),
		activityInfo.ActivityID)

	// Signal the workflow with the approval information
	err := taskCtx.GetTemporalClient().SignalWorkflow(taskCtx.GetContext(), activityInfo.WorkflowExecution.ID, taskCtx.GetWorkflowRunID(), "approval-token", map[string]string{
		"task_id":    activityInfo.ActivityID,
		"task_token": compoundToken,
	})
	if err != nil {
		return nil, err
	}

	approvalOptions := []string{"approve", "reject"}
	if opts, ok := params["approval_options"].([]any); ok {
		approvalOptions = make([]string, len(opts))
		for i, v := range opts {
			approvalOptions[i] = fmt.Sprint(v)
		}
	}

	// No default case on purpose: "in_app" (and an absent value, for workflows
	// authored before approval_type existed) sends no notification and simply
	// waits for the approval signal raised from the execution view.
	switch params["approval_type"] {
	case "instant_message":
		imProvider, ok := params["im_provider"].(string)
		if !ok || imProvider == "" {
			return nil, fmt.Errorf("im_provider is required when approval_type is instant_message")
		}

		imChannel, ok := params["im_channel"].(string)
		if !ok || imChannel == "" {
			return nil, fmt.Errorf("im_channel is required when approval_type is instant_message")
		}

		requestContext := taskCtx.GetNewRequestContext()
		request := notification.SendImNotificationRequest{
			Body:      fmt.Sprint(params["message"]),
			Channel:   imChannel,
			AccountID: taskCtx.GetAccountID(),
			Platform:  imProvider,
			Parameters: map[string]any{
				"message":          params["message"],
				"approval_token":   compoundToken,
				"approval_options": approvalOptions,
				"workflow_name":    taskCtx.GetWorkflowName(),
				"run_id":           taskCtx.GetWorkflowRunID(),
				"requested_at":     time.Now().Unix(),
			},
		}

		if teamID, ok := params["im_team_id"].(string); ok && teamID != "" {
			request.TeamId = teamID
		}

		// Optional: post the approval prompt as a reply in an existing thread —
		// typically the message a preceding notifications.im task sent, via
		// {{ Tasks['<id>'].output.message_id }}. Without this the prompt always
		// lands top-level, detached from the context that justifies it.
		// Note the trade-off: Slack collapses thread replies, so a threaded
		// prompt is less visible than a top-level one. Leave unset for channels
		// where the approval must not be missed.
		if thread, ok := params["message_thread_id"].(string); ok && thread != "" {
			request.ThreadId = thread
		}

		resp, err := notification.SendNotification(requestContext, request)
		if err != nil {
			taskCtx.GetLogger().Error("Failed to send IM notification for approval", "error", err)
			// We don't fail the task here because the primary purpose is to wait for approval,
			// and the signal might still come through other means or the user might retry the notification.
		} else if resp.MessageTs != "" {
			// Stash the originating IM coordinates as activity heartbeat data so
			// CompleteApprovalTask can surface them in the task output for #29110
			// — workflow authors then thread downstream notifications.im replies
			// via {{ tasks.<id>.im_message_id }}.
			activity.RecordHeartbeat(taskCtx.GetContext(), map[string]string{
				"im_provider":   resp.Platform,
				"im_channel":    resp.ChannelId,
				"im_message_id": resp.MessageTs,
				"im_team_id":    resp.TeamId,
			})
		}

	case "email":
		recipients, err := extractEmailRecipients(params["email_recipients"])
		if err != nil {
			return nil, err
		}

		if config.Config.ApprovalSigningKey == "" {
			taskCtx.GetLogger().Warn("APPROVAL_SIGNING_KEY is not set; approval links will fail signature verification on the app side")
		}

		subject := fmt.Sprint(params["message"])
		if s, ok := params["subject"].(string); ok && s != "" {
			subject = s
		}

		requestContext := taskCtx.GetNewRequestContext()
		for _, recipient := range recipients {
			actions := make([]map[string]any, 0, len(approvalOptions))
			for i, opt := range approvalOptions {
				actions = append(actions, map[string]any{
					"label":   titleCaseLabel(opt),
					"url":     buildApprovalURL(config.Config.BaseUrl, compoundToken, opt, recipient, config.Config.ApprovalSigningKey),
					"primary": i == 0,
				})
			}

			req := notification.SendEmailRequest{
				Recipients: []string{recipient},
				Subject:    subject,
				Template:   "approval_request",
				TemplateParams: map[string]any{
					"message":         params["message"],
					"workflow_name":   taskCtx.GetWorkflowName(),
					"run_id":          taskCtx.GetWorkflowRunID(),
					"recipient_email": recipient,
					"actions":         actions,
				},
			}

			if _, err := notification.SendEmail(requestContext, req); err != nil {
				taskCtx.GetLogger().Error("Failed to send approval email", "recipient", recipient, "error", err)
			}
		}
	}

	// Return pending error to pause the activity until the signal is received
	return nil, activity.ErrResultPending
}

// extractEmailRecipients normalises the schema-provided recipients field into
// a []string. Accepts []any (from JSON arrays), []string, or a comma-separated
// string (convenience for expression mode).
func extractEmailRecipients(raw any) ([]string, error) {
	var out []string
	switch v := raw.(type) {
	case []any:
		for _, r := range v {
			if s, ok := r.(string); ok {
				if s = strings.TrimSpace(s); s != "" {
					out = append(out, s)
				}
			}
		}
	case []string:
		for _, s := range v {
			if s = strings.TrimSpace(s); s != "" {
				out = append(out, s)
			}
		}
	case string:
		for _, s := range strings.Split(v, ",") {
			if s = strings.TrimSpace(s); s != "" {
				out = append(out, s)
			}
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("email_recipients is required when approval_type is email")
	}
	return out, nil
}

// approvalSignature computes an HMAC-SHA256 over "token|status|approver" and
// returns it in the "v0=<hex>" format (matches the shared format used by the
// app's verifier and notifications-server/utils/action_requests.py).
func approvalSignature(token, status, approver, signingKey string) string {
	mac := hmac.New(sha256.New, []byte(signingKey))
	mac.Write([]byte(token + "|" + status + "|" + approver))
	return "v0=" + hex.EncodeToString(mac.Sum(nil))
}

// buildApprovalURL returns the one-click approval link embedded in the email.
// It points at the public app (Next.js) — the app server-side verifies the
// HMAC and proxies the decision to runbook-server. runbook-server itself must
// NOT be exposed publicly.
//
// URL shape: {appBaseURL}/workflow/approvals/{token}?status=...&approver=...&sig=...
func buildApprovalURL(appBaseURL, token, status, approver, signingKey string) string {
	sig := approvalSignature(token, status, approver, signingKey)
	q := url.Values{}
	q.Set("status", status)
	q.Set("approver", approver)
	q.Set("sig", sig)
	return fmt.Sprintf("%s/workflow/approvals/%s?%s",
		strings.TrimRight(appBaseURL, "/"),
		url.PathEscape(token),
		q.Encode())
}

// titleCaseLabel converts an approval_options value ("approve", "needs-info",
// "approve_with_notes") into a display label ("Approve", "Needs info",
// "Approve with notes").
func titleCaseLabel(s string) string {
	if s == "" {
		return s
	}
	replaced := strings.ReplaceAll(strings.ReplaceAll(s, "-", " "), "_", " ")
	runes := []rune(replaced)
	runes[0] = []rune(strings.ToUpper(string(runes[0])))[0]
	return string(runes)
}

// InputSchema returns the schema for the task's expected parameters.
func (t *ApprovalTask) InputSchema() *types.Schema {
	return &types.Schema{
		Properties: map[string]types.Property{
			"message": {
				Type:        "string",
				Description: "The message to display to the user for approval.",
				Required:    true,
				Order:       1,
			},
			"approval_type": {
				Type: "string",
				// "in_app" is the no-notification mode: the run pauses and is
				// approved from the execution view's approval prompt, which is
				// available for every pending approval regardless of this field.
				// It is the default because #32013 made the field required
				// without one, which left every bundled template carrying an
				// approval step (21 of them, none able to know a tenant's Slack
				// channel or approver emails) invalid on load.
				//
				// Required stays false deliberately. Schema.Validate treats a
				// missing value as missing even when the property declares a
				// Default, so requiring it 400s the save of any workflow whose
				// approval task the author never opened — the builder only
				// commits schema defaults into params on task selection. The
				// default, not the flag, is what stops the blank-and-silent
				// state #32013 reported: picking instant_message or email still
				// requires its channel or recipients via RequiredWhen.
				Description: "Where to send the approval request. \"in_app\" sends nothing and waits for an approval from the execution view.",
				Options:     []string{"in_app", "instant_message", "email"},
				Default:     "in_app",
				Required:    false,
				Order:       2,
			},
			"im_provider": {
				Type:         "string",
				Description:  "IM Provider (required if approval_type is instant_message)",
				Options:      []string{"slack"},
				Required:     false,
				Order:        3,
				VisibleWhen:  &types.VisibleWhen{Field: "approval_type", Value: []string{"instant_message"}},
				RequiredWhen: &types.RequiredWhen{Field: "approval_type", Value: []string{"instant_message"}},
			},
			"im_channel": {
				Type:         "string",
				Description:  "IM Channel ID (required if approval_type is instant_message)",
				Required:     false,
				Order:        4,
				VisibleWhen:  &types.VisibleWhen{Field: "approval_type", Value: []string{"instant_message"}},
				RequiredWhen: &types.RequiredWhen{Field: "approval_type", Value: []string{"instant_message"}},
			},
			"im_team_id": {
				Type:        "string",
				Description: "IM Team ID (optional, applicable for ms_teams)",
				Required:    false,
				Order:       5,
				VisibleWhen: &types.VisibleWhen{Field: "approval_type", Value: []string{"instant_message"}},
			},
			"email_recipients": {
				Type:         types.PropertyTypeArray,
				Description:  "Pick from Nudgebee users or type any email address and press Enter to add it.",
				Required:     false,
				Order:        6,
				VisibleWhen:  &types.VisibleWhen{Field: "approval_type", Value: []string{"email"}},
				RequiredWhen: &types.RequiredWhen{Field: "approval_type", Value: []string{"email"}},
				OptionsSource: &types.OptionsSource{
					Type: "onboarded_users",
				},
			},
			"approval_options": {
				Type:        types.PropertyTypeArray,
				Description: "Approval options",
				Required:    false,
				Default:     []any{"approve", "reject"},
				Order:       7,
			},
			"message_thread_id": {
				Type:        "string",
				Description: "Optional IM thread to post the approval prompt into, e.g. {{ Tasks['notify'].output.message_id }} from a preceding Notifications IM task. Leave empty to post top-level — Slack collapses thread replies, so a threaded prompt is easier to miss.",
				Required:    false,
				Order:       8,
				VisibleWhen: &types.VisibleWhen{Field: "approval_type", Value: []string{"instant_message"}},
			},
		},
	}
}

// OutputSchema returns the schema for the task's output.
func (t *ApprovalTask) OutputSchema() *types.Schema {
	return &types.Schema{
		Properties: map[string]types.Property{
			"status": {
				Type: "string",
				// The value is whichever `approval_options` entry the approver
				// chose — by default "approve" / "reject", NOT "approved" /
				// "rejected". This previously read "(approved/rejected)", which
				// led workflow authors to write `if status == 'approved'`
				// conditions that silently never fire: the approval succeeds,
				// the workflow completes, and the gated task is skipped with no
				// error anywhere. Custom approval_options come through verbatim.
				Description: "The approval_options value the approver chose — \"approve\" or \"reject\" by default, or your custom option verbatim. Compare against your approval_options, not against \"approved\"/\"rejected\".",
				Required:    true,
			},
			"approver": {
				Type: "string",
				// Populated from the caller's security context (GetUserId), so
				// it is empty when the approval arrives without a user identity
				// — notably the Slack button path, which completes via a system
				// context. The signed `approver` in the approval URL is not
				// forwarded by handleCompleteWorkflowApproval today. Treat as
				// best-effort and always template it with a default.
				Description: "Approver identifier. Best-effort: empty when the approval arrives without a user identity (e.g. the Slack button path).",
				Required:    false,
			},
			"comments": {
				Type:        "string",
				Description: "Approval Comments.",
				Required:    false,
			},
			"im_provider": {
				Type:        "string",
				Description: "Originating IM provider (set only when approval_type=instant_message).",
				Required:    false,
			},
			"im_channel": {
				Type:        "string",
				Description: "IM channel where the approval was sent (set only when approval_type=instant_message).",
				Required:    false,
			},
			"im_message_id": {
				Type:        "string",
				Description: "Original IM message ID. Pipe to notifications.im → message_thread_id to reply in the same thread.",
				Required:    false,
			},
			"im_team_id": {
				Type:        "string",
				Description: "IM team ID (set only when approval_type=instant_message).",
				Required:    false,
			},
		},
	}
}
