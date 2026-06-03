package common

import (
	"fmt"

	"github.com/spf13/viper"
)

const SERVICE_NAME = "ticket-server"

var Config appConfig

type appConfig struct {
	EtlServerEndpoint string `mapstructure:"etl_server_endpoint"`
	EtlServerToken    string `mapstructure:"etl_server_token"`

	ServiceApiServerToken       string `mapstructure:"action_api_server_token"`
	ServiceApiServerTokenHeader string `mapstructure:"action_api_server_token_header"`
	ServiceDBUrl                string `mapstructure:"app_database_url"`
	ServiceEndpoint             string `mapstructure:"service_api_server_url"`

	NudgebeeEncryptionKey string `mapstructure:"nudgebee_encryption_key"`

	ClickhouseHost     string `mapstructure:"clickhouse_host"`
	ClickhouseUser     string `mapstructure:"clickhouse_user"`
	ClickhousePassword string `mapstructure:"clickhouse_password"`
	ClickhouseDatabase string `mapstructure:"clickhouse_database"`

	Env          string `mapstructure:"env"`
	Port         int    `mapstructure:"port"`
	DBSslEnabled bool   `mapstructure:"nudgebee_db_ssl_enabled"`

	MlServiceUrl   string `mapstructure:"ml_service_url"`
	GPTToken       string `mapstructure:"gpt_token"`
	OpenAIEndpoint string `mapstructure:"openai_endpoint"`
	GPTModel       string `mapstructure:"gpt_model"`

	OtelServiceName          string `mapstructure:"otel_service_name"`
	OtelExporterOtlpEndpoint string `mapstructure:"otel_exporter_otlp_endpoint"`

	OtelExporter                   string `mapstructure:"otel_exporter"`
	OtelTracesExporter             string `mapstructure:"otel_traces_exporter"`
	OtelExporterOtlpTracesEndpoint string `mapstructure:"otel_exporter_otlp_traces_endpoint"`

	OtelMetricesExporter            string `mapstructure:"otel_metrics_exporter"`
	OtelExporterOtlpMetricsEndpoint string `mapstructure:"otel_exporter_otlp_metrics_endpoint"`
	OtelLogsExporter                string `mapstructure:"otel_logs_exporter"`

	OtelGrpcTimeoutSeconds int    `mapstructure:"otel_grpc_timeout_seconds"`
	OtelGrpcMaxMsgSize     int    `mapstructure:"otel_grpc_max_msg_size"`
	GithubAppID            string `mapstructure:"github_app_id"`
	GithubPvtKey           string `mapstructure:"github_private_key"`

	// Cache (create-meta). Keys mirror api-server so the same configmap/secret
	// values can be reused. Defaults to in-memory so dev/test need no Redis.
	CacheProvider           string `mapstructure:"cache_provider"`
	CacheExpirationMinutes  int    `mapstructure:"cache_expiration_minutes"`
	CacheInMemorySizeMb     int    `mapstructure:"cache_inmemory_size_mb"`
	CacheInMemoryMaxEntries int    `mapstructure:"cache_inmemory_max_entries"`
	CacheRedisServerHost    string `mapstructure:"redis_server_host"`
	CacheRedisServerPort    int    `mapstructure:"redis_server_port"`
	CacheRedisUserName      string `mapstructure:"redis_user_name"`
	CacheRedisUserPassword  string `mapstructure:"redis_user_password"`
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

	viper.SetDefault("ml_service_url", "http://localhost:9000")
	viper.SetDefault("etl_server_endpoint", "http://localhost:5000")

	viper.SetDefault("env", "production")
	viper.SetDefault("port", 8080)
	viper.SetDefault("nudgebee_db_ssl_enabled", "true")
	viper.SetDefault("service_api_server_url", "http://services-server:8000")
	viper.SetDefault("gpt_token", "default")
	viper.SetDefault("openai_endpoint", "https://api.openai.com/v1")
	viper.SetDefault("gpt_model", "gpt-3.5-turbo")

	// viper requires default values or bind.. else Unmarshal skips fields with no default values
	viper.SetDefault("etl_server_token", "")
	viper.SetDefault("action_api_server_token", "")
	viper.SetDefault("app_database_url", "")
	viper.SetDefault("nudgebee_encryption_key", "")

	viper.SetDefault("otel_service_name", SERVICE_NAME)
	viper.SetDefault("otel_exporter", "noop")
	viper.SetDefault("otel_exporter_otlp_endpoint", "127.0.0.1:4317")
	viper.SetDefault("otel_grpc_timeout_seconds", 5)
	viper.SetDefault("otel_grpc_max_msg_size", 8*1024*1024)

	viper.SetDefault("github_app_id", "")
	viper.SetDefault("github_private_key", "")

	viper.SetDefault("cache_provider", "in_memory")
	viper.SetDefault("cache_expiration_minutes", 30)
	viper.SetDefault("cache_inmemory_size_mb", 20)
	viper.SetDefault("cache_inmemory_max_entries", 1000)
	viper.SetDefault("redis_server_host", "redis")
	viper.SetDefault("redis_server_port", 6379)
	viper.SetDefault("redis_user_name", "")
	viper.SetDefault("redis_user_password", "")

	err := viper.ReadInConfig()
	if err != nil {
		fmt.Println("Error reading config file:", err)
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
