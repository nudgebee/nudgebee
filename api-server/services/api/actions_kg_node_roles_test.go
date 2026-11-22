package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"nudgebee/services/knowledge_graph/core"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNodeRoleWriteStatus(t *testing.T) {
	cases := map[error]int{
		core.ErrNodeRoleNotFound:                                404,
		core.ErrNodeRoleForbidden:                               403,
		core.ErrNodeRoleNotTarget:                               400,
		fmt.Errorf("%w: unknown role", core.ErrNodeRoleInvalid): 400,
		fmt.Errorf("lock nodes: connection reset"):              500,
	}
	for err, want := range cases {
		assert.Equal(t, want, nodeRoleWriteStatus(err), err.Error())
	}
}

// The UI offers exactly what the server accepts.
func TestHandleKgListNodeRoles(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/rpc/kg", nil)
	handleKgListNodeRoles(c)
	require.Equal(t, 200, rec.Code)

	var body struct {
		Data struct {
			Roles           []core.NodeRoleDef `json:"roles"`
			TargetNodeTypes []string           `json:"target_node_types"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, core.NodeRoles, body.Data.Roles)
	assert.Contains(t, body.Data.TargetNodeTypes, "Workload")
	assert.Contains(t, body.Data.TargetNodeTypes, "ComputeInstance")
	assert.NotContains(t, body.Data.TargetNodeTypes, "Namespace")
}

func TestHandleKgWriteNodeRole_RejectsMalformedRequests(t *testing.T) {
	for name, input := range map[string]map[string]any{
		"no request object": {},
		"no node id":        {"request": map[string]any{"role": "cache"}},
	} {
		gin.SetMode(gin.TestMode)
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodPost, "/rpc/kg", nil)
		handleKgWriteNodeRole(&ActionRequest{Action: ActionRequestAction{Name: "kg_upsert_node_role"}, Input: input}, c, nil, nil, nil, true)
		assert.Equal(t, 400, rec.Code, name)
	}
}
