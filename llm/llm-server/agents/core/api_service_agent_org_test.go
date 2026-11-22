package core

import (
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateOrgAgentTools(t *testing.T) {
	const tenant = "00000000-0000-0000-0000-000000000001"
	visibilityRows := func(v ...string) *sqlmock.Rows {
		rows := sqlmock.NewRows([]string{"visibility"})
		for _, x := range v {
			rows.AddRow(x)
		}
		return rows
	}

	tests := []struct {
		name     string
		tool     string
		toolRows []string // visibilities of llm_tools rows with that name
		agentRow []string // visibilities of llm_agents rows with that name (queried only when no tool row)
		mcp      []string // the tenant's MCP integrations (queried only when neither table has the name)
		want     AgentPlannerType
		wantErr  string
	}{
		{name: "organisation tool", tool: "deploy-check", toolRows: []string{"organisation"}, want: AgentPlannerTypeReAct},
		{name: "tool of one account", tool: "deploy-check", toolRows: []string{"account"},
			wantErr: "deploy-check, which not every account can use; an organisation agent can use built-in, organisation-wide and MCP tools only"},
		{name: "tool of selected accounts", tool: "deploy-check", toolRows: []string{"selected_accounts"}, wantErr: "deploy-check, which not every account can use"},
		{name: "organisation agent as a tool", tool: "triage", agentRow: []string{"organisation"}, want: AgentPlannerTypeOrchestrating},
		{name: "agent of selected accounts", tool: "triage", agentRow: []string{"selected_accounts"}, wantErr: "triage, which not every account can use"},
		{name: "agent of one account", tool: "triage", agentRow: []string{"account"}, wantErr: "triage, which not every account can use"},
		{name: "unknown name without an account", tool: "nothing-here", mcp: []string{"GitHub Prod"}, wantErr: "nothing-here which does not exist"},
		{name: "MCP tool without an account", tool: "github_prod_list_issues", mcp: []string{"GitHub Prod"}, want: AgentPlannerTypeReAct},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer func() { _ = db.Close() }()

			mock.ExpectQuery(`FROM llm_tools WHERE type = 'custom'`).WithArgs(tenant, tt.tool).WillReturnRows(visibilityRows(tt.toolRows...))
			if len(tt.toolRows) == 0 {
				mock.ExpectQuery(`FROM llm_agents WHERE type = 'custom'`).WithArgs(tenant, tt.tool).WillReturnRows(visibilityRows(tt.agentRow...))
			}
			if len(tt.toolRows) == 0 && len(tt.agentRow) == 0 {
				mcp := sqlmock.NewRows([]string{"name"})
				for _, n := range tt.mcp {
					mcp.AddRow(n)
				}
				mock.ExpectQuery(`FROM integrations WHERE tenant_id`).WithArgs(tenant).WillReturnRows(mcp)
			}

			got, err := validateOrgAgentTools(sqlx.NewDb(db, "postgres"), tenant, "", "self", []string{tt.tool})
			if tt.wantErr != "" {
				assert.ErrorContains(t, err, tt.wantErr)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.want, got)
			}
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestValidateOrgAgentToolsRefusesItself(t *testing.T) {
	_, err := validateOrgAgentTools(nil, "t", "", "Triage", []string{"triage"})
	assert.ErrorContains(t, err, "cannot contain name of agent itself")
}
