package event

import (
	"testing"

	"nudgebee/services/common"
	"nudgebee/services/internal/database"

	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A repeat delivery of an alert that is already open (an Alertmanager repeat, a
// Datadog re-notification) lands on InvestigateEvent's existing-row branch. It
// used to write the delivery's raw subject, resource link and labels over the
// stored ones, undoing what the first delivery's insert and its enrichment had
// worked out. These tests seed the row as the first delivery left it and send the
// repeat through InvestigateEvent against a real Postgres: what survives is
// decided by the statement, over a uuid column, a NOT NULL one and jsonb.

const (
	repeatExporterPod      = "victoria-prometheus-node-exporter-skqnn"
	repeatNode             = "gke-pool-1-node-7f3a"
	repeatExporterResource = "aaaaaaaa-0000-4000-8000-000000000001"
	repeatLinkedResource   = "bbbbbbbb-0000-4000-8000-000000000002"
	repeatNodeResource     = "cccccccc-0000-4000-8000-000000000003"
)

// repeatRow is the part of an event a repeat delivery writes. An empty
// CloudResourceId or Labels stands for NULL.
type repeatRow struct {
	Labels           string `db:"labels"`
	Priority         string `db:"priority"`
	SubjectType      string `db:"subject_type"`
	SubjectName      string `db:"subject_name"`
	SubjectNamespace string `db:"subject_namespace"`
	SubjectOwner     string `db:"subject_owner"`
	SubjectOwnerKind string `db:"subject_owner_kind"`
	CloudResourceId  string `db:"cloud_resource_id"`
}

// repeatDeliveryTestDB adds, to the enrichment fixture, the columns a repeat
// delivery writes and the k8s state its lookups read: the node-exporter pod, the
// node it runs on, and the DaemonSet that owns it.
func repeatDeliveryTestDB(t *testing.T) *sqlx.DB {
	t.Helper()
	db := enrichmentTestDB(t)

	_, err := db.Exec(`
		ALTER TABLE events
			ADD COLUMN tenant uuid, ADD COLUMN cloud_account_id uuid, ADD COLUMN finding_id text, ADD COLUMN source text,
			ADD COLUMN priority text NOT NULL DEFAULT 'LOW', ADD COLUMN subject_type text,
			ADD COLUMN subject_owner text, ADD COLUMN subject_owner_kind text;
		CREATE TABLE cloud_accounts (id uuid PRIMARY KEY, status text);
		CREATE TABLE k8s_workloads (tenant_id uuid, cloud_account_id uuid, namespace text, name text, kind text,
			cloud_resource_id uuid, is_active boolean, last_seen timestamp);
		CREATE TABLE k8s_pods (tenant_id uuid, cloud_account_id uuid, namespace text, name text, workload_name text,
			workload_type text, node_name text, cloud_resource_id uuid, is_active boolean, last_seen timestamp);
		CREATE TABLE k8s_nodes (tenant_id uuid, cloud_account_id uuid, name text, cloud_resource_id uuid,
			is_active boolean, updated_at timestamp);
		INSERT INTO cloud_accounts VALUES ('` + resolveTestAccount + `', 'active');
		INSERT INTO k8s_workloads VALUES ('` + resolveTestTenant + `', '` + resolveTestAccount + `', 'monitoring',
			'victoria-prometheus-node-exporter', 'DaemonSet', '` + repeatExporterResource + `', true, now());
		INSERT INTO k8s_pods VALUES ('` + resolveTestTenant + `', '` + resolveTestAccount + `', 'monitoring',
			'` + repeatExporterPod + `', 'victoria-prometheus-node-exporter', 'DaemonSet', '` + repeatNode + `', NULL, true, now());
		INSERT INTO k8s_nodes VALUES ('` + resolveTestTenant + `', '` + resolveTestAccount + `', '` + repeatNode + `',
			'` + repeatNodeResource + `', true, now());`)
	require.NoError(t, err)

	database.RegisterDatabaseManagerHook(database.Metastore, func() (*database.DatabaseManager, error) {
		return &database.DatabaseManager{Db: db}, nil
	})
	clearAccountStatusCache(t)
	require.NoError(t, common.CacheClear(podOwnerCacheNamespace))
	t.Cleanup(func() { _ = common.CacheClear(podOwnerCacheNamespace) })
	return db
}

func seedRepeatEvent(t *testing.T, db *sqlx.DB, findingId string, row repeatRow) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO events (id, tenant, cloud_account_id, finding_id, source, cluster, labels, priority,
			subject_type, subject_name, subject_namespace, subject_owner, subject_owner_kind, cloud_resource_id)
		VALUES (gen_random_uuid(), $1, $2, $3, 'prometheus', '', NULLIF($4, '')::jsonb, $5, $6, $7, $8, $9, $10, NULLIF($11, '')::uuid)`,
		resolveTestTenant, resolveTestAccount, findingId, row.Labels, row.Priority,
		row.SubjectType, row.SubjectName, row.SubjectNamespace, row.SubjectOwner, row.SubjectOwnerKind, row.CloudResourceId)
	require.NoError(t, err)
}

func fetchRepeatRow(t *testing.T, db *sqlx.DB, findingId string) repeatRow {
	t.Helper()
	var row repeatRow
	require.NoError(t, db.Get(&row, `SELECT coalesce(labels::text, '') AS labels, priority, subject_type, subject_name,
			subject_namespace, subject_owner, subject_owner_kind, coalesce(cloud_resource_id::text, '') AS cloud_resource_id
		FROM events WHERE finding_id = $1`, findingId))
	return row
}

// assertRepeatRow compares labels as JSON, since jsonb does not keep the text it
// was given, and everything else exactly.
func assertRepeatRow(t *testing.T, want, got repeatRow) {
	t.Helper()
	assert.JSONEq(t, want.Labels, got.Labels)
	got.Labels = want.Labels
	assert.Equal(t, want, got)
}

// repeatEvent is a FIRING delivery for findingId with no subject of its own.
func repeatEvent(findingId string, labels map[string]string) Event {
	return Event{
		Tenant:         resolveTestTenant,
		AccountId:      resolveTestAccount,
		FindingId:      findingId,
		Source:         "prometheus",
		Status:         EventStatusFiring,
		AggregationKey: labels["alertname"],
		Priority:       EventPriorityHigh,
		Labels:         labels,
	}
}

func deliverRepeat(t *testing.T, e Event) {
	t.Helper()
	_, err := InvestigateEvent(resolveTestContext(), e, "")
	require.NoError(t, err)
}

// nodeAlertRepeat is a node alert as Prometheus sends it: the series comes from
// node-exporter, so the alert names the exporter pod and its namespace.
func nodeAlertRepeat(findingId string) Event {
	e := repeatEvent(findingId, map[string]string{
		"alertname": "NodeCPUHighUsage", "job": "node-exporter", "instance": "10.0.0.5:9100",
		"pod": repeatExporterPod, "namespace": "monitoring",
	})
	e.SubjectType = "pod"
	e.SubjectName = repeatExporterPod
	e.SubjectNamespace = "monitoring"
	return e
}

// What the first delivery of nodeAlertRepeat stores: the node and its resource,
// with the namespace and owner that described the exporter pod cleared.
var nodeSubjectRow = repeatRow{
	Labels:          `{"alertname":"NodeCPUHighUsage","job":"node-exporter","instance":"10.0.0.5:9100","pod":"` + repeatExporterPod + `","namespace":"monitoring"}`,
	Priority:        "HIGH",
	SubjectType:     "node",
	SubjectName:     repeatNode,
	CloudResourceId: repeatNodeResource,
}

// The insert repoints a node alert from the node-exporter pod to the node. The
// repeat arrives naming the exporter pod again, and resolves that pod's owner and
// resource; none of it may be written back.
func TestRepeatDelivery_KeepsTheNodeSubject_DB(t *testing.T) {
	db := repeatDeliveryTestDB(t)
	seedRepeatEvent(t, db, "node-cpu-1", nodeSubjectRow)

	deliverRepeat(t, nodeAlertRepeat("node-cpu-1"))

	assertRepeatRow(t, nodeSubjectRow, fetchRepeatRow(t, db, "node-cpu-1"))
}

// The node was worked out on the first delivery. By the time of this repeat the
// exporter pod has been rescheduled and its row is gone, so the node cannot be
// worked out again and the delivery still names the pod. The stored node stands.
func TestRepeatDelivery_KeepsTheNodeWhenItCannotBeResolvedAgain_DB(t *testing.T) {
	db := repeatDeliveryTestDB(t)
	seedRepeatEvent(t, db, "node-cpu-3", nodeSubjectRow)
	_, err := db.Exec(`DELETE FROM k8s_pods`)
	require.NoError(t, err)

	deliverRepeat(t, nodeAlertRepeat("node-cpu-3"))

	assertRepeatRow(t, nodeSubjectRow, fetchRepeatRow(t, db, "node-cpu-3"))
}

// An event an earlier repeat already pointed back at the exporter pod is
// repaired whole: the namespace, owner and resource of the pod go with its name,
// rather than staying behind on a row that now names the node.
func TestRepeatDelivery_RepointsAnEventAnEarlierRepeatOverwrote_DB(t *testing.T) {
	db := repeatDeliveryTestDB(t)
	overwritten := nodeSubjectRow
	overwritten.SubjectType = "pod"
	overwritten.SubjectName = repeatExporterPod
	overwritten.SubjectNamespace = "monitoring"
	overwritten.SubjectOwner = "victoria-prometheus-node-exporter"
	overwritten.SubjectOwnerKind = "DaemonSet"
	overwritten.CloudResourceId = repeatExporterResource
	seedRepeatEvent(t, db, "node-cpu-2", overwritten)

	deliverRepeat(t, nodeAlertRepeat("node-cpu-2"))

	assertRepeatRow(t, nodeSubjectRow, fetchRepeatRow(t, db, "node-cpu-2"))
}

// A delivery with no subject fields gets its subject from its labels on insert.
// The repeat carries the same empty fields, and must not blank the stored ones —
// whether it still carries those labels, or the subject was resolved some other
// way and the repeat carries nothing to resolve it from.
func TestRepeatDelivery_KeepsASubjectTheRepeatDoesNotCarry_DB(t *testing.T) {
	stored := repeatRow{
		Labels:           `{"alertname":"HighErrorRate","pod":"api-7","namespace":"payments"}`,
		Priority:         "HIGH",
		SubjectName:      "api-7",
		SubjectNamespace: "payments",
	}
	for name, labels := range map[string]map[string]string{
		"labels still name the subject": {"alertname": "HighErrorRate", "pod": "api-7", "namespace": "payments"},
		"labels name no subject":        {"alertname": "HighErrorRate"},
	} {
		t.Run(name, func(t *testing.T) {
			db := repeatDeliveryTestDB(t)
			seedRepeatEvent(t, db, "error-rate-1", stored)

			deliverRepeat(t, repeatEvent("error-rate-1", labels))

			got := fetchRepeatRow(t, db, "error-rate-1")
			assert.Equal(t, "api-7", got.SubjectName)
			assert.Equal(t, "payments", got.SubjectNamespace)
		})
	}
}

// A cloud event's resource is linked from playbook evidence after the insert.
// The repeat is the source's payload again, which never carries that link.
func TestRepeatDelivery_KeepsTheLinkedCloudResource_DB(t *testing.T) {
	db := repeatDeliveryTestDB(t)
	stored := repeatRow{
		Labels:          `{"alertname":"HighCPU"}`,
		Priority:        "HIGH",
		SubjectType:     "ec2_instance",
		SubjectName:     "i-0abc123",
		SubjectOwner:    "asg-1",
		CloudResourceId: repeatLinkedResource,
	}
	seedRepeatEvent(t, db, "high-cpu-1", stored)

	e := repeatEvent("high-cpu-1", map[string]string{"alertname": "HighCPU"})
	e.SubjectType = "ec2_instance"
	e.SubjectName = "i-0abc123"
	e.SubjectOwner = "asg-1"
	deliverRepeat(t, e)

	assertRepeatRow(t, stored, fetchRepeatRow(t, db, "high-cpu-1"))
}

// An event stored without a subject name has no old subject for its linked
// resource to describe. A repeat that names it is not a changed subject: the
// name is filled in and the link stays.
func TestRepeatDelivery_NamingAnUnnamedEventKeepsItsLinkedResource_DB(t *testing.T) {
	db := repeatDeliveryTestDB(t)
	seedRepeatEvent(t, db, "high-cpu-2", repeatRow{
		Labels:          `{"alertname":"HighCPU"}`,
		Priority:        "HIGH",
		CloudResourceId: repeatLinkedResource,
	})

	e := repeatEvent("high-cpu-2", map[string]string{"alertname": "HighCPU"})
	e.SubjectType = "ec2_instance"
	e.SubjectName = "i-0abc123"
	deliverRepeat(t, e)

	assertRepeatRow(t, repeatRow{
		Labels:          `{"alertname":"HighCPU"}`,
		Priority:        "HIGH",
		SubjectType:     "ec2_instance",
		SubjectName:     "i-0abc123",
		CloudResourceId: repeatLinkedResource,
	}, fetchRepeatRow(t, db, "high-cpu-2"))
}

// Labels are merged, not replaced: a label enrichment added survives, and the
// delivery's own value wins for a label both carry.
func TestRepeatDelivery_MergesLabels_DB(t *testing.T) {
	repeatLabels := map[string]string{
		"alertname": "ApplicationAPIFailures", "destination_workload_name": "checkout", "nb_webhook_id": "1003",
	}
	for name, tt := range map[string]struct {
		stored string
		repeat map[string]string
		want   string
	}{
		"enrichment labels survive and the delivery's values win": {
			stored: `{"alertname":"ApplicationAPIFailures","destination_workload_name":"checkout","nb_webhook_id":"1001","path":"/pay","method":"POST","status":"500"}`,
			repeat: repeatLabels,
			want:   `{"alertname":"ApplicationAPIFailures","destination_workload_name":"checkout","nb_webhook_id":"1003","path":"/pay","method":"POST","status":"500"}`,
		},
		"an event stored without labels takes the delivery's": {
			stored: "",
			repeat: repeatLabels,
			want:   `{"alertname":"ApplicationAPIFailures","destination_workload_name":"checkout","nb_webhook_id":"1003"}`,
		},
		"a delivery without labels leaves the stored ones": {
			stored: `{"alertname":"ApplicationAPIFailures","path":"/pay"}`,
			repeat: nil,
			want:   `{"alertname":"ApplicationAPIFailures","path":"/pay"}`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			db := repeatDeliveryTestDB(t)
			seedRepeatEvent(t, db, "api-failures-1", repeatRow{Labels: tt.stored, Priority: "HIGH", SubjectName: "checkout", SubjectOwner: "checkout"})

			e := repeatEvent("api-failures-1", tt.repeat)
			e.SubjectName = "checkout"
			e.SubjectOwner = "checkout"
			deliverRepeat(t, e)

			assert.JSONEq(t, tt.want, fetchRepeatRow(t, db, "api-failures-1").Labels)
		})
	}
}

// A repeat that names a different subject, or carries a different priority, is
// the source changing its mind and applies as sent: the stored namespace, owner
// and resource described the old subject, so they go with it.
func TestRepeatDelivery_AppliesAChangedSubjectAndPriority_DB(t *testing.T) {
	db := repeatDeliveryTestDB(t)
	seedRepeatEvent(t, db, "disk-1", repeatRow{
		Labels:           `{"alertname":"DiskFull"}`,
		Priority:         "LOW",
		SubjectType:      "pod",
		SubjectName:      "api-7c9d-abcde",
		SubjectNamespace: "payments",
		SubjectOwner:     "api",
		SubjectOwnerKind: "Deployment",
		CloudResourceId:  repeatLinkedResource,
	})

	e := repeatEvent("disk-1", map[string]string{"alertname": "DiskFull"})
	e.SubjectType = "host"
	e.SubjectName = "dc01.example.com"
	deliverRepeat(t, e)

	assertRepeatRow(t, repeatRow{
		Labels:      `{"alertname":"DiskFull"}`,
		Priority:    "HIGH",
		SubjectType: "host",
		SubjectName: "dc01.example.com",
	}, fetchRepeatRow(t, db, "disk-1"))
}
