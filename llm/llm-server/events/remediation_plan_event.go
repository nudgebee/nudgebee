package events

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/jmoiron/sqlx"
)

// Which event a duplicate's remediation is on (#40734). A duplicate follows its main event while the main is live;
// once the main is over, the duplicate heads its own chain, as a chain breaks after a resolve. A plan stored on the
// event itself is never hidden behind a main event that has none.

// livePrimaryEventSQL is the main event of e (labels.nb_duplicate_of, else event_duplicates.first_event_id for a
// later occurrence that triage marked DUPLICATE or SUPPRESSED) when that main is live: not resolved, not
// suppressed, and either still firing or worked on (an investigation or a plan of its own). A closed main nobody
// analysed is over. NULL otherwise.
const livePrimaryEventSQL = `(SELECT m.id FROM events m
	 WHERE m.cloud_account_id = e.cloud_account_id AND m.id <> e.id
	   AND m.id = COALESCE(
	       CASE WHEN e.nb_status = 'DUPLICATE' AND jsonb_typeof(e.labels) = 'object'
	                 AND e.labels->>'nb_duplicate_of' ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
	            THEN (e.labels->>'nb_duplicate_of')::uuid END,
	       (SELECT d.first_event_id FROM event_duplicates d
	         WHERE d.event_id = e.id AND d.cloud_account_id = e.cloud_account_id AND d.occurrence_number > 1
	           AND e.nb_status IN ('DUPLICATE', 'SUPPRESSED') LIMIT 1))
	   AND COALESCE(m.nb_status, '') NOT IN ('RESOLVED', 'SUPPRESSED')
	   AND (COALESCE(m.status, '') = 'FIRING'
	     OR EXISTS (SELECT 1 FROM event_log_analysis ma
	         WHERE ma.event_id = m.id AND ma.analysis_type IN ('investigation', 'remediation') AND ma.status = 'COMPLETED')
	     OR EXISTS (SELECT 1 FROM event_analysis_mapping mm JOIN event_log_analysis ma ON ma.id = mm.analysis_id
	         WHERE mm.event_id = m.id AND mm.analysis_type IN ('investigation', 'remediation') AND ma.status = 'COMPLETED')))`

// PlanEventOf is the event whose plan eventId reads and acts on: its live main event when it is a duplicate of
// one, unless the main has no plan and eventId has one of its own; else eventId itself.
func PlanEventOf(ctx context.Context, db sqlx.QueryerContext, accountId, eventId string) (string, error) {
	var planEvent string
	err := sqlx.GetContext(ctx, db, &planEvent, `SELECT CASE
	         WHEN p.main IS NULL THEN e.id::text
	         WHEN NOT EXISTS (SELECT 1 FROM event_log_analysis pa WHERE pa.event_id = p.main AND pa.analysis_type = $3 AND COALESCE(pa.analysis, '') <> '')
	          AND EXISTS (SELECT 1 FROM event_log_analysis oa WHERE oa.event_id = e.id AND oa.analysis_type = $3 AND COALESCE(oa.analysis, '') <> '')
	           THEN e.id::text
	         ELSE p.main::text END
	  FROM events e CROSS JOIN LATERAL (SELECT `+livePrimaryEventSQL+` AS main) p
	 WHERE e.id = $2::uuid AND e.cloud_account_id = $1::uuid`, accountId, eventId, string(AnalysisTypeRemediation))
	if errors.Is(err, sql.ErrNoRows) {
		return eventId, nil
	}
	if err != nil {
		return "", fmt.Errorf("PlanEventOf: %w", err)
	}
	return planEvent, nil
}
