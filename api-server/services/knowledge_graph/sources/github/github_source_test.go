package github

import (
	"testing"

	gogithub "github.com/google/go-github/v61/github"

	"nudgebee/services/knowledge_graph/core"
)

func testReq() *core.SourceBuildRequest {
	return &core.SourceBuildRequest{TenantID: "tenant-1", CloudAccountID: "integration-1"}
}

func TestSpecificTypeSchemasRegistered(t *testing.T) {
	for _, specificType := range []string{"GitHubOrganization", "GitHubRepository", "GitHubTeam", "GitHubUser"} {
		if _, ok := core.LookupSpecificTypeSchema(specificType); !ok {
			t.Errorf("expected specific_type schema %q to be registered", specificType)
		}
	}
}

func TestDerivePermission(t *testing.T) {
	cases := []struct {
		name     string
		roleName *string
		perms    map[string]bool
		want     string
	}{
		{"role name wins", gogithub.String("maintain"), map[string]bool{"admin": true}, "maintain"},
		{"falls back to highest true permission", nil, map[string]bool{"pull": true, "push": true}, "push"},
		{"admin beats everything", nil, map[string]bool{"admin": true, "pull": true}, "admin"},
		{"no permissions", nil, map[string]bool{}, ""},
		{"empty role name ignored", gogithub.String(""), map[string]bool{"pull": true}, "pull"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := derivePermission(tc.roleName, tc.perms); got != tc.want {
				t.Errorf("derivePermission() = %q, want %q", got, tc.want)
			}
		})
	}
}

func newGitHubSourceForTest() *GitHubSource {
	return NewGitHubSource(nil)
}

func TestNewRepoNode(t *testing.T) {
	s := newGitHubSourceForTest()
	repo := &gogithub.Repository{
		Name:          gogithub.String("widget"),
		FullName:      gogithub.String("acme/widget"),
		Description:   gogithub.String("a widget"),
		Language:      gogithub.String("Go"),
		HTMLURL:       gogithub.String("https://github.com/acme/widget"),
		DefaultBranch: gogithub.String("main"),
		Private:       gogithub.Bool(true),
		Archived:      gogithub.Bool(false),
		Topics:        []string{"cli", "infra"},
	}

	node := s.newRepoNode(repo, testReq())

	if node.NodeType != core.NodeTypeRepository {
		t.Errorf("NodeType = %v, want %v", node.NodeType, core.NodeTypeRepository)
	}
	if node.SpecificType != "GitHubRepository" {
		t.Errorf("SpecificType = %q, want GitHubRepository", node.SpecificType)
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
	if node.Properties["topics"] != "cli,infra" {
		t.Errorf("topics = %v, want cli,infra", node.Properties["topics"])
	}
	if node.Properties["private"] != true {
		t.Errorf("private = %v, want true", node.Properties["private"])
	}
	// specific_type must never leak into Properties — core.NewNode lifts it into
	// the dedicated column.
	if _, present := node.Properties["specific_type"]; present {
		t.Errorf("properties should not contain specific_type after NewNode")
	}
}

func TestNewUserNodeDedup(t *testing.T) {
	s := newGitHubSourceForTest()
	userNodes := make(map[string]*core.DbNode)
	user := &gogithub.User{Login: gogithub.String("octocat"), Name: gogithub.String("The Octocat")}

	node1, isNew1 := s.newUserNode(user, userNodes, testReq())
	if !isNew1 {
		t.Fatal("expected first call to report isNew=true")
	}
	if node1.Properties["username"] != "octocat" {
		t.Errorf("username = %v, want octocat", node1.Properties["username"])
	}
	if node1.Properties["name"] != "The Octocat" {
		t.Errorf("name = %v, want The Octocat", node1.Properties["name"])
	}

	node2, isNew2 := s.newUserNode(user, userNodes, testReq())
	if isNew2 {
		t.Error("expected second call for the same login to report isNew=false")
	}
	if node1 != node2 {
		t.Error("expected the same node instance to be returned for a repeated login")
	}
}

func TestNewUserNodeNilLogin(t *testing.T) {
	s := newGitHubSourceForTest()
	userNodes := make(map[string]*core.DbNode)
	node, isNew := s.newUserNode(&gogithub.User{}, userNodes, testReq())
	if node != nil || isNew {
		t.Errorf("expected (nil, false) for a user with no login, got (%v, %v)", node, isNew)
	}
}

func TestNewTeamNodeFallsBackToSlugWhenNameMissing(t *testing.T) {
	s := newGitHubSourceForTest()
	team := &gogithub.Team{Slug: gogithub.String("platform-eng")}

	node := s.newTeamNode(team, testReq())

	if node.Properties["name"] != "platform-eng" {
		t.Errorf("name = %v, want platform-eng (fallback to slug)", node.Properties["name"])
	}
	if node.Properties["slug"] != "platform-eng" {
		t.Errorf("slug = %v, want platform-eng", node.Properties["slug"])
	}
	if node.NodeType != core.NodeTypeUserGroup {
		t.Errorf("NodeType = %v, want %v", node.NodeType, core.NodeTypeUserGroup)
	}
}
