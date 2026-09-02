package aws

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func cwProduct(usageType, usd string) map[string]interface{} {
	p := pricedInstance(usd)
	p["product"] = map[string]any{"attributes": map[string]any{"usagetype": usageType}}
	return p
}

// The class lives in the tail of the usage type; the region prefix varies and
// us-east-1 also publishes a legacy unprefixed entry.
func TestCloudWatchStorageUsageTypeSuffix(t *testing.T) {
	assert.Equal(t, "TimedStorage-ByteHrs", cloudWatchStorageUsageTypeSuffix("STANDARD"))
	assert.Equal(t, "TimedStorage-ByteHrs", cloudWatchStorageUsageTypeSuffix(""))
	assert.Equal(t, "TimedStorage-ByteHrs", cloudWatchStorageUsageTypeSuffix("DELIVERY"))
	assert.Equal(t, "TimedStorage-IA-ByteHrs", cloudWatchStorageUsageTypeSuffix("INFREQUENT_ACCESS"))
	assert.Equal(t, "TimedStorage-IA-ByteHrs", cloudWatchStorageUsageTypeSuffix(" infrequent_access "))
}

// Standard must not swallow the IA or Archive SKUs, which sit in the same list
// at a fifth of the price — the whole reason the class is read at all.
func TestSelectCloudWatchStorageRateDoesNotCrossMatchClasses(t *testing.T) {
	products := []map[string]interface{}{
		cwProduct("USE1-TimedStorage-ByteHrs", "0.0300000000"),
		cwProduct("USE1-TimedStorage-IA-ByteHrs", "0.0180000000"),
		cwProduct("USE1-TimedStorage-AIA-ByteHrs", "0.0060000000"),
	}
	std, ok := selectCloudWatchStorageRate(products, cloudWatchStorageUsageTypeSuffix("STANDARD"))
	assert.True(t, ok)
	assert.InDelta(t, 0.03, std, 1e-9)

	ia, ok := selectCloudWatchStorageRate(products, cloudWatchStorageUsageTypeSuffix("INFREQUENT_ACCESS"))
	assert.True(t, ok)
	assert.InDelta(t, 0.018, ia, 1e-9)
}

// us-east-1 lists the current prefixed SKU and a legacy unprefixed one at the
// same price; either is a correct answer, neither may be skipped.
func TestSelectCloudWatchStorageRateAcceptsTheLegacyUnprefixedSku(t *testing.T) {
	rate, ok := selectCloudWatchStorageRate([]map[string]interface{}{cwProduct("TimedStorage-ByteHrs", "0.0300000000")}, "TimedStorage-ByteHrs")
	assert.True(t, ok)
	assert.InDelta(t, 0.03, rate, 1e-9)
}

func TestSelectCloudWatchStorageRateReportsNoMatch(t *testing.T) {
	_, ok := selectCloudWatchStorageRate([]map[string]interface{}{cwProduct("USE1-DataProcessing-Bytes", "0.5")}, "TimedStorage-ByteHrs")
	assert.False(t, ok, "an unrelated SKU must not be mistaken for storage")

	_, ok = selectCloudWatchStorageRate([]map[string]interface{}{cwProduct("USE1-TimedStorage-ByteHrs", "0.0000000000")}, "TimedStorage-ByteHrs")
	assert.False(t, ok, "a zero price parses without error and must not be trusted")
}

func TestSelectCloudWatchStorageRateTakesTheCheapestDuplicate(t *testing.T) {
	products := []map[string]interface{}{
		cwProduct("USE1-TimedStorage-ByteHrs", "0.0400000000"),
		cwProduct("TimedStorage-ByteHrs", "0.0300000000"),
	}
	rate, ok := selectCloudWatchStorageRate(products, "TimedStorage-ByteHrs")
	assert.True(t, ok)
	assert.InDelta(t, 0.03, rate, 1e-9)
}

func TestAgedOutFraction(t *testing.T) {
	now := time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC)
	// 365 days old, keep 30: 335/365 of the bytes are older than the cut-off.
	assert.InDelta(t, 335.0/365.0, agedOutFraction(now.AddDate(0, 0, -365), 30, now), 1e-9)
	// Exactly at the boundary, and younger than it, nothing ages out.
	assert.Zero(t, agedOutFraction(now.AddDate(0, 0, -30), 30, now))
	assert.Zero(t, agedOutFraction(now.AddDate(0, 0, -5), 30, now))
	// A group with no creation time yields no claim rather than a wrong one.
	assert.Zero(t, agedOutFraction(time.Time{}, 30, now))
	assert.Zero(t, agedOutFraction(now.AddDate(0, 0, -365), 0, now))
}

func TestAgedOutFractionApproachesButNeverReachesOne(t *testing.T) {
	now := time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC)
	f := agedOutFraction(now.AddDate(-10, 0, 0), 30, now)
	assert.Greater(t, f, 0.99)
	assert.Less(t, f, 1.0, "the retained window is always still stored")
}

func TestLogRetentionMonthlySaving(t *testing.T) {
	// 100 GB, all of it aged out, at the us-east-1 standard rate.
	assert.InDelta(t, 3.0, logRetentionMonthlySaving(100*bytesPerGB, 1.0, 0.03), 1e-9)
	// Half aged out halves the figure.
	assert.InDelta(t, 1.5, logRetentionMonthlySaving(100*bytesPerGB, 0.5, 0.03), 1e-9)
	// An empty or brand-new group claims nothing.
	assert.Zero(t, logRetentionMonthlySaving(0, 1.0, 0.03))
	assert.Zero(t, logRetentionMonthlySaving(100*bytesPerGB, 0, 0.03))
	assert.Zero(t, logRetentionMonthlySaving(100*bytesPerGB, 1.0, 0))
}

func TestMetaFloatHandlesSdkAndJsonForms(t *testing.T) {
	var i64 int64 = 42
	var i32 int32 = 42
	for name, v := range map[string]any{"float64": float64(42), "int": 42, "int64": i64, "*int64": &i64, "*int32": &i32} {
		got, ok := metaFloat(v)
		assert.True(t, ok, name)
		assert.InDelta(t, 42.0, got, 1e-9, name)
	}
	for name, v := range map[string]any{"string": "42", "nil": nil, "nil ptr": (*int64)(nil)} {
		_, ok := metaFloat(v)
		assert.False(t, ok, name)
	}
}
