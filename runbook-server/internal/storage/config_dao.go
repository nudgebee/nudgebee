package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"nudgebee/runbook/common"
	"nudgebee/runbook/internal/model"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

type ConfigDao struct {
	db *sqlx.DB
}

func NewConfigDao() (*ConfigDao, error) {
	db, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return nil, err
	}
	return &ConfigDao{db: db.Db}, nil
}

// nullableAccountID returns the account_id arg to use in SQL — a NULL when the
// caller passes nil or the empty string (tenant-scoped row), otherwise the value.
func nullableAccountID(accountID *string) any {
	if accountID == nil || *accountID == "" {
		return nil
	}
	return *accountID
}

// Save upserts a config. It picks the upsert key based on scope:
//   - account-level rows (account_id NOT NULL) conflict on (account_id, key) via uq_configs_account_key
//   - tenant-level rows (account_id IS NULL) conflict on (tenant_id, key) via uq_configs_tenant_key_global
func (d *ConfigDao) Save(ctx context.Context, config model.Config) (string, error) {
	if config.ID == "" {
		config.ID = uuid.New().String()
	}

	labelsBytes, err := json.Marshal(config.Labels)
	if err != nil {
		return "", fmt.Errorf("failed to marshal labels: %w", err)
	}

	metadataBytes, err := json.Marshal(config.Metadata)
	if err != nil {
		return "", fmt.Errorf("failed to marshal metadata: %w", err)
	}

	conflictTarget := "(account_id, key) WHERE account_id IS NOT NULL"
	if config.IsTenantScoped() {
		conflictTarget = "(tenant_id, key) WHERE account_id IS NULL"
	}

	query := fmt.Sprintf(`
		INSERT INTO configs (id, key, value, type, labels, metadata, tenant_id, account_id, created_by, updated_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT %s DO UPDATE SET
			value = EXCLUDED.value,
			type = EXCLUDED.type,
			labels = EXCLUDED.labels,
			metadata = EXCLUDED.metadata,
			updated_at = NOW(),
			updated_by = EXCLUDED.updated_by
	`, conflictTarget)

	_, err = d.db.ExecContext(ctx, query,
		config.ID, config.Key, config.Value, config.Type,
		labelsBytes, metadataBytes,
		config.TenantID, nullableAccountID(config.AccountID),
		config.CreatedBy, config.UpdatedBy)
	if err != nil {
		return "", fmt.Errorf("failed to save config: %w", err)
	}
	return config.ID, nil
}

// Get fetches a single config row. When accountID is nil/empty, looks up the
// tenant-level row; otherwise looks up the account-level row.
func (d *ConfigDao) Get(ctx context.Context, tenantID string, accountID *string, key string) (*model.Config, error) {
	var query string
	var args []any
	if accountID == nil || *accountID == "" {
		query = `
			SELECT id, key, value, type, labels, metadata, tenant_id, account_id, created_at, updated_at, created_by, updated_by
			FROM configs
			WHERE tenant_id = $1 AND account_id IS NULL AND key = $2
		`
		args = []any{tenantID, key}
	} else {
		query = `
			SELECT id, key, value, type, labels, metadata, tenant_id, account_id, created_at, updated_at, created_by, updated_by
			FROM configs
			WHERE tenant_id = $1 AND account_id = $2 AND key = $3
		`
		args = []any{tenantID, *accountID, key}
	}

	row := d.db.QueryRowContext(ctx, query, args...)
	cfg, err := scanConfig(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get config: %w", err)
	}
	return cfg, nil
}

// listScope describes what rows List should return.
type ListScope struct {
	// IncludeTenant returns tenant-level rows (account_id IS NULL).
	IncludeTenant bool
	// AccountID, when non-nil/non-empty, also includes rows for that account.
	AccountID *string
}

// List returns rows matching the given scope, optionally filtered by labels.
func (d *ConfigDao) List(ctx context.Context, tenantID string, scope ListScope, labels map[string]string) ([]model.Config, error) {
	clauses := []string{"tenant_id = $1"}
	args := []any{tenantID}
	argIdx := 2

	scopeClauses := []string{}
	if scope.IncludeTenant {
		scopeClauses = append(scopeClauses, "account_id IS NULL")
	}
	if scope.AccountID != nil && *scope.AccountID != "" {
		scopeClauses = append(scopeClauses, fmt.Sprintf("account_id = $%d", argIdx))
		args = append(args, *scope.AccountID)
		argIdx++
	}
	if len(scopeClauses) == 0 {
		// Nothing to return.
		return []model.Config{}, nil
	}
	clauses = append(clauses, "("+joinOr(scopeClauses)+")")

	if len(labels) > 0 {
		labelsJSON, err := json.Marshal(labels)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal labels filter: %w", err)
		}
		clauses = append(clauses, fmt.Sprintf("labels @> $%d", argIdx))
		args = append(args, labelsJSON)
	}

	query := `
		SELECT id, key, value, type, labels, metadata, tenant_id, account_id, created_at, updated_at, created_by, updated_by
		FROM configs
		WHERE ` + joinAnd(clauses) + `
		ORDER BY key ASC
	`

	rows, err := d.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to list configs: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			slog.Info("failed to close rows", "error", err)
		}
	}()

	var configs []model.Config
	for rows.Next() {
		cfg, err := scanConfig(rows)
		if err != nil {
			return nil, fmt.Errorf("failed to scan config: %w", err)
		}
		configs = append(configs, *cfg)
	}
	return configs, nil
}

// Delete removes a single config row, scoped to tenant-level or account-level
// based on whether accountID is empty.
func (d *ConfigDao) Delete(ctx context.Context, tenantID string, accountID *string, key string) error {
	var query string
	var args []any
	if accountID == nil || *accountID == "" {
		query = `DELETE FROM configs WHERE tenant_id = $1 AND account_id IS NULL AND key = $2`
		args = []any{tenantID, key}
	} else {
		query = `DELETE FROM configs WHERE tenant_id = $1 AND account_id = $2 AND key = $3`
		args = []any{tenantID, *accountID, key}
	}
	if _, err := d.db.ExecContext(ctx, query, args...); err != nil {
		return fmt.Errorf("failed to delete config: %w", err)
	}
	return nil
}

// rowScanner abstracts *sql.Row and *sql.Rows for shared scan logic.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanConfig(s rowScanner) (*model.Config, error) {
	var (
		id, k, v, t, ten, cb, ub string
		acc                      sql.NullString
		ca, ua                   time.Time
		labelsBytes              []byte
		metadataBytes            []byte
	)
	if err := s.Scan(&id, &k, &v, &t, &labelsBytes, &metadataBytes, &ten, &acc, &ca, &ua, &cb, &ub); err != nil {
		return nil, err
	}
	cfg := &model.Config{
		ID:        id,
		Key:       k,
		Value:     v,
		Type:      model.ConfigType(t),
		TenantID:  ten,
		CreatedAt: ca,
		UpdatedAt: ua,
		CreatedBy: cb,
		UpdatedBy: ub,
	}
	if acc.Valid && acc.String != "" {
		s := acc.String
		cfg.AccountID = &s
	}
	if len(labelsBytes) > 0 {
		if err := json.Unmarshal(labelsBytes, &cfg.Labels); err != nil {
			return nil, fmt.Errorf("failed to unmarshal labels: %w", err)
		}
	}
	if len(metadataBytes) > 0 {
		if err := json.Unmarshal(metadataBytes, &cfg.Metadata); err != nil {
			return nil, fmt.Errorf("failed to unmarshal metadata: %w", err)
		}
	}
	return cfg, nil
}

func joinAnd(clauses []string) string {
	out := ""
	for i, c := range clauses {
		if i > 0 {
			out += " AND "
		}
		out += c
	}
	return out
}

func joinOr(clauses []string) string {
	out := ""
	for i, c := range clauses {
		if i > 0 {
			out += " OR "
		}
		out += c
	}
	return out
}
