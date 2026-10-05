package handlers

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"nudgebee/relay-server/pkg/db"
)

func TestBuildArgoCDFlagsFromConfig(t *testing.T) {
	const base = "--server $ARGOCD_SERVER --auth-token $ARGOCD_AUTH_TOKEN"
	tests := []struct {
		name   string
		values []db.WorkspaceConfigValue
		want   string
	}{
		{name: "insecure unset verifies certificates", values: nil, want: base},
		{name: "insecure false verifies certificates", values: []db.WorkspaceConfigValue{{Name: "insecure", Value: "false"}}, want: base},
		{name: "insecure true skips verification", values: []db.WorkspaceConfigValue{{Name: "insecure", Value: "true"}}, want: base + " --insecure"},
		{name: "insecure is case-insensitive", values: []db.WorkspaceConfigValue{{Name: "insecure", Value: "TRUE"}}, want: base + " --insecure"},
		{name: "timeout is not passed to the cli", values: []db.WorkspaceConfigValue{{Name: "timeout", Value: "60"}}, want: base},
		{name: "grpc web and config file", values: []db.WorkspaceConfigValue{{Name: "grpc_web", Value: "true"}, {Name: "config_file_path", Value: "/tmp/argocd.yaml"}}, want: base + " --grpc-web --config /tmp/argocd.yaml"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, buildArgoCDFlagsFromConfig(tt.values))
		})
	}
}
