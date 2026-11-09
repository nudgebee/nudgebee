package api

import (
	"testing"

	"nudgebee/services/common"

	"github.com/stretchr/testify/assert"
)

func TestQueryErrorsMessage(t *testing.T) {
	t.Run("server-side error text never reaches the client", func(t *testing.T) {
		// What a DefGenerator database error looks like once wrapped into the
		// response: the text can carry row values, so it stays server-side.
		errs := []common.Error{common.ErrorInternal(`pq: invalid input syntax for type integer: "$2a$10$secrethash"`)}
		msg, internal := queryErrorsMessage(errs, "req-123")
		assert.True(t, internal)
		assert.Equal(t, "Unable to execute query (request id: req-123)", msg)
		assert.NotContains(t, msg, "secrethash")
	})

	t.Run("no request id", func(t *testing.T) {
		msg, internal := queryErrorsMessage([]common.Error{common.ErrorInternal("boom")}, "")
		assert.True(t, internal)
		assert.Equal(t, "Unable to execute query", msg)
	})

	t.Run("request errors keep their text", func(t *testing.T) {
		msg, internal := queryErrorsMessage([]common.Error{common.ErrorUnauthorized("Access Denied")}, "req-123")
		assert.False(t, internal)
		assert.Contains(t, msg, "Access Denied")
	})

	t.Run("one server-side error masks the whole response", func(t *testing.T) {
		errs := []common.Error{common.ErrorBadRequest("bad filter"), common.ErrorInternal("pq: relation secret")}
		msg, internal := queryErrorsMessage(errs, "r")
		assert.True(t, internal)
		assert.NotContains(t, msg, "secret")
	})
}
