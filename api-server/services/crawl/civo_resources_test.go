package crawl

import (
	"nudgebee/services/internal/testenv"
	"nudgebee/services/security"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCivoResourceMeta(t *testing.T) {
	testenv.RequireMetastore(t)
	tenant, _, user := testenv.RequireTenant(t)
	ctxt := security.NewRequestContextForUserTenant(user, tenant, nil, nil, nil)
	err := CivoResourceMeta(ctxt)
	assert.Nil(t, err)
}
