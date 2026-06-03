package notifications

import (
	"errors"
	"fmt"
	"nudgebee/runbook/internal/tasks/types"
	"nudgebee/runbook/services/notification"
)

// parseAddressList normalizes an address field from workflow params into a
// []string. Accepts string, []string, or []any (the JSON-decoded form). Empty
// or missing values yield (nil, nil); invalid types yield an error.
func parseAddressList(v any) ([]string, error) {
	switch r := v.(type) {
	case nil:
		return nil, nil
	case string:
		if r == "" {
			return nil, nil
		}
		return []string{r}, nil
	case []string:
		return r, nil
	case []any:
		out := make([]string, 0, len(r))
		for _, item := range r {
			if s, ok := item.(string); ok && s != "" {
				out = append(out, s)
			}
		}
		return out, nil
	default:
		return nil, errors.New("invalid address list type")
	}
}

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

	recipients, err := parseAddressList(params["recipients"])
	if err != nil {
		return nil, fmt.Errorf("invalid recipients: %w", err)
	}
	if len(recipients) == 0 {
		return nil, errors.New("recipients is required")
	}

	// cc and bcc are optional but must be the right type when provided.
	cc, err := parseAddressList(params["cc"])
	if err != nil {
		return nil, fmt.Errorf("invalid cc: %w", err)
	}
	bcc, err := parseAddressList(params["bcc"])
	if err != nil {
		return nil, fmt.Errorf("invalid bcc: %w", err)
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
		CC:         cc,
		BCC:        bcc,
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
				Title:       "To",
				Description: "Pick from Nudgebee users or type any email address and press Enter to add it.",
				Required:    true,
				Order:       1,
				OptionsSource: &types.OptionsSource{
					Type: "onboarded_users",
				},
			},
			"cc": {
				Type:        "array",
				Title:       "Cc",
				Description: "Optional carbon-copy recipients. Visible to all addressees.",
				Required:    false,
				Order:       2,
				OptionsSource: &types.OptionsSource{
					Type: "onboarded_users",
				},
			},
			"bcc": {
				Type:        "array",
				Title:       "Bcc",
				Description: "Optional blind carbon-copy recipients. Hidden from other addressees.",
				Required:    false,
				Order:       3,
				OptionsSource: &types.OptionsSource{
					Type: "onboarded_users",
				},
			},
			"subject": {
				Type:        "string",
				Description: "Email subject line. Use the variable picker to insert dynamic values like date, time, or workflow name.",
				Required:    true,
				Order:       4,
			},
			"body_format": {
				Type:        "string",
				Description: "How the body content should be rendered. Use 'markdown' when piping LLM output so headings, lists and code blocks render correctly.",
				Required:    false,
				Order:       5,
				Default:     "text",
				Options:     []string{"text", "markdown", "html"},
			},
			"body": {
				Type:        "string",
				Description: "Email body content. Honors the selected body format.",
				Required:    true,
				Order:       6,
				SubType:     "textarea",
			},
			"reply_to": {
				Type:        "string",
				Description: "Reply-to email address",
				Required:    false,
				Order:       7,
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
