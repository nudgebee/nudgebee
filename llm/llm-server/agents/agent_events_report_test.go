package agents

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// The RCA Format settings editor in the frontend relies on this constant being
// a usable Markdown skeleton — it is served verbatim as the "Nudgebee default"
// starting template. Lock its shape so a future edit can't quietly break it.
func TestDefaultRCAFormat_Shape(t *testing.T) {
	assert.NotEmpty(t, DefaultRCAFormat)
	assert.True(t, strings.HasPrefix(DefaultRCAFormat, "# "), "should start with a top-level Markdown heading")
	assert.NotContains(t, DefaultRCAFormat, "TODO")
	assert.Contains(t, DefaultRCAFormat, "5-Whys")
}
