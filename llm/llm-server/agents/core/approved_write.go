package core

import (
	"context"
	"strings"

	"nudgebee/llm/security"
	toolcore "nudgebee/llm/tools/core"
)

// A write a person approved can be recorded before it runs and settled after (#40734: an alert's
// conversation records every approved infra write on the alert). The recording lives in the api
// package, which installs the hooks below from init(); core only says which call was approved, by
// whom, and under which approval, and runs the tool once if the recorder lets it.

// ApprovedWrite is a write call a person approved, about to run.
type ApprovedWrite struct {
	AccountId      string
	ConversationId string
	MessageId      string
	AgentId        string
	// Tool is the registered name of the tool that runs the call.
	Tool  string
	Input string
	// ConfirmationKey is the key the approval was recorded under (QueryConfig.ToolConfirmations).
	ConfirmationKey string
	// Ref is the follow-up message the person answered and who they were; empty when the approval
	// came with no follow-up this server knows of.
	Ref toolcore.ToolConfirmationRef
	// Rule is the tool-approval rule that approved the write in place of a person; nil when a person did.
	Rule *RuleApproval
	// Destination is where the write lands, as the approval card names it; "" when unknown.
	Destination string
	// TargetAccountId is the account the write lands on when that is not AccountId (a call naming
	// another cluster or account); "" on the conversation's own account.
	TargetAccountId string
}

// RuleApproval is a tool-approval rule that approved one call, as its decision record names it
// (llm_tool_approval_decisions is keyed by the call: message, agent and tool call id).
type RuleApproval struct {
	RuleID     string
	RuleName   string
	Revision   int
	ToolCallID string
}

// ApprovedWriteClaim is what the recorder decided before the call runs.
type ApprovedWriteClaim struct {
	// Reask: the approval cannot be used for this call (another write already used it, or it is
	// not known which approval this is); the person is asked again.
	Reask bool
	// Refusal is set when the call must not run; it is the observation the model reads.
	Refusal string
	// Settle records how the call ended. Nil when nothing was recorded: the call runs as before.
	Settle func(resp toolcore.NBToolResponse, err error)
}

// ApprovedWriteRecorder records an approved write before it runs (callNbTool, right before the
// tool's Call). Nil, or a zero claim: the call runs as before, unrecorded.
var ApprovedWriteRecorder func(ctx *security.RequestContext, w ApprovedWrite) ApprovedWriteClaim

// ApprovalCardQuestion is the approval card's text for a write, or "" for the default text. The
// Ref of w is empty: nothing is approved yet.
var ApprovalCardQuestion func(ctx *security.RequestContext, w ApprovedWrite) string

// approvalReaskDetail marks a tool response whose call was not run because its approval must be
// asked for again; doAction then asks.
const approvalReaskDetail = "approval_reask"

// approvedWriteKey carries, from doAction to callNbTool, the approved write the call is.
type approvedWriteKey struct{}

// withApprovedWrite marks ctx for the one approved call; nil clears the mark, so nothing the tool
// calls inherits it.
func withApprovedWrite(ctx *security.RequestContext, w *ApprovedWrite) *security.RequestContext {
	if ctx == nil || ctx.GetContext() == nil {
		return ctx
	}
	return ctx.WithContext(context.WithValue(ctx.GetContext(), approvedWriteKey{}, w))
}

// approvedWriteOf is the approved write ctx was marked for, if it is tool's.
func approvedWriteOf(ctx *security.RequestContext, tool toolcore.NBTool) *ApprovedWrite {
	if ctx == nil || ctx.GetContext() == nil || tool == nil {
		return nil
	}
	w, _ := ctx.GetContext().Value(approvedWriteKey{}).(*ApprovedWrite)
	if w == nil || !strings.EqualFold(w.Tool, tool.Name()) {
		return nil
	}
	return w
}

// callApprovalKey carries, to the tool call a person approved, the approval it runs under.
type callApprovalKey struct{}

// WithCallApproval gives the tool's own context the approval its call runs under.
func WithCallApproval(ctx *security.RequestContext, w ApprovedWrite) *security.RequestContext {
	if ctx == nil || ctx.GetContext() == nil {
		return ctx
	}
	return ctx.WithContext(context.WithValue(ctx.GetContext(), callApprovalKey{}, w))
}

// CallApproval is the approval a person gave for the tool call ctx belongs to, when they gave one:
// a tool that records its own decision (run_step) records it under that approval.
func CallApproval(ctx *security.RequestContext) (ApprovedWrite, bool) {
	if ctx == nil || ctx.GetContext() == nil {
		return ApprovedWrite{}, false
	}
	w, ok := ctx.GetContext().Value(callApprovalKey{}).(ApprovedWrite)
	return w, ok
}

// claimApprovedWrite asks the recorder about the approved call ctx is marked for, if any. A
// response is returned when the call must not run (refused, or its approval must be asked again);
// otherwise settle (possibly nil) records the outcome. ctx comes back with the mark cleared.
func claimApprovedWrite(ctx *security.RequestContext, tool toolcore.NBTool) (*security.RequestContext, *toolcore.NBToolResponse, func(toolcore.NBToolResponse, error)) {
	w := approvedWriteOf(ctx, tool)
	if w == nil {
		return ctx, nil, nil
	}
	ctx = withApprovedWrite(ctx, nil)
	recorder := ApprovedWriteRecorder
	if recorder == nil {
		return ctx, nil, nil
	}
	claim := recorder(ctx, *w)
	switch {
	case claim.Reask:
		return ctx, &toolcore.NBToolResponse{
			Status:            toolcore.NBToolResponseStatusError,
			Data:              "This change was not run: it needs its own approval. The person is asked to approve it.",
			AdditionalDetails: map[string]any{approvalReaskDetail: true},
		}, nil
	case claim.Refusal != "":
		return ctx, &toolcore.NBToolResponse{Status: toolcore.NBToolResponseStatusError, Data: claim.Refusal}, nil
	}
	return ctx, nil, claim.Settle
}

// needsReask reports whether a tool response says the call's approval must be asked for again.
func needsReask(resp toolcore.NBToolResponse) bool {
	reask, _ := resp.AdditionalDetails[approvalReaskDetail].(bool)
	return reask
}

// recordConfirmationRef returns refs with ref recorded under key (refs may be nil).
func recordConfirmationRef(refs map[string]toolcore.ToolConfirmationRef, key string, ref toolcore.ToolConfirmationRef) map[string]toolcore.ToolConfirmationRef {
	if key == "" || ref.MessageId == "" {
		return refs
	}
	if refs == nil {
		refs = map[string]toolcore.ToolConfirmationRef{}
	}
	refs[key] = ref
	return refs
}

// approvedWriteFor is the write a call is, for its approval card and, once approved, its record.
// target is where the write lands when that is another account (writeTargetOf), else nil.
func (e *plannerExecutor) approvedWriteFor(tool toolcore.NBTool, action NBAgentPlannerToolAction, confirmationKey string, target, destination *writeTarget) ApprovedWrite {
	w := ApprovedWrite{
		AccountId:       e.agentRequest.AccountId,
		ConversationId:  e.agentRequest.ConversationId,
		MessageId:       e.agentRequest.MessageId,
		AgentId:         e.agentRequest.AgentId,
		Tool:            tool.Name(),
		Input:           action.ToolInput,
		ConfirmationKey: confirmationKey,
	}
	if destination != nil {
		w.Destination = destination.where()
	}
	if target != nil && target.Target.AccountID != "" && target.Target.AccountID != w.AccountId {
		w.TargetAccountId = target.Target.AccountID
	}
	return w
}

// requestUserId is the person a request belongs to, or "".
func requestUserId(ctx *security.RequestContext) string {
	if ctx == nil || ctx.GetSecurityContext() == nil {
		return ""
	}
	return strings.TrimSpace(ctx.GetSecurityContext().GetUserId())
}
