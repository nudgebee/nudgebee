package config

import (
	"fmt"

	"github.com/spf13/viper"
)

var Config appConfig

const SERVICE_NAME = "cloud-collector-server"

type appConfig struct {
	ServiceApiServerToken       string `mapstructure:"action_api_server_token"`
	ServiceApiServerTokenHeader string `mapstructure:"action_api_server_token_header"`
	ServiceEndpoint             string `mapstructure:"service_api_server_url"`

	CloudCollectorServerTokenHeader              string `mapstructure:"cloud_collector_server_token_header"`
	CloudCollectorServerToken                    string `mapstructure:"cloud_collector_server_token"`
	CloudCollectorServerDBUrl                    string `mapstructure:"cloud_collector_server_db_url"`
	CloudCollectorServerDBMaxConnection          int    `mapstructure:"cloud_collector_server_db_max_connection"`
	CloudCollectorServerDBMinConnection          int    `mapstructure:"cloud_collector_server_db_min_connection"`
	CloudCollectorServerDBIdleMinutes            int    `mapstructure:"cloud_collector_server_db_idle_minutes"`
	CloudCollectorServerCostProcessingWorkersMax int    `mapstructure:"cloud_collector_server_cost_processing_workers_max"`
	CloudCollectorServerCostBackfillMonths       int    `mapstructure:"cloud_collector_server_cost_backfill_months"`
	CloudCollectorServerMetricsWorkersMax        int    `mapstructure:"cloud_collector_server_metrics_workers_max"`
	CloudCollectorServerEventsWorkersMax         int    `mapstructure:"cloud_collector_server_events_workers_max"`

	Env     string `mapstructure:"env"`
	BaseUrl string `mapstructure:"base_url"`

	NudgebeeEncryptionKey string `mapstructure:"nudgebee_encryption_key"`

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

	RabbitMqUsername string `mapstructure:"rabbit_mq_username"`
	RabbitMqPassword string `mapstructure:"rabbit_mq_password"`
	RabbitMqHost     string `mapstructure:"rabbit_mq_host"`
	RabbitMqPort     int    `mapstructure:"rabbit_mq_port"`

	RabbitMqNotificationsQueue    string `mapstructure:"rabbit_mq_notifications_queue"`
	RabbitMqNotificationsExchange string `mapstructure:"rabbit_mq_notifications_exchange"`

	RabbitMqCloudAccountCostReportQueue       string `mapstructure:"rabbit_mq_cloud_account_cost_report_queue"`
	RabbitMqCloudAccountCostReportExchange    string `mapstructure:"rabbit_mq_cloud_account_cost_report_exchange"`
	RabbitMqCloudAccountCostReportDLQQueue    string `mapstructure:"rabbit_mq_cloud_account_cost_report_dlq_queue"`
	RabbitMqCloudAccountCostReportDLQExchange string `mapstructure:"rabbit_mq_cloud_account_cost_report_dlq_exchange"`

	RabbitMqCloudAccountMetricsQueue       string `mapstructure:"rabbit_mq_cloud_account_metrics_queue"`
	RabbitMqCloudAccountMetricsExchange    string `mapstructure:"rabbit_mq_cloud_account_metrics_exchange"`
	RabbitMqCloudAccountMetricsDLQQueue    string `mapstructure:"rabbit_mq_cloud_account_metrics_dlq_queue"`
	RabbitMqCloudAccountMetricsDLQExchange string `mapstructure:"rabbit_mq_cloud_account_metrics_dlq_exchange"`

	RabbitMqCloudAccountEventsQueue       string `mapstructure:"rabbit_mq_cloud_account_events_queue"`
	RabbitMqCloudAccountEventsExchange    string `mapstructure:"rabbit_mq_cloud_account_events_exchange"`
	RabbitMqCloudAccountEventsDLQQueue    string `mapstructure:"rabbit_mq_cloud_account_events_dlq_queue"`
	RabbitMqCloudAccountEventsDLQExchange string `mapstructure:"rabbit_mq_cloud_account_events_dlq_exchange"`

	RabbitMqCloudAccountPostReportQueue       string `mapstructure:"rabbit_mq_cloud_account_post_report_queue"`
	RabbitMqCloudAccountPostReportExchange    string `mapstructure:"rabbit_mq_cloud_account_post_report_exchange"`
	RabbitMqCloudAccountPostReportDLQQueue    string `mapstructure:"rabbit_mq_cloud_account_post_report_dlq_queue"`
	RabbitMqCloudAccountPostReportDLQExchange string `mapstructure:"rabbit_mq_cloud_account_post_report_dlq_exchange"`

	RabbitMqKGUpdateExchange string `mapstructure:"rabbit_mq_kg_update_exchange"`
	RabbitMqKGUpdateQueue    string `mapstructure:"rabbit_mq_kg_update_queue"`

	ClickhouseHost     string `mapstructure:"clickhouse_host"`
	ClickhouseUser     string `mapstructure:"clickhouse_user"`
	ClickhousePassword string `mapstructure:"clickhouse_password"`
	ClickhouseDatabase string `mapstructure:"clickhouse_database"`
	ClickhouseEnabled  bool   `mapstructure:"clickhouse_enabled"`

	CloudCollectorServerEventCloudTrailEnabled      bool   `mapstructure:"cloud_collector_server_event_cloudtrail_enabled"`
	CloudCollectorServerEventCloudWatchAlarmEnabled bool   `mapstructure:"cloud_collector_server_event_cloudwatchalarm_enabled"`
	CloudCollectorAwsEventbridgeSqs                 string `mapstructure:"cloud_collector_aws_eventbridge_sqs"`
	CloudCollectorNotificationSlackChannel          string `mapstructure:"cloud_collector_notifications_slack_channel"`

	// Azure Pricing API Configuration
	AzurePricingEnabled  bool `mapstructure:"azure_pricing_enabled"`
	AzurePricingCacheTTL int  `mapstructure:"azure_pricing_cache_ttl_hours"`
	AzurePricingFallback bool `mapstructure:"azure_pricing_fallback_enabled"`

	// Azure Event Grid / Service Bus Configuration
	// Event Grid events are delivered to Service Bus Queue, consumed by cloud-collector
	CloudCollectorAzureServiceBusConnectionString string `mapstructure:"cloud_collector_azure_service_bus_connection_string"`
	CloudCollectorAzureServiceBusNamespace        string `mapstructure:"cloud_collector_azure_service_bus_namespace"`
	CloudCollectorAzureServiceBusQueueName        string `mapstructure:"cloud_collector_azure_service_bus_queue_name"`
	CloudCollectorAzureEventRulesPath             string `mapstructure:"cloud_collector_azure_event_rules_path"`

	// AWS Organization Onboarding - SQS queue for CF Custom Resource registration callbacks
	CloudCollectorOrgRegistrationSqs string `mapstructure:"cloud_collector_org_registration_sqs"`

	// GCP Pub/Sub Configuration for Cloud Monitoring alerts and events
	CloudCollectorGcpPubSubProjectID      string `mapstructure:"cloud_collector_gcp_pubsub_project_id"`
	CloudCollectorGcpPubSubSubscriptionID string `mapstructure:"cloud_collector_gcp_pubsub_subscription_id"`
	CloudCollectorGcpEventRulesPath       string `mapstructure:"cloud_collector_gcp_event_rules_path"`

	// Request timeout in seconds for API handler context and HTTP server read/write
	CloudCollectorRequestTimeoutSeconds int `mapstructure:"cloud_collector_request_timeout_seconds"`

	// Redis Configuration
	RedisServerHost   string `mapstructure:"redis_server_host"`
	RedisServerPort   int    `mapstructure:"redis_server_port"`
	RedisUserName     string `mapstructure:"redis_user_name"`
	RedisUserPassword string `mapstructure:"redis_user_password"`
}

// initialize based on environment variables using viper
func init() {
	viper.SetConfigName("config")
	viper.SetConfigFile(".env")
	viper.SetConfigType("dotenv")
	viper.AddConfigPath(".")

	viper.SetDefault("action_api_server_token_header", "X-ACTION-TOKEN")
	viper.SetDefault("cloud_collector_server_token_header", "X-ACTION-TOKEN")

	viper.SetDefault("env", "")
	viper.SetDefault("service_api_server_url", "http://services-server:8000")

	// viper requires default values or bind.. else Unmarshal skips fields with no default values
	viper.SetDefault("action_api_server_token", "")
	viper.SetDefault("base_url", "http://nudgebee")

	viper.SetDefault("otel_service_name", SERVICE_NAME)
	viper.SetDefault("otel_provider", "noop")

	viper.SetDefault("cloud_collector_server_token", "")
	viper.SetDefault("nudgebee_encryption_key", "")

	viper.SetDefault("cloud_collector_server_db_url", "postgresql://postgres:postgres@localhost:5432/nudgebee?sslmode=disable")
	// Default max connections for PostgreSQL. Tune based on production load and DB server specs.
	viper.SetDefault("cloud_collector_server_db_max_connection", 50)
	viper.SetDefault("cloud_collector_server_db_min_connection", 1)
	viper.SetDefault("cloud_collector_server_db_idle_minutes", 10)
	viper.SetDefault("cloud_collector_azure_service_bus_connection_string", "")

	viper.SetDefault("cloud_collector_azure_service_bus_namespace", "")
	viper.SetDefault("cloud_collector_azure_service_bus_queue_name", "")

	// RabbitMQ defaults

	viper.SetDefault("rabbit_mq_username", "user")
	viper.SetDefault("rabbit_mq_password", "")
	viper.SetDefault("rabbit_mq_host", "localhost")
	viper.SetDefault("rabbit_mq_port", 5672)

	viper.SetDefault("rabbit_mq_notifications_queue", "notifications")
	viper.SetDefault("rabbit_mq_notifications_exchange", "notifications_exchange")

	viper.SetDefault("rabbit_mq_cloud_account_cost_report_queue", "cloud_account_cost_report")
	viper.SetDefault("rabbit_mq_cloud_account_cost_report_exchange", "cloud_account_cost_report_exchange")

	viper.SetDefault("rabbit_mq_cloud_account_metrics_queue", "cloud_account_metrics")
	viper.SetDefault("rabbit_mq_cloud_account_metrics_exchange", "cloud_account_metrics_exchange")
	viper.SetDefault("rabbit_mq_cloud_account_metrics_dlq_queue", "cloud_account_metrics_dlq")
	viper.SetDefault("rabbit_mq_cloud_account_metrics_dlq_exchange", "cloud_account_metrics_dlq_exchange")

	viper.SetDefault("rabbit_mq_cloud_account_events_queue", "cloud_account_events")
	viper.SetDefault("rabbit_mq_cloud_account_events_exchange", "cloud_account_events_exchange")
	viper.SetDefault("rabbit_mq_cloud_account_events_dlq_queue", "cloud_account_events.dlq")
	viper.SetDefault("rabbit_mq_cloud_account_events_dlq_exchange", "cloud_account_events_exchange_dlx")

	viper.SetDefault("rabbit_mq_cloud_account_post_report_queue", "cloud_account_post_report")
	viper.SetDefault("rabbit_mq_cloud_account_post_report_exchange", "cloud_account_post_report_exchange")
	viper.SetDefault("rabbit_mq_cloud_account_post_report_dlq_queue", "cloud_account_post_report.dlq")
	viper.SetDefault("rabbit_mq_cloud_account_post_report_dlq_exchange", "cloud_account_post_report_exchange_dlx")

	viper.SetDefault("rabbit_mq_kg_update_exchange", "kg_update_exchange")
	viper.SetDefault("rabbit_mq_kg_update_queue", "kg_update")

	viper.SetDefault("clickhouse_host", "http://localhost:8123")
	viper.SetDefault("clickhouse_user", "default")
	viper.SetDefault("clickhouse_database", "nudgebee")
	viper.SetDefault("clickhouse_password", "default")
	viper.SetDefault("clickhouse_enabled", false)

	viper.SetDefault("otel_service_name", SERVICE_NAME)
	viper.SetDefault("otel_exporter", "noop")
	viper.SetDefault("otel_exporter_otlp_endpoint", "127.0.0.1:4317")
	viper.SetDefault("otel_grpc_timeout_seconds", 5)
	viper.SetDefault("otel_grpc_max_msg_size", 8*1024*1024)

	viper.SetDefault("cloud_collector_aws_eventbridge_sqs", "")
	viper.SetDefault("cloud_collector_org_registration_sqs", "")

	// GCP Pub/Sub defaults
	viper.SetDefault("cloud_collector_gcp_pubsub_project_id", "")
	viper.SetDefault("cloud_collector_gcp_pubsub_subscription_id", "")
	viper.SetDefault("cloud_collector_gcp_event_rules_path", "")

	viper.SetDefault("cloud_collector_server_cost_processing_workers_max", 1)
	// Max number of trailing months of historical CUR data to backfill at new-account onboarding (0 disables).
	viper.SetDefault("cloud_collector_server_cost_backfill_months", 6)
	viper.SetDefault("cloud_collector_server_metrics_workers_max", 1)
	viper.SetDefault("cloud_collector_server_events_workers_max", 1)

	viper.SetDefault("cloud_collector_notifications_slack_channel", "")

	viper.SetDefault("cloud_collector_server_event_cloudtrail_enabled", false)
	viper.SetDefault("cloud_collector_server_event_cloudwatchalarm_enabled", true)

	viper.SetDefault("cloud_collector_request_timeout_seconds", 130)

	// Redis defaults
	viper.SetDefault("redis_server_host", "localhost")
	viper.SetDefault("redis_server_port", 6379)
	viper.SetDefault("redis_user_name", "")
	viper.SetDefault("redis_user_password", "")

	// Azure Pricing defaults
	viper.SetDefault("azure_pricing_enabled", true)
	viper.SetDefault("azure_pricing_cache_ttl_hours", 24)
	viper.SetDefault("azure_pricing_fallback_enabled", true)

	err := viper.ReadInConfig()
	if err != nil {
		fmt.Println("unable to read config file:", err)
	}

	viper.AutomaticEnv()
	err = viper.Unmarshal(&Config)
	if err != nil {
		fmt.Println("Error unmarshalling config:", err)
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
}
