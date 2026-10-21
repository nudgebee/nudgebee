//go:build e2e

package agents

import (
	"nudgebee/llm/common"
	"testing"

	"github.com/stretchr/testify/require"
)

// scenarioLastMsgID returns the most recent message id for a session.
func scenarioLastMsgID(t *testing.T, sessionID, userID string) (convID, msgID string) {
	t.Helper()
	db, err := common.GetDatabaseManager(common.Metastore)
	require.NoError(t, err)
	var row struct {
		ConvID string `db:"conversation_id"`
		MsgID  string `db:"message_id"`
	}
	err = db.Db.Get(&row, `
		SELECT c.id::text AS conversation_id, m.id::text AS message_id
		FROM llm_conversations c
		JOIN llm_conversation_messages m ON m.conversation_id = c.id
		WHERE c.session_id = $1 AND c.user_id = $2::uuid
		ORDER BY m.created_at DESC LIMIT 1
	`, sessionID, userID)
	require.NoError(t, err)
	return row.ConvID, row.MsgID
}
