package triage

import (
	"context"
	"encoding/json"
	"regexp"
	"testing"

	"nudgebee/services/internal/database/models"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	pushedAlarmARN      = "arn:aws:cloudwatch:us-east-1:123456789012:alarm:orders-cpu-high"
	otherRegionAlarmARN = "arn:aws:cloudwatch:eu-west-1:123456789012:alarm:orders-cpu-high"
)

// syncedAlarmExpr is an event_rules.expr as the alarm sync writes it: the alarm
// as DescribeAlarms returned it.
func syncedAlarmExpr(arn string, threshold float64) string {
	b, _ := json.Marshal(map[string]any{
		"AlarmArn": arn, "AlarmName": "orders-cpu-high", "ComparisonOperator": "GreaterThanThreshold",
		"Threshold": threshold, "EvaluationPeriods": 3, "Period": 60, "Statistic": "Average",
	})
	return string(b)
}

func TestApplySyncedAlarm(t *testing.T) {
	pushed := func() *AlertDefinition {
		return &AlertDefinition{AlarmName: "orders-cpu-high", AlarmARN: pushedAlarmARN, CurrentThreshold: 80, Period: 300}
	}

	t.Run("same ARN and threshold: operator and evaluation periods from the rule, threshold and period from the firing", func(t *testing.T) {
		def := pushed()
		require.NoError(t, applySyncedAlarm([]string{syncedAlarmExpr(pushedAlarmARN, 80.000000)}, def))
		assert.Equal(t, "GreaterThanThreshold", def.Operator)
		assert.Equal(t, 3, def.EvaluationPeriods)
		assert.Equal(t, 80.0, def.CurrentThreshold)
		assert.Equal(t, 300, def.Period, "the firing's own period is kept")
	})

	t.Run("period from the rule when the firing has none", func(t *testing.T) {
		def := pushed()
		def.Period = 0
		require.NoError(t, applySyncedAlarm([]string{syncedAlarmExpr(pushedAlarmARN, 80)}, def))
		assert.Equal(t, 60, def.Period)
	})

	t.Run("one name in two regions, the rule holding the other region's alarm", func(t *testing.T) {
		def := pushed()
		err := applySyncedAlarm([]string{syncedAlarmExpr(otherRegionAlarmARN, 80)}, def)
		assert.ErrorIs(t, err, errRuleNotFound)
		assert.Empty(t, def.Operator)
	})

	t.Run("definition changed since the sync", func(t *testing.T) {
		def := pushed()
		err := applySyncedAlarm([]string{syncedAlarmExpr(pushedAlarmARN, 90)}, def)
		assert.ErrorIs(t, err, errThresholdUnreadable)
		assert.Empty(t, def.Operator)
	})

	t.Run("no rule", func(t *testing.T) {
		assert.ErrorIs(t, applySyncedAlarm(nil, pushed()), errRuleNotFound)
	})

	t.Run("placeholder-shaped rule with no stored alarm", func(t *testing.T) {
		assert.ErrorIs(t, applySyncedAlarm([]string{`{"summary":"orders-cpu-high"}`, "not json"}, pushed()), errRuleNotFound)
	})
}

func TestSameThreshold(t *testing.T) {
	assert.True(t, sameThreshold(80, 80.000000))
	assert.True(t, sameThreshold(-0.75, -0.75))
	assert.False(t, sameThreshold(80, 80.5))
	assert.False(t, sameThreshold(0, 0.0001))
}

// A pushed alarm's row has the threshold label and a raw event with the period
// but no operator; the definition is completed from its synced rule.
func TestExtractAWSAlertDefinition_PushedAlarm(t *testing.T) {
	raw, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = raw.Close() }()
	db := sqlx.NewDb(raw, "postgres")

	labels := models.NewJsonObject(map[string]any{
		"aws_event_type": "CloudWatch Alarm State Change", "aws_event_arn": pushedAlarmARN,
		"aws_event_name": "orders-cpu-high", "aws_event_threshold": "80", "aws_event_metric_name": "CPUUtilization",
		"aws_event_metric_namespace": "AWS/EC2", "aws_event_metric_statistic": "Average",
	})
	account := "acc-1"
	ev := models.Event{Id: "ev-1", CloudAccountId: &account, Labels: &labels}

	mock.ExpectQuery(regexp.QuoteMeta("SELECT evidences FROM events WHERE id = $1")).WithArgs("ev-1").
		WillReturnRows(sqlmock.NewRows([]string{"evidences"}).
			AddRow(`[{"data":"{\"alarmName\":\"orders-cpu-high\",\"threshold\":80,\"period\":300}"}]`))
	mock.ExpectQuery(regexp.QuoteMeta("source = 'AWS_CloudWatch_Alarm'")).WithArgs("acc-1", "orders-cpu-high").
		WillReturnRows(sqlmock.NewRows([]string{"expr"}).
			AddRow(syncedAlarmExpr(otherRegionAlarmARN, 80)).
			AddRow(syncedAlarmExpr(pushedAlarmARN, 80)))

	def, err := extractAWSAlertDefinition(context.Background(), db, ev)
	require.NoError(t, err)
	assert.Equal(t, "GreaterThanThreshold", def.Operator)
	assert.Equal(t, 3, def.EvaluationPeriods)
	assert.Equal(t, 300, def.Period)
	assert.Equal(t, 80.0, def.CurrentThreshold)
	require.NoError(t, mock.ExpectationsWereMet())
}

// A polled alarm whose raw event lacks the operator is left as it was: the
// synced rule is consulted only for pushed alarms.
func TestExtractAWSAlertDefinition_PolledAlarmDoesNotReadTheRule(t *testing.T) {
	raw, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = raw.Close() }()
	db := sqlx.NewDb(raw, "postgres")

	labels := models.NewJsonObject(map[string]any{
		"aws_event_arn": pushedAlarmARN, "aws_event_name": "orders-cpu-high",
		"aws_event_threshold": "80.000000", "aws_event_metric_name": "CPUUtilization",
	})
	ev := models.Event{Id: "ev-2", Labels: &labels}
	mock.ExpectQuery(regexp.QuoteMeta("SELECT evidences FROM events WHERE id = $1")).WithArgs("ev-2").
		WillReturnRows(sqlmock.NewRows([]string{"evidences"}).AddRow(`[]`))

	def, err := extractAWSAlertDefinition(context.Background(), db, ev)
	require.NoError(t, err)
	assert.Empty(t, def.Operator)
	require.NoError(t, mock.ExpectationsWereMet())
}
