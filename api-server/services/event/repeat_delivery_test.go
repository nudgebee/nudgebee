package event

import (
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A repeat delivery must reach the existing-row UPDATE with the same subject the
// insert would have stored, not the raw one it arrived with. These assert the
// arguments of that UPDATE; repeat_delivery_db_test.go asserts what the statement
// then does with them.

// The delivery names the node-exporter pod and resolves that pod's resource. The
// UPDATE carries the node instead, with the pod's namespace and owner cleared
// and the node's resource in place of the pod's — exactly what the insert stores.
func TestInvestigateEvent_RepeatCarriesTheNodeSubject(t *testing.T) {
	mock, _ := setupResolveTest(t)
	e := automationEvent(EventStatusFiring, "node-cpu-1", "")
	e.Source = "prometheus"
	e.AggregationKey = "NodeCPUHighUsage"
	e.SubjectType = "pod"
	e.SubjectName = "victoria-prometheus-node-exporter-skqnn"
	e.SubjectNamespace = "monitoring"
	e.SubjectOwner = "victoria-prometheus-node-exporter"
	e.SubjectOwnerKind = "DaemonSet"
	e.Labels = map[string]string{"alertname": "NodeCPUHighUsage", "job": "node-exporter", "node": "gke-pool-1-node-7f3a"}

	mock.ExpectQuery(regexp.QuoteMeta(workloadCloudResourceSQL)).
		WithArgs(resolveTestTenant, resolveTestAccount, "monitoring", "victoria-prometheus-node-exporter").
		WillReturnRows(sqlmock.NewRows([]string{"cloud_resource_id"}).AddRow("aaaaaaaa-0000-4000-8000-000000000001"))
	mock.ExpectQuery(findingLookupSQL).WillReturnRows(existingRow("prometheus"))
	mock.ExpectQuery(regexp.QuoteMeta(nodeCloudResourceSQL)).
		WithArgs(resolveTestTenant, resolveTestAccount, "gke-pool-1-node-7f3a").
		WillReturnRows(sqlmock.NewRows([]string{"cloud_resource_id"}).AddRow("cccccccc-0000-4000-8000-000000000003"))
	mock.ExpectExec(labelUpdateSQL).
		WithArgs(sqlmock.AnyArg(), resolveTestEventID, sqlmock.AnyArg(), "HIGH", "gke-pool-1-node-7f3a", "", "node", "cccccccc-0000-4000-8000-000000000003", "", "").
		WillReturnResult(sqlmock.NewResult(0, 1))

	id, err := InvestigateEvent(resolveTestContext(), e, "")
	require.NoError(t, err)
	assert.Equal(t, resolveTestEventID, id)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// The delivery has no subject fields of its own, only labels. The UPDATE carries
// the subject those labels name, as the insert does.
func TestInvestigateEvent_RepeatCarriesTheSubjectFromItsLabels(t *testing.T) {
	mock, _ := setupResolveTest(t)
	e := automationEvent(EventStatusFiring, "error-rate-1", "")
	e.SubjectName = ""
	e.SubjectOwner = "api"
	e.Labels = map[string]string{"pod": "api-7", "namespace": "payments"}

	mock.ExpectQuery(regexp.QuoteMeta(workloadCloudResourceSQL)).
		WithArgs(resolveTestTenant, resolveTestAccount, "payments", "api").
		WillReturnRows(sqlmock.NewRows([]string{"cloud_resource_id"}))
	mock.ExpectQuery(findingLookupSQL).WillReturnRows(existingRow("automation"))
	mock.ExpectExec(labelUpdateSQL).
		WithArgs(sqlmock.AnyArg(), resolveTestEventID, sqlmock.AnyArg(), "HIGH", "api-7", "payments", "", nil, "api", "").
		WillReturnResult(sqlmock.NewResult(0, 1))

	id, err := InvestigateEvent(resolveTestContext(), e, "")
	require.NoError(t, err)
	assert.Equal(t, resolveTestEventID, id)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// The node correction resolved on the first delivery and stored the node. A later
// repeat can miss it — the exporter pod was rescheduled and its row is gone — and
// then still names the exporter pod. That must not move the event back onto the
// pod: the UPDATE carries no subject, so the stored node stands.
func TestInvestigateEvent_RepeatThatCannotResolveTheNodeKeepsTheStoredSubject(t *testing.T) {
	mock, _ := setupResolveTest(t)
	e := automationEvent(EventStatusFiring, "node-cpu-1", "")
	e.Source = "prometheus"
	e.AggregationKey = "NodeCPUHighUsage"
	e.SubjectType = "pod"
	e.SubjectName = "victoria-prometheus-node-exporter-skqnn"
	e.SubjectNamespace = "monitoring"
	e.SubjectOwner = "victoria-prometheus-node-exporter"
	e.SubjectOwnerKind = "DaemonSet"
	e.Labels = map[string]string{"alertname": "NodeCPUHighUsage", "job": "node-exporter"}

	mock.ExpectQuery(regexp.QuoteMeta(workloadCloudResourceSQL)).
		WithArgs(resolveTestTenant, resolveTestAccount, "monitoring", "victoria-prometheus-node-exporter").
		WillReturnRows(sqlmock.NewRows([]string{"cloud_resource_id"}).AddRow("aaaaaaaa-0000-4000-8000-000000000001"))
	mock.ExpectQuery(findingLookupSQL).WillReturnRows(
		sqlmock.NewRows([]string{"id", "subject_name", "source"}).AddRow(resolveTestEventID, "gke-pool-1-node-7f3a", "prometheus"))
	// The exporter pod is no longer known, so the node cannot be worked out again.
	mock.ExpectQuery(`FROM k8s_pods`).WillReturnRows(sqlmock.NewRows([]string{"node_name"}))
	mock.ExpectExec(labelUpdateSQL).
		WithArgs(sqlmock.AnyArg(), resolveTestEventID, sqlmock.AnyArg(), "HIGH", "", "", "", nil, "", "").
		WillReturnResult(sqlmock.NewResult(0, 1))

	id, err := InvestigateEvent(resolveTestContext(), e, "")
	require.NoError(t, err)
	assert.Equal(t, resolveTestEventID, id)
	assert.NoError(t, mock.ExpectationsWereMet())
}
