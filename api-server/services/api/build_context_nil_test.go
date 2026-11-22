package api

import (
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

// When the tenant's accounts cannot be loaded the tenant-admin security context
// is nil; building the request context must fail with an error, not hand the
// handler a context with no identity (where the first check would panic).
func TestBuildContextFailsWhenSecurityContextUnavailable(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mock := mockMetastore(t)
	mock.ExpectQuery("SELECT id FROM cloud_accounts WHERE tenant").
		WithArgs(accessTestTenant).
		WillReturnError(errors.New("connection refused"))

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/rpc/test", nil)
	c.Request.Header.Set("x-tenant-id", accessTestTenant)

	var ctx any
	assert.NotPanics(t, func() {
		rc, err := buildContextFromPayload(c, &ActionRequest{Action: ActionRequestAction{Name: "cloud_resources"}}, nil, nil, slog.Default())
		ctx = rc
		assert.EqualError(t, err, "security context unavailable")
	})
	assert.Nil(t, ctx)
}
