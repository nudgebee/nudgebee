package crawl

import (
	"nudgebee/services/security"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCivoResourceMeta(t *testing.T) {
	ctxt := security.NewRequestContextForUserTenant("af4cb6af-1254-421d-bfa5-ffcfe649017e", "0053b816-4b45-4dcd-a612-19545110f8aa", nil, nil, nil)
	err := CivoResourceMeta(ctxt)
	assert.Nil(t, err)
}
