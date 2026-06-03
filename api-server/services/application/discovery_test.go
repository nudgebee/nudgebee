package application

import (
	"nudgebee/services/internal/testenv"
	"nudgebee/services/security"
	"os"
	"testing"
)

func TestDiscoverAndUpdateFrameworkAndDashboardAttributes(t *testing.T) {
	testenv.RequireEnv(t, testenv.User, testenv.Tenant, testenv.Account)
	ctxt := security.NewRequestContextForUserTenant(os.Getenv("TEST_USER"), os.Getenv("TEST_TENANT"), nil, nil, nil)

	err := discoverAndUpdateFrameworkAndDashboardAttributes(ctxt, os.Getenv("TEST_USER"), os.Getenv("TEST_ACCOUNT"))

	if err != nil {
		println(err)
		t.Errorf("Test case 1 failed")
	}
}

func TestDiscoverAndUpdateExternalApps(t *testing.T) {
	testenv.RequireEnv(t, testenv.User, testenv.Tenant, testenv.Account)
	ctxt := security.NewRequestContextForUserTenant(os.Getenv("TEST_USER"), os.Getenv("TEST_TENANT"), nil, nil, nil)

	err := discoverAndUpdateExternalApps(ctxt, os.Getenv("TEST_USER"), os.Getenv("TEST_ACCOUNT"))

	if err != nil {
		println(err)
		t.Errorf("Test case 1 failed")
	}
}

func TestDiscoverAndUpdateExternalVMs(t *testing.T) {
	testenv.RequireEnv(t, testenv.User, testenv.Tenant, testenv.Account)
	ctxt := security.NewRequestContextForUserTenant(os.Getenv("TEST_USER"), os.Getenv("TEST_TENANT"), nil, nil, nil)

	err := discoverAndUpdateExternalVMs(ctxt, os.Getenv("TEST_TENANT"), os.Getenv("TEST_ACCOUNT"))

	if err != nil {
		println(err)
		t.Errorf("Test case 1 failed")
	}
}
