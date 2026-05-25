package config

import (
	"log/slog"
	"os"

	"github.com/spf13/viper"
)

var Config appConfig

const SERVICE_NAME = "services-server"

type appConfig struct {
	NudgebeeLicense string `mapstructure:"nudgebee_license"`

	ServiceApiServerToken           string `mapstructure:"action_api_server_token"`
	ServiceApiServerTokenHeader     string `mapstructure:"action_api_server_token_header"`
	ServiceDBUrl                    string `mapstructure:"app_database_url"`
	ServiceDBMaxConnection          int    `mapstructure:"app_database_max_connection"`
	ServiceDBMinConnection          int    `mapstructure:"app_database_min_connection"`
	ServiceDBIdleMinutes            int    `mapstructure:"app_database_idle_minutes"`
	ServiceDBConnMaxLifetimeMinutes int    `mapstructure:"app_database_conn_max_lifetime_minutes"`
	ServiceEndpoint                 string `mapstructure:"service_api_server_url"`

	NudgebeeEncryptionKey string `mapstructure:"nudgebee_encryption_key"`

	ClickhouseHost     string `mapstructure:"clickhouse_host"`
	ClickhouseUser     string `mapstructure:"clickhouse_user"`
	ClickhousePassword string `mapstructure:"clickhouse_password"`
	ClickhouseDatabase string `mapstructure:"clickhouse_database"`
	ClickhouseEnabled  bool   `mapstructure:"clickhouse_enabled"`

	RabbitMqUsername string `mapstructure:"rabbit_mq_username"`
	RabbitMqPassword string `mapstructure:"rabbit_mq_password"`
	RabbitMqHost     string `mapstructure:"rabbit_mq_host"`
	RabbitMqPort     int    `mapstructure:"rabbit_mq_port"`

	Env          string `mapstructure:"env"`
	DBSslEnabled bool   `mapstructure:"nudgebee_db_ssl_enabled"`

	MlServiceUrl string `mapstructure:"ml_service_url"`

	RabbitMqNotificationsQueue    string `mapstructure:"rabbit_mq_notifications_queue"`
	RabbitMqNotificationsExchange string `mapstructure:"rabbit_mq_notifications_exchange"`
	RabbitMqUserOnboardQueue      string `mapstructure:"rabbit_mq_user_onboard_queue"`
	RabbitMqUserOnboardExchange   string `mapstructure:"rabbit_mq_user_onboard_exchange"`

	RabbitMqServicesExchange                     string `mapstructure:"rabbit_mq_services_exchange"`
	RabbitMqServicesCloudAccountOnboardingQueue  string `mapstructure:"rabbit_mq_services_cloudaccount_onboarding_queue"`
	RabbitMqServicesAnomalyProcessingQueue       string `mapstructure:"rabbit_mq_services_anomaly_processing_queue"`
	RabbitMqServicesAnomalyProcessingConcurrency int    `mapstructure:"rabbit_mq_services_anomaly_processing_concurrency"`
	BaseUrl                                      string `mapstructure:"base_url"`
	BrandingName                                 string `mapstructure:"branding_name"`

	RabbitMqTroubleshootExchange string `mapstructure:"rabbit_mq_troubleshoot_exchange"`
	RabbitMqTroubleshootQueue    string `mapstructure:"rabbit_mq_troubleshoot_queue"`

	AuditPublishEnabled bool `mapstructure:"audit_publish_enabled"`
	// Fan-out exchange used to broadcast integration cache invalidation
	// events to every llm-server replica. Each llm-server pod binds an
	// auto-delete + exclusive queue to it so every pod receives every
	// message.
	RabbitMqLLMCacheInvalidationExchange string `mapstructure:"rabbit_mq_llm_cache_invalidation_exchange"`

	AutoPilotUrl string `mapstructure:"auto_pilot_url"`

	RelayServerEndpoint  string `mapstructure:"relay_server_endpoint"`
	RelayServerSecretKey string `mapstructure:"relay_server_secret_key"`

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

	GitCommitNudgebeeUser  string `mapstructure:"git_commit_nudgebee_user"`
	GitCommitNudgebeeEmail string `mapstructure:"git_commit_nudgebee_user_email"`

	AWS_TEMPLATE_URL       string `mapstructure:"aws_template_url"`
	NUDGEBEE_INSTANCE_ROLE string `mapstructure:"nudgebee_instance_role"`
	NUDGEBEE_URL           string `mapstructure:"nudgebee_url"`

	AwsOrgTemplateUrl string `mapstructure:"aws_org_template_url"`
	AwsOrgSnsTopicArn string `mapstructure:"aws_org_sns_topic_arn"`

	CloudCollectorAwsEventbridgeSqs string `mapstructure:"cloud_collector_aws_eventbridge_sqs"`
	AwsEventBridgeAddonTemplateURL  string `mapstructure:"aws_eventbridge_addon_template_url"`

	AzureARMTemplateURL string `mapstructure:"azure_arm_template_url"`

	GcpPubSubTemplateURL                  string `mapstructure:"gcp_pubsub_template_url"`
	GcpProjectID                          string `mapstructure:"gcp_project_id"`
	CloudCollectorGcpPubSubSubscriptionID string `mapstructure:"cloud_collector_gcp_pubsub_subscription_id"`

	CloudCollectorServerToken       string `mapstructure:"cloud_collector_server_token"`
	CloudCollectorServerUrl         string `mapstructure:"cloud_collector_server_url"`
	CloudCollectorServerTokenHeader string `mapstructure:"cloud_collector_server_token_header"`

	LLMServerEndpoint    string `mapstructure:"llm_server_endpoint"`
	LLMServerToken       string `mapstructure:"llm_server_token"`
	LLMServerTokenHeader string `mapstructure:"llm_server_token_header"`

	ServicesServerLLMRetryAttempts         int `mapstructure:"services_server_llm_retry_attempts"`
	ServicesServerLLMInitialBackoffSeconds int `mapstructure:"services_server_llm_initial_backoff_seconds"`

	WorkflowServerEndpoint string `mapstructure:"workflow_server_endpoint"`
	WorkflowServerToken    string `mapstructure:"workflow_server_token"`

	NotificationServiceUrl string `mapstructure:"notification_service_url"`
	TicketServiceUrl       string `mapstructure:"ticket_service_url"`

	NBRetentionDaysCronEvents              int `mapstructure:"nb_retention_days_hasura_cron_events"`
	NBRetentionDaysCloudAccountUsageReport int `mapstructure:"nb_retention_days_cloud_account_usage_report"`

	NBRetentionDaysAgentConnectLogs int `mapstructure:"nb_retention_days_agent_connect_logs"`

	NBRetentionDaysEventsNormal   int `mapstructure:"nb_retention_days_events_normal"`
	NBRetentionDaysEventsCritical int `mapstructure:"nb_retention_days_events_critical"`

	NBRetentionDaysK8sResources int `mapstructure:"nb_retention_days_k8s_resources"`

	KGEdgeStaleAfterDays           int    `mapstructure:"kg_edge_stale_after_days"`
	NBRetentionDaysKGInactiveEdges int    `mapstructure:"nb_retention_days_kg_inactive_edges"`
	KGBehavioralEdgeTypes          string `mapstructure:"kg_behavioral_edge_types"` // comma-separated; empty falls back to DefaultBehavioralEdgeTypes

	NBAnomalyTrainingDays    int `mapstructure:"nb_anomaly_training_days"`
	NBAnomalyEvaluationHours int `mapstructure:"nb_anomaly_evaluation_hours"`

	NBSpendAnomalyBaselineDays               int     `mapstructure:"nb_spend_anomaly_baseline_days"`
	NBSpendAnomalyZScoreThreshold            float64 `mapstructure:"nb_spend_anomaly_zscore_threshold"`
	NBSpendAnomalyMinAbsChange               float64 `mapstructure:"nb_spend_anomaly_min_abs_change"`
	NBSpendAnomalyMinPctChange               float64 `mapstructure:"nb_spend_anomaly_min_pct_change"`
	NBSpendAnomalyMinBaselineSpend           float64 `mapstructure:"nb_spend_anomaly_min_baseline_spend"`
	NBSpendAnomalyCooldownDays               int     `mapstructure:"nb_spend_anomaly_cooldown_days"`
	NBSpendAnomalyResolutionStddevMultiplier float64 `mapstructure:"nb_spend_anomaly_resolution_stddev_multiplier"`
	NBSpendAnomalyResolutionConsecutiveDays  int     `mapstructure:"nb_spend_anomaly_resolution_consecutive_days"`

	CacheProvider           string `mapstructure:"cache_provider"`
	CacheExpirationMinutes  int    `mapstructure:"cache_expiration_minutes"`
	CacheInMemorySizeMb     int    `mapstructure:"cache_inmemory_size_mb"`
	CacheInMemoryMaxEntries int    `mapstructure:"cache_inmemory_max_entries"`
	CacheRedisUserName      string `mapstructure:"redis_user_name"`
	CacheRedisUserPassword  string `mapstructure:"redis_user_password"`
	CacheRedisServerHost    string `mapstructure:"redis_server_host"`
	CacheRedisServerPort    int    `mapstructure:"redis_server_port"`

	FeatureEventAutoAiSummaryEnabled bool   `mapstructure:"feature_event_auto_ai_summary_enabled"`
	ServerName                       string `mapstructure:"services_server_name"`

	// Webhook execution mode - true for async (default), false for sync (useful for tests)
	WebhookAsyncExecution bool `mapstructure:"webhook_async_execution"`

	LlmServerShellImage string `mapstructure:"llm_server_tool_shell_image"`

	ServicesServerRelayCommandExecutionTimeoutSeconds int `mapstructure:"services_server_relay_command_execution_timeout_seconds"`

	RabbitMqRunbookEventExchange   string `mapstructure:"rabbit_mq_runbook_event_exchange"`
	RabbitMqRunbookEventQueue      string `mapstructure:"rabbit_mq_runbook_event_queue"`
	RabbitMqRunbookEventRoutingKey string `mapstructure:"rabbit_mq_runbook_event_routing_key"`

	// GitHub App configuration for authentication
	GithubAppId         string `mapstructure:"github_app_id"`
	GithubPrivateKey    string `mapstructure:"github_private_key"`
	GithubWebhookSecret string `mapstructure:"github_webhook_secret"`

	// Knowledge Graph queue configuration
	RabbitMqKGUpdateExchange         string `mapstructure:"rabbit_mq_kg_update_exchange"`
	RabbitMqKGUpdateQueue            string `mapstructure:"rabbit_mq_kg_update_queue"`
	RabbitMqKGUpdateConcurrency      int    `mapstructure:"rabbit_mq_kg_update_concurrency"`
	KGUpdateDeduplicationMinutes     int    `mapstructure:"kg_update_deduplication_minutes"`
	KGUpdateProcessingTimeoutMinutes int    `mapstructure:"kg_update_processing_timeout_minutes"`

	// Event post-process queue configuration
	RabbitMqEventPostProcessExchange    string `mapstructure:"rabbit_mq_event_post_process_exchange"`
	RabbitMqEventPostProcessQueue       string `mapstructure:"rabbit_mq_event_post_process_queue"`
	RabbitMqEventPostProcessConcurrency int    `mapstructure:"rabbit_mq_event_post_process_concurrency"`

	// Webhook async processing queue configuration
	RabbitMqWebhookProcessExchange    string `mapstructure:"rabbit_mq_webhook_process_exchange"`
	RabbitMqWebhookProcessQueue       string `mapstructure:"rabbit_mq_webhook_process_queue"`
	RabbitMqWebhookProcessConcurrency int    `mapstructure:"rabbit_mq_webhook_process_concurrency"`
}

// postInitHooks are callbacks fired after Config has been unmarshalled.
// EE packages register here to populate their own config structs (see
// ee/config) without OSS code having to know about EE-only fields.
var postInitHooks []func()

// RegisterPostInit registers a callback to be invoked after Config is
// populated. Callbacks run in registration order.
func RegisterPostInit(fn func()) {
	postInitHooks = append(postInitHooks, fn)
}

// initialize based on environment variables using viper
func init() {
	viper.SetConfigName("config")
	viper.SetConfigFile(".env")
	viper.SetConfigType("dotenv")
	viper.AddConfigPath(".")

	viper.SetDefault("action_api_server_token_header", "X-ACTION-TOKEN")

	viper.SetDefault("clickhouse_host", "http://localhost:8123")
	viper.SetDefault("clickhouse_user", "default")
	viper.SetDefault("clickhouse_database", "nudgebee")
	viper.SetDefault("clickhouse_password", "default")
	viper.SetDefault("clickhouse_enabled", false)

	viper.SetDefault("rabbit_mq_username", "user")
	viper.SetDefault("rabbit_mq_password", "")
	viper.SetDefault("rabbit_mq_host", "localhost")
	viper.SetDefault("rabbit_mq_port", 5672)

	// Disabled by default: no consumer is bound to the nb_audits exchange yet,
	// so publishing produces unroutable-dropped messages and trips the
	// rabbitmq_channel_messages_unroutable_dropped_total alert. Flip to true
	// once a consumer exists.
	viper.SetDefault("audit_publish_enabled", false)

	viper.SetDefault("ml_service_url", "http://localhost:9000")

	viper.SetDefault("env", "")
	viper.SetDefault("nudgebee_db_ssl_enabled", "true")
	viper.SetDefault("service_api_server_url", "http://services-server:8000")

	// viper requires default values or bind.. else Unmarshal skips fields with no default values
	viper.SetDefault("action_api_server_token", "")
	viper.SetDefault("app_database_url", "")
	viper.SetDefault("nudgebee_license", "")
	viper.SetDefault("app_database_max_connection", "20")
	viper.SetDefault("app_database_min_connection", "2")
	viper.SetDefault("app_database_idle_minutes", "5")
	viper.SetDefault("app_database_conn_max_lifetime_minutes", "5")
	viper.SetDefault("nudgebee_encryption_key", "")

	viper.SetDefault("rabbit_mq_notifications_queue", "notifications")
	viper.SetDefault("rabbit_mq_notifications_exchange", "notifications_exchange")

	viper.SetDefault("rabbit_mq_troubleshoot_exchange", "llm_server_event_investigate")
	viper.SetDefault("rabbit_mq_troubleshoot_queue", "llm_server_event_investigate")
	viper.SetDefault("rabbit_mq_llm_cache_invalidation_exchange", "llm_cache_invalidation")

	viper.SetDefault("rabbit_mq_user_onboard_queue", "notifications")
	viper.SetDefault("rabbit_mq_user_onboard_exchange", "notifications_exchange")

	viper.SetDefault("rabbit_mq_services_exchange", "services_exchange")
	viper.SetDefault("rabbit_mq_services_cloudaccount_onboarding_queue", "cloudaccount_onboarding")
	viper.SetDefault("rabbit_mq_services_anomaly_processing_queue", "anomaly_processing")
	viper.SetDefault("rabbit_mq_services_anomaly_processing_concurrency", "1")
	viper.SetDefault("base_url", "http://nudgebee")
	viper.SetDefault("branding_name", "Nudgebee")

	viper.SetDefault("auto_pilot_url", "http://auto-pilot-server:9988")

	viper.SetDefault("relay_server_endpoint", "http://localhost:52832")
	viper.SetDefault("relay_server_secret_key", "")

	viper.SetDefault("git_commit_nudgebee_user", "Nudgebee Bot")
	viper.SetDefault("git_commit_nudgebee_user_email", "")
	viper.SetDefault("aws_template_url", "")
	viper.SetDefault("aws_org_template_url", "")
	viper.SetDefault("aws_org_sns_topic_arn", "")
	viper.SetDefault("nudgebee_instance_role", "")
	viper.SetDefault("nudgebee_url", "http://localhost:3000")
	viper.SetDefault("cloud_collector_aws_eventbridge_sqs", "")
	viper.SetDefault("aws_eventbridge_addon_template_url", "")

	viper.SetDefault("azure_arm_template_url", "")

	viper.SetDefault("gcp_pubsub_template_url", "")
	viper.SetDefault("gcp_project_id", "")
	viper.SetDefault("cloud_collector_gcp_pubsub_subscription_id", "")

	viper.SetDefault("cloud_collector_server_url", "http://cloud-collector-servert:8000")
	viper.SetDefault("cloud_collector_server_token", "")
	viper.SetDefault("cloud_collector_server_token_header", "X-ACTION-TOKEN")

	viper.SetDefault("llm_server_endpoint", "http://llm-server:8000")
	viper.SetDefault("llm_server_token", "")
	viper.SetDefault("llm_server_token_header", "X-ACTION-TOKEN")
	viper.SetDefault("services_server_llm_retry_attempts", 180)
	viper.SetDefault("services_server_llm_initial_backoff_seconds", 5)

	viper.SetDefault("workflow_server_endpoint", "http://workflow-server:8000")
	viper.SetDefault("workflow_server_token", "")

	viper.SetDefault("notification_service_url", "http://notifications:8080")
	viper.SetDefault("ticket_service_url", "http://ticket-server:8080")

	viper.SetDefault("otel_service_name", SERVICE_NAME)
	viper.SetDefault("otel_exporter", "noop")
	viper.SetDefault("otel_exporter_otlp_endpoint", "127.0.0.1:4317")
	viper.SetDefault("otel_grpc_timeout_seconds", 5)
	viper.SetDefault("otel_grpc_max_msg_size", 8*1024*1024)

	viper.SetDefault("nb_retention_days_hasura_cron_events", 1)
	viper.SetDefault("nb_retention_days_agent_connect_logs", 30)
	viper.SetDefault("nb_retention_days_events_normal", 30)
	viper.SetDefault("nb_retention_days_events_critical", 90)
	viper.SetDefault("nb_retention_days_cloud_account_usage_report", 90)
	viper.SetDefault("nb_retention_days_k8s_resources", 30)
	viper.SetDefault("kg_edge_stale_after_days", 7)
	viper.SetDefault("nb_retention_days_kg_inactive_edges", 14)
	viper.SetDefault("kg_behavioral_edge_types", "") // empty → use DefaultBehavioralEdgeTypes

	viper.SetDefault("cache_provider", "in_memory")
	viper.SetDefault("cache_expiration_minutes", 30)
	viper.SetDefault("cache_inmemory_size_mb", 20)
	viper.SetDefault("cache_inmemory_max_entries", 1000)

	viper.SetDefault("redis_user_name", "")
	viper.SetDefault("redis_user_password", "")
	viper.SetDefault("redis_server_host", "localhost")
	viper.SetDefault("redis_server_port", 6379)

	viper.SetDefault("nb_anomaly_training_days", 7)
	viper.SetDefault("nb_anomaly_evaluation_hours", 1)

	viper.SetDefault("feature_event_auto_ai_summary_enabled", true)
	viper.SetDefault("webhook_async_execution", true)

	viper.SetDefault("LLM_SERVER_TOOL_SHELL_IMAGE", "")

	viper.SetDefault("services_server_relay_command_execution_timeout_seconds", 60)

	viper.SetDefault("rabbit_mq_runbook_event_exchange", "runbook_event_process")
	viper.SetDefault("rabbit_mq_runbook_event_queue", "runbook_event_process")
	viper.SetDefault("rabbit_mq_runbook_event_routing_key", "troubleshooting")

	// GitHub App configuration
	viper.SetDefault("github_app_id", "")
	viper.SetDefault("github_private_key", "")
	viper.SetDefault("github_webhook_secret", "")

	// Knowledge Graph queue configuration
	viper.SetDefault("rabbit_mq_kg_update_exchange", "kg_update_exchange")
	viper.SetDefault("rabbit_mq_kg_update_queue", "kg_update")
	viper.SetDefault("rabbit_mq_kg_update_concurrency", 2)
	viper.SetDefault("kg_update_deduplication_minutes", 120)
	viper.SetDefault("kg_update_processing_timeout_minutes", 30)

	// Event post-process queue configuration
	viper.SetDefault("rabbit_mq_event_post_process_exchange", "event_post_process_exchange")
	viper.SetDefault("rabbit_mq_event_post_process_queue", "event_post_process")
	viper.SetDefault("rabbit_mq_event_post_process_concurrency", 5)

	// Webhook async processing queue configuration
	viper.SetDefault("rabbit_mq_webhook_process_exchange", "webhook_process_exchange")
	viper.SetDefault("rabbit_mq_webhook_process_queue", "webhook_process")
	viper.SetDefault("rabbit_mq_webhook_process_concurrency", 5)

	err := viper.ReadInConfig()
	if err != nil {
		slog.Warn("reading config file failed, using .env file")
	}

	viper.AutomaticEnv()
	err = viper.Unmarshal(&Config)
	if err != nil {
		slog.Error("Error unmarshalling config", "error", err)
	}

	for _, fn := range postInitHooks {
		fn()
	}

	if Config.OtelExporterOtlpEndpoint == "" {
		Config.OtelExporterOtlpEndpoint = "127.0.0.1:4317"
	}

	if Config.OtelExporterOtlpTracesEndpoint == "" {
		Config.OtelExporterOtlpTracesEndpoint = Config.OtelExporterOtlpEndpoint
	}

	if Config.OtelExporterOtlpMetricsEndpoint == "" {
		Config.OtelExporterOtlpMetricsEndpoint = Config.OtelExporterOtlpEndpoint
	}

	if Config.OtelExporter == "" {
		Config.OtelExporter = "noop"
	}
	if Config.OtelTracesExporter == "" {
		Config.OtelTracesExporter = Config.OtelExporter
	}
	if Config.OtelMetricesExporter == "" {
		Config.OtelMetricesExporter = Config.OtelExporter
	}

	hostName, err := os.Hostname()
	if err != nil {
		slog.Error("Unable to get hostname", "error", err)
		hostName = "localhost"
	}

	viper.SetDefault("services_server_name", hostName)

}
