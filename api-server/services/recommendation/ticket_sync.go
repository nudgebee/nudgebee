package recommendation

import (
	"errors"
	"fmt"
	"strings"

	"nudgebee/services/internal/database"
	"nudgebee/services/internal/database/models"
	"nudgebee/services/recommendation/coordinator"
	"nudgebee/services/security"
)

// ticketOutcomeForStatus maps a (provider-normalized) ticket status to the
// resolution outcome it settles to. ok=false means the ticket is still being
// worked — or carries a status this sync does not recognize — and the
// resolution stays InProgress. Closed-style statuses settle Success without
// verification for now; the verification loop refines closed-but-unfixed into
// needs-review later.
func ticketOutcomeForStatus(status string) (models.RecommendationResolutionStatus, bool) {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "resolved", "closed", "done", "complete", "completed":
		return models.RecommendationResolutionStatusSuccess, true
	case "rejected", "cancelled", "canceled", "declined":
		return models.RecommendationResolutionStatusFailed, true
	}
	return "", false
}

type ticketResolutionRow struct {
	ResolutionId string `db:"resolution_id"`
	TicketId     string `db:"ticket_id"`
	TicketStatus string `db:"ticket_status"`
}

// SyncTicketResolutions settles open Ticket resolutions against the local
// tickets table (shared metastore; ticket-server keeps rows current as tools
// report status). Delegating to a ticket claims the recommendation
// (RecordTicketResolution); this is the return path — the ticket reaching a
// terminal status settles that claim through the coordinator, which also
// projects the recommendation (Closed on success, back to Open on rejection).
// Rows whose ticket is missing, still open, or in an unrecognized status are
// left alone.
func SyncTicketResolutions(ctx *security.RequestContext) error {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return err
	}

	// Joined on BOTH the recommendation reference and the external key:
	// ticket_id alone is only unique per integration, while reference_id pins
	// the exact recommendation the ticket was cut for. The latest ticket row
	// wins when a created-then-commented pair exists for the same key.
	// reference_id is text (V249 widened it to hold non-uuid references), so
	// the uuid side is cast — casting reference_id to uuid instead would error
	// the whole query on the first non-uuid reference row.
	rows := []ticketResolutionRow{}
	err = dbms.Db.SelectContext(ctx.GetContext(), &rows, `
		SELECT DISTINCT ON (rr.id) rr.id AS resolution_id, t.ticket_id AS ticket_id, COALESCE(t.status, '') AS ticket_status
		FROM recommendation_resolution rr
		JOIN tickets t ON t.reference_id = rr.recommendation_id::text AND t.ticket_id = rr.type_reference_id
		WHERE rr.type = $1 AND rr.status = $2
		ORDER BY rr.id, t.updated_at DESC NULLS LAST`,
		models.RecommendationResolutionTypeTicket, models.RecommendationResolutionStatusInProgress)
	if err != nil {
		return fmt.Errorf("ticket sync: query open ticket resolutions: %w", err)
	}

	var settled int
	var errs error
	for _, row := range rows {
		outcome, ok := ticketOutcomeForStatus(row.TicketStatus)
		if !ok {
			continue
		}
		msg := fmt.Sprintf("Ticket %s %s", row.TicketId, strings.ToLower(strings.TrimSpace(row.TicketStatus)))
		if _, err := coordinator.SettleResolution(ctx, row.ResolutionId, outcome, msg, coordinator.SourceTicketSync); err != nil {
			errs = errors.Join(errs, fmt.Errorf("ticket sync: settle resolution %s: %w", row.ResolutionId, err))
			continue
		}
		settled++
	}
	if settled > 0 || errs != nil {
		ctx.GetLogger().Info("ticket sync: settled ticket resolutions", "checked", len(rows), "settled", settled)
	}
	return errs
}
