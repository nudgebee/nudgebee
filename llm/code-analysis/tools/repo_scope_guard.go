package tools

import (
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"sync"
)

// foreignPRLookupPatterns match a command that asks GitHub about a specific pull
// request or issue, capturing the owner and repo it addresses. Only PR and issue
// endpoints are matched: reading another repository's source (raw file fetches,
// upstream release notes) stays allowed, since that is legitimate research.
// Reporting on another repository's PR never is.
//
// The API-path pattern deliberately anchors on `repos/` rather than on the
// api.github.com host, so it catches `gh api repos/o/r/pulls/1` and
// `gh api /repos/o/r/pulls/1` as well as the full-URL curl form — `gh api` is
// the idiomatic way to reach the API and omits the host entirely.
var foreignPRLookupPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\brepos/([\w.-]+)/([\w.-]+)/(?:pulls|issues)\b`),
	regexp.MustCompile(`(?i)https?://(?:www\.)?github\.com/([\w.-]+)/([\w.-]+)/(?:pull|issues)/\d+`),
	// `gh pr view 123 --repo owner/name` / `-R owner/name`. The flag alone is not
	// enough — `gh repo view --repo o/r` is fine — so require a pr/issue command.
	regexp.MustCompile(`(?i)\bgh\s+(?:pr|issue)\b[^|;&]*?(?:--repo|-R)[= ]+([\w.-]+)/([\w.-]+)`),
}

// originRepoPattern extracts owner/repo from a GitHub remote URL in either the
// https or ssh form.
var originRepoPattern = regexp.MustCompile(`(?i)github\.com[:/]([\w.-]+)/([\w.-]+?)(?:\.git)?/?$`)

// ghContentFlags carry free text rather than a target: a title, a body, a REST
// field value. Their contents are excluded from scope matching.
var ghContentFlags = map[string]bool{
	"--body": true, "-b": true,
	"--title": true, "-t": true,
	"--body-file": true,
	"-f":          true, "-F": true,
}

// ghCommandForScopeCheck renders gh args as a command line for scope matching,
// dropping the values of content-bearing flags.
//
// A pull request body legitimately quotes other repositories — our own generated
// descriptions cite the PRs and runs they came from — and that prose must not be
// read as a lookup. Without this, `gh pr create --body "...see metabase#1..."`
// would be refused and the orchestrator could not open a PR at all. The target
// of a gh command is always a positional argument or --repo, never body text.
func ghCommandForScopeCheck(args []string) string {
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, "gh")
	for i := 0; i < len(args); i++ {
		if ghContentFlags[args[i]] {
			i++ // skip the flag's value too
			continue
		}
		if key, _, found := strings.Cut(args[i], "="); found && ghContentFlags[key] {
			continue
		}
		parts = append(parts, args[i])
	}
	return strings.Join(parts, " ")
}

// repoScopeGuard refuses commands that ask GitHub about a pull request or issue
// in a repository other than the one checked out. Embedded by every tool that
// can reach the GitHub API — the cli tool and the gh tool — because a guard on
// only one of them is no guard at all: the specialist agents hold both.
//
// This exists because an agent asked to "confirm the status of PR #35094" ran
// `curl -s https://api.github.com/repos/metabase/metabase/pulls/35094`, got a
// real response for an unrelated project's PR, and reported that PR's title and
// merge state as the outcome of its own work. A wrong-repo answer that returns
// HTTP 200 is indistinguishable from a right one downstream, so it has to be
// stopped before the command runs.
type repoScopeGuard struct {
	mu           sync.Mutex
	scopeRepo    string
	scopeResolve bool
}

// checkCommand reports whether command addresses a PR or issue outside the
// repository checked out in workingDir, with a message explaining the refusal.
//
// Fails open: when the command names no PR/issue, or the checkout's own repo
// cannot be determined, nothing is blocked. This is a correctness aid, not a
// sandbox — blocking work we cannot classify would be worse than the bug.
func (g *repoScopeGuard) checkCommand(command, workingDir string) (bool, string) {
	var referenced [][2]string
	for _, re := range foreignPRLookupPatterns {
		for _, m := range re.FindAllStringSubmatch(command, -1) {
			referenced = append(referenced, [2]string{m[1], m[2]})
		}
	}
	if len(referenced) == 0 {
		return false, ""
	}

	scope := g.resolveScopeRepo(workingDir)
	if scope == "" {
		return false, ""
	}

	for _, ref := range referenced {
		if !strings.EqualFold(ref[0]+"/"+ref[1], scope) {
			return true, fmt.Sprintf(
				"Command blocked: it looks up a pull request or issue in %s/%s, but this analysis is scoped to %s. "+
					"Answering from another repository's PR would report someone else's work as your own. "+
					"Query %s instead, or say you could not verify the PR status.",
				ref[0], ref[1], scope, scope)
		}
	}
	return false, ""
}

// resolveScopeRepo returns "owner/repo" for the checkout in workingDir, or ""
// when it cannot be determined. Cached per tool instance: a single analysis
// works in one clone, and the remote does not change mid-run.
func (g *repoScopeGuard) resolveScopeRepo(workingDir string) string {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.scopeResolve {
		return g.scopeRepo
	}
	g.scopeResolve = true

	cmd := exec.Command("git", "remote", "get-url", "origin")
	cmd.Dir = workingDir
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	if m := originRepoPattern.FindStringSubmatch(strings.TrimSpace(string(out))); len(m) == 3 {
		g.scopeRepo = m[1] + "/" + m[2]
	}
	return g.scopeRepo
}
