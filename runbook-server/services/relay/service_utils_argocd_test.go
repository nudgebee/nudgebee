package relay

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"nudgebee/runbook/services/integrations"
)

func TestBuildArgoCDFlags(t *testing.T) {
	const base = "--server $ARGOCD_SERVER --auth-token $ARGOCD_AUTH_TOKEN"
	tests := []struct {
		name   string
		values []integrations.IntegrationConfigValue
		want   string
	}{
		{name: "insecure unset verifies certificates", values: nil, want: base},
		{name: "insecure false verifies certificates", values: []integrations.IntegrationConfigValue{{Name: "insecure", Value: "false"}}, want: base},
		{name: "insecure true skips verification", values: []integrations.IntegrationConfigValue{{Name: "insecure", Value: "true"}}, want: base + " --insecure"},
		{name: "insecure is case-insensitive", values: []integrations.IntegrationConfigValue{{Name: "insecure", Value: "TRUE"}}, want: base + " --insecure"},
		{name: "timeout is not passed to the cli", values: []integrations.IntegrationConfigValue{{Name: "timeout", Value: "60"}}, want: base},
		{name: "grpc web and config file", values: []integrations.IntegrationConfigValue{{Name: "grpc_web", Value: "true"}, {Name: "config_file_path", Value: "/tmp/argocd.yaml"}}, want: base + " --grpc-web --config /tmp/argocd.yaml"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, buildArgoCDFlags(tt.values))
		})
	}
}
