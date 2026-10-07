package agents

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"nudgebee/llm/agents/core"
	"nudgebee/llm/common"
	"nudgebee/llm/security"
	toolcore "nudgebee/llm/tools/core"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsRepoReference(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want bool
	}{
		{"https url", "https://github.com/example-org/payments", true},
		{"bare owner/repo", "example-org/payments", true},
		{"scp-style remote", "git@github.com:example-org/payments.git", true},
		{"ssh url", "ssh://git@github.com/example-org/payments.git", true},
		{"a url the workspace would judge itself", "https://github.com/example-org/payments?tab=readme", true},
		{"a single bare word", "payments-infra", false},
		{"prose", "the payments repository", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, isRepoReference(tt.in))
		})
	}
}

func TestRepoProbeURL(t *testing.T) {
	const suffix = "/info/refs?service=git-upload-pack"
	tests := []struct {
		name     string
		repo     string
		provider string
		want     string
	}{
		{"https url is asked as given", "https://github.com/example-org/payments", "github", "https://github.com/example-org/payments" + suffix},
		{"an existing .git and a trailing slash", "https://github.com/example-org/payments.git/", "github", "https://github.com/example-org/payments.git" + suffix},
		{"port and case are kept", "https://git.example.com:8443/Platform/Payments", "gitlab", "https://git.example.com:8443/Platform/Payments" + suffix},
		{"a scheme in upper case is still asked", "HTTPS://github.com/example-org/payments", "github", "https://github.com/example-org/payments" + suffix},
		{"a url carrying its own credentials is not asked", "https://bot:secret@github.com/example-org/payments", "github", ""},
		{"subgroups are kept", "https://gitlab.example.com/platform/billing/payments", "gitlab", "https://gitlab.example.com/platform/billing/payments" + suffix},
		{"bare owner/repo on github", "example-org/payments", "github", "https://github.com/example-org/payments.git" + suffix},
		{"bare owner/repo on gitlab", "example-org/payments", "gitlab", "https://gitlab.com/example-org/payments.git" + suffix},
		{"bare owner/repo, unknown provider", "example-org/payments", "bitbucket", ""},
		{"bare path with three segments", "platform/billing/payments", "gitlab", ""},
		{"plain http is not asked", "http://git.example.com/example-org/payments", "github", ""},
		{"rest api url is not asked", "https://api.github.com/repos/example-org/payments", "github", ""},
		{"url with a query", "https://github.com/example-org/payments?tab=readme", "github", ""},
		{"scp-style remote", "git@github.com:example-org/payments.git", "github", ""},
		{"ssh url", "ssh://git@github.com/example-org/payments.git", "github", ""},
		{"a single bare word", "payments-infra", "github", ""},
		{"an owner with no repository is asked as given, and the host answers", "https://github.com/example-org", "github", "https://github.com/example-org" + suffix},
		{"empty", "", "github", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, repoProbeURL(tt.repo, tt.provider))
		})
	}
}

func TestCodeAgentMode(t *testing.T) {
	assert.Equal(t, codeAgentModeExplore, codeAgentMode(CodeAgent2Request{}))
	assert.Equal(t, codeAgentModeFix, codeAgentMode(CodeAgent2Request{RaisePr: true}))
	assert.Equal(t, codeAgentModeExplore, codeAgentMode(CodeAgent2Request{Mode: "explore", RaisePr: true}))
	assert.Equal(t, codeAgentModeFix, codeAgentMode(CodeAgent2Request{Mode: "fix"}))
	assert.Equal(t, codeAgentModeFix, codeAgentMode(CodeAgent2Request{Mode: "something-else", RaisePr: true}))
}

func TestRepoListed(t *testing.T) {
	listed := []string{"https://github.com/example-org/payments", "https://gitlab.example.com/platform/billing/ledger"}
	tests := []struct {
		name string
		repo string
		want bool
	}{
		{"the listed url", "https://github.com/example-org/payments", true},
		{"with .git and another case", "https://GitHub.com/Example-Org/Payments.git", true},
		{"a bare name of a listed repository", "example-org/payments", true},
		{"a subgroup repository", "https://gitlab.example.com/platform/billing/ledger", true},
		{"the same path on another host", "https://gitlab.com/example-org/payments", false},
		{"a service name under the right owner", "https://github.com/example-org/payments-api", false},
		{"a single bare word", "payments", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, repoListed(listed, tt.repo))
		})
	}
}

func TestTextNamesRepo(t *testing.T) {
	const repo = "https://github.com/example-org/payments-api"
	tests := []struct {
		name string
		text string
		want bool
	}{
		{"the url written out", "please analyse https://github.com/example-org/payments-api for this crash", true},
		{"in backticks with a full stop", "look at `https://github.com/example-org/payments-api`.", true},
		{"as an owner/repo name", "check example-org/payments-api, it changed yesterday", true},
		{"another case", "check Example-Org/Payments-API", true},
		{"host without a scheme", "it is at github.com/example-org/payments-api", true},
		{"with a branch", "use example-org/payments-api@main", true},
		{"with an issue number", "see example-org/payments-api#12", true},
		{"with a typographic apostrophe", "example-org/payments-api\u2019s retry logic", true},
		{"a link into the repository", "see https://github.com/example-org/payments-api/pull/12", true},
		{"only the service name", "why is payments-api slow", false},
		{"another repository", "the source is https://github.com/example-org/payments", false},
		{"nothing", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, textNamesRepo(tt.text, repo))
		})
	}
}

func TestSameRepository(t *testing.T) {
	assert.True(t, sameRepository("https://github.com/example-org/payments-api", "https://github.com/Example-Org/payments-api.git"))
	assert.True(t, sameRepository("example-org/payments-api", "https://github.com/example-org/payments-api"))
	assert.False(t, sameRepository("https://github.com/example-org/payments-api", "https://github.com/example-org/payments"))
	assert.False(t, sameRepository("https://gitlab.com/example-org/payments-api", "https://github.com/example-org/payments-api"))
	assert.False(t, sameRepository("payments-api", "payments-api"))
	assert.False(t, sameRepository("", ""))
}

func TestCredentialRepoURLs(t *testing.T) {
	creds := []GitCredentials{
		{Provider: "github", Url: "api.github.com", Projects: []map[string]string{{"key": "example-org/payments"}}},
		{Provider: "github", Url: "api.github.com"},
	}
	urls := credentialRepoURLs(creds)
	assert.True(t, repoListed(urls, "https://github.com/example-org/payments"))
	assert.False(t, repoListed(urls, "https://github.com/example-org/payments-api"))
	assert.Empty(t, credentialRepoURLs(nil))
}

func TestCallerRepoMayGiveWay(t *testing.T) {
	const invented = "https://github.com/example-org/payments-api"
	accountRepos := []string{"https://github.com/example-org/payments"}
	fromPlanner := core.NBAgentRequest{ParentAgentId: "orchestrator-run", AgentId: "code-run"}
	explore := CodeAgent2Request{GitRepo: invented, Mode: "explore"}

	tests := []struct {
		name    string
		query   core.NBAgentRequest
		request CodeAgent2Request
		want    bool
	}{
		{"a planner's read-only request for an unlisted repository", fromPlanner, explore, true},
		{"no mode and no pull request is read-only too", fromPlanner, CodeAgent2Request{GitRepo: invented}, true},
		{"a request that addressed code_analyzer directly", core.NBAgentRequest{}, explore, false},
		{"the agent's own id as parent", core.NBAgentRequest{ParentAgentId: "code-run", AgentId: "code-run"}, explore, false},
		{"a fix", fromPlanner, CodeAgent2Request{GitRepo: invented, Mode: "fix"}, false},
		{"a pull request", fromPlanner, CodeAgent2Request{GitRepo: invented, RaisePr: true}, false},
		{"explore that still asks for a pull request", fromPlanner, CodeAgent2Request{GitRepo: invented, Mode: "explore", RaisePr: true}, false},
		{"a follow-up", fromPlanner, CodeAgent2Request{GitRepo: invented, Followup: true}, false},
		{"no repository named", fromPlanner, CodeAgent2Request{Mode: "explore"}, false},
		{"a value that is not a repository is left to be turned down by name", fromPlanner, CodeAgent2Request{GitRepo: "payments-infra", Mode: "explore"}, false},
		{"a repository an integration lists", fromPlanner, CodeAgent2Request{GitRepo: "https://github.com/example-org/payments", Mode: "explore"}, false},
		{"a listed repository by its bare name", fromPlanner, CodeAgent2Request{GitRepo: "example-org/payments", Mode: "explore"}, false},
		{
			"a repository the person who asked named",
			core.NBAgentRequest{ParentAgentId: "orchestrator-run", AgentId: "code-run", OriginalQueryFromUser: true,
				OriginalQuery: "analyse " + invented + " for this crash"},
			explore, false,
		},
		{
			"a generated prompt that writes the workload as namespace/workload is not the user naming a repository",
			core.NBAgentRequest{ParentAgentId: "orchestrator-run", AgentId: "code-run",
				OriginalQuery: "The upstream dependency example-org/payments-api had a configuration change 7 minutes before the alert."},
			explore, true,
		},
		{
			"a person asking about the service without naming a repository",
			core.NBAgentRequest{ParentAgentId: "orchestrator-run", AgentId: "code-run", OriginalQueryFromUser: true,
				OriginalQuery: "can you check the payments-api code for recent changes"},
			explore, true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, callerRepoMayGiveWay(tt.query, tt.request, accountRepos))
		})
	}
}

func TestRepoSetAsideNote(t *testing.T) {
	note := repoSetAsideNote("https://github.com/example-org/payments-api", "https://github.com/example-org/payments")
	assert.Contains(t, note, "https://github.com/example-org/payments-api was not found by the git host")
	assert.Contains(t, note, "This analysis ran on https://github.com/example-org/payments")
	// The note travels inside the result the planner reads; it must not trip
	// the check that looks for an off-topic analysis.
	assert.False(t, isIrrelevantAnalysis(note))
}

func TestProbeRepoAccess(t *testing.T) {
	type seen struct {
		path, query, user, password, agent string
		hasAuth                            bool
	}
	var (
		mu       sync.Mutex
		last     seen
		requests int
	)
	lastSeen := func() (seen, int) {
		mu.Lock()
		defer mu.Unlock()
		return last, requests
	}
	release := make(chan struct{})
	mux := http.NewServeMux()
	respond := func(pattern string, status int) {
		mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			user, password, hasAuth := r.BasicAuth()
			mu.Lock()
			last = seen{path: r.URL.Path, query: r.URL.RawQuery, user: user, password: password, agent: r.UserAgent(), hasAuth: hasAuth}
			requests++
			mu.Unlock()
			w.WriteHeader(status)
		})
	}
	respond("/example-org/readable/info/refs", http.StatusOK)
	respond("/gitlab-group/readable/info/refs", http.StatusOK)
	respond("/example-org/missing/info/refs", http.StatusNotFound)
	respond("/example-org/bad-credential/info/refs", http.StatusUnauthorized)
	respond("/example-org/forbidden/info/refs", http.StatusForbidden)
	respond("/example-org/rate-limited/info/refs", http.StatusTooManyRequests)
	respond("/example-org/broken/info/refs", http.StatusInternalServerError)
	mux.HandleFunc("/example-org/renamed/info/refs", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/example-org/readable/info/refs?"+r.URL.RawQuery, http.StatusMovedPermanently)
	})
	mux.HandleFunc("/example-org/slow/info/refs", func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	defer close(release)

	probe := func(path string) string { return srv.URL + path + "/info/refs?service=git-upload-pack" }
	client := &http.Client{Timeout: 200 * time.Millisecond}

	tests := []struct {
		name string
		path string
		want repoAccess
	}{
		{"the host serves it", "/example-org/readable", repoAccessReadable},
		{"a renamed repository is followed", "/example-org/renamed", repoAccessReadable},
		{"the host says there is no such repository", "/example-org/missing", repoAccessNotFound},
		{"a refused credential says nothing about the repository", "/example-org/bad-credential", repoAccessUnknown},
		{"forbidden says nothing about the repository", "/example-org/forbidden", repoAccessUnknown},
		{"a rate limit says nothing about the repository", "/example-org/rate-limited", repoAccessUnknown},
		{"a host error says nothing about the repository", "/example-org/broken", repoAccessUnknown},
		{"a slow host does not hold the run", "/example-org/slow", repoAccessUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, probeRepoAccess(context.Background(), client, probe(tt.path), "tok"))
		})
	}

	t.Run("asks the way a clone does", func(t *testing.T) {
		probeRepoAccess(context.Background(), client, probe("/example-org/readable"), "tok")
		got, _ := lastSeen()
		assert.Equal(t, "/example-org/readable/info/refs", got.path)
		assert.Equal(t, "service=git-upload-pack", got.query)
		assert.True(t, got.hasAuth)
		assert.Equal(t, "x-access-token", got.user)
		assert.Equal(t, "tok", got.password)
		assert.Contains(t, got.agent, "git/")
	})
	t.Run("uses the workspace's user name for a gitlab url", func(t *testing.T) {
		probeRepoAccess(context.Background(), client, probe("/gitlab-group/readable"), "tok")
		got, _ := lastSeen()
		assert.Equal(t, "oauth2", got.user)
	})
	t.Run("without a credential nothing is asked, even of a host that would say not found", func(t *testing.T) {
		_, before := lastSeen()
		assert.Equal(t, repoAccessUnknown, probeRepoAccess(context.Background(), client, probe("/example-org/missing"), ""))
		_, after := lastSeen()
		assert.Equal(t, before, after)
	})
	t.Run("an unreachable host does not stop the run", func(t *testing.T) {
		gone := httptest.NewServer(mux)
		goneURL := gone.URL + "/example-org/readable/info/refs?service=git-upload-pack"
		gone.Close()
		assert.Equal(t, repoAccessUnknown, probeRepoAccess(context.Background(), client, goneURL, "tok"))
	})
	t.Run("nothing to ask", func(t *testing.T) {
		assert.Equal(t, repoAccessUnknown, probeRepoAccess(context.Background(), client, "", "tok"))
	})
}

func TestUnreadableRepoError(t *testing.T) {
	accountRepos := []string{"https://github.com/example-org/payments", "https://github.com/example-org/platform-config"}

	t.Run("a planner named a repository that is not there", func(t *testing.T) {
		msg := unreadableRepoError("https://github.com/example-org/payments-api", true, true, true,
			"https://github.com/example-org/payments", accountRepos).Error()
		assert.Contains(t, msg, `repository "https://github.com/example-org/payments-api" was not found by the git host`)
		assert.Contains(t, msg, "no code analysis ran")
		assert.Contains(t, msg, "Repository this conversation has been using: https://github.com/example-org/payments.")
		assert.Contains(t, msg, "Repositories on this account's git integration: https://github.com/example-org/payments, https://github.com/example-org/platform-config.")
		assert.Contains(t, msg, "leave git_repo empty and pass namespace and workload")
		assert.NotContains(t, msg, "resolved for this request")
	})
	t.Run("a non-planner caller is not told how to fill a tool field", func(t *testing.T) {
		msg := unreadableRepoError("https://github.com/example-org/payments-api", true, false, true, "", accountRepos).Error()
		assert.NotContains(t, msg, "leave git_repo empty")
		assert.Contains(t, msg, "Repositories on this account's git integration")
	})
	t.Run("the server's own resolution is named as such", func(t *testing.T) {
		msg := unreadableRepoError("https://github.com/example-org/payments-api", false, true, true, "", nil).Error()
		assert.Contains(t, msg, "It was not passed in git_repo; it is the repository resolved for this request.")
		assert.NotContains(t, msg, "leave git_repo empty")
		assert.NotContains(t, msg, "Repositories on this account's git integration")
	})
	t.Run("a value that is not a repository", func(t *testing.T) {
		msg := unreadableRepoError("payments-infra", true, true, false, "", accountRepos).Error()
		assert.Contains(t, msg, `"payments-infra" is not a repository`)
		assert.NotContains(t, msg, "was not found by the git host")
	})
	t.Run("the conversation's repository is not repeated when it is the one refused", func(t *testing.T) {
		msg := unreadableRepoError("https://github.com/example-org/payments", false, false, true,
			"https://github.com/example-org/payments", nil).Error()
		assert.NotContains(t, msg, "this conversation has been using")
	})
	t.Run("a long repository list is cut", func(t *testing.T) {
		many := make([]string, 0, maxListedRepos+4)
		for range maxListedRepos + 4 {
			many = append(many, "https://github.com/example-org/service")
		}
		msg := unreadableRepoError("https://github.com/example-org/payments-api", true, true, true, "", many).Error()
		assert.Contains(t, msg, "and 4 more.")
	})
}

func TestRefuseUnreadableRepo(t *testing.T) {
	var requests atomic.Int64
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		switch r.URL.Path {
		case "/example-org/payments/info/refs":
			w.WriteHeader(http.StatusOK)
		case "/example-org/rate-limited/info/refs":
			w.WriteHeader(http.StatusTooManyRequests)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	previous := repoProbeClient
	repoProbeClient = srv.Client()
	defer func() { repoProbeClient = previous }()

	const accountId = "repo-access-test-account"
	listed, err := json.Marshal([]string{srv.URL + "/example-org/payments"})
	require.NoError(t, err)
	require.NoError(t, common.CacheSet(integrationReposCacheNS, accountId, listed))
	defer func() { _ = common.CacheDelete(integrationReposCacheNS, accountId) }()

	ctx := security.NewRequestContextForSuperAdmin()
	planner := core.NBAgentRequest{AccountId: accountId, ParentAgentId: "parent-agent"}
	readable := srv.URL + "/example-org/payments"
	missing := srv.URL + "/example-org/payments-api"

	t.Run("a readable repository goes ahead", func(t *testing.T) {
		assert.NoError(t, refuseUnreadableRepo(ctx, planner, readable, readable, "github", "tok"))
	})
	t.Run("no repository goes ahead without asking", func(t *testing.T) {
		before := requests.Load()
		assert.NoError(t, refuseUnreadableRepo(ctx, planner, "", "", "github", "tok"))
		assert.Equal(t, before, requests.Load())
	})
	t.Run("a missing repository goes ahead when there is no credential to ask with", func(t *testing.T) {
		before := requests.Load()
		assert.NoError(t, refuseUnreadableRepo(ctx, planner, missing, missing, "github", ""))
		assert.Equal(t, before, requests.Load())
	})
	t.Run("no definite answer goes ahead", func(t *testing.T) {
		rateLimited := srv.URL + "/example-org/rate-limited"
		assert.NoError(t, refuseUnreadableRepo(ctx, planner, rateLimited, rateLimited, "github", "tok"))
	})
	t.Run("a repository the caller named that is not there is refused with what the server knows", func(t *testing.T) {
		err := refuseUnreadableRepo(ctx, planner, missing, missing, "github", "tok")
		require.Error(t, err)
		assert.Contains(t, err.Error(), missing)
		assert.Contains(t, err.Error(), "Repositories on this account's git integration: "+readable)
		assert.Contains(t, err.Error(), "leave git_repo empty")
	})
	t.Run("the conversation's repository is offered", func(t *testing.T) {
		withConversationRepo := planner
		withConversationRepo.QueryConfig = toolcore.NBQueryConfig{GitRepo: readable}
		err := refuseUnreadableRepo(ctx, withConversationRepo, missing, missing, "github", "tok")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "Repository this conversation has been using: "+readable)
	})
	t.Run("a repository the server resolved that is not there is refused as the server's", func(t *testing.T) {
		err := refuseUnreadableRepo(ctx, planner, missing, "", "github", "tok")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "resolved for this request")
		assert.NotContains(t, err.Error(), "leave git_repo empty")
	})
	t.Run("a request that addressed code_analyzer directly is not told how to fill a tool field", func(t *testing.T) {
		for _, direct := range []core.NBAgentRequest{
			{AccountId: accountId},
			{AccountId: accountId, ParentAgentId: uuid.Nil.String()},
			{AccountId: accountId, ParentAgentId: "agent-1", AgentId: "agent-1"},
		} {
			err := refuseUnreadableRepo(ctx, direct, missing, missing, "github", "tok")
			require.Error(t, err)
			assert.NotContains(t, err.Error(), "leave git_repo empty")
			assert.Contains(t, err.Error(), "Repositories on this account's git integration")
		}
	})
	t.Run("a bare word is refused without asking the host", func(t *testing.T) {
		before := requests.Load()
		err := refuseUnreadableRepo(ctx, planner, "payments-infra", "payments-infra", "github", "tok")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "is not a repository")
		assert.Equal(t, before, requests.Load())
	})
}
