package tenant

import (
	"log/slog"
	"nudgebee/services/internal/testenv"
	"nudgebee/services/security"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDeleteTenant(t *testing.T) {
	testenv.RequireMetastore(t)
	m := testenv.RequireEnv(t, "TEST_DELETE_TENANT_ID")
	_, err := DeleteTenant(security.NewRequestContextForSuperAdmin(slog.Default(), nil, nil), TenantDeleteRequest{
		Id: m["TEST_DELETE_TENANT_ID"],
	})
	assert.Nil(t, err)
}
