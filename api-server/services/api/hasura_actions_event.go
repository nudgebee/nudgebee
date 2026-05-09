package api

import (
	"fmt"
	"log/slog"
	"nudgebee/services/audit"
	"nudgebee/services/common"
	"nudgebee/services/event"
	"nudgebee/services/internal/database/models"
	"time"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

func handleHasuraEventAction(hasuraPayload *HasuraActionRequest, c *gin.Context, tracer *trace.Tracer, meter *metric.Meter, logger *slog.Logger) {
	hasuraRequest := hasuraPayload.Input

	switch hasuraPayload.Action.Name {
	case "event_resolve":
		var request event.EventRecommendationApplyRequest
		var err error
		if hasuraRequest["object"] == nil {
			err = common.UnmarshalMapToStruct(hasuraRequest, &request)
		} else {
			err = common.UnmarshalMapToStruct(hasuraRequest["object"].(map[string]any), &request)
		}

		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		ctx, err := buildContextFromHasuraPayload(c, hasuraPayload, tracer, meter, logger)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		auditEvent := audit.Audit{
			UserId:         ctx.GetSecurityContext().GetUserId(),
			TenantId:       ctx.GetSecurityContext().GetTenantId(),
			AccountId:      request.AccountId,
			EventTime:      time.Now().UTC(),
			EventCategory:  audit.EventAlertEvent,
			EventTarget:    request.EventId,
			EventType:      audit.EventTypeEventResolve,
			EventState:     request.Data,
			EventPrevState: nil,
			EventActor:     audit.EventActorUiService,
			EventAction:    audit.EventActionCreate,
			EventStatus:    audit.EventStatusSuccess,
			EventAttr:      map[string]any{},
		}

		resp, err := event.ApplyEventResolution(ctx, request)
		if err != nil {
			auditEvent.EventStatus = audit.EventStatusFailure
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
		} else {
			c.JSON(200, resp)
		}

		if auditErr := audit.CreateAudit(ctx, &audit.AuditRequest{Audits: []audit.Audit{auditEvent}}); auditErr != nil {
			ctx.GetLogger().Error("failed to create audit event", "error", auditErr)
		}
		return

	case "refresh_investigation":
		var eventId string
		if hasuraRequest["event_id"] != nil {
			if id, ok := hasuraRequest["event_id"].(string); ok {
				eventId = id
			} else {
				c.JSON(400, common.ErrorHasuraActionBadRequest("event_id must be a string"))
				return
			}
		} else if hasuraRequest["object"] != nil {
			if objectMap, ok := hasuraRequest["object"].(map[string]any); ok {
				if eventIdValue := objectMap["event_id"]; eventIdValue != nil {
					if id, ok := eventIdValue.(string); ok {
						eventId = id
					} else {
						c.JSON(400, common.ErrorHasuraActionBadRequest("event_id must be a string"))
						return
					}
				}
			} else {
				c.JSON(400, common.ErrorHasuraActionBadRequest("object field is not valid"))
				return
			}
		}

		if eventId == "" {
			c.JSON(400, common.ErrorHasuraActionBadRequest("event_id is required"))
			return
		}

		ctx, err := buildContextFromHasuraPayload(c, hasuraPayload, tracer, meter, logger)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		err = event.RefreshInvestigation(ctx, eventId)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		c.JSON(200, map[string]any{
			"success": true,
			"message": "Investigation refreshed successfully",
		})
		if err := audit.PublishAuditEvent(ctx, audit.Audit{
			TenantId:      ctx.GetSecurityContext().GetTenantId(),
			UserId:        ctx.GetSecurityContext().GetUserId(),
			EventTime:     time.Now(),
			EventCategory: audit.EventAlertEvent,
			EventType:     audit.EventTypeEventInvestigate,
			EventState:    map[string]any{"event_id": eventId, "action": "refresh"},
			EventActor:    audit.EventActorApiService,
			EventTarget:   eventId,
			EventAction:   audit.EventActionUpdate,
			EventStatus:   audit.EventStatusSuccess,
		}); err != nil {
			ctx.GetLogger().Error("failed to publish audit event", "error", err)
		}

	case "trigger_investigation":
		slog.Info("trigger_investigation: received request", "has_events", hasuraPayload.Input["events"] != nil, "has_event", hasuraPayload.Input["event"] != nil)
		eventInput := []map[string]any{}
		if el, ok := hasuraPayload.Input["events"].([]map[string]any); ok {
			eventInput = el
			slog.Info("trigger_investigation: parsed events as []map[string]any", "count", len(eventInput))
		} else if el, ok := hasuraPayload.Input["events"].([]any); ok {
			for _, el := range el {
				if e, ok := el.(map[string]any); ok {
					eventInput = append(eventInput, e)
				}
			}
			slog.Info("trigger_investigation: parsed events as []any", "count", len(eventInput))
		} else if el, ok := hasuraPayload.Input["event"].(map[string]any); ok {
			eventInput = append(eventInput, el)
			slog.Info("trigger_investigation: parsed single event", "count", 1)
		} else {
			slog.Error("integrations: failed to decode request", "input", slog.AnyValue(hasuraPayload.Input))
			c.JSON(400, common.ErrorHasuraActionBadRequest("event: invalid request, events/event is required"))
			return
		}

		ctx, err := buildContextFromHasuraPayload(c, hasuraPayload, tracer, meter, logger)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		if len(eventInput) == 0 {
			c.JSON(200, map[string]any{
				"success": true,
				"async":   true,
			})
			return
		}

		var eventDtos []event.Event

		for idx, eventInput := range eventInput {
			for _, key := range []string{"starts_at", "ends_at"} {
				if ts, ok := eventInput[key].(string); ok && ts != "" {
					if !common.HasTimezoneIndicator(ts) {
						eventInput[key] = ts + "Z"
					}
				}
			}

			eventDto := event.Event{}
			err := common.UnmarshalMapToStruct(eventInput, &eventDto)
			if err != nil {
				slog.Error("integrations: failed to decode request", "error", err, "input", slog.AnyValue(eventInput))
				c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
				return
			}

			// Debug log all fields before validation
			slog.Info("trigger_investigation: event fields",
				"index", idx,
				"finding_id", eventDto.FindingId,
				"aggregation_key", eventDto.AggregationKey,
				"title", eventDto.Title,
				"tenant", eventDto.Tenant,
				"account_id", eventDto.AccountId,
				"finding_type", eventDto.FindingType,
				"priority", eventDto.Priority,
				"subject_name", eventDto.SubjectName,
				"cluster", eventDto.Cluster,
				"source", eventDto.Source)

			if eventDto.FindingId == "" || eventDto.AggregationKey == "" || eventDto.Title == "" || eventDto.Tenant == "" || eventDto.AccountId == "" || eventDto.FindingType == "" || eventDto.Priority == "" || eventDto.SubjectName == "" || eventDto.Cluster == "" {
				slog.Error("trigger_investigation: validation failed",
					"finding_id_empty", eventDto.FindingId == "",
					"aggregation_key_empty", eventDto.AggregationKey == "",
					"title_empty", eventDto.Title == "",
					"tenant_empty", eventDto.Tenant == "",
					"account_id_empty", eventDto.AccountId == "",
					"finding_type_empty", eventDto.FindingType == "",
					"priority_empty", eventDto.Priority == "",
					"subject_name_empty", eventDto.SubjectName == "",
					"cluster_empty", eventDto.Cluster == "")
				c.JSON(400, common.ErrorHasuraActionBadRequest("finding_id/aggregation_key/title/tenant/account_id/finding_type/priority/subject_name/cluster is required. findingId - "+eventDto.FindingId))
				return
			}

			if eventDto.Evidences == nil {
				eventDto.Evidences = []any{}

			}
			eventDtos = append(eventDtos, eventDto)
		}

		ids := make([]string, len(eventDtos))
		for i := range len(eventDtos) {
			ids[i] = common.GenerateUUID()
		}

		slog.Info("event: STARTING event processing", "count", len(eventDtos), "tenant", ctx.GetSecurityContext().GetTenantId(), "first_event_source", func() string {
			if len(eventDtos) > 0 {
				return eventDtos[0].Source
			}
			return "none"
		}())

		for i, eventDto := range eventDtos {
			slog.Info("event: processing single event",
				"index", i,
				"total", len(eventDtos),
				"finding_id", eventDto.FindingId,
				"source", eventDto.Source,
				"title", eventDto.Title,
				"tenant", eventDto.Tenant,
				"account", eventDto.AccountId)

			eventId, err := event.InvestigateEvent(ctx, eventDto, ids[i])
			if err != nil {
				slog.Error("event: unable to investigate event", "error", err, "fingerprint", eventDto.Fingerprint, "aggregation_key", eventDto.AggregationKey, "finding_id", eventDto.FindingId, "finding_type", eventDto.FindingType, "title", eventDto.Title, "tenant", eventDto.Tenant, "account", eventDto.AccountId, "source", eventDto.Source)
				c.JSON(500, common.ErrorHasuraActionInternal(fmt.Sprintf("event: failed to process event %s: %v", eventDto.FindingId, err)))
				return
			}
			ids[i] = eventId
			slog.Info("event: successfully investigated event", "event_id", eventId, "finding_id", eventDto.FindingId, "source", eventDto.Source)
		}
		slog.Info("event: COMPLETED event processing", "count", len(eventDtos))

		c.JSON(200, map[string]any{
			"success": true,
			"async":   false,
			"id":      ids,
		})
		if err := audit.PublishAuditEvent(ctx, audit.Audit{
			TenantId:      ctx.GetSecurityContext().GetTenantId(),
			UserId:        ctx.GetSecurityContext().GetUserId(),
			EventTime:     time.Now(),
			EventCategory: audit.EventAlertEvent,
			EventType:     audit.EventTypeEventInvestigate,
			EventState:    map[string]any{"event_count": len(eventDtos), "ids": ids},
			EventActor:    audit.EventActorApiService,
			EventTarget:   "event",
			EventAction:   audit.EventActionCreate,
			EventStatus:   audit.EventStatusSuccess,
		}); err != nil {
			ctx.GetLogger().Error("failed to publish audit event", "error", err)
		}

	case "event_update":
		var request models.UpdateEventRequest
		err := common.UnmarshalMapToStruct(hasuraPayload.Input["request"].(map[string]interface{}), &request)
		if err != nil {
			slog.Error("event_update: failed to decode request", "error", err)
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		err = common.ValidateStruct(request)

		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}
		ctx, err := buildContextFromHasuraPayload(c, hasuraPayload, tracer, meter, logger)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}
		resp, err := event.UpdateEvent(ctx, request)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		c.JSON(200, resp)
		if err := audit.PublishAuditEvent(ctx, audit.Audit{
			TenantId:      ctx.GetSecurityContext().GetTenantId(),
			UserId:        ctx.GetSecurityContext().GetUserId(),
			EventTime:     time.Now(),
			EventCategory: audit.EventAlertEvent,
			EventType:     audit.EventTypeEventUpdate,
			EventState:    request,
			EventActor:    audit.EventActorApiService,
			EventTarget:   "event",
			EventAction:   audit.EventActionUpdate,
			EventStatus:   audit.EventStatusSuccess,
		}); err != nil {
			ctx.GetLogger().Error("failed to publish audit event", "error", err)
		}
		return

	case "event_get_filter_values":
		var request event.GetEventFilterValuesRequest

		// Handle both direct input and nested request object
		requestData := hasuraRequest
		if hasuraRequest["request"] != nil {
			if reqMap, ok := hasuraRequest["request"].(map[string]any); ok {
				requestData = reqMap
			}
		}

		err := common.UnmarshalMapToStruct(requestData, &request)
		if err != nil {
			c.JSON(400, []common.Error{
				{
					Message: "invalid request format: " + err.Error(),
				},
			})
			return
		}

		ctx, err := buildContextFromHasuraPayload(c, hasuraPayload, tracer, meter, logger)
		if err != nil {
			c.JSON(500, []common.Error{
				{
					Message: err.Error(),
				},
			})
			return
		}

		response, err := event.GetEventFilterValues(ctx, request)
		if err != nil {
			ctx.GetLogger().Error("failed to get event filter values", "error", err)
			c.JSON(400, []common.Error{
				{
					Message: err.Error(),
				},
			})
			return
		}

		c.JSON(200, response)

	default:
		c.JSON(400, common.ErrorHasuraActionBadRequest("invalid action name - "+hasuraPayload.Action.Name))
	}
}
