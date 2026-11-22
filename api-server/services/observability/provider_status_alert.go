package observability

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"nudgebee/services/common"
	"nudgebee/services/config"
	"nudgebee/services/internal/database"
	"nudgebee/services/security"
	"slices"
	"sort"
	"strings"
	"time"
)

// providerAlertHold is how long an integration's probe has to keep failing before
// anyone is told. The check runs every 10 minutes, so this means "failed on two
// checks in a row": one timed-out probe is not worth a page, and a post-save
// re-check right after a cron tick does not count as a second failure. Kept under
// the 10-minute interval so a run that starts a little early still qualifies.
const providerAlertHold = 8 * time.Minute

// providerAlertErrorLimit bounds the probe error quoted in a notification; some
// backends answer with an entire HTML error page.
const providerAlertErrorLimit = 500

// integrationHealthMessageType is the notifications-server message type for these
// alerts. It renders like a generic message, but notifications-server reads its
// account_id so an account-scoped rule can route or silence it.
const integrationHealthMessageType = "integration_health"

// Seams so tests can read the previous stamp and the published messages without a
// database or a broker.
var (
	previousProviderStatus = loadProviderStatusStamp
	publishProviderAlert   = func(ctx *security.RequestContext, message map[string]any) error {
		return common.MqPublish(config.Config.RabbitMqNotificationsExchange, config.Config.RabbitMqNotificationsQueue, message, common.MqPublishWithContext(ctx.GetContext()))
	}
	providerAlertNow     = time.Now
	providerAlertEnabled = func() bool { return config.Config.FeatureProviderHealthAlertsEnabled }
)

// providerAlert is one integration's change of state on one account: the signals
// it serves that just crossed the hold (or just recovered).
type providerAlert struct {
	IntegrationId   string
	IntegrationName string
	Provider        string
	Signals         []string
	FailingSince    string
	Error           string
}

// loadProviderStatusStamp reads the stamp the previous run wrote, keyed by signal.
// Read straight from the row rather than through the agent cache: this is the
// state the alert decision is made from, and a stale copy would announce a failure
// twice.
func loadProviderStatusStamp(ctx *security.RequestContext, accountId string) (map[string]ProviderConnectionStatus, error) {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return nil, err
	}
	var raw sql.NullString
	const q = `
		SELECT (connection_status -> '` + ProviderStatusKey + `')::text
		FROM agent
		WHERE cloud_account_id = $1 AND type != 'proxy'
		LIMIT 1`
	if err := dbms.Db.QueryRowxContext(ctx.GetContext(), q, accountId).Scan(&raw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	if !raw.Valid || raw.String == "" || raw.String == "null" {
		return nil, nil
	}
	return decodeProviderStatusStamp([]byte(raw.String))
}

// decodeProviderStatusStamp keeps the per-signal entries of a stamp and ignores the
// rest (checkedAt).
func decodeProviderStatusStamp(raw []byte) (map[string]ProviderConnectionStatus, error) {
	var entries map[string]json.RawMessage
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, err
	}
	out := map[string]ProviderConnectionStatus{}
	for _, providerType := range providerTypesForStatus {
		entry, ok := entries[providerType]
		if !ok {
			continue
		}
		var status ProviderConnectionStatus
		if err := json.Unmarshal(entry, &status); err != nil {
			return nil, err
		}
		out[providerType] = status
	}
	return out, nil
}

// applyAlertState carries each integration-served signal's failure run over from
// the previous stamp and decides which integrations change state now. It sets
// FailingSince and Alerted on statuses, which the caller then stamps.
//
// A signal's history only carries over while the same integration serves it:
// pointing logs at a different backend starts afresh, and the old backend's alert
// is not followed by a recovery for a backend that never failed.
//
// With alerting off the failure run is still tracked, but nothing is marked as
// alerted, so turning the flag on announces whatever is already failing.
func applyAlertState(prev map[string]ProviderConnectionStatus, statuses []ProviderConnectionStatus, now time.Time, enabled bool) (failing, recovered []providerAlert) {
	// One message per integration: a backend that serves logs and metrics fails
	// for both at once. Grouped by index, since append moves the slice.
	group := func(signals []ProviderConnectionStatus) []providerAlert {
		var list []providerAlert
		index := map[string]int{}
		for _, s := range signals {
			if i, ok := index[s.IntegrationId]; ok {
				list[i].Signals = append(list[i].Signals, s.ProviderType)
				continue
			}
			index[s.IntegrationId] = len(list)
			list = append(list, providerAlert{
				IntegrationId:   s.IntegrationId,
				IntegrationName: s.IntegrationName,
				Provider:        s.Provider,
				Signals:         []string{s.ProviderType},
				FailingSince:    s.FailingSince,
				Error:           s.Error,
			})
		}
		return list
	}

	var fire, clear []ProviderConnectionStatus
	for i := range statuses {
		s := &statuses[i]
		if s.IntegrationId == "" {
			continue
		}
		p, seen := prev[s.ProviderType]
		same := seen && p.IntegrationId == s.IntegrationId

		if s.Connected {
			if same && p.Alerted && enabled {
				clear = append(clear, *s)
			}
			continue
		}

		s.FailingSince = now.UTC().Format(time.RFC3339)
		if same && !p.Connected && p.FailingSince != "" {
			s.FailingSince = p.FailingSince
			s.Alerted = p.Alerted
		}
		if s.Alerted || !enabled {
			continue
		}
		since, err := time.Parse(time.RFC3339, s.FailingSince)
		if err != nil || now.Sub(since) >= providerAlertHold {
			s.Alerted = true
			fire = append(fire, *s)
		}
	}

	return group(fire), group(clear)
}

// notifyProviderTransitions announces integrations that started or stopped failing
// on this account, and records on statuses what was announced. A failure or a
// recovery that could not be published is left as it was, so the next run tries again.
//
// Errors are logged, never returned: an alerting problem must not stop the status
// itself from being stamped.
func notifyProviderTransitions(ctx *security.RequestContext, acc providerStatusAccount, statuses []ProviderConnectionStatus) {
	logger := ctx.GetLogger()
	prev, err := previousProviderStatus(ctx, acc.AccountId)
	if err != nil {
		// Without the previous stamp a failure run cannot be told from a new one;
		// stamping fresh state would restart every hold, so leave alert state as is.
		logger.Warn("provider status: failed to read previous stamp, skipping alerts", "error", err)
		return
	}
	failing, recovered := applyAlertState(prev, statuses, providerAlertNow(), providerAlertEnabled())

	for _, alert := range failing {
		if err := publishProviderAlert(ctx, integrationHealthMessage(acc, alert, false)); err != nil {
			logger.Error("provider status: failed to publish failure alert", "integration_id", alert.IntegrationId, "error", err)
			// Only the signals this alert announced: another signal of the same
			// integration may have been announced on an earlier run.
			for i := range statuses {
				if statuses[i].IntegrationId == alert.IntegrationId && slices.Contains(alert.Signals, statuses[i].ProviderType) {
					statuses[i].Alerted = false
				}
			}
			continue
		}
		logger.Info("provider status: integration failing, alert published", "integration_id", alert.IntegrationId, "signals", alert.Signals)
	}
	for _, alert := range recovered {
		if err := publishProviderAlert(ctx, integrationHealthMessage(acc, alert, true)); err != nil {
			logger.Error("provider status: failed to publish recovery alert", "integration_id", alert.IntegrationId, "error", err)
			// Keep the announced failure on the stamp, or the next run sees a healthy
			// integration with nothing to recover and the recovery is never sent.
			for i := range statuses {
				if statuses[i].IntegrationId == alert.IntegrationId && slices.Contains(alert.Signals, statuses[i].ProviderType) {
					statuses[i].Alerted = true
					statuses[i].FailingSince = prev[statuses[i].ProviderType].FailingSince
				}
			}
			continue
		}
		logger.Info("provider status: integration recovered, alert published", "integration_id", alert.IntegrationId, "signals", alert.Signals)
	}
}

// integrationHealthMessage builds the notifications-server message for one
// integration's change of state.
func integrationHealthMessage(acc providerStatusAccount, alert providerAlert, recovered bool) map[string]any {
	cluster := acc.AccountName
	if cluster == "" {
		cluster = acc.AccountId
	}
	name := alert.IntegrationName
	if alert.Provider != "" {
		name = fmt.Sprintf("%s (%s)", alert.IntegrationName, alert.Provider)
	}
	signals := append([]string(nil), alert.Signals...)
	sort.Strings(signals)

	var b strings.Builder
	if recovered {
		fmt.Fprintf(&b, "**Integration recovered: %s**\n", name)
		fmt.Fprintf(&b, "Nudgebee can reach the %s provider for cluster **%s** again.\n", strings.Join(signals, ", "), cluster)
	} else {
		fmt.Fprintf(&b, "**Integration failing: %s**\n", name)
		fmt.Fprintf(&b, "Nudgebee cannot reach the %s provider for cluster **%s**. ", strings.Join(signals, ", "), cluster)
		b.WriteString("Logs, metrics and investigations that depend on it will be incomplete until it is fixed.\n")
		if alert.FailingSince != "" {
			fmt.Fprintf(&b, "- Failing since: %s\n", alert.FailingSince)
		}
		if alert.Error != "" {
			fmt.Fprintf(&b, "- Last error: `%s`\n", truncateAlertError(alert.Error))
		}
	}
	if config.Config.BaseUrl != "" {
		fmt.Fprintf(&b, "\n[View provider status](%s/agentHealth?accountId=%s)", strings.TrimRight(config.Config.BaseUrl, "/"), url.QueryEscape(acc.AccountId))
	}

	state := "failing"
	if recovered {
		state = "recovered"
	}
	return map[string]any{
		"kind":      "notification",
		"type":      integrationHealthMessageType,
		"source":    "troubleshoot",
		"tenant_id": acc.TenantId,
		"parameters": map[string]any{
			"message":        b.String(),
			"account_id":     acc.AccountId,
			"integration_id": alert.IntegrationId,
			"state":          state,
		},
	}
}

func truncateAlertError(s string) string {
	s = strings.Join(strings.Fields(strings.ReplaceAll(s, "`", "'")), " ")
	runes := []rune(s)
	if len(runes) <= providerAlertErrorLimit {
		return s
	}
	return string(runes[:providerAlertErrorLimit]) + "…"
}
