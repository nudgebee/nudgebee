package config

import (
	"context"
	"fmt"
	"nudgebee/runbook/common"
	"nudgebee/runbook/internal/model"
	"nudgebee/runbook/internal/storage"
)

// ConfigService defines the interface for managing configurations.
type ConfigService interface {
	SaveConfig(ctx context.Context, config model.Config) (string, error)
	GetConfig(ctx context.Context, tenantID, accountID, key string, decrypt bool) (*model.Config, error)
	ListConfigs(ctx context.Context, tenantID, accountID string, labels map[string]string) ([]model.Config, error)
	ListConfigsDecrypted(ctx context.Context, tenantID, accountID string, labels map[string]string) ([]model.Config, error)
	DeleteConfig(ctx context.Context, tenantID, accountID, key string) error
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

func (s *Service) GetConfig(ctx context.Context, tenantID, accountID, key string, decrypt bool) (*model.Config, error) {
	config, err := s.dao.Get(ctx, tenantID, accountID, key)
	if err != nil {
		return nil, err
	}
	if config == nil {
		return nil, nil
	}

	if config.Type == model.ConfigTypeSecret {
		if decrypt {
			decrypted, err := common.Decrypt(config.Value)
			if err != nil {
				return nil, fmt.Errorf("failed to decrypt secret: %w", err)
			}
			config.Value = decrypted
		} else {
			config.Value = "*****" // Mask for safety
		}
	}
	return config, nil
}

func (s *Service) ListConfigs(ctx context.Context, tenantID, accountID string, labels map[string]string) ([]model.Config, error) {
	configs, err := s.dao.List(ctx, tenantID, accountID, labels)
	if err != nil {
		return nil, err
	}
	for i := range configs {
		if configs[i].Type == model.ConfigTypeSecret {
			configs[i].Value = "*****" // Always mask in list
		}
	}
	return configs, nil
}

func (s *Service) ListConfigsDecrypted(ctx context.Context, tenantID, accountID string, labels map[string]string) ([]model.Config, error) {
	configs, err := s.dao.List(ctx, tenantID, accountID, labels)
	if err != nil {
		return nil, err
	}
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

func (s *Service) DeleteConfig(ctx context.Context, tenantID, accountID, key string) error {
	return s.dao.Delete(ctx, tenantID, accountID, key)
}
