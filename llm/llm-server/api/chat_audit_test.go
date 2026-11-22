package api

import (
	"strings"
	"testing"

	"nudgebee/llm/agents/core"
	"nudgebee/llm/audit"

	"github.com/stretchr/testify/assert"
)

func TestToolConfirmationDecision(t *testing.T) {
	cases := map[string]string{
		"yes":       "approved",
		"Yes":       "approved",
		" YES ":     "approved",
		"approve":   "approved",
		"proceed":   "approved",
		"no":        "rejected",
		"No":        "rejected",
		"reject":    "rejected",
		"cancelled": "rejected",
		"maybe":     "other",
		"":          "other",
		"do it now": "other",
	}
	for response, want := range cases {
		t.Run(response, func(t *testing.T) {
			assert.Equal(t, want, toolConfirmationDecision(response))
		})
	}
}

func TestTruncateQueryForAudit(t *testing.T) {
	t.Run("short query is returned unchanged", func(t *testing.T) {
		q := "why is my pod crashlooping?"
		assert.Equal(t, q, truncateQueryForAudit(q))
	})

	t.Run("empty query is returned unchanged", func(t *testing.T) {
		assert.Equal(t, "", truncateQueryForAudit(""))
	})

	t.Run("query at the cap is not truncated", func(t *testing.T) {
		q := strings.Repeat("a", maxAuditQueryPreviewRunes)
		assert.Equal(t, q, truncateQueryForAudit(q))
	})

	t.Run("long query is capped with marker", func(t *testing.T) {
		q := strings.Repeat("a", maxAuditQueryPreviewRunes+50)
		got := truncateQueryForAudit(q)
		assert.Equal(t, strings.Repeat("a", maxAuditQueryPreviewRunes)+"…[truncated]", got)
		assert.Equal(t, maxAuditQueryPreviewRunes, len([]rune(strings.TrimSuffix(got, "…[truncated]"))))
	})

	t.Run("multi-byte runes are not split", func(t *testing.T) {
		// Each emoji is multi-byte but a single rune; truncation must cut on rune boundaries so the preview stays valid UTF-8.
		q := strings.Repeat("🚀", maxAuditQueryPreviewRunes+10)
		got := truncateQueryForAudit(q)
		assert.True(t, strings.HasSuffix(got, "…[truncated]"))
		assert.Equal(t, strings.Repeat("🚀", maxAuditQueryPreviewRunes), strings.TrimSuffix(got, "…[truncated]"))
	})
}

// A follow-up answer's audit keeps what was asked and approved, not the
// credentials in it.
func TestFollowupAuditStateMasksCredentials(t *testing.T) {
	const secret = "Qv7rTx2mWp9z"
	for _, command := range []string{
		"PGPASSWORD=" + secret + " psql -h db.prod -U app -c 'VACUUM'",
		"mysql -uroot -p" + secret + " -e 'SHOW TABLES'",
		"kubectl 'exec' 'cache-0' '--' 'redis-cli' '-a' '" + secret + "' 'FLUSHALL'",
		"curl -u admin:" + secret + " -X POST https://grafana.internal/api/admin/users",
		"aws rds modify-db-instance --db-instance-identifier prod --master-user-password " + secret,
	} {
		t.Run(command, func(t *testing.T) {
			info := &core.PendingFollowupInfo{
				FollowupType:       "tool_confirmation",
				IsToolConfirmation: true,
				ToolName:           "execute_command",
				Command:            command,
				Query:              "reset the db, the url is postgres://app:" + secret + "@db.prod/app",
			}
			state := followupAuditState(info, "yes")
			for key, value := range state {
				assert.NotContains(t, value, secret, key)
			}
			assert.Contains(t, state["command"], "[REDACTED:")
			assert.Equal(t, "execute_command", state["tool_name"])
			assert.Equal(t, "approved", state["decision"])
			assert.Contains(t, state["query"], "@db.prod/app")
		})
	}

	t.Run("clarification answer", func(t *testing.T) {
		state := followupAuditState(&core.PendingFollowupInfo{FollowupType: "text", Query: "which token?"}, "use GITHUB_TOKEN=ghp_"+strings.Repeat("a", 36))
		assert.NotContains(t, state["response"], "ghp_aaaa")
		assert.NotContains(t, state, "command", "only a tool confirmation records a command")
	})

	t.Run("a secret straddling the preview cap is masked whole", func(t *testing.T) {
		query := strings.Repeat("x", maxAuditQueryPreviewRunes-12) + " PASSWORD=" + secret + " " + strings.Repeat("y", 2000)
		state := followupAuditState(&core.PendingFollowupInfo{FollowupType: "text", Query: query}, "ok")
		preview := state["query"].(string)
		assert.True(t, strings.HasSuffix(preview, "…[truncated]"))
		assert.NotContains(t, preview, secret[:2], "no unmasked prefix of the secret is kept")
	})
}

func TestChatAuditApplies(t *testing.T) {
	assert.True(t, chatAuditApplies(audit.EventTypeChatClarificationRespond, core.ConversationSourceUserInvestigation))
	assert.False(t, chatAuditApplies(audit.EventTypeChatClarificationRespond, core.ConversationSourceInvestigation))
	for _, source := range []core.ConversationSource{core.ConversationSourceUserInvestigation, core.ConversationSourceInvestigation, core.ConversationSourceAutomation} {
		assert.True(t, chatAuditApplies(audit.EventTypeChatToolConfirmationRespond, source), source)
	}
}
