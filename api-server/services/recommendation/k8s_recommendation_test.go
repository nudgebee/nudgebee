package recommendation

import (
	"nudgebee/services/internal/database"
	"nudgebee/services/internal/database/models"
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

func TestArchiveRecommendationsForInactiveResources(t *testing.T) {
	testenv.RequireMetastore(t)
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		t.Fatal(err)
	}
	tenant, account, user := testenv.RequireTenant(t)
	ctxt := security.NewRequestContextForUserTenant(user, tenant, nil, nil, nil)

	seedResource := func(name string, isActive bool) string {
		var id string
		err := dbms.Db.Get(&id, `insert into cloud_resourses (name, account, cloud_provider, region, tenant, is_active)
			values ($1, $2, 'K8s', 'none', $3, $4) returning id::varchar`, name, account, tenant, isActive)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	seedRecommendation := func(resourceId string) string {
		var id string
		err := dbms.Db.Get(&id, `insert into recommendation (tenant_id, cloud_account_id, resource_id, category, rule_name, recommendation, recommendation_action, status)
			values ($1, $2, $3, 'K8sSpotRecommendation', 'archive_inactive_resources_test', '{}', 'Modify', $4) returning id::varchar`,
			tenant, account, resourceId, models.RecommendationStatusOpen)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}

	deadResource := seedResource("archive-inactive-test-dead", false)
	liveResource := seedResource("archive-inactive-test-live", true)
	deadRec := seedRecommendation(deadResource)
	liveRec := seedRecommendation(liveResource)
	defer func() {
		_, _ = dbms.Db.Exec(`delete from recommendation where id in ($1, $2)`, deadRec, liveRec)
		_, _ = dbms.Db.Exec(`delete from cloud_resourses where id in ($1, $2)`, deadResource, liveResource)
	}()

	err = archiveRecommendationsForInactiveResources(ctxt, dbms)
	assert.Nil(t, err)

	var status string
	err = dbms.Db.Get(&status, `select status from recommendation where id = $1`, deadRec)
	assert.Nil(t, err)
	assert.Equal(t, string(models.RecommendationStatusArchive), status)

	err = dbms.Db.Get(&status, `select status from recommendation where id = $1`, liveRec)
	assert.Nil(t, err)
	assert.Equal(t, string(models.RecommendationStatusOpen), status)
}

func TestReopenOrphanedInProgressRecommendations(t *testing.T) {
	testenv.RequireMetastore(t)
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		t.Fatal(err)
	}
	tenant, account, user := testenv.RequireTenant(t)
	ctxt := security.NewRequestContextForUserTenant(user, tenant, nil, nil, nil)

	// Registered before the first insert and appended to as rows are created, so a
	// t.Fatal partway through seeding still cleans up what already landed.
	var seeded []string
	defer func() {
		for _, id := range seeded {
			_, _ = dbms.Db.Exec(`delete from recommendation_resolution where recommendation_id = $1`, id)
			_, _ = dbms.Db.Exec(`delete from recommendation where id = $1`, id)
		}
	}()

	seedRecommendation := func(objectId string, status models.RecommendationStatus) string {
		var id string
		err := dbms.Db.Get(&id, `insert into recommendation (tenant_id, cloud_account_id, category, rule_name, account_object_id, recommendation, recommendation_action, status)
			values ($1, $2, 'RightSizing', 'reopen_orphaned_inprogress_test', $3, '{}', 'Modify', $4) returning id::varchar`,
			tenant, account, objectId, status)
		if err != nil {
			t.Fatal(err)
		}
		seeded = append(seeded, id)
		return id
	}
	seedResolution := func(recommendationId string) {
		_, err := dbms.Db.Exec(`insert into recommendation_resolution (recommendation_id, type, status, type_reference_id, resolver_type, resolver_id)
			values ($1, $2, $3, '', $4, $5)`,
			recommendationId, models.RecommendationResolutionTypePullRequest,
			models.RecommendationResolutionStatusInProgress, models.RecommendationResolutionResolverTypeUser, user)
		if err != nil {
			t.Fatal(err)
		}
	}

	orphan := seedRecommendation("reopen-orphan", models.RecommendationStatusInProgress)
	claimed := seedRecommendation("reopen-claimed", models.RecommendationStatusInProgress)
	untouched := seedRecommendation("reopen-open", models.RecommendationStatusOpen)
	seedResolution(claimed)

	err = reopenOrphanedInProgressRecommendations(ctxt, dbms)
	assert.Nil(t, err)

	statusOf := func(id string) string {
		var status string
		err := dbms.Db.Get(&status, `select status from recommendation where id = $1`, id)
		assert.Nil(t, err)
		return status
	}

	// No resolution behind it — nobody is working it, so it returns to Open.
	assert.Equal(t, string(models.RecommendationStatusOpen), statusOf(orphan))
	// A real in-flight resolution must keep its claim.
	assert.Equal(t, string(models.RecommendationStatusInProgress), statusOf(claimed))
	// Rows that were never InProgress are out of scope.
	assert.Equal(t, string(models.RecommendationStatusOpen), statusOf(untouched))
}
