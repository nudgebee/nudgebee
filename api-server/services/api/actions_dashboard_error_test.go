package api

import (
	"errors"
	"fmt"
	"net/http/httptest"
	"testing"

	"nudgebee/services/dashboard"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

// The editor tells "someone saved over you" apart from every other failure by
// the 409 alone, so the mapping is the contract.
func TestDashboardErrorStatus(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cases := map[string]struct {
		err  error
		want int
	}{
		"conflict":         {dashboard.ErrConflict, 409},
		"wrapped conflict": {fmt.Errorf("save: %w", dashboard.ErrConflict), 409},
		"not found":        {dashboard.ErrNotFound, 404},
		"forbidden":        {dashboard.ErrForbidden, 403},
		"anything else":    {errors.New("panel 0: title is required"), 400},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			dashboardError(c, tc.err)
			assert.Equal(t, tc.want, rec.Code)
		})
	}
}
