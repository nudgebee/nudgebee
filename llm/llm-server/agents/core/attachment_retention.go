package core

import (
	"log/slog"

	"nudgebee/llm/config"
)

const (
	defaultImageRetentionDays = 7
)

// GetImageRetentionDays returns the configured retention period for image data.
// Returns the default if the configured value is invalid (0 or negative).
func GetImageRetentionDays() int {
	days := config.Config.GetInt("llm_server_image_retention_days", defaultImageRetentionDays)
	if days < 1 {
		slog.Warn("attachment_retention: invalid retention config, using default",
			"configured_days", days, "default_days", defaultImageRetentionDays)
		return defaultImageRetentionDays
	}
	return days
}

// PurgeExpiredImageAttachments runs the retention cleanup, nullifying image data
// older than the configured retention period while preserving metadata and descriptions.
// Returns the number of purged attachments or an error.
func PurgeExpiredImageAttachments() (int64, error) {
	dao := GetAttachmentDAO()
	if dao == nil {
		return 0, nil
	}

	retentionDays := GetImageRetentionDays()
	return dao.PurgeExpiredAttachments(retentionDays)
}
