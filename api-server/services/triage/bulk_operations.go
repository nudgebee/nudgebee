package triage

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"nudgebee/services/common"
	"nudgebee/services/internal/database"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
)

const (
	// Queue configuration
	BulkClassificationExchange   = "event_bulk_classification"
	BulkClassificationRoutingKey = "bulk_classification"
	BulkClassificationQueue      = "event_bulk_classification_queue"

	// Batch configuration
	DefaultBatchSize = 500
)

// BulkClassificationJob represents a job for bulk classification
type BulkClassificationJob struct {
	JobID            string    `json:"job_id"`
	Fingerprint      string    `json:"fingerprint"`
	AccountID        string    `json:"account_id"`
	NewStatus        string    `json:"new_status"`
	Classification   string    `json:"classification"`
	RuleID           *string   `json:"rule_id,omitempty"`
	ClassificationID *string   `json:"classification_id,omitempty"`
	ExcludeEventID   string    `json:"exclude_event_id"`
	CreatedBy        *string   `json:"created_by,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
}

// queueBulkClassification queues a bulk classification job
func queueBulkClassification(ctx context.Context, db *sqlx.DB, job BulkClassificationJob) (*BulkOperation, error) {
	// 1. Count events to update
	count, _, err := countExistingEventsWithFingerprint(ctx, db, job.Fingerprint, job.AccountID, job.ExcludeEventID, job.NewStatus)
	if err != nil {
		slog.WarnContext(ctx, "Failed to count events for bulk operation", "error", err)
		count = 0
	}

	if count == 0 {
		slog.InfoContext(ctx, "No events to update for bulk classification",
			"fingerprint", job.Fingerprint,
			"account_id", job.AccountID,
		)
		return nil, nil
	}

	// 2. Generate job ID and create bulk operation record
	jobID := uuid.New().String()
	job.JobID = jobID
	job.CreatedAt = time.Now()

	bulkOp := &BulkOperation{
		ID:               jobID,
		OperationType:    "classification",
		Fingerprint:      &job.Fingerprint,
		AccountID:        job.AccountID,
		TargetStatus:     job.NewStatus,
		TotalEvents:      count,
		ProcessedEvents:  0,
		Status:           BulkStatusQueued,
		CreatedBy:        job.CreatedBy,
		CreatedAt:        time.Now(),
		RuleID:           job.RuleID,
		ClassificationID: job.ClassificationID,
	}

	// 3. Insert bulk operation record
	if err := insertBulkOperation(ctx, db, bulkOp); err != nil {
		return nil, err
	}

	// 4. Publish to RabbitMQ queue
	if err := common.MqPublish(BulkClassificationExchange, BulkClassificationRoutingKey, job); err != nil {
		slog.ErrorContext(ctx, "Failed to publish bulk classification job", "error", err, "job_id", jobID)
		// Update status to failed
		updateBulkOperationStatus(ctx, db, jobID, BulkStatusFailed, "Failed to queue job")
		return nil, err
	}

	slog.InfoContext(ctx, "Queued bulk classification job",
		"job_id", jobID,
		"fingerprint", job.Fingerprint,
		"total_events", count,
		"target_status", job.NewStatus,
	)

	return bulkOp, nil
}

// ProcessBulkClassification processes a bulk classification job (called by consumer)
func ProcessBulkClassification(ctx context.Context, db *sqlx.DB, jobData []byte) error {
	var job BulkClassificationJob
	if err := json.Unmarshal(jobData, &job); err != nil {
		slog.ErrorContext(ctx, "Failed to unmarshal bulk classification job", "error", err)
		return err
	}

	slog.InfoContext(ctx, "Processing bulk classification job",
		"job_id", job.JobID,
		"fingerprint", job.Fingerprint,
		"target_status", job.NewStatus,
	)

	// Update status to processing
	updateBulkOperationStatus(ctx, db, job.JobID, BulkStatusProcessing, "")

	// Process in batches
	processedCount := 0
	batchSize := DefaultBatchSize

	for {
		// Batch update events
		rowsAffected, err := batchUpdateEvents(ctx, db, &job, batchSize)
		if err != nil {
			slog.ErrorContext(ctx, "Batch update failed",
				"error", err,
				"job_id", job.JobID,
				"processed_so_far", processedCount,
			)
			updateBulkOperationStatus(ctx, db, job.JobID, BulkStatusFailed, err.Error())
			return err
		}

		if rowsAffected == 0 {
			break // No more events to update
		}

		processedCount += int(rowsAffected)

		// Update progress
		updateBulkOperationProgress(ctx, db, job.JobID, processedCount)

		slog.InfoContext(ctx, "Batch processed",
			"job_id", job.JobID,
			"rows_affected", rowsAffected,
			"total_processed", processedCount,
		)
	}

	// Mark as completed
	completeBulkOperation(ctx, db, job.JobID, processedCount)

	// Update rule match count with the number of bulk-classified events
	if job.RuleID != nil && processedCount > 0 {
		updateRuleMatchCountBy(ctx, db, *job.RuleID, processedCount)
	}

	slog.InfoContext(ctx, "Bulk classification job completed",
		"job_id", job.JobID,
		"total_processed", processedCount,
	)

	return nil
}

// batchUpdateEvents updates a batch of events
func batchUpdateEvents(ctx context.Context, db *sqlx.DB, job *BulkClassificationJob, batchSize int) (int64, error) {
	// Get IDs of events to update (need to select first because UPDATE...LIMIT varies by DB)
	selectQuery := `
		SELECT id
		FROM events
		WHERE fingerprint = $1
		  AND cloud_account_id = $2
		  AND id != $3
		  AND nb_status != $4
		ORDER BY created_at DESC
		LIMIT $5
	`

	var eventIDs []string
	err := db.SelectContext(ctx, &eventIDs, selectQuery, job.Fingerprint, job.AccountID, job.ExcludeEventID, job.NewStatus, batchSize)
	if err != nil {
		return 0, err
	}

	if len(eventIDs) == 0 {
		return 0, nil
	}

	// Update the events
	updateQuery := `
		UPDATE events
		SET nb_status = $1,
		    nb_status_changed_at = NOW(),
		    updated_at = NOW()
		WHERE id = ANY($2)
	`

	result, err := db.ExecContext(ctx, updateQuery, job.NewStatus, pq.Array(eventIDs))
	if err != nil {
		return 0, err
	}

	rowsAffected, _ := result.RowsAffected()

	// Record rule matches and log to event_history (async to not slow down batch)
	go logBulkStatusChangeToHistory(context.Background(), db, eventIDs, job)
	if job.RuleID != nil {
		go insertBulkRuleMatches(context.Background(), db, eventIDs, job)
	}

	return rowsAffected, nil
}

// insertBulkRuleMatches records rule matches for bulk-classified events in event_triage_rule_matches.
func insertBulkRuleMatches(ctx context.Context, db *sqlx.DB, eventIDs []string, job *BulkClassificationJob) {
	if job.RuleID == nil {
		return
	}

	ruleType := RuleTypeSuppression
	action := ActionSuppress
	if job.Classification == ClassificationDuplicate {
		ruleType = RuleTypeClassification
		action = ActionAutoClassifyDuplicate
	}

	var tenantID string
	err := db.GetContext(ctx, &tenantID, "SELECT tenant FROM events WHERE cloud_account_id = $1 LIMIT 1", job.AccountID)
	if err != nil {
		slog.WarnContext(ctx, "Failed to get tenant for bulk rule matches", "error", err)
		return
	}

	for i := 0; i < len(eventIDs); i += 100 {
		end := i + 100
		if end > len(eventIDs) {
			end = len(eventIDs)
		}
		batch := eventIDs[i:end]

		// Use strings.Builder to avoid O(n²) string concatenation from repeated += in the loop.
		// Each placeholder group is ~30 chars; pre-allocate to avoid reallocations.
		var sb strings.Builder
		sb.Grow(100 + len(batch)*35)
		sb.WriteString(`INSERT INTO event_triage_rule_matches (event_id, rule_id, cloud_account_id, tenant_id, rule_type, action) VALUES `)
		args := make([]interface{}, 0, len(batch)*6)
		for j, eid := range batch {
			if j > 0 {
				sb.WriteString(", ")
			}
			base := j * 6
			fmt.Fprintf(&sb, "($%d, $%d, $%d, $%d, $%d, $%d)", base+1, base+2, base+3, base+4, base+5, base+6)
			args = append(args, eid, *job.RuleID, job.AccountID, tenantID, ruleType, action)
		}
		sb.WriteString(" ON CONFLICT (event_id, rule_id, cloud_account_id) DO NOTHING")
		query := sb.String()

		_, err := db.ExecContext(ctx, query, args...)
		if err != nil {
			slog.WarnContext(ctx, "Failed to insert bulk rule matches",
				"error", err,
				"job_id", job.JobID,
				"batch_size", len(batch),
			)
		}
	}
}

// logBulkStatusChangeToHistory logs bulk status changes to event_history.
// Uses batch SELECT + batch INSERT to avoid N+1 queries (previously 2 queries per event).
func logBulkStatusChangeToHistory(ctx context.Context, db *sqlx.DB, eventIDs []string, job *BulkClassificationJob) {
	if len(eventIDs) == 0 {
		return
	}

	// Add a timeout since this runs in a background goroutine with context.Background().
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	changeType := "status"
	changedBy := "system"
	if job.CreatedBy != nil {
		changedBy = *job.CreatedBy
	}
	newValue := map[string]interface{}{
		"job_id":         job.JobID,
		"nb_status":      job.NewStatus,
		"classification": job.Classification,
	}
	newValueJSON, err := json.Marshal(newValue)
	if err != nil {
		slog.ErrorContext(ctx, "Failed to marshal history value", "error", err, "job_id", job.JobID)
		return
	}

	// Batch fetch all event info in one query instead of one query per event.
	type eventInfo struct {
		ID             string `db:"id"`
		CloudAccountID string `db:"cloud_account_id"`
		TenantID       string `db:"tenant"`
	}
	var events []eventInfo
	query := "SELECT id, cloud_account_id, tenant FROM events WHERE id = ANY($1)"
	err = db.SelectContext(ctx, &events, query, pq.Array(eventIDs))
	if err != nil {
		slog.WarnContext(ctx, "Failed to batch-fetch events for history logging", "error", err)
		return
	}

	// Batch INSERT history records in chunks of 100 to avoid oversized queries.
	for i := 0; i < len(events); i += 100 {
		end := i + 100
		if end > len(events) {
			end = len(events)
		}
		batch := events[i:end]

		var sb strings.Builder
		sb.Grow(150 + len(batch)*60)
		sb.WriteString(`INSERT INTO event_history (id, event_id, cloud_account_id, tenant_id, change_type, changed_by, new_value, change_reason) VALUES `)
		args := make([]interface{}, 0, len(batch)*8)
		for j, ev := range batch {
			if j > 0 {
				sb.WriteString(", ")
			}
			base := j * 8
			fmt.Fprintf(&sb, "($%d, $%d, $%d, $%d, $%d, $%d, $%d, $%d)",
				base+1, base+2, base+3, base+4, base+5, base+6, base+7, base+8)
			args = append(args, uuid.New().String(), ev.ID, ev.CloudAccountID, ev.TenantID,
				changeType, changedBy, string(newValueJSON), "bulk_classification")
		}

		_, err := db.ExecContext(ctx, sb.String(), args...)
		if err != nil {
			slog.WarnContext(ctx, "Failed to batch-insert bulk status change history",
				"error", err,
				"job_id", job.JobID,
				"batch_size", len(batch),
			)
		}
	}
}

// GetBulkOperationStatus retrieves the status of a bulk operation
func GetBulkOperationStatus(ctx context.Context, db *sqlx.DB, jobID string) (*BulkOperation, error) {
	query := `
		SELECT
			id, operation_type, fingerprint, account_id, target_status,
			total_events, processed_events, status,
			created_by, created_at, completed_at,
			rule_id, classification_id, error_message
		FROM event_bulk_operations
		WHERE id = $1
	`

	var op BulkOperation
	err := db.GetContext(ctx, &op, query, jobID)
	if err != nil {
		return nil, err
	}

	return &op, nil
}

// -------------------- Database Operations --------------------

// insertBulkOperation inserts a bulk operation record
func insertBulkOperation(ctx context.Context, db *sqlx.DB, op *BulkOperation) error {
	query := `
		INSERT INTO event_bulk_operations (
			id, operation_type, fingerprint, account_id, target_status,
			total_events, processed_events, status,
			created_by, created_at, rule_id, classification_id
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12
		)
	`

	_, err := db.ExecContext(ctx, query,
		op.ID, op.OperationType, op.Fingerprint, op.AccountID, op.TargetStatus,
		op.TotalEvents, op.ProcessedEvents, op.Status,
		op.CreatedBy, op.CreatedAt, op.RuleID, op.ClassificationID,
	)

	return err
}

// updateBulkOperationStatus updates the status of a bulk operation
func updateBulkOperationStatus(ctx context.Context, db *sqlx.DB, jobID, status, errorMsg string) {
	query := `
		UPDATE event_bulk_operations
		SET status = $1,
		    error_message = NULLIF($2, '')
		WHERE id = $3
	`

	_, err := db.ExecContext(ctx, query, status, errorMsg, jobID)
	if err != nil {
		slog.WarnContext(ctx, "Failed to update bulk operation status",
			"error", err,
			"job_id", jobID,
		)
	}
}

// updateBulkOperationProgress updates the processed count
func updateBulkOperationProgress(ctx context.Context, db *sqlx.DB, jobID string, processedCount int) {
	query := `
		UPDATE event_bulk_operations
		SET processed_events = $1
		WHERE id = $2
	`

	_, err := db.ExecContext(ctx, query, processedCount, jobID)
	if err != nil {
		slog.WarnContext(ctx, "Failed to update bulk operation progress",
			"error", err,
			"job_id", jobID,
		)
	}
}

// completeBulkOperation marks a bulk operation as completed
func completeBulkOperation(ctx context.Context, db *sqlx.DB, jobID string, processedCount int) {
	query := `
		UPDATE event_bulk_operations
		SET status = $1,
		    processed_events = $2,
		    completed_at = NOW()
		WHERE id = $3
	`

	_, err := db.ExecContext(ctx, query, BulkStatusCompleted, processedCount, jobID)
	if err != nil {
		slog.WarnContext(ctx, "Failed to complete bulk operation",
			"error", err,
			"job_id", jobID,
		)
	}
}

// init starts the bulk classification consumer automatically when the package is loaded
func init() {
	err := common.MqConsume(
		BulkClassificationExchange,
		BulkClassificationRoutingKey,
		BulkClassificationQueue,
		1, // concurrency=1 to process one job at a time
		func(data []byte) error {
			dbms, err := database.GetDatabaseManager(database.Metastore)
			if err != nil {
				slog.Error("Failed to get database for bulk classification", "error", err)
				return err
			}
			return ProcessBulkClassification(context.Background(), dbms.Db, data)
		},
	)
	if err != nil {
		slog.Error("Failed to start bulk classification consumer", "error", err)
	}
}
