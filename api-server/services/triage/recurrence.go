package triage

import (
	"fmt"

	"nudgebee/services/internal/database"
	"nudgebee/services/security"
)

type RecurrenceInfoItem struct {
	EventID          string `json:"event_id" db:"event_id"`
	FirstEventID     string `json:"first_event_id" db:"first_event_id"`
	PreviousEventID  string `json:"previous_event_id" db:"previous_event_id"`
	OccurrenceNumber int    `json:"occurrence_number" db:"occurrence_number"`
}

type RecurrenceInfoRequest struct {
	EventID string `json:"event_id"`
}

type RecurrenceInfoResponse struct {
	Data []RecurrenceInfoItem `json:"data"`
}

func GetRecurrenceInfo(ctx *security.RequestContext, request RecurrenceInfoRequest) (RecurrenceInfoResponse, error) {
	if request.EventID == "" {
		return RecurrenceInfoResponse{}, fmt.Errorf("event_id is required")
	}

	tenantID := ctx.GetSecurityContext().GetTenantId()
	if tenantID == "" {
		return RecurrenceInfoResponse{}, fmt.Errorf("unauthorized: missing tenant")
	}

	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return RecurrenceInfoResponse{}, fmt.Errorf("failed to get database: %w", err)
	}

	var items []RecurrenceInfoItem
	err = dbms.Db.Select(&items,
		`SELECT event_id, first_event_id, previous_event_id, occurrence_number
		 FROM event_duplicates
		 WHERE event_id = $1 AND tenant_id = $2`,
		request.EventID, tenantID)
	if err != nil {
		return RecurrenceInfoResponse{}, fmt.Errorf("failed to query event_duplicates: %w", err)
	}

	if items == nil {
		items = []RecurrenceInfoItem{}
	}
	return RecurrenceInfoResponse{Data: items}, nil
}
