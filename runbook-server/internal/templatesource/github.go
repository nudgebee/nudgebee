package templatesource

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/go-github/v66/github"
	"gopkg.in/yaml.v3"
)

// maxArchiveBytes caps the decompressed archive read to guard against a hostile or
// runaway tarball (decompression bomb). The real template set is a few hundred KB.
const maxArchiveBytes = 32 << 20 // 32 MiB

// manifestEntry is one row of the CI-generated manifest.yaml in the repo.
type manifestEntry struct {
	Slug   string `yaml:"slug"`
	Path   string `yaml:"path"`
	SHA256 string `yaml:"sha256"`
}

// GitHubSource fetches templates from a public GitHub repo, pinned to an immutable
// commit SHA resolved from the configured ref, and verifies every file's sha256
// against the repo's manifest.yaml before returning. Any mismatch rejects the whole
// bundle (OK stays false) so a corrupt/incomplete fetch never reconciles.
type GitHubSource struct {
	owner  string
	repo   string
	ref    string
	token  string
	client *github.Client
	http   *http.Client
	// archiveBase is where tarballs are downloaded from; a field so a test can
	// point it at a local server.
	archiveBase string
}

// NewGitHubSource builds a source for "owner/repo" at ref (tag/branch/SHA). If token
// is non-empty the API + archive download are authenticated, enabling private repos.
func NewGitHubSource(repoSlug, ref, token string) *GitHubSource {
	owner, repo, _ := strings.Cut(repoSlug, "/")
	if ref == "" {
		ref = "main"
	}
	client := github.NewClient(nil)
	if token != "" {
		client = client.WithAuthToken(token)
	}
	return &GitHubSource{
		owner:  owner,
		repo:   repo,
		ref:    ref,
		token:  token,
		client: client,
		http:   &http.Client{Timeout: 60 * time.Second},

		archiveBase: "https://codeload.github.com",
	}
}

func (g *GitHubSource) Name() string { return SourceGitHub }

func (g *GitHubSource) Fetch(ctx context.Context) (*Bundle, error) {
	if g.owner == "" || g.repo == "" {
		return nil, fmt.Errorf("github: invalid repo %q/%q", g.owner, g.repo)
	}

	// 1. Resolve the ref to an immutable commit SHA (recorded as source_ref).
	sha, _, err := g.client.Repositories.GetCommitSHA1(ctx, g.owner, g.repo, g.ref, "")
	if err != nil {
		return nil, fmt.Errorf("github: failed to resolve ref %q: %w", g.ref, err)
	}

	// 2. Download the tarball pinned to that SHA (atomic snapshot, one request).
	archiveURL := fmt.Sprintf("%s/%s/%s/tar.gz/%s", g.archiveBase, g.owner, g.repo, sha)
	archive, err := g.downloadArchive(ctx, archiveURL)
	if err != nil {
		return nil, fmt.Errorf("github: %w", err)
	}
	files := archive.templates

	// 3. Parse the manifest and verify checksums before any DB write.
	manifestRaw, ok := files["manifest.yaml"]
	if !ok {
		return nil, fmt.Errorf("github: archive at %s has no manifest.yaml", sha)
	}
	var manifest []manifestEntry
	if err := yaml.Unmarshal(manifestRaw, &manifest); err != nil {
		return nil, fmt.Errorf("github: invalid manifest.yaml: %w", err)
	}

	valid := validTaskTypes()
	bundle := &Bundle{SourceName: SourceGitHub, Ref: sha}

	for _, entry := range manifest {
		if entry.Slug == "" || entry.Path == "" || entry.SHA256 == "" {
			return nil, fmt.Errorf("github: manifest entry missing slug/path/sha256: %+v", entry)
		}
		raw, ok := files[entry.Path]
		if !ok {
			return nil, fmt.Errorf("github: manifest references missing file %q", entry.Path)
		}
		rt, err := decode(raw, valid)
		if err != nil {
			return nil, fmt.Errorf("github: %w", err)
		}
		if rt.Checksum != entry.SHA256 {
			return nil, fmt.Errorf("github: checksum mismatch for %q (manifest %s, computed %s)", entry.Slug, entry.SHA256, rt.Checksum)
		}
		if rt.Slug != entry.Slug {
			return nil, fmt.Errorf("github: slug mismatch for %q (file declares %q)", entry.Slug, rt.Slug)
		}
		bundle.Templates = append(bundle.Templates, rt)
	}

	bundle.OK = len(bundle.Templates) > 0
	bundle.Extra, bundle.ExtraError = archive.extra, archive.extraErr
	return bundle, nil
}

// archiveFiles is what downloadArchive keeps, as repo-relative path -> bytes.
// Files an extension claims are kept apart from the templates so that nothing
// about them can change what the template sync sees.
type archiveFiles struct {
	// templates holds manifest.yaml and templates/*.yaml.
	templates map[string][]byte
	// extra holds the files a registered extension claims.
	extra map[string][]byte
	// extraErr is set, and extra emptied, when the claimed files were too many
	// or too large to keep.
	extraErr error
}

// downloadArchive GETs a .tar.gz and returns the files it keeps, with the leading
// "<owner>-<repo>-<sha>/" archive prefix stripped from their paths. Only
// manifest.yaml, templates/*.yaml and the files a registered extension claims
// are retained.
func (g *GitHubSource) downloadArchive(ctx context.Context, url string) (*archiveFiles, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	if g.token != "" {
		// Required for private repos; harmless for public ones.
		req.Header.Set("Authorization", "Bearer "+g.token)
	}
	resp, err := g.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to download archive: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("archive download returned status %d", resp.StatusCode)
	}

	gz, err := gzip.NewReader(io.LimitReader(resp.Body, maxArchiveBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to open gzip stream: %w", err)
	}
	defer func() { _ = gz.Close() }()

	out := &archiveFiles{templates: make(map[string][]byte), extra: make(map[string][]byte)}
	extraBytes := 0
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("failed to read tar: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		// Strip the leading archive directory component.
		_, rel, ok := strings.Cut(hdr.Name, "/")
		if !ok {
			continue
		}
		if claimedByExtension(rel) {
			if out.extraErr != nil {
				continue
			}
			// Read one byte past what is left of the budget, to tell "fits" from "too large".
			data, err := io.ReadAll(io.LimitReader(tr, int64(maxExtraBytes-extraBytes)+1))
			if err != nil {
				return nil, fmt.Errorf("failed to read %q from archive: %w", rel, err)
			}
			extraBytes += len(data)
			if len(out.extra) >= maxExtraFiles || extraBytes > maxExtraBytes {
				out.extra = nil
				out.extraErr = fmt.Errorf("extension files exceed the limit of %d files or %d bytes", maxExtraFiles, maxExtraBytes)
				continue
			}
			out.extra[rel] = data
			continue
		}
		if !templateOrManifest(rel) {
			continue
		}
		data, err := io.ReadAll(io.LimitReader(tr, maxArchiveBytes))
		if err != nil {
			return nil, fmt.Errorf("failed to read %q from archive: %w", rel, err)
		}
		out.templates[rel] = data
	}
	return out, nil
}
