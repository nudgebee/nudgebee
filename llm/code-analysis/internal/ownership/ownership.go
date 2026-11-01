// Package ownership resolves and assigns code-fix owners without gating publication.
package ownership

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"nudgebee/code-analysis-agent/internal/gitprovider"
)

// Result travels with the PR response so companion issues can reuse the identity.
type Result struct {
	Provider   string `json:"provider"`
	Repository string `json:"repository"`
	Handle     string `json:"handle,omitempty"`
	Status     string `json:"status"` // unresolved, resolved, assigned, failed
	Warning    string `json:"warning,omitempty"`
	UserID     int    `json:"-"`
}

type Client struct {
	Info    *gitprovider.RepoInfo
	baseURL string
	token   string
	http    *http.Client
}

func New(repoURL, token string, provider gitprovider.GitProvider) (*Client, error) {
	info, err := gitprovider.ExtractRepoInfo(repoURL, provider)
	if err != nil {
		return nil, fmt.Errorf("resolve ownership repository: %w", err)
	}
	if info.Host == "" {
		return nil, fmt.Errorf("ownership requires an explicit repository host")
	}
	base := "https://" + info.Host
	if provider == gitprovider.GitProviderGitLab {
		base += "/api/v4"
	} else if info.Host == "github.com" {
		base = "https://api.github.com"
	} else {
		base += "/api/v3"
	}
	return &Client{Info: info, baseURL: base, token: token, http: &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

type apiError struct{ status int }

func (e apiError) Error() string { return fmt.Sprintf("provider API returned HTTP %d", e.status) }

// Do not include response bodies or URLs in errors: they can contain private emails.
func (c *Client) request(ctx context.Context, method, path string, body any, result any) error {
	var data []byte
	var err error
	if body != nil {
		data, err = json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode provider request: %w", err)
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("invalid provider request")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("provider request unavailable")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return apiError{resp.StatusCode}
	}
	if result != nil {
		if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(result); err != nil {
			return fmt.Errorf("invalid provider response")
		}
	}
	return nil
}

func (c *Client) repoPath() string {
	if c.Info.Provider == gitprovider.GitProviderGitLab {
		return "/projects/" + url.PathEscape(c.Info.FullPath)
	}
	return "/repos/" + c.Info.FullPath
}

type user struct {
	ID          int    `json:"id"`
	Login       string `json:"login"`
	Username    string `json:"username"`
	Type        string `json:"type"`
	Bot         bool   `json:"bot"`
	State       string `json:"state"`
	PublicEmail string `json:"public_email"`
}

func (u user) handle() string {
	if u.Login != "" {
		return u.Login
	}
	return u.Username
}
func (u user) human() bool {
	h := strings.ToLower(u.handle())
	return h != "" && !u.Bot && u.Type != "Bot" && !strings.HasSuffix(h, "[bot]") && h != "nudgebee-bot" && (u.State == "" || u.State == "active")
}

type commit struct {
	SHA    string    `json:"sha"`
	ID     string    `json:"id"`
	Author user      `json:"author"`
	Email  string    `json:"author_email"`
	Name   string    `json:"author_name"`
	Date   time.Time `json:"committed_date"`
	Commit struct {
		Author struct {
			Email string `json:"email"`
			Name  string `json:"name"`
		} `json:"author"`
		Committer struct {
			Date time.Time `json:"date"`
		} `json:"committer"`
	} `json:"commit"`
}

// Resolve queries history at the pre-fix SHA, so generated commits cannot elect
// themselves and shallow local checkouts never invent file authors at their boundary.
func (c *Client) Resolve(ctx context.Context, baseSHA string, paths []string) Result {
	r := Result{Provider: string(c.Info.Provider), Repository: c.Info.Host + "/" + c.Info.FullPath, Status: "unresolved"}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if baseSHA == "" || len(paths) == 0 {
		r.Warning = "No pre-fix file history available; published without an assignee."
		return r
	}
	// Bound request count as well as elapsed time. Do not silently pick an
	// owner from only the first paths of a larger change.
	if len(paths) > 100 {
		r.Warning = "Changed-file count exceeds ownership lookup budget; published without an assignee."
		return r
	}
	bySHA := map[string]commit{}
	for _, path := range paths {
		q := url.Values{"path": {path}, "per_page": {"100"}}
		endpoint := c.repoPath() + "/commits"
		if c.Info.Provider == gitprovider.GitProviderGitLab {
			endpoint = c.repoPath() + "/repository/commits"
			q.Set("ref_name", baseSHA)
		} else {
			q.Set("sha", baseSHA)
		}
		var commits []commit
		if err := c.request(ctx, http.MethodGet, endpoint+"?"+q.Encode(), nil, &commits); err != nil {
			r.Warning = fmt.Sprintf("Author history lookup failed (%s); published without an assignee.", err)
			return r
		}
		for _, v := range commits {
			if v.SHA == "" {
				v.SHA = v.ID
			} else {
				v.Date = v.Commit.Committer.Date
				v.Email = v.Commit.Author.Email
				v.Name = v.Commit.Author.Name
			}
			if v.SHA != "" {
				bySHA[v.SHA] = v
			}
		}
	}
	candidates := make([]commit, 0, len(bySHA))
	for _, v := range bySHA {
		candidates = append(candidates, v)
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].Date.Equal(candidates[j].Date) {
			return candidates[i].SHA < candidates[j].SHA
		}
		return candidates[i].Date.After(candidates[j].Date)
	})
	if len(candidates) > 100 {
		candidates = candidates[:100]
	}
	resolved := map[string]user{}
	eligible := map[string]bool{}
	for _, v := range candidates {
		if ctx.Err() != nil {
			break
		}
		if strings.EqualFold(v.Email, "bot@nudgebee.com") || strings.EqualFold(v.Name, "nudgebee-bot") {
			continue
		}
		u := v.Author
		if c.Info.Provider == gitprovider.GitProviderGitLab {
			var exists bool
			u, exists = resolved[v.Email]
			if !exists {
				u = c.gitlabAuthor(ctx, v.Email)
				resolved[v.Email] = u
			}
		}
		if !u.human() {
			continue
		}
		key := u.handle()
		ok, checked := eligible[key]
		if !checked {
			ok = c.assignable(ctx, u)
			eligible[key] = ok
		}
		if ok {
			r.Handle = key
			r.UserID = u.ID
			r.Status = "resolved"
			return r
		}
	}
	r.Warning = "No eligible human author resolved within the lookup budget; published without an assignee."
	return r
}

func (c *Client) gitlabAuthor(ctx context.Context, email string) user {
	if email == "" {
		return user{}
	}
	q := url.Values{"public_email": {email}, "per_page": {"100"}, "humans": {"true"}, "active": {"true"}}
	var users []user
	if c.request(ctx, http.MethodGet, "/users?"+q.Encode(), nil, &users) == nil {
		// List responses omit public_email and bot. Verify using the detail
		// endpoint rather than treating missing fields as identity evidence.
		if len(users) == 1 && users[0].ID > 0 {
			var detail user
			if c.request(ctx, http.MethodGet, "/users/"+strconv.Itoa(users[0].ID), nil, &detail) == nil && detail.ID == users[0].ID && strings.EqualFold(detail.PublicEmail, email) && detail.human() {
				return detail
			}
		}

	}
	// GitLab's generated private address is <id>-<username>@users.noreply.<host>.
	parts := strings.Split(email, "@")
	if len(parts) != 2 || !strings.EqualFold(parts[1], "users.noreply."+c.Info.Host) {
		return user{}
	}
	id, name, ok := strings.Cut(parts[0], "-")
	n, err := strconv.Atoi(id)
	if !ok || err != nil || n <= 0 {
		return user{}
	}
	var u user
	if c.request(ctx, http.MethodGet, "/users/"+strconv.Itoa(n), nil, &u) != nil || u.ID != n || u.Username != name {
		return user{}
	}
	return u
}

func (c *Client) assignable(ctx context.Context, u user) bool {
	if c.Info.Provider == gitprovider.GitProviderGitLab {
		var member user
		return c.request(ctx, http.MethodGet, c.repoPath()+"/members/all/"+strconv.Itoa(u.ID), nil, &member) == nil && member.human()
	}
	return c.request(ctx, http.MethodGet, c.repoPath()+"/assignees/"+url.PathEscape(u.Login), nil, nil) == nil
}

// ErrNotFound means the provider successfully confirmed that no matching PR exists.
var ErrNotFound = errors.New("no matching PR/MR")

// Find recovers partial CLI creation without retrying a non-idempotent create.
func (c *Client) Find(ctx context.Context, head, base string) (string, error) {
	if c == nil {
		return "", fmt.Errorf("ownership client unavailable")
	}
	q := url.Values{"state": {"open"}, "head": {c.Info.Owner + ":" + head}, "base": {base}}
	endpoint := c.repoPath() + "/pulls"
	if c.Info.Provider == gitprovider.GitProviderGitLab {
		endpoint = c.repoPath() + "/merge_requests"
		q = url.Values{"state": {"opened"}, "source_branch": {head}, "target_branch": {base}, "scope": {"all"}}
	}
	var prs []struct {
		URL             string `json:"html_url"`
		WebURL          string `json:"web_url"`
		SourceProjectID int    `json:"source_project_id"`
		TargetProjectID int    `json:"target_project_id"`
		Head            struct {
			Repo struct {
				FullName string `json:"full_name"`
			} `json:"repo"`
		} `json:"head"`
	}

	if err := c.request(ctx, http.MethodGet, endpoint+"?"+q.Encode(), nil, &prs); err != nil {
		return "", err
	}
	var matches []string
	for _, pr := range prs {
		if c.Info.Provider == gitprovider.GitProviderGitLab {
			if pr.SourceProjectID > 0 && pr.SourceProjectID == pr.TargetProjectID {
				matches = append(matches, pr.WebURL)
			}
		} else if strings.EqualFold(pr.Head.Repo.FullName, c.Info.FullPath) {
			matches = append(matches, pr.URL)
		}
	}
	if len(matches) == 0 {
		return "", ErrNotFound
	}
	if len(matches) != 1 {
		return "", fmt.Errorf("could not uniquely recover created PR/MR")
	}
	return matches[0], nil
}

// VerifyAndAssign is idempotent. It never closes or recreates a published artifact.
func (c *Client) VerifyAndAssign(ctx context.Context, prURL string, r Result) Result {
	if r.Handle == "" {
		return r
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	u, err := url.Parse(prURL)
	if err != nil || !strings.EqualFold(u.Host, c.Info.Host) || u.Scheme != "https" {
		r.Status = "failed"
		r.Warning = "Unable to verify assignment: unexpected PR/MR URL."
		return r
	}
	prefix := "/" + c.Info.FullPath + "/pull/"
	if c.Info.Provider == gitprovider.GitProviderGitLab {
		prefix = "/" + c.Info.FullPath + "/-/merge_requests/"
	}
	comparisonPath := u.Path
	if c.Info.Provider == gitprovider.GitProviderGitHub {
		comparisonPath = strings.ToLower(comparisonPath)
		prefix = strings.ToLower(prefix)
	}
	if !strings.HasPrefix(comparisonPath, prefix) {
		r.Status = "failed"
		r.Warning = "Unable to verify assignment: unexpected repository."
		return r
	}
	n, err := strconv.Atoi(u.Path[len(prefix):])
	if err != nil || n <= 0 {
		r.Status = "failed"
		r.Warning = "Unable to verify assignment: invalid PR/MR number."
		return r
	}
	endpoint := c.repoPath() + "/issues/" + strconv.Itoa(n)
	if c.Info.Provider == gitprovider.GitProviderGitLab {
		endpoint = c.repoPath() + "/merge_requests/" + strconv.Itoa(n)
	}
	for attempt := 0; attempt < 3; attempt++ {
		var state struct {
			Assignees []user `json:"assignees"`
		}
		err = c.request(ctx, http.MethodGet, endpoint, nil, &state)
		if err == nil {
			for _, assignee := range state.Assignees {
				if strings.EqualFold(assignee.handle(), r.Handle) {
					r.Status = "assigned"
					r.Warning = ""
					return r
				}
			}
		}
		if attempt == 2 || ctx.Err() != nil {
			break
		}
		if c.Info.Provider == gitprovider.GitProviderGitLab {
			err = c.request(ctx, http.MethodPut, endpoint, map[string]any{"assignee_ids": []int{r.UserID}}, nil)
		} else {
			err = c.request(ctx, http.MethodPost, endpoint+"/assignees", map[string]any{"assignees": []string{r.Handle}}, nil)
		}
		if e, ok := err.(apiError); ok && e.status >= 400 && e.status < 500 && e.status != 429 {
			break
		}
		timer := time.NewTimer(time.Duration(attempt+1) * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
		case <-timer.C:
		}
	}
	r.Status = "failed"
	r.Warning = "PR/MR published, but assignment could not be verified; please assign it manually."
	return r
}
