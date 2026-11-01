package api

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The wire contract for the memory overrides: what a caller actually posts to
// /v1/completions/chat, decoded by the same struct the handler binds to.
//
// The conversation-level tests start below this boundary, so without this a
// rename or a wrong json tag would leave the switches silently inert for every
// real caller while every other test passed.
func TestConversationApiRequest_MemoryOverridesFromWire(t *testing.T) {
	body := []byte(`{
	  "query": "why is checkout slow",
	  "account_id": "a2a30b02-0f67-42e5-a2ab-c658230fd798",
	  "user_id": "f7147b1a-2b03-454e-b6cd-964e6fc9b369",
	  "session_id": "s1",
	  "config": { "memory": { "write": false, "rerank": false } }
	}`)

	var req ConversationApiRequest
	require.NoError(t, json.Unmarshal(body, &req))

	require.NotNil(t, req.Config.Memory, "the memory block must survive into Config")
	assert.Nil(t, req.Config.Memory.Read, "an absent switch stays unset")
	require.NotNil(t, req.Config.Memory.Write)
	assert.False(t, *req.Config.Memory.Write)

	// And the resolvers the gates call must agree, end of the chain.
	assert.True(t, req.Config.MemoryReadAllowed(), "reading stays on")
	assert.False(t, req.Config.MemoryWriteAllowed())
	assert.False(t, req.Config.MemoryRerankAllowed())
}

// A body with no memory block must leave every switch deferring to the
// deployment — the overwhelmingly common request.
func TestConversationApiRequest_NoMemoryBlockChangesNothing(t *testing.T) {
	var req ConversationApiRequest
	require.NoError(t, json.Unmarshal([]byte(`{"query":"q","config":{}}`), &req))
	assert.Nil(t, req.Config.Memory)
	assert.True(t, req.Config.MemoryReadAllowed())
	assert.True(t, req.Config.MemoryWriteAllowed())
	assert.True(t, req.Config.MemoryRerankAllowed())
}

// The field is "config", not "query_config" — a caller using the wrong key gets
// no override rather than an error, so this pins the name.
func TestConversationApiRequest_WrongKeyIsIgnored(t *testing.T) {
	var req ConversationApiRequest
	require.NoError(t, json.Unmarshal(
		[]byte(`{"query":"q","query_config":{"memory":{"write":false}}}`), &req))
	assert.Nil(t, req.Config.Memory, "query_config is not the wire field; config is")
}
