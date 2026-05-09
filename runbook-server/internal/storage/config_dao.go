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

func (d *ConfigDao) Save(ctx context.Context, config model.Config) (string, error) {
	if config.ID == "" {
		config.ID = uuid.New().String()
	}

	labelsBytes, err := json.Marshal(config.Labels)
	if err != nil {
		return "", fmt.Errorf("failed to marshal labels: %w", err)
	}

	// Marshal Metadata to JSON
	metadataBytes, err := json.Marshal(config.Metadata)
	if err != nil {
		return "", fmt.Errorf("failed to marshal metadata: %w", err)
	}

	query := `
		INSERT INTO configs (id, key, value, type, labels, metadata, tenant_id, account_id, created_by, updated_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT (account_id, key) DO UPDATE SET
			value = EXCLUDED.value,
			type = EXCLUDED.type,
			labels = EXCLUDED.labels,
            metadata = EXCLUDED.metadata,
			updated_at = NOW(),
			updated_by = EXCLUDED.updated_by
	`
	_, err = d.db.ExecContext(ctx, query, config.ID, config.Key, config.Value, config.Type, labelsBytes, metadataBytes, config.TenantID, config.AccountID, config.CreatedBy, config.UpdatedBy)
	if err != nil {
		return "", fmt.Errorf("failed to save config: %w", err)
	}
	return config.ID, nil
}

func (d *ConfigDao) Get(ctx context.Context, tenantID, accountID, key string) (*model.Config, error) {
	query := `
		SELECT id, key, value, type, labels, metadata, tenant_id, account_id, created_at, updated_at, created_by, updated_by
		FROM configs
		WHERE tenant_id = $1 AND account_id = $2 AND key = $3
	`
	var id, k, v, t, ten, acc, cb, ub string
	var ca, ua time.Time
	var labelsBytes []byte
	var metadataBytes []byte

	err := d.db.QueryRowContext(ctx, query, tenantID, accountID, key).Scan(&id, &k, &v, &t, &labelsBytes, &metadataBytes, &ten, &acc, &ca, &ua, &cb, &ub)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get config: %w", err)
	}

	config := &model.Config{
		ID:        id,
		Key:       k,
		Value:     v,
		Type:      model.ConfigType(t),
		TenantID:  ten,
		AccountID: acc,
		CreatedAt: ca,
		UpdatedAt: ua,
		CreatedBy: cb,
		UpdatedBy: ub,
	}

	if len(labelsBytes) > 0 {
		if err := json.Unmarshal(labelsBytes, &config.Labels); err != nil {
			return nil, fmt.Errorf("failed to unmarshal labels: %w", err)
		}
	}
	if len(metadataBytes) > 0 {
		if err := json.Unmarshal(metadataBytes, &config.Metadata); err != nil {
			return nil, fmt.Errorf("failed to unmarshal metadata: %w", err)
		}
	}

	return config, nil
}

func (d *ConfigDao) List(ctx context.Context, tenantID, accountID string, labels map[string]string) ([]model.Config, error) {
	query := `
		SELECT id, key, value, type, labels, metadata, tenant_id, account_id, created_at, updated_at, created_by, updated_by
		FROM configs
		WHERE tenant_id = $1 AND account_id = $2
	`
	args := []any{tenantID, accountID}
	argIdx := 3

	if len(labels) > 0 {
		labelsJSON, err := json.Marshal(labels)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal labels filter: %w", err)
		}
		query += fmt.Sprintf(" AND labels @> $%d", argIdx)
		args = append(args, labelsJSON)
	}

	query += " ORDER BY key ASC"

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
		var id, k, v, t, ten, acc, cb, ub string
		var ca, ua time.Time
		var labelsBytes []byte
		var metadataBytes []byte

		if err := rows.Scan(&id, &k, &v, &t, &labelsBytes, &metadataBytes, &ten, &acc, &ca, &ua, &cb, &ub); err != nil {
			return nil, fmt.Errorf("failed to scan config: %w", err)
		}

		config := model.Config{
			ID:        id,
			Key:       k,
			Value:     v,
			Type:      model.ConfigType(t),
			TenantID:  ten,
			AccountID: acc,
			CreatedAt: ca,
			UpdatedAt: ua,
			CreatedBy: cb,
			UpdatedBy: ub,
		}
		if len(labelsBytes) > 0 {
			if err := json.Unmarshal(labelsBytes, &config.Labels); err != nil {
				return nil, fmt.Errorf("failed to unmarshal labels: %w", err)
			}
		}
		if len(metadataBytes) > 0 {
			if err := json.Unmarshal(metadataBytes, &config.Metadata); err != nil {
				return nil, fmt.Errorf("failed to unmarshal metadata: %w", err)
			}
		}
		configs = append(configs, config)
	}
	return configs, nil
}

func (d *ConfigDao) Delete(ctx context.Context, tenantID, accountID, key string) error {
	query := `
		DELETE FROM configs
		WHERE tenant_id = $1 AND account_id = $2 AND key = $3
	`
	_, err := d.db.ExecContext(ctx, query, tenantID, accountID, key)
	if err != nil {
		return fmt.Errorf("failed to delete config: %w", err)
	}
	return nil
}
