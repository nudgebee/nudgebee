package config

import (
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/spf13/viper"
)

func (c *appConfig) GetString(key string, defaultValue string) string {
	val := viper.GetString(key)
	if val == "" {
		return defaultValue
	}
	return val
}

func (c *appConfig) GetInt(key string, defaultValue int) int {
	if !viper.IsSet(key) {
		return defaultValue
	}
	return viper.GetInt(key)
}

func (c *appConfig) GetBool(key string, defaultValue bool) bool {
	if !viper.IsSet(key) {
		return defaultValue
	}
	return viper.GetBool(key)
}

var Config appConfig

const SERVICE_NAME = "llm-server"

type appConfig struct {
	Port string `mapstructure:"port"`

	// AI Assistant identity (white-labeling)
	AIAssistantName    string `mapstructure:"llm_server_ai_assistant_name"`
	AIAssistantCompany string `mapstructure:"llm_server_ai_assistant_company"`

	NudgebeeEncryptionKey string `mapstructure:"nudgebee_encryption_key"`

	ServiceApiServerToken       string `mapstructure:"action_api_server_token"`
	ServiceApiServerTokenHeader string `mapstructure:"action_api_server_token_header"`
	ServiceEndpoint             string `mapstructure:"service_api_server_url"`
	// ServiceApiServerTimeoutSeconds caps the time allowed for calls to the main services-server API.
	ServiceApiServerTimeoutSeconds int `mapstructure:"service_api_server_timeout_seconds"`

	WorkflowServerEndpoint string `mapstructure:"workflow_server_url"`

	LlmServerTokenHeader     string `mapstructure:"llm_server_token_header"`
	LlmServerToken           string `mapstructure:"llm_server_token"`
	LlmServerUrl             string `mapstructure:"llm_server_url"`
	LlmServerDBUrl           string `mapstructure:"llm_server_db_url"`
	LlmServerDBMaxConnection int    `mapstructure:"llm_server_db_max_connection"`
	LlmServerDBMinConnection int    `mapstructure:"llm_server_db_min_connection"`
	// LlmServerDBIdleMinutes defines how long a database connection can remain idle before being closed.
	LlmServerDBIdleMinutes int `mapstructure:"llm_server_db_idle_minutes"`

	Env string `mapstructure:"env"`

	BaseUrl string `mapstructure:"base_url"`

	RelayServerEndpoint  string `mapstructure:"relay_server_endpoint"`
	RelayServerSecretKey string `mapstructure:"relay_server_secret_key"`
	LlmServerJwtSecret   string `mapstructure:"llm_server_jwt_secret"`

	OtelServiceName          string `mapstructure:"otel_service_name"`
	OtelExporterOtlpEndpoint string `mapstructure:"otel_exporter_otlp_endpoint"`

	OtelExporter                   string `mapstructure:"otel_exporter"`
	OtelTracesExporter             string `mapstructure:"otel_traces_exporter"`
	OtelExporterOtlpTracesEndpoint string `mapstructure:"otel_exporter_otlp_traces_endpoint"`

	OtelMetricsExporter             string `mapstructure:"otel_metrics_exporter"`
	OtelExporterOtlpMetricsEndpoint string `mapstructure:"otel_exporter_otlp_metrics_endpoint"`
	OtelLogsExporter                string `mapstructure:"otel_logs_exporter"`

	// OtelGrpcTimeoutSeconds caps the duration of any individual gRPC call to the OTEL collector.
	OtelGrpcTimeoutSeconds int `mapstructure:"otel_grpc_timeout_seconds"`
	OtelGrpcMaxMsgSize     int `mapstructure:"otel_grpc_max_msg_size"`

	LogsStreamToFetch int    `mapstructure:"logs_stream_to_fetch"`
	RAGServerUrl      string `mapstructure:"rag_server_url"`

	// LLM specific configs
	LlmProvider               string `mapstructure:"llm_provider"`
	LlmModel                  string `mapstructure:"llm_model_name"`
	LlmModelFallbacks         string `mapstructure:"llm_model_fallbacks"`
	LlmProviderApiEndpoint    string `mapstructure:"llm_provider_api_endpoint"`
	LlmProviderApiKey         string `mapstructure:"llm_provider_api_key"`
	LlmProviderApiVersion     string `mapstructure:"llm_provider_api_version"`
	LlmProviderApiType        string `mapstructure:"llm_provider_api_type"`
	LlmProviderRegion         string `mapstructure:"llm_provider_region"`
	LlmProviderAccessKey      string `mapstructure:"llm_provider_access_key"`
	LlmProviderSecretKey      string `mapstructure:"llm_provider_secret_key"`
	LlmProviderSessionToken   string `mapstructure:"llm_provider_session_token"`
	LlmProviderEnbeddingModel string `mapstructure:"llm_provider_embedding_model"`
	LlmProviderMaxRetries     int    `mapstructure:"llm_provider_max_retries"`
	LlmProviderThinkingLevel  string `mapstructure:"llm_provider_thinking_level"` // empty (default): use per-model default; "minimal"/"low"/"medium"/"high": explicit level
	// LlmCacheTTLMinutes defines the lifespan of LLM request/response pairs in the cache.
	LlmCacheTTLMinutes int  `mapstructure:"llm_cache_ttl_minutes"`
	LlmEnableCaching   bool `mapstructure:"llm_enable_caching"`

	// LlmServerMaxIndividualCallTimeoutMinutes caps the duration of a single LLM request.
	// Prevents the system from hanging indefinitely if a provider (like Google AI) stalls.
	LlmServerMaxIndividualCallTimeoutMinutes int `mapstructure:"llm_server_max_individual_call_timeout_minutes"`

	// LlmServerGlobalRetryBudgetMinutes caps the total time spent on a single agent step,
	// including the initial call and all subsequent retries/continuations.
	// This ensures a single step doesn't consume the entire request budget.
	LlmServerGlobalRetryBudgetMinutes int `mapstructure:"llm_server_global_retry_budget_minutes"`

	// Lite model for summarization/fast tasks
	LlmModelLite string `mapstructure:"llm_model_lite_name"`

	// Agent specific configs
	LLMServerAgentReWooMaxIterations          int `mapstructure:"llm_server_agent_rewoo_max_iterations"`
	LLMServerAgentReWooMaxParallel            int `mapstructure:"llm_server_agent_rewoo_max_parallel"`
	LLMServerAgentReActMaxIterations          int `mapstructure:"llm_server_agent_react_max_iterations"`
	LLMServerAgentReActSubAgentMaxIterations  int `mapstructure:"llm_server_agent_react_sub_agent_max_iterations"`
	LLMServerAgentPromqlMaxIterations         int `mapstructure:"llm_server_agent_promql_max_iterations"`
	LLMServerAgentObservabilityMaxIterations  int `mapstructure:"llm_server_agent_observability_max_iterations"`
	LLMServerAgentObservabilityTimeoutSeconds int `mapstructure:"llm_server_agent_observability_timeout_seconds"`
	// LlmServerAgentPromqlCacheTTLMinutes defines the lifespan of PromQL query results in the cache.
	LlmServerAgentPromqlCacheTTLMinutes         int `mapstructure:"llm_server_agent_promql_metrics_cache_ttl_minutes"`
	LlmServerAgentPromqlMaxToolRespChars        int `mapstructure:"llm_server_agent_promql_max_tool_response_chars"`
	LlmServerAgentPrometheusMaxInlineDataPoints int `mapstructure:"llm_server_agent_prometheus_max_inline_data_points"`
	LLMServerAgentMaxLogLines                   int `mapstructure:"llm_server_agent_max_loglines"`
	LlmServerAgentMaxSqlRows                    int `mapstructure:"llm_server_agent_max_sqlrows"`
	LlmServerAgentMaxTracesRows                 int `mapstructure:"llm_server_agent_max_tracesrows"`
	LlmServerAgentMaxScratchpadChars            int `mapstructure:"llm_server_agent_max_scratchpad_chars"`
	LlmServerMaxGCTokens                        int `mapstructure:"llm_server_max_gc_tokens"`
	LlmServerMaxSkillContentLength int  `mapstructure:"llm_server_max_skill_content_length"`
	LlmServerIntegrationKBEnabled  bool `mapstructure:"llm_server_integration_kb_enabled"`
	// LlmServerMaxToolOutputLen caps a successful tool response at the source,
	// before it enters cache, DB, or scratchpad. 0 disables truncation.
	LlmServerMaxToolOutputLen int `mapstructure:"llm_server_max_tool_output_len"`
	// LlmServerMaxToolErrorOutputLen caps a failed tool response at the source.
	// Errors use a lower cap since stack traces tend to be repetitive. 0 disables.
	LlmServerMaxToolErrorOutputLen int `mapstructure:"llm_server_max_tool_error_output_len"`

	ServerName string `mapstructure:"llm_server_name"`
	// ServerHeartBeatFrequncySecond defines how often the server sends a heartbeat to indicate it is alive.
	ServerHeartBeatFrequncySecond int `mapstructure:"server_heartbeat_frequency_second"`
	// ServerHeartBeatTimeoutSecond defines the time after which a server is considered dead if no heartbeat is received.
	ServerHeartBeatTimeoutSecond int `mapstructure:"server_heartbeat_timeout_second"`

	CloudCollectorServerUrl   string `mapstructure:"cloud_collector_server_url"`
	CloudCollectorServerToken string `mapstructure:"cloud_collector_server_token"`

	RabbitMqUsername string `mapstructure:"rabbit_mq_username"`
	RabbitMqPassword string `mapstructure:"rabbit_mq_password"`
	RabbitMqHost     string `mapstructure:"rabbit_mq_host"`
	RabbitMqPort     int    `mapstructure:"rabbit_mq_port"`

	RabbitMqTroubleshootExchange    string `mapstructure:"rabbit_mq_troubleshoot_exchange"`
	RabbitMqTroubleshootQueue       string `mapstructure:"rabbit_mq_troubleshoot_queue"`
	LlmServerMqTroubleshootExchange string `mapstructure:"llm_server_mq_troubleshoot_exchange"`
	LlmServerMqTroubleshootQueue    string `mapstructure:"llm_server_mq_troubleshoot_queue"`

	// Investigation completion fan-out — published when a troubleshoot
	// request carries a task_token (i.e. originated from a runbook-server
	// workflow activity that is suspended waiting for the result).
	RabbitMqEventInvestigateCompletedExchange   string `mapstructure:"rabbit_mq_event_investigate_completed_exchange"`
	RabbitMqEventInvestigateCompletedRoutingKey string `mapstructure:"rabbit_mq_event_investigate_completed_routing_key"`
	// Fan-out exchange used by api-server to broadcast integration cache
	// invalidation events to every llm-server replica. Each pod binds an
	// auto-delete + exclusive queue ("<exchange>_<ServerName>") so every
	// pod receives every message.
	RabbitMqLLMCacheInvalidationExchange string `mapstructure:"rabbit_mq_llm_cache_invalidation_exchange"`

	LlmServerShellImage             string `mapstructure:"llm_server_tool_shell_image"`
	LlmToolCrawlDevtoolWebsocketUrl string `mapstructure:"llm_server_tool_crawl_devtool_websocket_url"`

	ConversationTaskWorkerCount    int `mapstructure:"llm_server_conversation_task_worker_count"`
	AuditApiWorkerCount            int `mapstructure:"llm_server_audit_api_worker_count"`
	AsyncApiWorkerCount            int `mapstructure:"llm_server_async_api_worker_count"`
	AsyncApiQueueSize              int `mapstructure:"llm_server_async_api_queue_size"`
	EventAnalysisWorkerCount       int `mapstructure:"llm_server_event_analysis_worker_count"`
	EventAnalysisQueueSize         int `mapstructure:"llm_server_event_analysis_queue_size"`
	EventAnalysisRecoveryBatchSize int `mapstructure:"llm_server_event_analysis_recovery_batch_size"`
	SyncDeadWorkerCount            int `mapstructure:"llm_server_sync_dead_worker_count"`
	SyncDeadQueueSize              int `mapstructure:"llm_server_sync_dead_queue_size"`
	// AsyncApiTimeoutSeconds caps the time allowed for asynchronous API requests.
	AsyncApiTimeoutSeconds int `mapstructure:"llm_server_async_api_timeout_seconds"`
	// AsyncOperationTimeoutSeconds caps the time allowed for individual asynchronous background operations.
	AsyncOperationTimeoutSeconds      int  `mapstructure:"llm_server_async_operation_timeout_seconds"`
	AsyncPlanExecutionWorkerCount     int  `mapstructure:"llm_server_async_plan_execution_worker_count"`
	AsyncRefWorkerCount               int  `mapstructure:"llm_server_async_ref_worker_count"`
	PlannerRewooParallelExecEnabled   bool `mapstructure:"llm_server_planner_rewoo_parallel_exec_enabled"`
	PlannerRewooInvestigationMaxSteps int  `mapstructure:"llm_server_planner_rewoo_investigation_max_steps"`
	PlannerRewooInfoMaxSteps          int  `mapstructure:"llm_server_planner_rewoo_info_max_steps"`

	LlmServerCodeAgentImage           string `mapstructure:"llm_server_agent_codeagent_image"`
	LlmServerCodeAgentNamespace       string `mapstructure:"llm_server_agent_codeagent_namespace"`
	LlmServerCodeAgentSecret          string `mapstructure:"llm_server_agent_codeagent_secret"`
	LlmServerCodeAgentMode            string `mapstructure:"llm_server_agent_codeagent_mode"`
	LlmServerCodeAgentLocalExecPath   string `mapstructure:"llm_server_agent_codeagent_local_exec_path"`
	LlmServerCodeAgentImagePullSecret string `mapstructure:"llm_server_agent_codeagent_image_pull_secret"`
	LlmServerSearchAgentProvider      string `mapstructure:"llm_server_agent_search_provider"`
	LlmServerSerperApiKey             string `mapstructure:"serper_api_key"`
	LlmServerJinaApiKey               string `mapstructure:"jina_api_key"`

	LlmServerWorkspaceEnabled bool `mapstructure:"llm_server_workspace_enabled"`
	// LlmServerWorkspaceKubeconfigPath optionally overrides the kubeconfig file used
	// when llm-server creates/manages the workspace pod. If empty, falls back to
	// in-cluster config, then $KUBECONFIG, then ~/.kube/config. Useful for local dev
	// where llm-server runs locally but the workspace pod should be on a remote cluster.
	LlmServerWorkspaceKubeconfigPath string `mapstructure:"llm_server_workspace_kubeconfig_path"`
	// LlmServerWorkspaceKubeContext optionally selects a specific context within the
	// kubeconfig (only applied when a kubeconfig file is loaded, not in-cluster).
	LlmServerWorkspaceKubeContext           string `mapstructure:"llm_server_workspace_kube_context"`
	LlmServerWorkspaceResourceLimitCpu      string `mapstructure:"llm_server_workspace_resource_limit_cpu"`
	LlmServerWorkspaceResourceLimitMemory   string `mapstructure:"llm_server_workspace_resource_limit_memory"`
	LlmServerWorkspaceResourceRequestCpu    string `mapstructure:"llm_server_workspace_resource_request_cpu"`
	LlmServerWorkspaceResourceRequestMemory string `mapstructure:"llm_server_workspace_resource_request_memory"`

	LlmServerShellToolEnabled              bool   `mapstructure:"llm_server_shell_tool_enabled"`
	LlmServerWorkspacePort                 int    `mapstructure:"llm_server_workspace_port"`
	LlmServerWorkspaceLocalUrl             string `mapstructure:"llm_server_workspace_local_url"`
	LlmServerWorkspaceFileMaxDownloadBytes int    `mapstructure:"llm_server_workspace_file_max_download_bytes"`

	NotificationServerUrl string `mapstructure:"notification_service_url"`
	TicketServerUrl       string `mapstructure:"ticket_server_url"`

	LlmServerSecurityMode string `mapstructure:"llm_server_security_mode"`

	// LlmServerRelayCommandExecutionTimeoutSeconds caps the time allowed for a single command to execute on a relay.
	LlmServerRelayCommandExecutionTimeoutSeconds int `mapstructure:"llm_server_relay_command_execution_timeout_seconds"`
	// LlmServerRelayPodExecutionTimeoutSeconds caps the time allowed for a pod-based operation to complete on a relay.
	LlmServerRelayPodExecutionTimeoutSeconds int `mapstructure:"llm_server_relay_pod_execution_timeout_seconds"`
	// LlmServerMCPDiscoveryTimeoutSeconds caps the time allowed for MCP tools/list discovery calls.
	LlmServerMCPDiscoveryTimeoutSeconds int `mapstructure:"llm_server_mcp_discovery_timeout_seconds"`
	// LlmServerMCPExecutionTimeoutSeconds caps the time allowed for MCP tools/call execution.
	LlmServerMCPExecutionTimeoutSeconds int `mapstructure:"llm_server_mcp_execution_timeout_seconds"`

	LlmServerLlmRetryAttempts int `mapstructure:"llm_server_llm_retry_attempts"`
	// LlmServerLlmInitialBackoffSeconds defines the starting delay for exponential backoff during LLM retries.
	LlmServerLlmInitialBackoffSeconds int `mapstructure:"llm_server_llm_initial_backoff_seconds"`

	LlmServerMaxConcurrentLlmCalls int `mapstructure:"llm_server_max_concurrent_llm_calls"`

	SecurityContextRetryAttempts int `mapstructure:"security_context_retry_attempts"`
	// SecurityContextInitialBackoffSeconds defines the starting delay for exponential backoff during security context retries.
	SecurityContextInitialBackoffSeconds int `mapstructure:"security_context_initial_backoff_seconds"`

	SummarizationWorkers   int `mapstructure:"llm_server_summarization_workers"`
	SummarizationQueueSize int `mapstructure:"llm_server_summarization_queue_size"`
	// KBSyncIntervalMinutes defines how often the knowledge base is synchronized.
	KBSyncIntervalMinutes int `mapstructure:"kb_sync_interval_minutes"`

	CacheProvider string `mapstructure:"cache_provider"`
	// CacheExpirationMinutes defines the default lifespan of items in the general purpose cache.
	CacheExpirationMinutes int `mapstructure:"cache_expiration_minutes"`
	// CacheToolConfigExpirationMin defines the lifespan of tool configurations in the cache.
	CacheToolConfigExpirationMin int    `mapstructure:"cache_tool_config_expiration_minutes"`
	CacheInMemorySizeMb          int    `mapstructure:"cache_inmemory_size_mb"`
	CacheInMemoryMaxEntries      int    `mapstructure:"cache_inmemory_max_entries"`
	CacheRedisUserName           string `mapstructure:"redis_user_name"`
	CacheRedisUserPassword       string `mapstructure:"redis_user_password"`
	CacheRedisServerHost         string `mapstructure:"redis_server_host"`
	CacheRedisServerPort         int    `mapstructure:"redis_server_port"`

	// Feature flags
	EnableEnhancedQueryAgentsResponse bool `mapstructure:"enable_enhanced_query_agents_response"`
	RemediationAgentEnabled           bool `mapstructure:"remediation_agent_enabled"`
	LlmSummarizationParallelEnabled   bool `mapstructure:"llm_server_summarization_parallel_enabled"`
	// LlmServerPreflightMaxMessageBytes is the per-message byte cap applied before every LLM call.
	// Messages exceeding this are hard-truncated to prevent token-limit errors from large payloads.
	// Default 0 means use the built-in default (1.5 MB). Set to -1 to disable.
	LlmServerPreflightMaxMessageBytes       int  `mapstructure:"llm_server_preflight_max_message_bytes"`
	ConversationContextEnabled              bool `mapstructure:"conversation_context_enabled"`
	EnableLLMReferenceTitleGeneration       bool `mapstructure:"enable_llm_reference_title_generation"`
	SlackCompactResponse                    bool `mapstructure:"llm_server_slack_compact_response"`
	LlmConfigAutoSelectionEnabled           bool `mapstructure:"llm_config_auto_selection_enabled"`
	LlmConfigAutoSelectionContextSteps      int  `mapstructure:"llm_config_auto_selection_context_steps"`
	LlmConfigAutoSelectionMaxObservationLen int  `mapstructure:"llm_config_auto_selection_max_observation_length"`
	ConversationHistoryWindowSize           int  `mapstructure:"conversation_history_window_size"`
	EnableLLMMetricsFiltering               bool `mapstructure:"enable_llm_metrics_filtering"`
	// DistillationRedistillInterval defines how many conversation turns occur between redistillation of context.
	DistillationRedistillInterval int  `mapstructure:"distillation_redistill_interval"`
	LlmServerReActCritiqueEnabled bool `mapstructure:"llm_server_react_critique_enabled"`
	LlmServerReAct3Enabled        bool `mapstructure:"llm_server_react3_enabled"`
	LlmServerRewooToReact3Enabled bool `mapstructure:"llm_server_rewoo_to_react3_enabled"`
	LlmServerThinkToolEnabled     bool `mapstructure:"llm_server_think_tool_enabled"`
	// KGToolsEnabled gates Knowledge Graph tools (kg_search_nodes, kg_traverse) on
	// the service_dependency_graph agent, enabling static topology + CALLS queries
	// alongside runtime metrics. Defaults to false — enable per-tenant for canary first.
	KGToolsEnabled bool `mapstructure:"llm_server_kg_tools_enabled"`
	// KGGetNodeEnabled independently gates the kg_get_node drill-down tool. Takes
	// effect only when KGToolsEnabled=true (kg_get_node is part of the KG family).
	// Split from KGToolsEnabled so the drill-down can be canaried or disabled
	// separately if its payload size or latency proves problematic.
	KGGetNodeEnabled bool `mapstructure:"llm_server_kg_get_node_enabled"`
	// ServiceDependencyGraphV2Enabled selects the KG-only V2 implementation of the
	// service_dependency_graph agent. V1 and V2 register under the SAME name
	// ("service_dependency_graph") and are mutually exclusive at process start —
	// exactly one of them runs RegisterNBAgentFactoryAndTool. Defaults to false.
	ServiceDependencyGraphV2Enabled bool `mapstructure:"llm_server_service_dependency_graph_v2_enabled"`
	// LlmServerSkillSelectionTopK, when > 0, enables question-aware skill selection.
	// At top-level entry the executor scores every active KB mapped to the agent
	// (or any inherited ancestor) against the user's question and keeps only the top
	// K. Both the eager-inline path used by custom-planner agents and the lazy
	// <skill-lists> + load_skills path used by ReAct/ReWoo planners are narrowed to
	// the same selection, which propagates unchanged through delegated sub-agents.
	// 0 (default) preserves the legacy "show every mapped skill" behaviour.
	LlmServerSkillSelectionTopK int `mapstructure:"llm_server_skill_selection_top_k"`

	// Scratchpad summarization: when enabled, older observations are summarized by an LLM
	// instead of blindly truncated to 100 bytes. This preserves analytical value (error
	// patterns, metric values, causal relationships) across long multi-step investigations.
	LlmServerScratchpadSummarizationEnabled bool `mapstructure:"llm_server_scratchpad_summarization_enabled"`
	// LlmServerScratchpadSummaryMaxLen is the target character budget for each LLM-generated
	// observation summary. The resulting summary is capped to this length.
	LlmServerScratchpadSummaryMaxLen int `mapstructure:"llm_server_scratchpad_summary_max_len"`
	// LlmServerScratchpadSummaryMinBytes is the minimum observation size that triggers
	// LLM summarization. Smaller observations fall through to byte truncation — LLM
	// summarization is not worth the latency and cost for small payloads.
	LlmServerScratchpadSummaryMinBytes int `mapstructure:"llm_server_scratchpad_summary_min_bytes"`
	// LlmServerScratchpadSummaryTimeoutMs caps the time allowed for a single observation
	// summarization call. On timeout, falls back to byte truncation.
	LlmServerScratchpadSummaryTimeoutMs int  `mapstructure:"llm_server_scratchpad_summary_timeout_ms"`
	EvaluationEnabled                   bool `mapstructure:"llm_server_evaluation_enabled"`
	AutoIdentifyAccountEnabled          bool `mapstructure:"llm_server_auto_identify_account_enabled"`

	// Termination cache configs
	LlmServerMessageTerminationCacheTTLSeconds int `mapstructure:"llm_server_message_termination_cache_ttl_seconds"`
	LlmServerMessageTerminatedCacheTTLMinutes  int `mapstructure:"llm_server_message_terminated_cache_ttl_minutes"`

	// Budget limits - monthly cost defaults
	TenantLlmDefaultBudgetLimitUserInvestigation  float64 `mapstructure:"llm_default_budget_limit_tenant_user_investigation"`
	TenantLlmDefaultBudgetLimitInvestigation      float64 `mapstructure:"llm_default_budget_limit_tenant_investigation"`
	AccountLlmDefaultBudgetLimitUserInvestigation float64 `mapstructure:"llm_default_budget_limit_account_user_investigation"`
	AccountLlmDefaultBudgetLimitInvestigation     float64 `mapstructure:"llm_default_budget_limit_account_investigation"`

	// Budget limits - daily cost defaults
	DailyDefaultCostLimitTenant  float64 `mapstructure:"llm_default_daily_cost_limit_tenant"`
	DailyDefaultCostLimitAccount float64 `mapstructure:"llm_default_daily_cost_limit_account"`

	// Count limits - monthly defaults (0 = block all, for unlimited set enabled=false)
	TenantLlmDefaultCountLimitUserInvestigation int `mapstructure:"llm_default_count_limit_tenant_user_investigation"`
	TenantLlmDefaultCountLimitInvestigation     int `mapstructure:"llm_default_count_limit_tenant_investigation"`

	// Count limits - daily default
	DailyDefaultCountLimitTenant int `mapstructure:"llm_default_daily_count_limit_tenant"`

	// Budget max caps - admins cannot exceed these values
	MaxMonthlyCostLimitTenant     float64 `mapstructure:"llm_max_monthly_cost_limit_tenant"`
	MaxMonthlyCostLimitAccount    float64 `mapstructure:"llm_max_monthly_cost_limit_account"`
	MaxDailyCostLimitTenant       float64 `mapstructure:"llm_max_daily_cost_limit_tenant"`
	MaxDailyCostLimitAccount      float64 `mapstructure:"llm_max_daily_cost_limit_account"`
	MaxMonthlyCountLimit          int     `mapstructure:"llm_max_monthly_count_limit"`
	MaxDailyCountLimit            int     `mapstructure:"llm_max_daily_count_limit"`
	MaxMemoryFactsPerConversation int     `mapstructure:"max_memory_facts_per_conversation"`
	ProductivityMetricsEnabled    bool    `mapstructure:"llm_server_productivity_metrics_enabled"`
	TicketV2Enabled               bool    `mapstructure:"llm_server_ticket_v2_enabled"`
	// FollowupResumeV2Enabled routes followup submissions through the clean
	// single-entry resume path (#28141) that uses conv-level locking and
	// looks up the agent's correct message_id from DB instead of trusting
	// the request's message_id. Falls back to legacy path when disabled.
	FollowupResumeV2Enabled bool `mapstructure:"llm_server_followup_resume_v2_enabled"`

	// AgentIntegrationPrecheckEnabled gates a fail-fast check that runs only
	// when a user invokes an agent via @<name>. If every tool the agent
	// declares requires an integration config and zero configs exist for the
	// account, the API short-circuits with a structured "missing integration"
	// response instead of letting the planner pick a tool that will fail.
	AgentIntegrationPrecheckEnabled bool `mapstructure:"llm_server_agent_integration_precheck_enabled"`

	// Long-term memory TTL settings.
	// MemoryTTLNeverUsedDays: delete memories that have never been retrieved after this many days.
	// MemoryTTLStaleDays: delete memories not retrieved in this many days (use_count > 0 but stale).
	// MemoryTTLCleanupIntervalHours: how often the cleanup job runs (0 = disabled).
	MemoryTTLNeverUsedDays        int `mapstructure:"llm_memory_ttl_never_used_days"`
	MemoryTTLStaleDays            int `mapstructure:"llm_memory_ttl_stale_days"`
	MemoryTTLCleanupIntervalHours int `mapstructure:"llm_memory_ttl_cleanup_interval_hours"`

	// LlmCircuitBreakerCooldownSeconds defines how long a model is placed in cooldown after hitting rate limits.
	LlmCircuitBreakerCooldownSeconds int `mapstructure:"llm_server_circuit_breaker_cooldown_seconds"`

	SyncDeadWorkerMessages bool `mapstructure:"llm_server_sync_dead_worker_messages"`

	// LLM Trace - logs full prompt messages and LLM responses for debugging
	LlmTraceEnabled bool `mapstructure:"llm_trace_enabled"`

	// Memory Module — layered memory architecture (Phase 1+)
	MemoryModuleEnabled     bool   `mapstructure:"memory_module_enabled"`
	MemoryLayerSoulEnabled  bool   `mapstructure:"memory_layer_soul_enabled"`
	MemoryLayerPrefsEnabled bool   `mapstructure:"memory_layer_preferences_enabled"`
	MemoryComposeEnabled    bool   `mapstructure:"memory_compose_enabled"`
	MemoryTenantAllowlist   string `mapstructure:"memory_tenant_allowlist"`
	MemorySoulMaxTokens     int    `mapstructure:"memory_soul_max_tokens"`
	MemoryPrefsMaxTokens    int    `mapstructure:"memory_prefs_max_tokens"`
	MemoryCacheTTLSeconds   int    `mapstructure:"memory_cache_ttl_seconds"`
	MemoryProjectionWorkers int    `mapstructure:"memory_projection_workers"`

	// Phase 2 layer toggles
	MemoryLayerPatternsEnabled   bool `mapstructure:"memory_layer_patterns_enabled"`
	MemoryLayerDecisionsEnabled  bool `mapstructure:"memory_layer_decisions_enabled"`
	MemoryLayerCollectiveEnabled bool `mapstructure:"memory_layer_collective_enabled"`
	MemoryPatternsMaxTokens      int  `mapstructure:"memory_patterns_max_tokens"`
	MemoryDecisionsMaxTokens     int  `mapstructure:"memory_decisions_max_tokens"`
	MemoryCollectiveMaxTokens    int  `mapstructure:"memory_collective_max_tokens"`

	// Phase 2 migration mode: shadow | dual | cutover | retired
	// Gated per-tenant at runtime via MemoryTenantAllowlist.
	MemoryMigrationMode string `mapstructure:"memory_migration_mode"`
	// Sample fraction for Shadow-mode parallel writes (0.0-1.0).
	MemoryShadowSampleFraction float64 `mapstructure:"memory_shadow_sample_fraction"`

	// Productivity dashboard tunables. The "Time Saved" widget compares each
	// completed investigation's AI runtime against a flat per-task manual
	// baseline; the "Savings" widget multiplies the resulting hours by an
	// engineer hourly rate. Both values are crude single-tier approximations
	// kept here so they can be tuned without a frontend redeploy. A per-task
	// complexity tier replaces the flat baseline in a later phase.
	ProductivityManualBaselineMinutes int     `mapstructure:"llm_productivity_manual_baseline_minutes"`
	ProductivityEngineerHourlyRateUsd float64 `mapstructure:"llm_productivity_engineer_hourly_rate_usd"`
}

func (a appConfig) SetString(key string, value string) {
	viper.Set(key, value)
}

// initialize based on environment variables using viper
func init() {
	viper.SetDefault("port", "8000")
	viper.SetConfigName("config")
	viper.SetConfigFile(".env")
	viper.SetConfigType("dotenv")
	viper.AddConfigPath(".")
	viper.AddConfigPath("./..")

	viper.SetDefault("llm_server_ai_assistant_name", "Nubi")
	viper.SetDefault("llm_server_ai_assistant_company", "Nudgebee")

	viper.SetDefault("nudgebee_encryption_key", "")

	viper.SetDefault("action_api_server_token_header", "X-ACTION-TOKEN")
	viper.SetDefault("llm_server_token_header", "X-ACTION-TOKEN")
	viper.SetDefault("llm_server_url", "http://llm-server:8000")

	viper.SetDefault("env", "")
	viper.SetDefault("service_api_server_url", "http://services-server:8000")
	viper.SetDefault("service_api_server_timeout_seconds", "10")

	// viper requires default values or bind.. else Unmarshal skips fields with no default values
	viper.SetDefault("action_api_server_token", "")
	viper.SetDefault("base_url", "http://nudgebee")

	viper.SetDefault("relay_server_endpoint", "http://127.0.0.1:52832")
	viper.SetDefault("relay_server_secret_key", "default")
	viper.SetDefault("llm_server_jwt_secret", "default-jwt-secret")

	viper.SetDefault("otel_service_name", SERVICE_NAME)
	viper.SetDefault("otel_provider", "noop")

	viper.SetDefault("llm_server_db_url", "")
	viper.SetDefault("logs_stream_to_fetch", 5)
	viper.SetDefault("rag_server_url", "http://127.0.0.1:9999")
	viper.SetDefault("llm_server_db_max_connection", 150)
	viper.SetDefault("llm_server_db_min_connection", 1)
	viper.SetDefault("llm_server_db_idle_minutes", 10)
	viper.SetDefault("llm_server_agent_react_max_iterations", 10)
	viper.SetDefault("llm_server_agent_react_sub_agent_max_iterations", 10)
	viper.SetDefault("llm_server_agent_rewoo_max_iterations", 10)
	viper.SetDefault("llm_server_agent_rewoo_max_parallel", 4)
	viper.SetDefault("llm_server_agent_promql_max_iterations", 4)
	viper.SetDefault("llm_server_agent_observability_max_iterations", 7)
	viper.SetDefault("llm_server_agent_observability_timeout_seconds", 180)
	viper.SetDefault("llm_server_agent_promql_metrics_cache_ttl_minutes", 5)
	viper.SetDefault("llm_server_agent_promql_max_tool_response_chars", 4000)
	viper.SetDefault("llm_server_agent_prometheus_max_inline_data_points", 5) // reduced from 10; above this threshold raw values are replaced with a stats summary to avoid context bloat
	viper.SetDefault("llm_server_planner_rewoo_investigation_max_steps", 6)
	viper.SetDefault("llm_server_planner_rewoo_info_max_steps", 1)

	viper.SetDefault("llm_server_agent_max_loglines", 100)
	viper.SetDefault("llm_server_agent_max_sqlrows", 10)
	viper.SetDefault("llm_server_agent_max_tracesrows", 10)
	viper.SetDefault("llm_server_agent_max_scratchpad_chars", 200000)
	viper.SetDefault("llm_server_max_skill_content_length", 5000)
	viper.SetDefault("llm_server_integration_kb_enabled", false)
	viper.SetDefault("llm_server_max_tool_output_len", 65536)
	viper.SetDefault("llm_server_max_tool_error_output_len", 16384)

	viper.SetDefault("llm_provider", "bedrock")
	// openai | aws_bedrock | sagemaker | azure | ollama | huggingface
	viper.SetDefault("llm_model_name", "arn:aws:bedrock:us-west-2:864186153326:inference-profile/us.meta.llama3-1-70b-instruct-v1:0") // "nudgebee.arn:aws:bedrock:us-east-1:280501305789:imported-model/h0oket1ako8o", "llama-3.3-70B-Instruct"
	viper.SetDefault("llm_model_fallbacks", "")
	viper.SetDefault("llm_provider_api_endpoint", "") // https://api.openai.com/v1 | https://nudgebee-slm.services.ai.azure.com | https://api-inference.huggingface.co
	viper.SetDefault("llm_provider_api_key", "")
	viper.SetDefault("llm_provider_api_version", "") // 2024-05-01-preview | 2024-05-01-preview | 2024-05-01-preview
	viper.SetDefault("llm_provider_api_type", "")    // openai | azure
	viper.SetDefault("llm_provider_region", "us-west-2")
	viper.SetDefault("llm_provider_access_key", "")
	viper.SetDefault("llm_provider_secret_key", "")
	viper.SetDefault("llm_provider_session_token", "")
	viper.SetDefault("llm_provider_embedding_model", "text-embedding-ada-002")
	viper.SetDefault("llm_provider_max_retries", 5)
	viper.SetDefault("llm_provider_thinking_level", "") // empty = not configured (use per-model default); "minimal"/"low"/"medium"/"high" = explicit level
	viper.SetDefault("llm_cache_ttl_minutes", 10)
	viper.SetDefault("llm_enable_caching", true)
	viper.SetDefault("llm_server_max_individual_call_timeout_minutes", 5)
	viper.SetDefault("llm_server_global_retry_budget_minutes", 10)

	// SLM specific configs for agents
	viper.SetDefault("llm_provider_promql_query", "")
	viper.SetDefault("llm_model_name_promql_query", "")
	viper.SetDefault("llm_tool_support_promql_query", "false") // Whether promql SLM supports tool calls or not
	viper.SetDefault("llm_provider_api_endpoint_promql_query", "")
	viper.SetDefault("llm_provider_api_key_promql_query", "")
	viper.SetDefault("llm_provider_api_version_promql_query", "")
	viper.SetDefault("llm_provider_api_type_promql_query", "")
	viper.SetDefault("llm_provider_region_promql_query", "")
	viper.SetDefault("llm_provider_require_adapter_id_promql_query", "false") // whether promql SLM supports adapter or not
	viper.SetDefault("llm_provider_adapter_id_promql_query", "")              // adapter repo id

	viper.SetDefault("llm_provider_logql_query", "")
	viper.SetDefault("llm_model_name_logql_query", "")
	viper.SetDefault("llm_tool_support_logql_query", "false")     // Whether logql SLM supports tool calls or not
	viper.SetDefault("llm_provider_api_endpoint_logql_query", "") // slm emdpoint
	viper.SetDefault("llm_provider_api_key_logql_query", "")
	viper.SetDefault("llm_provider_api_version_logql_query", "")
	viper.SetDefault("llm_provider_api_type_logql_query", "")
	viper.SetDefault("llm_provider_region_logql_query", "")
	viper.SetDefault("llm_provider_require_adapter_id_logql_query", "false") // whether logql SLM supports adapter or not
	viper.SetDefault("llm_provider_adapter_id_logql_query", "")              // adapter repo id

	viper.SetDefault("otel_service_name", SERVICE_NAME)
	viper.SetDefault("otel_exporter", "noop")
	viper.SetDefault("otel_exporter_otlp_endpoint", "127.0.0.1:4317")
	viper.SetDefault("otel_grpc_timeout_seconds", 5)
	viper.SetDefault("otel_grpc_max_msg_size", 8*1024*1024)
	viper.SetDefault("llm_server_max_gc_tokens", 1024)

	viper.SetDefault("server_heartbeat_frequency_second", 15)
	viper.SetDefault("server_heartbeat_timeout_second", 30)
	viper.SetDefault("llm_server_async_plan_execution_worker_count", 10)
	viper.SetDefault("llm_server_async_ref_worker_count", 10)
	viper.SetDefault("llm_server_async_api_worker_count", 100)
	viper.SetDefault("llm_server_async_api_queue_size", 1000)
	viper.SetDefault("llm_server_audit_api_worker_count", 5)
	viper.SetDefault("llm_server_conversation_task_worker_count", 20)
	viper.SetDefault("llm_server_event_analysis_worker_count", 5)
	viper.SetDefault("llm_server_event_analysis_queue_size", 100)
	viper.SetDefault("llm_server_event_analysis_recovery_batch_size", 5)
	viper.SetDefault("llm_server_sync_dead_worker_count", 3)
	viper.SetDefault("llm_server_sync_dead_queue_size", 50)

	viper.SetDefault("llm_server_planner_rewoo_parallel_exec_enabled", true)

	viper.SetDefault("CLOUD_COLLECTOR_SERVER_URL", "http://127.0.0.1:8000")
	viper.SetDefault("CLOUD_COLLECTOR_SERVER_TOKEN", "")

	viper.SetDefault("WORKFLOW_SERVER_URL", "http://workflow-server:8000")

	viper.SetDefault("rabbit_mq_username", "user")
	viper.SetDefault("rabbit_mq_password", "password")
	viper.SetDefault("rabbit_mq_host", "127.0.0.1")
	viper.SetDefault("rabbit_mq_port", 5672)

	viper.SetDefault("rabbit_mq_troubleshoot_exchange", "llm_server_event_investigate")
	viper.SetDefault("rabbit_mq_troubleshoot_queue", "llm_server_event_investigate")
	viper.SetDefault("llm_server_mq_troubleshoot_exchange", "llm_server_event_investigate")
	viper.SetDefault("llm_server_mq_troubleshoot_queue", "llm_server_event_investigate")
	viper.SetDefault("rabbit_mq_llm_cache_invalidation_exchange", "llm_cache_invalidation")

	viper.SetDefault("rabbit_mq_event_investigate_completed_exchange", "llm_server_event_investigate_completed")
	viper.SetDefault("rabbit_mq_event_investigate_completed_routing_key", "llm_server_event_investigate_completed")

	viper.SetDefault("LLM_SERVER_TOOL_CRAWL_DEVTOOL_WEBSOCKET_URL", "")

	viper.SetDefault("LLM_SERVER_TOOL_SHELL_IMAGE", "registry.nudgebee.com/nudgebee-debug:0.3.9")

	viper.SetDefault("llm_server_async_api_timeout_seconds", 15)
	viper.SetDefault("llm_server_async_operation_timeout_seconds", 5)
	viper.SetDefault("llm_server_agent_codeagent_namespace", "nudgebee")
	viper.SetDefault("llm_server_agent_codeagent_secret", "nudgebee")
	viper.SetDefault("llm_server_agent_codeagent_mode", "remote-cli") // remote-cli, remote-http, "local"
	viper.SetDefault("llm_server_agent_codeagent_image", "registry.dev.nudgebee.pollux.in/code-analysis-agent:0.0.4-rc-13")
	viper.SetDefault("llm_server_agent_codeagent_local_exec_path", "")
	viper.SetDefault("llm_server_agent_codeagent_image_pull_secret", "")
	viper.SetDefault("llm_server_agent_search_provider", "")
	viper.SetDefault("serper_api_key", "")
	viper.SetDefault("jina_api_key", "")

	viper.SetDefault("llm_server_workspace_enabled", true)
	viper.SetDefault("llm_server_workspace_resource_limit_cpu", "")
	viper.SetDefault("llm_server_workspace_resource_limit_memory", "")
	viper.SetDefault("llm_server_workspace_resource_request_cpu", "250m")
	viper.SetDefault("llm_server_workspace_resource_request_memory", "256Mi")
	viper.SetDefault("llm_server_shell_tool_enabled", true)
	viper.SetDefault("llm_server_workspace_port", 8080)
	viper.SetDefault("llm_server_workspace_local_url", "") // e.g. http://localhost:8080 for local dev
	viper.SetDefault("llm_server_workspace_file_max_download_bytes", 5*1024*1024)

	viper.SetDefault("notification_service_url", "http://notifications:8080")
	viper.SetDefault("ticket_server_url", "http://ticket-server:8080")

	viper.SetDefault("llm_server_llm_retry_attempts", 5)
	viper.SetDefault("llm_server_max_concurrent_llm_calls", 20)
	viper.SetDefault("llm_server_llm_initial_backoff_seconds", 1)
	viper.SetDefault("llm_server_relay_command_execution_timeout_seconds", 120)
	viper.SetDefault("llm_server_relay_pod_execution_timeout_seconds", 120)
	viper.SetDefault("llm_server_mcp_discovery_timeout_seconds", 15)
	viper.SetDefault("llm_server_mcp_execution_timeout_seconds", 120)

	viper.SetDefault("security_context_retry_attempts", 3)
	viper.SetDefault("security_context_initial_backoff_seconds", 1)

	viper.SetDefault("llm_server_summarization_workers", 2)
	viper.SetDefault("llm_server_summarization_queue_size", 100)
	viper.SetDefault("remediation_agent_enabled", false)

	viper.SetDefault("kb_sync_interval_minutes", 30)

	viper.SetDefault("cache_provider", "in_memory")
	viper.SetDefault("cache_expiration_minutes", 30)
	viper.SetDefault("cache_tool_config_expiration_minutes", 30) // Tool configs change rarely, cache for 30 min
	viper.SetDefault("cache_inmemory_size_mb", 20)
	viper.SetDefault("cache_inmemory_max_entries", 1000)
	viper.SetDefault("redis_server_host", "")
	viper.SetDefault("redis_server_port", 6379)
	viper.SetDefault("redis_user_name", "")
	viper.SetDefault("redis_user_password", "")

	// Feature flags - default to false to use old implementation
	viper.SetDefault("enable_enhanced_query_agents_response", true)
	viper.SetDefault("llm_server_summarization_parallel_enabled", true)
	viper.SetDefault("conversation_context_enabled", true)
	viper.SetDefault("conversation_history_window_size", 6)
	viper.SetDefault("distillation_redistill_interval", 6)
	viper.SetDefault("enable_llm_reference_title_generation", false)
	viper.SetDefault("llm_server_slack_compact_response", false)
	viper.SetDefault("llm_server_react_critique_enabled", false)
	viper.SetDefault("llm_server_react3_enabled", true)
	viper.SetDefault("llm_server_rewoo_to_react3_enabled", true)
	viper.SetDefault("llm_server_think_tool_enabled", true)
	viper.SetDefault("llm_server_kg_tools_enabled", false)
	viper.SetDefault("llm_server_kg_get_node_enabled", false)
	viper.SetDefault("llm_server_service_dependency_graph_v2_enabled", false)
	viper.SetDefault("llm_server_evaluation_enabled", false)
	viper.SetDefault("llm_server_auto_identify_account_enabled", false)

	viper.SetDefault("llm_server_message_termination_cache_ttl_seconds", 15)
	viper.SetDefault("llm_server_message_terminated_cache_ttl_minutes", 10)

	viper.SetDefault("llm_config_auto_selection_enabled", false)
	viper.SetDefault("llm_config_auto_selection_context_steps", 15)
	viper.SetDefault("llm_config_auto_selection_max_observation_length", 500)
	viper.SetDefault("enable_llm_metrics_filtering", true)
	// Budget limits - module-specific defaults applied when no tenant/account specific limit is set
	viper.SetDefault("llm_default_budget_limit_tenant_user_investigation", 1000.0)
	viper.SetDefault("llm_default_budget_limit_tenant_investigation", 1000.0)
	viper.SetDefault("llm_default_budget_limit_account_user_investigation", 400.0)
	viper.SetDefault("llm_default_budget_limit_account_investigation", 600.0)

	// Count limits - module-specific defaults (0 = block all, for unlimited set enabled=false, only tenant-level)
	viper.SetDefault("llm_default_count_limit_tenant_user_investigation", 500)
	viper.SetDefault("llm_default_count_limit_tenant_investigation", 500)

	// Daily cost defaults
	viper.SetDefault("llm_default_daily_cost_limit_tenant", 50.0)
	viper.SetDefault("llm_default_daily_cost_limit_account", 30.0)

	// Daily count default
	viper.SetDefault("llm_default_daily_count_limit_tenant", 50)

	// Max caps - admins cannot exceed these values
	viper.SetDefault("llm_max_monthly_cost_limit_tenant", 10000.0)
	viper.SetDefault("llm_max_monthly_cost_limit_account", 5000.0)
	viper.SetDefault("llm_max_daily_cost_limit_tenant", 500.0)
	viper.SetDefault("llm_max_daily_cost_limit_account", 250.0)
	viper.SetDefault("llm_max_monthly_count_limit", 5000)
	viper.SetDefault("llm_max_daily_count_limit", 500)
	viper.SetDefault("max_memory_facts_per_conversation", 30)
	viper.SetDefault("llm_memory_ttl_never_used_days", 90)
	viper.SetDefault("llm_memory_ttl_stale_days", 180)
	viper.SetDefault("llm_memory_ttl_cleanup_interval_hours", 24)

	viper.SetDefault("llm_server_productivity_metrics_enabled", false)

	viper.SetDefault("llm_server_productivity_metrics_enabled", false)

	viper.SetDefault("llm_server_ticket_v2_enabled", true)

	viper.SetDefault("llm_server_followup_resume_v2_enabled", false)

	viper.SetDefault("llm_server_agent_integration_precheck_enabled", true)

	viper.SetDefault("llm_server_circuit_breaker_cooldown_seconds", 60)

	viper.SetDefault("llm_server_sync_dead_worker_messages", true)

	viper.SetDefault("llm_trace_enabled", false)

	// Memory Module defaults — all off
	viper.SetDefault("memory_module_enabled", false)
	viper.SetDefault("memory_layer_soul_enabled", false)
	viper.SetDefault("memory_layer_preferences_enabled", false)
	viper.SetDefault("memory_compose_enabled", false)
	viper.SetDefault("memory_tenant_allowlist", "")
	viper.SetDefault("memory_soul_max_tokens", 100)
	viper.SetDefault("memory_prefs_max_tokens", 400)
	viper.SetDefault("memory_cache_ttl_seconds", 300)
	viper.SetDefault("memory_projection_workers", 4)

	viper.SetDefault("memory_layer_patterns_enabled", false)
	viper.SetDefault("memory_layer_decisions_enabled", false)
	viper.SetDefault("memory_layer_collective_enabled", false)
	viper.SetDefault("memory_patterns_max_tokens", 300)
	viper.SetDefault("memory_decisions_max_tokens", 200)
	viper.SetDefault("memory_collective_max_tokens", 300)

	viper.SetDefault("memory_migration_mode", "off") // off | shadow | dual | cutover | retired
	viper.SetDefault("memory_shadow_sample_fraction", 1.0)

	viper.SetDefault("llm_productivity_manual_baseline_minutes", 25)
	viper.SetDefault("llm_productivity_engineer_hourly_rate_usd", 5.0)

	viper.SetDefault("llm_server_scratchpad_summarization_enabled", true)

	hostName, err := os.Hostname()
	if err != nil {
		slog.Error("Unable to get hostname", "error", err)
		hostName = "127.0.0.1"
	}

	viper.SetDefault("llm_server_name", hostName)

	err = viper.ReadInConfig()
	if err != nil {
		fmt.Println("Unable to read config file:", err)
		wd, _ := os.Getwd()
		fmt.Println("Current Workdir:", wd)
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
	if Config.OtelMetricsExporter == "" {
		Config.OtelMetricsExporter = Config.OtelExporter
	}

	if data, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/namespace"); err == nil {
		namespace := strings.TrimSpace(string(data))
		if namespace != "" {
			Config.LlmServerCodeAgentNamespace = namespace
		}
	}
	// if max iteractions are default && react3 is enabled then use 50 as max iteractions
	if Config.LlmServerRewooToReact3Enabled && Config.LLMServerAgentReActMaxIterations <= 10 {
		Config.LLMServerAgentReActMaxIterations = 50
	}

	if Config.LlmServerRewooToReact3Enabled {
		Config.LlmServerReActCritiqueEnabled = true
	}
}
