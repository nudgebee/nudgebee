package templatesource

import (
	"context"
	"fmt"
)

// URLSource (phase 2) will fetch a tarball + manifest from a configurable mirror
// URL, for environments that cannot reach GitHub directly but are not fully
// air-gapped. Stubbed for now so the source factory and config wiring are complete.
type URLSource struct {
	url string
}

func NewURLSource(url string) *URLSource { return &URLSource{url: url} }

func (u *URLSource) Name() string { return SourceURL }

func (u *URLSource) Fetch(_ context.Context) (*Bundle, error) {
	return nil, fmt.Errorf("url template source is not implemented yet")
}
