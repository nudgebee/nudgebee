package agents

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"

	"github.com/google/uuid"
)

// The repository a code analysis runs on can come from a model: a planner fills
// git_repo from the words of its brief, and a name that only looks like a
// repository ("org/service") is cloned as if it were one. The workspace then
// spends a whole run finding out it does not exist. So before anything is
// dispatched, the git host is asked the question a clone asks first — can this
// credential read this repository? — and a definite "no" stops the run here,
// with what the server knows returned to the caller so it can correct itself.
//
// One case is not turned down: a planner's read-only request for a repository
// the account's integrations do not list and the host does not know. There the
// server resolves the repository itself, as it does when none is named, and
// the result says which one it analysed (callerRepoGivesWay).
//
// Everywhere else nothing is substituted. "Not found" from a git host also
// means "this credential does not cover it", so picking another repository for
// a change, for a listed repository, or for one a person named could analyse,
// or raise a pull request on, the wrong one.

// repoAccess is the git host's answer for one repository and one credential.
type repoAccess int

const (
	// repoAccessUnknown is every answer that is not a definite yes or no: the
	// host was unreachable or slow, refused the credential, rate-limited the
	// request, or the reference is not one this check can ask about. The run
	// goes ahead exactly as it did before this check existed.
	repoAccessUnknown repoAccess = iota
	repoAccessReadable
	repoAccessNotFound
)

// repoProbeTimeout bounds the single request the check makes. It sits in front
// of every analysis, so a slow git host must cost seconds, not the run.
const repoProbeTimeout = 5 * time.Second

var repoProbeClient = &http.Client{Timeout: repoProbeTimeout}

// repoSaaSHosts are the hosts the workspace expands a bare "owner/repo" to, by
// provider. A self-hosted host cannot be inferred from a bare name.
var repoSaaSHosts = map[string]string{
	"github": "github.com",
	"gitlab": "gitlab.com",
}

// isRepoReference reports whether repo could name a repository at all. Only a
// value with no scheme and no "owner/repo" shape fails: the workspace refuses
// such a value outright, so nothing that runs today is turned away. Anything
// carrying a scheme or an ssh remote is left for the workspace to judge.
func isRepoReference(repo string) bool {
	if strings.Contains(repo, "://") || strings.HasPrefix(repo, "git@") {
		return true
	}
	_, ok := parseRepoRef(repo, nil)
	return ok
}

// repoProbeURL returns the smart-HTTP discovery URL for repo — the first
// request `git clone` itself makes — or "" when repo is not cloned over https
// from a host this check can address. The URL is the one the workspace hands
// to git, port and case included, so the answer here is the answer the clone
// would get. Plain http is left alone: the check would have to send the
// credential in the clear to ask.
func repoProbeURL(repo, provider string) string {
	repo = strings.TrimSpace(repo)
	cloneURL := ""
	switch {
	case strings.HasPrefix(strings.ToLower(repo), "https://"):
		parsed, err := url.Parse(repo)
		if err != nil || parsed.Hostname() == "" || parsed.RawQuery != "" || parsed.Fragment != "" {
			return ""
		}
		// The workspace rewrites REST API URLs to clone URLs; asking the API host
		// for a git endpoint would answer "not found" for a repository that clones.
		if strings.EqualFold(parsed.Hostname(), "api.github.com") {
			return ""
		}
		// Credentials embedded in the URL are what the workspace clones with when
		// no token resolves. Asking without them could refuse a repository they
		// can read, and they are not this check's to send.
		if parsed.User != nil {
			return ""
		}
		cloneURL = strings.TrimRight(parsed.String(), "/")
	case strings.Contains(repo, "://"), strings.HasPrefix(repo, "git@"):
		return ""
	default:
		host := repoSaaSHosts[strings.ToLower(provider)]
		if host == "" || strings.Count(repo, "/") != 1 {
			return ""
		}
		if _, ok := parseRepoRef(repo, nil); !ok {
			return ""
		}
		cloneURL = "https://" + host + "/" + strings.TrimSuffix(repo, ".git") + ".git"
	}
	return cloneURL + "/info/refs?service=git-upload-pack"
}

// probeRepoAccess makes the one request. Only 404 counts as "not found": it is
// what a git host answers for a repository that does not exist or that the
// credential cannot see. A 401 or 403 says the credential itself was refused,
// and 429 or 5xx say nothing about the repository, so none of them may stop a
// run that could still work.
//
// With no token there is nothing to ask: the question is whether this
// credential can read the repository, and a host that hides private
// repositories from anonymous requests would answer "not found" for one a
// failed token mint, not a wrong name, is keeping out of reach.
func probeRepoAccess(ctx context.Context, client *http.Client, probeURL, token string) repoAccess {
	if probeURL == "" || token == "" {
		return repoAccessUnknown
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, probeURL, nil)
	if err != nil {
		return repoAccessUnknown
	}
	// Git hosts serve the smart protocol to git clients. The user name follows
	// the workspace's own rule for the clone — "gitlab" anywhere in the URL —
	// not the provider llm-server detected, so both present the same identity.
	req.Header.Set("User-Agent", "git/2.0 (nudgebee repository check)")
	user := "x-access-token"
	if strings.Contains(probeURL, "gitlab") {
		user = "oauth2"
	}
	req.SetBasicAuth(user, token)
	resp, err := client.Do(req)
	if err != nil {
		return repoAccessUnknown
	}
	// A short body (every refusal is one) is read to the end so the connection
	// can be reused by the next check; a long ref listing is not downloaded.
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		_ = resp.Body.Close()
	}()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return repoAccessNotFound
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return repoAccessReadable
	default:
		return repoAccessUnknown
	}
}

// maxListedRepos caps how many of the account's repositories an error lists.
const maxListedRepos = 15

// unreadableRepoError is what the caller is told when nothing was dispatched.
// It carries facts only — which repository, where it came from, and the
// repositories the server does know — because the caller is usually a model
// that had no way to know which repository holds the code it is asking about.
// fromPlanner is true when another agent called code_analyzer as a tool.
func unreadableRepoError(repo string, fromCaller, fromPlanner, isReference bool, conversationRepo string, accountRepos []string) error {
	var b strings.Builder
	if !isReference {
		fmt.Fprintf(&b, "%q is not a repository: a repository is a full URL or an owner/repo name. No code analysis ran.", repo)
	} else {
		fmt.Fprintf(&b, "repository %q was not found by the git host with this account's git access, so no code analysis ran.", repo)
		if !fromCaller {
			b.WriteString(" It was not passed in git_repo; it is the repository resolved for this request.")
		}
	}
	if conversationRepo != "" && conversationRepo != repo {
		fmt.Fprintf(&b, " Repository this conversation has been using: %s.", conversationRepo)
	}
	if len(accountRepos) > 0 {
		listed := accountRepos
		more := ""
		if len(listed) > maxListedRepos {
			more = fmt.Sprintf(" and %d more", len(listed)-maxListedRepos)
			listed = listed[:maxListedRepos]
		}
		fmt.Fprintf(&b, " Repositories on this account's git integration: %s%s.", strings.Join(listed, ", "), more)
	}
	// How to ask again is for a planner that named the repository itself; a
	// person reading a failed resolution has no git_repo field to leave empty.
	if fromCaller && fromPlanner {
		b.WriteString(" To have the repository resolved from a workload instead, leave git_repo empty and pass namespace and workload.")
	}
	return fmt.Errorf("%s", b.String())
}

// refuseUnreadableRepo returns an error when repo must not be dispatched: it is
// not a repository reference, or the git host says the clone credential cannot
// find it. callerRepo is the git_repo the caller passed, used only to say where
// repo came from. A nil return means go ahead, including whenever the host gave
// no definite answer.
//
// The same-message retry guard is deliberately not written here. Nothing ran,
// so there is no analysis for a retry to collide with, and a caller that named
// the wrong repository has to be able to name the right one in the same
// message. What bounds repeated refusals is the caller's own iteration budget.
func refuseUnreadableRepo(ctx *security.RequestContext, query core.NBAgentRequest, repo, callerRepo, provider, gitToken string) error {
	if repo == "" {
		return nil
	}
	fromCaller := callerRepo != "" && callerRepo == repo
	isReference := isRepoReference(repo)
	if isReference && probeRepoAccess(ctx.GetContext(), repoProbeClient, repoProbeURL(repo, provider), gitToken) != repoAccessNotFound {
		return nil
	}
	return repoRefusal(ctx, query, repo, fromCaller, isReference, provider)
}

// repoRefusal logs and builds the error for a repository that is not
// dispatched.
func repoRefusal(ctx *security.RequestContext, query core.NBAgentRequest, repo string, fromCaller, isReference bool, provider string) error {
	ctx.GetLogger().Warn("code: not dispatching — the repository cannot be read with the clone credential",
		"repo", repo, "from_caller", fromCaller, "is_repository_reference", isReference, "provider", provider)
	return unreadableRepoError(repo, fromCaller, calledByAnotherAgent(query), isReference,
		repoFromQueryConfig(query.QueryConfig), integrationRepoURLs(ctx, query.AccountId))
}

// repositoryNoteKey holds the sentence a result carries when it ran on a
// repository other than the one the caller named. It is a key of its own so
// nothing that reads the analysis fields mistakes it for part of the answer.
const repositoryNoteKey = "repository_note"

func repoSetAsideNote(requested, analysed string) string {
	return fmt.Sprintf("The requested repository %s was not found by the git host with this account's git access. This analysis ran on %s, the repository resolved for this request.", requested, analysed)
}

// callerRepoMayGiveWay holds every condition for setting the caller's
// repository aside that needs no network. All of them must hold:
//
//   - another agent made the call. An entry point, a pipeline stage and a
//     person addressing code_analyzer pass a repository they mean.
//   - the request only reads. A change or a pull request goes to the repository
//     that was named, or nowhere.
//   - the value reads as a repository. One that does not is turned down by
//     name, so the caller can say which repository it meant.
//   - the person who asked did not name it. A repository the user typed is
//     the user's choice, not a guess. A prompt this system generated is not
//     consulted: it writes workloads as namespace/workload, which reads exactly
//     like the owner/repo a planner then invents from it.
//   - no git integration of the account lists it. A listed repository the host
//     cannot find is a credential problem, and that is reported, not papered
//     over.
//
// What remains is asking the host, which callerRepoGivesWay does.
func callerRepoMayGiveWay(query core.NBAgentRequest, request CodeAgent2Request, accountRepos []string) bool {
	repo := request.GitRepo
	if repo == "" || !calledByAnotherAgent(query) {
		return false
	}
	if request.RaisePr || request.Followup || codeAgentMode(request) != codeAgentModeExplore {
		return false
	}
	if !isRepoReference(repo) {
		return false
	}
	if query.OriginalQueryFromUser && textNamesRepo(query.OriginalQuery, repo) {
		return false
	}
	return !repoListed(accountRepos, repo)
}

// callerRepoGivesWay reports whether the repository the caller named should
// step aside for the server's own detection: callerRepoMayGiveWay holds and
// the git host answers "not found" for it with the credential a clone of it
// would use. Any doubt — no credential, no definite answer — keeps the
// caller's value.
func (l CodeAgent2) callerRepoGivesWay(ctx *security.RequestContext, query core.NBAgentRequest, request CodeAgent2Request) bool {
	if !callerRepoMayGiveWay(query, request, integrationRepoURLs(ctx, query.AccountId)) {
		return false
	}
	creds, _, provider, err := l.getGitCredentials(ctx, request.GitRepo, query.AccountId)
	if err != nil || len(creds) == 0 {
		return false
	}
	// The list above is cached and comes back empty when it cannot be read; the
	// credentials just loaded carry the same projects, read now.
	if repoListed(credentialRepoURLs(creds), request.GitRepo) {
		return false
	}
	gitToken := resolveGitToken(ctx, creds, request.GitRepo, provider)
	return probeRepoAccess(ctx.GetContext(), repoProbeClient, repoProbeURL(request.GitRepo, provider), gitToken) == repoAccessNotFound
}

// repoListed reports whether repo is one of repoURLs. A reference without a
// host matches a listed repository of the same path on any host.
func repoListed(repoURLs []string, repo string) bool {
	ref, ok := parseRepoRef(repo, nil)
	if !ok {
		return false
	}
	for _, repoURL := range repoURLs {
		known, ok := parseRepoRef(repoURL, nil)
		if ok && known.path == ref.path && (ref.host == "" || known.host == "" || ref.host == known.host) {
			return true
		}
	}
	return false
}

// credentialRepoURLs lists the repositories the given credentials enumerate.
func credentialRepoURLs(creds []GitCredentials) []string {
	repoURLs := []string{}
	for _, cred := range creds {
		for _, project := range cred.Projects {
			if repoURL := resolveProjectRepoURL(project, cred); repoURL != "" {
				repoURLs = append(repoURLs, repoURL)
			}
		}
	}
	return repoURLs
}

// sameRepository reports whether two references name one repository. A
// reference without a host matches the same path on any host.
func sameRepository(a, b string) bool {
	refA, okA := parseRepoRef(a, nil)
	refB, okB := parseRepoRef(b, nil)
	return okA && okB && refA.path == refB.path && (refA.host == "" || refB.host == "" || refA.host == refB.host)
}

// textNamesRepo reports whether text mentions repo's owner/name path anywhere:
// as a URL, a bare name, with a branch or issue suffix, or inside a link into
// the repository. It errs towards yes — a yes only means the repository is
// treated as the one that was asked for.
func textNamesRepo(text, repo string) bool {
	ref, ok := parseRepoRef(repo, nil)
	if !ok || text == "" {
		return false
	}
	return strings.Contains(strings.ToLower(text), ref.path)
}

// calledByAnotherAgent reports whether code_analyzer is running as a tool of
// another agent rather than as the agent a request addressed directly.
func calledByAnotherAgent(query core.NBAgentRequest) bool {
	return query.ParentAgentId != "" && query.ParentAgentId != query.AgentId && query.ParentAgentId != uuid.Nil.String()
}
