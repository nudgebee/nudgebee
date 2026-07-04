package gcloud

import (
	"fmt"
	"nudgebee/collector/cloud/providers"
	"strconv"
	"time"

	trace "cloud.google.com/go/trace/apiv1"
	"cloud.google.com/go/trace/apiv1/tracepb"
	"google.golang.org/api/iterator"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	defaultTraceLimit  = 20
	maxTraceLimit      = 100
	maxTraceSpans      = 800 // total spans across returned traces — size governance
	defaultTraceWindow = 1 * time.Hour
)

// queryGcloudTraces fetches distributed traces from Cloud Trace for the account's project,
// scoped to the time window (and an optional provider-native filter). It returns the full
// span trees flattened into spans — no incident-type filtering, bounded by size caps.
func queryGcloudTraces(ctx providers.CloudProviderContext, account providers.Account, query providers.QueryTracesRequest) (providers.QueryTracesResponse, error) {
	logger := ctx.GetLogger()

	// Enrich ctx with audit info so permission errors are recorded (the query-traces entry
	// point builds a bare request context without it), mirroring query_logs.
	ctx = &gcpAuditContextWrapper{
		CloudProviderContext: ctx,
		enrichedCtx: WithGCPAuditInfo(ctx.GetContext(), &GCPAuditInfo{
			TenantID:       extractGcloudTenantID(ctx),
			CloudAccountID: account.ID,
			AccountNumber:  account.AccountNumber,
			ServiceName:    "Cloud Trace",
		}),
	}

	session, err := getGcloudSessionFromAccount(ctx, account)
	if err != nil {
		RecordGCPPermissionError(ctx, err)
		logger.Error("failed to get gcloud session for QueryTraces", "error", err, "accountNumber", account.AccountNumber)
		return providers.QueryTracesResponse{}, fmt.Errorf("failed to get gcloud session: %w", err)
	}

	client, err := trace.NewClient(ctx.GetContext(), session.Opts...)
	if err != nil {
		RecordGCPPermissionError(ctx, err)
		logger.Error("failed to create cloud trace client", "error", err, "projectId", session.ProjectId)
		return providers.QueryTracesResponse{}, fmt.Errorf("failed to create trace client: %w", err)
	}
	defer func() {
		if cerr := client.Close(); cerr != nil {
			logger.Error("failed to close cloud trace client", "error", cerr)
		}
	}()

	// Single-trace fetch (heatmap/gantt drilldown): Cloud Trace's GetTrace returns the
	// full span tree for one trace id, which the window-scoped ListTraces can't target.
	if query.TraceId != "" {
		tr, err := client.GetTrace(ctx.GetContext(), &tracepb.GetTraceRequest{
			ProjectId: session.ProjectId,
			TraceId:   query.TraceId,
		})
		if err != nil {
			RecordGCPPermissionError(ctx, err)
			logger.Error("failed to get cloud trace", "error", err, "projectId", session.ProjectId, "traceId", query.TraceId)
			return providers.QueryTracesResponse{}, fmt.Errorf("failed to get trace %s: %w", query.TraceId, err)
		}
		single := make([]providers.TraceSpanItem, 0, len(tr.GetSpans()))
		for _, s := range tr.GetSpans() {
			if len(single) >= maxTraceSpans {
				break
			}
			single = append(single, traceSpanToItem(tr.GetTraceId(), s))
		}
		logger.Info("GCP single-trace fetch complete", "projectId", session.ProjectId, "traceId", query.TraceId, "spans", len(single))
		return providers.QueryTracesResponse{Spans: single, TraceCount: 1, Status: "Complete"}, nil
	}

	end := time.Now()
	if query.EndTime != nil {
		end = *query.EndTime
	}
	start := end.Add(-defaultTraceWindow)
	if query.StartTime != nil {
		start = *query.StartTime
	}

	limit := defaultTraceLimit
	if query.Limit != nil && *query.Limit > 0 {
		limit = int(*query.Limit)
	}
	if limit > maxTraceLimit {
		limit = maxTraceLimit
	}

	// Order at the source so a limited query returns the correct page of traces.
	// Cloud Trace's default is trace_id order, which would page in arbitrary
	// (oldest-by-id) traces regardless of the window; "start desc" surfaces the
	// most recent traces first.
	orderBy := query.OrderBy
	if orderBy == "" {
		orderBy = "start desc"
	}

	// "By Traces" view wants one representative (root) span per trace. ROOTSPAN
	// returns exactly that from the source, so PageSize bounds traces (not spans)
	// and there's no in-memory root reduction. Otherwise COMPLETE for full trees.
	view := tracepb.ListTracesRequest_COMPLETE
	if query.RootsOnly {
		view = tracepb.ListTracesRequest_ROOTSPAN
	}

	req := &tracepb.ListTracesRequest{
		ProjectId: session.ProjectId,
		View:      view,
		PageSize:  int32(limit),
		StartTime: timestamppb.New(start.UTC()),
		EndTime:   timestamppb.New(end.UTC()),
		Filter:    query.Filter, // generic passthrough; empty = all in-window traces
		OrderBy:   orderBy,
	}

	logger.Info("querying GCP traces", "projectId", session.ProjectId,
		"filter", query.Filter, "limit", limit, "start", start.UTC(), "end", end.UTC())

	it := client.ListTraces(ctx.GetContext(), req)
	spans := make([]providers.TraceSpanItem, 0, limit*8)
	traceCount := 0
	for traceCount < limit && len(spans) < maxTraceSpans {
		tr, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			RecordGCPPermissionError(ctx, err)
			logger.Error("error reading trace", "error", err)
			break
		}
		traceCount++
		for _, s := range tr.GetSpans() {
			if len(spans) >= maxTraceSpans {
				break
			}
			spans = append(spans, traceSpanToItem(tr.GetTraceId(), s))
		}
	}

	logger.Info("GCP trace query complete", "projectId", session.ProjectId, "traces", traceCount, "spans", len(spans))
	return providers.QueryTracesResponse{
		Spans:      spans,
		TraceCount: traceCount,
		Status:     "Complete",
	}, nil
}

func traceSpanToItem(traceId string, s *tracepb.TraceSpan) providers.TraceSpanItem {
	item := providers.TraceSpanItem{
		TraceId: traceId,
		SpanId:  strconv.FormatUint(s.GetSpanId(), 10),
		Name:    s.GetName(),
		Kind:    s.GetKind().String(),
		Labels:  s.GetLabels(),
	}
	if s.GetParentSpanId() != 0 {
		item.ParentSpanId = strconv.FormatUint(s.GetParentSpanId(), 10)
	}
	if s.GetStartTime() != nil {
		st := s.GetStartTime().AsTime()
		item.StartTime = st.UnixMilli()
		if s.GetEndTime() != nil {
			item.DurationMs = float64(s.GetEndTime().AsTime().Sub(st).Microseconds()) / 1000.0
		}
	}
	return item
}
