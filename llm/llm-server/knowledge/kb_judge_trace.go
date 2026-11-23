package knowledge

import (
	"time"

	"nudgebee/llm/common"
	"nudgebee/llm/security"

	"github.com/lib/pq"
)

// Verdicts recorded on llm_retrieval_trace. The distinction that earns its keep
// is judgeVerdictUnavailable against judgeVerdictNone: one means the judge could
// not run and every candidate was kept, the other means it ran and rejected them
// all. Rendered as the same thing, a reader would be told "nothing was relevant"
// during an outage.
const (
	judgeVerdictKept        = "kept"
	judgeVerdictNone        = "none"
	judgeVerdictUnavailable = "unavailable"
)

// recordJudgeVerdict stamps what the judge decided onto the trace rows that fed
// it.
//
// One decision can span several rows: the KB pre-step searches account-wide and
// per mapped collection in parallel, then judges the merged set once. Each row
// gets the same verdict, and keptRef names the single document that won — so at
// most one row's candidates contain it, and the rest are readable as
// considered-but-not-chosen rather than as failures.
//
// Best-effort. A trace is a record of what happened, not part of answering, so a
// failed write is logged and swallowed rather than failing the turn.
func recordJudgeVerdict(rc *security.RequestContext, traceIDs []string, verdict, keptRef string, score float64, elapsed time.Duration) {
	if len(traceIDs) == 0 {
		// Callers that did not supply ids: the searches still happened and are
		// traced, they just cannot be tied back to this decision.
		return
	}
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		rc.GetLogger().Warn("kb judge: cannot record verdict, no database", "error", err)
		return
	}

	// judge_score and judge_kept_ref stay NULL unless something was kept —
	// a 0.0 score on a keep-none row reads as "scored zero", which is a
	// different claim from "no score applies".
	var scorePtr *float64
	var refPtr *string
	if verdict == judgeVerdictKept {
		scorePtr = &score
		if keptRef != "" {
			refPtr = &keptRef
		}
	}

	const update = `UPDATE llm_retrieval_trace
		SET judge_verdict = $2, judge_score = $3, judge_kept_ref = $4, judge_ms = $5
		WHERE id = ANY($1)`
	if _, err := dbms.Db.Exec(update, pq.Array(traceIDs), verdict, scorePtr, refPtr,
		int(elapsed.Milliseconds())); err != nil {
		rc.GetLogger().Warn("kb judge: failed to record verdict", "error", err, "traces", len(traceIDs))
	}
}
