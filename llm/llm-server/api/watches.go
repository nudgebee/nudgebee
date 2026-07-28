package api

import (
	"errors"
	"log/slog"

	"nudgebee/llm/common"
	"nudgebee/llm/security"
	"nudgebee/llm/watch"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// watchManagerFactory is the seam tests override to inject a sqlmock-backed
// *watch.Manager so handler-level tests can pin the cross-tenant /
// envelope / affected_rows contracts without standing up a real metastore.
// Production callers leave this at the default which constructs a Manager
// bound to common.GetDatabaseManager(common.Metastore). Replaced by tests
// via the SetWatchManagerFactory helper below and restored on cleanup.
var watchManagerFactory = func() *watch.Manager { return watch.NewManager() }

// SetWatchManagerFactory installs a custom factory; returns the prior one so
// the caller can defer-restore it. Test-only seam — not exported beyond
// the api package's test files in normal usage.
func SetWatchManagerFactory(f func() *watch.Manager) func() *watch.Manager {
	prev := watchManagerFactory
	watchManagerFactory = f
	return prev
}

// handleWatchesApi exposes two Hasura action endpoints over plain HTTP so
// the watch UI works in both Hasura-served and RPC-bypass modes. Today
// the UI was reading the llm_watch_tasks table via raw GraphQL (auth via
// Hasura row-permissions); under bypass mode Hasura is not in the request
// path, so we need a service-owned endpoint that enforces tenant scoping
// itself. The two endpoints mirror the two queries the watches drawer
// uses (list + cancel) and call directly into the existing Manager —
// neither piece of business logic is duplicated.
//
// Endpoint paths follow the convention from budget.go: /v1/<module>/<verb>.
// Request body comes in as the standard Hasura action envelope
// ({action, input, session_variables, ...}); both flat and nested
// {request: {...}} shapes are accepted so dev tools and bypass routers
// don't need to know which one wraps their payload.
func handleWatchesApi(r *gin.Engine, tracer trace.Tracer, meter metric.Meter) {
	group := r.Group("/v1/watches")

	// POST /v1/watches/list-by-conversation — ai_list_watches_by_conversation
	group.POST("/list-by-conversation", func(c *gin.Context) {
		common.MetricsApiRequestsTotal("watches_list_by_conversation")

		requestMap := make(map[string]any)
		if err := c.ShouldBindJSON(&requestMap); err != nil {
			slog.Error(errorBindingMessage, "error", err)
			actionError(c, 400, "api: "+err.Error())
			return
		}

		var actionRequest ActionRequest
		if err := common.DecodeMapToStruct(requestMap, &actionRequest); err != nil {
			slog.Error(errorBindingMessage, "error", err)
			actionError(c, 400, "api: "+err.Error())
			return
		}

		payload := unwrapHasuraInput(actionRequest, requestMap)
		var request listWatchesByConversationInput
		if err := common.DecodeMapToStruct(payload, &request); err != nil {
			actionError(c, 400, "api: "+err.Error())
			return
		}
		if request.ConversationID == "" {
			actionError(c, 400, "api: conversation_id is required")
			return
		}
		conversationUUID, err := uuid.Parse(request.ConversationID)
		if err != nil {
			actionError(c, 400, "api: conversation_id must be a valid UUID")
			return
		}

		logger := slog.With("conversation_id", request.ConversationID)
		agentContext, err := buildContextFromPayload(c.Request.Context(), c, &actionRequest, tracer, meter, logger)
		if err != nil {
			actionError(c, 401, err.Error())
			return
		}

		// Server-side tenant scoping. Under Hasura-served mode the row
		// permission filter would do this; under bypass mode the row
		// permission filter is not in the request path, so we enforce
		// it here. Either way, the caller never sees rows from another
		// tenant.
		tenantStr := agentContext.GetSecurityContext().GetTenantId()
		tenantUUID, err := uuid.Parse(tenantStr)
		if err != nil {
			actionError(c, 401, "api: invalid tenant in security context")
			return
		}

		watches, err := watchManagerFactory().ListByConversation(c.Request.Context(), tenantUUID, conversationUUID)
		if err != nil {
			logger.Error("api: list watches failed", "error", err)
			actionError(c, 500, "api: failed to list watches")
			return
		}

		// Per-account authorization. The action is granted to account_admin /
		// account_admin_readonly, so tenant scope alone is not enough — an
		// admin scoped to account A must not see watches (predicate_expr,
		// source_config, last_poll_result, …) for conversations owned by
		// account B within the same tenant. Filter the tenant-scoped rows down
		// to accounts the caller can read, mirroring the per-account gating the
		// rest of the conversation API enforces.
		sc := agentContext.GetSecurityContext()
		authorized := make([]watch.Watch, 0, len(watches))
		for _, w := range watches {
			if sc.HasAccountAccess(w.AccountID.String(), security.SecurityAccessTypeRead) {
				authorized = append(authorized, w)
			}
		}

		// Hasura actions expect {data, err} shape so the GraphQL field
		// matches the typed schema in actions.graphql. Empty err is
		// represented as `nil` (omitted via json:omitempty).
		c.JSON(200, gin.H{
			"data": gin.H{"watches": toListResponse(authorized)},
		})
	})

	// POST /v1/watches/cancel — ai_cancel_watch
	group.POST("/cancel", func(c *gin.Context) {
		common.MetricsApiRequestsTotal("watches_cancel")

		requestMap := make(map[string]any)
		if err := c.ShouldBindJSON(&requestMap); err != nil {
			slog.Error(errorBindingMessage, "error", err)
			actionError(c, 400, "api: "+err.Error())
			return
		}

		var actionRequest ActionRequest
		if err := common.DecodeMapToStruct(requestMap, &actionRequest); err != nil {
			slog.Error(errorBindingMessage, "error", err)
			actionError(c, 400, "api: "+err.Error())
			return
		}

		payload := unwrapHasuraInput(actionRequest, requestMap)
		var request cancelWatchInput
		if err := common.DecodeMapToStruct(payload, &request); err != nil {
			actionError(c, 400, "api: "+err.Error())
			return
		}
		if request.WatchID == "" {
			actionError(c, 400, "api: watch_id is required")
			return
		}
		watchUUID, err := uuid.Parse(request.WatchID)
		if err != nil {
			actionError(c, 400, "api: watch_id must be a valid UUID")
			return
		}

		logger := slog.With("watch_id", request.WatchID)
		agentContext, err := buildContextFromPayload(c.Request.Context(), c, &actionRequest, tracer, meter, logger)
		if err != nil {
			actionError(c, 401, err.Error())
			return
		}

		tenantStr := agentContext.GetSecurityContext().GetTenantId()
		tenantUUID, err := uuid.Parse(tenantStr)
		if err != nil {
			actionError(c, 401, "api: invalid tenant in security context")
			return
		}
		// Per-account authorization. The action is granted to account_admin,
		// so we MUST authorize at account granularity, not tenant — a
		// tenant-only check (HasTenantAccess) both 403s the account_admin the
		// action was granted to AND fails to stop an admin scoped to account A
		// from cancelling a watch owned by account B within the same tenant.
		// Load the watch first (tenant-scoped) to learn its owning account_id,
		// then gate with HasAccountAccess(..., Update). A watch that doesn't
		// exist for this tenant is reported as affected_rows=0 with no leaked
		// detail, matching the no-op-on-no-match semantics below.
		mgr := watchManagerFactory()
		existing, getErr := mgr.Get(c.Request.Context(), tenantUUID, watchUUID)
		if getErr != nil {
			if errors.Is(getErr, watch.ErrWatchNotFound) {
				c.JSON(200, gin.H{"data": gin.H{"affected_rows": 0, "watch_id": "", "status": ""}})
				return
			}
			logger.Error("api: cancel watch pre-fetch failed", "error", getErr)
			actionError(c, 500, "api: failed to cancel watch")
			return
		}
		if !agentContext.GetSecurityContext().HasAccountAccess(existing.AccountID.String(), security.SecurityAccessTypeUpdate) {
			actionError(c, 403, errorUserAccessMessage)
			return
		}

		// Manager.Cancel is the same SQL guard the Hasura update permission
		// enforced: WHERE tenant_id=$1 AND id=$2 AND status IN
		// ('PENDING','ACTIVE') → SET status='CANCELLED'. It returns nil
		// on both "row updated" and "no rows matched" — the latter
		// happens when the watch is already terminal or never existed
		// or belongs to another tenant. We re-fetch to disambiguate and
		// return the right response shape; callers (the UI) use
		// affected_rows to decide whether to show success vs "already
		// terminal" toasts.
		err = mgr.Cancel(c.Request.Context(), tenantUUID, watchUUID)
		// ErrWatchNotFound is the "row doesn't exist for this tenant" signal:
		// either the watch_id is bogus or it belongs to another tenant. Match
		// Hasura's row-permission behaviour — silent no-op with affected_rows=0
		// — rather than 5xx-ing so the UI does not need to special-case this.
		// Only surface as 500 when Cancel returns a genuine system error
		// (DB unreachable, post-check failure, etc.).
		if err != nil && !errors.Is(err, watch.ErrWatchNotFound) {
			logger.Error("api: cancel watch failed", "error", err)
			actionError(c, 500, "api: failed to cancel watch")
			return
		}

		// Read back the row to report final status. Get is tenant-scoped so a
		// watch in another tenant fails the fetch here — caller sees
		// affected_rows=0 with no leaked information about the row's
		// existence. ErrWatchNotFound from Get is also expected for the
		// not-mine and never-existed cases; same response shape.
		updated, getErr := mgr.Get(c.Request.Context(), tenantUUID, watchUUID)
		affected := 0
		var finalStatus string
		var watchIDStr string
		if getErr == nil && updated != nil {
			watchIDStr = updated.ID.String()
			finalStatus = string(updated.Status)
			if updated.Status == watch.StatusCancelled {
				affected = 1
			}
		}

		c.JSON(200, gin.H{
			"data": gin.H{
				"affected_rows": affected,
				"watch_id":      watchIDStr,
				"status":        finalStatus,
			},
		})
	})
}

// listWatchesByConversationInput matches the GraphQL action's
// AILlmWatchListRequest input type. mapstructure tag for the decoder.
type listWatchesByConversationInput struct {
	ConversationID string `json:"conversation_id" mapstructure:"conversation_id"`
}

// cancelWatchInput matches the GraphQL action's AILlmWatchCancelRequest
// input type.
type cancelWatchInput struct {
	WatchID string `json:"watch_id" mapstructure:"watch_id"`
}

// watchRow is the wire shape returned to the UI — matches the field set
// the watches drawer reads from the previously-raw llm_watch_tasks table
// query, so the React component doesn't have to change when the call
// route switches from GraphQL-over-PG to action-over-HTTP. Snake_case to
// match Hasura's emitted field names.
type watchRow struct {
	ID              string  `json:"id"`
	Status          string  `json:"status"`
	SourceKind      string  `json:"source_kind"`
	PredicateKind   string  `json:"predicate_kind"`
	PredicateExpr   string  `json:"predicate_expr"`
	PredicateNegate bool    `json:"predicate_negate"`
	PollIntervalSec int     `json:"poll_interval_sec"`
	MaxDurationSec  int     `json:"max_duration_sec"`
	PollCount       int     `json:"poll_count"`
	FailureCount    int     `json:"failure_count"`
	NextPollAt      string  `json:"next_poll_at"`
	LastPollAt      *string `json:"last_poll_at"`
	LastPollResult  *string `json:"last_poll_result"`
	FinalResult     *string `json:"final_result"`
	Error           *string `json:"error"`
	ExpiresAt       string  `json:"expires_at"`
	CreatedAt       string  `json:"created_at"`
	UpdatedAt       string  `json:"updated_at"`
}

func toListResponse(in []watch.Watch) []watchRow {
	out := make([]watchRow, 0, len(in))
	for _, w := range in {
		var lastPollAt *string
		if w.LastPollAt != nil {
			s := w.LastPollAt.Format("2006-01-02T15:04:05.000000Z")
			lastPollAt = &s
		}
		out = append(out, watchRow{
			ID:              w.ID.String(),
			Status:          string(w.Status),
			SourceKind:      string(w.SourceKind),
			PredicateKind:   string(w.PredicateKind),
			PredicateExpr:   w.PredicateExpr,
			PredicateNegate: w.PredicateNegate,
			PollIntervalSec: w.PollIntervalSec,
			MaxDurationSec:  w.MaxDurationSec,
			PollCount:       w.PollCount,
			FailureCount:    w.FailureCount,
			NextPollAt:      w.NextPollAt.Format("2006-01-02T15:04:05.000000Z"),
			LastPollAt:      lastPollAt,
			LastPollResult:  w.LastPollResult,
			FinalResult:     w.FinalResult,
			Error:           w.Error,
			ExpiresAt:       w.ExpiresAt.Format("2006-01-02T15:04:05.000000Z"),
			CreatedAt:       w.CreatedAt.Format("2006-01-02T15:04:05.000000Z"),
			UpdatedAt:       w.UpdatedAt.Format("2006-01-02T15:04:05.000000Z"),
		})
	}
	return out
}

// unwrapHasuraInput pulls the action's input payload out of the Hasura
// envelope. Hasura sends `{action, input: {request: {...actual...}}, session_variables}`
// when the action's input is a typed object; bypass routers + dev tools
// often send the flat `{action, input: {...actual...}, session_variables}` or
// even the raw body. Trying all three is cheaper than failing the request
// and forcing the caller to guess.
func unwrapHasuraInput(h ActionRequest, rawBody map[string]any) map[string]any {
	if h.Input == nil {
		return rawBody
	}
	if req, ok := h.Input["request"].(map[string]any); ok {
		return req
	}
	if len(h.Input) > 0 {
		return h.Input
	}
	return rawBody
}
