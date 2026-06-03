package recommendation

import (
	"nudgebee/services/internal/database"
	"nudgebee/services/internal/testenv"
	"nudgebee/services/security"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRecommendation(t *testing.T) {
	testenv.RequireMetastore(t)
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		t.Fatal(err)
	}
	tenant, account, user := testenv.RequireTenant(t)
	ctxt := security.NewRequestContextForUserTenant(user, tenant, nil, nil, nil)
	err = processAbandonedRecommendations(ctxt, account, dbms)
	if err != nil {
		assert.Nil(t, err)
	}
}

func TestSpotRecommendation(t *testing.T) {
	testenv.RequireMetastore(t)
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		t.Fatal(err)
	}
	tenant, account, user := testenv.RequireTenant(t)
	ctxt := security.NewRequestContextForUserTenant(user, tenant, nil, nil, nil)
	err = processSpotInstanceRecommendations(ctxt, account, dbms)
	if err != nil {
		assert.Nil(t, err)
	}
}

func TestHealthCheckRecommendation(t *testing.T) {
	testenv.RequireMetastore(t)
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		t.Fatal(err)
	}
	tenant, account, user := testenv.RequireTenant(t)
	ctxt := security.NewRequestContextForUserTenant(user, tenant, nil, nil, nil)
	err = processHealthCheckRecommendations(ctxt, account, dbms)
	if err != nil {
		assert.Nil(t, err)
	}
}
