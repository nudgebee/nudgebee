package adapter

import (
	"context"
	"fmt"
	"io"
	"path"
	"strings"
	"time"

	"nudgebee/services/common"
	"nudgebee/services/internal/database"
	"nudgebee/services/security"
)

// pr_value_refresh_diff.go reads the lines a pull request adds, so a value
// refresh can confirm its numbers are on the branch instead of trusting the
// agent's report (#34959).

// githubAPIBase is the GitHub REST root. A variable so tests can serve the API
// from a fake; the PR-status lookups use the same public host.
var githubAPIBase = "https://api.github.com"

// prDiffMaxPages bounds pagination. 30 pages of 100 is GitHub's own ceiling for
// the pull request files endpoint.
const prDiffMaxPages = 30

// fetchPRAddedLines returns every line the pull request adds, across all of its
// files, without the leading "+". It fails, rather than returning a partial
// list, when the provider withholds the diff of a changed config file: a
// missing diff is not evidence that the values are absent.
func fetchPRAddedLines(ctx *security.RequestContext, dbms *database.DatabaseManager, tenantID string, meta prMetadata) ([]string, error) {
	integrationType := "github"
	if strings.EqualFold(meta.Provider, "gitlab") {
		integrationType = "gitlab"
	}
	cfg, err := loadGitIntegrationConfig(dbms, tenantID, integrationType)
	if err != nil {
		return nil, err
	}
	if integrationType == "gitlab" {
		return fetchGitLabMRAddedLines(meta, cfg)
	}
	return fetchGitHubPRAddedLines(ctx, meta, cfg)
}

type githubPRFile struct {
	Filename string `json:"filename"`
	Changes  int    `json:"changes"`
	Patch    string `json:"patch"`
}

func fetchGitHubPRAddedLines(ctx *security.RequestContext, meta prMetadata, cfg map[string]string) ([]string, error) {
	org, repo, number := prCoordinatesFromMeta(meta)
	if org == "" || repo == "" || number == "" {
		return nil, fmt.Errorf("cannot read org/repo/number from %s", meta.PRURL)
	}
	token, err := githubAPIToken(ctx.GetContext(), cfg)
	if err != nil {
		return nil, err
	}
	headers := map[string]string{"Authorization": "Bearer " + token, "Accept": "application/vnd.github+json"}

	var added []string
	for page := 1; page <= prDiffMaxPages; page++ {
		var files []githubPRFile
		url := fmt.Sprintf("%s/repos/%s/%s/pulls/%s/files?per_page=100&page=%d", githubAPIBase, org, repo, number, page)
		if err := getJSONInto(url, headers, &files); err != nil {
			return nil, err
		}
		for _, f := range files {
			if f.Patch == "" && f.Changes > 0 && isConfigFile(f.Filename) {
				return nil, fmt.Errorf("GitHub did not return the diff of %s", f.Filename)
			}
			added = append(added, addedLinesFromPatch(f.Patch)...)
		}
		if len(files) < 100 {
			return added, nil
		}
	}
	return nil, fmt.Errorf("pull request changes more files than can be read")
}

type gitlabMRDiff struct {
	NewPath   string `json:"new_path"`
	Diff      string `json:"diff"`
	TooLarge  bool   `json:"too_large"`
	Collapsed bool   `json:"collapsed"`
}

func fetchGitLabMRAddedLines(meta prMetadata, cfg map[string]string) ([]string, error) {
	base := cfg["url"]
	if base == "" {
		base = "https://gitlab.com"
	}
	projectPath, mrIID, err := parseGitLabMRURL(meta.PRURL, base)
	if err != nil {
		return nil, err
	}
	encodedPath := strings.ReplaceAll(projectPath, "/", "%2F")
	headers := map[string]string{"PRIVATE-TOKEN": cfg["password"]}

	var added []string
	for page := 1; page <= prDiffMaxPages; page++ {
		var diffs []gitlabMRDiff
		url := fmt.Sprintf("%s/api/v4/projects/%s/merge_requests/%s/diffs?per_page=100&page=%d", strings.TrimSuffix(base, "/"), encodedPath, mrIID, page)
		if err := getJSONInto(url, headers, &diffs); err != nil {
			return nil, err
		}
		for _, d := range diffs {
			if (d.TooLarge || d.Collapsed || d.Diff == "") && isConfigFile(d.NewPath) {
				return nil, fmt.Errorf("GitLab did not return the diff of %s", d.NewPath)
			}
			added = append(added, addedLinesFromPatch(d.Diff)...)
		}
		if len(diffs) < 100 {
			return added, nil
		}
	}
	return nil, fmt.Errorf("merge request changes more files than can be read")
}

// addedLinesFromPatch returns the lines a unified diff adds, without the "+".
func addedLinesFromPatch(patch string) []string {
	var added []string
	for _, line := range strings.Split(patch, "\n") {
		if strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++") {
			added = append(added, strings.TrimSuffix(line[1:], "\r"))
		}
	}
	return added
}

// isConfigFile is true for the file types rightsizing values live in. A
// withheld diff matters only for these; an image or lockfile without one is
// irrelevant to the check.
func isConfigFile(name string) bool {
	switch strings.ToLower(path.Ext(name)) {
	case ".yaml", ".yml", ".json", ".tpl", ".tf", ".tfvars", ".hcl":
		return true
	}
	return false
}

// githubAPIToken returns a token usable against the GitHub REST API: the stored
// token for token auth, or an installation token minted from the App
// installation id stored in its place for auth_type "application".
func githubAPIToken(ctx context.Context, cfg map[string]string) (string, error) {
	token := cfg["password"]
	if cfg["auth_type"] != "application" {
		return token, nil
	}
	tokCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	appToken, err := common.GetGithubAppInstallationToken(tokCtx, token)
	if err != nil {
		return "", fmt.Errorf("GitHub App token: %w", err)
	}
	return appToken, nil
}

// getJSONInto GETs url and decodes a 200 response into out.
func getJSONInto(url string, headers map[string]string, out any) error {
	resp, err := common.HttpGet(url, common.HttpWithHeaders(headers))
	if err != nil {
		return err
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode != 200 {
		return fmt.Errorf("%s returned %d", strings.SplitN(url, "?", 2)[0], resp.StatusCode)
	}
	return common.UnmarshalJson(raw, out)
}
