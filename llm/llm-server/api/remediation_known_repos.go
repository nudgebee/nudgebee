package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"nudgebee/llm/agents"
	"nudgebee/llm/common"
	"nudgebee/llm/security"
	"nudgebee/llm/services_server"
)

// Raise a PR (#40734). A plan option's repository is written by the model, so it is checked when the plan is read,
// not trusted: "workload" when the alert's workload is known to come from it (its source or deployment annotations,
// a repository a person saved on the workload, or a VM service's linked repository), one click; "integration" when
// it is one of the account's connected repositories, which the person confirms; otherwise the person picks one.
// The stored plan keeps what the model wrote.
const (
	repoKnownWorkload    = "workload"
	repoKnownIntegration = "integration"
)

// optionRepo is how a plan option that changes a repository is raised: Option numbers it from 1; Repo is the known
// repository, else the model's guess ("" for none); Known is repoKnownWorkload, repoKnownIntegration or "".
type optionRepo struct {
	Option int    `json:"option"`
	Repo   string `json:"repo,omitempty"`
	Known  string `json:"known,omitempty"`
}

// workloadRepoFacts are the repositories the event's workload is known to come from, and its inventory row (where
// the Workloads page's Git dialog saves a repository).
type workloadRepoFacts struct {
	Repos      []string `json:"repos,omitempty"`
	ResourceId string   `json:"resource_id,omitempty"`
}

const workloadReposCacheNS = "remediation_workload_repos"

func init() {
	// The tab reads the plan every few seconds, and the lookup can ask Argo CD; repositories change at deploy pace.
	common.CacheCreateNamespace(workloadReposCacheNS, common.CacheNamespaceWithExpiration(5*time.Minute))
}

// workloadRepos reads workloadRepoFacts for eventId; ok is false when they could not be read. A var so tests can
// stub it.
var workloadRepos = func(ctx *security.RequestContext, accountId, eventId string) (facts workloadRepoFacts, ok bool) {
	key := accountId + "/" + eventId
	if raw, hit := common.CacheGet(workloadReposCacheNS, key); hit && json.Unmarshal(raw, &facts) == nil {
		return facts, true
	}
	event, err := getEventData(ctx, EventAnalysisRequest{AccountId: accountId, EventId: eventId})
	if err != nil {
		return facts, false
	}
	labels := map[string]any{}
	if text, isText := event.Labels.(string); isText {
		if parsed := parseEventLabels(text); parsed != nil {
			labels = parsed
		}
	}
	dbm, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return facts, false
	}
	if namespace, workload := resolveEventWorkload(ctx, event, labels); namespace != "" && workload != "" {
		caps, err := services_server.ResolveEventCodeCapabilities(ctx, dbm, accountId, services_server.SourceCodeAnnotationOptions{
			EventId: eventId, WorkloadName: workload, Namespace: namespace,
		})
		if err != nil {
			return facts, false
		}
		if caps.Source != nil {
			facts.Repos = append(facts.Repos, caps.Source.Repo)
		}
		if caps.Deployment != nil {
			facts.Repos = append(facts.Repos, caps.Deployment.Repo)
		}
		err = dbm.Db.Get(&facts.ResourceId, `SELECT COALESCE(cloud_resource_id::text, '') FROM k8s_workloads
			WHERE cloud_account_id = $1 AND namespace = $2 AND name = $3 LIMIT 1`, accountId, namespace, workload)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return facts, false // not cached: the tab would not save a picked repository for 5 minutes
		}
	}
	if link := vmServiceCodeLink(ctx, eventId, labels); link.Repo != "" {
		facts.Repos = append(facts.Repos, link.Repo)
	}
	if raw, err := json.Marshal(facts); err == nil {
		_ = common.CacheSet(workloadReposCacheNS, key, raw)
	}
	return facts, true
}

// integrationRepo is the account's connected repository repo names, or "". A var so tests can stub it.
var integrationRepo = agents.IntegrationRepo

// inRepoWheres are the changes always made in a repository (code, a deployment source), so a person can pick it when
// none is known; an infrastructure or dependency change may be made elsewhere (a cloud quota, another team's
// service), so it is a pull request only when the plan names a repository.
var inRepoWheres = map[string]bool{"code": true, "deployment_source": true}

// repoChange reports whether a is a change made in a repository, so raised as a pull request.
func repoChange(a RemediationAction) bool {
	rc := a.RequiredChange
	if rc == nil || strings.TrimSpace(rc.Change) == "" {
		return false
	}
	where := strings.ToLower(strings.TrimSpace(rc.Where))
	return inRepoWheres[where] || (prWheres[where] && strings.TrimSpace(rc.Repo) != "")
}

// planOptionRepos is optionRepos for eventId's plan, with the workload's inventory row; it reads the workload's
// repositories only when an option changes one.
func planOptionRepos(ctx *security.RequestContext, accountId, eventId string, plan RemediationPlan) ([]optionRepo, string) {
	if !slices.ContainsFunc(plan.Actions, repoChange) {
		return nil, ""
	}
	workload, _ := workloadRepos(ctx, accountId, eventId)
	return optionRepos(ctx, accountId, eventId, plan, workload), workload.ResourceId
}

// optionRepos says, for each option of plan that changes a repository, which repository and how it is known.
func optionRepos(ctx *security.RequestContext, accountId, eventId string, plan RemediationPlan, workload workloadRepoFacts) []optionRepo {
	var out []optionRepo
	for i, a := range plan.Actions {
		if !repoChange(a) {
			continue
		}
		o := optionRepo{Option: i + 1, Repo: strings.TrimSpace(a.RequiredChange.Repo)}
		if o.Repo != "" {
			if known := matchingRepo(o.Repo, workload.Repos); known != "" {
				o.Repo, o.Known = known, repoKnownWorkload
			} else if listed := integrationRepo(ctx, accountId, o.Repo); listed != "" {
				o.Repo, o.Known = listed, repoKnownIntegration
			}
		}
		out = append(out, o)
	}
	return out
}

// matchingRepo is the repository of known that repo names, or "".
func matchingRepo(repo string, known []string) string {
	for _, k := range known {
		if strings.TrimSpace(k) != "" && agents.SameRepo(repo, k) {
			return k
		}
	}
	return ""
}

// optionRepoOf is the option's entry in repos, or the zero value.
func optionRepoOf(repos []optionRepo, option int) optionRepo {
	for _, r := range repos {
		if r.Option == option {
			return r
		}
	}
	return optionRepo{}
}
