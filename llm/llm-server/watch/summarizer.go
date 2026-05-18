package watch

import (
	"sync"

	"nudgebee/llm/security"
)

// Summarizer turns a watch's terminal state + raw outcome into a short,
// natural-language message addressed to the user. The message gets
// appended to the response of the agent turn that registered the watch
// so the user sees the follow-up in the same chat thread.
//
// Mirrors the LLMJudgeClientFactory pattern (see predicate.go): the
// implementation lives in agents/core where the LLM stack is reachable,
// and is wired into the watch package via SetSummarizerFactory at
// startup. This keeps the watch package below agents/core in the
// import graph.
type Summarizer interface {
	Summarize(ctx *security.RequestContext, w Watch, status Status, rawSummary, rawError string) (string, error)
}

// SummarizerFactory builds a Summarizer for the given watch. We don't
// hold a single global Summarizer because callers may want per-account
// model selection identical to the LLMJudge path.
type SummarizerFactory func(ctx *security.RequestContext, w Watch) (Summarizer, error)

var (
	summarizerFactoryMu sync.RWMutex
	summarizerFactory   SummarizerFactory
)

// SetSummarizerFactory installs the summarizer builder. Called once at
// process init from agents/core (BootstrapWatch). When unset, the
// executor falls back to the raw rendered template — no regression vs.
// pre-summarizer behaviour.
func SetSummarizerFactory(f SummarizerFactory) {
	summarizerFactoryMu.Lock()
	defer summarizerFactoryMu.Unlock()
	summarizerFactory = f
}

func getSummarizerFactory() SummarizerFactory {
	summarizerFactoryMu.RLock()
	defer summarizerFactoryMu.RUnlock()
	return summarizerFactory
}
