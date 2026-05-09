package agents

import (
	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func skipIfNoTestEnv(t *testing.T) {
	t.Helper()
	if os.Getenv("TEST_ACCOUNT") == "" || os.Getenv("TEST_USER") == "" {
		t.Skip("Skipping: TEST_ACCOUNT and TEST_USER env vars required")
	}
}

func TestTicketV2Agent_CreateTicketFlow(t *testing.T) {
	skipIfNoTestEnv(t)
	agent := TicketMasterV2Agent{}
	sc := security.NewRequestContextForSuperAdmin()

	testCases := []struct {
		Name      string
		SessionId string
		Query     string
		AccountId string
		UserId    string
	}{
		{
			Name:      "Create ticket with title and description",
			SessionId: "ut-ticket-v2-create-1",
			AccountId: os.Getenv("TEST_ACCOUNT"),
			UserId:    os.Getenv("TEST_USER"),
			Query:     "Create a ticket for OOM issue in production k8s cluster. Pod api-server keeps getting killed.",
		},
		{
			Name:      "Create ticket with severity",
			SessionId: "ut-ticket-v2-create-2",
			AccountId: os.Getenv("TEST_ACCOUNT"),
			UserId:    os.Getenv("TEST_USER"),
			Query:     "Create a high priority bug ticket: Database connection pool exhaustion causing 500 errors on the checkout service",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.Name, func(t *testing.T) {
			err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
			assert.Nil(t, err)

			resp, err := core.HandleConversationSessionRequest(sc, agent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
			assert.Nil(t, err)
			assert.NotNil(t, resp)
			assert.Equal(t, TicketsV2AgentName, agent.GetName())
			assert.NotEmpty(t, resp.Query)
			assert.NotNil(t, resp.AgentStepResponse)
			assert.Greater(t, len(resp.Response), 0)
		})
	}
}

func TestTicketV2Agent_AddComment(t *testing.T) {
	skipIfNoTestEnv(t)
	agent := TicketMasterV2Agent{}
	sc := security.NewRequestContextForSuperAdmin()

	testCases := []struct {
		Name      string
		SessionId string
		Query     string
		AccountId string
		UserId    string
	}{
		{
			Name:      "Add comment to existing ticket",
			SessionId: "ut-ticket-v2-comment-1",
			AccountId: os.Getenv("TEST_ACCOUNT"),
			UserId:    os.Getenv("TEST_USER"),
			Query:     "Add a comment to ticket PROJ-123: 'Root cause identified - memory leak in cache layer. Fix deployed in v2.3.1'",
		},
		{
			Name:      "Add comment with detailed analysis",
			SessionId: "ut-ticket-v2-comment-2",
			AccountId: os.Getenv("TEST_ACCOUNT"),
			UserId:    os.Getenv("TEST_USER"),
			Query:     "Comment on ticket INFRA-456: 'Scaled up the replica count from 3 to 5. CPU usage dropped from 95% to 60%. Monitoring for stability.'",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.Name, func(t *testing.T) {
			err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
			assert.Nil(t, err)

			resp, err := core.HandleConversationSessionRequest(sc, agent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
			assert.Nil(t, err)
			assert.NotNil(t, resp)
			assert.Equal(t, TicketsV2AgentName, agent.GetName())
			assert.Greater(t, len(resp.Response), 0)
		})
	}
}

func TestTicketV2Agent_GetComments(t *testing.T) {
	skipIfNoTestEnv(t)
	agent := TicketMasterV2Agent{}
	sc := security.NewRequestContextForSuperAdmin()

	testCases := []struct {
		Name      string
		SessionId string
		Query     string
		AccountId string
		UserId    string
	}{
		{
			Name:      "Get comments from a ticket",
			SessionId: "ut-ticket-v2-getcomments-1",
			AccountId: os.Getenv("TEST_ACCOUNT"),
			UserId:    os.Getenv("TEST_USER"),
			Query:     "Show me all comments on ticket PROJ-123",
		},
		{
			Name:      "Get comments with platform hint",
			SessionId: "ut-ticket-v2-getcomments-2",
			AccountId: os.Getenv("TEST_ACCOUNT"),
			UserId:    os.Getenv("TEST_USER"),
			Query:     "Get the discussion thread on Jira ticket INFRA-789",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.Name, func(t *testing.T) {
			err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
			assert.Nil(t, err)

			resp, err := core.HandleConversationSessionRequest(sc, agent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
			assert.Nil(t, err)
			assert.NotNil(t, resp)
			assert.Equal(t, TicketsV2AgentName, agent.GetName())
			assert.Greater(t, len(resp.Response), 0)
		})
	}
}

func TestTicketV2Agent_GetTicketDetails(t *testing.T) {
	skipIfNoTestEnv(t)
	agent := TicketMasterV2Agent{}
	sc := security.NewRequestContextForSuperAdmin()

	testCases := []struct {
		Name      string
		SessionId string
		Query     string
		AccountId string
		UserId    string
	}{
		{
			Name:      "Get full ticket details by ID",
			SessionId: "ut-ticket-v2-getticket-1",
			AccountId: os.Getenv("TEST_ACCOUNT"),
			UserId:    os.Getenv("TEST_USER"),
			Query:     "Get full details of ticket PROJ-123",
		},
		{
			Name:      "Fetch ticket details with platform hint",
			SessionId: "ut-ticket-v2-getticket-2",
			AccountId: os.Getenv("TEST_ACCOUNT"),
			UserId:    os.Getenv("TEST_USER"),
			Query:     "Show me the details of Jira ticket INFRA-456 including description, assignee and status",
		},
		{
			Name:      "Get GitHub issue details",
			SessionId: "ut-ticket-v2-getticket-3",
			AccountId: os.Getenv("TEST_ACCOUNT"),
			UserId:    os.Getenv("TEST_USER"),
			Query:     "What is the current status of GitHub issue #42 in the nudgebee/api-server repo?",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.Name, func(t *testing.T) {
			err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
			assert.Nil(t, err)

			resp, err := core.HandleConversationSessionRequest(sc, agent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
			assert.Nil(t, err)
			assert.NotNil(t, resp)
			assert.Equal(t, TicketsV2AgentName, agent.GetName())
			assert.Greater(t, len(resp.Response), 0)
		})
	}
}

func TestTicketV2Agent_MultiPlatform(t *testing.T) {
	skipIfNoTestEnv(t)
	agent := TicketMasterV2Agent{}
	sc := security.NewRequestContextForSuperAdmin()

	testCases := []struct {
		Name      string
		SessionId string
		Query     string
		AccountId string
		UserId    string
	}{
		{
			Name:      "Create Jira ticket explicitly",
			SessionId: "ut-ticket-v2-platform-jira",
			AccountId: os.Getenv("TEST_ACCOUNT"),
			UserId:    os.Getenv("TEST_USER"),
			Query:     "Create a Jira ticket in project INFRA: API gateway returning 502 errors intermittently",
		},
		{
			Name:      "Create GitHub issue",
			SessionId: "ut-ticket-v2-platform-github",
			AccountId: os.Getenv("TEST_ACCOUNT"),
			UserId:    os.Getenv("TEST_USER"),
			Query:     "Create a GitHub issue in nudgebee/llm-server: Add retry logic for flaky LLM provider connections",
		},
		{
			Name:      "Create GitLab issue",
			SessionId: "ut-ticket-v2-platform-gitlab",
			AccountId: os.Getenv("TEST_ACCOUNT"),
			UserId:    os.Getenv("TEST_USER"),
			Query:     "Create a GitLab issue in nudgebee/frontend: Dark mode toggle not persisting after page refresh",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.Name, func(t *testing.T) {
			err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
			assert.Nil(t, err)

			resp, err := core.HandleConversationSessionRequest(sc, agent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
			assert.Nil(t, err)
			assert.NotNil(t, resp)
			assert.Equal(t, TicketsV2AgentName, agent.GetName())
			assert.Greater(t, len(resp.Response), 0)
		})
	}
}

func TestTicketV2Agent_ListTickets(t *testing.T) {
	skipIfNoTestEnv(t)
	agent := TicketMasterV2Agent{}
	sc := security.NewRequestContextForSuperAdmin()

	testCases := []struct {
		Name      string
		SessionId string
		Query     string
		AccountId string
		UserId    string
	}{
		{
			Name:      "List all tickets in a project",
			SessionId: "ut-ticket-v2-list-1",
			AccountId: os.Getenv("TEST_ACCOUNT"),
			UserId:    os.Getenv("TEST_USER"),
			Query:     "List all tickets in project INFRA",
		},
		{
			Name:      "List open high-priority tickets",
			SessionId: "ut-ticket-v2-list-2",
			AccountId: os.Getenv("TEST_ACCOUNT"),
			UserId:    os.Getenv("TEST_USER"),
			Query:     "Show me all open high-priority tickets",
		},
		{
			Name:      "List tickets with pagination",
			SessionId: "ut-ticket-v2-list-3",
			AccountId: os.Getenv("TEST_ACCOUNT"),
			UserId:    os.Getenv("TEST_USER"),
			Query:     "List the latest 5 tickets sorted by creation date",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.Name, func(t *testing.T) {
			err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
			assert.Nil(t, err)

			resp, err := core.HandleConversationSessionRequest(sc, agent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
			assert.Nil(t, err)
			assert.NotNil(t, resp)
			assert.Equal(t, TicketsV2AgentName, agent.GetName())
			assert.Greater(t, len(resp.Response), 0)
		})
	}
}

func TestTicketV2Agent_AgentMetadata(t *testing.T) {
	agent := TicketMasterV2Agent{}

	t.Run("Name", func(t *testing.T) {
		assert.Equal(t, "tickets_v2", agent.GetName())
	})

	t.Run("Aliases", func(t *testing.T) {
		aliases := agent.GetNameAliases()
		assert.Contains(t, aliases, "TicketsV2")
	})

	t.Run("PlannerType", func(t *testing.T) {
		assert.Equal(t, core.AgentPlannerTypeReAct, agent.GetPlannerType())
	})

	t.Run("Description", func(t *testing.T) {
		desc := agent.GetDescription()
		assert.Contains(t, desc, "Jira")
		assert.Contains(t, desc, "GitHub")
		assert.Contains(t, desc, "GitLab")
		assert.Contains(t, desc, "ServiceNow")
		assert.Contains(t, desc, "PagerDuty")
		assert.Contains(t, desc, "ZenDuty")
		assert.Contains(t, desc, "Lists tickets")
	})

	t.Run("SupportedTools", func(t *testing.T) {
		sc := security.NewRequestContextForSuperAdmin()
		supportedTools := agent.GetSupportedTools(sc)
		assert.GreaterOrEqual(t, len(supportedTools), 1)

		toolNames := make([]string, len(supportedTools))
		for i, tool := range supportedTools {
			toolNames[i] = tool.Name()
		}
		assert.Contains(t, toolNames, "ticket_master_v2")
	})

	t.Run("SystemPrompt", func(t *testing.T) {
		sc := security.NewRequestContextForSuperAdmin()
		prompt := agent.GetSystemPrompt(sc, core.NBAgentRequest{})
		assert.Equal(t, "An intelligent multi-platform ticketing assistant", prompt.Role)
		assert.NotEmpty(t, prompt.Instructions)
		assert.NotEmpty(t, prompt.Constraints)
		assert.NotEmpty(t, prompt.Examples)
		assert.Equal(t, "Markdown", prompt.OutputFormat)

		// Verify key instructions are present (includes list_tickets instruction)
		assert.GreaterOrEqual(t, len(prompt.Instructions), 8)
		assert.GreaterOrEqual(t, len(prompt.Constraints), 5)

		// Verify tool usage includes ticket_master_v2
		assert.Contains(t, prompt.ToolUsage, "ticket_master_v2")
	})
}
