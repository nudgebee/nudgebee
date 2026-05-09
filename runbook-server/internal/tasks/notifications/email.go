package notifications

import (
	"errors"
	"nudgebee/runbook/internal/tasks/types"
	"nudgebee/runbook/services/notification"
)

// EmailTask defines a task for sending emails.
type EmailTask struct{}

func (t *EmailTask) GetName() string {
	return "notifications.email"
}

// GetDescription returns a brief description of the task.
func (t *EmailTask) GetDescription() string {
	return "Sends an email to one or more recipients."
}

// GetDisplayName returns a human-readable name for the task.
func (t *EmailTask) GetDisplayName() string {
	return "Send Email"
}

func (t *EmailTask) Execute(taskCtx types.TaskContext, params map[string]any) (any, error) {
	taskCtx.GetLogger().Debug("Executing Email Task", "params", params)

	// Get recipients - can be string or array
	var recipients []string
	switch r := params["recipients"].(type) {
	case string:
		if r == "" {
			return nil, errors.New("recipients is required")
		}
		recipients = []string{r}
	case []any:
		for _, v := range r {
			if s, ok := v.(string); ok && s != "" {
				recipients = append(recipients, s)
			}
		}
		if len(recipients) == 0 {
			return nil, errors.New("recipients is required")
		}
	case []string:
		recipients = r
		if len(recipients) == 0 {
			return nil, errors.New("recipients is required")
		}
	default:
		return nil, errors.New("recipients is required")
	}

	subject, ok := params["subject"].(string)
	if !ok || subject == "" {
		return nil, errors.New("subject is required")
	}

	body, ok := params["body"].(string)
	if !ok || body == "" {
		return nil, errors.New("body is required")
	}

	requestContext := taskCtx.GetNewRequestContext()
	request := notification.SendEmailRequest{
		Recipients: recipients,
		Subject:    subject,
		Body:       body,
	}

	if bodyFormat, ok := params["body_format"].(string); ok && bodyFormat != "" {
		request.BodyFormat = bodyFormat
	}

	// Handle reply_to
	if replyTo, ok := params["reply_to"].(string); ok {
		request.ReplyTo = replyTo
	}

	resp, err := notification.SendEmail(requestContext, request)
	if err != nil {
		return map[string]any{
			"success": false,
			"error":   err.Error(),
		}, err
	}

	return map[string]any{
		"success": resp.Success,
		"sent_to": resp.SentTo,
	}, nil
}

func (t *EmailTask) InputSchema() *types.Schema {
	return &types.Schema{
		Properties: map[string]types.Property{
			"recipients": {
				Type:        "array",
				Description: "Pick from Nudgebee users or type any email address and press Enter to add it.",
				Required:    true,
				Order:       1,
				OptionsSource: &types.OptionsSource{
					Type: "onboarded_users",
				},
			},
			"subject": {
				Type:        "string",
				Description: "Email subject line. Use the variable picker to insert dynamic values like date, time, or workflow name.",
				Required:    true,
				Order:       2,
			},
			"body_format": {
				Type:        "string",
				Description: "How the body content should be rendered. Use 'markdown' when piping LLM output so headings, lists and code blocks render correctly.",
				Required:    false,
				Order:       3,
				Default:     "text",
				Options:     []string{"text", "markdown", "html"},
			},
			"body": {
				Type:        "string",
				Description: "Email body content. Honors the selected body format.",
				Required:    true,
				Order:       4,
				SubType:     "textarea",
			},
			"reply_to": {
				Type:        "string",
				Description: "Reply-to email address",
				Required:    false,
				Order:       5,
			},
		},
	}
}

// OutputSchema returns the schema for the task's output.
func (t *EmailTask) OutputSchema() *types.Schema {
	return &types.Schema{
		Properties: map[string]types.Property{
			"success": {
				Type:        "boolean",
				Description: "Whether the email was sent successfully.",
				Required:    true,
			},
			"sent_to": {
				Type:        "array",
				Description: "List of recipients the email was sent to.",
				Required:    false,
			},
		},
	}
}
