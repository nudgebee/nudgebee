package integrations

import (
	"context"
	"fmt"
	"strings"
	"time"

	"nudgebee/services/integrations/core"
	"nudgebee/services/integrations/orionclient"
	"nudgebee/services/security"
)

func init() {
	core.RegisterIntegration(Orion{})
}

const (
	IntegrationOrion = "orion"

	OrionURLKey            = "orion_url"
	OrionUsernameKey       = "username"
	OrionPasswordKey       = "password"
	OrionTLSSkipVerifyKey  = "tls_skip_verify"
	OrionConnectionModeKey = "connection_mode"
	OrionDatasourceKey     = "datasource_key"

	// Orion is reachable from Nudgebee directly, or only from inside the customer's
	// network through a forager. The values match the ones Prometheus, Loki and
	// SigNoz already use so core.IsForagerRegisteredDatasource recognises the row.
	OrionConnectionModeDirect  = "direct"
	OrionConnectionModeVMAgent = "vm_agent"

	// SWIS serves its REST API on 17774 from SolarWinds Platform 2023.1. Older
	// installs answer on 17778, which that release deprecated.
	OrionDefaultSWISPort = 17774
)

// Orion is the self-hosted SolarWinds Platform (formerly Orion Platform, sold today
// as SolarWinds Observability Self-Hosted). It is a different product from the
// `solarwinds` integration, which is the SaaS one: this reads SWIS, a SWQL (SQL
// dialect) endpoint, while the SaaS speaks a REST metrics API.
//
// Scope is hosts and network devices. Orion's Kubernetes container monitoring is
// deliberately not wired up: its whole schema is five Orion.Cman.* entities holding
// per-container CPU and memory, with no namespace, pod, workload, request or limit
// column anywhere (verified against the SWIS schema on 2026.4.0), so a container's
// numbers cannot be attached to a workload and the utilisation screens have nothing
// to render. See nb-orion-observability-spec.md.
type Orion struct{}

func (m Orion) Name() string {
	return IntegrationOrion
}

func (m Orion) Category() core.IntegrationCategory {
	return core.IntegrationCategoryMetrics
}

func (m Orion) ConfigSchema() core.IntegrationSchema {
	return core.IntegrationSchema{
		Type:     core.ToolSchemaTypeObject,
		Required: []string{OrionURLKey, OrionUsernameKey, OrionPasswordKey},
		Testable: true,
		Properties: map[string]core.IntegrationSchemaProperty{
			core.IntegrationConfigName: {
				Type:        core.ToolSchemaTypeString,
				Description: "Name of SolarWinds Platform (Orion) integration",
				Priority:    100,
			},
			core.AccountId: {
				Type:             core.ToolSchemaTypeArray,
				Description:      "Select Account",
				Default:          "",
				AutoGenerateFunc: "listAccounts",
				Priority:         95,
			},
			OrionConnectionModeKey: {
				Type: core.ToolSchemaTypeString,
				Description: "How Nudgebee reaches the Orion server: directly from Nudgebee, or through a " +
					"forager running in your network (on-prem Orion is usually only reachable that way)",
				Default:    OrionConnectionModeDirect,
				Enum:       []any{OrionConnectionModeDirect, OrionConnectionModeVMAgent},
				Priority:   92,
				IsTestable: true,
			},
			OrionURLKey: {
				Type: core.ToolSchemaTypeString,
				Description: "Base URL of the SolarWinds Information Service, including the port " +
					"(e.g. https://orion.example.com:17774). Port 17774 serves the API from SolarWinds " +
					"Platform 2023.1; older installs use 17778.",
				Priority:   85,
				IsTestable: true,
			},
			OrionUsernameKey: {
				Type:        core.ToolSchemaTypeString,
				Description: "Orion account used to query SWIS. A read-only account is enough.",
				Priority:    80,
				IsTestable:  true,
			},
			OrionPasswordKey: {
				Type:        core.ToolSchemaTypeString,
				Description: "Password for that Orion account",
				IsEncrypted: true,
				Priority:    78,
				IsTestable:  true,
			},
			OrionTLSSkipVerifyKey: {
				Type: core.ToolSchemaTypeBoolean,
				Description: "Skip TLS certificate verification. SWIS ships with a self-signed certificate, " +
					"so most on-prem installs need this until the certificate is replaced.",
				Default:    false,
				Priority:   30,
				IsTestable: true,
			},
			OrionDatasourceKey: {
				Type:        core.ToolSchemaTypeString,
				Description: "Forager datasource id",
				Hidden:      true,
				Priority:    10,
			},
			// No DefaultLogProvider: Orion's log tables (Orion.OLM.*) need the
			// separately licensed Log Analyzer module, which is not read yet.
			// No DefaultTraceProvider: self-hosted Orion has no distributed tracing.
			core.DefaultMetricsProvider: {
				Type:             core.ToolSchemaTypeBoolean,
				Description:      "Make SolarWinds Platform (Orion) default Metric Provider",
				Default:          false,
				AutoGenerateFunc: "",
				Priority:         24,
			},
		},
	}
}

// ValidateConfig gates saving, not just the Test button, the way Prometheus and
// SigNoz do. Values arrive decrypted.
func (m Orion) ValidateConfig(_ *security.SecurityContext, config []core.IntegrationConfigValue, _ string) []error {
	values := make(map[string]string, len(config))
	for _, c := range config {
		values[c.Name] = c.Value
	}

	var errs []error
	if strings.TrimSpace(values[OrionUsernameKey]) == "" {
		errs = append(errs, fmt.Errorf("%s is required", OrionUsernameKey))
	}
	if values[OrionPasswordKey] == "" {
		errs = append(errs, fmt.Errorf("%s is required", OrionPasswordKey))
	}

	rawURL := strings.TrimSpace(values[OrionURLKey])
	if rawURL == "" {
		errs = append(errs, fmt.Errorf("%s is required", OrionURLKey))
		return errs
	}

	// A forager-reached Orion lives on a private address, so neither the egress
	// check that rejects private targets nor a live probe can run here: the
	// pre-save connectivity test goes through the forager, the way Prometheus's
	// vm_agent mode does.
	if values[OrionConnectionModeKey] == OrionConnectionModeVMAgent {
		if _, err := orionclient.JoinQueryPath(rawURL); err != nil {
			errs = append(errs, fmt.Errorf("%s %w", OrionURLKey, err))
		}
		return errs
	}

	if err := validateEgressURL(rawURL); err != nil {
		errs = append(errs, fmt.Errorf("%s %w", OrionURLKey, err))
	}
	if len(errs) > 0 {
		return errs
	}

	return probeOrion(NewOrionConfig(values))
}

// NewOrionConfig builds a SWIS client config from stored (decrypted) values.
// The forager transport is attached by the caller that owns the account, since
// only it knows the datasource key.
func NewOrionConfig(values map[string]string) orionclient.Config {
	return orionclient.Config{
		BaseURL:       strings.TrimSpace(values[OrionURLKey]),
		Username:      strings.TrimSpace(values[OrionUsernameKey]),
		Password:      values[OrionPasswordKey],
		TLSSkipVerify: values[OrionTLSSkipVerifyKey] == "true",
		ViaForager:    values[OrionConnectionModeKey] == OrionConnectionModeVMAgent,
	}
}

// probeOrion runs the cheapest real query there is. It proves the URL points at
// SWIS, the credentials work, and the account can read the node table every
// other query here starts from.
func probeOrion(cfg orionclient.Config) []error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if _, err := cfg.Query(ctx, "SELECT TOP 1 NodeID FROM Orion.Nodes", nil); err != nil {
		return []error{err}
	}
	return nil
}
