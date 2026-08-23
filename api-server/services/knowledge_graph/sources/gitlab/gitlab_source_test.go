package gitlab

import (
	"testing"

	golangGitlab "gitlab.com/gitlab-org/api/client-go"

	"nudgebee/services/knowledge_graph/core"
)

func testReq() *core.SourceBuildRequest {
	return &core.SourceBuildRequest{TenantID: "tenant-1", CloudAccountID: "integration-1"}
}

func TestSpecificTypeSchemasRegistered(t *testing.T) {
	for _, specificType := range []string{"GitLabOrganization", "GitLabGroup", "GitLabProject", "GitLabUser"} {
		if _, ok := core.LookupSpecificTypeSchema(specificType); !ok {
			t.Errorf("expected specific_type schema %q to be registered", specificType)
		}
	}
}

func TestAccessLevelRole(t *testing.T) {
	cases := []struct {
		name  string
		level golangGitlab.AccessLevelValue
		want  string
	}{
		{"owner", golangGitlab.OwnerPermissions, "owner"},
		{"maintainer", golangGitlab.MaintainerPermissions, "maintainer"},
		{"developer", golangGitlab.DeveloperPermissions, "developer"},
		{"reporter", golangGitlab.ReporterPermissions, "reporter"},
		{"guest", golangGitlab.GuestPermissions, "guest"},
		{"unknown level", golangGitlab.AccessLevelValue(0), ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := accessLevelRole(tc.level); got != tc.want {
				t.Errorf("accessLevelRole() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestPickPrimaryLanguage(t *testing.T) {
	cases := []struct {
		name      string
		languages *golangGitlab.ProjectLanguages
		want      string
	}{
		{"nil languages", nil, ""},
		{"empty languages", &golangGitlab.ProjectLanguages{}, ""},
		{
			"picks highest percentage",
			&golangGitlab.ProjectLanguages{"Go": 20.5, "TypeScript": 75.0, "Shell": 4.5},
			"TypeScript",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := pickPrimaryLanguage(tc.languages); got != tc.want {
				t.Errorf("pickPrimaryLanguage() = %q, want %q", got, tc.want)
			}
		})
	}
}

func newGitLabSourceForTest() *GitLabSource {
	return NewGitLabSource(nil)
}

func TestNewProjectNode(t *testing.T) {
	s := newGitLabSourceForTest()
	project := &golangGitlab.Project{
		ID:                1,
		Name:              "widget",
		PathWithNamespace: "acme/widget",
		Description:       "a widget",
		WebURL:            "https://gitlab.com/acme/widget",
		DefaultBranch:     "main",
		Visibility:        golangGitlab.PrivateVisibility,
		Archived:          false,
	}

	node := s.newProjectNode(project, "Go", testReq())

	if node.NodeType != core.NodeTypeRepository {
		t.Errorf("NodeType = %v, want %v", node.NodeType, core.NodeTypeRepository)
	}
	if node.SpecificType != "GitLabProject" {
		t.Errorf("SpecificType = %q, want GitLabProject", node.SpecificType)
	}
	if node.Properties["name"] != "widget" {
		t.Errorf("name = %v, want widget", node.Properties["name"])
	}
	if node.Properties["full_name"] != "acme/widget" {
		t.Errorf("full_name = %v, want acme/widget", node.Properties["full_name"])
	}
	if node.Properties["language"] != "Go" {
		t.Errorf("language = %v, want Go", node.Properties["language"])
	}
	if node.Properties["private"] != true {
		t.Errorf("private = %v, want true", node.Properties["private"])
	}
	if node.Properties["archived"] != false {
		t.Errorf("archived = %v, want false", node.Properties["archived"])
	}
	// specific_type must never leak into Properties — core.NewNode lifts it into
	// the dedicated column.
	if _, present := node.Properties["specific_type"]; present {
		t.Errorf("properties should not contain specific_type after NewNode")
	}
}

func TestNewUserNodeDedup(t *testing.T) {
	s := newGitLabSourceForTest()
	userNodes := make(map[string]*core.DbNode)

	node1, isNew1 := s.newUserNode("octocat", "The Octocat", "octo@example.com", "https://gitlab.com/octocat", userNodes, testReq())
	if !isNew1 {
		t.Fatal("expected first call to report isNew=true")
	}
	if node1.Properties["username"] != "octocat" {
		t.Errorf("username = %v, want octocat", node1.Properties["username"])
	}
	if node1.Properties["name"] != "The Octocat" {
		t.Errorf("name = %v, want The Octocat", node1.Properties["name"])
	}

	node2, isNew2 := s.newUserNode("octocat", "The Octocat", "octo@example.com", "https://gitlab.com/octocat", userNodes, testReq())
	if isNew2 {
		t.Error("expected second call for the same username to report isNew=false")
	}
	if node1 != node2 {
		t.Error("expected the same node instance to be returned for a repeated username")
	}
}

func TestNewUserNodeEmptyUsername(t *testing.T) {
	s := newGitLabSourceForTest()
	userNodes := make(map[string]*core.DbNode)
	node, isNew := s.newUserNode("", "", "", "", userNodes, testReq())
	if node != nil || isNew {
		t.Errorf("expected (nil, false) for a user with no username, got (%v, %v)", node, isNew)
	}
}

func TestNewUserNodeNameFallsBackToUsername(t *testing.T) {
	s := newGitLabSourceForTest()
	userNodes := make(map[string]*core.DbNode)
	node, _ := s.newUserNode("octocat", "", "", "", userNodes, testReq())
	if node.Properties["name"] != "octocat" {
		t.Errorf("name = %v, want octocat (fallback to username)", node.Properties["name"])
	}
}

func TestNewGroupNode(t *testing.T) {
	s := newGitLabSourceForTest()
	group := &golangGitlab.Group{
		ID:          2,
		FullPath:    "acme/platform",
		WebURL:      "https://gitlab.com/acme/platform",
		Description: "platform subgroup",
	}

	node := s.newGroupNode(group, testReq())

	if node.NodeType != core.NodeTypeUserGroup {
		t.Errorf("NodeType = %v, want %v", node.NodeType, core.NodeTypeUserGroup)
	}
	if node.SpecificType != "GitLabGroup" {
		t.Errorf("SpecificType = %q, want GitLabGroup", node.SpecificType)
	}
	if node.Properties["name"] != "acme/platform" {
		t.Errorf("name = %v, want acme/platform", node.Properties["name"])
	}
	if node.Properties["url"] != "https://gitlab.com/acme/platform" {
		t.Errorf("url = %v, want https://gitlab.com/acme/platform", node.Properties["url"])
	}
}
