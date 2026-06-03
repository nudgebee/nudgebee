package providers

import (
	"context"
	"log/slog"
	"nudgebee/collector/cloud/security"
	"strings"
)

type cloudProviderContext struct {
	logger *slog.Logger
	ctx    context.Context
}

func (c *cloudProviderContext) GetContext() context.Context {
	return c.ctx
}

func (c *cloudProviderContext) GetLogger() *slog.Logger {
	return c.logger
}

func (c *cloudProviderContext) GetSecurityContext() *security.SecurityContext {
	return nil
}

func NewCloudProviderContext(ctx context.Context) CloudProviderContext {
	if ctx == nil {
		ctx = context.Background()
	}
	return &cloudProviderContext{
		logger: slog.Default(),
		ctx:    ctx,
	}
}

// NewCloudProviderContextWithLogger creates a CloudProviderContext with a custom logger
func NewCloudProviderContextWithLogger(ctx context.Context, logger *slog.Logger) CloudProviderContext {
	if ctx == nil {
		ctx = context.Background()
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &cloudProviderContext{
		logger: logger,
		ctx:    ctx,
	}
}

var providers = make(map[string]CloudProvider)

func RegisterProvider(provider CloudProvider) {
	providers[strings.ToLower(provider.Name())] = provider
}

func GetProvider(name string) (CloudProvider, bool) {
	provider, ok := providers[strings.ToLower(name)]
	return provider, ok
}
