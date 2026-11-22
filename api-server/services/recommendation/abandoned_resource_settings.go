package recommendation

import (
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"strings"

	"nudgebee/services/internal/database"
	"nudgebee/services/security"
)

// abandonedResourceSettingsAttr is the cloud_account_attrs key the cluster
// integration settings write the abandoned-app configuration under: one row,
// one JSON value {network_threshold, observation_days}. The UI stores each
// field as a number or as a numeric string, depending on whether it was edited.
const abandonedResourceSettingsAttr = "abandoned_resource"

// abandonedResourceMinObservationDays is the shortest window that reliably
// flags anything. findAbandonedWorkloads skips a workload with five or fewer
// whole days of data. A six-day window clears that only when samples land on
// both ends of the range, and anything shorter never does, so a smaller saved
// value would silently stop detection.
const abandonedResourceMinObservationDays = 7

type abandonedResourceSettings struct {
	NetworkThreshold int
	ObservationDays  int
}

// parseAbandonedResourceSettings applies a stored value over the defaults, one
// field at a time, so one bad field does not discard the other. ok is false
// when any part of the value was ignored or raised to the minimum window.
func parseAbandonedResourceSettings(raw string) (settings abandonedResourceSettings, ok bool) {
	settings = abandonedResourceSettings{
		NetworkThreshold: abandonedResourceNetworkThresholdDefault,
		ObservationDays:  abandonedResourceObservationPeriodDaysDefault,
	}
	if strings.TrimSpace(raw) == "" {
		return settings, true
	}
	var fields map[string]any
	if err := json.Unmarshal([]byte(raw), &fields); err != nil {
		return settings, false
	}
	ok = true
	if v, present := fields["network_threshold"]; present {
		if n, valid := positiveWholeNumber(v); valid {
			settings.NetworkThreshold = n
		} else {
			ok = false
		}
	}
	if v, present := fields["observation_days"]; present {
		if n, valid := positiveWholeNumber(v); valid {
			settings.ObservationDays = n
		} else {
			ok = false
		}
	}
	if settings.ObservationDays < abandonedResourceMinObservationDays {
		settings.ObservationDays = abandonedResourceMinObservationDays
		ok = false
	}
	return settings, ok
}

// positiveWholeNumber accepts a JSON number or a numeric string holding a
// whole number greater than zero.
func positiveWholeNumber(v any) (int, bool) {
	switch t := v.(type) {
	case float64:
		if t <= 0 || t != math.Trunc(t) || t > math.MaxInt32 {
			return 0, false
		}
		return int(t), true
	case string:
		n, err := strconv.Atoi(strings.TrimSpace(t))
		if err != nil || n <= 0 || n > math.MaxInt32 {
			return 0, false
		}
		return n, true
	default:
		return 0, false
	}
}

// getAbandonedResourceSettings reads the account's abandoned-app settings. No
// row means the defaults. A stored value that is partly unusable falls back
// per field and is logged. A failed read is returned rather than defaulted:
// these recommendations offer to scale workloads to zero, so the scan should
// not run on a window the user did not choose.
func getAbandonedResourceSettings(ctx *security.RequestContext, dbms *database.DatabaseManager, accountId string) (abandonedResourceSettings, error) {
	var raw sql.NullString
	err := dbms.Db.Get(&raw,
		`SELECT value FROM cloud_account_attrs WHERE cloud_account_id = $1 AND name = $2`,
		accountId, abandonedResourceSettingsAttr)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return abandonedResourceSettings{}, err
	}
	settings, ok := parseAbandonedResourceSettings(raw.String)
	if !ok {
		ctx.GetLogger().Warn("abandoned_resource: stored settings partly unusable, using defaults or minimum for those fields",
			"account_id", accountId, "value", raw.String,
			"network_threshold", settings.NetworkThreshold, "observation_days", settings.ObservationDays)
	}
	return settings, nil
}
