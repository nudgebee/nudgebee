package user

import (
	"log/slog"
	"nudgebee/services/security"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCreateUserAuthToken(t *testing.T) {
	sc := security.NewRequestContextForUserTenant(os.Getenv("TEST_USER"), os.Getenv("TEST_TENANT"), slog.Default(), nil, nil)
	request, err := CreateUserAuthToken(sc, UserTokenCreateRequest{
		Name: "test",
	})
	assert.Nil(t, err)
	assert.NotNil(t, request)
	assert.NotEmpty(t, request.Token)
	assert.NotEmpty(t, request.Name)
}
