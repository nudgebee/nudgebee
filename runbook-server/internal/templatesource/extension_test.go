package templatesource

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeExtension claims every path under its prefix and records what it is
// handed.
type fakeExtension struct {
	prefix string
	calls  []fakeCheck
}

type fakeCheck struct {
	source, ref string
	files       map[string][]byte
	err         error
}

func (f *fakeExtension) Claims(p string) bool { return strings.HasPrefix(p, f.prefix) }

func (f *fakeExtension) Check(_ context.Context, source, ref string, files map[string][]byte, err error, _ *slog.Logger) {
	f.calls = append(f.calls, fakeCheck{source, ref, files, err})
}

// withExtensions registers extensions for one test only.
func withExtensions(t *testing.T, exts ...Extension) {
	t.Helper()
	prev := extensions
	extensions = exts
	t.Cleanup(func() { extensions = prev })
}

// fakeGitHub serves one commit and its tarball the way the API and the archive
// host do, and returns a source pointed at it.
func fakeGitHub(t *testing.T, files map[string][]byte) *GitHubSource {
	t.Helper()
	const sha = "0123456789abcdef0123456789abcdef01234567"

	var archive bytes.Buffer
	gz := gzip.NewWriter(&archive)
	tw := tar.NewWriter(gz)
	for p, raw := range files {
		require.NoError(t, tw.WriteHeader(&tar.Header{Name: "acme-templates-" + sha + "/" + p, Mode: 0o644, Size: int64(len(raw)), Typeflag: tar.TypeReg}))
		_, err := tw.Write(raw)
		require.NoError(t, err)
	}
	require.NoError(t, tw.Close())
	require.NoError(t, gz.Close())

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/acme/templates/commits/main":
			_, _ = w.Write([]byte(sha))
		case "/acme/templates/tar.gz/" + sha:
			_, _ = w.Write(archive.Bytes())
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	source := NewGitHubSource("acme/templates", "main", "")
	base, err := url.Parse(server.URL + "/")
	require.NoError(t, err)
	source.client.BaseURL = base
	source.archiveBase = server.URL
	return source
}

// templateFiles returns one real template and the manifest that pins it.
func templateFiles(t *testing.T) map[string][]byte {
	t.Helper()
	raw, err := bundledTemplates.ReadFile("templates/rollout_restart_deployment.yaml")
	require.NoError(t, err)
	manifest := fmt.Sprintf("- slug: rollout_restart_deployment\n  path: templates/rollout_restart_deployment.yaml\n  sha256: %x\n", sha256.Sum256(raw))
	return map[string][]byte{
		"manifest.yaml": []byte(manifest),
		"templates/rollout_restart_deployment.yaml": raw,
	}
}

func TestGitHubFetchKeepsClaimedFilesBesideTemplates(t *testing.T) {
	withExtensions(t, &fakeExtension{prefix: "extra/"})
	files := templateFiles(t)
	files["extra/a.yaml"] = []byte("a")
	files["extra/.hidden/b.json"] = []byte("b")
	files["README.md"] = []byte("not kept")

	bundle, err := fakeGitHub(t, files).Fetch(context.Background())
	require.NoError(t, err)
	assert.True(t, bundle.OK)
	require.Len(t, bundle.Templates, 1)
	assert.Equal(t, "rollout_restart_deployment", bundle.Templates[0].Slug)
	assert.NoError(t, bundle.ExtraError)
	assert.Equal(t, map[string][]byte{"extra/a.yaml": []byte("a"), "extra/.hidden/b.json": []byte("b")}, bundle.Extra)
}

func TestGitHubFetchWithoutExtensionsKeepsOnlyTemplates(t *testing.T) {
	withExtensions(t)
	files := templateFiles(t)
	files["extra/a.yaml"] = []byte("a")

	bundle, err := fakeGitHub(t, files).Fetch(context.Background())
	require.NoError(t, err)
	assert.True(t, bundle.OK)
	assert.Len(t, bundle.Templates, 1)
	assert.Empty(t, bundle.Extra)
	assert.NoError(t, bundle.ExtraError)
}

// A claim on the template sync's own files is ignored, so an extension cannot
// hide a template.
func TestAnExtensionCannotClaimATemplate(t *testing.T) {
	withExtensions(t, &fakeExtension{prefix: ""})
	bundle, err := fakeGitHub(t, templateFiles(t)).Fetch(context.Background())
	require.NoError(t, err)
	assert.True(t, bundle.OK)
	assert.Len(t, bundle.Templates, 1)
	assert.Empty(t, bundle.Extra)
}

func TestGitHubFetchDropsClaimedFilesOverTheLimit(t *testing.T) {
	withExtensions(t, &fakeExtension{prefix: "extra/"})
	for name, extra := range map[string]map[string][]byte{
		"too many files": func() map[string][]byte {
			files := map[string][]byte{}
			for i := 0; i <= maxExtraFiles; i++ {
				files[fmt.Sprintf("extra/%d.txt", i)] = []byte("x")
			}
			return files
		}(),
		"too many bytes": {"extra/big.bin": make([]byte, maxExtraBytes+1)},
	} {
		t.Run(name, func(t *testing.T) {
			files := templateFiles(t)
			for p, raw := range extra {
				files[p] = raw
			}

			bundle, err := fakeGitHub(t, files).Fetch(context.Background())
			require.NoError(t, err)
			assert.True(t, bundle.OK, "templates still sync")
			assert.Len(t, bundle.Templates, 1)
			assert.ErrorContains(t, bundle.ExtraError, "extension files exceed the limit")
			assert.Empty(t, bundle.Extra)
		})
	}
}

// A template manifest cannot reach into claimed files: they are kept apart.
func TestGitHubFetchKeepsClaimedFilesOutOfTheTemplateLookup(t *testing.T) {
	withExtensions(t, &fakeExtension{prefix: "extra/"})
	files := templateFiles(t)
	template := files["templates/rollout_restart_deployment.yaml"]
	files["extra/template.yaml"] = template
	files["manifest.yaml"] = []byte(fmt.Sprintf("- slug: rollout_restart_deployment\n  path: extra/template.yaml\n  sha256: %x\n", sha256.Sum256(template)))

	_, err := fakeGitHub(t, files).Fetch(context.Background())
	assert.ErrorContains(t, err, "manifest references missing file")
}

// panickyExtension claims everything and panics when it is called.
type panickyExtension struct{}

func (panickyExtension) Claims(string) bool { return true }

func (panickyExtension) Check(context.Context, string, string, map[string][]byte, error, *slog.Logger) {
	panic("boom")
}

func TestAPanickingExtensionDoesNotStopTheOthers(t *testing.T) {
	after := &fakeExtension{prefix: "x/"}
	withExtensions(t, panickyExtension{}, after)
	var logs bytes.Buffer
	syncer := NewSyncer(nil, NewBundledSource(), slog.New(slog.NewTextHandler(&logs, nil)), false)

	assert.NotPanics(t, func() {
		syncer.checkExtensions(context.Background(), &Bundle{Ref: "abc", Extra: map[string][]byte{"x/1": []byte("1")}})
	})
	assert.Len(t, after.calls, 1, "the next extension still runs")
	assert.Contains(t, logs.String(), "template sync extension panicked, skipped")
	assert.Contains(t, logs.String(), "panic=boom")
}

func TestSyncerHandsEachExtensionItsFiles(t *testing.T) {
	a, b, idle := &fakeExtension{prefix: "a/"}, &fakeExtension{prefix: "b/"}, &fakeExtension{prefix: "c/"}
	withExtensions(t, a, b, idle)
	syncer := NewSyncer(nil, NewBundledSource(), slog.Default(), false)

	syncer.checkExtensions(context.Background(), &Bundle{Ref: "abc", Extra: map[string][]byte{"a/1": []byte("1"), "b/2": []byte("2")}})
	require.Len(t, a.calls, 1)
	assert.Equal(t, fakeCheck{SourceBundled, "abc", map[string][]byte{"a/1": []byte("1")}, nil}, a.calls[0])
	require.Len(t, b.calls, 1)
	assert.Equal(t, map[string][]byte{"b/2": []byte("2")}, b.calls[0].files)
	assert.Empty(t, idle.calls, "an extension with nothing to read is not called")

	// When the claimed files could not be kept, every extension hears why.
	limit := errors.New("over the limit")
	syncer.checkExtensions(context.Background(), &Bundle{Ref: "abc", ExtraError: limit})
	require.Len(t, idle.calls, 1)
	assert.Equal(t, limit, idle.calls[0].err)
}
