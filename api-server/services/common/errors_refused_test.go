package common

import (
	"errors"
	"testing"

	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
)

func TestErrorRefusedOrShowsTheDatabasesReasonForACheckRefusal(t *testing.T) {
	refused := &pq.Error{Code: "23514", Message: "a bee account cannot hold built-in roles or join user groups; give it a custom role"}
	got := ErrorRefusedOr(refused, "Error updating user role")
	assert.Equal(t, 400, got.Code)
	assert.Equal(t, refused.Message, got.Message)

	other := ErrorRefusedOr(errors.New("connection reset"), "Error updating user role")
	assert.Equal(t, 500, other.Code)
	assert.Equal(t, "Error updating user role", other.Message)
}
