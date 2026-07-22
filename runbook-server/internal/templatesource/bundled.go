package templatesource

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strings"
)

// bundledTemplates is the vendored, trusted snapshot shipped in the image. It is
// the offline/air-gapped default and is always present. Mirrors the embed idiom in
// internal/system/cron_config.go.
//
//go:embed templates/*.yaml
var bundledTemplates embed.FS

// BundledSource decodes the embedded YAML snapshot.
type BundledSource struct{}

func NewBundledSource() *BundledSource { return &BundledSource{} }

func (b *BundledSource) Name() string { return SourceBundled }

func (b *BundledSource) Fetch(_ context.Context) (*Bundle, error) {
	entries, err := fs.ReadDir(bundledTemplates, "templates")
	if err != nil {
		return nil, fmt.Errorf("bundled: failed to read embedded templates: %w", err)
	}

	valid := validTaskTypes()
	bundle := &Bundle{SourceName: SourceBundled, Ref: "embedded"}

	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".yaml") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names) // deterministic order

	for _, name := range names {
		raw, err := bundledTemplates.ReadFile("templates/" + name)
		if err != nil {
			return nil, fmt.Errorf("bundled: failed to read %s: %w", name, err)
		}
		rt, err := decode(raw, valid)
		if err != nil {
			// A bad embedded template is a build-time mistake; fail the whole fetch
			// rather than silently shipping a partial set.
			return nil, fmt.Errorf("bundled: %w", err)
		}
		bundle.Templates = append(bundle.Templates, rt)
	}

	bundle.OK = len(bundle.Templates) > 0
	return bundle, nil
}
