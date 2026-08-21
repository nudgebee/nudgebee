package pagerduty

import (
	"testing"

	gopagerduty "github.com/PagerDuty/go-pagerduty"

	"nudgebee/services/knowledge_graph/core"
)

func testReq() *core.SourceBuildRequest {
	return &core.SourceBuildRequest{TenantID: "tenant-1", CloudAccountID: "integration-1"}
}

func TestSpecificTypeSchemasRegistered(t *testing.T) {
	for _, specificType := range []string{"PagerDutyUser", "PagerDutyTeam", "PagerDutyService"} {
		if _, ok := core.LookupSpecificTypeSchema(specificType); !ok {
			t.Errorf("expected specific_type schema %q to be registered", specificType)
		}
	}
}

func newPagerDutySourceForTest() *PagerDutySource {
	return NewPagerDutySource(nil)
}

func TestNewUserNode(t *testing.T) {
	s := newPagerDutySourceForTest()
	user := gopagerduty.User{
		APIObject: gopagerduty.APIObject{ID: "PXPGF42", HTMLURL: "https://acme.pagerduty.com/users/PXPGF42"},
		Name:      "The Octocat",
		Email:     "octocat@acme.com",
		Role:      "admin",
	}

	node := s.newUserNode(user, testReq())

	if node.NodeType != core.NodeTypeUserAccount {
		t.Errorf("NodeType = %v, want %v", node.NodeType, core.NodeTypeUserAccount)
	}
	if node.SpecificType != "PagerDutyUser" {
		t.Errorf("SpecificType = %q, want PagerDutyUser", node.SpecificType)
	}
	// name must stay the human-readable display name (drives the unique key).
	if node.Properties["name"] != "The Octocat" {
		t.Errorf("name = %v, want The Octocat", node.Properties["name"])
	}
	// username must be the PagerDuty user ID (not the email) — this is what
	// Identity Sync stores as integration_user_accounts.external_user_id, and what
	// sources/identity_enricher.go matches SAME_AS edges against.
	if node.Properties["username"] != "PXPGF42" {
		t.Errorf("username = %v, want PXPGF42 (the PD user ID, not the email)", node.Properties["username"])
	}
	if node.Properties["email"] != "octocat@acme.com" {
		t.Errorf("email = %v, want octocat@acme.com", node.Properties["email"])
	}
	if node.Properties["role"] != "admin" {
		t.Errorf("role = %v, want admin", node.Properties["role"])
	}
	if _, present := node.Properties["specific_type"]; present {
		t.Errorf("properties should not contain specific_type after NewNode")
	}
}

func TestNewTeamNode(t *testing.T) {
	s := newPagerDutySourceForTest()
	team := gopagerduty.Team{
		APIObject:   gopagerduty.APIObject{ID: "PTEAM1", HTMLURL: "https://acme.pagerduty.com/teams/PTEAM1"},
		Name:        "Platform Engineering",
		Description: "Owns core infra",
	}

	node := s.newTeamNode(team, testReq())

	if node.NodeType != core.NodeTypeUserGroup {
		t.Errorf("NodeType = %v, want %v", node.NodeType, core.NodeTypeUserGroup)
	}
	if node.SpecificType != "PagerDutyTeam" {
		t.Errorf("SpecificType = %q, want PagerDutyTeam", node.SpecificType)
	}
	if node.Properties["name"] != "Platform Engineering" {
		t.Errorf("name = %v, want Platform Engineering", node.Properties["name"])
	}
	if node.Properties["description"] != "Owns core infra" {
		t.Errorf("description = %v, want Owns core infra", node.Properties["description"])
	}
	if node.Properties["url"] != "https://acme.pagerduty.com/teams/PTEAM1" {
		t.Errorf("url = %v, want the team's HTMLURL", node.Properties["url"])
	}
}

func TestNewServiceNode(t *testing.T) {
	s := newPagerDutySourceForTest()
	service := gopagerduty.Service{
		APIObject:   gopagerduty.APIObject{ID: "PSERV1", HTMLURL: "https://acme.pagerduty.com/services/PSERV1"},
		Name:        "checkout-api",
		Description: "Checkout service",
		Status:      "active",
	}

	node := s.newServiceNode(service, testReq())

	if node.NodeType != core.NodeTypeOnCallService {
		t.Errorf("NodeType = %v, want %v", node.NodeType, core.NodeTypeOnCallService)
	}
	if node.SpecificType != "PagerDutyService" {
		t.Errorf("SpecificType = %q, want PagerDutyService", node.SpecificType)
	}
	if node.Properties["name"] != "checkout-api" {
		t.Errorf("name = %v, want checkout-api", node.Properties["name"])
	}
	if node.Properties["status"] != "active" {
		t.Errorf("status = %v, want active", node.Properties["status"])
	}
	if node.Properties["description"] != "Checkout service" {
		t.Errorf("description = %v, want Checkout service", node.Properties["description"])
	}
}

func TestNormalizeEndpoint(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty defaults to public API", "", "https://api.pagerduty.com"},
		{"bare host gets https scheme", "acme.pagerduty.com", "https://acme.pagerduty.com"},
		{"already has scheme", "https://acme.pagerduty.com", "https://acme.pagerduty.com"},
		{"trailing slash trimmed", "https://acme.pagerduty.com/", "https://acme.pagerduty.com"},
		{"http scheme preserved", "http://internal-pd.local", "http://internal-pd.local"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := normalizeEndpoint(tc.in); got != tc.want {
				t.Errorf("normalizeEndpoint(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
