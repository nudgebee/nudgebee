package queue

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"nudgebee/services/common"
	"nudgebee/services/config"
	"nudgebee/services/internal/database"
	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/knowledge_graph/flow_sources"
	kgmodels "nudgebee/services/knowledge_graph/models"
	"nudgebee/services/knowledge_graph/resourcepatch"
	"nudgebee/services/knowledge_graph/sources"
	_ "nudgebee/services/knowledge_graph/sources/aws"        // register AWS source factory (init side-effect)
	_ "nudgebee/services/knowledge_graph/sources/azure"      // register Azure source factory (init side-effect)
	_ "nudgebee/services/knowledge_graph/sources/gcp"        // register GCP source factory (init side-effect)
	_ "nudgebee/services/knowledge_graph/sources/github"     // register GitHub source factory (init side-effect)
	_ "nudgebee/services/knowledge_graph/sources/gitlab"     // register GitLab source factory (init side-effect)
	_ "nudgebee/services/knowledge_graph/sources/k8s"        // register K8s source factory (init side-effect)
	_ "nudgebee/services/knowledge_graph/sources/pagerduty"  // register PagerDuty source factory (init side-effect)
	_ "nudgebee/services/knowledge_graph/sources/selfhosted" // register self-hosted VM source factory (init side-effect)
	"nudgebee/services/security"
)

// maxDrainsPerDelivery bounds how many follow-up builds one delivery will run
// before handing the remainder back to the queue. Concurrent same-tenant builds
// deadlock on knowledge_graph_node upserts, so a tenant is always built by one
// worker at a time; this cap stops a steady stream of requests from pinning that
// worker past RabbitMQ's consumer acknowledgement timeout.
const maxDrainsPerDelivery = 3

// drainContinuationSource marks the message a worker republishes when it hands
// back work at maxDrainsPerDelivery. The work itself is already in the queue
// table as pending rows; the message only exists to make some replica claim it,
// so it must not be enqueued as a request of its own — carrying no scope, it
// would become a tenant-wide rebuild nobody asked for.
const drainContinuationSource = "drain-continuation"

func init() {
	err := common.MqConsume(
		config.Config.RabbitMqKGUpdateExchange,
		config.Config.RabbitMqKGUpdateQueue,
		config.Config.RabbitMqKGUpdateQueue,
		config.Config.RabbitMqKGUpdateConcurrency,
		processKGUpdateMessage,
	)
	if err != nil {
		slog.Error("kg_queue: failed to start consumer", "error", err)
	}
}

func processKGUpdateMessage(msgCtx context.Context, data []byte) error {
	// 1. Unmarshal message
	var message KGUpdateMessage
	if err := common.UnmarshalJson(data, &message); err != nil {
		slog.Error("kg_queue: failed to unmarshal message", "error", err)
		return nil // Don't requeue malformed messages
	}

	// Stamp trace_id/span_id from the consumer span (msgCtx) so the whole KG
	// update flow — which threads this logger, not a RequestContext — correlates.
	logger := common.LoggerWithTrace(msgCtx, slog.Default()).With(
		"tenant_id", message.TenantID,
		"source", message.Source,
		"correlation_id", message.CorrelationID,
	)

	if message.TenantID == "" {
		logger.Error("kg_queue: message has no tenant id, dropping")
		return nil
	}

	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		logger.Error("kg_queue: failed to get database manager", "error", err)
		return nil // Don't requeue on DB errors, log and move on
	}
	requestRepo := core.NewBuildRequestRepository(dbms, logger).WithPolicy(core.BuildRequestPolicy{
		StaleAfter:            time.Duration(config.Config.KGUpdateProcessingTimeoutMinutes) * time.Minute,
		MinTenantWideInterval: time.Duration(config.Config.KGUpdateDeduplicationMinutes) * time.Minute,
	})

	// 2. Apply individually named resources directly; only the accounts where
	// that is not possible become build requests.
	accountIDs := message.AccountIDs
	if len(message.Resources) > 0 {
		rebuildAccounts := applyResourceRefs(msgCtx, dbms, message, logger)
		accountIDs = unionAccounts(message.AccountIDs, rebuildAccounts)
	}

	// 3. Record the request durably before competing for the tenant. If another
	// worker is mid-build it will drain this row when it finishes, so no request
	// is ever silently dropped (which the old time-window claim did).
	if shouldEnqueueScope(message, accountIDs) {
		if err := requestRepo.Enqueue(core.BuildRequestSpec{
			TenantID:      message.TenantID,
			AccountIDs:    accountIDs,
			Sources:       message.Sources,
			FlowSources:   message.FlowSources,
			Trigger:       message.Source,
			CorrelationID: message.CorrelationID,
		}); err != nil {
			logger.Error("kg_queue: failed to enqueue build request", "error", err)
			return nil
		}
	}

	// 4. Try to become the tenant's builder. Losing the race is the normal case
	// under a burst and needs no retry — the winner drains what we just enqueued.
	claim, claimed, err := requestRepo.TryClaim(message.TenantID)
	if err != nil {
		logger.Error("kg_queue: failed to claim tenant build", "error", err)
		return nil
	}
	if !claimed {
		logger.Info("kg_queue: nothing claimable now; request stays queued (another build is running, or a tenant-wide rebuild is waiting out the minimum interval)")
		return nil
	}

	// 5. Build, then drain anything queued while we were building.
	for drains := 0; claim != nil; drains++ {
		logger.Info("kg_queue: starting KG update for tenant",
			"account_ids", claim.AccountIDs,
			"partial", claim.Partial(),
			"request_count", len(claim.RequestIDs),
			"drain_index", drains)

		buildErr := processKGUpdateForTenant(msgCtx, message.TenantID, message.CorrelationID, claim, logger)
		if buildErr != nil {
			logger.Error("kg_queue: failed to process KG update", "error", buildErr)
		} else {
			logger.Info("kg_queue: successfully completed KG update",
				"account_ids", claim.AccountIDs,
				"partial", claim.Partial())
		}

		next, finishErr := requestRepo.Finish(claim, buildErr)
		if finishErr != nil {
			logger.Error("kg_queue: failed to finish build request", "error", finishErr)
			return nil
		}
		if next == nil {
			return nil
		}
		if drains+1 >= maxDrainsPerDelivery {
			// Hand the remainder back rather than hold this delivery open: the
			// republished message lets any replica pick the work up on a fresh
			// acknowledgement deadline.
			if err := requestRepo.Release(next); err != nil {
				logger.Error("kg_queue: failed to release drained claim", "error", err)
				return nil
			}
			if err := PublishKGUpdateMessage(msgCtx, KGUpdateMessage{
				TenantID: message.TenantID,
				Source:   drainContinuationSource,
			}); err != nil {
				logger.Error("kg_queue: failed to republish drain continuation", "error", err)
			}
			logger.Info("kg_queue: drain cap reached, remaining requests republished",
				"max_drains", maxDrainsPerDelivery)
			return nil
		}
		claim = next
	}

	return nil
}

// processKGUpdateForTenant processes all enabled filters for a specific tenant,
// narrowed to the claim's scope. A claim with no account ids reproduces the
// historical tenant-wide build.
func processKGUpdateForTenant(msgCtx context.Context, tenantID string, correlationID string, claim *core.BuildClaim, logger *slog.Logger) error {
	// Get database manager
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return err
	}

	// Create context with timeout. msgCtx carries the trace context extracted
	// from the consumed message's headers so the trace continues into the build.
	timeout := time.Duration(config.Config.KGUpdateProcessingTimeoutMinutes) * time.Minute
	ctx, cancel := context.WithTimeout(msgCtx, timeout)
	defer cancel()

	// Create security context for tenant
	reqCtx := security.NewRequestContextForTenantAdmin(tenantID, logger, nil, nil)
	reqCtx = security.NewRequestContext(ctx, reqCtx.GetSecurityContext(), logger, nil, nil)

	// Get filter repository
	filterRepo := core.NewFilterRepository(dbms, logger)

	// Get all enabled filters for this tenant
	filters, err := filterRepo.GetAllEnabledFiltersForTenant(reqCtx, tenantID)
	if err != nil {
		return err
	}

	if len(filters) == 0 {
		logger.Info("kg_queue: no enabled filters for tenant")
		return nil
	}

	// Create KG service
	kgService := core.NewService(reqCtx, logger, dbms)

	// Register sources (same as existing processFiltersAsync)
	if err := sources.RegisterAllSourcesToService(kgService, "", "", reqCtx); err != nil {
		logger.Warn("kg_queue: failed to register sources", "error", err)
	}
	if err := flow_sources.RegisterAllFlowSourcesToService(kgService, reqCtx); err != nil {
		logger.Warn("kg_queue: failed to register flow sources", "error", err)
	}
	if err := sources.RegisterAllEnrichersToService(kgService, reqCtx); err != nil {
		logger.Warn("kg_queue: failed to register enrichers", "error", err)
	}

	// Process each filter
	successCount := 0
	failureCount := 0
	var filterErrs []error
	for i, filter := range filters {
		logger.Info("kg_queue: processing filter",
			"correlation_id", correlationID,
			"filter_id", filter.ID,
			"filter_name", filter.FilterName,
			"index", i+1,
			"total", len(filters))

		if err := processFilter(reqCtx, kgService, filter, claim, logger); err != nil {
			logger.Error("kg_queue: filter processing failed",
				"correlation_id", correlationID,
				"filter_id", filter.ID,
				"filter_name", filter.FilterName,
				"error", err)
			failureCount++
			filterErrs = append(filterErrs, fmt.Errorf("filter %q: %w", filter.FilterName, err))
			// Continue with other filters
		} else {
			successCount++
			logger.Info("kg_queue: filter processed successfully",
				"correlation_id", correlationID,
				"filter_id", filter.ID,
				"filter_name", filter.FilterName)
		}
	}

	logger.Info("kg_queue: completed processing all filters",
		"correlation_id", correlationID,
		"total_filters", len(filters),
		"success_count", successCount,
		"failure_count", failureCount)

	return aggregateFilterErrors(filterErrs, len(filters))
}

// aggregateFilterErrors turns the per-filter build failures into the error the
// build request is resolved with, so a failed build is recorded as 'failed' with
// its cause instead of 'done'. Nil when every filter succeeded.
func aggregateFilterErrors(errs []error, totalFilters int) error {
	if len(errs) == 0 {
		return nil
	}
	return fmt.Errorf("%d of %d filters failed: %w", len(errs), totalFilters, errors.Join(errs...))
}

// shouldEnqueue reports whether a message represents a build request of its own.
// A drain continuation does not: its work is already pending in the queue table.
func shouldEnqueue(message KGUpdateMessage) bool {
	return message.Source != drainContinuationSource
}

// shouldEnqueueScope reports whether the message, after its resource refs were
// applied, still asks for a build. A message that named only resources and had
// every one applied needs none; enqueueing its empty account list would turn it
// into a tenant-wide rebuild.
func shouldEnqueueScope(message KGUpdateMessage, accountIDs []string) bool {
	if !shouldEnqueue(message) {
		return false
	}
	if len(message.Resources) > 0 && len(message.AccountIDs) == 0 {
		return len(accountIDs) > 0
	}
	return true
}

// applyResourceRefs applies the message's resource refs and returns the accounts
// that need an account-scoped build instead. If the applier cannot be created,
// every referenced account is returned so nothing is lost.
//
// Refs for an account the message already asks to rebuild are skipped: that
// build rewrites them anyway.
func applyResourceRefs(ctx context.Context, dbms *database.DatabaseManager, message KGUpdateMessage, logger *slog.Logger) []string {
	rebuilding := make(map[string]struct{}, len(message.AccountIDs))
	for _, id := range message.AccountIDs {
		rebuilding[id] = struct{}{}
	}
	refs := make([]resourcepatch.Ref, 0, len(message.Resources))
	for _, r := range message.Resources {
		accountID := r.AccountID
		if accountID == "" && len(message.AccountIDs) == 1 {
			// A publisher that named one account may leave it off its refs.
			accountID = message.AccountIDs[0]
		}
		if _, skip := rebuilding[accountID]; skip {
			continue
		}
		refs = append(refs, resourcepatch.Ref{
			AccountID:          accountID,
			ProviderResourceID: r.ProviderResourceID,
			NodeType:           r.NodeType,
			Op:                 r.Op,
		})
	}
	if len(refs) == 0 {
		return nil
	}
	if !config.Config.KGResourcePatchEnabled {
		// Per-resource updates are switched off: rebuild each referenced account,
		// exactly as before they existed.
		return refAccounts(refs)
	}
	applier, err := resourcepatch.NewApplier(dbms, logger)
	if err != nil {
		logger.Error("kg_queue: failed to create resource applier, rebuilding referenced accounts", "error", err)
		return referencedAccounts(message)
	}
	return applier.ApplyAll(ctx, message.TenantID, refs)
}

// refAccounts lists the distinct accounts of refs, in first-seen order.
func refAccounts(refs []resourcepatch.Ref) []string {
	out := make([]string, 0)
	seen := make(map[string]bool)
	for _, r := range refs {
		if r.AccountID == "" || seen[r.AccountID] {
			continue
		}
		seen[r.AccountID] = true
		out = append(out, r.AccountID)
	}
	return out
}

// referencedAccounts lists the distinct accounts a message's refs belong to.
func referencedAccounts(message KGUpdateMessage) []string {
	out := make([]string, 0)
	for _, r := range message.Resources {
		accountID := r.AccountID
		if accountID == "" && len(message.AccountIDs) == 1 {
			accountID = message.AccountIDs[0]
		}
		if accountID != "" {
			out = append(out, accountID)
		}
	}
	return unionAccounts(nil, out)
}

// unionAccounts returns the distinct account ids of a and b, a's order first.
func unionAccounts(a, b []string) []string {
	seen := make(map[string]struct{}, len(a)+len(b))
	out := make([]string, 0, len(a)+len(b))
	for _, list := range [][]string{a, b} {
		for _, id := range list {
			if _, dup := seen[id]; dup || id == "" {
				continue
			}
			seen[id] = struct{}{}
			out = append(out, id)
		}
	}
	return out
}

// buildRequestForFilter turns a tenant filter plus the claim's scope into the
// BuildRequest to execute, or nil when this filter has nothing to do for this
// claim.
//
// The filter decides what a tenant is ALLOWED to build; the claim decides what
// was asked for this time. The result is the intersection — never the union: a
// request naming an account or source the filter excludes yields nil (skip), so
// a caller-supplied scope can only ever narrow a filter, never widen it.
func buildRequestForFilter(filter *kgmodels.KnowledgeGraphTenantFilter, claim *core.BuildClaim, logger *slog.Logger) *core.BuildRequest {
	accountIDs, sourcesFromFilter, flowSourcesFromFilter := filter.ToSlices()

	partial := false
	if len(claim.AccountIDs) > 0 {
		accountIDs = intersectScope(accountIDs, claim.AccountIDs)
		if len(accountIDs) == 0 {
			logger.Info("kg_queue: filter does not cover any requested account, skipping",
				"filter_name", filter.FilterName,
				"requested_account_ids", claim.AccountIDs)
			return nil
		}
		partial = true
		// Flow sources observe traffic tenant-wide, so an account-scoped rebuild
		// runs only the ones explicitly asked for (normally none).
		flowSourcesFromFilter = claim.FlowSources
	}
	if len(claim.Sources) > 0 {
		sourcesFromFilter = intersectScope(sourcesFromFilter, claim.Sources)
		if len(sourcesFromFilter) == 0 {
			logger.Info("kg_queue: filter does not allow any requested source, skipping",
				"filter_name", filter.FilterName,
				"requested_sources", claim.Sources)
			return nil
		}
	}

	// Set time range for last 24 hours
	now := time.Now().UTC()
	timeRange := &core.TimeRange{
		StartTime: now.Add(-24 * time.Hour),
		EndTime:   now,
	}

	buildRequest := &core.BuildRequest{
		TenantID:    filter.TenantID.String(),
		AccountIDs:  accountIDs,
		Sources:     sourcesFromFilter,
		FlowSources: flowSourcesFromFilter,
		Filters:     make(map[string]string),
		SaveToDB:    true,
		TimeRange:   timeRange,
		Partial:     partial,
	}

	// Copy additional filters from JSONB
	if filter.Filters != nil {
		for key, value := range filter.Filters {
			if strVal, ok := value.(string); ok {
				buildRequest.Filters[key] = strVal
			}
		}
	}
	return buildRequest
}

// processFilter processes a single filter (reuses logic from existing processFiltersAsync),
// intersected with the claim's scope.
func processFilter(ctx *security.RequestContext, kgService *core.Service,
	filter *kgmodels.KnowledgeGraphTenantFilter, claim *core.BuildClaim, logger *slog.Logger) error {

	buildRequest := buildRequestForFilter(filter, claim, logger)
	if buildRequest == nil {
		return nil
	}

	logger.Info("kg_queue: building knowledge graph for filter",
		"tenant_id", buildRequest.TenantID,
		"filter_name", filter.FilterName,
		"account_ids", buildRequest.AccountIDs,
		"sources", buildRequest.Sources,
		"flow_sources", buildRequest.FlowSources,
		"partial", buildRequest.Partial)

	// Build the graph
	response, err := kgService.BuildGraphs(ctx, buildRequest)
	if err != nil {
		return err
	}

	logger.Info("kg_queue: successfully built graph for filter",
		"tenant_id", filter.TenantID.String(),
		"filter_name", filter.FilterName,
		"nodes_saved", response.NodesSaved,
		"edges_saved", response.EdgesSaved,
		"accounts_processed", response.AccountsProcessed)

	return nil
}

// intersectScope returns the members of filterValues that are also in
// requestValues. An empty filterValues means "no restriction", so the request's
// own list is the answer.
func intersectScope(filterValues, requestValues []string) []string {
	if len(filterValues) == 0 {
		return requestValues
	}
	allowed := make(map[string]struct{}, len(filterValues))
	for _, v := range filterValues {
		allowed[v] = struct{}{}
	}
	out := make([]string, 0, len(requestValues))
	for _, v := range requestValues {
		if _, ok := allowed[v]; ok {
			out = append(out, v)
		}
	}
	return out
}
