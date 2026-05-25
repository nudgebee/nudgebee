package entitlement

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

// IncidentCounter handles session-based incident counting
// Each unique incident/investigation counts as one, follow-ups are free

// CheckAndRecordIncident checks entitlement and records usage for an incident
// Returns: (allowed, isNewIncident, status, error)
func (s *Service) CheckAndRecordIncident(ctx context.Context, tenantID, eventID string) (bool, bool, *EntitlementStatus, error) {
	// Session ID is based on the event ID for incidents
	sessionID := fmt.Sprintf("event-%s", eventID)

	// 1. Check if this is a new incident or a follow-up
	isNew, err := s.IsNewIncident(ctx, tenantID, eventID, sessionID)
	if err != nil {
		slog.Error("Failed to check if new incident", "tenantID", tenantID, "eventID", eventID, "error", err)
		return false, false, nil, err
	}

	// 2. If it's a follow-up, always allow (already counted)
	if !isNew {
		return true, false, &EntitlementStatus{
			Allowed: true,
			Message: "Follow-up investigation (not billed)",
		}, nil
	}

	// 3. For new incidents, check entitlement
	status, err := s.CheckEntitlement(ctx, tenantID, DimensionIncidents)
	if err != nil {
		return false, true, nil, err
	}

	// 4. If not allowed (limit reached, no overage), return graceful degradation
	if !status.Allowed {
		return false, true, status, nil
	}

	// 5. Record the usage (this is a new billable incident)
	isBillable := true
	_, err = s.RecordUsage(ctx, RecordUsageRequest{
		TenantID:      tenantID,
		Dimension:     DimensionIncidents,
		ReferenceID:   eventID,
		ReferenceType: strPtr("event"),
		SessionID:     &sessionID,
		IsBillable:    &isBillable,
	})
	if err != nil {
		slog.Error("Failed to record incident usage", "tenantID", tenantID, "eventID", eventID, "error", err)
		// Don't fail the request, just log the error
	}

	return true, true, status, nil
}

// RecordIncidentFollowUp records a follow-up for an existing incident (not billed)
func (s *Service) RecordIncidentFollowUp(ctx context.Context, tenantID, eventID, referenceID string) error {
	sessionID := fmt.Sprintf("event-%s", eventID)
	isBillable := false

	_, err := s.RecordUsage(ctx, RecordUsageRequest{
		TenantID:      tenantID,
		Dimension:     DimensionIncidents,
		ReferenceID:   referenceID,
		ReferenceType: strPtr("follow_up"),
		SessionID:     &sessionID,
		IsBillable:    &isBillable,
	})

	return err
}

// GetIncidentCount returns the number of incidents used in the current billing period
func (s *Service) GetIncidentCount(ctx context.Context, tenantID string) (int, int, error) {
	billingPeriod := getFirstDayOfMonth(time.Now())
	usage, err := s.getCurrentUsage(ctx, tenantID, DimensionIncidents, billingPeriod)
	if err != nil {
		return 0, 0, err
	}

	limit, err := s.getEffectiveLimit(ctx, tenantID, DimensionIncidents)
	if err != nil {
		return usage, 0, err
	}

	return usage, limit, nil
}

// strPtr returns a pointer to a string
func strPtr(s string) *string {
	return &s
}
