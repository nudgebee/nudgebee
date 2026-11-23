package knowledge

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// GitHubTokenResolver returns the tenant's GitHub API base URL and token, or
// an empty token when the tenant has no GitHub integration (public repos still
// work, at GitHub's lower anonymous rate limit). Set by the api package.
var GitHubTokenResolver func(ctx context.Context, tenantId string) (apiURL, token string, err error)

var skillSourceHTTP = &http.Client{Timeout: 2 * time.Minute}

// githubAnonymousAPIBase is the API used without an integration (a variable
// for tests).
var githubAnonymousAPIBase = "https://api.github.com"

// githubCommitETags remembers, per repository and ref, the ETag and commit of
// the last commit lookup. Asking again with If-None-Match gets a 304 when the
// ref hasn't moved, and GitHub doesn't count a 304 against the rate limit, so
// a scheduled sync of an unchanged repository costs nothing. Kept in memory:
// after a restart the first lookup is simply a full one.
var githubCommitETags sync.Map // key -> githubCommitETag

type githubCommitETag struct{ etag, commit string }

// FetchGitHubSkillSource resolves ref (a branch, or "" for the default branch)
// to a commit. When that commit is knownCommit it returns no archive: nothing
// changed. Otherwise it returns the repo tarball at that commit as a stream,
// for the caller to read and close.
func FetchGitHubSkillSource(ctx context.Context, tenantId, repo, ref, knownCommit string) (string, io.ReadCloser, error) {
	apiURL, token := githubAnonymousAPIBase, ""
	var integrationErr error
	if GitHubTokenResolver != nil {
		u, t, err := GitHubTokenResolver(ctx, tenantId)
		if err != nil {
			// A broken or expired integration must not block public repos:
			// fetch anonymously, and name the integration if that fails too.
			integrationErr = err
			slog.Warn("skill source: GitHub integration unavailable, fetching anonymously", "tenant_id", tenantId, "repo", repo, "error", err)
		} else {
			if u != "" {
				apiURL = strings.TrimRight(u, "/")
			}
			token = t
		}
	}
	commit, tarball, err := fetchGitHubSkillSource(ctx, tenantId, apiURL, token, repo, ref, knownCommit)
	if err != nil && integrationErr != nil {
		return "", nil, fmt.Errorf("%w (the GitHub integration could not be used: %v)", err, integrationErr)
	}
	return commit, tarball, err
}

func fetchGitHubSkillSource(ctx context.Context, tenantId, apiURL, token, repo, ref, knownCommit string) (string, io.ReadCloser, error) {
	if ref == "" {
		ref = "HEAD"
	}

	commitURL := fmt.Sprintf("%s/repos/%s/commits/%s", apiURL, repo, url.PathEscape(ref))
	// Keyed by tenant: a 304 answered for one tenant's credentials must not
	// stand in for a lookup made with another's. Not by token, which an App
	// installation renews hourly: the map would grow and keep expired ones.
	cacheKey := tenantId + "\x00" + commitURL
	var known githubCommitETag
	if v, ok := githubCommitETags.Load(cacheKey); ok {
		known = v.(githubCommitETag)
	}
	resp, err := githubGet(ctx, commitURL, token, "application/vnd.github.sha", known.etag)
	if err != nil {
		return "", nil, err
	}
	commit := known.commit
	if resp.StatusCode == http.StatusNotModified {
		_ = resp.Body.Close()
	} else {
		body, err := readLimited(resp.Body, 1<<10)
		_ = resp.Body.Close()
		if err != nil {
			return "", nil, err
		}
		commit = strings.TrimSpace(string(body))
		if etag := resp.Header.Get("ETag"); etag != "" && len(commit) >= 7 {
			githubCommitETags.Store(cacheKey, githubCommitETag{etag: etag, commit: commit})
		}
	}
	if len(commit) < 7 {
		return "", nil, fmt.Errorf("could not resolve %q in %s", ref, repo)
	}
	if commit == knownCommit {
		return commit, nil, nil
	}
	tarball, err := githubGet(ctx, fmt.Sprintf("%s/repos/%s/tarball/%s", apiURL, repo, commit), token, "application/vnd.github+json", "")
	if err != nil {
		return "", nil, err
	}
	return commit, tarball.Body, nil
}

// githubGet sends one GitHub API request and maps its errors. A 2xx, or a 304
// when an etag was sent, is returned for the caller to read and close.
func githubGet(ctx context.Context, u, token, accept, etag string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", accept)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}
	resp, err := skillSourceHTTP.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 300 || (etag != "" && resp.StatusCode == http.StatusNotModified) {
		return resp, nil
	}
	_ = resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusNotFound:
		return nil, fmt.Errorf("repository or branch not found (private repositories need the GitHub integration)")
	case http.StatusUnprocessableEntity:
		// GitHub answers an unknown ref with 422.
		return nil, fmt.Errorf("branch not found in the repository")
	case http.StatusForbidden, http.StatusTooManyRequests:
		return nil, fmt.Errorf("GitHub refused the request (%d): rate limit or missing access", resp.StatusCode)
	}
	return nil, fmt.Errorf("GitHub returned %d", resp.StatusCode)
}

func readLimited(r io.Reader, max int64) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > max {
		return nil, fmt.Errorf("GitHub response is larger than %d bytes", max)
	}
	return b, nil
}
