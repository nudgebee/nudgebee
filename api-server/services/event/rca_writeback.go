package event

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"time"

	"nudgebee/services/common"
	"nudgebee/services/config"
	integrationcore "nudgebee/services/integrations/core"
	"nudgebee/services/security"
)

// RCAWritebackRequest is the internal payload llm-server POSTs to /rpc/event
// (action "event_rca_writeback") once an RCA report has been saved. It carries
// only what llm-server knows; everything else (incident id, source, account,
// severity, per-tenant config) is resolved here from the authoritative event row.
type RCAWritebackRequest struct {
	EventId   string `json:"event_id"`
	AccountId string `json:"account_id"`
	RCAText   string `json:"rca_text"`
}

// rcaWritebackTarget holds the per-provider strings the writeback needs once a
// source is deemed eligible. The rcaWritebackSources map is the source seam:
// adding an incident-ticketing provider is a single entry (event source ->
// add-comment token + integration-config namespace), with no new code path —
// both ZenDuty and PagerDuty already have ticket-server AddComment and a
// per-tenant integration-config namespace, and their webhooks set
// events.finding_id to the external incident id.
type rcaWritebackTarget struct {
	commentSource   string // "source" token the ticket-server add-comment route consumes
	configNamespace string // integration type passed to the tenant config lookup
}

var rcaWritebackSources = map[string]rcaWritebackTarget{
	"zenduty_webhook":   {commentSource: "zenduty", configNamespace: "zenduty"},
	"pagerduty_webhook": {commentSource: "pagerduty", configNamespace: "pagerduty"},
}

const (
	// rcaWritebackConfigSeverities is read from the integration config but is NOT
	// surfaced in any ConfigSchema for the PoC — severities stay backend-only
	// (default HIGH), tunable per-tenant via a direct integration-config write if
	// a tenant asks. This is a deliberate forward-looking seam, not dead config.
	rcaWritebackConfigSeverities = "rca_writeback_severities"
	rcaWritebackDefaultSeverity  = "HIGH"
	rcaWritebackMaxCommentChars  = 3500
	rcaWritebackCacheNamespace   = "rca_writeback"
	rcaWritebackCacheTTL         = 7 * 24 * time.Hour
)

var rcaWritebackCacheOnce sync.Once

// ProcessRCAWriteback posts a completed RCA report back onto the originating
// incident (currently ZenDuty) as a note. It is best-effort and idempotent: a
// no-op unless the event came from a registered source, the per-tenant toggle is
// on, and the event's NudgeBee priority is in the configured severity set.
// Re-posting the same RCA version is suppressed; a revised RCA appends a new note.
//
// All trust-bearing fields (tenant, account, incident id, source, priority) are
// taken from the event row, never from the caller-supplied request.
func ProcessRCAWriteback(ctx *security.RequestContext, req RCAWritebackRequest) error {
	if req.EventId == "" {
		return fmt.Errorf("rca_writeback: event_id is required")
	}
	if strings.TrimSpace(req.RCAText) == "" {
		ctx.GetLogger().Info("rca_writeback: empty RCA text, skipping", "event_id", req.EventId)
		return nil
	}

	evt, err := GetEvent(ctx, req.EventId)
	if err != nil {
		return fmt.Errorf("rca_writeback: get event %s: %w", req.EventId, err)
	}

	target, ok := rcaWritebackSources[common.StrVal(evt.Source)]
	if !ok {
		// Not a writeback-eligible source (e.g. PagerDuty not yet enabled) — no-op.
		return nil
	}

	tenant := common.StrVal(evt.Tenant)
	if tenant == "" {
		return fmt.Errorf("rca_writeback: event %s has no tenant", req.EventId)
	}
	// Cross-tenant guard: the caller's authenticated tenant must own the event.
	// An empty caller tenant is a super-admin context (no tenant assertion to
	// check); the llm-server signal always sends a concrete x-tenant-id, so the
	// legitimate path is always enforced.
	if callerTenant := ctx.GetSecurityContext().GetTenantId(); callerTenant != "" && callerTenant != tenant {
		return fmt.Errorf("rca_writeback: caller tenant %s does not match event tenant %s", callerTenant, tenant)
	}

	findingId := common.StrVal(evt.FindingId)
	if findingId == "" {
		ctx.GetLogger().Error("rca_writeback: event has no finding_id (incident id), skipping", "event_id", req.EventId)
		return nil
	}

	// Authoritative account is the event's own, not the caller-supplied hint
	// (they can legitimately diverge under same-tenant account remapping).
	accountId := common.StrVal(evt.CloudAccountId)
	if accountId == "" {
		accountId = req.AccountId
	}

	// Tenant scope for the integration-config lookup (mirrors processPagerDutyComment).
	// NewSecurityContextForTenantAdmin returns nil on a transient account-ids
	// lookup error; guard so the best-effort writeback fails soft instead of
	// nil-panicking the read path.
	sc := security.NewSecurityContextForTenantAdmin(tenant)
	if sc == nil {
		ctx.GetLogger().Error("rca_writeback: failed to build tenant security context", "tenant", tenant)
		return nil
	}
	tenantCtx := security.NewRequestContext(ctx.GetContext(), sc, ctx.GetLogger(), nil, nil)

	enabled, severities := loadRCAWritebackConfig(tenantCtx, target.configNamespace)
	if !enabled {
		return nil
	}

	priority := strings.ToUpper(common.StrVal(evt.Priority))
	if !severities[priority] {
		ctx.GetLogger().Info("rca_writeback: priority not in configured severities, skipping",
			"event_id", req.EventId, "priority", priority)
		return nil
	}

	// Idempotency: best-effort sequential de-dup. Skip if this exact RCA version
	// was already posted, and record the marker only AFTER a successful post — so
	// a failed post is retried, and a failed post never erases a prior version's
	// marker. This suppresses re-delivery of the same RCA (replay / sync-recovery
	// paths). It is NOT an atomic claim: two truly-concurrent deliveries of the
	// same version could both post, which is acceptable here (llm-server
	// serializes RCA generation per fingerprint upstream, and the note is
	// best-effort). The 7-day TTL is honored on the redis provider (prod);
	// in-memory falls back to the global cache LifeWindow (dev/test only).
	hash := rcaContentHash(req.RCAText)
	cacheKey := accountId + ":" + req.EventId
	rcaWritebackCacheOnce.Do(func() {
		common.CacheCreateNamespace(rcaWritebackCacheNamespace, common.CacheNamespaceWithExpiration(rcaWritebackCacheTTL))
	})
	if prev, found := common.CacheGet(rcaWritebackCacheNamespace, cacheKey); found && string(prev) == hash {
		ctx.GetLogger().Info("rca_writeback: RCA unchanged since last post, skipping", "event_id", req.EventId)
		return nil
	}

	comment := buildRCAComment(req.RCAText, req.EventId)
	if err := postIncidentComment(tenant, accountId, target.commentSource, findingId, comment); err != nil {
		return fmt.Errorf("rca_writeback: post comment to %s incident %s: %w", target.commentSource, findingId, err)
	}

	if err := common.CacheSet(rcaWritebackCacheNamespace, cacheKey, []byte(hash),
		common.CacheSetWithExpiration(rcaWritebackCacheTTL)); err != nil {
		ctx.GetLogger().Warn("rca_writeback: failed to record idempotency marker (may re-post on redelivery)",
			"error", err, "event_id", req.EventId)
	}

	ctx.GetLogger().Info("rca_writeback: posted RCA note",
		"event_id", req.EventId, "incident_id", findingId, "source", target.commentSource, "priority", priority)
	return nil
}

// loadRCAWritebackConfig reads the per-tenant on/off flag and severity gate from
// the ticketing integration's config values. It reads by tenant+type (NOT via
// ListIntegrationConfigs, whose integrations_cloud_accounts join returns zero
// rows for tenant-scoped ticketing integrations). Severities default to
// HIGH-only when unset.
func loadRCAWritebackConfig(ctx *security.RequestContext, namespace string) (bool, map[string]bool) {
	severities := map[string]bool{rcaWritebackDefaultSeverity: true}

	configs, err := integrationcore.ListIntegrationConfigsByTenant(ctx, namespace)
	if err != nil {
		ctx.GetLogger().Error("rca_writeback: failed to get integration configs", "error", err, "namespace", namespace)
		return false, severities
	}

	enabled := false
	customSeverities := ""
	for _, cfg := range configs {
		for _, v := range cfg.Configs {
			switch v.Name {
			case integrationcore.IntegrationConfigRCAWritebackEnabled:
				if strings.EqualFold(v.Value, "true") {
					enabled = true
				}
			case rcaWritebackConfigSeverities:
				customSeverities = v.Value
			}
		}
	}

	if custom := parseSeveritySet(customSeverities); len(custom) > 0 {
		severities = custom
	}
	return enabled, severities
}

func parseSeveritySet(csv string) map[string]bool {
	out := map[string]bool{}
	for _, p := range strings.Split(csv, ",") {
		if p = strings.ToUpper(strings.TrimSpace(p)); p != "" {
			out[p] = true
		}
	}
	return out
}

// buildRCAComment renders the incident note: the RCA report (rune-safe truncated
// to keep within provider note limits) plus a deep link back to the full
// analysis in NudgeBee.
func buildRCAComment(rcaText, eventId string) string {
	body := strings.TrimSpace(rcaText)
	if runes := []rune(body); len(runes) > rcaWritebackMaxCommentChars {
		body = string(runes[:rcaWritebackMaxCommentChars]) + "\n\n…(truncated)"
	}
	return fmt.Sprintf("NudgeBee RCA Analysis\n\n%s\n\nView full analysis in NudgeBee: %s/investigate?id=%s",
		body, config.Config.BaseUrl, eventId)
}

// postIncidentComment posts a note onto an incident via the ticket-server
// add-comment route. Shared by processPagerDutyComment (ingestion deep-link) and
// ProcessRCAWriteback (post-RCA writeback); source selects the provider.
func postIncidentComment(tenant, accountId, source, ticketId, comment string) error {
	ticketUrl := config.Config.TicketServiceUrl + "/tickets/rpc/add-comment"
	resp, err := common.HttpPost(ticketUrl,
		common.HttpWithJsonBody(map[string]any{
			"action": map[string]any{"name": "ticket_add_comment"},
			"input": map[string]any{
				"object": map[string]any{
					"tenant":     tenant,
					"account_id": accountId,
					"source":     source,
					"ticket_id":  ticketId,
					"comment":    comment,
				},
			},
			"session_variables": map[string]any{"role": "admin"},
		}),
		common.HttpWithHeaders(map[string]string{"Content-Type": "application/json"}),
	)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 200 {
		return fmt.Errorf("ticket-server returned status %d", resp.StatusCode)
	}
	return nil
}

func rcaContentHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
