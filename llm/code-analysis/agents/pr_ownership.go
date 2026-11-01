package agents

import (
	"context"
	"strings"
	"time"

	"nudgebee/code-analysis-agent/internal/gitprovider"
	"nudgebee/code-analysis-agent/internal/ownership"
	"nudgebee/code-analysis-agent/internal/session"
	"nudgebee/code-analysis-agent/tools/core"
)

func changedOwnershipPaths(diff string) []string {
	fields := strings.Split(strings.TrimSuffix(diff, "\x00"), "\x00")
	seen := map[string]bool{}
	var paths []string
	for i := 0; i+1 < len(fields); {
		status := fields[i]
		i++
		count := 1
		if strings.HasPrefix(status, "R") || strings.HasPrefix(status, "C") {
			count = 2
		}
		for j := 0; j < count && i < len(fields); j++ {
			path := fields[i]
			i++
			if path != "" && !seen[path] {
				seen[path] = true
				paths = append(paths, path)
			}
		}
	}
	return paths
}

func (a *OrchestratorAgent) resolvePROwnership(ctx context.Context, sc *session.SessionContext, repoDir, baseSHA string, provider gitprovider.GitProvider) (*ownership.Client, ownership.Result) {
	result := ownership.Result{Provider: string(provider), Status: "unresolved", Warning: "Ownership lookup unavailable; published without an assignee."}
	if sc == nil {
		return nil, result
	}
	repoURL, token := "", ""
	if sc.RepoContext != nil {
		repoURL = sc.RepoContext.URL
	}
	if sc.Credentials != nil {
		token = sc.Credentials.Token
	}
	if repoURL == "" {
		if out, err := a.runGit(ctx, repoDir, "remote", "get-url", "origin"); err == nil {
			repoURL = strings.TrimSpace(out)
		}
	}
	client, err := ownership.New(repoURL, token, provider)
	if err != nil {
		return nil, result
	}
	result.Repository = client.Info.Host + "/" + client.Info.FullPath
	if baseSHA == "" {
		return client, result
	}
	out, err := a.runGit(ctx, repoDir, "diff", "--name-status", "-z", "-M", baseSHA, "HEAD", "--")
	if err != nil {
		return client, result
	}
	return client, client.Resolve(ctx, baseSHA, changedOwnershipPaths(out))
}

// Creation deliberately carries no optional assignment metadata: even a valid
// owner can become ineligible between lookup and publication. Assign afterward.
// Recover an ambiguous CLI failure without retrying the non-idempotent create.
type prFinder interface {
	Find(context.Context, string, string) (string, error)
}

func createOwnedPR(ctx context.Context, client prFinder, head, base, dir string, args []any, execute func(context.Context, map[string]any) core.NBToolResponse) core.NBToolResponse {
	response := execute(ctx, map[string]any{"args": args, "working_directory": dir})
	if response.Status == "success" || client == nil {
		return response
	}
	recoveryCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	prURL, err := client.Find(recoveryCtx, head, base)
	if err == nil && prURL != "" {
		return core.CreateSuccessResponse(prURL, prURL, map[string]any{"stdout": prURL})
	}
	return response
}
