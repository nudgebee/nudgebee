package gcloud

import (
	"fmt"
	"nudgebee/collector/cloud/providers"
	"sort"

	run "cloud.google.com/go/run/apiv2"
	"cloud.google.com/go/run/apiv2/runpb"
	"google.golang.org/api/iterator"
	"gopkg.in/yaml.v3"
)

const (
	// defaultDeployDiffRevisions is enough for a before/after diff (newest + prior).
	defaultDeployDiffRevisions = 2
	// maxDeployDiffRevisions bounds the API paging / response size.
	maxDeployDiffRevisions = 10
	// secretEnvPlaceholder is shown for env vars sourced from Secret Manager so a
	// secret name change is visible in the diff without leaking the value.
	secretEnvPlaceholder = "<from-secret>"
)

// queryGcloudDeploymentDiff lists a Cloud Run service's revisions (immutable
// desired-state snapshots) newest-first and returns each as a normalized,
// status-stripped spec YAML, so the api-server can diff the two most recent into a
// before/after "what changed" view. Generic: no incident-type filtering, the spec
// view is the deploy-relevant surface for every Cloud Run service.
func queryGcloudDeploymentDiff(ctx providers.CloudProviderContext, account providers.Account, query providers.QueryDeploymentDiffRequest) (providers.QueryDeploymentDiffResponse, error) {
	logger := ctx.GetLogger()

	if query.ServiceName == "" || query.Region == "" {
		// Revisions are addressed by a fully-qualified, regional service path; without
		// both we cannot scope the list (unlike global resource listing).
		return providers.QueryDeploymentDiffResponse{Status: "Failed"},
			fmt.Errorf("query_deployment_diff requires service_name and region")
	}

	// Enrich ctx with audit info so permission errors are recorded (the entry point
	// builds a bare request context without it), mirroring query_logs / query_traces.
	ctx = &gcpAuditContextWrapper{
		CloudProviderContext: ctx,
		enrichedCtx: WithGCPAuditInfo(ctx.GetContext(), &GCPAuditInfo{
			TenantID:       extractGcloudTenantID(ctx),
			CloudAccountID: account.ID,
			AccountNumber:  account.AccountNumber,
			ServiceName:    ServiceNameRun,
		}),
	}

	session, err := getGcloudSessionFromAccount(ctx, account)
	if err != nil {
		RecordGCPPermissionError(ctx, err)
		logger.Error("failed to get gcloud session for QueryDeploymentDiff", "error", err, "accountNumber", account.AccountNumber)
		return providers.QueryDeploymentDiffResponse{Status: "Failed"}, fmt.Errorf("failed to get gcloud session: %w", err)
	}

	client, err := run.NewRevisionsClient(ctx.GetContext(), session.Opts...)
	if err != nil {
		RecordGCPPermissionError(ctx, err)
		logger.Error("failed to create Cloud Run revisions client", "error", err, "projectId", session.ProjectId)
		return providers.QueryDeploymentDiffResponse{Status: "Failed"}, fmt.Errorf("failed to create revisions client: %w", err)
	}
	defer func() {
		if cerr := client.Close(); cerr != nil {
			logger.Error("failed to close revisions client", "error", cerr)
		}
	}()

	limit := defaultDeployDiffRevisions
	if query.Limit != nil && *query.Limit > 0 {
		limit = int(*query.Limit)
	}
	if limit > maxDeployDiffRevisions {
		limit = maxDeployDiffRevisions
	}

	parent := fmt.Sprintf("projects/%s/locations/%s/services/%s", session.ProjectId, query.Region, query.ServiceName)
	logger.Info("querying Cloud Run revisions for deployment diff", "parent", parent, "limit", limit)

	it := client.ListRevisions(ctx.GetContext(), &runpb.ListRevisionsRequest{
		Parent:   parent,
		PageSize: int32(maxDeployDiffRevisions),
	})

	var revs []*runpb.Revision
	for len(revs) < maxDeployDiffRevisions {
		rev, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			RecordGCPPermissionError(ctx, err)
			if isGCPPermissionOrNotFoundError(err) {
				logger.Warn("skipping deployment diff — API disabled, permission denied, or service not found", "error", err, "parent", parent)
				return providers.QueryDeploymentDiffResponse{Status: "Failed"}, err
			}
			logger.Error("error listing Cloud Run revisions", "error", err, "parent", parent)
			break
		}
		revs = append(revs, rev)
	}

	// Newest first. The API does not guarantee order, so sort by CreateTime desc.
	sort.SliceStable(revs, func(i, j int) bool {
		return revisionCreateMillis(revs[i]) > revisionCreateMillis(revs[j])
	})
	if len(revs) > limit {
		revs = revs[:limit]
	}

	items := make([]providers.DeploymentRevisionItem, 0, len(revs))
	for _, rev := range revs {
		yamlSpec, err := revisionToSpecYAML(rev)
		if err != nil {
			logger.Warn("failed to serialize revision spec", "error", err, "revision", rev.GetName())
			continue
		}
		items = append(items, providers.DeploymentRevisionItem{
			Name:       shortRevisionName(rev.GetName()),
			CreateTime: revisionCreateMillis(rev),
			Creator:    rev.GetCreator(),
			SpecYAML:   yamlSpec,
		})
	}

	logger.Info("Cloud Run deployment diff complete", "parent", parent, "revisions", len(items))
	return providers.QueryDeploymentDiffResponse{Revisions: items, Status: "Complete"}, nil
}

func revisionCreateMillis(rev *runpb.Revision) int64 {
	if rev.GetCreateTime() != nil {
		return rev.GetCreateTime().AsTime().UnixMilli()
	}
	return 0
}

// shortRevisionName trims the full resource path to the trailing revision id
// (projects/.../revisions/<id> -> <id>).
func shortRevisionName(name string) string {
	for i := len(name) - 1; i >= 0; i-- {
		if name[i] == '/' {
			return name[i+1:]
		}
	}
	return name
}

// revisionContainerView is the deploy-relevant subset of a container, ordered for a
// stable, low-noise YAML diff.
type revisionContainerView struct {
	Name    string            `yaml:"name,omitempty"`
	Image   string            `yaml:"image"`
	Command []string          `yaml:"command,omitempty"`
	Args    []string          `yaml:"args,omitempty"`
	CPU     string            `yaml:"cpu,omitempty"`
	Memory  string            `yaml:"memory,omitempty"`
	Env     map[string]string `yaml:"env,omitempty"`
}

// revisionSpecView is the normalized, status-stripped desired-state of a revision —
// the spec surface that actually changes behavior across a deploy. Status fields
// (conditions, observedGeneration, scalingStatus) and managed metadata (uid, etag,
// generation) are intentionally excluded so the diff highlights real changes.
type revisionSpecView struct {
	Containers           []revisionContainerView `yaml:"containers"`
	MinInstances         int32                   `yaml:"minInstances"`
	MaxInstances         int32                   `yaml:"maxInstances"`
	ContainerConcurrency int32                   `yaml:"containerConcurrency,omitempty"`
	TimeoutSeconds       int64                   `yaml:"timeoutSeconds,omitempty"`
	ServiceAccount       string                  `yaml:"serviceAccount,omitempty"`
	VPCConnector         string                  `yaml:"vpcConnector,omitempty"`
	ExecutionEnvironment string                  `yaml:"executionEnvironment,omitempty"`
}

func revisionToSpecYAML(rev *runpb.Revision) (string, error) {
	view := revisionSpecView{
		ServiceAccount: rev.GetServiceAccount(),
	}

	if s := rev.GetScaling(); s != nil {
		view.MinInstances = s.GetMinInstanceCount()
		view.MaxInstances = s.GetMaxInstanceCount()
	}
	view.ContainerConcurrency = rev.GetMaxInstanceRequestConcurrency()
	if t := rev.GetTimeout(); t != nil {
		view.TimeoutSeconds = int64(t.AsDuration().Seconds())
	}
	if v := rev.GetVpcAccess(); v != nil {
		view.VPCConnector = v.GetConnector()
	}
	if rev.GetExecutionEnvironment() != runpb.ExecutionEnvironment_EXECUTION_ENVIRONMENT_UNSPECIFIED {
		view.ExecutionEnvironment = rev.GetExecutionEnvironment().String()
	}

	for _, c := range rev.GetContainers() {
		cv := revisionContainerView{
			Name:    c.GetName(),
			Image:   c.GetImage(),
			Command: c.GetCommand(),
			Args:    c.GetArgs(),
		}
		if r := c.GetResources(); r != nil && r.GetLimits() != nil {
			cv.CPU = r.GetLimits()["cpu"]
			cv.Memory = r.GetLimits()["memory"]
		}
		if len(c.GetEnv()) > 0 {
			cv.Env = make(map[string]string, len(c.GetEnv()))
			for _, e := range c.GetEnv() {
				if e.GetValueSource() != nil {
					cv.Env[e.GetName()] = secretEnvPlaceholder
				} else {
					cv.Env[e.GetName()] = e.GetValue()
				}
			}
		}
		view.Containers = append(view.Containers, cv)
	}

	out, err := yaml.Marshal(view)
	if err != nil {
		return "", err
	}
	return string(out), nil
}
