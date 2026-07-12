// Package config holds the gateway service configuration, loaded from
// environment variables (and an optional .env file) via viper. It mirrors the
// llm-server config convention: a flat Configuration struct with mapstructure
// tags, one global Config value, populated in init().
package config

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/viper"
)

// SERVICE_NAME is the otel/telemetry service name for the gateway.
const SERVICE_NAME = "llm-gateway"

// MeteringSink backends.
const (
	SinkPostgres   = "postgres"
	SinkClickhouse = "clickhouse"
)

type Configuration struct {
	Port     string `mapstructure:"port"`
	LogLevel string `mapstructure:"log_level"`

	// MaxRequestBodyBytes caps the inbound request body the edge will read, to
	// bound memory. Generous by default (LLM payloads carry images/PDFs); a client
	// exceeding it gets 413. Streaming responses are not affected.
	MaxRequestBodyBytes int64 `mapstructure:"max_request_body_bytes"`
	// ReadHeaderTimeoutSeconds bounds how long the server waits for request
	// headers (Slowloris guard). Does not bound body upload or streaming.
	ReadHeaderTimeoutSeconds int `mapstructure:"read_header_timeout_seconds"`

	// Per-provider API keys for the key-based providers — set any subset to serve
	// them simultaneously (the passthrough routes by endpoint, so one key each).
	// For a cloud provider needing structured creds (Bedrock/Vertex/Azure), use the
	// LLM_PROVIDER_* block below instead.
	AnthropicAPIKey string `mapstructure:"gateway_anthropic_api_key"`
	OpenAIAPIKey    string `mapstructure:"gateway_openai_api_key"`
	GeminiAPIKey    string `mapstructure:"gateway_gemini_api_key"`

	// Operator default / cloud provider credential — mirrors llm-server's
	// LLM_PROVIDER_* env convention. Use for a single provider or a cloud provider
	// (Bedrock/Vertex/Azure) needing structured creds; the per-provider keys above
	// take precedence for the key-based providers. Cloud fields (access/secret/
	// session) feed BedrockKeyConfig; api_key feeds the Value for api-key providers.
	LlmProvider             string `mapstructure:"llm_provider"`
	LlmProviderApiKey       string `mapstructure:"llm_provider_api_key"`
	LlmProviderApiEndpoint  string `mapstructure:"llm_provider_api_endpoint"`
	LlmProviderRegion       string `mapstructure:"llm_provider_region"`
	LlmProviderAccessKey    string `mapstructure:"llm_provider_access_key"`
	LlmProviderSecretKey    string `mapstructure:"llm_provider_secret_key"`
	LlmProviderSessionToken string `mapstructure:"llm_provider_session_token"`

	// Metastore (Postgres): virtual keys, identity, pricing catalog.
	GatewayDBURL        string `mapstructure:"gateway_db_url"`
	GatewayDBMaxConns   int    `mapstructure:"gateway_db_max_connections"`
	GatewayDBMinConns   int    `mapstructure:"gateway_db_min_connections"`
	GatewayDBIdleMinute int    `mapstructure:"gateway_db_idle_minutes"`

	// Shared NB AES key (hex) used to decrypt per-tenant provider secrets stored in
	// integration_config_values. Same key/scheme as llm-server (NUDGEBEE_ENCRYPTION_KEY).
	NudgebeeEncryptionKey string `mapstructure:"nudgebee_encryption_key"`

	// Service token for the read-only usage query plane (POST /rpc/usage/*), sent by
	// the app's RPC gateway as X-ACTION-TOKEN. Separate from the NB-PAT passthrough auth.
	// OPTIONAL: enforced when set; when unset the plane stays open (internal endpoint,
	// gated by the RPC permission layer + network isolation).
	GatewayActionToken string `mapstructure:"gateway_action_token"`

	// Metering sink — pluggable: "postgres" (default) or "clickhouse".
	MeteringSink  string `mapstructure:"gateway_metering_sink"`
	ClickhouseURL string `mapstructure:"clickhouse_url"`

	// Cache / Redis — mirrors llm-server's naming so common/cache.go lifts cleanly.
	// Used for distributed rate limiting/budgets (task 5). cache_provider selects
	// "redis" (distributed, multi-replica) or "inmemory" (single-pod dev).
	CacheProvider           string `mapstructure:"cache_provider"`
	CacheExpirationMinutes  int    `mapstructure:"cache_expiration_minutes"`
	CacheInMemorySizeMb     int    `mapstructure:"cache_inmemory_size_mb"`
	CacheInMemoryMaxEntries int    `mapstructure:"cache_inmemory_max_entries"`
	CacheRedisUserName      string `mapstructure:"redis_user_name"`
	CacheRedisUserPassword  string `mapstructure:"redis_user_password"`
	CacheRedisServerHost    string `mapstructure:"redis_server_host"`
	CacheRedisServerPort    int    `mapstructure:"redis_server_port"`

	// Auth. Mode: "user_auths" (real NB PAT lookup via the sha256 column — the
	// SECURE DEFAULT), "static" (single configured token → the Static* identity, an
	// ASSUMPTION for controlled setups), or "none" (NO auth, identity empty — an
	// open proxy to the provider keys; permitted only for local dev and only when
	// AuthAllowInsecure is explicitly set). The gateway authenticates itself because
	// it receives requests directly from ingress (no Next.js in front).
	// Identity grain for now is tenant + user (account_id kept nullable in the
	// metering row for possible account-level attribution later).
	AuthMode       string `mapstructure:"gateway_auth_mode"`
	StaticToken    string `mapstructure:"gateway_static_token"`
	StaticTenantID string `mapstructure:"gateway_static_tenant_id"`
	StaticUserID   string `mapstructure:"gateway_static_user_id"`
	// AuthAllowInsecure must be true to boot with AuthMode="none". It exists so the
	// insecure open-proxy mode can only be entered by a conscious act, never by an
	// accidental/empty config on an internet-facing deploy.
	AuthAllowInsecure bool `mapstructure:"gateway_auth_allow_insecure"`

	// Body logging (DATA-CUSTODY SENSITIVE; OFF by default). When enabled, the full
	// request + response bodies are stored (capped at BodyMaxBytes) in
	// llm_gateway_request_log with a TTL, then periodically SOFT-deleted by a
	// background cleanup job. Per-tenant gating is a later refinement.
	CaptureBody             bool  `mapstructure:"gateway_capture_body"`
	BodyMaxBytes            int64 `mapstructure:"gateway_body_max_bytes"`
	BodyTTLHours            int   `mapstructure:"gateway_body_ttl_hours"`
	BodyCleanupIntervalMins int   `mapstructure:"gateway_body_cleanup_interval_minutes"`

	// Capture — session correlation + structure-only attributes.
	// SessionHeader: request header a client sets to group requests into a
	// session/round (fallback: body metadata.user_id). CaptureAttributes: extract
	// structure-only attributes (tool names, params, stop_reason) — NOT message
	// content. Full request/response BODY logging is a separate, off-by-default,
	// per-tenant feature (not this flag).
	SessionHeader     string `mapstructure:"gateway_session_header"`
	CaptureAttributes bool   `mapstructure:"gateway_capture_attributes"`

	// Routing. Path to a JSON routing-rules config file (global/default rules);
	// empty = passthrough only. Per-tenant rules come from the metastore table
	// llm_gateway_routing_rules, refreshed every RoutingRefreshSeconds.
	RoutingConfig         string `mapstructure:"gateway_routing_config"`
	RoutingRefreshSeconds int    `mapstructure:"gateway_routing_refresh_seconds"`

	// OpenTelemetry.
	OtelTracesExporter              string `mapstructure:"otel_traces_exporter"`
	OtelMetricsExporter             string `mapstructure:"otel_metrics_exporter"`
	OtelExporterOtlpTracesEndpoint  string `mapstructure:"otel_exporter_otlp_traces_endpoint"`
	OtelExporterOtlpMetricsEndpoint string `mapstructure:"otel_exporter_otlp_metrics_endpoint"`
	OtelGrpcTimeoutSeconds          int    `mapstructure:"otel_grpc_timeout_seconds"`
	OtelGrpcMaxMsgSize              int    `mapstructure:"otel_grpc_max_msg_size"`
}

// Config is the process-wide configuration, populated in init().
var Config Configuration

// keyDefaults lists every config key with its default. Every key is also
// BindEnv'd so environment variables override defaults even through Unmarshal
// (viper's AutomaticEnv alone does not feed Unmarshal reliably).
var keyDefaults = map[string]any{
	"port":                                  "8000",
	"log_level":                             "info",
	"max_request_body_bytes":                33554432, // 32 MiB
	"read_header_timeout_seconds":           15,
	"gateway_anthropic_api_key":             "",
	"gateway_openai_api_key":                "",
	"gateway_gemini_api_key":                "",
	"llm_provider":                          "anthropic",
	"llm_provider_api_key":                  "",
	"llm_provider_api_endpoint":             "",
	"llm_provider_region":                   "us-west-2",
	"llm_provider_access_key":               "",
	"llm_provider_secret_key":               "",
	"llm_provider_session_token":            "",
	"gateway_db_url":                        "",
	"gateway_db_max_connections":            20,
	"gateway_db_min_connections":            2,
	"gateway_db_idle_minutes":               5,
	"gateway_metering_sink":                 SinkPostgres,
	"nudgebee_encryption_key":               "",
	"gateway_action_token":                  "",
	"clickhouse_url":                        "",
	"cache_provider":                        "inmemory",
	"cache_expiration_minutes":              10,
	"cache_inmemory_size_mb":                100,
	"cache_inmemory_max_entries":            10000,
	"redis_user_name":                       "",
	"redis_user_password":                   "",
	"redis_server_host":                     "",
	"redis_server_port":                     6379,
	"gateway_virtual_key_header":            "Authorization",
	"gateway_auth_mode":                     "user_auths",
	"gateway_auth_allow_insecure":           false,
	"gateway_static_token":                  "",
	"gateway_static_tenant_id":              "",
	"gateway_static_user_id":                "",
	"gateway_session_header":                "x-nb-session-id",
	"gateway_capture_attributes":            true,
	"gateway_routing_config":                "",
	"gateway_routing_refresh_seconds":       30,
	"gateway_capture_body":                  false,
	"gateway_body_max_bytes":                1048576, // 1 MiB per body
	"gateway_body_ttl_hours":                168,     // 7 days
	"gateway_body_cleanup_interval_minutes": 60,
	"otel_traces_exporter":                  "",
	"otel_metrics_exporter":                 "",

	"otel_exporter_otlp_traces_endpoint":  "",
	"otel_exporter_otlp_metrics_endpoint": "",
	"otel_grpc_timeout_seconds":           10,
	"otel_grpc_max_msg_size":              16777216,
}

func init() {
	// Load a .env from the working directory if present (dev convenience). An
	// explicit SetConfigFile bypasses AddConfigPath search, which is what we want:
	// deployed environments have no .env and rely on env vars + defaults below.
	viper.SetConfigFile(".env")
	viper.SetConfigType("dotenv")
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	viper.AutomaticEnv()

	for key, def := range keyDefaults {
		viper.SetDefault(key, def)
		// Bind the UPPERCASE env var explicitly so Unmarshal honors it.
		_ = viper.BindEnv(key, strings.ToUpper(key))
	}

	// init() runs before main() configures the slog default handler, so log the
	// optional-.env notice and any fatal straight to stderr to avoid inconsistent
	// formatting. A .env file is optional in deployed environments (env vars win).
	if err := viper.ReadInConfig(); err != nil {
		fmt.Fprintf(os.Stderr, "config: no .env file loaded (using env + defaults): %v\n", err)
	}

	if err := viper.Unmarshal(&Config); err != nil {
		fmt.Fprintf(os.Stderr, "config: failed to unmarshal configuration: %v\n", err)
		os.Exit(1)
	}
}
