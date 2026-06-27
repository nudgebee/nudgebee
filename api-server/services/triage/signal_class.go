package triage

import (
	"context"
	"errors"
	"log/slog"
	"strings"

	"nudgebee/services/internal/database/models"

	"github.com/jmoiron/sqlx"
)

// ErrMintSuperseded is returned by saveSignalVerdict when its compare-and-swap finds no
// minting row to promote — the class was released or already minted by another goroutine.
// It is a benign race outcome, not an error to surface.
var ErrMintSuperseded = errors.New("signal-class mint superseded")

// ClassKey derives the signal-class cache key for an event:
//
//	lower(aggregation_key) | finding_type | coalesce(subject_owner, subject_namespace)
//
// This collapses per-instance fingerprint fragmentation (e.g. 137 fingerprints of the same
// DB-slow-query alert -> one class) while separating conflated generic buckets (e.g. a
// "prod-alert" webhook carrying both RabbitMQ and slow-query alerts -> distinct scopes).
// Empirically validated against live data before adoption.
func ClassKey(event *models.Event) string {
	get := func(p *string) string {
		if p == nil {
			return ""
		}
		return *p
	}
	return classKeyFromParts(get(event.AggregationKey), get(event.FindingType), get(event.SubjectOwner), get(event.SubjectNamespace))
}

// classKeyFromParts builds the class key from raw string parts, so callers that only have a
// partial event projection (e.g. eventBasicInfo) can derive the same key as ClassKey.
func classKeyFromParts(aggregationKey, findingType, subjectOwner, subjectNamespace string) string {
	aggKey := strings.ToLower(strings.TrimSpace(aggregationKey))
	finding := strings.ToLower(strings.TrimSpace(findingType))
	scope := strings.TrimSpace(subjectOwner)
	if scope == "" {
		scope = strings.TrimSpace(subjectNamespace)
	}
	return aggKey + "|" + finding + "|" + scope
}

// getSignalVerdict returns the cached, servable verdict for a (tenant, class). The second
// return is false when no servable verdict exists yet (miss) — caller falls back. A class being
// lazily re-minted (status='reminting') keeps serving its existing verdict until the new one is
// promoted, so a prompt bump never causes a fallback-scoring blip.
func getSignalVerdict(ctx context.Context, db *sqlx.DB, tenantID, classKey string) (*SignalVerdict, bool) {
	if tenantID == "" || classKey == "" {
		return nil, false
	}
	var v SignalVerdict
	err := db.GetContext(ctx, &v, `
		SELECT category, intrinsic, blast, recurrence_semantics, env_sensitivity,
		       band_floor, band_ceiling, confidence,
		       COALESCE(reasoning, '')      AS reasoning,
		       COALESCE(source_model,'')    AS source_model,
		       COALESCE(prompt_version, 0)  AS prompt_version
		FROM triage_signal_class
		WHERE tenant_id = $1 AND class_key = $2 AND status IN ('active', 'reminting')
		LIMIT 1`, tenantID, classKey)
	if err != nil {
		return nil, false
	}
	return &v, true
}

// claimSignalClass atomically claims a class for minting across replicas. Returns true only
// for the caller that inserted the placeholder row; concurrent callers get false (they fall
// back and let the winner mint). Idempotent via the (tenant_id, class_key) unique constraint.
func claimSignalClass(ctx context.Context, db *sqlx.DB, tenantID, classKey string) bool {
	if tenantID == "" || classKey == "" {
		return false
	}
	res, err := db.ExecContext(ctx, `
		INSERT INTO triage_signal_class (tenant_id, class_key, status)
		VALUES ($1, $2, 'minting')
		ON CONFLICT (tenant_id, class_key) DO NOTHING`, tenantID, classKey)
	if err != nil {
		return false
	}
	n, _ := res.RowsAffected()
	return n > 0
}

// saveSignalVerdict promotes a class to active with the minted verdict and stamps the prompt
// version it was minted under. It is a compare-and-swap on status IN ('minting','reminting') — a
// first mint claims via 'minting', a lazy re-mint via 'reminting'; either way it only writes the
// row this mint actually claimed and has not yet promoted. If the row was released, reverted, or
// already promoted by a newer mint, the UPDATE matches 0 rows and we return ErrMintSuperseded
// instead of resurrecting a stale verdict over a fresher one.
func saveSignalVerdict(ctx context.Context, db *sqlx.DB, tenantID, classKey string, v *SignalVerdict) error {
	if tenantID == "" || classKey == "" {
		return ErrMintSuperseded
	}
	res, err := db.ExecContext(ctx, `
		UPDATE triage_signal_class
		SET category=$1, intrinsic=$2, blast=$3, recurrence_semantics=$4, env_sensitivity=$5,
		    band_floor=$6, band_ceiling=$7, confidence=$8, reasoning=$9, source_model=$10,
		    prompt_version=$13, status='active', updated_at=now()
		WHERE tenant_id=$11 AND class_key=$12 AND status IN ('minting', 'reminting')`,
		v.Category, v.Intrinsic, v.Blast, v.RecurrenceSemantics, v.EnvSensitivity,
		v.BandFloor, v.BandCeiling, v.Confidence, v.Reasoning, v.SourceModel,
		tenantID, classKey, v.PromptVersion)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrMintSuperseded
	}
	return nil
}

// claimRemint atomically flips an active, stale verdict to 'reminting' so exactly one serving
// caller re-mints it. The row keeps serving its existing verdict (getSignalVerdict accepts
// 'reminting') until the new verdict is promoted. Returns true only for the CAS winner. The
// version guard makes it idempotent and self-rate-limiting: once flipped, no other serve re-claims.
func claimRemint(ctx context.Context, db *sqlx.DB, tenantID, classKey string, currentVersion int) bool {
	if tenantID == "" || classKey == "" {
		return false
	}
	res, err := db.ExecContext(ctx, `
		UPDATE triage_signal_class
		SET status='reminting', updated_at=now()
		WHERE tenant_id=$1 AND class_key=$2 AND status='active'
		  AND COALESCE(prompt_version, 0) < $3`, tenantID, classKey, currentVersion)
	if err != nil {
		return false
	}
	n, _ := res.RowsAffected()
	return n > 0
}

// revertRemint returns a reminting row to active WITHOUT changing its verdict — used when a
// re-mint fails, so the existing (older-prompt) verdict keeps serving instead of being lost.
func revertRemint(ctx context.Context, db *sqlx.DB, tenantID, classKey string) {
	if _, err := db.ExecContext(ctx,
		`UPDATE triage_signal_class SET status='active', updated_at=now()
		 WHERE tenant_id=$1 AND class_key=$2 AND status='reminting'`, tenantID, classKey); err != nil {
		slog.WarnContext(ctx, "failed to revert signal-class re-mint", "class_key", classKey, "error", err)
	}
}
