package core

import (
	"context"

	"nudgebee/llm/security"
)

// DepthPolicy is what an investigation depth level changes for one run
// (#41806). No policy means every knob keeps its configured value, which is
// all a build without an InvestigationDepthResolver ever sees.
type DepthPolicy struct {
	Level string
	// MaxIterations is the top-level agent's planning budget.
	MaxIterations int
	// SubAgentMaxIterations is each sub-agent's planning budget.
	SubAgentMaxIterations int
	// MaxRefinements is how often the answer check may send an answer back;
	// 0 skips the check.
	MaxRefinements int
	// Direction says which way the level moves the step budgets, so a
	// configured default already past its budget is kept: a faster level never
	// runs longer, a deeper one never runs shorter.
	Direction BudgetDirection
}

// BudgetDirection is which way a depth level moves the configured step budgets.
type BudgetDirection int

const (
	// BudgetLowers makes the level's step budgets a ceiling.
	BudgetLowers BudgetDirection = iota + 1
	// BudgetRaises makes the level's step budgets a floor.
	BudgetRaises
)

// InvestigationDepthResolver picks a top-level run's depth, or nil to leave
// the run unchanged. Enterprise builds install one from init(); it must not
// fail the run.
type InvestigationDepthResolver func(ctx *security.RequestContext, request NBAgentRequest, agent NBAgent) *DepthPolicy

var investigationDepthResolver InvestigationDepthResolver

// SetInvestigationDepthResolver installs the resolver. Call it from init().
func SetInvestigationDepthResolver(resolver InvestigationDepthResolver) {
	investigationDepthResolver = resolver
}

func resolveInvestigationDepth(ctx *security.RequestContext, request NBAgentRequest, agent NBAgent) *DepthPolicy {
	if investigationDepthResolver == nil {
		return nil
	}
	return investigationDepthResolver(ctx, request, agent)
}

type depthPolicyKey struct{}

// WithInvestigationDepth puts the run's depth on ctx; sub-agents started from
// ctx inherit it. nil leaves ctx unchanged (today's behaviour).
func WithInvestigationDepth(ctx *security.RequestContext, policy *DepthPolicy) *security.RequestContext {
	if ctx == nil || ctx.GetContext() == nil || policy == nil {
		return ctx
	}
	return ctx.WithContext(context.WithValue(ctx.GetContext(), depthPolicyKey{}, policy))
}

// InvestigationDepthOf is the depth governing the run ctx belongs to, or nil.
func InvestigationDepthOf(ctx *security.RequestContext) *DepthPolicy {
	if ctx == nil || ctx.GetContext() == nil {
		return nil
	}
	policy, _ := ctx.GetContext().Value(depthPolicyKey{}).(*DepthPolicy)
	return policy
}

// depthMaxIterations applies the run's depth to an agent's planning budget.
// An agent that declares its own budget keeps it: depth tunes the default
// investigation loop, not agents that know what they need.
func depthMaxIterations(ctx *security.RequestContext, agent NBAgent, request NBAgentRequest, current int) int {
	policy := InvestigationDepthOf(ctx)
	if policy == nil {
		return current
	}
	if provider, ok := agent.(NBAgentIterationProvider); ok && provider.GetMaxIterations() > 0 {
		return current
	}
	budget := policy.MaxIterations
	if request.ParentAgentId != "" && request.ParentAgentId != request.AgentId {
		budget = policy.SubAgentMaxIterations
	}
	switch policy.Direction {
	case BudgetLowers:
		return min(budget, current)
	case BudgetRaises:
		return max(budget, current)
	}
	// No direction set: keep the configured budget rather than overwrite it.
	return current
}

// depthRefinementBudget is how often the answer check may send an answer back
// in this run; fallback is the planner's own default.
func depthRefinementBudget(ctx *security.RequestContext, fallback int) int {
	if policy := InvestigationDepthOf(ctx); policy != nil {
		return policy.MaxRefinements
	}
	return fallback
}
