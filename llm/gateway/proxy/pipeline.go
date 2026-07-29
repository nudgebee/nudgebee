package proxy

import (
	"context"

	"github.com/gin-gonic/gin"
	"github.com/maximhq/bifrost/core/schemas"

	"nudgebee/llm-gateway/auth"
	"nudgebee/llm-gateway/routing"
)

// RequestContext is the mutable state threaded through the request pipeline. It is
// built once per request after auth, then passed to each Stage in order. Stages
// read identity/provider and may: rewrite the request shape (route), inject a
// per-request credential onto Bctx (resolver), filter the body (filter), or reject
// the request (ratelimit). Terminal passthrough + metering are NOT stages — they
// run after the pipeline on the (possibly mutated) RequestContext.
type RequestContext struct {
	Gin  *gin.Context
	Ctx  context.Context
	Bctx *schemas.BifrostContext

	Identity auth.Identity
	Provider schemas.ModelProvider

	// Mutable request shape. The route stage may rewrite Model/Body/Path (alias/tier
	// resolution); the filter stage may transform Body.
	Model string
	Path  string
	Body  []byte

	Streaming bool

	// Set by the route stage; consumed by metering as the routing decision record.
	Decision routing.Decision
}

// Stage is one step in the request pipeline. Handle may mutate rc in place.
//
//   - stop=true means the stage has ALREADY written a response (a rejection, e.g.
//     429) and the pipeline must halt without calling the provider.
//   - a non-nil err is a fatal internal error; the runner writes a 500.
//
// The control stages (route, ratelimit, resolver, filter) are pluggable; an unset
// stage defaults to pass-through.
type Stage interface {
	Name() string
	Handle(rc *RequestContext) (stop bool, err error)
}

// Pipeline runs its stages in order, halting on the first stop or error.
type Pipeline struct{ stages []Stage }

// NewPipeline builds a pipeline from stages in execution order.
func NewPipeline(stages ...Stage) *Pipeline { return &Pipeline{stages: stages} }

// Run executes each stage in order. It returns stop=true if a stage short-circuited
// (response already written), or a non-nil err on a fatal stage error.
func (p *Pipeline) Run(rc *RequestContext) (stop bool, err error) {
	for _, s := range p.stages {
		stop, err = s.Handle(rc)
		if err != nil || stop {
			return stop, err
		}
	}
	return false, nil
}
