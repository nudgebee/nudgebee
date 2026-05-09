package config

import (
	"context"
	"fmt"
	"nudgebee/runbook/common"
	"nudgebee/runbook/internal/model"
	"nudgebee/runbook/internal/storage"
)

// ConfigService defines the interface for managing configurations.
//
// accountID is a pointer to support tenant-scoped operations: a nil/empty
// pointer means the operation targets the tenant-level row; a non-empty
// pointer targets the account-level row.
type ConfigService interface {
	SaveConfig(ctx context.Context, config model.Config) (string, error)
	GetConfig(ctx context.Context, tenantID string, accountID *string, key string, decrypt bool) (*model.Config, error)
	ListConfigs(ctx context.Context, tenantID string, accountID *string, labels map[string]string) ([]model.Config, error)
	ListConfigsDecrypted(ctx context.Context, tenantID string, accountID *string, labels map[string]string) ([]model.Config, error)
	DeleteConfig(ctx context.Context, tenantID string, accountID *string, key string) error
}

type Service struct {
	dao *storage.ConfigDao
}

const MaxConfigSize = 100 * 1024 // 100KB

func NewService() (*Service, error) {
	dao, err := storage.NewConfigDao()
	if err != nil {
		return nil, err
	}
	return &Service{dao: dao}, nil
}

func (s *Service) SaveConfig(ctx context.Context, config model.Config) (string, error) {
	if len(config.Value) > MaxConfigSize {
		return "", fmt.Errorf("config value too large: exceeds %d bytes", MaxConfigSize)
	}

	if config.Type == model.ConfigTypeSecret {
		encrypted, err := common.Encrypt(config.Value)
		if err != nil {
			return "", fmt.Errorf("failed to encrypt secret: %w", err)
		}
		config.Value = encrypted
	}
	return s.dao.Save(ctx, config)
}

// listScopeFor returns the scope for a List call:
//   - accountID empty/nil → tenant-level only
//   - accountID set       → tenant-level + that account, merged (account overrides on key)
func listScopeFor(accountID *string) storage.ListScope {
	if accountID == nil || *accountID == "" {
		return storage.ListScope{IncludeTenant: true}
	}
	return storage.ListScope{IncludeTenant: true, AccountID: accountID}
}

// mergeAccountOverTenant collapses (tenant, account) rows into the effective
// list a workflow would see for the given account: tenant rows first, account
// rows overwrite on duplicate keys.
func mergeAccountOverTenant(rows []model.Config) []model.Config {
	byKey := make(map[string]int, len(rows))
	out := make([]model.Config, 0, len(rows))
	for _, r := range rows {
		if idx, ok := byKey[r.Key]; ok {
			// Account rows always sort/win over tenant rows (tenant rows have nil AccountID).
			if !out[idx].IsTenantScoped() && r.IsTenantScoped() {
				continue
			}
			out[idx] = r
			continue
		}
		byKey[r.Key] = len(out)
		out = append(out, r)
	}
	return out
}

func (s *Service) GetConfig(ctx context.Context, tenantID string, accountID *string, key string, decrypt bool) (*model.Config, error) {
	// When account-scoped, prefer the account row but fall back to the tenant row.
	if accountID != nil && *accountID != "" {
		if cfg, err := s.dao.Get(ctx, tenantID, accountID, key); err != nil {
			return nil, err
		} else if cfg != nil {
			return s.applySecretMask(cfg, decrypt)
		}
	}
	cfg, err := s.dao.Get(ctx, tenantID, nil, key)
	if err != nil {
		return nil, err
	}
	if cfg == nil {
		return nil, nil
	}
	return s.applySecretMask(cfg, decrypt)
}

func (s *Service) applySecretMask(cfg *model.Config, decrypt bool) (*model.Config, error) {
	if cfg.Type != model.ConfigTypeSecret {
		return cfg, nil
	}
	if decrypt {
		decrypted, err := common.Decrypt(cfg.Value)
		if err != nil {
			return nil, fmt.Errorf("failed to decrypt secret: %w", err)
		}
		cfg.Value = decrypted
		return cfg, nil
	}
	cfg.Value = "*****"
	return cfg, nil
}

func (s *Service) ListConfigs(ctx context.Context, tenantID string, accountID *string, labels map[string]string) ([]model.Config, error) {
	configs, err := s.dao.List(ctx, tenantID, listScopeFor(accountID), labels)
	if err != nil {
		return nil, err
	}
	configs = mergeAccountOverTenant(configs)
	for i := range configs {
		if configs[i].Type == model.ConfigTypeSecret {
			configs[i].Value = "*****"
		}
	}
	return configs, nil
}

func (s *Service) ListConfigsDecrypted(ctx context.Context, tenantID string, accountID *string, labels map[string]string) ([]model.Config, error) {
	configs, err := s.dao.List(ctx, tenantID, listScopeFor(accountID), labels)
	if err != nil {
		return nil, err
	}
	configs = mergeAccountOverTenant(configs)
	for i := range configs {
		if configs[i].Type == model.ConfigTypeSecret {
			decrypted, err := common.Decrypt(configs[i].Value)
			if err != nil {
				return nil, fmt.Errorf("failed to decrypt secret for key %s: %w", configs[i].Key, err)
			}
			configs[i].Value = decrypted
		}
	}
	return configs, nil
}

func (s *Service) DeleteConfig(ctx context.Context, tenantID string, accountID *string, key string) error {
	return s.dao.Delete(ctx, tenantID, accountID, key)
}
