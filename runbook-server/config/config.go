package config

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/viper"
)

const SERVICE_NAME = "runbook-server"

var Config appConfig

type appConfig struct {
	APIPort string `mapstructure:"API_PORT"`

	RunbookServerDBUrl           string `mapstructure:"auto_pilot_database_url"`
	RunbookServerDBMaxConnection int    `mapstructure:"runbook_server_db_max_connection"`
	RunbookServerDBMinConnection int    `mapstructure:"runbook_server_db_min_connection"`
	RunbookServerDBIdleMinutes   int    `mapstructure:"runbook_server_db_idle_minutes"`

	TemporalGRPCAddress string `mapstructure:"TEMPORAL_GRPC_ENDPOINT"`
	RunIntegrationTests string `mapstructure:"RUN_INTEGRATION_TESTS"`

	NudgebeeEncryptionKey string `mapstructure:"nudgebee_encryption_key"`

	ServiceApiServerToken          string `mapstructure:"action_api_server_token"`
	ServiceApiServerTokenHeader    string `mapstructure:"action_api_server_token_header"`
	ServiceEndpoint                string `mapstructure:"service_api_server_url"`
	ServiceApiServerTimeoutSeconds int    `mapstructure:"service_api_server_timeout_seconds"`

	BaseUrl string `mapstructure:"base_url"`

	ApprovalSigningKey string `mapstructure:"approval_signing_key"`

	RelayServerEndpoint  string `mapstructure:"relay_server_endpoint"`
	RelayServerSecretKey string `mapstructure:"relay_server_secret_key"`

	TicketServerEndpoint string `mapstructure:"ticket_service_url"`

	OtelServiceName          string `mapstructure:"otel_service_name"`
	OtelExporterOtlpEndpoint string `mapstructure:"otel_exporter_otlp_endpoint"`

	OtelExporter                   string `mapstructure:"otel_exporter"`
	OtelTracesExporter             string `mapstructure:"otel_traces_exporter"`
	OtelExporterOtlpTracesEndpoint string `mapstructure:"otel_exporter_otlp_traces_endpoint"`

	OtelMetricesExporter            string `mapstructure:"otel_metrics_exporter"`
	OtelExporterOtlpMetricsEndpoint string `mapstructure:"otel_exporter_otlp_metrics_endpoint"`
	OtelLogsExporter                string `mapstructure:"otel_logs_exporter"`

	OtelGrpcTimeoutSeconds int `mapstructure:"otel_grpc_timeout_seconds"`
	OtelGrpcMaxMsgSize     int `mapstructure:"otel_grpc_max_msg_size"`

	ServerHeartBeatFrequncySecond int `mapstructure:"server_heartbeat_frequency_second"`
	ServerHeartBeatTimeoutSecond  int `mapstructure:"server_heartbeat_timeout_second"`

	CloudCollectorServerUrl   string `mapstructure:"cloud_collector_server_url"`
	CloudCollectorServerToken string `mapstructure:"cloud_collector_server_token"`

	RabbitMqUsername string `mapstructure:"rabbit_mq_username"`
	RabbitMqPassword string `mapstructure:"rabbit_mq_password"`
	RabbitMqHost     string `mapstructure:"rabbit_mq_host"`
	RabbitMqPort     int    `mapstructure:"rabbit_mq_port"`

	RabbitMqRunbookEventExchange   string `mapstructure:"rabbit_mq_runbook_event_exchange"`
	RabbitMqRunbookEventQueue      string `mapstructure:"rabbit_mq_runbook_event_queue"`
	RabbitMqRunbookEventRoutingKey string `mapstructure:"rabbit_mq_runbook_event_routing_key"`

	// Outbound: the llm.event_investigate task publishes here to kick off
	// an investigation in llm-server, attaching a Temporal task_token so
	// the result can be routed back to the suspended activity.
	RabbitMqTroubleshootExchange   string `mapstructure:"rabbit_mq_troubleshoot_exchange"`
	RabbitMqTroubleshootRoutingKey string `mapstructure:"rabbit_mq_troubleshoot_routing_key"`

	// Inbound: llm-server publishes here when an investigation that
	// originated from a workflow activity completes. The completion
	// consumer reads task_token + summary and resumes the suspended
	// activity via CompleteActivity.
	RabbitMqEventInvestigateCompletedExchange   string `mapstructure:"rabbit_mq_event_investigate_completed_exchange"`
	RabbitMqEventInvestigateCompletedQueue      string `mapstructure:"rabbit_mq_event_investigate_completed_queue"`
	RabbitMqEventInvestigateCompletedRoutingKey string `mapstructure:"rabbit_mq_event_investigate_completed_routing_key"`

	LlmServerUrl         string `mapstructure:"llm_server_url"`
	LlmServerToken       string `mapstructure:"llm_server_token"`
	LlmServerTokenHeader string `mapstructure:"llm_server_token_header"`

	MlServerUrl string `mapstructure:"ml_server_url"`

	NotificationServerUrl string `mapstructure:"notification_service_url"`

	RunbookServerRelayCommandExecutionTimeoutSeconds int `mapstructure:"runbook_server_relay_command_execution_timeout_seconds"`
	RunbookServerRelayPodExecutionTimeoutSeconds     int `mapstructure:"runbook_server_relay_pod_execution_timeout_seconds"`

	RunbookServerLlmRetryAttempts         int `mapstructure:"runbook_server_llm_retry_attempts"`
	RunbookServerLlmInitialBackoffSeconds int `mapstructure:"runbook_server_llm_initial_backoff_seconds"`
	RunbookServerLlmMaxRetryDuration      time.Duration

	CacheProvider           string `mapstructure:"cache_provider"`
	CacheExpirationMinutes  int    `mapstructure:"cache_expiration_minutes"`
	CacheInMemorySizeMb     int    `mapstructure:"cache_inmemory_size_mb"`
	CacheInMemoryMaxEntries int    `mapstructure:"cache_inmemory_max_entries"`
	CacheRedisUserName      string `mapstructure:"redis_user_name"`
	CacheRedisUserPassword  string `mapstructure:"redis_user_password"`
	CacheRedisServerHost    string `mapstructure:"redis_server_host"`
	CacheRedisServerPort    int    `mapstructure:"redis_server_port"`

	LlmServerShellImage           string `mapstructure:"llm_server_tool_shell_image"`
	ScriptExecutorNodeImage       string `mapstructure:"script_executor_node_image"`
	ScriptExecutorPowerShellImage string `mapstructure:"script_executor_powershell_image"`

	ServerName string `mapstructure:"runbook_server_name"`

	TaskScriptExecutionModel              string `mapstructure:"runbook_server_task_scripting_mode"`
	RunbookServerEventSyncIntervalSeconds int    `mapstructure:"runbook_server_event_sync_interval_seconds"`
	RunbookServerTemporalQueue            string `mapstructure:"runbook_server_temporal_queue"`

	NudgebeeNamespace string `mapstructure:"nudgebee_namespace"`

	OptimizationEnabled                           bool `mapstructure:"runbook_server_optimization_enabled"`
	OptimizationRecommendationPollIntervalSeconds int  `mapstructure:"runbook_server_optimization_poll_interval_seconds"`
}

func init() {
	viper.SetDefault("API_PORT", "8000")
	viper.SetDefault("TEMPORAL_GRPC_ENDPOINT", "localhost:7233")
	viper.SetDefault("RUN_INTEGRATION_TESTS", "false")
	viper.SetDefault("OTEL_TRACES_EXPORTER", "noop")
	viper.SetDefault("AUTO_PILOT_DATABASE_URL", "postgres://temporal:temporal@localhost:5432/temporal?sslmode=disable")
	viper.SetDefault("runbook_server_optimization_enabled", false)
	viper.SetDefault("runbook_server_optimization_poll_interval_seconds", 180)

	viper.SetDefault("nudgebee_encryption_key", "")

	viper.SetDefault("action_api_server_token_header", "X-ACTION-TOKEN")
	viper.SetDefault("llm_server_token_header", "X-ACTION-TOKEN")
	viper.SetDefault("llm_server_token", "")

	viper.SetDefault("env", "")
	viper.SetDefault("service_api_server_url", "http://services-server:8000")
	viper.SetDefault("service_api_server_timeout_seconds", "10")

	// viper requires default values or bind.. else Unmarshal skips fields with no default values
	viper.SetDefault("action_api_server_token", "")
	viper.SetDefault("base_url", "https://nudgebee.com")
	viper.SetDefault("approval_signing_key", "")

	viper.SetDefault("relay_server_endpoint", "http://localhost:52832")
	viper.SetDefault("relay_server_secret_key", "default")

	viper.SetDefault("otel_service_name", SERVICE_NAME)
	viper.SetDefault("otel_provider", "noop")

	viper.SetDefault("runbook_server_db_max_connection", 10)
	viper.SetDefault("runbook_server_db_min_connection", 1)
	viper.SetDefault("runbook_server_db_idle_minutes", 10)

	viper.SetDefault("otel_service_name", SERVICE_NAME)
	viper.SetDefault("otel_exporter", "noop")
	viper.SetDefault("otel_exporter_otlp_endpoint", "127.0.0.1:4317")
	viper.SetDefault("otel_grpc_timeout_seconds", 5)
	viper.SetDefault("otel_grpc_max_msg_size", 8*1024*1024)

	viper.SetDefault("server_heartbeat_frequency_second", 15)
	viper.SetDefault("server_heartbeat_timeout_second", 30)

	viper.SetDefault("CLOUD_COLLECTOR_SERVER_URL", "http://localhost:8000")
	viper.SetDefault("CLOUD_COLLECTOR_SERVER_TOKEN", "")

	viper.SetDefault("TICKET_SERVICE_URL", "http://localhost:9097")
	viper.SetDefault("LLM_SERVER_URL", "http://localhost:8000")
	viper.SetDefault("ML_SERVER_URL", "http://ml-k8s-server:8000")

	viper.SetDefault("rabbit_mq_username", "user")
	viper.SetDefault("rabbit_mq_password", "password")
	viper.SetDefault("rabbit_mq_host", "localhost")
	viper.SetDefault("rabbit_mq_port", 5672)

	viper.SetDefault("rabbit_mq_runbook_event_exchange", "runbook_event_process")
	viper.SetDefault("rabbit_mq_runbook_event_queue", "runbook_event_process")
	viper.SetDefault("rabbit_mq_runbook_event_routing_key", "troubleshooting")

	viper.SetDefault("rabbit_mq_troubleshoot_exchange", "llm_server_event_investigate")
	viper.SetDefault("rabbit_mq_troubleshoot_routing_key", "llm_server_event_investigate")

	viper.SetDefault("rabbit_mq_event_investigate_completed_exchange", "llm_server_event_investigate_completed")
	viper.SetDefault("rabbit_mq_event_investigate_completed_queue", "runbook_server_event_investigate_completed")
	viper.SetDefault("rabbit_mq_event_investigate_completed_routing_key", "llm_server_event_investigate_completed")

	viper.SetDefault("LLM_SERVER_TOOL_SHELL_IMAGE", "registry.nudgebee.com/nudgebee-debug:0.3.9")
	viper.SetDefault("SCRIPT_EXECUTOR_NODE_IMAGE", "node:22-alpine")
	viper.SetDefault("SCRIPT_EXECUTOR_POWERSHELL_IMAGE", "mcr.microsoft.com/powershell:lts-alpine-3.17")

	viper.SetDefault("notification_service_url", "http://notifications:8080")

	viper.SetDefault("runbook_server_llm_retry_attempts", 180)
	viper.SetDefault("runbook_server_llm_initial_backoff_seconds", 5)
	viper.SetDefault("runbook_server_relay_command_execution_timeout_seconds", 120)
	viper.SetDefault("runbook_server_relay_pod_execution_timeout_seconds", 120)

	viper.SetDefault("cache_provider", "in_memory")
	viper.SetDefault("cache_expiration_minutes", 30)
	viper.SetDefault("cache_inmemory_size_mb", 20)
	viper.SetDefault("cache_inmemory_max_entries", 1000)

	viper.SetDefault("runbook_server_event_sync_interval_seconds", 30)

	viper.SetDefault("runbook_server_task_scripting_mode", "agent")
	viper.SetDefault("runbook_server_temporal_queue", "runbook-tasks")

	viper.SetConfigName("config")
	viper.SetConfigFile(".env")
	viper.SetConfigType("dotenv")
	viper.AddConfigPath(".")
	viper.AddConfigPath("./..")

	viper.AutomaticEnv()
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))

	viper.SetDefault("nudgebee_namespace", "nudgebee")

	err := viper.ReadInConfig()
	if err != nil {
		fmt.Println("Unable to read config file:", err)
	}

	err = viper.Unmarshal(&Config)
	if err != nil {
		fmt.Println("Error unmarshalling config:", err)
	}

	if data, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/namespace"); err == nil {
		namespace := strings.TrimSpace(string(data))
		if namespace != "" {
			Config.NudgebeeNamespace = namespace
		}
	}
}
