package watch

import (
	"context"
	"errors"
	"fmt"
	"nudgebee/llm/security"
	"time"

	"github.com/google/uuid"
)

// PrimePhase names the step a priming attempt got to. The caller needs the
// distinction because the two phases fail for different reasons: an observe
// failure is almost always a deterministic misconfiguration (tool not enabled
// for the account, an agent-style tool, a command that cannot run), while a
// predicate failure is usually transient (the llm_judge model call).
type PrimePhase string

const (
	// PrimePhaseValidate is the source's own config gate — including the
	// destructive-verb deny-list. It runs BEFORE anything is executed.
	PrimePhaseValidate  PrimePhase = "validate"
	PrimePhaseObserve   PrimePhase = "observe"
	PrimePhasePredicate PrimePhase = "predicate"
)

// PrimeOutcome is the result of the single synchronous observation run at
// registration time.
type PrimeOutcome struct {
	// Skipped is true when priming is disabled (timeout <= 0).
	Skipped bool
	// Met reports that the predicate already holds, so there is nothing to
	// watch for — the caller should answer now instead of registering.
	Met bool
	// Summary is the predicate's description of what it saw.
	Summary string
	// Observation is the normalized observation text.
	Observation string
	// Phase is where Err occurred; empty when Err is nil.
	Phase PrimePhase
	// TimedOut is true when the attempt hit the priming deadline rather than
	// failing outright. A slow command is not a broken one, so callers should
	// register the watch and let the dispatcher handle it.
	TimedOut bool
	// Err is the failure, if any.
	Err error
}

// Prime runs one observation + predicate evaluation synchronously, before a
// watch row exists.
//
// Why this exists: without it the first poll lands a full poll_interval after
// registration, and the agent's turn is long over by the time anything is
// known. Every FAILED watch in the dev metastore is an "observe failed: ..."
// — a missing tool config, an expired GitHub App token, a command that cannot
// run — which is exactly the class the agent could have corrected in the same
// turn if it had been told. It also short-circuits the "already done" case:
// registering a background job for something that has already finished costs
// the user a poll interval of waiting for an answer the agent could give now.
//
// Priming deliberately runs under the CALLER's security context, not the
// static admin context a dispatcher poll uses (buildWatchSecurityContext),
// so the check reflects what the requesting user may actually do rather than
// what an admin could.
//
// Known asymmetry, accepted: a caller whose own session is narrower than that
// admin context could fail the probe on a source whose real polls would have
// succeeded, costing a registration. The reverse — priming under admin — would
// let registration validate a command the requesting user cannot run, and the
// watch then drives that command unattended for up to WatchMaxDurationSec.
// Rejecting too much is the safer direction, so it stays.
//
// The observation is thrown away on success: Prime never writes, and a watch
// row is still created by the caller in the normal not-yet-met case.
func Prime(rctx *security.RequestContext, in CreateInput, timeout time.Duration) PrimeOutcome {
	if timeout <= 0 {
		return PrimeOutcome{Skipped: true}
	}

	src, ok := GetSource(in.SourceKind)
	if !ok {
		return PrimeOutcome{Phase: PrimePhaseValidate, Err: fmt.Errorf("unknown source_kind %q", in.SourceKind)}
	}

	// ValidateConfig FIRST, and never reorder this below Observe.
	//
	// It is not a formatting check: for toolSource it carries the agent-tool
	// reject and the destructive-verb deny-list (`kubectl delete`, `rm -rf`,
	// `helm uninstall`, ...). Manager.Create runs it before a row is written,
	// which used to mean a mutating source was refused without ever running.
	// Priming executes the command for real, so skipping this gate would turn
	// a rejected registration into a single live `kubectl delete` — the guard
	// would fire afterwards, against a cluster that had already lost the pod.
	if err := src.ValidateConfig(in.SourceConfig); err != nil {
		return PrimeOutcome{Phase: PrimePhaseValidate, Err: err}
	}

	pred, err := CompilePredicate(in.PredicateKind, in.PredicateExpr)
	if err != nil {
		return PrimeOutcome{Phase: PrimePhasePredicate, Err: err}
	}

	// A throwaway Watch value carrying only what a Source needs. The synthetic
	// ID is what the tool context uses as its conversation/message id, exactly
	// as a real poll does, so priming cannot append to the user's history.
	probe := Watch{
		ID:           uuid.New(),
		AccountID:    in.AccountID,
		TenantID:     in.TenantID,
		UserID:       in.UserID,
		SourceKind:   in.SourceKind,
		SourceConfig: string(in.SourceConfig),
	}

	primeCtx, cancel := context.WithTimeout(rctx.GetContext(), timeout)
	defer cancel()
	primeRctx := security.NewRequestContext(primeCtx, rctx.GetSecurityContext(), rctx.GetLogger(), rctx.GetTracer(), rctx.GetMeter())

	obs, obsErr := src.Observe(primeRctx, probe)
	if errors.Is(obsErr, ErrNotReadOnly) {
		// Refused before it ran, like a ValidateConfig rejection.
		return PrimeOutcome{Phase: PrimePhaseValidate, Err: obsErr}
	}
	if obsErr != nil {
		return PrimeOutcome{
			Phase:    PrimePhaseObserve,
			TimedOut: isDeadlineExceeded(primeCtx, obsErr),
			Err:      obsErr,
		}
	}

	result, evalErr := pred.Eval(primeRctx, probe, obs)
	if evalErr != nil {
		return PrimeOutcome{
			Phase:       PrimePhasePredicate,
			Observation: obs.Data,
			TimedOut:    isDeadlineExceeded(primeCtx, evalErr),
			Err:         evalErr,
		}
	}

	met := result.Met
	if in.PredicateNegate {
		met = !met
	}
	return PrimeOutcome{Met: met, Summary: result.Summary, Observation: obs.Data}
}

// isDeadlineExceeded reports whether a failure was the priming deadline rather
// than the operation itself. Checks the context as well as the error because a
// tool that wraps its own transport error may not preserve context.Err in the
// chain.
func isDeadlineExceeded(ctx context.Context, err error) bool {
	return errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded)
}
