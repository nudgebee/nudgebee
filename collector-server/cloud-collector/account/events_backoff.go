package account

import (
	"fmt"
	"nudgebee/collector/cloud/common"
	"nudgebee/collector/cloud/security"
	"time"
)

// An account whose credentials the cloud rejects is polled for events less and
// less often: 10, 20, 40 minutes, then hourly, until a poll succeeds. The state
// lives in agent.connection_status.events next to the poll's error, so every
// collector pod sees it, and a successful poll overwrites it.
const (
	eventsBackoffBase = 10 * time.Minute
	eventsBackoffCap  = time.Hour
	// eventsSchedulerSlack lets an account whose wait ends just after a scheduler
	// tick be polled on that tick. A poll runs a few seconds after the tick that
	// queued it, so without this a 10-minute wait would always miss the next one.
	eventsSchedulerSlack = 2 * time.Minute
)

// credentialsRejectedError is what StoreEvents returns when the cloud rejected the
// account's credentials. The account is backed off, so the job is done: the
// consumer logs it once and acknowledges the message.
type credentialsRejectedError struct {
	err        error
	failures   int
	retryAfter time.Time
}

func (e *credentialsRejectedError) Error() string {
	return fmt.Sprintf("credentials rejected (%d in a row, next poll after %s): %v", e.failures, e.retryAfter.Format(time.RFC3339), e.err)
}

func (e *credentialsRejectedError) Unwrap() error { return e.err }

// eventsRetryDelay is how long to wait after the nth rejected poll in a row.
func eventsRetryDelay(failures int) time.Duration {
	delay := eventsBackoffBase
	for i := 1; i < failures && delay < eventsBackoffCap; i++ {
		delay *= 2
	}
	return min(delay, eventsBackoffCap)
}

// eventsStatusOnFailure is the events entry of connection_status after a failed
// poll. A rejected-credentials failure adds one to the count and sets when the
// account may be polled again. Any other failure (throttling, network) keeps the
// count as it was, so a passing glitch neither slows an account down nor resets it.
func eventsStatusOnFailure(err error, previousFailures int, now time.Time) (map[string]any, *credentialsRejectedError) {
	status := map[string]any{"err": err.Error()}
	if !isAuthFailure(err) {
		if previousFailures > 0 {
			status["auth_failures"] = previousFailures
		}
		return status, nil
	}
	failures := previousFailures + 1
	retryAfter := now.UTC().Add(eventsRetryDelay(failures))
	status["auth_failures"] = failures
	status["retry_after"] = retryAfter.Format(time.RFC3339)
	return status, &credentialsRejectedError{err: err, failures: failures, retryAfter: retryAfter}
}

// previousEventsAuthFailures reads how many polls in a row had their credentials
// rejected. It ignores the agent's status: an account with rejected credentials
// is often already DISCONNECTED by the cost sync.
func previousEventsAuthFailures(ctx *security.RequestContext, dbms *common.DatabaseManager, accountId string) int {
	var failures []int
	err := dbms.QueryAndScan(&failures, `select coalesce((connection_status->'events'->>'auth_failures')::int, 0)
		from agent where cloud_account_id = $1 and type != 'proxy'
		order by updated_at desc limit 1`, accountId)
	if err != nil {
		ctx.GetLogger().Warn("events: unable to read earlier credential failures", "error", err)
		return 0
	}
	if len(failures) == 0 {
		return 0
	}
	return failures[0]
}
