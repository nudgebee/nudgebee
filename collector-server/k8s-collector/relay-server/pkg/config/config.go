package config

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/viper"
)

const SERVICE_NAME = "relay-server"

// Config holds all configuration for the relay‐server service.
type Config struct {
	HTTP struct {
		Port         int           `mapstructure:"port"`
		ReadTimeout  time.Duration `mapstructure:"read_timeout"`
		WriteTimeout time.Duration `mapstructure:"write_timeout"`
	} `mapstructure:"http"`

	Postgres struct {
		// DSN is loaded from COLLECTOR_DB_URL if set, otherwise from file
		DSN             string        `mapstructure:"dsn"`
		Driver          string        `mapstructure:"driver"`
		MaxOpenConns    int           `mapstructure:"max_open_conns"`
		MaxIdleConns    int           `mapstructure:"max_idle_conns"`
		ConnMaxLifetime time.Duration `mapstructure:"conn_max_lifetime"`
	} `mapstructure:"postgres"`

	RabbitMQ struct {
		Host          string        `mapstructure:"host"`
		Port          int           `mapstructure:"port"`
		Username      string        `mapstructure:"username"`
		Password      string        `mapstructure:"password"`
		URL           string        `mapstructure:"url"`
		ExchangeName  string        `mapstructure:"exchange_name"`
		RequestQueue  string        `mapstructure:"request_queue"`
		PrefetchCount int           `mapstructure:"prefetch_count"`
		RetryDelay    time.Duration `mapstructure:"retry_delay"`
		MessageTTL    time.Duration `mapstructure:"message_ttl"`
	} `mapstructure:"rabbitmq"`

	Security struct {
		// RELAY_SERVER_SECRET_KEY
		SecretKey string `mapstructure:"secret_key"`
		// NUDGEBEE_ENCRYPTION_KEY
		EncryptionKey string `mapstructure:"encryption_key"`
		// RELAY_WORKSPACE_JWT_SECRET - shared with llm-server's LLM_SERVER_JWT_SECRET
		WorkspaceJWTSecret string `mapstructure:"workspace_jwt_secret"`
	} `mapstructure:"security"`

	Signing struct {
		// Ed25519 private key (base64) for signing messages sent to proxy agents.
		// If empty, signing is disabled (backward compatible).
		// Env: SIGNING_PRIVATE_KEY
		PrivateKey string `mapstructure:"private_key"`
		// Key identifier for key rotation tracking.
		// Env: SIGNING_KEY_ID
		KeyID string `mapstructure:"key_id"`
	} `mapstructure:"signing"`

	Cache struct {
		// Provider selects the cache backend: "redis" or "in_memory" (default).
		Provider string `mapstructure:"provider"`
		Redis    struct {
			Host     string `mapstructure:"host"`
			Port     int    `mapstructure:"port"`
			Username string `mapstructure:"username"`
			Password string `mapstructure:"password"`
		} `mapstructure:"redis"`
	} `mapstructure:"cache"`

	Workspace struct {
		// RELAY_WORKSPACE_SHELL_IMAGE - image used for pod_script_run_enricher jobs
		ShellImage string `mapstructure:"shell_image"`
	} `mapstructure:"workspace"`

	Otel struct {
		ServiceName string `mapstructure:"service_name"`

		ExporterOtlpEndpoint string `mapstructure:"exporter_otlp_endpoint"`
		Exporter             string `mapstructure:"exporter"`

		TracesExporter             string `mapstructure:"traces_exporter"`
		ExporterOtlpTracesEndpoint string `mapstructure:"exporter_otlp_traces_endpoint"`

		MetricesExporter            string `mapstructure:"metrics_exporter"`
		ExporterOtlpMetricsEndpoint string `mapstructure:"exporter_otlp_metrics_endpoint"`

		LogsExporter             string `mapstructure:"logs_exporter"`
		ExporterOtlpLogsEndpoint string `mapstructure:"exporter_otlp_logs_endpoint"`

		GrpcTimeoutSeconds int `mapstructure:"grpc_timeout_seconds"`
		GrpcMaxMsgSize     int `mapstructure:"grpc_max_msg_size"`
	} `mapstructure:"otel"`
}

func Load() (*Config, error) {
	v := viper.New()

	// 2) Env
	v.SetEnvPrefix("RELAY") // so REST from RELAY_HTTP_PORT etc if you choose
	v.AutomaticEnv()
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))

	// 3) Bind our special env vars
	_ = v.BindEnv("postgres.dsn", "COLLECTOR_DB_URL")
	_ = v.BindEnv("security.encryption_key", "NUDGEBEE_ENCRYPTION_KEY")
	_ = v.BindEnv("security.secret_key", "RELAY_SERVER_SECRET_KEY")
	_ = v.BindEnv("security.workspace_jwt_secret", "RELAY_WORKSPACE_JWT_SECRET")
	_ = v.BindEnv("workspace.shell_image", "RELAY_WORKSPACE_SHELL_IMAGE")
	_ = v.BindEnv("rabbitmq.exchange_name", "RABBITMQ_EXCHANGE_NAME")
	_ = v.BindEnv("rabbitmq.request_queue", "RABBITMQ_REQUEST_QUEUE")

	_ = v.BindEnv("rabbitmq.host", "RABBIT_MQ_HOST")
	_ = v.BindEnv("rabbitmq.port", "RABBIT_MQ_PORT")
	_ = v.BindEnv("rabbitmq.username", "RABBIT_MQ_USERNAME")
	_ = v.BindEnv("rabbitmq.password", "RABBIT_MQ_PASSWORD")
	_ = v.BindEnv("rabbitmq.url", "RABBITMQ_URL")
	_ = v.BindEnv("rabbitmq.message_ttl", "RABBITMQ_MESSAGE_TTL")

	//bind all otel env vars
	_ = v.BindEnv("otel.exporter_otlp_endpoint", "OTEL_EXPORTER_OTLP_ENDPOINT")
	_ = v.BindEnv("otel.exporter", "OTEL_EXPORTER")
	_ = v.BindEnv("otel.exporter_otlp_traces_endpoint", "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT")
	_ = v.BindEnv("otel.traces_exporter", "OTEL_TRACES_EXPORTER")
	_ = v.BindEnv("otel.metrices_exporter", "OTEL_METRICS_EXPORTER")
	_ = v.BindEnv("otel.exporter_otlp_metrics_endpoint", "OTEL_EXPORTER_OTLP_METRICS_ENDPOINT")
	_ = v.BindEnv("otel.exporter_otlp_logs_endpoint", "OTEL_EXPORTER_OTLP_LOGS_ENDPOINT")
	_ = v.BindEnv("otel.logs_exporter", "OTEL_LOGS_EXPORTER")
	_ = v.BindEnv("otel.grpc_timeout_seconds", "OTEL_GRPC_TIMEOUT_SECONDS")
	_ = v.BindEnv("otel.grpc_max_msg_size", "OTEL_GRPC_MAX_MSG_SIZE")
	_ = v.BindEnv("http.read_timeout", "RELAY_HTTP_READ_TIMEOUT")
	_ = v.BindEnv("http.write_timeout", "RELAY_HTTP_WRITE_TIMEOUT")

	_ = v.BindEnv("signing.private_key", "SIGNING_PRIVATE_KEY")
	_ = v.BindEnv("signing.key_id", "SIGNING_KEY_ID")

	_ = v.BindEnv("cache.provider", "CACHE_PROVIDER")
	_ = v.BindEnv("cache.redis.host", "REDIS_SERVER_HOST")
	_ = v.BindEnv("cache.redis.port", "REDIS_SERVER_PORT")
	_ = v.BindEnv("cache.redis.username", "REDIS_USER_NAME")
	_ = v.BindEnv("cache.redis.password", "REDIS_USER_PASSWORD")
	// 4) Defaults
	v.SetDefault("security.secret_key", "")

	v.SetDefault("http.port", 8080)
	v.SetDefault("http.read_timeout", "180s")
	v.SetDefault("http.write_timeout", "180s")

	v.SetDefault("postgres.max_open_conns", 25)
	v.SetDefault("postgres.max_idle_conns", 25)
	v.SetDefault("postgres.conn_max_lifetime", "5m")
	v.SetDefault("postgres.driver", "postgres")

	v.SetDefault("rabbitmq.prefetch_count", 1)
	v.SetDefault("rabbitmq.retry_delay", "1s")
	v.SetDefault("rabbitmq.exchange_name", "nudgebee-relay")
	v.SetDefault("rabbitmq.request_queue", "nudgebee_relay_request")
	v.SetDefault("rabbitmq.host", "")
	v.SetDefault("rabbitmq.port", 5672)
	v.SetDefault("rabbitmq.username", "")
	v.SetDefault("rabbitmq.password", "")
	v.SetDefault("rabbitmq.url", "")
	v.SetDefault("rabbitmq.exchange_name", "nudgebee-relay")
	v.SetDefault("rabbitmq.message_ttl", "1m")

	v.SetDefault("signing.private_key", "")
	v.SetDefault("signing.key_id", "default")

	v.SetDefault("cache.provider", "in_memory")
	v.SetDefault("cache.redis.host", "localhost")
	v.SetDefault("cache.redis.port", 6379)
	v.SetDefault("cache.redis.username", "")
	v.SetDefault("cache.redis.password", "")

	v.SetDefault("otel_service_name", SERVICE_NAME)
	v.SetDefault("otel_exporter", "console")
	v.SetDefault("otel_exporter_otlp_endpoint", "127.0.0.1:4317")
	v.SetDefault("otel.grpc_timeout_seconds", 5)
	v.SetDefault("otel.grpc_max_msg_size", 8*1024*1024)

	// 6) Unmarshal
	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, err
	}

	if cfg.Otel.ExporterOtlpEndpoint == "" {
		cfg.Otel.ExporterOtlpEndpoint = "127.0.0.1:4317"
	}

	if cfg.Otel.ExporterOtlpTracesEndpoint == "" {
		cfg.Otel.ExporterOtlpTracesEndpoint = cfg.Otel.ExporterOtlpEndpoint
	}

	if cfg.Otel.ExporterOtlpMetricsEndpoint == "" {
		cfg.Otel.ExporterOtlpMetricsEndpoint = cfg.Otel.ExporterOtlpEndpoint
	}

	if cfg.Otel.Exporter == "" {
		cfg.Otel.Exporter = "noop"
	}
	if cfg.Otel.TracesExporter == "" {
		cfg.Otel.TracesExporter = cfg.Otel.Exporter
	}
	if cfg.Otel.MetricesExporter == "" {
		cfg.Otel.MetricesExporter = cfg.Otel.Exporter
	}
	if cfg.RabbitMQ.URL == "" {
		if cfg.RabbitMQ.Host != "" && cfg.RabbitMQ.Username != "" {
			cfg.RabbitMQ.URL = fmt.Sprintf(
				"amqp://%s:%s@%s:%d/",
				cfg.RabbitMQ.Username,
				cfg.RabbitMQ.Password,
				cfg.RabbitMQ.Host,
				cfg.RabbitMQ.Port,
			)
		} else {
			// ultimate fallback
			cfg.RabbitMQ.URL = "amqp://guest:guest@localhost:5672/"
		}
	}

	return &cfg, nil
}
