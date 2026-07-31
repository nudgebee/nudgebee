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

func (c *appConfig) GetFloat64(key string, defaultValue float64) float64 {
	if !viper.IsSet(key) {
		return defaultValue
	}
	return viper.GetFloat64(key)
}

var Config appConfig

// SERVICE_NAME is the worker_type used in the leader-election table (nb_workers).
// Defaults to "llm-server" so production behavior is unchanged. Local developers
// can override via LLM_SERVER_SERVICE_NAME=llm-server-<dev> to opt out of the
// shared election pool — each dev's local llm-server then has its own pool of
// one, always wins the leader lease, and runs the watch dispatcher reliably.
var SERVICE_NAME = func() string {
	if v := os.Getenv("LLM_SERVER_SERVICE_NAME"); v != "" {
		return v
	}
	return "llm-server"
}()

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
	RAGServerToken    string `mapstructure:"rag_server_token"`

	// PII detector within the egressfilter family. When the master
	// LlmServerEgressFilterEnabled is on, the wrapper is installed for every
	// LLM call — but PII scrubbing itself is a per-tenant opt-in (see the
	// tenant_config `pii_enabled` column and EffectivePIIEnabled). Env-level
	// PII enable/disable was removed 2026-07-30 in favor of tenant control;
	// operators toggle the whole subsystem via the master switch.
	//
	// NER default and timeout below are the platform-wide baseline shown to
	// tenants as "Use platform default (X)" — a tenant admin can override on
	// their own config row. Secrets stay owned by the egressfilter secrets
	// detector, so the PII scrub request never sets scrub_secrets.
	LlmServerEgressFilterPIINerEnabled     bool `mapstructure:"llm_server_egressfilter_pii_ner_enabled"`
	LlmServerEgressFilterPIITimeoutSeconds int  `mapstructure:"llm_server_egressfilter_pii_timeout_seconds"`
	// Outage policy for the PII scrubber. Same vocabulary as the secrets
	// detector's mode (which owns "detect" / "enforce" / future "redact"),
	// so ops reason about one word across the family:
	//   - "detect"  (default) — scrubber up: scrub-and-forward; scrubber down:
	//                forward RAW to the LLM (fail-OPEN, availability wins).
	//   - "enforce" — scrubber up: scrub-and-forward; scrubber down:
	//                return an error (fail-CLOSED, no raw PII to the LLM
	//                under any circumstance). For regulated tenants
	//                (HIPAA / GDPR) where a bypass is unacceptable.
	// Any unrecognized value is treated as "detect" so a typo can never
	// silently escalate a tenant to fail-closed and break traffic.
	LlmServerEgressFilterPIIMode string `mapstructure:"llm_server_egressfilter_pii_mode"`
	MLK8sServerURL               string `mapstructure:"ml_k8s_server_url"`

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
	LlmProviderThinkingLevel  string `mapstructure:"llm_provider_thinking_level"`  // empty (default): use per-model default; "minimal"/"low"/"medium"/"high": explicit level
	LlmProviderThinkingBudget int    `mapstructure:"llm_provider_thinking_budget"` // -1 (default): use per-model default; 0: disable thinking; >0: explicit token budget — global override, wins over the per-tier budgets below
	// Per-tier thinking-token ceilings (ModelTier), applied when LlmProviderThinkingBudget is unset (-1). 0 leaves a tier uncapped.
	LlmThinkingBudgetReasoning int `mapstructure:"llm_thinking_budget_reasoning"`
	LlmThinkingBudgetRetrieval int `mapstructure:"llm_thinking_budget_retrieval"`
	LlmThinkingBudgetSummary   int `mapstructure:"llm_thinking_budget_summary"`
	// LlmCacheTTLMinutes defines the lifespan of LLM request/response pairs in the cache.
	LlmCacheTTLMinutes int  `mapstructure:"llm_cache_ttl_minutes"`
	LlmEnableCaching   bool `mapstructure:"llm_enable_caching"`

	// Outbound egressfilter master switch. When false, the LLM factory does NOT
	// install the egressfilter decorator at all — GetLLMModel returns the raw
	// provider unchanged, no payload serialization, no metric emission. Per-
	// detector flags below only take effect when this is true.
	//
	// This umbrella flag exists so the entire egressfilter subsystem can be
	// disabled with a single env, independent of which detectors are wired in.
	// Default false (off).
	LlmServerEgressFilterEnabled bool `mapstructure:"llm_server_egressfilter_enabled"`

	// Per-detector knobs. These only apply when LlmServerEgressFilterEnabled is true.
	//
	// Secrets detector — scans every outbound LLM payload for high-confidence
	// credential patterns. Mode controls action on a hit:
	//   "audit"   — detect + emit metrics/logs, do NOT block (safe rollout)
	//   "enforce" — block the call and return a EgressFilterError to the caller
	// Any other value is treated as "audit".
	LlmServerEgressFilterSecretsEnabled bool   `mapstructure:"llm_server_egressfilter_secrets_enabled"`
	LlmServerEgressFilterSecretsMode    string `mapstructure:"llm_server_egressfilter_secrets_mode"`

	// LlmServerEgressFilterAllowlist is a comma-separated list of values that
	// will be excluded from detection even when they match a rule. Typical
	// use is canonical docs samples (AWS's AKIAIOSFODNN7EXAMPLE, a GCP
	// "AIzaSyExampleKey…" snippet, etc.) that legitimately appear in prompts.
	// Without entries here, enforce mode would block any prompt quoting
	// vendor docs.
	//
	// Loaded once at startup; runtime changes require a process restart.
	// Whitespace around each value is trimmed; empty entries are skipped.
	LlmServerEgressFilterAllowlist string `mapstructure:"llm_server_egressfilter_allowlist"`

	// LlmServerMaxIndividualCallTimeoutMinutes caps the duration of a single LLM request.
	// Prevents the system from hanging indefinitely if a provider (like Google AI) stalls.
	LlmServerMaxIndividualCallTimeoutMinutes int `mapstructure:"llm_server_max_individual_call_timeout_minutes"`

	// Global default TTFT (time-to-first-token) timeout in seconds. Applies only
	// when a provider is explicitly enabled via LLM_PROVIDER_TTFT_TIMEOUT_ENABLED_<PROVIDER>=true
	// and does NOT have its own LLM_PROVIDER_TTFT_TIMEOUT_SECONDS_<PROVIDER> override.
	// See getLLMTTFTTimeout in agents/core/llm_config.go. The watchdog cancels and
	// retries the same model if no first streamed token arrives within this deadline.
	LlmProviderTTFTTimeoutSeconds int `mapstructure:"llm_provider_ttft_timeout_seconds"`

	// LlmServerGlobalRetryBudgetMinutes caps the total time spent on a single agent step,
	// including the initial call and all subsequent retries/continuations.
	// This ensures a single step doesn't consume the entire request budget.
	LlmServerGlobalRetryBudgetMinutes int `mapstructure:"llm_server_global_retry_budget_minutes"`

	// Lite model for summarization/fast tasks
	LlmModelLite string `mapstructure:"llm_model_lite_name"`

	// Agent specific configs
	LLMServerAgentMaxParallel                 int `mapstructure:"llm_server_agent_max_parallel"`
	LLMServerAgentReActMaxIterations          int `mapstructure:"llm_server_agent_react_max_iterations"`
	LLMServerAgentReActSubAgentMaxIterations  int `mapstructure:"llm_server_agent_react_sub_agent_max_iterations"`
	LLMServerAgentPromqlMaxIterations         int `mapstructure:"llm_server_agent_promql_max_iterations"`
	LLMServerAgentObservabilityMaxIterations  int `mapstructure:"llm_server_agent_observability_max_iterations"`
	LLMServerAgentObservabilityTimeoutSeconds int `mapstructure:"llm_server_agent_observability_timeout_seconds"`
	// LlmServerAgentPromqlCacheTTLMinutes defines the lifespan of PromQL query results in the cache.
	LlmServerAgentPromqlCacheTTLMinutes int `mapstructure:"llm_server_agent_promql_metrics_cache_ttl_minutes"`
	// LlmServerAgentSeriesMatchCacheTTLMinutes defines the lifespan of metrics_series_match
	// (workload family discovery) results in the cache. Defaults to 30m — series for a workload
	// change far slower than metric values, so a long TTL is cheap and cuts repeat lookups.
	LlmServerAgentSeriesMatchCacheTTLMinutes    int `mapstructure:"llm_server_agent_series_match_cache_ttl_minutes"`
	LlmServerAgentPromqlMaxToolRespChars        int `mapstructure:"llm_server_agent_promql_max_tool_response_chars"`
	LlmServerAgentPrometheusMaxInlineDataPoints int `mapstructure:"llm_server_agent_prometheus_max_inline_data_points"`
	LLMServerAgentMaxLogLines                   int `mapstructure:"llm_server_agent_max_loglines"`
	// Dev-only. Set to "jaeger" / "chronosphere" / etc. to bypass per-account trace
	// provider routing on the canonical (v2) path. Empty (default) lets services-server
	// resolve the account's default trace provider.
	LLMServerTraceProviderOverride   string `mapstructure:"llm_server_trace_provider_override"`
	LlmServerAgentMaxSqlRows         int    `mapstructure:"llm_server_agent_max_sqlrows"`
	LlmServerAgentMaxTracesRows      int    `mapstructure:"llm_server_agent_max_tracesrows"`
	LlmServerAgentMaxScratchpadChars int    `mapstructure:"llm_server_agent_max_scratchpad_chars"`
	LlmServerMaxGCBytes              int    `mapstructure:"llm_server_max_gc_bytes"`
	// LlmServerAgentAccountPromptMaxBytes caps the account GlobalContext
	// fragment attached to custom-planner agent LLM calls (log/trace/kubectl
	// intent generators, resource search). Distinct from
	// llm_server_max_gc_bytes, which limits the stored GC size at upload.
	LlmServerAgentAccountPromptMaxBytes int  `mapstructure:"llm_server_agent_account_prompt_max_bytes"`
	LlmServerMaxSkillContentLength      int  `mapstructure:"llm_server_max_skill_content_length"`
	LlmServerIntegrationKBEnabled       bool `mapstructure:"llm_server_integration_kb_enabled"`
	// LlmServerKBPrestepEnabled gates the KB pre-step: when on, the executor
	// retrieves relevant KB content before planning and places it (plus the
	// skill-lists menu) in the human message instead of the cacheable system
	// prefix. Off keeps the legacy in-prompt <skill-lists> + lazy load_skills flow.
	LlmServerKBPrestepEnabled bool `mapstructure:"llm_server_kb_prestep_enabled"`
	// LlmServerKBPrestepTimeoutSeconds bounds the pre-step's RAG call. The
	// default is sized for the reranked search (embed + query + one LLM rerank
	// call); the pre-step fails open on timeout, so setting this too low turns
	// reranking into silent knowledge loss. Values <= 0 fall back to the default.
	LlmServerKBPrestepTimeoutSeconds int `mapstructure:"llm_server_kb_prestep_timeout_seconds"`
	// LlmServerSkillDelegationPropagationEnabled, when on, propagates a delegating
	// agent's skill scope (its own name + the question-aware SelectedSkillIds) to the
	// sub-agents it delegates to. Skills are agent-scoped, so a runbook mapped to an
	// orchestrator otherwise never reaches the sub-agent that executes; with this on,
	// the sub-agent's own <skill-lists> menu surfaces the parent's selected runbooks
	// and its planner chooses whether to load_skills (no eager injection). Off keeps
	// today's behavior (only custom-planner agents thread skills explicitly).
	LlmServerSkillDelegationPropagationEnabled bool `mapstructure:"llm_server_skill_delegation_propagation_enabled"`
	// LlmServerToolSchemaValidationTools is a comma-separated allowlist of tool
	// names for which the framework treats the InputSchema as authoritative.
	// A tool on this list has BOTH of the following applied by the framework:
	//
	//   1. Its InputSchema is rendered into the AVAILABLE TOOLS block for the
	//      LLM as a compact "Input: object with fields:" line-list (via
	//      agents/core/utils.go:renderInputSchema) — the LLM sees required
	//      fields, types, and enum values at plan-time instead of having to
	//      infer them from prose.
	//   2. Its input is validated pre-execution against the schema (types,
	//      required, enums, RequiredOneOf) — handled by the coordinated
	//      schema-validation PR (piyushbhavsarr/nudgebee-enterprise#31271).
	//
	// The same allowlist gates BOTH intentionally. A tool whose schema is
	// authoritative enough to render is also authoritative enough to enforce.
	// Splitting the two would create tools where the LLM sees a schema the
	// framework won't hold it to — confusing feedback loop.
	//
	// Empty (default) = both features OFF for every tool. Special value "*"
	// = both ON for every registered tool.
	//
	// Per-tool gating is used here rather than a global on/off because most
	// existing tools have InputSchema declarations that don't match how their
	// Call() actually accepts input (schema says one required "command",
	// Call() unpacks a bunch of top-level fields — legacy pattern documented
	// in tool_postgres.go's Call() and elsewhere). Making those schemas
	// visible + enforced would push the LLM toward a different tool_input
	// shape than the agent prompt teaches, drifting the DB `parameters`
	// column and downstream consumers. The right sequence is: reconcile each
	// tool's schema with its Call() reality, then add the tool to this list,
	// then watch `parameters` shape stability, then flip the next tool.
	// Bootstrap default: "think", which is the tool that motivated the whole
	// feature (88% error rate in PR #33748 investigation) and has a schema
	// that already matches its Call() behaviour.
	LlmServerToolSchemaValidationTools string `mapstructure:"llm_server_tool_schema_validation_tools"`
	// LlmServerMaxToolOutputLen caps a successful tool response at the source,
	// before it enters cache, DB, or scratchpad. 0 disables truncation.
	LlmServerMaxToolOutputLen int `mapstructure:"llm_server_max_tool_output_len"`
	// LlmServerMaxToolErrorOutputLen caps a failed tool response at the source.
	// Errors use a lower cap since stack traces tend to be repetitive. 0 disables.
	LlmServerMaxToolErrorOutputLen int `mapstructure:"llm_server_max_tool_error_output_len"`

	// Image attachment support
	LlmServerImageMaxPerMessage int     `mapstructure:"llm_server_image_max_per_message"`
	LlmServerImageMaxSizeMB     float64 `mapstructure:"llm_server_image_max_size_mb"`

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
	AsyncOperationTimeoutSeconds  int  `mapstructure:"llm_server_async_operation_timeout_seconds"`
	AsyncPlanExecutionWorkerCount int  `mapstructure:"llm_server_async_plan_execution_worker_count"`
	AsyncRefWorkerCount           int  `mapstructure:"llm_server_async_ref_worker_count"`
	PlannerParallelExecEnabled    bool `mapstructure:"llm_server_planner_parallel_exec_enabled"`

	// DropExtraAgentMentions controls what happens to a repeated leading mention
	// run ("@a @b q") in the query handed to the agent. false (default) keeps the
	// extras -> "@b q"; true drops them -> "q". Routing always uses the first mention.
	DropExtraAgentMentions bool `mapstructure:"llm_server_drop_extra_agent_mentions"`

	LlmServerCodeAgentImage           string `mapstructure:"llm_server_agent_codeagent_image"`
	LlmServerCodeAgentNamespace       string `mapstructure:"llm_server_agent_codeagent_namespace"`
	LlmServerCodeAgentSecret          string `mapstructure:"llm_server_agent_codeagent_secret"`
	LlmServerCodeAgentMode            string `mapstructure:"llm_server_agent_codeagent_mode"`
	LlmServerCodeAgentLocalExecPath   string `mapstructure:"llm_server_agent_codeagent_local_exec_path"`
	LlmServerCodeAgentImagePullSecret string `mapstructure:"llm_server_agent_codeagent_image_pull_secret"`
	// LlmServerCodeAgentExtraEnv is a comma-separated KEY=VALUE list appended to
	// workspace pod env — the operator-facing knob for code-analysis flags (e.g.
	// "AGENT_HARNESS_VERIFY=true,AGENT_INLOOP_VERIFY=true") without an image or
	// code change. Workspace pods only pick it up when (re)created.
	LlmServerCodeAgentExtraEnv   string `mapstructure:"llm_server_agent_codeagent_extra_env"`
	LlmServerSearchAgentProvider string `mapstructure:"llm_server_agent_search_provider"`
	LlmServerSerperApiKey        string `mapstructure:"serper_api_key"`
	LlmServerJinaApiKey          string `mapstructure:"jina_api_key"`

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

	LlmServerShellToolEnabled bool `mapstructure:"llm_server_shell_tool_enabled"`
	// LogAgentV2Enabled gates the canonical, provider-independent fetch_logs
	// agent (FetchLogsAgentV2). Global per-deploy toggle; default false.
	LogAgentV2Enabled bool `mapstructure:"llm_server_log_agent_v2_enabled"`
	// K8sOrchestratorMode selects what the router-selected k8s_orchestrator runs.
	// Boot-time, per-deploy (rollback = change + redeploy). One of:
	//   "delegating" (default) — v1: route kubectl work through the `kubectl` sub-agent
	//   "direct"               — v2: hold `kubectl_execute` and run kubectl directly
	//   "lean"                 — EXPERIMENTAL: minimal principle-level prompt + critique off
	// Unknown/empty falls back to "delegating". Replaces the former
	// llm_server_k8s_orchestrator_{v2,lean}_enabled booleans. The @k8s_orchestrator_2
	// (always direct) and @k8s_orchestrator_lean (always lean) eval handles are
	// unaffected by this — they exist for side-by-side A/B regardless of mode.
	K8sOrchestratorMode string `mapstructure:"llm_server_k8s_orchestrator_mode"`
	// AwsOrchestratorMode selects what the router-selected aws_orchestrator runs.
	// Boot-time, per-deploy (rollback = change + redeploy). One of:
	//   "delegating" (default) — v1: route AWS resource CLI through the `aws` sub-agent
	//   "direct"               — v2: hold `aws_execute` and run the AWS CLI directly
	//   "lean"                 — EXPERIMENTAL: minimal principle-level prompt + direct aws_execute
	// (`aws_observability` stays delegated in all.) Unknown/empty falls back to
	// "delegating". The @aws_orchestrator_2 (always direct) and @aws_orchestrator_lean
	// (always lean) eval handles are unaffected — they exist for side-by-side A/B.
	AwsOrchestratorMode string `mapstructure:"llm_server_aws_orchestrator_mode"`
	// GcpOrchestratorMode selects what the router-selected gcp_orchestrator runs.
	// Boot-time, per-deploy (rollback = change + redeploy). One of:
	//   "delegating" (default) — v1: route GCP resource CLI through the `gcp` sub-agent
	//   "direct"               — v2: hold `gcloud_execute` and run the gcloud CLI directly
	//   "lean"                 — EXPERIMENTAL: minimal principle-level prompt + direct gcloud_execute
	// Unknown/empty falls back to "delegating". The @gcp_orchestrator_2 (always direct) and
	// @gcp_orchestrator_lean (always lean) eval handles are unaffected — they exist for side-by-side A/B.
	GcpOrchestratorMode string `mapstructure:"llm_server_gcp_orchestrator_mode"`
	// AzureOrchestratorMode selects what the router-selected azure_orchestrator runs.
	// Boot-time, per-deploy (rollback = change + redeploy). One of:
	//   "delegating" (default) — v1: route Azure resource CLI through the `azure` sub-agent
	//   "direct"               — v2: hold `azure_execute` and run the az CLI directly
	//   "lean"                 — EXPERIMENTAL: minimal principle-level prompt + direct azure_execute
	// Unknown/empty falls back to "delegating". The @azure_orchestrator_2 (always direct) and
	// @azure_orchestrator_lean (always lean) eval handles are unaffected — they exist for side-by-side A/B.
	AzureOrchestratorMode string `mapstructure:"llm_server_azure_orchestrator_mode"`
	// TraceAgentV2Enabled gates the canonical, provider-independent traces agent
	// (TracesDefaultAgentV2). Global per-deploy toggle; default false.
	TraceAgentV2Enabled                    bool   `mapstructure:"llm_server_trace_agent_v2_enabled"`
	LlmServerWorkspacePort                 int    `mapstructure:"llm_server_workspace_port"`
	LlmServerWorkspaceLocalUrl             string `mapstructure:"llm_server_workspace_local_url"`
	LlmServerWorkspaceFileMaxDownloadBytes int    `mapstructure:"llm_server_workspace_file_max_download_bytes"`

	NotificationServerUrl   string `mapstructure:"notification_service_url"`
	NotificationServerToken string `mapstructure:"notification_server_token"`
	TicketServerUrl         string `mapstructure:"ticket_server_url"`

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

	// LlmHFEnableThinking controls whether HF/vLLM OpenAI-compat requests opt out of chat-template
	// thinking mode. Default false (we send chat_template_kwargs.enable_thinking=false). Set true
	// only when a vLLM-served thinking model is intentionally used for reasoning-tier work.
	LlmHFEnableThinking bool `mapstructure:"llm_hf_enable_thinking"`

	LlmServerMaxConcurrentLlmCalls int `mapstructure:"llm_server_max_concurrent_llm_calls"`

	SecurityContextRetryAttempts int `mapstructure:"security_context_retry_attempts"`
	// SecurityContextInitialBackoffSeconds defines the starting delay for exponential backoff during security context retries.
	SecurityContextInitialBackoffSeconds int `mapstructure:"security_context_initial_backoff_seconds"`

	SummarizationWorkers   int `mapstructure:"llm_server_summarization_workers"`
	SummarizationQueueSize int `mapstructure:"llm_server_summarization_queue_size"`
	// KBSyncIntervalMinutes defines how often the knowledge base is synchronized.
	KBSyncIntervalMinutes int `mapstructure:"kb_sync_interval_minutes"`
	// KBProcessingStaleMinutes is how long an integration KB may sit in
	// 'processing' before the reconcile treats it as a failed load and resets
	// it to 'error'. Must be well above the longest real scrape to avoid
	// flipping a healthy in-progress scrape.
	KBProcessingStaleMinutes int `mapstructure:"kb_processing_stale_minutes"`

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
	// LlmServerSDGGroundingContractEnabled gates the critiquer's Rule 8
	// dependency-claim grounding contract. When on, the critiquer rejects
	// inter-service relationship claims ("X calls Y", "Y depends on Z") that
	// cite no evidence of any kind (SDG, ConfigMap value, log line, or kubectl
	// endpoint). Non-SDG evidence is accepted with a soft advisory — the rule
	// enforces grounding, not tool choice. Default off; enable per env after
	// monitoring `SDG_no_data_rate` on dev to confirm no over-firing.
	LlmServerSDGGroundingContractEnabled bool `mapstructure:"llm_server_sdg_grounding_contract_enabled"`
	// LlmServerThinkToolEnabled gates injection of the `think` tool into the
	// six orchestrator agents (k8s / aws / azure / gcp / datadog / finops).
	// Default flipped to false 2026-07-12 after 30d prod data showed the
	// tool has never produced actionable reasoning:
	//   - 88% of think calls were rejected by the in-Call narration guard
	//     (#33080) as "not reasoning, just narration"
	//   - Of the ~12% that passed, every sampled case was still narration
	//     that the guard's regex happened to miss ("I have enough info,
	//     will now provide final answer" x N)
	//   - Zero observed cases where think meaningfully changed the model's
	//     next action; the ReAct3 <thought> block already carries any
	//     genuine reasoning inline
	// Kept as a flag (not deleted) so ops can re-enable per-env for a
	// week-long observation window; if signal remains flat, follow-up
	// PR deletes the tool + all injection sites entirely.
	// Rollback: set LLM_SERVER_THINK_TOOL_ENABLED=true in the env.
	LlmServerThinkToolEnabled bool `mapstructure:"llm_server_think_tool_enabled"`
	// LlmServerReact3OrchestratorModeEnabled gates the react_3 role-split prompt
	// overlays: top-level planner instances get the orchestrator overlay (answer
	// contract, deliberate first-iteration thought, completion self-check) while
	// sub-agents get the executor overlay (stay in brief, surface anomalies as
	// notes). Off = both overlays absent, prompt identical to pre-split behavior.
	LlmServerReact3OrchestratorModeEnabled bool `mapstructure:"llm_server_react3_orchestrator_mode_enabled"`
	// LlmServerReact3QueryLeanPromptEnabled drops the heavy investigation overlays
	// (answer contract, notebook discipline, hypothesis tree) from the TOP-LEVEL
	// orchestrator prompt on a plain-retrieval turn ("list pods"), and keys that
	// lean prompt into its own cache slot so it does not thrash the full-prompt
	// slot. Sub-agents and investigation turns are unaffected. Off = no-op, prompt
	// and cache keys byte-identical to today. Opt-in for safe rollout.
	LlmServerReact3QueryLeanPromptEnabled bool `mapstructure:"llm_server_react3_query_lean_prompt_enabled"`
	// LlmServerReact3QueryModelDownshiftEnabled downshifts the MODEL TIER for a
	// TOP-LEVEL plain-retrieval turn ("list pods") on a Reasoning-tier orchestrator
	// from Reasoning (pro) to Summary (a cheaper/faster model): a query doesn't need
	// deep causal reasoning, only tool orchestration + formatting. It keys off the
	// SAME signal as the lean-prompt variant (promptVariantForRequest → non-investigation
	// top-level), so tier, prompt variant, and cache slot stay consistent — and the
	// LLM cache already keys on model, so it is cache-correct. Investigations and
	// sub-agents are unaffected. Off (default) = no-op, tier byte-identical to today.
	// Ship dark; enable after cheap-vs-pro validation on query answers.
	LlmServerReact3QueryModelDownshiftEnabled bool `mapstructure:"llm_server_react3_query_model_downshift_enabled"`
	// LlmServerReact3OrchestratorThinkingLevel is the thinking level applied to
	// the orchestrator's direction-setting LLM calls (first plan call of a turn
	// and post-critique refinement passes). Elevate-only: thinking level is
	// otherwise resolved dynamically per model/tier, and this override applies
	// only when it is above that baseline — it never lowers thinking. Executor
	// sub-agents and mid-loop iterations always keep the dynamic resolution.
	// Empty disables the override.
	LlmServerReact3OrchestratorThinkingLevel string `mapstructure:"llm_server_react3_orchestrator_thinking_level"`
	// KGToolsEnabled gates Knowledge Graph tools (kg_list_nodes, kg_list_path) on
	// the service_dependency_graph agent, enabling static topology + CALLS queries
	// alongside runtime metrics. Defaults to false — enable per-tenant for canary first.
	KGToolsEnabled bool `mapstructure:"llm_server_kg_tools_enabled"`
	// KGGetNodeEnabled independently gates the kg_get_node drill-down tool. Takes
	// effect only when KGToolsEnabled=true (kg_get_node is part of the KG family).
	// Split from KGToolsEnabled so the drill-down can be canaried or disabled
	// separately if its payload size or latency proves problematic.
	KGGetNodeEnabled bool `mapstructure:"llm_server_kg_get_node_enabled"`
	// LlmServerSkillSelectionTopK, when > 0, enables question-aware skill selection.
	// At top-level entry the executor scores every active KB mapped to the agent
	// (or any inherited ancestor) against the user's question and keeps only the top
	// K. Both the eager-inline path used by custom-planner agents and the lazy
	// <skill-lists> + load_skills path used by ReAct planners are narrowed to
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
	LlmServerScratchpadSummaryTimeoutMs int `mapstructure:"llm_server_scratchpad_summary_timeout_ms"`
	// LlmServerScratchpadMaxObservationChars is the per-observation byte cap applied in the
	// scratchpad as a hard safety net (single huge tool outputs are middle-truncated to this).
	// This is intentionally separate from llm_config_auto_selection_max_observation_length,
	// which governs the lightweight config auto-selection heuristic (default 500) and must not
	// double as the scratchpad cap. Default 65536; clamped to a 4096 minimum.
	LlmServerScratchpadMaxObservationChars int `mapstructure:"llm_server_scratchpad_max_observation_chars"`
	// LlmServerScratchpadCompressionActivationFraction is the fraction of the resolved model
	// context window at which scratchpad compression activates. Below this the scratchpad is
	// left uncompressed (subject only to the per-observation hard cap); compression of older
	// observations only kicks in as the scratchpad approaches the window. Gating on the real
	// window — instead of a flat step count — stops compression from firing on small
	// conversations. Default 0.75; values <=0 or >=1 fall back to the default.
	//
	// Same gate governs the refinement-focus compression path (#33897): when the critiquer
	// rejects an answer, pre-refinement observations are prioritized (compressed FIRST
	// under pressure) but no compression fires unless this activation threshold is crossed.
	LlmServerScratchpadCompressionActivationFraction float64 `mapstructure:"llm_server_scratchpad_compression_activation_fraction"`
	// LlmServerSubAgentEvidenceEnabled attaches a small, budget-bounded manifest of the
	// concrete tool calls a sub-agent actually ran (tool + input + short output digest) to
	// the observation the parent orchestrator ingests — carried as a SEPARATE field, not
	// concatenated into the observation text, so it is exempt from scratchpad compression
	// (the raw observation may be summarized as it ages; this distilled manifest survives
	// verbatim). Without it the parent sees only the sub-agent's prose conclusion and cannot
	// verify or reconcile the real artifacts (the query EXPLAIN'd, the grep that produced a
	// count, etc.). Default false (opt-in).
	LlmServerSubAgentEvidenceEnabled bool `mapstructure:"llm_server_sub_agent_evidence_enabled"`
	// LlmServerSubAgentEvidenceMaxChars is the HARD total byte budget for that manifest. The
	// whole block is capped here regardless of how many steps a sub-agent ran or how large
	// their raw outputs were — a multi-MB fetch_logs step cannot inflate the parent context
	// because the manifest is assembled newest-first (most-distilled steps win) and truncated
	// to this budget. Default 2048; clamped to a 256 minimum.
	LlmServerSubAgentEvidenceMaxChars int  `mapstructure:"llm_server_sub_agent_evidence_max_chars"`
	EvaluationEnabled                 bool `mapstructure:"llm_server_evaluation_enabled"`
	AutoIdentifyAccountEnabled        bool `mapstructure:"llm_server_auto_identify_account_enabled"`

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
	// EventsV2Enabled gates the events_v2 agent (deterministic structured
	// tools fronting raw SQL — see docs/architecture-decisions.md). Unlike
	// TicketV2Enabled, this does not redirect any existing alias to v2 —
	// events_v2 isn't wired into generic "events" routing — it's a pure kill
	// switch on whether an explicit @events_v2 mention resolves. Default false.
	EventsV2Enabled bool `mapstructure:"llm_server_events_v2_enabled"`
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

	// LogsStandardGrepEnabled exposes the standard_diagnostic_grep primitive
	// to the logs agent — a server-side pattern-bundle grep tool that avoids
	// one-LLM-turn-per-pattern latency. Default off; flip on after validating
	// the bundle set matches production log shapes. When off, the logs agent
	// falls back to LLM-emitted shell_execute greps as today.
	LogsStandardGrepEnabled bool `mapstructure:"llm_logs_standard_grep_enabled"`

	// Memory Module — layered memory architecture (Phase 1+)
	MemoryModuleEnabled     bool   `mapstructure:"llm_memory_module_enabled"`
	MemoryLayerSoulEnabled  bool   `mapstructure:"llm_memory_layer_soul_enabled"`
	MemoryLayerPrefsEnabled bool   `mapstructure:"llm_memory_layer_preferences_enabled"`
	MemoryComposeEnabled    bool   `mapstructure:"llm_memory_compose_enabled"`
	MemoryTenantAllowlist   string `mapstructure:"llm_memory_tenant_allowlist"`
	MemorySoulMaxTokens     int    `mapstructure:"llm_memory_soul_max_tokens"`
	MemoryPrefsMaxTokens    int    `mapstructure:"llm_memory_prefs_max_tokens"`
	MemoryCacheTTLSeconds   int    `mapstructure:"llm_memory_cache_ttl_seconds"`
	MemoryProjectionWorkers int    `mapstructure:"llm_memory_projection_workers"`

	// RAG projection for memory. When enabled, the memory module writes each
	// row into rag-server (in addition to Postgres) via an async outbox
	// worker, and Compose queries rag-server for query-relevance ranking
	// before falling back to the static ranker. Default off — flag OFF means
	// pre-flag behaviour is preserved everywhere. See docs/memory-rag-integration.md.
	MemoryRagEnabled             bool `mapstructure:"llm_memory_rag_enabled"`
	MemoryRagTimeoutMs           int  `mapstructure:"llm_memory_rag_timeout_ms"`
	MemoryRagOverfetchMultiplier int  `mapstructure:"llm_memory_rag_overfetch_multiplier"`
	// MemoryRerankEnabled turns on the llm-server-side LLM rerank of memory RAG
	// candidates. When on, each hybrid layer's cosine candidates are reordered/
	// filtered by a fast, non-thinking retrieval-tier LLM call (cacheable static
	// prompt) before hydration — collapsing topically-similar-but-irrelevant hits
	// that cosine alone can't separate. Default off (pre-flag behaviour: cosine
	// order only). Fail-open: any rerank miss keeps the cosine order.
	MemoryRerankEnabled bool `mapstructure:"llm_memory_rerank_enabled"`
	// MemoryRerankMinScore is an optional COARSE cosine pre-filter applied before
	// the LLM rerank: candidates whose normalized similarity (0..1) is below it
	// are dropped cheaply, shrinking the LLM rerank's input and cutting obvious
	// low-cosine tail without an LLM call. Default 0.8. It is blunt — cosine can't
	// separate same-domain candidates (which cluster high, ~0.85; that's the
	// reranker's job), so this removes only clearly-lower-scoring hits; the LLM
	// rerank still does the fine relevance filtering on the survivors. 0 disables
	// it. Fail-open: if the floor would leave fewer than 2 candidates it is skipped
	// and the full set reranked, so it can't starve the reranker. Composes ahead
	// of a future rag-side cross-encoder stage.
	MemoryRerankMinScore float64 `mapstructure:"llm_memory_rerank_min_score"`
	// MemoryRerank{WSimilarity,WRelevancy,WDecay} weight the three signals blended
	// into a pattern's final combined score after the LLM rerank: RAG cosine
	// similarity, the LLM-emitted relevancy (0..1), and the decayed pattern score.
	// Only the signals present for a row are used and their weights are
	// renormalized to sum to 1 (e.g. the static path has no similarity), so the
	// combined score stays on a 0..1 scale regardless of which signals exist.
	MemoryRerankWSimilarity float64 `mapstructure:"llm_memory_rerank_w_similarity"`
	MemoryRerankWRelevancy  float64 `mapstructure:"llm_memory_rerank_w_relevancy"`
	MemoryRerankWDecay      float64 `mapstructure:"llm_memory_rerank_w_decay"`
	// MemoryRerankThreshold drops patterns whose final combined score is below it,
	// after the rerank and before the top-N cap. Only applied when
	// MemoryRerankEnabled is on (it's part of the rerank feature; with rerank off
	// the combined score has no relevancy term, so filtering is skipped). Default
	// 0.6 — trims the low-relevance tail while keeping decay/similarity-strong
	// rows. 0 disables it even when rerank is on. Fail-open: never blanks a layer
	// that had candidates (keeps the single best row if all would be filtered).
	MemoryRerankThreshold float64 `mapstructure:"llm_memory_rerank_threshold"`
	// MemoryInjectEnabled is path A: when on, the query-dependent layers
	// (patterns/decisions/collective/inferred-prefs) are fetched (RAG-hybrid) +
	// reranked and injected into the prompt at assembly time, before the
	// orchestrator runs. When off, only the always-on ambient core (soul,
	// explicit prefs, session) is injected. Default false — the ambient core is
	// the baseline; the heavier query-dependent layers are opt-in per env.
	MemoryInjectEnabled bool `mapstructure:"llm_memory_inject_enabled"`
	// MemoryToolEnabled is path B: when on, the on-demand `memory` tool is enabled
	// so agents can search memory via keywords (plain DB fetch, no RAG/rerank).
	// Independent of MemoryInjectEnabled — either, both, or neither. Default off.
	MemoryToolEnabled            bool `mapstructure:"llm_memory_tool_enabled"`
	MemoryRagProjectorBatchSize  int  `mapstructure:"llm_memory_rag_projector_batch_size"`
	MemoryRagProjectorIntervalMs int  `mapstructure:"llm_memory_rag_projector_interval_ms"`
	MemoryRagProjectorWorkers    int  `mapstructure:"llm_memory_rag_projector_workers"`

	// Phase 2 layer toggles
	MemoryLayerPatternsEnabled   bool `mapstructure:"llm_memory_layer_patterns_enabled"`
	MemoryLayerDecisionsEnabled  bool `mapstructure:"llm_memory_layer_decisions_enabled"`
	MemoryLayerCollectiveEnabled bool `mapstructure:"llm_memory_layer_collective_enabled"`
	MemoryPatternsMaxTokens      int  `mapstructure:"llm_memory_patterns_max_tokens"`
	// MemoryPatternsPerKindLimit caps how many rows of a single pattern_kind
	// reach Compose. Without it one chatty kind (e.g. lots of
	// frequent_service rows) can crowd out frequent_namespace /
	// preferred_diagnostic_flow / etc. Within each kind the rows are
	// ordered by last_seen_at DESC (most recent first); pinned rows still
	// bypass this cap because pinning is an explicit "always show me this"
	// signal. Set to 0 to disable.
	MemoryPatternsPerKindLimit int `mapstructure:"llm_memory_patterns_per_kind_limit"`
	MemoryDecisionsMaxTokens   int `mapstructure:"llm_memory_decisions_max_tokens"`
	MemoryCollectiveMaxTokens  int `mapstructure:"llm_memory_collective_max_tokens"`

	// Phase 4 layer toggles
	MemoryLayerSessionEnabled bool `mapstructure:"llm_memory_layer_session_enabled"`
	MemorySessionMaxTokens    int  `mapstructure:"llm_memory_session_max_tokens"`
	MemorySessionIdleMinutes  int  `mapstructure:"llm_memory_session_idle_minutes"`

	// Phase 8 — scheduled maintenance jobs (per-layer distill / consolidate /
	// summarise). Each schedule is a standard 5-field cron string. The master
	// switch must be on for any of the per-job schedules to register.
	MemoryMaintenanceEnabled                 bool   `mapstructure:"llm_memory_maintenance_enabled"`
	MemoryMaintenanceSessionExpireSchedule   string `mapstructure:"llm_memory_maintenance_session_expire_schedule"`
	MemoryMaintenanceSessionDistillSchedule  string `mapstructure:"llm_memory_maintenance_session_distill_schedule"`
	MemoryMaintenanceSessionDistillBatchSize int    `mapstructure:"llm_memory_maintenance_session_distill_batch_size"`
	MemoryMaintenancePreferencesSchedule     string `mapstructure:"llm_memory_maintenance_preferences_schedule"`
	MemoryMaintenancePatternsSchedule        string `mapstructure:"llm_memory_maintenance_patterns_schedule"`
	MemoryMaintenanceEventsRotateSchedule    string `mapstructure:"llm_memory_maintenance_events_rotate_schedule"`
	MemoryMaintenanceCollectiveSchedule      string `mapstructure:"llm_memory_maintenance_collective_schedule"`
	MemoryMaintenanceSoulSchedule            string `mapstructure:"llm_memory_maintenance_soul_schedule"`
	MemoryMaintenanceDecisionsSchedule       string `mapstructure:"llm_memory_maintenance_decisions_schedule"`
	// MemoryMaintenancePatternsExtractSchedule drives the cross-conversation
	// pattern-extract job. Daily cadence
	// keeps the LLM bill bounded while still catching new recurrences within
	// a day of the second observation.
	MemoryMaintenancePatternsExtractSchedule string `mapstructure:"llm_memory_maintenance_patterns_extract_schedule"`
	// MemoryMaintenanceRagSweepSchedule drives the memory-v2 RAG
	// tombstone sweeper — hourly by default. See
	// memory/maintenance/patterns_rag_ttl_sweeper.go.
	MemoryMaintenanceRagSweepSchedule     string `mapstructure:"llm_memory_maintenance_rag_sweep_schedule"`
	MemoryMaintenanceRagSweepBatch        int    `mapstructure:"llm_memory_maintenance_rag_sweep_batch"`
	MemoryMaintenancePreferencesDecayDays int    `mapstructure:"llm_memory_maintenance_preferences_decay_days"`
	MemoryMaintenancePatternsRetireDays   int    `mapstructure:"llm_memory_maintenance_patterns_retire_days"`
	// MemoryMaintenancePatternsFadingDays / StaleDays drive the
	// active → fading → stale lifecycle that RunPatternsConsolidate writes
	// onto llm_memory_patterns.decay_state. The UI's filter chips read this
	// column; without the consolidator update they stay 'active' forever.
	// Defaults: 14 / 60.
	MemoryMaintenancePatternsFadingDays  int `mapstructure:"llm_memory_maintenance_patterns_fading_days"`
	MemoryMaintenancePatternsStaleDays   int `mapstructure:"llm_memory_maintenance_patterns_stale_days"`
	MemoryMaintenanceEventsRetentionDays int `mapstructure:"llm_memory_maintenance_events_retention_days"`

	// Productivity dashboard tunables. The "Time Saved" widget compares each
	// completed investigation's AI runtime against a flat per-task manual
	// baseline; the "Savings" widget multiplies the resulting hours by an
	// engineer hourly rate. Both values are crude single-tier approximations
	// kept here so they can be tuned without a frontend redeploy. A per-task
	// complexity tier replaces the flat baseline in a later phase.
	ProductivityManualBaselineMinutes int     `mapstructure:"llm_productivity_manual_baseline_minutes"`
	ProductivityEngineerHourlyRateUsd float64 `mapstructure:"llm_productivity_engineer_hourly_rate_usd"`

	// Watch (background-poll-and-notify) feature.
	// When enabled, agents can register a "watch" via the watch_resource tool;
	// a leader-elected dispatcher polls each watch's source on a fixed cadence,
	// evaluates a predicate, and notifies the user's conversation on termination.
	WatchEnabled               bool `mapstructure:"llm_server_watch_enabled"`
	WatchDispatcherIntervalSec int  `mapstructure:"llm_server_watch_dispatcher_interval_sec"`
	WatchWorkerCount           int  `mapstructure:"llm_server_watch_worker_count"`
	WatchWorkerQueueSize       int  `mapstructure:"llm_server_watch_worker_queue_size"`
	WatchMinIntervalSec        int  `mapstructure:"llm_server_watch_min_interval_sec"`
	WatchMaxDurationSec        int  `mapstructure:"llm_server_watch_max_duration_sec"`
	WatchMaxPerTenant          int  `mapstructure:"llm_server_watch_max_per_tenant"`
	WatchMaxFailures           int  `mapstructure:"llm_server_watch_max_failures"`
	WatchPrimingPollTimeoutSec int  `mapstructure:"llm_server_watch_priming_poll_timeout_sec"`
	WatchSubmitTimeoutSec      int  `mapstructure:"llm_server_watch_submit_timeout_sec"`
	WatchPollTimeoutSec        int  `mapstructure:"llm_server_watch_poll_timeout_sec"`
	WatchSummarizerTimeoutSec  int  `mapstructure:"llm_server_watch_summarizer_timeout_sec"`
	WatchDispatchBatchSize     int  `mapstructure:"llm_server_watch_dispatch_batch_size"`
	// WatchSqlSourceEnabled gates the sql watch source kind separately from
	// the overall feature flag. The sql source executes agent-authored
	// SELECTs against the metastore in a READ ONLY tx — the predicate stays
	// safe but the query has no per-tenant row filter (a SELECT on a
	// shared table will see all tenants). Default false until that path
	// runs under a tenant-scoped role / RLS.
	WatchSqlSourceEnabled bool `mapstructure:"llm_server_watch_sql_source_enabled"`
	// WatchBypassLeaderElection skips the leader-elected scheduler and runs
	// the dispatcher tick in a plain goroutine on every replica. Intended
	// for local demos where a shared dev DB has cluster pods holding the
	// lease. Never set this in a multi-replica production deployment.
	WatchBypassLeaderElection bool `mapstructure:"llm_server_watch_bypass_leader_election"`
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
	viper.SetDefault("llm_server_token", "")
	viper.SetDefault("notification_server_token", "")
	viper.SetDefault("base_url", "http://nudgebee")

	viper.SetDefault("relay_server_endpoint", "http://127.0.0.1:52832")
	viper.SetDefault("relay_server_secret_key", "default")
	viper.SetDefault("llm_server_jwt_secret", "default-jwt-secret")

	viper.SetDefault("otel_service_name", SERVICE_NAME)
	viper.SetDefault("otel_provider", "noop")

	viper.SetDefault("llm_server_db_url", "")
	viper.SetDefault("logs_stream_to_fetch", 5)
	viper.SetDefault("rag_server_url", "http://127.0.0.1:9999")
	viper.SetDefault("rag_server_token", "")
	viper.SetDefault("llm_server_egressfilter_pii_ner_enabled", false)
	viper.SetDefault("llm_server_egressfilter_pii_timeout_seconds", 10)
	viper.SetDefault("llm_server_egressfilter_pii_mode", "detect")
	viper.SetDefault("ml_k8s_server_url", "http://ml-k8s-server:9999")
	viper.SetDefault("llm_server_db_max_connection", 150)
	viper.SetDefault("llm_server_db_min_connection", 1)
	viper.SetDefault("llm_server_db_idle_minutes", 10)
	// 50 (up from 10) is the value the deleted ReWoo→ReAct3 upgrade used to set;
	// baking it in preserves that behavior now that orchestrating agents always run as ReAct3.
	viper.SetDefault("llm_server_agent_react_max_iterations", 50)
	viper.SetDefault("llm_server_agent_react_sub_agent_max_iterations", 10)
	viper.SetDefault("llm_server_agent_max_parallel", 4)
	viper.SetDefault("llm_server_agent_promql_max_iterations", 4)
	viper.SetDefault("llm_server_agent_observability_max_iterations", 7)
	viper.SetDefault("llm_server_agent_observability_timeout_seconds", 180)
	viper.SetDefault("llm_server_agent_promql_metrics_cache_ttl_minutes", 5)
	viper.SetDefault("llm_server_agent_series_match_cache_ttl_minutes", 30)
	viper.SetDefault("llm_server_agent_promql_max_tool_response_chars", 4000)
	viper.SetDefault("llm_server_agent_prometheus_max_inline_data_points", 5) // reduced from 10; above this threshold raw values are replaced with a stats summary to avoid context bloat

	viper.SetDefault("llm_server_agent_max_loglines", 100)
	viper.SetDefault("llm_server_trace_provider_override", "")
	viper.SetDefault("llm_server_agent_max_sqlrows", 10)
	viper.SetDefault("llm_server_agent_max_tracesrows", 10)
	viper.SetDefault("llm_server_agent_max_scratchpad_chars", 200000)
	viper.SetDefault("llm_server_max_skill_content_length", 5000)
	viper.SetDefault("llm_server_integration_kb_enabled", true)
	viper.SetDefault("llm_server_kb_prestep_enabled", false)
	viper.SetDefault("llm_server_kb_prestep_timeout_seconds", 12)
	viper.SetDefault("llm_server_skill_delegation_propagation_enabled", false)
	// Bootstrap: only `think` gets schema-authoritative treatment (renderer +
	// validator). Other tools stay text-description-only until their schema
	// is reconciled with their Call() acceptance shape. See
	// LlmServerToolSchemaValidationTools docstring.
	viper.SetDefault("llm_server_tool_schema_validation_tools", "think")
	viper.SetDefault("llm_server_max_tool_output_len", 65536)
	viper.SetDefault("llm_server_max_tool_error_output_len", 16384)

	viper.SetDefault("llm_provider", "bedrock")
	// openai | aws_bedrock | sagemaker | azure | ollama | huggingface
	// Examples:
	//   bedrock:        "arn:aws:bedrock:<region>:<aws-account-id>:inference-profile/<profile-id>"
	//   bedrock import: "arn:aws:bedrock:<region>:<aws-account-id>:imported-model/<model-id>"
	//   openai:         "gpt-4o", "gpt-4-turbo"
	//   azure:          deployment name configured in the Azure resource
	//   googleai:       "gemini-2.0-flash", "gemini-1.5-pro"
	//   huggingface:    "meta-llama/Llama-3.3-70B-Instruct"
	viper.SetDefault("llm_model_name", "")
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
	viper.SetDefault("llm_provider_thinking_level", "")  // empty = not configured (use per-model default); "minimal"/"low"/"medium"/"high" = explicit level
	viper.SetDefault("llm_provider_thinking_budget", -1) // -1: model default, 0: disable, >0: token budget (global override, wins over the per-tier budgets below)
	viper.SetDefault("llm_thinking_budget_reasoning", 16000)
	viper.SetDefault("llm_thinking_budget_retrieval", 8000)
	viper.SetDefault("llm_thinking_budget_summary", 4000)
	viper.SetDefault("llm_cache_ttl_minutes", 10)
	viper.SetDefault("llm_enable_caching", true)

	// Outbound egressfilter — wrapper installed and secrets detector on by
	// default, so `metadata.egressfilter` is populated, the UI chip appears,
	// and metrics fire out of the box. Mode defaults to "detect" so nothing
	// is ever blocked; operators flip mode to "enforce" once their audit
	// metrics show a clean FP baseline. When no ActionGate is registered,
	// "enforce" silently degrades to "detect" (see
	// security/egressfilter/action_gate.go). Master switch can still be
	// flipped off explicitly if an operator doesn't want the wrapper
	// installed at all.
	viper.SetDefault("llm_server_egressfilter_enabled", true)
	viper.SetDefault("llm_server_egressfilter_secrets_enabled", true)
	viper.SetDefault("llm_server_egressfilter_secrets_mode", "detect")
	// Required even though "" is the natural zero — viper.Unmarshal skips
	// fields with no default set, so without this line the env var
	// LLM_SERVER_EGRESSFILTER_ALLOWLIST is silently ignored in any
	// deployment that doesn't read a `.env` file (i.e. prod k8s).
	viper.SetDefault("llm_server_egressfilter_allowlist", "")
	viper.SetDefault("llm_server_max_individual_call_timeout_minutes", 5)
	viper.SetDefault("llm_server_global_retry_budget_minutes", 10)
	viper.SetDefault("llm_provider_ttft_timeout_seconds", 30)

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
	viper.SetDefault("llm_server_max_gc_bytes", 10240)
	viper.SetDefault("llm_server_agent_account_prompt_max_bytes", 8192)

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

	viper.SetDefault("llm_server_planner_parallel_exec_enabled", true)

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

	viper.SetDefault("LLM_SERVER_TOOL_SHELL_IMAGE", "ghcr.io/nudgebee/nudgebee-debug:0.3.12")

	viper.SetDefault("llm_server_async_api_timeout_seconds", 15)
	viper.SetDefault("llm_server_async_operation_timeout_seconds", 5)
	viper.SetDefault("llm_server_agent_codeagent_namespace", "nudgebee")
	viper.SetDefault("llm_server_agent_codeagent_secret", "nudgebee")
	viper.SetDefault("llm_server_agent_codeagent_mode", "remote-cli") // remote-cli, remote-http, "local"
	viper.SetDefault("llm_server_agent_codeagent_image", "ghcr.io/nudgebee/code-analysis-agent:latest")
	viper.SetDefault("llm_server_agent_codeagent_extra_env", "")
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
	viper.SetDefault("llm_server_log_agent_v2_enabled", false)
	viper.SetDefault("llm_server_drop_extra_agent_mentions", false)
	viper.SetDefault("llm_server_trace_agent_v2_enabled", false)
	// k8s_orchestrator mode: delegating (v1, default) | direct (v2) | lean (experimental).
	viper.SetDefault("llm_server_k8s_orchestrator_mode", "lean")
	viper.SetDefault("llm_server_aws_orchestrator_mode", "lean")
	viper.SetDefault("llm_server_gcp_orchestrator_mode", "lean")
	viper.SetDefault("llm_server_azure_orchestrator_mode", "lean")
	viper.SetDefault("llm_server_workspace_port", 8080)
	viper.SetDefault("llm_server_workspace_local_url", "") // e.g. http://localhost:8080 for local dev
	viper.SetDefault("llm_server_workspace_file_max_download_bytes", 5*1024*1024)

	viper.SetDefault("notification_service_url", "http://notifications:8080")
	viper.SetDefault("ticket_server_url", "http://ticket-server:8080")

	viper.SetDefault("llm_server_llm_retry_attempts", 5)
	viper.SetDefault("llm_server_max_concurrent_llm_calls", 20)
	viper.SetDefault("llm_server_llm_initial_backoff_seconds", 1)
	viper.SetDefault("llm_hf_enable_thinking", false)
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
	viper.SetDefault("kb_processing_stale_minutes", 30)

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
	// react_critique defaults to true: the ReWoo→ReAct3 upgrade (now permanent)
	// used to flip this on at boot; baking it in preserves that behavior.
	viper.SetDefault("llm_server_react_critique_enabled", true)
	viper.SetDefault("llm_server_sdg_grounding_contract_enabled", false)
	viper.SetDefault("llm_server_react3_orchestrator_mode_enabled", true)
	viper.SetDefault("llm_server_react3_query_lean_prompt_enabled", true)
	viper.SetDefault("llm_server_react3_query_model_downshift_enabled", false)
	viper.SetDefault("llm_server_react3_orchestrator_thinking_level", "medium")
	// Flipped false 2026-07-12 — see LlmServerThinkToolEnabled docstring.
	// Any env that wants the tool back sets LLM_SERVER_THINK_TOOL_ENABLED=true
	// (env override still wins over SetDefault).
	viper.SetDefault("llm_server_think_tool_enabled", false)
	viper.SetDefault("llm_server_kg_tools_enabled", true)
	viper.SetDefault("llm_server_kg_get_node_enabled", false)
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
	viper.SetDefault("llm_server_events_v2_enabled", false)

	viper.SetDefault("llm_server_followup_resume_v2_enabled", true)

	viper.SetDefault("llm_server_agent_integration_precheck_enabled", true)

	viper.SetDefault("llm_server_circuit_breaker_cooldown_seconds", 60)

	viper.SetDefault("llm_server_sync_dead_worker_messages", true)

	viper.SetDefault("llm_trace_enabled", false)

	// Memory Module defaults. LLM_MEMORY_MODULE_ENABLED is the single master
	// switch — when false the entire subsystem (writes, reads, event log) is
	// off, so an unchanged deployment sees zero memory behaviour. The
	// LLM_MEMORY_COMPOSE_ENABLED flag stays as a sub-master under the master
	// (only checked when MODULE is on) and defaults to true so flipping the
	// master alone is enough to turn the whole feature on; set it to false
	// to run shadow mode (write but don't inject). The per-layer flags
	// default to true and act as emergency kill-switches: once the master
	// is flipped on, every layer is active unless explicitly opted out.
	// Avoids the prior trap where turning the module on yielded no
	// observable effect because each per-layer write/read gate still
	// required its own opt-in.
	viper.SetDefault("llm_memory_module_enabled", true)
	viper.SetDefault("llm_memory_compose_enabled", true)
	// Logs agent latency improvement (#34712). Defaults OFF so the change is
	// inert on merge; flip via env per-tenant to validate.
	viper.SetDefault("llm_logs_standard_grep_enabled", false)
	// RAG projection defaults — flag OFF preserves pre-flag behaviour. The
	// timeout is per Compose read (Slice 2); the overfetch multiplier is how
	// many candidates the RAG query asks for relative to the target per-kind
	// cap (Slice 2). The projector-worker knobs govern the outbox drain loop
	// added in Slice 1: batch size = SKIP LOCKED LIMIT, interval = idle-tick
	// between drains, worker count = hash-shard fanout for per-row ordering.
	viper.SetDefault("llm_memory_rag_enabled", true)
	viper.SetDefault("llm_memory_rerank_enabled", false)
	viper.SetDefault("llm_memory_rerank_min_score", 0.8)
	// Combined-score weights (similarity / relevancy / decay) and the post-rerank
	// filter threshold. Threshold defaults to 0 (off) so an unchanged deployment
	// filters nothing; weights emphasise query-match (similarity+relevancy) over
	// freshness (decay) and are renormalized per-row over whichever signals exist.
	viper.SetDefault("llm_memory_rerank_w_similarity", 0.35)
	viper.SetDefault("llm_memory_rerank_w_relevancy", 0.45)
	viper.SetDefault("llm_memory_rerank_w_decay", 0.20)
	viper.SetDefault("llm_memory_rerank_threshold", 0.6)
	viper.SetDefault("llm_memory_inject_enabled", false)
	viper.SetDefault("llm_memory_tool_enabled", false)
	viper.SetDefault("llm_memory_rag_timeout_ms", 800)
	viper.SetDefault("llm_memory_rag_overfetch_multiplier", 3)
	viper.SetDefault("llm_memory_rag_projector_batch_size", 50)
	viper.SetDefault("llm_memory_rag_projector_interval_ms", 1000)
	viper.SetDefault("llm_memory_rag_projector_workers", 4)
	viper.SetDefault("llm_memory_layer_soul_enabled", true)
	viper.SetDefault("llm_memory_layer_preferences_enabled", true)
	viper.SetDefault("llm_memory_tenant_allowlist", "")
	viper.SetDefault("llm_memory_soul_max_tokens", 100)
	viper.SetDefault("llm_memory_prefs_max_tokens", 400)
	viper.SetDefault("llm_memory_cache_ttl_seconds", 300)
	viper.SetDefault("llm_memory_projection_workers", 4)

	viper.SetDefault("llm_memory_layer_patterns_enabled", true)
	viper.SetDefault("llm_memory_layer_decisions_enabled", true)
	viper.SetDefault("llm_memory_layer_collective_enabled", true)
	viper.SetDefault("llm_memory_patterns_max_tokens", 300)
	viper.SetDefault("llm_memory_patterns_per_kind_limit", 2)
	viper.SetDefault("llm_memory_decisions_max_tokens", 200)
	viper.SetDefault("llm_memory_collective_max_tokens", 300)

	viper.SetDefault("llm_memory_layer_session_enabled", true)
	viper.SetDefault("llm_memory_session_max_tokens", 400)
	viper.SetDefault("llm_memory_session_idle_minutes", 30)

	viper.SetDefault("llm_memory_maintenance_enabled", true)
	viper.SetDefault("llm_memory_maintenance_session_expire_schedule", "*/30 * * * *")
	viper.SetDefault("llm_memory_maintenance_session_distill_schedule", "*/30 * * * *")
	viper.SetDefault("llm_memory_maintenance_session_distill_batch_size", 50)
	viper.SetDefault("llm_memory_maintenance_preferences_schedule", "0 3 * * *")
	viper.SetDefault("llm_memory_maintenance_patterns_schedule", "0 4 * * *")
	viper.SetDefault("llm_memory_maintenance_events_rotate_schedule", "0 2 * * *")
	viper.SetDefault("llm_memory_maintenance_collective_schedule", "0 5 * * *")
	viper.SetDefault("llm_memory_maintenance_soul_schedule", "0 6 * * 0")
	viper.SetDefault("llm_memory_maintenance_decisions_schedule", "0 7 * * 0")
	viper.SetDefault("llm_memory_maintenance_patterns_extract_schedule", "0 5 * * *")
	// RAG tombstone sweep — hourly on the hour. Small batches so a single
	// pass never floods rag-server; the partial index keeps the scan cheap.
	viper.SetDefault("llm_memory_maintenance_rag_sweep_schedule", "0 * * * *")
	viper.SetDefault("llm_memory_maintenance_rag_sweep_batch", 100)
	viper.SetDefault("llm_memory_maintenance_preferences_decay_days", 60)
	// Decay ladder: active -> fading (14d) -> stale (30d) -> retired (60d).
	// Must be strictly increasing; a shorter retire than stale would retire a
	// pattern before it can go stale.
	viper.SetDefault("llm_memory_maintenance_patterns_fading_days", 14)
	viper.SetDefault("llm_memory_maintenance_patterns_stale_days", 30)
	viper.SetDefault("llm_memory_maintenance_patterns_retire_days", 60)
	viper.SetDefault("llm_memory_maintenance_events_retention_days", 90)

	viper.SetDefault("llm_productivity_manual_baseline_minutes", 25)
	viper.SetDefault("llm_productivity_engineer_hourly_rate_usd", 5.0)

	// Watch (background-poll-and-notify) defaults. Disabled by default — opt in via env.
	viper.SetDefault("llm_server_watch_enabled", false)
	viper.SetDefault("llm_server_watch_dispatcher_interval_sec", 10)
	viper.SetDefault("llm_server_watch_worker_count", 10)
	viper.SetDefault("llm_server_watch_worker_queue_size", 50)
	viper.SetDefault("llm_server_watch_min_interval_sec", 30)
	// Default cap is 1h, lowered from 86400 (24h) to shrink the blast
	// radius of the static-admin context the watch poll runs under: an
	// off-boarded user / disabled account would otherwise keep driving
	// admin-scoped tool calls for a full day before the watch expires.
	// Ops can raise per-environment via LLM_SERVER_WATCH_MAX_DURATION_SEC
	// once the per-poll re-validation hook lands. Real watches today cap
	// at 600s (rollouts) / 1800s (jobs) / 3600s (stack ops) per the
	// shared async-completion prompt, so this is a generous ceiling.
	viper.SetDefault("llm_server_watch_max_duration_sec", 3600)
	viper.SetDefault("llm_server_watch_max_per_tenant", 20)
	viper.SetDefault("llm_server_watch_max_failures", 3)
	viper.SetDefault("llm_server_watch_priming_poll_timeout_sec", 5)
	viper.SetDefault("llm_server_watch_submit_timeout_sec", 5)
	// Lowered from 60s — 60 was the LLM-judge worst case, not the typical
	// case. A bounded worker pool of 10 was being pinned for the full 60s
	// by a handful of slow kubectl / github / llm_judge polls, so other
	// tenants' watches got cascade-starved. Slow predicates needing more
	// budget should raise this per-tenant via env override rather than
	// inflict the worst case on everyone.
	viper.SetDefault("llm_server_watch_poll_timeout_sec", 15)
	// Bounds the terminal-state LLM summarizer. The summarize call runs
	// after MarkTerminal commits, so it is OK for it to fail (responder
	// falls back to the raw predicate summary); the timeout is here only
	// to prevent a stuck Gemini/Bedrock call from pinning a watch worker.
	viper.SetDefault("llm_server_watch_summarizer_timeout_sec", 30)
	viper.SetDefault("llm_server_watch_dispatch_batch_size", 100)
	viper.SetDefault("llm_server_watch_sql_source_enabled", false)
	viper.SetDefault("llm_server_watch_bypass_leader_election", false)

	viper.SetDefault("llm_server_scratchpad_summarization_enabled", true)
	viper.SetDefault("llm_server_scratchpad_max_observation_chars", 65536)
	viper.SetDefault("llm_server_sub_agent_evidence_enabled", true)
	viper.SetDefault("llm_server_sub_agent_evidence_max_chars", 2048)
	viper.SetDefault("llm_server_scratchpad_compression_activation_fraction", 0.75)

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
}

const insecureJWTSecret = "default-jwt-secret"
const insecureRelaySecret = "default"

// LogSecurityWarnings emits warnings for insecure config defaults that operators
// should override before deploying. Intentionally non-blocking: existing
// deployments that inherited defaults must keep starting while ops act on the
// warnings. Tighten to a hard fail in a future release once dashboards confirm
// the warning is no longer fired.
//
// Call from main() at startup — not from init(), to avoid noise in test
// processes.
func LogSecurityWarnings() {
	if Config.LlmServerJwtSecret == insecureJWTSecret {
		if Config.LlmServerSecurityMode == "local" {
			slog.Warn("config: llm_server_jwt_secret is set to the insecure default — acceptable for local dev only")
		} else {
			slog.Warn("config: SECURITY — llm_server_jwt_secret is set to the publicly known default value; " +
				"any attacker who knows this default can forge workspace JWTs and execute commands in the workspace pod. " +
				"Set LLM_SERVER_JWT_SECRET to a strong random value before deploying.")
		}
	}

	if Config.RelayServerSecretKey == insecureRelaySecret {
		if Config.LlmServerSecurityMode == "local" {
			slog.Warn("config: relay_server_secret_key is set to the insecure default — acceptable for local dev only")
		} else {
			slog.Warn("config: SECURITY — relay_server_secret_key is set to the publicly known default value; " +
				"set RELAY_SERVER_SECRET_KEY to a strong random value before deploying.")
		}
	}
}
