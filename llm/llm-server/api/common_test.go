package api

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"

	"nudgebee/llm/common"
)

func TestServiceErrorStatus(t *testing.T) {
	refused := common.ErrorBadRequest("tool deploy-check is used by organisation agents triage; remove it from them first")
	assert.Equal(t, 400, serviceErrorStatus(refused))
	assert.Equal(t, 400, serviceErrorStatus(fmt.Errorf("agent: transaction failed: %w", refused)))
	assert.Equal(t, 500, serviceErrorStatus(errors.New("pq: connection reset")))
	assert.Equal(t, 404, serviceErrorStatus(common.ErrorNotFound("agent not found")))
	assert.Equal(t, 500, serviceErrorStatus(common.Error{Message: "no code"}))
	assert.Equal(t, 500, serviceErrorStatus(common.Error{Message: "not an error code", Code: 200}))
}

func TestToApiError(t *testing.T) {
	refused := common.ErrorBadRequest("tool deploy-check is used by organisation agents triage; remove it from them first")
	assert.Equal(t, refused, toApiError(fmt.Errorf("agent: transaction failed: %w", refused)))
	assert.Equal(t, common.Error{Message: "pq: connection reset"}, toApiError(errors.New("pq: connection reset")))
}
