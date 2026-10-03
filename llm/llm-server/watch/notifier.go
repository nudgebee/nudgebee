package watch

import (
	"context"
	"fmt"
	"io"
	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Notifier delivers one terminal message when a watch ends — thin wrapper over the
// same notifications-server endpoint replies use → shows up as a normal in-thread msg.
type Notifier interface {
	Notify(ctx *security.RequestContext, w Watch, status Status, summary string) error
}

// HTTPNotifier: default Notifier — POSTs {NotificationServerUrl}/llm/response.
// resolveSessionOverride = per-instance test seam (a pkg-level var would race). nil → prod.
type HTTPNotifier struct {
	resolveSessionOverride func(*security.RequestContext, Watch) string
}

// resolveSession: test override if installed, else the production lookup.
func (n HTTPNotifier) resolveSession(ctx *security.RequestContext, w Watch) string {
	if n.resolveSessionOverride != nil {
		return n.resolveSessionOverride(ctx, w)
	}
	return resolveNotifySession(ctx, w)
}

// Notify sends the watch outcome to notifications-server. Payload is built here
// (not via agents/core) to keep agents/core out of the dep graph.
func (n HTTPNotifier) Notify(ctx *security.RequestContext, w Watch, status Status, summary string) error {
	if config.Config.NotificationServerUrl == "" {
		ctx.GetLogger().Warn("watch: notification server URL not configured; skipping notify",
			"watch_id", w.ID.String(), "status", string(status))
		return nil
	}
	body := buildNotifyBody(w, status, summary)
	// Chat platforms route on the session id ("<channel>-<thread_ts>"), not the
	// conversation UUID → override when routable. Web/UUID → DB responder.
	if s := n.resolveSession(ctx, w); isRoutableChatSession(s) {
		body["conversation_id"] = s
		body["session_id"] = s
	}
	url := strings.TrimRight(config.Config.NotificationServerUrl, "/") + "/llm/response"

	// From request ctx → respects the poll deadline + carries trace/span IDs.
	c, cancel := context.WithTimeout(ctx.GetContext(), 10*time.Second)
	defer cancel()

	postOptions := []common.HttpOption{
		common.HttpWithJsonBody(body),
		common.HttpWithContext(c),
	}
	// /llm/response is behind verify_action_token → 401 without this once
	// NOTIFICATION_SERVER_TOKEN is set. Mirrors sendReplyToNotificationServer.
	if config.Config.NotificationServerToken != "" {
		postOptions = append(postOptions, common.HttpWithHeaders(map[string]string{"X-ACTION-TOKEN": config.Config.NotificationServerToken}))
	}

	resp, err := common.HttpPost(url, postOptions...)
	if err != nil {
		return fmt.Errorf("watch: notification post failed: %w", err)
	}
	defer func() {
		// Drain before close → keep-alive conn is reused, not dropped.
		_, _ = io.Copy(io.Discard, resp.Body)
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
		// watch_id → lets notifications-server retire the watch_registered status
		// message once the result is posted. Web/DB path ignores it.
		"watch_id": w.ID.String(),
	}
	return body
}

// isRoutableChatSession: allow-list of shapes _parse_conversation accepts (kept in
// sync w/ it). Positive match, not "not a UUID" → "wf__…" etc. can't 404.
func isRoutableChatSession(sessionID string) bool {
	switch {
	case sessionID == "":
		return false
	case strings.HasPrefix(sessionID, "spaces/"), // Google Chat
		strings.HasPrefix(sessionID, "a:"),  // Teams
		strings.HasPrefix(sessionID, "19:"): // Teams
		return true
	// "event-" is deliberately NOT here: _handle_event_conversation needs the
	// per-question reply_ref, which watch payloads don't carry → wrong thread
	// + channel-wide broadcast. Falls through to the DB responder instead.
	default:
		return isSlackSessionID(sessionID)
	}
}

// Slack session key from Events._session_id: "<channel_id>-<thread_ts>". Both halves
// pinned (C/D/G/U/W id + dotted ts) so a stray single-dash id can't 404.
var slackSessionRegex = regexp.MustCompile(`^[CDGUW][A-Z0-9]{7,}-\d+\.\d+$`)

// isSlackSessionID: Slack "<channel>-<thread_ts>"? Split out to be testable.
func isSlackSessionID(sessionID string) bool {
	return slackSessionRegex.MatchString(sessionID)
}

// IsRoutableChatSession — exported form; watch_resource uses it to gate the indicator.
func IsRoutableChatSession(sessionID string) bool { return isRoutableChatSession(sessionID) }

// resolveNotifySession → chat-routable session id: notify_session col first, legacy
// rows fall back to llm_conversations. Best-effort — "" on err → caller keeps the UUID.
func resolveNotifySession(ctx *security.RequestContext, w Watch) string {
	if w.NotifySession != nil && *w.NotifySession != "" {
		return *w.NotifySession
	}
	if w.ConversationID == uuid.Nil || w.TenantID == uuid.Nil {
		return ""
	}
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		ctx.GetLogger().Debug("watch.notifier: metastore unavailable for session resolve; using conversation id",
			"watch_id", w.ID.String(), "error", err)
		return ""
	}
	c, cancel := context.WithTimeout(ctx.GetContext(), 3*time.Second)
	defer cancel()
	var sessionID string
	if err := dbms.Db.QueryRowContext(c,
		`SELECT COALESCE(session_id, '') FROM llm_conversations WHERE id = $1 AND tenant_id = $2`,
		w.ConversationID, w.TenantID,
	).Scan(&sessionID); err != nil {
		ctx.GetLogger().Warn("watch.notifier: failed to resolve notify session; using conversation id",
			"watch_id", w.ID.String(), "error", err)
		return ""
	}
	return sessionID
}

// NotifyWatchRegistered signals Slack to claim an in-progress indicator, retired once
// the result is posted. Best-effort; notifySession MUST be chat-routable.
func NotifyWatchRegistered(ctx *security.RequestContext, w *Watch, notifySession string) error {
	if config.Config.NotificationServerUrl == "" || notifySession == "" || w == nil {
		return nil
	}
	body := map[string]any{
		"conversation_id": notifySession,
		"session_id":      notifySession,
		"tenant_id":       w.TenantID.String(),
		"type":            "watch_registered",
		"watch_id":        w.ID.String(),
		// Empty — notifications-server renders its own text; field is schema-required.
		"response": "",
	}
	url := strings.TrimRight(config.Config.NotificationServerUrl, "/") + "/llm/response"
	c, cancel := context.WithTimeout(ctx.GetContext(), 5*time.Second)
	defer cancel()

	postOptions := []common.HttpOption{
		common.HttpWithJsonBody(body),
		common.HttpWithContext(c),
	}
	if config.Config.NotificationServerToken != "" {
		postOptions = append(postOptions, common.HttpWithHeaders(map[string]string{"X-ACTION-TOKEN": config.Config.NotificationServerToken}))
	}

	resp, err := common.HttpPost(url, postOptions...)
	if err != nil {
		return fmt.Errorf("watch: registered-notice post failed: %w", err)
	}
	defer func() {
		// Drain before close → keep-alive conn is reused, not dropped.
		_, _ = io.Copy(io.Discard, resp.Body)
		if cerr := resp.Body.Close(); cerr != nil {
			ctx.GetLogger().Warn("watch: registered-notice body close failed", "error", cerr)
		}
	}()
	if resp.StatusCode >= 300 {
		data, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("watch: registered-notice returned %d: %s", resp.StatusCode, truncate(string(data), 300))
	}
	return nil
}

// renderNotifyMessage → user-facing text: notify_template with {summary} substituted,
// else a short canonical line per terminal status.
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
