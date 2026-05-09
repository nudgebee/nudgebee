package config

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/viper"
)

type Config struct {
	Server      ServerConfig      `mapstructure:"server"`
	Analysis    AnalysisConfig    `mapstructure:"analysis"`
	Git         GitConfig         `mapstructure:"git"`
	GitHub      GitHubConfig      `mapstructure:"github"`
	GitLab      GitLabConfig      `mapstructure:"gitlab"`
	Credentials CredentialsConfig `mapstructure:"credentials"`
	LLM         LLMConfig         `mapstructure:",squash"`
	Agent       AgentConfig       `mapstructure:"agent"`
	NudgeBee    NudgeBeeConfig    `mapstructure:"nudgebee"`
	Execution   ExecutionConfig   `mapstructure:"execution"`
}

type ServerConfig struct {
	Port            int           `mapstructure:"port"`
	ReadTimeout     time.Duration `mapstructure:"read_timeout"`
	WriteTimeout    time.Duration `mapstructure:"write_timeout"`
	MaxRequestSize  int64         `mapstructure:"max_request_size"`
	ShutdownTimeout time.Duration `mapstructure:"shutdown_timeout"`
}

type ExecutionConfig struct {
	WorkspaceDir string `mapstructure:"workspace_dir"`
}

type AnalysisConfig struct {
	MaxProcessingTime time.Duration `mapstructure:"max_processing_time"`
	WorkspaceDir      string        `mapstructure:"workspace_dir"`
	FuzzyThreshold    float64       `mapstructure:"fuzzy_threshold"`
	MaxResults        int           `mapstructure:"max_results"`
}

type GitConfig struct {
	CloneTimeout  time.Duration `mapstructure:"clone_timeout"`
	MaxRepoSize   int64         `mapstructure:"max_repo_size"`
	DefaultBranch string        `mapstructure:"default_branch"`
	UserName      string        `mapstructure:"user_name"`
	UserEmail     string        `mapstructure:"user_email"`
}

type GitHubConfig struct {
	BaseURL       string        `mapstructure:"base_url"`
	Timeout       time.Duration `mapstructure:"timeout"`
	RetryAttempts int           `mapstructure:"retry_attempts"`
}

type GitLabConfig struct {
	BaseURL       string        `mapstructure:"base_url"`
	Timeout       time.Duration `mapstructure:"timeout"`
	RetryAttempts int           `mapstructure:"retry_attempts"`
}

type CredentialsConfig struct {
	EncryptionKey string   `mapstructure:"encryption_key"`
	AllowedTypes  []string `mapstructure:"allowed_types"`
}

type NudgeBeeConfig struct {
	BaseURL string `mapstructure:"base_url"`
}

type LLMConfig struct {
	Provider       string `mapstructure:"llm_provider"`
	Model          string `mapstructure:"llm_model_name"`
	ApiEndpoint    string `mapstructure:"llm_provider_api_endpoint"`
	ApiKey         string `mapstructure:"llm_provider_api_key"`
	ApiVersion     string `mapstructure:"llm_provider_api_version"`
	ApiType        string `mapstructure:"llm_provider_api_type"`
	Region         string `mapstructure:"llm_provider_region"`
	MaxRetries     int    `mapstructure:"llm_provider_max_retries"`
	EmbeddingModel string `mapstructure:"llm_provider_embedding_model"`
}

type AgentConfig struct {
	ReActMaxIterations int           `mapstructure:"react_max_iterations"`
	ReWooMaxIterations int           `mapstructure:"rewoo_max_iterations"`
	MaxLogLines        int           `mapstructure:"max_log_lines"`
	MaxSearchResults   int           `mapstructure:"max_search_results"`
	BuildVerifyEnabled bool          `mapstructure:"build_verify_enabled"`
	BuildVerifyTimeout time.Duration `mapstructure:"build_verify_timeout"`
}

func LoadConfig() (*Config, error) {
	viper.SetConfigName("config")
	viper.SetConfigType("yaml")
	viper.AddConfigPath(".")
	viper.AddConfigPath("./config")

	// Load main config file
	if err := viper.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return nil, err
		}
	}

	// Set defaults
	viper.SetDefault("server.port", 8080)
	viper.SetDefault("server.read_timeout", "5m")
	viper.SetDefault("server.write_timeout", "0s")        // disabled — handler uses context.WithTimeout(MaxProcessingTime) instead
	viper.SetDefault("server.max_request_size", 10485760) // 10MB
	viper.SetDefault("server.shutdown_timeout", "30s")

	viper.SetDefault("analysis.max_processing_time", "30m")
	viper.SetDefault("analysis.workspace_dir", "/tmp/code-analysis")
	viper.SetDefault("analysis.fuzzy_threshold", 0.8)
	viper.SetDefault("analysis.max_results", 100)

	viper.SetDefault("execution.workspace_dir", "/tmp/code-analysis/exec_workspaces")

	viper.SetDefault("git.clone_timeout", "5m")
	viper.SetDefault("git.max_repo_size", 536870912) // 512MB
	viper.SetDefault("git.default_branch", "main")
	viper.SetDefault("git.user_name", "nudgebee-bot")
	viper.SetDefault("git.user_email", "bot@nudgebee.com")

	viper.SetDefault("github.base_url", "https://api.github.com")
	viper.SetDefault("github.timeout", "30s")
	viper.SetDefault("github.retry_attempts", 3)

	viper.SetDefault("gitlab.base_url", "https://gitlab.com")
	viper.SetDefault("gitlab.timeout", "30s")
	viper.SetDefault("gitlab.retry_attempts", 3)

	viper.SetDefault("nudgebee.base_url", "https://dev.nudgebee.pollux.in")

	viper.SetDefault("credentials.encryption_key", "default-key-change-in-production")
	viper.SetDefault("credentials.allowed_types", []string{"token", "ssh_key", "basic", "encrypted", "env_ref"})

	// LLM specific configs (consistent with llm-server env var naming)
	viper.SetDefault("llm_provider", "googleai")
	viper.SetDefault("llm_model_name", "gemini-2.5-pro")
	viper.SetDefault("llm_provider_api_endpoint", "")
	viper.SetDefault("llm_provider_api_key", "")
	viper.SetDefault("llm_provider_api_version", "")
	viper.SetDefault("llm_provider_api_type", "")
	viper.SetDefault("llm_provider_region", "us-west-2")
	viper.SetDefault("llm_provider_max_retries", 3)
	viper.SetDefault("llm_provider_embedding_model", "text-embedding-ada-002")

	// Agent specific configs
	viper.SetDefault("agent.react_max_iterations", 30)
	viper.SetDefault("agent.rewoo_max_iterations", 30)
	viper.SetDefault("agent.max_log_lines", 50)
	viper.SetDefault("agent.max_search_results", 20)
	viper.SetDefault("agent.build_verify_enabled", true)
	viper.SetDefault("agent.build_verify_timeout", "5m")

	// Load secrets file if it exists (e.g., secrets.yaml or secrets.json)
	// This file should contain sensitive information and should not be committed to VCS.
	viper.SetConfigName("secrets") // Look for secrets.yaml or secrets.json
	viper.AddConfigPath(".")
	viper.AddConfigPath("./config")
	viper.AddConfigPath("/etc/nudgebee")   // Common path for system-wide secrets
	viper.AddConfigPath("$HOME/.nudgebee") // User-specific secrets

	if err := viper.MergeInConfig(); err != nil {
		// Ignore if secrets file is not found, but return other errors
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return nil, err
		}
	}

	// Reset config name to original for subsequent reads if any
	viper.SetConfigName("config")

	// Load environment variables from mounted secrets
	loadSecretsFromMountPath("/etc/secrets/nudgebee")

	// Enable automatic environment variable binding
	viper.AutomaticEnv()

	// Bind NudgeBee environment variables
	_ = viper.BindEnv("nudgebee.base_url", "BASE_URL")

	// Bind server environment variables
	_ = viper.BindEnv("server.port", "SERVER_PORT")

	// Explicitly bind environment variables for LLM config (consistent with llm-server)
	_ = viper.BindEnv("llm_provider", "LLM_PROVIDER")
	_ = viper.BindEnv("llm_model_name", "LLM_MODEL_NAME")
	// Also support LLM_MODEL for backward compatibility
	if os.Getenv("LLM_MODEL") != "" && os.Getenv("LLM_MODEL_NAME") == "" {
		_ = os.Setenv("LLM_MODEL_NAME", os.Getenv("LLM_MODEL"))
	}
	_ = viper.BindEnv("llm_provider_api_endpoint", "LLM_PROVIDER_API_ENDPOINT")
	_ = viper.BindEnv("llm_provider_api_key", "LLM_PROVIDER_API_KEY")
	_ = viper.BindEnv("llm_provider_api_version", "LLM_PROVIDER_API_VERSION")
	_ = viper.BindEnv("llm_provider_api_type", "LLM_PROVIDER_API_TYPE")
	_ = viper.BindEnv("llm_provider_region", "LLM_PROVIDER_REGION")
	_ = viper.BindEnv("llm_provider_max_retries", "LLM_PROVIDER_MAX_RETRIES")
	_ = viper.BindEnv("llm_provider_embedding_model", "LLM_PROVIDER_EMBEDDING_MODEL")

	// Bind credentials encryption key
	_ = viper.BindEnv("credentials.encryption_key", "NUDGEBEE_ENCRYPTION_KEY")

	var config Config
	if err := viper.Unmarshal(&config); err != nil {
		return nil, err
	}

	return &config, nil
}

// loadSecretsFromMountPath loads environment variables from mounted secret files
// Each file in the directory becomes an environment variable with the filename as the key
// and file content as the value
func loadSecretsFromMountPath(mountPath string) {
	// Check if the mount path exists
	if _, err := os.Stat(mountPath); os.IsNotExist(err) {
		return // Mount path doesn't exist, skip loading secrets
	}

	err := filepath.WalkDir(mountPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		// Skip directories and hidden files
		if d.IsDir() || strings.HasPrefix(d.Name(), ".") {
			return nil
		}

		// Read file content
		content, err := os.ReadFile(path)
		if err != nil {
			fmt.Printf("Warning: failed to read secret file %s: %v\n", path, err)
			return nil // Continue with other files
		}

		// Use filename as environment variable name
		envKey := d.Name()
		envValue := strings.TrimSpace(string(content))

		// Set environment variable
		if err := os.Setenv(envKey, envValue); err != nil {
			fmt.Printf("Warning: failed to set environment variable %s: %v\n", envKey, err)
			return nil
		}
		return nil
	})

	if err != nil {
		fmt.Printf("Warning: failed to load secrets from mount path %s: %v\n", mountPath, err)
	}
}
