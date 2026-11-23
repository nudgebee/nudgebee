// Package gonjasafe builds Gonja templates that cannot load other templates.
//
// gonja.FromString backs every template with a file-system loader, so
// include / import / from / extends would read files from the server's disk.
// Template text here comes from users, alerts and LLMs, so every template in
// runbook-server must be built with FromString from this package instead.
package gonjasafe

import (
	"errors"
	"io"
	"strings"
	"sync/atomic"

	"github.com/nikolalohinski/gonja/v2"
	"github.com/nikolalohinski/gonja/v2/exec"
	"github.com/nikolalohinski/gonja/v2/loaders"
)

const rootID = "template"

// ErrTemplateLoadingDisabled is returned for any attempt to load a template
// other than the one being compiled.
var ErrTemplateLoadingDisabled = errors.New("loading other templates is not allowed")

// FromString parses source with the default Gonja config and environment, the
// same as gonja.FromString, but with a loader that refuses every other template.
func FromString(source string) (*exec.Template, error) {
	return exec.NewTemplate(rootID, gonja.DefaultConfig, &rootOnlyLoader{root: source}, gonja.DefaultEnvironment)
}

// rootOnlyLoader hands out the root source exactly once (to exec.NewTemplate)
// and refuses everything else, so a template can neither load another one nor
// load itself again.
type rootOnlyLoader struct {
	root   string
	served atomic.Bool
}

var _ loaders.Loader = (*rootOnlyLoader)(nil)

func (l *rootOnlyLoader) Read(identifier string) (io.Reader, error) {
	if identifier == rootID && l.served.CompareAndSwap(false, true) {
		return strings.NewReader(l.root), nil
	}
	return nil, ErrTemplateLoadingDisabled
}

func (l *rootOnlyLoader) Resolve(string) (string, error) {
	return "", ErrTemplateLoadingDisabled
}

func (l *rootOnlyLoader) Inherit(string) (loaders.Loader, error) {
	return nil, ErrTemplateLoadingDisabled
}
