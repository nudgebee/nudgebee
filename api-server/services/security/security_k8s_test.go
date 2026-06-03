package security

import (
	"database/sql"
	"database/sql/driver"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"nudgebee/services/config"
	"nudgebee/services/internal/database"
	"nudgebee/services/internal/testenv"
	"os"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
)

func mockRelayServer() (*httptest.Server, error) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			_, _ = fmt.Fprintln(w, `{"message": "error"}`)
			w.WriteHeader(http.StatusInternalServerError)
		}
		stringBody := string(body)
		if strings.Contains(stringBody, "roles,rolebindings") {
			roleBindingData, err := os.ReadFile("k8s_rolebindings.json")
			if err != nil {
				_, _ = fmt.Fprintln(w, `{"message": "error"}`)
				w.WriteHeader(http.StatusInternalServerError)
			}
			_, err = w.Write(roleBindingData)
			if err != nil {
				_, _ = fmt.Fprintln(w, `{"message": "error"}`)
				w.WriteHeader(http.StatusInternalServerError)
			}
		} else if strings.Contains(stringBody, "clusterroles,clusterrolebindings") {
			roleBindingData, err := os.ReadFile("k8s_clusterrolebindings.json")
			if err != nil {
				_, _ = fmt.Fprintln(w, `{"message": "error"}`)
				w.WriteHeader(http.StatusInternalServerError)
			}
			_, err = w.Write(roleBindingData)
			if err != nil {
				_, _ = fmt.Fprintln(w, `{"message": "error"}`)
				w.WriteHeader(http.StatusInternalServerError)
			}
		} else {
			_, _ = fmt.Fprintln(w, `{"message": "error"}`)
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	config.Config.RelayServerEndpoint = ts.URL
	return ts, nil
}

func mockPg() (*sql.DB, sqlmock.Sqlmock, error) {
	db, mock, err := sqlmock.New()
	if err != nil {
		return nil, nil, err
	}
	database.RegisterDatabaseManagerHook(database.Metastore, func() (*database.DatabaseManager, error) {
		return &database.DatabaseManager{
			Db: sqlx.NewDb(db, "postgresql"),
		}, nil
	})
	return db, mock, nil
}

func TestK8sVarifyPermission(t *testing.T) {
	ts, err := mockRelayServer()
	if err != nil {
		t.Error(err)
	}
	defer ts.Close()

	permsToCheck := [][]any{}
	permsToCheck = append(permsToCheck, []any{"random", K8sRbacSubjectTypeGroup, "system:masters", "namespaces", "nudgebee", K8sRbacPermissionTypeGet})
	permsToCheck = append(permsToCheck, []any{"random", K8sRbacSubjectTypeServiceAccount, "actions-runner-system-1/actions-runner-controller", "configmaps", "actions-runner-system-1/actions-runner-controller-leader-election", K8sRbacPermissionTypeGet})
	//permsToCheck = append(permsToCheck, []any{"random", K8sRbacSubjectTypeUser, "random", "configmaps", "actions-runner-system-1/actions-runner-controller-leader-election", K8sRbacPermissionTypeGet})
	for _, param := range permsToCheck {
		resp, err := k8sVarifyPermission(NewSecurityContextForSuperAdmin(), param[0].(string), param[1].(K8sRbacSubjectType), param[2].(string), param[3].(string), param[4].(string), param[5].(K8sRbacPermissionType))
		assert.Nil(t, err)
		assert.True(t, resp)
	}
}

func TestK8sVarifyPermissionForGroupUser(t *testing.T) {
	ts, err := mockRelayServer()
	if err != nil {
		t.Error(err)
	}
	defer ts.Close()

	permsToCheck := [][]any{}
	permsToCheck = append(permsToCheck, []any{"random", K8sRbacSubjectTypeGroup, "system:masters", "namespaces", "nudgebee", K8sRbacPermissionTypeGet})
	permsToCheck = append(permsToCheck, []any{"random", K8sRbacSubjectTypeServiceAccount, "actions-runner-system-1/actions-runner-controller", "configmaps", "actions-runner-system-1/actions-runner-controller-leader-election", K8sRbacPermissionTypeGet})
	for _, param := range permsToCheck {
		resp, err := k8sVarifyPermission(NewSecurityContextForSuperAdmin(), param[0].(string), param[1].(K8sRbacSubjectType), param[2].(string), param[3].(string), param[4].(string), param[5].(K8sRbacPermissionType))
		assert.Nil(t, err)
		assert.True(t, resp)
	}
}

func TestK8sListObjects(t *testing.T) {
	ts, err := mockRelayServer()
	if err != nil {
		t.Error(err)
	}
	defer ts.Close()

	db, mock, err := mockPg()
	if err != nil {
		t.Fatalf("an error '%s' was not expected when opening a stub database connection", err)
	}
	defer func() {
		err := db.Close()
		if err != nil {
			t.Error(err)
		}
	}()

	mock.ExpectQuery("select distinct name from k8s_namespaces where cloud_account_id .*").WithArgs(driver.Value("random")).WillReturnRows(sqlmock.NewRows([]string{"name"}).AddRow("nudgebee-dev"))
	mock.ExpectClose()
	resp, err := k8sListResourceNames(NewSecurityContextForSuperAdmin(), "random", K8sRbacSubjectTypeGroup, "system:masters", "namespaces", K8sRbacPermissionTypeGet)
	assert.Nil(t, err)
	assert.NotNil(t, resp)
	assert.Contains(t, resp, "nudgebee-dev")
}

func TestK8sListObjects2(t *testing.T) {
	testenv.RequireMetastore(t)
	m := testenv.RequireEnv(t, "TEST_ACCOUNT", "TEST_K8S_CLUSTER")
	// ts, err := mockRelayServer()
	// if err != nil {
	// 	t.Error(err)
	// }
	// defer ts.Close()

	// db, mock, err := mockPg()
	// if err != nil {
	// 	t.Fatalf("an error '%s' was not expected when opening a stub database connection", err)
	// }
	// defer db.Close()

	// mock.ExpectQuery("select distinct name from k8s_namespaces where cloud_account_id .*").WithArgs(driver.Value("random")).WillReturnRows(sqlmock.NewRows([]string{"name"}).AddRow("nudgebee-dev"))

	resp, err := k8sListResourceNames(NewSecurityContextForSuperAdmin(), m["TEST_ACCOUNT"], K8sRbacSubjectTypeUser, m["TEST_K8S_CLUSTER"], "pods", K8sRbacPermissionTypeGet)
	assert.Nil(t, err)
	assert.NotNil(t, resp)
	assert.Contains(t, resp, "nudgebee/app-dev")
}
