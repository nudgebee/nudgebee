// Package gonjasafe builds Gonja templates that cannot load other templates.
//
// gonja.FromString compiles against gonja.DefaultEnv, whose loader reads any
// path from the server's disk, so include / import / from / extends in a
// template would return the contents of files on the API server. Template text
// in api-server comes from tenants (webhook label mappings, event-rule action
// parameters), so every template must be built with FromString from this
// package instead.
package gonjasafe

import (
	"errors"

	"github.com/noirbizarre/gonja"
	"github.com/noirbizarre/gonja/exec"
)

// ErrTemplateLoadingDisabled is returned for any attempt to load a template
// other than the one being compiled.
var ErrTemplateLoadingDisabled = errors.New("loading other templates is not allowed")

// FromString parses source the same way gonja.FromString does — same config,
// filters (including the ones api-server registers on gonja.DefaultEnv),
// statements, tests and globals — but with a loader that refuses every other
// template, at parse time (extends) and at render time (include, import, from).
func FromString(source string) (*exec.Template, error) {
	cfg := gonja.DefaultEnv.Inherit()
	cfg.Loader = refusingLoader{}
	return exec.NewTemplate("string", source, cfg)
}

type refusingLoader struct{}

func (refusingLoader) GetTemplate(string) (*exec.Template, error) {
	return nil, ErrTemplateLoadingDisabled
}
