package flow_sources

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/observability"
	"nudgebee/services/security"
)

const openObserveAPMSourceName = "openobserve-apm"

func init() {
	RegisterFlowSourceFactory(
		openObserveAPMSourceName,
		func(logger *slog.Logger) (core.FlowSourceInterface, error) {
			return NewOpenObserveAPMFlowSource(logger), nil
		},
		"OpenObserve APM flow source that emits CALLS edges from trace spans",
		string(core.FlowSourceCategoryTracing),
	)
}

type OpenObserveAPMFlowSource struct {
	*BaseFlowSource
}

func NewOpenObserveAPMFlowSource(logger *slog.Logger) *OpenObserveAPMFlowSource {
	return &OpenObserveAPMFlowSource{
		BaseFlowSource: NewBaseFlowSource(openObserveAPMSourceName, core.FlowSourceCategoryTracing, true, logger),
	}
}

func (s *OpenObserveAPMFlowSource) Validate() error {
	return s.BaseFlowSource.Validate()
}

func (s *OpenObserveAPMFlowSource) BuildFlowRelationships(
	_ *security.RequestContext,
	req *core.FlowSourceBuildRequest,
) ([]*core.DbEdge, []*core.DbNode, error) {
	startedAt := time.Now()
	defer s.TrackBuildTime(startedAt)

	edges := make([]*core.DbEdge, 0)
	nodes := make([]*core.DbNode, 0)
	if req == nil {
		return edges, nodes, fmt.Errorf("flow source build request is required")
	}
	if req.TenantID == "" {
		return edges, nodes, fmt.Errorf("tenant ID is required for OpenObserve APM flow source")
	}
	s.InitializeNodeMatcher(req.ExistingNodes)

	accountID := resolveCloudAccountID(req)
	if accountID == "" {
		s.logger.Info("no cloud account ID available; skipping OpenObserve APM flow source")
		return edges, nodes, nil
	}

	endTime := time.Now()
	startTime := endTime.Add(-15 * time.Minute)
	if req.TimeRange != nil {
		startTime, endTime = req.TimeRange.StartTime, req.TimeRange.EndTime
	}

	queryCtx := security.NewRequestContextForTenantAdmin(req.TenantID, s.logger, nil, nil)
	spans, err := (&observability.OpenObserveTraceSource{}).QueryTraces(queryCtx, observability.TracesV3Request{
		AccountId: accountID,
		StartTime: startTime.UnixMilli(),
		EndTime:   endTime.UnixMilli(),
		QueryRequest: observability.TracesQueryBuilderRequest{
			Limit: 10000,
		},
	})
	if err != nil {
		if isIntegrationNotConfigured(err) {
			s.logger.Info("OpenObserve integration not linked to this cloud account; skipping",
				"cloud_account_id", accountID, "tenant_id", req.TenantID)
			return edges, nodes, nil
		}
		s.IncrementErrorCount()
		return nil, nil, fmt.Errorf("failed to query OpenObserve traces: %w", err)
	}

	type serviceCall struct {
		caller     *core.DbNode
		target     *core.DbNode
		callerName string
		targetName string
		count      int
	}
	calls := make(map[string]*serviceCall)
	unmatchedCallers, unmatchedTargets := 0, 0

	for _, span := range spans {
		callerName := strings.TrimSpace(span.ServiceName)
		targetName := strings.TrimSpace(span.DestinationWorkload)
		if targetName == "" {
			targetName = strings.TrimSpace(span.DestinationName)
		}
		if callerName == "" || targetName == "" {
			continue
		}

		caller, err := matchServiceToNode(
			s.GetNodeMatcher(), callerName, "Service", span.WorkloadNamespace, nil, accountID, s.logger,
		)
		if err != nil || caller == nil {
			unmatchedCallers++
			continue
		}
		target, err := matchServiceToNode(
			s.GetNodeMatcher(), targetName, "Service", span.DestinationNamespace, nil, accountID, s.logger,
		)
		if err != nil || target == nil {
			unmatchedTargets++
			continue
		}
		if caller.ID == target.ID {
			continue
		}

		key := caller.ID + "\x00" + target.ID
		if calls[key] == nil {
			calls[key] = &serviceCall{caller: caller, target: target, callerName: callerName, targetName: targetName}
		}
		calls[key].count++
	}

	for _, call := range calls {
		edge := s.CreateEdge(call.caller, call.target, core.RelationshipCalls, map[string]interface{}{
			"openobserve_source_service": call.callerName,
			"openobserve_target_service": call.targetName,
			"openobserve_span_count":     call.count,
		}, req.TenantID, accountID)
		edges = append(edges, edge)
	}

	s.logger.Info("completed OpenObserve APM edge build",
		"tenant_id", req.TenantID,
		"cloud_account_id", accountID,
		"spans", len(spans),
		"edges_created", len(edges),
		"unmatched_callers", unmatchedCallers,
		"unmatched_targets", unmatchedTargets)
	s.LogMetrics()
	return edges, nodes, nil
}
