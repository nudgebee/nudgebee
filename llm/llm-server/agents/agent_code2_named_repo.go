package agents

import (
	"encoding/json"
	"net/url"
	"nudgebee/llm/common"
	"nudgebee/llm/security"
	"regexp"
	"strings"
	"time"
)

// The repository list is read on the event investigation prompt-build path,
// which runs on every pipeline entry — cached and recovered runs included. An
// account's git integrations change at integration-edit cadence, not event
// cadence.
const (
	integrationReposCacheNS  = "event_code_integration_repos"
	integrationReposCacheTTL = 10 * time.Minute
)

func init() {
	common.CacheCreateNamespace(integrationReposCacheNS, common.CacheNamespaceWithExpiration(integrationReposCacheTTL))
}

// IntegrationRepoNamedBy returns the one repository of the account's git
// integrations that values name outright, or "" when none or more than one is
// named.
//
// A value counts only when the whole of it is a repository reference —
// "org/repo", a URL whose path is exactly the repository, or a git remote. A
// repository that merely occurs inside a longer value is ignored: on real
// events those are stack-trace module paths (github.com/org/shared-lib/pkg/x)
// and job or commit links, which say where a frame or a page lives, not which
// repository the event is about.
func IntegrationRepoNamedBy(ctx *security.RequestContext, accountId string, values []string) string {
	if len(values) == 0 {
		return ""
	}
	return repoNamedBy(integrationRepoURLs(ctx, accountId), values)
}

// repoRef is a repository reference: the host when the reference states one,
// and the lowercased repository path ("org/repo", or "group/subgroup/repo" on
// GitLab).
type repoRef struct {
	host string
	path string
}

func repoNamedBy(repoURLs []string, values []string) string {
	type knownRepo struct {
		ref repoRef
		url string
	}
	known := []knownRepo{}
	hosts := map[string]bool{}
	for _, repoURL := range repoURLs {
		if ref, ok := parseRepoRef(repoURL, nil); ok {
			known = append(known, knownRepo{ref: ref, url: repoURL})
			if ref.host != "" {
				hosts[ref.host] = true
			}
		}
	}
	// Keyed by the known repository, so one repository named twice is still one,
	// and a host-less value that fits repositories on two hosts names both.
	named := map[repoRef]string{}
	for _, value := range values {
		ref, ok := parseRepoRef(value, hosts)
		if !ok {
			continue
		}
		for _, k := range known {
			if k.ref.path == ref.path && (ref.host == "" || ref.host == k.ref.host) {
				named[k.ref] = k.url
			}
		}
	}
	if len(named) != 1 {
		return ""
	}
	for _, repoURL := range named {
		return repoURL
	}
	return ""
}

var repoPathSegment = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

// parseRepoRef reads s as a repository reference when s is one in its entirety:
// an http(s), ssh or git URL, an scp-style remote, or a bare path. knownHosts
// decides whether a scheme-less "host/org/repo" leads with a host: only a host
// some known repository is on counts, so a group whose name has a dot is not
// mistaken for one.
func parseRepoRef(s string, knownHosts map[string]bool) (repoRef, bool) {
	s = strings.TrimSpace(s)
	if s == "" || strings.ContainsAny(s, " \t\r\n") {
		return repoRef{}, false
	}
	host, path := "", s
	switch {
	case strings.Contains(s, "://"):
		parsed, err := url.Parse(s)
		if err != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Hostname() == "" {
			return repoRef{}, false
		}
		switch parsed.Scheme {
		case "http", "https", "ssh", "git":
		default:
			return repoRef{}, false
		}
		host, path = parsed.Hostname(), parsed.Path
	case strings.HasPrefix(s, "git@"):
		remoteHost, remotePath, found := strings.Cut(strings.TrimPrefix(s, "git@"), ":")
		if !found {
			return repoRef{}, false
		}
		host, path = remoteHost, remotePath
	}
	path = strings.TrimSuffix(strings.Trim(path, "/"), ".git")
	segments := strings.Split(path, "/")
	if host == "" && len(segments) >= 3 && knownHosts[normalizeRepoHost(segments[0])] {
		host, segments = segments[0], segments[1:]
	}
	if len(segments) < 2 {
		return repoRef{}, false
	}
	for _, segment := range segments {
		if !repoPathSegment.MatchString(segment) {
			return repoRef{}, false
		}
	}
	return repoRef{host: normalizeRepoHost(host), path: strings.ToLower(strings.Join(segments, "/"))}, true
}

func normalizeRepoHost(host string) string {
	return strings.TrimPrefix(strings.ToLower(host), "www.")
}

// integrationRepoURLs lists the repositories enumerated on the account's usable
// git integrations — the same integrations and project entries
// getGitCredentials hands code_analyzer. It reads no secret: recognising a
// repository by name needs only the provider, the base URL and the project
// list.
func integrationRepoURLs(ctx *security.RequestContext, accountId string) []string {
	if raw, ok := common.CacheGet(integrationReposCacheNS, accountId); ok {
		var cached []string
		if err := json.Unmarshal(raw, &cached); err == nil {
			return cached
		}
	}

	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		ctx.GetLogger().Warn("code: unable to get database manager for integration repositories", "error", err, "account_id", accountId)
		return nil
	}
	rows, err := dbms.Db.Queryx(`
		SELECT
			i.type as provider,
			COALESCE(MAX(CASE WHEN icv.name = 'url' THEN icv.value END), '') as url,
			COALESCE(MAX(CASE WHEN icv.name = 'projects' THEN icv.value END), '') as projects
		FROM integrations i
		JOIN integration_config_values icv ON i.id = icv.integration_id
		WHERE i.tenant_id IN (SELECT tenant FROM cloud_accounts WHERE id = $1)
		  AND i.status = 'enabled'
		  AND i.type IN ('github', 'gitlab')
		GROUP BY i.id, i.type
		HAVING COALESCE(MAX(CASE WHEN icv.name = 'username' THEN icv.value END), '') <> ''
		   AND COALESCE(MAX(CASE WHEN icv.name = 'url' THEN icv.value END), '') <> ''
		   AND COALESCE(MAX(CASE WHEN icv.name = 'password' THEN icv.value END), '') <> ''
	`, accountId)
	if err != nil {
		ctx.GetLogger().Warn("code: unable to query integration repositories", "error", err, "account_id", accountId)
		return nil
	}
	defer func() {
		if err := rows.Close(); err != nil {
			ctx.GetLogger().Error("code: unable to close rows", "error", err)
		}
	}()

	repoURLs := []string{}
	for rows.Next() {
		var provider, baseURL, projects string
		if err := rows.Scan(&provider, &baseURL, &projects); err != nil {
			ctx.GetLogger().Warn("code: unable to scan integration repositories", "error", err, "account_id", accountId)
			return nil
		}
		if projects == "" {
			continue
		}
		projectsMap := []map[string]string{}
		if err := common.UnmarshalJson([]byte(projects), &projectsMap); err != nil {
			ctx.GetLogger().Warn("unable to parse projects JSON", "error", err, "provider", provider)
			continue
		}
		cred := GitCredentials{Url: baseURL, Provider: provider}
		for _, project := range projectsMap {
			if repoURL := resolveProjectRepoURL(project, cred); repoURL != "" {
				repoURLs = append(repoURLs, repoURL)
			}
		}
	}
	if err := rows.Err(); err != nil {
		ctx.GetLogger().Warn("code: unable to read integration repositories", "error", err, "account_id", accountId)
		return nil
	}

	if raw, err := json.Marshal(repoURLs); err == nil {
		if cacheErr := common.CacheSet(integrationReposCacheNS, accountId, raw, common.CacheSetWithExpiration(integrationReposCacheTTL)); cacheErr != nil {
			ctx.GetLogger().Warn("code: unable to cache integration repositories", "error", cacheErr, "account_id", accountId)
		}
	}
	return repoURLs
}
