package settings

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestStore_EnvOnly(t *testing.T) {
	// No loader (OSS): capture follows the ceiling (always allowed here), DLP mode
	// is always the env default — no per-tenant override exists.
	s := NewStore(nil, 0)
	assert.True(t, s.CaptureAllowed("t1"))
	assert.Equal(t, "detect", s.EgressMode("t1", "detect"))
	assert.Equal(t, "", s.EgressMode("t1", ""))
}

func TestStore_WithLoader(t *testing.T) {
	off, enforce := "off", "enforce"
	load := func(_ context.Context) (map[string]TenantSettings, error) {
		return map[string]TenantSettings{
			"cap":     {CaptureBody: true},
			"dlp-off": {EgressMode: &off},
			"dlp-enf": {EgressMode: &enforce},
		}, nil
	}
	s := NewStore(load, time.Hour) // loads synchronously before returning
	defer s.Close()

	// Capture opt-in: only a tenant with capture_body=true; a tenant with no row
	// defaults to off (off-until-opt-in).
	assert.True(t, s.CaptureAllowed("cap"))
	assert.False(t, s.CaptureAllowed("no-row"))

	// DLP: an explicit override wins (including "off"); no override inherits env.
	assert.Equal(t, "off", s.EgressMode("dlp-off", "detect"))
	assert.Equal(t, "enforce", s.EgressMode("dlp-enf", "detect"))
	assert.Equal(t, "detect", s.EgressMode("cap", "detect"))    // row exists, no egress override → inherit
	assert.Equal(t, "detect", s.EgressMode("no-row", "detect")) // no row → inherit
}

func TestPackageAccessors_DelegateToActiveStore(t *testing.T) {
	// The hot-path accessors delegate to the active store; an env-only store returns
	// the env defaults. (Also exercises the nil-store default path indirectly: the
	// accessors are nil-safe by construction.)
	SetActive(NewStore(nil, 0))
	assert.True(t, CaptureAllowed("t1"))
	assert.Equal(t, "detect", EgressMode("t1", "detect"))
}
