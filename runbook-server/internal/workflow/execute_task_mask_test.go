package workflow

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

// A Task Runner call skips TaskWrapper's scrubbing, so its error and result
// must not carry the account's secret values back to the caller.
func TestMaskTaskOutcomeHidesSecrets(t *testing.T) {
	secret := "s3cr3t-token-value"
	result, err := maskTaskOutcome(
		map[string]any{"data": "called https://api.example.com/?token=" + secret},
		errors.New("http request failed: GET https://api.example.com/?token="+secret+": 401"),
		[]string{secret},
	)
	assert.Error(t, err)
	assert.NotContains(t, err.Error(), secret)
	assert.Contains(t, err.Error(), "http request failed")
	assert.NotContains(t, result.(map[string]any)["data"], secret)

	plain := errors.New("boom")
	_, err = maskTaskOutcome(nil, plain, nil)
	assert.Same(t, plain, err)
}
