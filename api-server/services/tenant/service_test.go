package tenant

import (
	"log/slog"
	"nudgebee/services/security"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDeleteTenant(t *testing.T) {
	_, err := DeleteTenant(security.NewRequestContextForSuperAdmin(slog.Default(), nil, nil), TenantDeleteRequest{
		Id: "6a89a0c5-9313-4b0a-a244-e7011673f520",
	})
	assert.Nil(t, err)
}
