package event

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// DBExecutor is satisfied by both *sqlx.DB and *sqlx.Tx, allowing
// InsertEventHistory to participate in an existing transaction.
type DBExecutor interface {
	ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error)
}

// GenerateHistoryID creates a deterministic ID from change fingerprint
// Same change -> Same ID -> Database rejects duplicate via PRIMARY KEY
func GenerateHistoryID(eventID, changeType string, oldValue, newValue interface{}) string {
	// Canonicalize values to JSON
	oldJSON, err := json.Marshal(oldValue)
	if err != nil {
		// Log the error or handle it appropriately, e.g., return an error or a default value
		oldJSON = []byte("null") // Fallback to a consistent representation
	}
	newJSON, err := json.Marshal(newValue)
	if err != nil {
		// Log the error or handle it appropriately
		newJSON = []byte("null") // Fallback to a consistent representation
	}

	// Create fingerprint: event_id|change_type|old_json|new_json
	fingerprint := fmt.Sprintf("%s|%s|%s|%s",
		eventID,
		changeType,
		string(oldJSON),
		string(newJSON),
	)

	// Generate deterministic hash
	hash := sha256.Sum256([]byte(fingerprint))
	// Return first 16 bytes as hex (32 characters)
	return hex.EncodeToString(hash[:16])
}

// InsertEventHistory records a change to an event attribute
func InsertEventHistory(
	ctx context.Context,
	db DBExecutor,
	eventID string,
	tenantID string,
	cloudAccountID string,
	changeType string,
	oldValue interface{},
	newValue interface{},
	changeReason string,
	metadata map[string]interface{},
) error {
	// Generate deterministic ID to prevent duplicates
	historyID := GenerateHistoryID(eventID, changeType, oldValue, newValue)

	var oldJSON, newJSON, metaJSON []byte
	var err error

	// Marshal old value, or use JSON null if nil
	if oldValue != nil {
		oldJSON, err = json.Marshal(oldValue)
		if err != nil {
			return err
		}
	} else {
		// PostgreSQL JSONB column requires valid JSON, not nil bytes
		oldJSON = []byte("null")
	}

	newJSON, err = json.Marshal(newValue)
	if err != nil {
		return err
	}

	if len(metadata) > 0 {
		metaJSON, err = json.Marshal(metadata)
		if err != nil {
			return err
		}
	}

	query := `
		INSERT INTO event_history (
			id, event_id, tenant_id, cloud_account_id,
			change_type, old_value, new_value,
			change_reason, metadata
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (id) DO NOTHING
	`

	_, err = db.ExecContext(ctx, query,
		historyID, eventID, tenantID, cloudAccountID,
		changeType, oldJSON, newJSON,
		changeReason, metaJSON,
	)

	return err
}
