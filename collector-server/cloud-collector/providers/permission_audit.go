package providers

import (
	"fmt"
	"log/slog"
	"nudgebee/collector/cloud/common"
	"sync"
	"time"
)

// PermissionAuditRecord represents a single cloud API permission error occurrence.
type PermissionAuditRecord struct {
	TenantID       string
	CloudAccountID string // nudgebee account UUID
	AccountNumber  string // AWS account ID / Azure subscription / GCP project
	CloudProvider  string // "AWS", "Azure", "GCP"
	ServiceName    string // e.g., "ec2", "microsoft.compute/virtualmachines", "compute engine"
	APIOperation   string // e.g., "GetBucketAcl", "Microsoft.Compute/virtualMachines", "compute.instances.list"
	WrapperMethod  string // e.g., "GetResources", "QueryMetrices"
	ErrorCode      string // e.g., "AccessDenied", "AuthorizationFailed", "403"
	ErrorMessage   string
	Region         string
	OccurredAt     time.Time
}

func (r PermissionAuditRecord) dedupKey() string {
	return fmt.Sprintf("%s|%s|%s|%s|%s|%s", r.TenantID, r.CloudAccountID, r.ServiceName, r.APIOperation, r.ErrorCode, r.Region)
}

// PermissionAuditCollector buffers permission error records in memory and
// periodically flushes them to PostgreSQL via batch upsert.
type PermissionAuditCollector struct {
	mu       sync.Mutex
	wg       sync.WaitGroup
	buffer   []PermissionAuditRecord
	dedupMap map[string]time.Time
	ticker   *time.Ticker
	stopCh   chan struct{}
	stopped  bool
}

const (
	flushInterval    = 30 * time.Second
	flushBufferLimit = 100
	dedupTTL         = 1 * time.Hour
)

var (
	collectorInstance *PermissionAuditCollector
	collectorOnce     sync.Once
)

// GetPermissionAuditCollector returns the singleton collector instance.
func GetPermissionAuditCollector() *PermissionAuditCollector {
	collectorOnce.Do(func() {
		collectorInstance = &PermissionAuditCollector{
			buffer:   make([]PermissionAuditRecord, 0, flushBufferLimit),
			dedupMap: make(map[string]time.Time),
			stopCh:   make(chan struct{}),
		}
	})
	return collectorInstance
}

// Record adds a permission error to the buffer if it hasn't been seen recently.
func (c *PermissionAuditCollector) Record(r PermissionAuditRecord) {
	if r.OccurredAt.IsZero() {
		r.OccurredAt = time.Now()
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	key := r.dedupKey()
	if lastSeen, ok := c.dedupMap[key]; ok && time.Since(lastSeen) < dedupTTL {
		return
	}
	c.dedupMap[key] = r.OccurredAt
	c.buffer = append(c.buffer, r)

	if len(c.buffer) >= flushBufferLimit {
		c.flushLocked()
	}
}

// Start begins the background flush goroutine.
func (c *PermissionAuditCollector) Start() {
	c.ticker = time.NewTicker(flushInterval)
	go func() {
		for {
			select {
			case <-c.ticker.C:
				c.Flush()
			case <-c.stopCh:
				return
			}
		}
	}()
	slog.Info("permission audit collector started")
}

// Stop flushes remaining records and stops the background goroutine.
func (c *PermissionAuditCollector) Stop() {
	c.mu.Lock()
	if c.stopped {
		c.mu.Unlock()
		return
	}
	c.stopped = true
	c.mu.Unlock()

	if c.ticker != nil {
		c.ticker.Stop()
	}
	close(c.stopCh)
	c.Flush()
	c.wg.Wait()
	slog.Info("permission audit collector stopped")
}

// Flush writes buffered records to PostgreSQL.
func (c *PermissionAuditCollector) Flush() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.flushLocked()
}

func (c *PermissionAuditCollector) flushLocked() {
	if len(c.buffer) == 0 {
		return
	}

	records := make([]PermissionAuditRecord, len(c.buffer))
	copy(records, c.buffer)
	c.buffer = c.buffer[:0]

	// Clean expired dedup entries
	now := time.Now()
	for k, t := range c.dedupMap {
		if now.Sub(t) > dedupTTL {
			delete(c.dedupMap, k)
		}
	}

	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		c.writeToDB(records)
	}()
}

func (c *PermissionAuditCollector) writeToDB(records []PermissionAuditRecord) {
	db, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		slog.Error("permission audit: failed to get database manager", "error", err)
		return
	}

	args := make([]map[string]any, 0, len(records))
	for _, r := range records {
		args = append(args, map[string]any{
			"tenant_id":        r.TenantID,
			"cloud_account_id": r.CloudAccountID,
			"account_number":   r.AccountNumber,
			"cloud_provider":   r.CloudProvider,
			"service_name":     r.ServiceName,
			"api_operation":    r.APIOperation,
			"wrapper_method":   r.WrapperMethod,
			"error_code":       r.ErrorCode,
			"error_message":    truncate(r.ErrorMessage, 4000),
			"region":           r.Region,
			"first_seen_at":    r.OccurredAt,
			"last_seen_at":     r.OccurredAt,
		})
	}

	query := `INSERT INTO cloud_api_permission_errors
		(tenant_id, cloud_account_id, account_number, cloud_provider, service_name, api_operation, wrapper_method, error_code, error_message, region, first_seen_at, last_seen_at, occurrence_count)
		VALUES (:tenant_id, :cloud_account_id, :account_number, :cloud_provider, :service_name, :api_operation, :wrapper_method, :error_code, :error_message, :region, :first_seen_at, :last_seen_at, 1)
		ON CONFLICT (tenant_id, cloud_account_id, service_name, api_operation, error_code, region)
		DO UPDATE SET
			last_seen_at = EXCLUDED.last_seen_at,
			occurrence_count = cloud_api_permission_errors.occurrence_count + 1,
			error_message = EXCLUDED.error_message,
			wrapper_method = EXCLUDED.wrapper_method,
			is_resolved = FALSE,
			resolved_at = NULL`

	_, err = db.NamedExec(query, args)
	if err != nil {
		slog.Error("permission audit: failed to flush records", "error", err, "count", len(records))
		return
	}
	slog.Info("permission audit: flushed records", "count", len(records))
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen]
}

// StartPermissionAuditCollector initializes and starts the singleton collector.
func StartPermissionAuditCollector() {
	GetPermissionAuditCollector().Start()
}

// StopPermissionAuditCollector stops the singleton collector and flushes remaining records.
func StopPermissionAuditCollector() {
	GetPermissionAuditCollector().Stop()
}
