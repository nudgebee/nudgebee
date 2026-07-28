package watch

import (
	"context"
	"fmt"
	"io"
	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"strings"
	"time"
)

// Notifier delivers a single terminal message to the user's conversation
// when a watch ends. It is intentionally a thin wrapper over the same
// notifications-server endpoint that conversation replies use, so a
// completed watch shows up as a normal in-thread message.
type Notifier interface {
	Notify(ctx *security.RequestContext, w Watch, status Status, summary string) error
}

// HTTPNotifier is the default Notifier — POSTs to
// {NotificationServerUrl}/llm/response with the same payload shape as
// agents/core.sendReplyToNotificationServer.
type HTTPNotifier struct{}

// Notify sends the watch outcome to the notifications server. The watch
// package builds the payload directly (rather than reusing the agent
// notifier) to avoid pulling agents/core into the dependency graph; the
// payload shape is intentionally a subset that the existing endpoint
// already accepts.
func (HTTPNotifier) Notify(ctx *security.RequestContext, w Watch, status Status, summary string) error {
	if config.Config.NotificationServerUrl == "" {
		ctx.GetLogger().Warn("watch: notification server URL not configured; skipping notify",
			"watch_id", w.ID.String(), "status", string(status))
		return nil
	}
	body := buildNotifyBody(w, status, summary)
	url := strings.TrimRight(config.Config.NotificationServerUrl, "/") + "/llm/response"

	// Derive from the request context so the HTTP call respects the
	// poll's deadline + carries trace/span IDs through to the notifier.
	c, cancel := context.WithTimeout(ctx.GetContext(), 10*time.Second)
	defer cancel()

	resp, err := common.HttpPost(url,
		common.HttpWithJsonBody(body),
		common.HttpWithContext(c),
	)
	if err != nil {
		return fmt.Errorf("watch: notification post failed: %w", err)
	}
	defer func() {
		if cerr := resp.Body.Close(); cerr != nil {
			ctx.GetLogger().Warn("watch: notification body close failed", "error", cerr)
		}
	}()
	if resp.StatusCode >= 300 {
		data, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("watch: notification server returned %d: %s", resp.StatusCode, truncate(string(data), 500))
	}
	return nil
}

// buildNotifyBody constructs the request body. Exposed for testing.
func buildNotifyBody(w Watch, status Status, summary string) map[string]any {
	rendered := renderNotifyMessage(w, status, summary)
	body := map[string]any{
		"conversation_id": w.ConversationID.String(),
		"session_id":      w.ConversationID.String(),
		"tenant_id":       w.TenantID.String(),
		"type":            "final",
		"response":        rendered,
	}
	return body
}

// renderNotifyMessage produces the user-facing message. If
// notify_template is set we substitute {summary} into it; otherwise we
// fall back to a short canonical line per terminal status.
func renderNotifyMessage(w Watch, status Status, summary string) string {
	if w.NotifyTemplate != nil && *w.NotifyTemplate != "" {
		return strings.ReplaceAll(*w.NotifyTemplate, "{summary}", summary)
	}
	switch status {
	case StatusCompleted:
		if summary != "" {
			return fmt.Sprintf("Watch completed: %s", summary)
		}
		return "Watch completed."
	case StatusExpired:
		if summary != "" {
			return fmt.Sprintf("Watch expired before its termination condition was met. Last state: %s", summary)
		}
		return "Watch expired before its termination condition was met."
	case StatusFailed:
		if summary != "" {
			return fmt.Sprintf("Watch failed: %s", summary)
		}
		return "Watch failed."
	case StatusCancelled:
		return "Watch cancelled."
	default:
		return summary
	}
}
