package crawl

import (
	"nudgebee/services/security"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGCPResourceMeta(t *testing.T) {
	ctxt := security.NewRequestContextForUserTenant("b1940352-01d1-423d-80b9-0ef52f3293a3", "0d511f65-e0c4-430d-a919-4cc77460c1bd", nil, nil, nil)
	err := GCPResourceMeta(ctxt)
	assert.Nil(t, err)
}
