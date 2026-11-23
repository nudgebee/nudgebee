package events

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizeCauseKey(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"  OOMKilled ", "oom"},
		{"Out-of-Memory", "oom"},
		{"oom_killed", "oom"},
		{"CrashLoopBackOff", "crashloop"},
		{"crash  loop", "crashloop"},
		{"Deployment/checkout", "deploy checkout"},
		{"deployment.checkout-api", "deploy checkout api"},
		{"Memory   limit", "memory limit"},
		{"timed out", "timeout"},
		{"", ""},
		{" - _ ", ""},
		// Kubernetes reasons as the API writes them, and the ways a model spells them.
		{"ImagePullBackOff", "image pull"},
		{"ImagePullBackoff", "image pull"},
		{"image pull backoff", "image pull"},
		{"image pull back off", "image pull"},
		{"imagepullbackoff", "image pull"},
		{"imagepull backoff", "image pull"},
		{"ErrImagePull", "image pull"},
		{"err image pull", "image pull"},
		{"CrashLoopBackOff", "crashloop"},
		{"crashloop backoff", "crashloop"},
		{"crash loop backoff", "crashloop"},
		{"OOMKilled", "oom"},
		{"OOM killed", "oom"},
		{"CreateContainerConfigError", "create container config error"},
		{"createcontainerconfigerror", "create container config error"},
		{"create container config error", "create container config error"},
	} {
		assert.Equal(t, tc.want, normalizeCauseField(tc.in), tc.in)
	}
	assert.Equal(t, CauseKey{What: "deploy web", How: "oom", Against: "memory limit"},
		NormalizeCauseKey(CauseKey{What: " Deployment/Web", How: "OOMKilled", Against: "memory-limit "}))
}

func TestSameCause(t *testing.T) {
	a := CauseKey{What: "Deployment/checkout", How: "OOMKilled", Against: "memory_limit"}
	b := CauseKey{What: "deployment checkout", How: "out of memory", Against: "Memory Limit"}
	assert.True(t, SameCause(a, b), "same cause written two ways")
	assert.False(t, SameCause(a, CauseKey{What: "deployment checkout", How: "crashloop", Against: "memory limit"}))
	// Any empty field: not comparable, never the same.
	assert.False(t, SameCause(CauseKey{What: "x", How: "oom"}, CauseKey{What: "x", How: "oom"}))
	assert.False(t, CauseKey{}.Comparable())
	assert.False(t, CauseKey{What: "x", How: " - ", Against: "y"}.Comparable(), "a field of separators only is empty")
	assert.True(t, a.Comparable())
}

// The stored shape is C8's: {what, how, against}.
func TestCauseKeyJSONShape(t *testing.T) {
	raw, err := json.Marshal(CauseKey{What: "deploy checkout", How: "oom", Against: "memory limit"})
	require.NoError(t, err)
	assert.JSONEq(t, `{"what":"deploy checkout","how":"oom","against":"memory limit"}`, string(raw))
}

// The alert's chat is event-<fp>; an RCA session and an investigation's own session are not it.
func TestAlertFingerprintOfSession(t *testing.T) {
	fp, ok := AlertFingerprintOfSession("event-fp-1")
	assert.True(t, ok)
	assert.Equal(t, "fp-1", fp)
	for _, s := range []string{"event-rca-fp-1", "C1-1700000000.1", "event-"} {
		_, ok := AlertFingerprintOfSession(s)
		assert.False(t, ok, s)
	}
}
