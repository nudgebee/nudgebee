package config

import (
	"fmt"

	"github.com/spf13/viper"
)

var Config appConfig

const SERVICE_NAME = "otel-collector-server"

type appConfig struct {
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

	CacheProvider           string `mapstructure:"cache_provider"`
	CacheExpirationMinutes  int    `mapstructure:"cache_expiration_minutes"`
	CacheInMemorySizeMb     int    `mapstructure:"cache_inmemory_size_mb"`
	CacheInMemoryMaxEntries int    `mapstructure:"cache_inmemory_max_entries"`
	CacheRedisUserName      string `mapstructure:"redis_user_name"`
	CacheRedisUserPassword  string `mapstructure:"redis_user_password"`
	CacheRedisServerHost    string `mapstructure:"redis_server_host"`
	CacheRedisServerPort    int    `mapstructure:"redis_server_port"`

	OtelServerDBUrl           string `mapstructure:"otel_server_db_url"`
	OtelServerDBMaxConnection int    `mapstructure:"otel_server_db_max_connection"`
	OtelServerDBMinConnection int    `mapstructure:"otel_server_db_min_connection"`
	OtelServerDBIdleMinutes   int    `mapstructure:"otel_server_db_idle_minutes"`

	OtelLogProvider string `mapstructure:"otel_server_log_provider"`

	OtelMetricsProvider string `mapstructure:"otel_server_metrics_provider"`

	OtelMetricsQueryProvider string `mapstructure:"otel_server_metrics_query_provider"`
	OtelMetricsQueryEndpoint string `mapstructure:"otel_server_metrics_query_endpoint"`

	OtelLogsQueryProvider string `mapstructure:"otel_server_logs_query_provider"`
	OtelLogsQueryEndpoint string `mapstructure:"otel_server_logs_query_endpoint"`

	OtelTraceProvider string `mapstructure:"otel_server_trace_provider"`
}

// initialize based on environment variables using viper
func init() {
	viper.SetConfigName("config")
	viper.SetConfigFile(".env")
	viper.SetConfigType("dotenv")
	viper.AddConfigPath(".")

	viper.SetDefault("action_api_server_token_header", "X-ACTION-TOKEN")

	viper.SetDefault("env", "")

	viper.SetDefault("nudgebee_encryption_key", "")

	viper.SetDefault("otel_service_name", SERVICE_NAME)
	viper.SetDefault("otel_exporter", "noop")
	viper.SetDefault("otel_exporter_otlp_endpoint", "127.0.0.1:4317")
	viper.SetDefault("otel_grpc_timeout_seconds", 5)
	viper.SetDefault("otel_grpc_max_msg_size", 8*1024*1024)

	viper.SetDefault("cache_provider", "in_memory")
	viper.SetDefault("cache_expiration_minutes", 30)
	viper.SetDefault("cache_inmemory_size_mb", 20)
	viper.SetDefault("cache_inmemory_max_entries", 1000)

	viper.SetDefault("redis_user_name", "")
	viper.SetDefault("redis_user_password", "")
	viper.SetDefault("redis_server_host", "localhost")
	viper.SetDefault("redis_server_port", 6379)

	viper.SetDefault("otel_server_db_url", "")

	viper.SetDefault("otel_server_db_max_connection", 5)
	viper.SetDefault("otel_server_db_min_connection", 1)
	viper.SetDefault("otel_server_db_idle_minutes", 10)

	viper.SetDefault("otel_server_log_provider", "")

	viper.SetDefault("otel_server_metrics_provider", "")

	viper.SetDefault("otel_server_metrics_query_provider", "prometheus")
	viper.SetDefault("otel_server_metrics_query_endpoint", "http://localhost:9090")

	viper.SetDefault("otel_server_trace_provider", "")

	err := viper.ReadInConfig()
	if err != nil {
		fmt.Println("reading config file failed, using .env file")
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

func (a appConfig) GetString(key string, defaultValue string) string {
	val := viper.GetString(key)
	if val == "" {
		val = defaultValue
	}
	return val
}

func (a appConfig) SetString(key string, value string) {
	viper.Set(key, value)
}
