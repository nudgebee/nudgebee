package cloud

import (
	"encoding/json"
	"errors"
	"log/slog"
	"regexp"
	"testing"

	"nudgebee/services/eventrule/playbooks"
	"nudgebee/services/internal/database"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCloudResourceActionReadsTheEventsLinkedResource(t *testing.T) {
	rawDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	database.RegisterDatabaseManagerHook(database.Metastore, func() (*database.DatabaseManager, error) {
		return &database.DatabaseManager{Db: sqlx.NewDb(rawDB, "postgresql")}, nil
	})
	t.Cleanup(func() {
		database.RegisterDatabaseManagerHook(database.Metastore, func() (*database.DatabaseManager, error) {
			return nil, errors.New("no metastore in unit tests")
		})
		_ = rawDB.Close()
	})

	columns := []string{"resourse_id", "name", "type", "arn", "service_name", "status", "region", "tags", "meta", "created_at"}
	lbArn := "arn:aws:elasticloadbalancing:us-east-1:123456789012:loadbalancer/app/example-alb/0123456789abcdef"
	event := playbooks.PlaybookEvent{
		CloudResourceId: "linked-row-id",
		Labels: map[string]string{
			"aws_account":                "123456789012",
			"aws_region":                 "us-east-1",
			"aws_service_name":           "AWSELB",
			"aws_event_instance":         "example-alb",
			"aws_event_alarm_dimensions": `[{"Name":"LoadBalancer","Value":"app/example-alb/0123456789abcdef"}]`,
		},
	}
	ctx := playbooks.NewPlaybookActionContext("tenant-1", "", slog.Default(), event)

	// The event is linked to the load balancer: the card shows it, and its ARN becomes the
	// resource labels, without matching the alarm's dimension value (which also matches the
	// load balancer's deleted cost-report record).
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id FROM cloud_accounts WHERE account_number = $1")).
		WithArgs("123456789012", "tenant-1").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("account-1"))
	mock.ExpectQuery(regexp.QuoteMeta("WHERE tenant = $1 AND id = $2 AND lower(service_name) = lower($3)")).
		WithArgs("tenant-1", "linked-row-id", "AWSELB").
		WillReturnRows(sqlmock.NewRows(columns).AddRow(lbArn, "example-alb", "application_loadbalancer", lbArn, "AWSELB", "Active", "us-east-1", nil, nil, nil))

	resp, err := (&cloudResourceAction{}).AutoExecute(ctx)
	require.NoError(t, err)
	require.NotNil(t, resp)
	labels := resp.(playbooks.PlaybookActionResponseLabelExtractor).ExtractLabels()
	assert.Equal(t, lbArn, labels["resource_arn"])
	assert.Equal(t, "application_loadbalancer", labels["resource_type"])
	var items []Resource
	require.NoError(t, json.Unmarshal([]byte(resp.GetData().(string)), &items))
	require.Len(t, items, 1)
	assert.Equal(t, ResourceStatus("Active"), items[0].Status)

	// The linked row is gone, or belongs to another service (a link made before the collector
	// ranked rows by service): fall back to the identifier match.
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id FROM cloud_accounts WHERE account_number = $1")).
		WithArgs("123456789012", "tenant-1").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("account-1"))
	mock.ExpectQuery(regexp.QuoteMeta("WHERE tenant = $1 AND id = $2 AND lower(service_name) = lower($3)")).
		WithArgs("tenant-1", "linked-row-id", "AWSELB").
		WillReturnRows(sqlmock.NewRows(columns))
	mock.ExpectQuery(regexp.QuoteMeta("WHERE tenant = $1 AND account = $2 AND lower(service_name) = lower($3)")).
		WithArgs("tenant-1", "account-1", "AWSELB", "us-east-1", sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows(columns).AddRow("app/example-alb/0123456789abcdef", "app/example-alb/0123456789abcdef", "loadbalancer", "", "AWSELB", "Deleted", "us-east-1", nil, nil, nil))

	resp, err = (&cloudResourceAction{}).AutoExecute(ctx)
	require.NoError(t, err)
	require.NotNil(t, resp)
	items = nil
	require.NoError(t, json.Unmarshal([]byte(resp.GetData().(string)), &items))
	require.Len(t, items, 1)
	assert.Equal(t, "app/example-alb/0123456789abcdef", items[0].Id)
	assert.NoError(t, mock.ExpectationsWereMet())
}
