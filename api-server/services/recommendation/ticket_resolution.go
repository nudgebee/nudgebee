package recommendation

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"nudgebee/services/common"
	"nudgebee/services/internal/database"
	"nudgebee/services/internal/database/models"
	"nudgebee/services/security"
)

type RecommendationTicketResolutionRequest struct {
	AccountId        string                                      `json:"account_id" mapstructure:"account_id" validate:"required"`
	RecommendationId string                                      `json:"recommendation_id" mapstructure:"recommendation_id" validate:"required"`
	TicketId         string                                      `json:"ticket_id" mapstructure:"ticket_id" validate:"required"`
	TicketKey        string                                      `json:"ticket_key" mapstructure:"ticket_key"`
	ResolverType     models.RecommendationResolutionResolverType `json:"resolver_type" mapstructure:"resolver_type"`
	ResolverId       string                                      `json:"resolver_id" mapstructure:"resolver_id"`
}

type RecommendationTicketResolutionResponse struct {
	Resolution models.RecommendationResolution `json:"resolution"`
	Status     models.RecommendationStatus     `json:"status"`
}

// RecordTicketResolution links a just-created ticket to its recommendation as a
// resolution attempt and claims the recommendation. Delegating to a ticket is a
// real act of resolution, but until now it left no trace: no
// recommendation_resolution row, no status change, so a ticketed recommendation
// read as untouched Open everywhere. The ticket itself already exists in the
// ticketing tool — this only records the fact.
//
// The whole read-check-insert-claim sequence runs in one transaction with the
// recommendation row locked, so concurrent recordings serialize: re-recording
// the same ticket returns the existing row, while a different ticket for the
// same recommendation is a genuinely new delegation and gets its own attempt
// row rather than being silently dropped.
//
// Ticket resolutions stay InProgress here; nothing in this path (or the
// resolution poll, which skips Ticket rows) settles them. They are settled by
// the ticket-status sync that arrives with the resolution coordinator.
func RecordTicketResolution(ctx *security.RequestContext, request RecommendationTicketResolutionRequest) (RecommendationTicketResolutionResponse, error) {
	if err := common.ValidateStruct(request); err != nil {
		return RecommendationTicketResolutionResponse{}, fmt.Errorf("invalid ticket resolution request: %w", err)
	}
	if !ctx.GetSecurityContext().HasAccountAccess(request.AccountId, security.SecurityAccessTypeCreate) {
		return RecommendationTicketResolutionResponse{}, common.ErrorUnauthorized("error: account access not found")
	}

	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return RecommendationTicketResolutionResponse{}, err
	}

	tx, err := dbms.Db.Beginx()
	if err != nil {
		return RecommendationTicketResolutionResponse{}, fmt.Errorf("error starting ticket resolution transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	rec := models.Recommendation{}
	err = tx.Get(&rec, `SELECT * FROM recommendation WHERE id = $1 AND cloud_account_id = $2 FOR UPDATE`, request.RecommendationId, request.AccountId)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return RecommendationTicketResolutionResponse{}, fmt.Errorf("recommendation %s not found for account", request.RecommendationId)
		}
		ctx.GetLogger().Error("error loading recommendation for ticket resolution", "error", err, "recommendation_id", request.RecommendationId)
		return RecommendationTicketResolutionResponse{}, err
	}

	// Re-recording the SAME ticket is an idempotent retry and returns the live
	// row. A different ticket id falls through and is recorded as an additional
	// attempt — silently ignoring it would leave a real delegation untracked.
	existing := models.RecommendationResolution{}
	err = tx.Get(&existing, `
		SELECT * FROM recommendation_resolution
		WHERE recommendation_id = $1 AND type = $2 AND status <> $3
		ORDER BY created_at DESC NULLS LAST LIMIT 1`,
		rec.Id, models.RecommendationResolutionTypeTicket, models.RecommendationResolutionStatusFailed)
	if err == nil && existing.TypeReferenceId == request.TicketId {
		return RecommendationTicketResolutionResponse{Resolution: existing, Status: rec.Status}, nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		ctx.GetLogger().Error("error checking existing ticket resolution", "error", err, "recommendation_id", rec.Id)
		return RecommendationTicketResolutionResponse{}, err
	}

	resolverType := request.ResolverType
	if resolverType == "" {
		resolverType = models.RecommendationResolutionResolverTypeUser
	}
	resolverId := request.ResolverId
	if resolverId == "" {
		resolverId = ctx.GetSecurityContext().GetUserId()
	}

	now := time.Now().UTC()
	statusMessage := "Ticket created"
	if request.TicketKey != "" {
		statusMessage = fmt.Sprintf("Ticket %s created", request.TicketKey)
	}

	resolution := models.RecommendationResolution{
		Id:               common.GenerateUUID(),
		CreatedAt:        &now,
		UpdatedAt:        &now,
		RecommendationId: rec.Id,
		Type:             models.RecommendationResolutionTypeTicket,
		Data:             models.NewJsonObject(map[string]any{"ticket_id": request.TicketId, "ticket_key": request.TicketKey}),
		Status:           models.RecommendationResolutionStatusInProgress,
		TypeReferenceId:  request.TicketId,
		ResolverType:     resolverType,
		ResolverId:       resolverId,
		StatusMessage:    &statusMessage,
	}
	_, err = tx.Exec(`INSERT INTO recommendation_resolution (id, created_at, updated_at, recommendation_id, type, data, status, type_reference_id, resolver_type, resolver_id, status_message) values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
		resolution.Id, now.Format(time.RFC3339), now.Format(time.RFC3339), resolution.RecommendationId, resolution.Type, resolution.Data, resolution.Status, resolution.TypeReferenceId, resolution.ResolverType, resolution.ResolverId, resolution.StatusMessage)
	if err != nil {
		ctx.GetLogger().Error("error inserting ticket resolution", "error", err, "recommendation_id", rec.Id, "resolution_id", resolution.Id)
		return RecommendationTicketResolutionResponse{}, common.ErrorInternal(fmt.Sprintf("error inserting ticket resolution: %s", err.Error()))
	}

	// Claim the recommendation so producer syncs and the reaper leave it alone
	// while the ticket is worked. Only an Open recommendation is claimed — a
	// ticket must never resurrect or downgrade Closed/Dismissed/Archive state.
	// The FOR UPDATE lock above makes this read-then-claim exact.
	status := rec.Status
	if rec.Status == models.RecommendationStatusOpen {
		_, err = tx.Exec(`UPDATE recommendation SET status = $1, updated_at = $2, updated_by = $3 WHERE id = $4`,
			models.RecommendationStatusInProgress, now.Format(time.RFC3339), resolverId, rec.Id)
		if err != nil {
			ctx.GetLogger().Error("error claiming recommendation for ticket resolution", "error", err, "recommendation_id", rec.Id)
			return RecommendationTicketResolutionResponse{}, err
		}
		status = models.RecommendationStatusInProgress
	}

	if err = tx.Commit(); err != nil {
		ctx.GetLogger().Error("error committing ticket resolution", "error", err, "recommendation_id", rec.Id)
		return RecommendationTicketResolutionResponse{}, err
	}

	ctx.GetLogger().Info("ticket resolution recorded", "recommendation_id", rec.Id, "resolution_id", resolution.Id, "ticket_id", request.TicketId)
	return RecommendationTicketResolutionResponse{Resolution: resolution, Status: status}, nil
}
