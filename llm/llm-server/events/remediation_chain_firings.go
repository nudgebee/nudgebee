package events

import (
	"context"
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"
)

// ChainFiring is a recent firing that joined a chain: a later occurrence of an alert that triage marked DUPLICATE
// or SUPPRESSED (event_duplicates), whose analysis is the chain's, reused rather than run again (#40734).
type ChainFiring struct {
	EventId   string `db:"event_id"`
	AccountId string `db:"account_id"`
}

// RecentChainFirings is the newest firing of each chain that joined it in the last `since`, of tenants with the
// remediation loop on, that a person was told about: posted to Slack or Teams (a sent finding in
// sent_notifications), or P1/P2. At most limit, newest chains first.
func RecentChainFirings(ctx context.Context, db sqlx.QueryerContext, since time.Duration, limit int) ([]ChainFiring, error) {
	var out []ChainFiring
	err := sqlx.SelectContext(ctx, db, &out, `SELECT event_id, account_id FROM (
		SELECT DISTINCT ON (e.cloud_account_id, d.first_event_id) e.id::text AS event_id, e.cloud_account_id::text AS account_id, e.created_at
		  FROM event_duplicates d JOIN events e ON e.id = d.event_id AND e.cloud_account_id = d.cloud_account_id
		 WHERE d.created_at > LOCALTIMESTAMP - make_interval(secs => $1) AND d.occurrence_number > 1
		   AND COALESCE(e.nb_status, '') IN ('DUPLICATE', 'SUPPRESSED')
		   AND `+remediationLoopTenant+`
		   AND (COALESCE(e.computed_priority, '') IN ('P1', 'P2') OR EXISTS (SELECT 1 FROM sent_notifications s
		         WHERE s.tenant_id = e.tenant AND s.created_at >= e.created_at - interval '1 minute' AND s.event_id = e.id
		           AND s.platform IN ('slack', 'ms_teams', 'ms_teams_webhook') AND s.kind = 'finding' AND s.status = 'sent'))
		 ORDER BY e.cloud_account_id, d.first_event_id, e.created_at DESC) f
		 ORDER BY created_at DESC LIMIT $2`, since.Seconds(), limit)
	if err != nil {
		return nil, fmt.Errorf("RecentChainFirings: %w", err)
	}
	return out, nil
}
