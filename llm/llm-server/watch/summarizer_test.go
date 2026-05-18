package watch

import (
	"errors"
	"testing"

	"nudgebee/llm/security"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeSummarizer is a stand-in injected by tests via SetSummarizerFactory
// to exercise the factory hook without pulling agents/core into the watch
// package's test build.
type fakeSummarizer struct {
	out string
	err error
}

func (f *fakeSummarizer) Summarize(_ *security.RequestContext, _ Watch, _ Status, _, _ string) (string, error) {
	return f.out, f.err
}

// TestSummarizerFactory_UnsetByDefault confirms the package ships with no
// summarizer wired — the executor must fall back to the raw summary in
// that case so a build without BootstrapWatch still produces sane
// notifications. We assert this explicitly because the default-nil state
// is load-bearing for graceful degradation.
func TestSummarizerFactory_UnsetByDefault(t *testing.T) {
	// Snapshot + restore so other tests in the suite (which may install
	// a real factory via BootstrapWatch indirectly) aren't disturbed.
	prev := getSummarizerFactory()
	SetSummarizerFactory(nil)
	t.Cleanup(func() { SetSummarizerFactory(prev) })

	assert.Nil(t, getSummarizerFactory(),
		"with no factory installed, getSummarizerFactory must return nil — the executor branches on this to decide between LLM summary and raw template fallback")
}

// TestSummarizerFactory_InstallsAndReturns documents the setter/getter
// contract used by agents/core.BootstrapWatch. The factory is held in a
// package-level slot guarded by a RWMutex — easy to break with a race-
// detector run if the mutex is dropped.
func TestSummarizerFactory_InstallsAndReturns(t *testing.T) {
	prev := getSummarizerFactory()
	t.Cleanup(func() { SetSummarizerFactory(prev) })

	want := &fakeSummarizer{out: "hello"}
	SetSummarizerFactory(func(_ *security.RequestContext, _ Watch) (Summarizer, error) {
		return want, nil
	})

	got := getSummarizerFactory()
	require.NotNil(t, got)
	s, err := got(nil, Watch{})
	require.NoError(t, err)
	out, err := s.Summarize(nil, Watch{}, StatusCompleted, "", "")
	require.NoError(t, err)
	assert.Equal(t, "hello", out)
}

// TestSummarizerFactory_FactoryErrorIsSurfaced asserts that errors from
// the factory itself (e.g., DB unreachable in newLlmSummarizer) propagate
// rather than producing a nil-Summarizer interface — the executor's
// fallback logic distinguishes "no factory" from "factory failed".
func TestSummarizerFactory_FactoryErrorIsSurfaced(t *testing.T) {
	prev := getSummarizerFactory()
	t.Cleanup(func() { SetSummarizerFactory(prev) })

	want := errors.New("metastore unavailable")
	SetSummarizerFactory(func(_ *security.RequestContext, _ Watch) (Summarizer, error) {
		return nil, want
	})

	got := getSummarizerFactory()
	require.NotNil(t, got)
	s, err := got(nil, Watch{})
	assert.Nil(t, s)
	assert.ErrorIs(t, err, want)
}
