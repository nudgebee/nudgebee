package recommendation

import (
	"nudgebee/services/internal/database"
	"nudgebee/services/security"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRecommendation(t *testing.T) {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		t.Fatal(err)
	}
	ctxt := security.NewRequestContextForUserTenant("af4cb6af-1254-421d-bfa5-ffcfe649017e", "0053b816-4b45-4dcd-a612-19545110f8aa", nil, nil, nil)
	err = processAbandonedRecommendations(ctxt, "0053b816-4b45-4dcd-a612-19545110f8aa", dbms, "")
	if err != nil {
		assert.Nil(t, err)
	}
}

func TestSpotRecommendation(t *testing.T) {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		t.Fatal(err)
	}
	ctxt := security.NewRequestContextForUserTenant("af4cb6af-1254-421d-bfa5-ffcfe649017e", "0053b816-4b45-4dcd-a612-19545110f8aa", nil, nil, nil)
	err = processSpotInstanceRecommendations(ctxt, "0053b816-4b45-4dcd-a612-19545110f8aa", dbms)
	if err != nil {
		assert.Nil(t, err)
	}
}

func TestHealthCheckRecommendation(t *testing.T) {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		t.Fatal(err)
	}
	ctxt := security.NewRequestContextForUserTenant("890cad87-c452-4aa7-b84a-742cee0454a1", "a2a30b02-0f67-42e5-a2ab-c658230fd798", nil, nil, nil)
	err = processHealthCheckRecommendations(ctxt, "a2a30b02-0f67-42e5-a2ab-c658230fd798", dbms)
	if err != nil {
		assert.Nil(t, err)
	}
}
