package gitlab

import (
	"context"
	"fmt"

	golangGitlab "gitlab.com/gitlab-org/api/client-go"

	"nudgebee/services/knowledge_graph/core"
)

// gitlabProjectSchema is the concrete property schema for the GitLabProject
// specific_type. Names are the node.Properties keys written by newProjectNode.
var gitlabProjectSchema = core.SpecificTypeSchema{
	SpecificType: "GitLabProject",
	NodeType:     core.NodeTypeRepository,
	Properties: []core.PropertyDef{
		{Name: "full_name", Indexed: true},
		{Name: "language", Indexed: true},
		{Name: "private", Indexed: true},
		{Name: "archived", Indexed: true},
		{Name: "default_branch"},
		{Name: "url", Indexed: true},
		{Name: "description"},
	},
}

func init() { core.RegisterSpecificTypeSchema(gitlabProjectSchema) }

// fetchGroupProjects lists every project under the integration's root group,
// including subgroups (a single call with IncludeSubGroups, rather than one call per
// group), and attributes each project's OWNS edge to whichever group actually owns
// it — resolved via the project's namespace ID against groupNodesByID — instead of
// blanket-attributing every project to the root org.
func (s *GitLabSource) fetchGroupProjects(
	ctx context.Context,
	client *golangGitlab.Client,
	rootGroupID int64,
	groupNodesByID map[int64]*core.DbNode,
	rootOrgNode *core.DbNode,
	req *core.SourceBuildRequest,
) ([]*core.DbNode, []*core.DbEdge, map[int64]*core.DbNode, error) {
	var projects []*golangGitlab.Project
	opts := &golangGitlab.ListGroupProjectsOptions{
		ListOptions:      golangGitlab.ListOptions{PerPage: 100},
		IncludeSubGroups: golangGitlab.Ptr(true),
	}
	for {
		page, resp, err := client.Groups.ListGroupProjects(rootGroupID, opts, golangGitlab.WithContext(ctx))
		if err != nil {
			return nil, nil, nil, fmt.Errorf("list group projects: %w", err)
		}
		projects = append(projects, page...)
		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}

	nodes := make([]*core.DbNode, 0, len(projects))
	edges := make([]*core.DbEdge, 0, len(projects))
	projectsByID := make(map[int64]*core.DbNode, len(projects))

	for _, project := range projects {
		if project == nil {
			continue
		}
		language := s.fetchPrimaryLanguage(ctx, client, project)
		node := s.newProjectNode(project, language, req)
		nodes = append(nodes, node)
		projectsByID[project.ID] = node

		ownerNode := rootOrgNode
		if project.Namespace != nil {
			if n, ok := groupNodesByID[project.Namespace.ID]; ok {
				ownerNode = n
			}
		}
		edges = append(edges, s.newEdge(ownerNode, node, core.RelationshipOwns, nil, req))
	}

	return nodes, edges, projectsByID, nil
}

// fetchOwnedProjects lists projects owned by the authenticated user directly — the
// personal-namespace fallback used when the integration has no `group` configured
// (GitLab's Owned option, the GitLab equivalent of GitHub's Affiliation: "owner").
func (s *GitLabSource) fetchOwnedProjects(
	ctx context.Context,
	client *golangGitlab.Client,
	ownerNode *core.DbNode,
	req *core.SourceBuildRequest,
) ([]*core.DbNode, []*core.DbEdge, map[int64]*core.DbNode, error) {
	var projects []*golangGitlab.Project
	opts := &golangGitlab.ListProjectsOptions{
		ListOptions: golangGitlab.ListOptions{PerPage: 100},
		Owned:       golangGitlab.Ptr(true),
	}
	for {
		page, resp, err := client.Projects.ListProjects(opts, golangGitlab.WithContext(ctx))
		if err != nil {
			return nil, nil, nil, fmt.Errorf("list owned projects: %w", err)
		}
		projects = append(projects, page...)
		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}

	nodes := make([]*core.DbNode, 0, len(projects))
	edges := make([]*core.DbEdge, 0, len(projects))
	projectsByID := make(map[int64]*core.DbNode, len(projects))

	for _, project := range projects {
		if project == nil {
			continue
		}
		language := s.fetchPrimaryLanguage(ctx, client, project)
		node := s.newProjectNode(project, language, req)
		nodes = append(nodes, node)
		projectsByID[project.ID] = node
		edges = append(edges, s.newEdge(ownerNode, node, core.RelationshipOwns, nil, req))
	}

	return nodes, edges, projectsByID, nil
}

// newProjectNode builds a Repository node for a GitLab project. language is
// resolved separately by fetchPrimaryLanguage (GitLab doesn't inline languages on
// the project list/get response) and passed in already-fetched so this stays a pure
// property-mapping function, like GitHub's newRepoNode.
func (s *GitLabSource) newProjectNode(project *golangGitlab.Project, language string, req *core.SourceBuildRequest) *core.DbNode {
	properties := map[string]interface{}{
		"specific_type": "GitLabProject",
		"name":          project.Name,
	}
	if project.PathWithNamespace != "" {
		properties["full_name"] = project.PathWithNamespace
	}
	if project.Description != "" {
		properties["description"] = project.Description
	}
	if project.WebURL != "" {
		properties["url"] = project.WebURL
	}
	if project.DefaultBranch != "" {
		properties["default_branch"] = project.DefaultBranch
	}
	properties["private"] = project.Visibility == golangGitlab.PrivateVisibility
	properties["archived"] = project.Archived
	if language != "" {
		properties["language"] = language
	}

	return s.newNode(core.NodeTypeRepository, properties, req)
}

// fetchPrimaryLanguage returns the language with the highest percentage from
// GitLab's per-project language breakdown, or "" if the call fails or the project
// has no detected languages (e.g. empty repository). A failure here is non-fatal and
// only costs this one project its language property, since Repository nodes aren't
// cross-source reconciled (see InfraAuthoritativeNodeTypes).
func (s *GitLabSource) fetchPrimaryLanguage(ctx context.Context, client *golangGitlab.Client, project *golangGitlab.Project) string {
	languages, _, err := client.Projects.GetProjectLanguages(project.ID, golangGitlab.WithContext(ctx))
	if err != nil {
		if ctx.Err() == nil {
			s.logger.Warn("gitlab: failed to fetch project languages", "project", project.PathWithNamespace, "error", err)
		}
		return ""
	}
	return pickPrimaryLanguage(languages)
}

// pickPrimaryLanguage is the pure selection logic behind fetchPrimaryLanguage,
// split out so it's testable without a network-backed client.
func pickPrimaryLanguage(languages *golangGitlab.ProjectLanguages) string {
	if languages == nil {
		return ""
	}
	var topLanguage string
	var topPercent float32
	for lang, percent := range *languages {
		if percent > topPercent {
			topPercent = percent
			topLanguage = lang
		}
	}
	return topLanguage
}
