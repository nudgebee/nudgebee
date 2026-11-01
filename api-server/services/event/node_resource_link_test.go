package event

import (
	"context"
	"database/sql"
	"log/slog"
	"nudgebee/services/internal/database"
	"nudgebee/services/security"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const nodeName = "ip-192-0-2-10.ec2.internal"

// A node alert is linked to the node even when its labels carry a namespace:
// prometheus stamps the exporter's namespace on every series it scrapes. The
// node's name is never sent to the workload match (sqlmock fails on a query it
// was not told about).
func TestLinkK8sCloudResourceId_NodeAlertWithExporterNamespace(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectQuery(`FROM k8s_nodes`).
		WithArgs(cmTenant, cmAccount, nodeName).
		WillReturnRows(sqlmock.NewRows([]string{"cloud_resource_id"}).AddRow(cmCrid))

	ev := &Event{
		AccountId:   cmAccount,
		Tenant:      cmTenant,
		Source:      "prometheus",
		SubjectType: "node",
		SubjectName: nodeName,
		Labels:      map[string]string{"namespace": "monitoring", "instance": nodeName},
	}
	linkK8sCloudResourceId(cmTestCtx(), &database.DatabaseManager{Db: sqlx.NewDb(db, "postgres")}, cmTenant, ev)

	require.NoError(t, mock.ExpectationsWereMet())
	assert.Equal(t, cmCrid, ev.CloudResourceId)
}

// An automation (Store Event) node alert has no namespace at all, which used to
// end the lookup before anything was tried. subject_type casing is the
// sender's, so it is matched case-insensitively.
func TestLinkK8sCloudResourceId_NodeAlertWithoutNamespace(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectQuery(`FROM k8s_nodes`).
		WithArgs(cmTenant, cmAccount, nodeName).
		WillReturnRows(sqlmock.NewRows([]string{"cloud_resource_id"}).AddRow(cmCrid))

	ev := &Event{
		AccountId:   cmAccount,
		Tenant:      cmTenant,
		Source:      "automation",
		SubjectType: "Node",
		SubjectName: nodeName,
	}
	linkK8sCloudResourceId(cmTestCtx(), &database.DatabaseManager{Db: sqlx.NewDb(db, "postgres")}, cmTenant, ev)

	require.NoError(t, mock.ExpectationsWereMet())
	assert.Equal(t, cmCrid, ev.CloudResourceId)
}

// A node the account does not list stays unlinked. Even with a namespace on the
// event it is not name-matched against workloads: a workload that happens to
// share the node's name is a different resource.
func TestLinkK8sCloudResourceId_UnknownNodeStaysUnlinked(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectQuery(`FROM k8s_nodes`).
		WithArgs(cmTenant, cmAccount, "search-master-01").
		WillReturnError(sql.ErrNoRows)
	// A same-named workload exists. The lookup that would find it must never
	// run, so this expectation has to be left over at the end.
	mock.ExpectQuery(`FROM k8s_workloads`).
		WithArgs(cmTenant, cmAccount, "search", "search-master-01").
		WillReturnRows(sqlmock.NewRows([]string{"cloud_resource_id"}).AddRow(cmCrid))

	ev := &Event{
		AccountId:        cmAccount,
		Tenant:           cmTenant,
		Source:           "automation",
		SubjectType:      "node",
		SubjectName:      "search-master-01",
		SubjectNamespace: "search",
	}
	linkK8sCloudResourceId(cmTestCtx(), &database.DatabaseManager{Db: sqlx.NewDb(db, "postgres")}, cmTenant, ev)

	assert.Empty(t, ev.CloudResourceId)
	assert.ErrorContains(t, mock.ExpectationsWereMet(), "k8s_workloads", "the workload lookup must not run for a node")
}

// A prometheus node alert arrives naming the node-exporter pod and has already
// been linked to that pod's resource when the subject is corrected to the node.
// The correction replaces the link with the node's, instead of only dropping it.
func TestApplyNodeSubjectCorrection_LinksTheNode(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectQuery(`FROM k8s_nodes`).
		WithArgs(cmTenant, cmAccount, nodeName).
		WillReturnRows(sqlmock.NewRows([]string{"cloud_resource_id"}).AddRow(cmCrid))

	ev := &Event{
		AccountId:        cmAccount,
		Tenant:           cmTenant,
		Source:           "prometheus",
		AggregationKey:   "NodeSystemSaturation",
		SubjectType:      "pod",
		SubjectName:      "prometheus-node-exporter-x7k2p",
		SubjectNamespace: "monitoring",
		SubjectOwner:     "prometheus-node-exporter",
		CloudResourceId:  "44444444-4444-4444-4444-444444444444", // the exporter's
		Labels: map[string]string{
			"job":       "node-exporter",
			"namespace": "monitoring",
			"pod":       "prometheus-node-exporter-x7k2p",
			"node":      nodeName,
		},
	}
	// cmTestCtx has no security context without a database; the correction
	// reads the tenant from it.
	sc := security.NewRequestContext(context.Background(),
		security.NewSecurityContextForAuditRelay(cmTenant, ""), slog.Default(), nil, nil)
	applyNodeSubjectCorrection(sc, &database.DatabaseManager{Db: sqlx.NewDb(db, "postgres")}, cmTenant, ev)

	require.NoError(t, mock.ExpectationsWereMet())
	assert.Equal(t, "node", ev.SubjectType)
	assert.Equal(t, nodeName, ev.SubjectName)
	assert.Equal(t, cmCrid, ev.CloudResourceId)
}
