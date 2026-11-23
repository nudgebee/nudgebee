package templatesource

import (
	"context"
	"log/slog"
	"strings"
)

// Extension reads content a GitHub source carries beside its templates. The
// files it claims are kept apart from the templates, within one budget shared by
// every extension, and handed to it once per sync. It only reads and reports: it
// cannot change the templates or whether the sync succeeds. With no extension
// registered, a source keeps only its templates.
type Extension interface {
	// Claims reports whether the extension reads this repo-relative path.
	Claims(path string) bool
	// Check receives the files it claimed from one fetch, or err when the
	// claimed files were too many or too large to keep, and reports what it
	// found to logger.
	Check(ctx context.Context, source, ref string, files map[string][]byte, err error, logger *slog.Logger)
}

var extensions []Extension

// RegisterExtension adds an extension to every later template sync. Call it
// from package init().
func RegisterExtension(e Extension) {
	extensions = append(extensions, e)
}

// Limits on the extension files kept from one archive. They bound the memory
// and time a hostile or broken repository can take.
const (
	maxExtraFiles = 5000
	maxExtraBytes = 32 << 20 // 32 MiB in total
)

// claimedByExtension reports whether an extension claims a path. The template
// sync's own files are never handed to an extension, so a claim cannot hide a
// template.
func claimedByExtension(path string) bool {
	if templateOrManifest(path) {
		return false
	}
	for _, e := range extensions {
		if e.Claims(path) {
			return true
		}
	}
	return false
}

// checkExtensions hands each extension the files it claimed from the bundle.
func (s *Syncer) checkExtensions(ctx context.Context, bundle *Bundle) {
	for _, e := range extensions {
		var files map[string][]byte
		for p, raw := range bundle.Extra {
			if !e.Claims(p) {
				continue
			}
			if files == nil {
				files = map[string][]byte{}
			}
			files[p] = raw
		}
		if files == nil && bundle.ExtraError == nil {
			continue
		}
		s.runCheck(ctx, e, bundle.Ref, files, bundle.ExtraError)
	}
}

// runCheck calls one extension, so that even a panic in it cannot stop the
// template sync.
func (s *Syncer) runCheck(ctx context.Context, e Extension, ref string, files map[string][]byte, err error) {
	defer func() {
		if r := recover(); r != nil {
			s.logger.Error("template sync extension panicked, skipped", "source", s.source.Name(), "ref", ref, "panic", r)
		}
	}()
	e.Check(ctx, s.source.Name(), ref, files, err, s.logger)
}

// templateOrManifest reports whether a path is one the template sync itself reads.
func templateOrManifest(rel string) bool {
	return rel == "manifest.yaml" || (strings.HasPrefix(rel, "templates/") && strings.HasSuffix(rel, ".yaml"))
}
