/**
 * Static usage guidance for the system-agent catalog rendered by
 * `ListAgents`. The agent list API (`ai_list_agents` → llm-server
 * `AgentDto`) only carries name / description / status / tools, so the
 * "when to use it" narrative lives here.
 *
 * Each entry is written from the agent's own `GetDescription()` in
 * `llm/llm-server/agents/*.go` — keep the two in sync when an agent's
 * scope changes.
 *
 * Keys are the agent's registered `name` (lowercase, as returned by the
 * API). Agents with no entry — and every user-created agent — render an
 * em dash instead of the info icon.
 */
export interface AgentUsageGuidance {
  /** The situation that should make a user reach for this agent. */
  whenToUse: string;
  /** A question phrased the way a user would actually ask it. */
  example: string;
  /** What the agent does that answers the question. */
  why: string;
  /** What it saves the user, versus doing it by hand. */
  advantages: string[];
}

export const AGENT_USAGE_GUIDANCE: Record<string, AgentUsageGuidance> = {
  // ---- Orchestrators -------------------------------------------------
  k8s_orchestrator: {
    whenToUse: 'Something is wrong in a Kubernetes cluster and you do not yet know which layer to look at.',
    example: '"Checkout is returning 502s in prod — what changed?"',
    why: 'Runs a lean troubleshooting loop with direct kubectl/helm access and pulls in log, metric and event specialists only as the investigation needs them.',
    advantages: [
      'One entry point instead of picking a specialist up front',
      'Follows the evidence across logs, metrics and events',
      'Cheaper than a full plan — specialists load on demand',
    ],
  },
  k8s_orchestrator_native: {
    whenToUse:
      'Same Kubernetes troubleshooting, but you want cluster-side operations (exec, describe, mutations) run directly rather than through cached data.',
    example: '"Exec into the failing pod and show me why the readiness probe fails."',
    why: 'Operates in K8s-native mode: live cluster reads and mutations go straight through kubectl_execute, with historical data still available.',
    advantages: ['Live cluster truth, not cached state', 'Can run describe/exec/mutations in one flow'],
  },
  aws_orchestrator: {
    whenToUse: 'An AWS-side problem — an EC2, RDS, ELB or IAM issue — where the failing resource is not yet identified.',
    example: '"Why is the payments RDS instance throttling connections?"',
    why: 'Lean AWS SRE loop with direct aws CLI execution; reaches AWS specialists on demand.',
    advantages: ['Handles its own resource discovery', 'No need to know the ARN or region up front'],
  },
  gcp_orchestrator: {
    whenToUse: 'A GCP-side problem across GKE, Cloud SQL, Cloud Run or Cloud Storage.',
    example: '"Which GKE node pool is hitting its quota?"',
    why: 'Lean GCP SRE loop with direct gcloud execution and on-demand specialists.',
    advantages: ['Covers the whole gcloud surface', 'Discovers projects and resources itself'],
  },
  azure_orchestrator: {
    whenToUse: 'An Azure-side problem across VMs, App Service, AKS or Azure SQL.',
    example: '"Why did the AKS ingress start returning 503s this morning?"',
    why: 'Lean Azure SRE loop with direct az CLI execution and on-demand specialists.',
    advantages: ['Covers the whole az CLI surface', 'Discovers subscriptions and resources itself'],
  },
  datadog_orchestrator: {
    whenToUse: 'Your observability data lives in Datadog and you want the whole investigation driven from there.',
    example: '"Trace the latency spike on checkout-svc in Datadog."',
    why: 'Plans and delegates across the Datadog metrics, logs, traces, containers and incident agents.',
    advantages: ['Single Datadog front door', 'Produces a step-by-step investigation plan'],
  },

  // ---- Observability -------------------------------------------------
  logs: {
    whenToUse: 'You need log lines and you do not want to work out which backend holds them or how to query it.',
    example: '"Show me the errors from checkout-svc in the last 30 minutes."',
    why: 'Translates the question into the right query for whichever backend is configured (Kubernetes, Loki, Elasticsearch, Datadog, Signoz) and finds the resource itself.',
    advantages: ['One question works across every log backend', 'No LogQL / Lucene / DQL syntax to remember', 'Discovers the pod or service for you'],
  },
  loganalysis: {
    whenToUse: 'You already have log output and want the root cause and the code location behind it.',
    example: '"Why did checkout-svc restart four times in the last hour?"',
    why: 'Analyses log data for issues and root causes, and extracts the source file, file name and line number tied to the failure.',
    advantages: ['Returns the source file and line, not just the stack trace', 'Summarises noisy logs into actionable findings'],
  },
  metrics: {
    whenToUse: 'You want utilisation, latency or a performance trend — and possibly a chart of it.',
    example: '"Plot memory usage for the api deployment over the last 7 days."',
    why: 'Translates the question into a query against Kubernetes, Prometheus or Datadog and can visualise the result.',
    advantages: ['Works across metric backends', 'Charts the answer without a dashboard', 'Handles SLO/SLA and threshold-breach questions'],
  },
  prometheus: {
    whenToUse: 'The data is specifically in Prometheus and you want PromQL-grade answers without writing PromQL.',
    example: '"What is the p99 request latency per service right now?"',
    why: 'Prometheus expert that discovers the relevant series itself and translates the question into PromQL.',
    advantages: ['No PromQL to write or debug', 'Finds the right metric name and labels for you'],
  },
  promql_query: {
    whenToUse: 'You want the PromQL expression itself — to paste into a dashboard or alert rule.',
    example: '"Give me a PromQL query for pods restarting more than 3 times an hour."',
    why: 'Generates and validates a PromQL expression from the natural-language question.',
    advantages: ['Returns a query you can reuse in a rule or panel', 'Validates the expression before handing it over'],
  },
  events: {
    whenToUse: 'You want to know what happened — alerts, deployments, config changes, anomalies, SLO violations.',
    example: '"What changed in the payments namespace before the outage?"',
    why: 'Answers questions over Nudgebee Events and discovers the relevant monitoring data itself.',
    advantages: ['Correlates alerts with deployments and config changes', 'No separate reconnaissance step needed'],
  },
  events_v2: {
    whenToUse: 'Same event questions as `events`, when you want deterministic, tool-first retrieval.',
    example: '"List every OOMKill in staging yesterday."',
    why: 'Tool-first event agent using structured retrieval rather than free-form discovery.',
    advantages: ['More repeatable answers for the same question', 'Better suited to precise, filterable queries'],
  },
  events_rca_report: {
    whenToUse: 'An incident is over and you need a written root-cause analysis for it.',
    example: '"Write the RCA report for event 4f21c9."',
    why: 'Generates a full RCA report for a given event ID.',
    advantages: ['Turns raw evidence into a shareable report', 'Consistent RCA structure every time'],
  },
  traces: {
    whenToUse: 'A request is slow or failing and you need to see where in the call chain it goes wrong.',
    example: '"Show me the slowest traces for the checkout endpoint."',
    why: 'Retrieves distributed traces from the configured backend and answers questions over them.',
    advantages: ['Pinpoints the slow span instead of the slow service', 'No trace-query syntax needed'],
  },
  service_dependency_graph: {
    whenToUse: 'You need to know what a service talks to, or what breaks if it goes down.',
    example: '"What depends on the auth service?"',
    why: 'Reads the Knowledge Graph to resolve service and cloud-resource dependencies across K8s and AWS/GCP/Azure.',
    advantages: ['Covers both K8s and cloud resources in one graph', 'Blast-radius answers before you make a change'],
  },
  elastic_search_metrics: {
    whenToUse: 'Your metrics live in Elasticsearch or OpenSearch rather than Prometheus.',
    example: '"Average CPU per node from the metricbeat index last 24h."',
    why: 'Builds aggregation DSL queries against Elasticsearch/OpenSearch and analyses the result.',
    advantages: ['No aggregation DSL to hand-write', 'Works against your existing ES indices'],
  },
  visualizer: {
    whenToUse: 'You want a diagram — an architecture sketch, a flow, a timeline or a chart.',
    example: '"Draw the request flow from ingress to the database."',
    why: 'Generates Mermaid.js diagrams from a natural-language description or a data flow.',
    advantages: ['Diagram in the answer, no drawing tool needed', 'Mermaid output is easy to paste into docs'],
  },

  // ---- Cost ----------------------------------------------------------
  finops: {
    whenToUse: 'You are asked where the cloud bill is going and what to do about it.',
    example: '"Where did our spend increase this month, and what can we cut?"',
    why: 'Cost optimisation supervisor that orchestrates spend, recommendation, cloud-debug and observability tools into evidence-backed savings.',
    advantages: ['Recommendations come with the evidence behind them', 'Spans spend data and live utilisation, not just billing'],
  },
  recommendations: {
    whenToUse: 'You want Nudgebee’s standing recommendations and what has already been done about them.',
    example: '"What right-sizing recommendations are open for the prod cluster?"',
    why: 'Returns RightSizing, Security, InfraUpgrade, Spot, Configuration and K8s-version recommendations along with their resolution history.',
    advantages: ['Shows the PRs, tickets and attempts already made', 'Avoids re-raising work someone has tried'],
  },
  cost_optimizer: {
    whenToUse: 'A conversation ran long or expensive and you want to know why.',
    example: '"Why did this investigation cost so much to run?"',
    why: 'Analyses a finished conversation’s cost and execution flow and suggests lighter models, redundant agents and wasteful retries.',
    advantages: ['Concrete per-conversation cost breakdown', 'Points at the specific calls worth changing'],
  },

  // ---- Code & delivery ----------------------------------------------
  code_analyzer: {
    whenToUse: 'The answer is in the source code — a bug to root-cause, a fix to write, or a repository to explain.',
    example: '"Find the code path that produces this NullPointerException and fix it."',
    why: 'Deep code analysis, debugging and RCA over the repository, including generating the code change.',
    advantages: ['Reads the actual repository, not just the stack trace', 'Can produce the fix as a diff or PR'],
  },
  github: {
    whenToUse: 'You need GitHub metadata — issues, PR state, workflow runs, run logs, releases, labels.',
    example: '"Why did the last deploy workflow run fail?"',
    why: 'Drives the gh CLI for metadata operations and discovers the repo and org itself.',
    advantages: ['Pulls failed-run logs and artifacts for triage', 'No repo or org argument to supply'],
    // Note: source-code work belongs to code_analyzer, per the agent's own description.
  },
  gitlab: {
    whenToUse: 'The same metadata questions, on GitLab — merge requests, pipelines, job logs, releases.',
    example: '"Which pipeline stage is failing on the release branch?"',
    why: 'Drives GitLab APIs for project and pipeline metadata with its own project discovery.',
    advantages: ['Pipeline triage without leaving the chat', 'Discovers the project for you'],
  },
  loggithub: {
    whenToUse: 'You have an error log and the fix is in a GitHub repository.',
    example: '"This stack trace is from our repo — what is the minimal fix?"',
    why: 'Correlates error logs against file content in GitHub to find the root cause and propose a minimal diff.',
    advantages: ['Ties the log line to the exact source file', 'Proposes the smallest change that fixes it'],
  },
  helm: {
    whenToUse: 'A release is in a bad state and you need Helm-level answers.',
    example: '"What changed between the last two releases of the api chart?"',
    why: 'Runs Helm commands from natural language and returns the output.',
    advantages: ['Release history and diffs without shell access', 'No helm flags to remember'],
  },
  automation: {
    whenToUse: 'You want to see, run, or set up a Nudgebee automation.',
    example: '"Trigger the node-drain automation for node-7."',
    why: 'Lists automations, shows their details, triggers executions and creates new ones.',
    advantages: ['Run an automation without leaving the conversation', 'Delegates construction to the builder agent'],
  },
  automation_builder: {
    whenToUse: 'You are building a new automation and want it drafted for you.',
    example: '"Build an automation that restarts a pod when it OOMKills twice in an hour."',
    why: 'Plan-then-build flow: extracts intent, generates a plan for your approval, then builds and validates the automation.',
    advantages: ['You approve the plan before anything is built', 'Validation loop catches a broken definition early'],
  },
  remediation: {
    whenToUse: 'You know the fix and want it planned, reviewed and applied safely.',
    example: '"Scale the api deployment to 6 replicas and confirm it recovers."',
    why: 'Owns the whole remediation lifecycle — plan generation, your edits, then execution with safety checks.',
    advantages: ['Nothing runs before you approve it', 'Safety checks guard destructive commands'],
  },

  // ---- Tickets, security, search -------------------------------------
  tickets: {
    whenToUse: 'The work needs to land in Jira.',
    example: '"Create a Jira ticket for this OOM issue and link the RCA."',
    why: 'Searches Jira issues, updates fields and adds comments from natural-language requests.',
    advantages: ['No JQL to write', 'Keeps the investigation and the ticket in one place'],
  },
  tickets_v2: {
    whenToUse: 'Your tickets live somewhere other than Jira, or across several trackers.',
    example: '"Open a ServiceNow incident and a GitHub issue for this alert."',
    why: 'Creates and manages tickets across Jira, GitHub, GitLab, ServiceNow, PagerDuty, ZenDuty and Freshdesk.',
    advantages: ['One agent for every configured tracker', 'Cross-platform lists without switching tools'],
  },
  security: {
    whenToUse: 'You have a security question, or need an image or CIS scan explained.',
    example: '"What critical CVEs are in the checkout image?"',
    why: 'Runs and summarises image and CIS scans and explains the output in the context of your question.',
    advantages: ['Turns raw scan output into a prioritised summary', 'Can trigger the scan as part of the answer'],
  },
  websearch: {
    whenToUse: 'The answer is in your documentation, a runbook, or on the public web.',
    example: '"What does this Kubernetes error code mean, and do we have a runbook for it?"',
    why: 'Searches internal documentation, skills and the web together.',
    advantages: ['Internal docs and public sources in one answer', 'Cites where each answer came from'],
  },

  // ---- Databases & infrastructure ------------------------------------
  postgres: {
    whenToUse: 'A PostgreSQL database is slow, locking, or behaving oddly.',
    example: '"Which queries are causing the lock waits on the orders table?"',
    why: 'Translates the question into SQL and discovers the database and instance itself.',
    advantages: ['No connection details to supply', 'Diagnostic SQL written and run for you'],
  },
  mysql: {
    whenToUse: 'A MySQL database is slow or erroring.',
    example: '"What are the slowest queries in the last hour?"',
    why: 'Translates the question into SQL with its own database discovery.',
    advantages: ['No connection details to supply', 'Diagnostic SQL written and run for you'],
  },
  mssql: {
    whenToUse: 'A SQL Server instance needs diagnosing.',
    example: '"Show me blocking sessions on the reporting database."',
    why: 'Translates the question into T-SQL with its own database discovery.',
    advantages: ['No T-SQL to hand-write', 'Finds the instance itself'],
  },
  oracle: {
    whenToUse: 'An Oracle Database needs diagnosing.',
    example: '"What is driving the wait events on the billing instance?"',
    why: 'Translates the question into Oracle SQL with its own database and instance discovery.',
    advantages: ['No Oracle SQL to hand-write', 'Finds the instance itself'],
  },
  clickhouse: {
    whenToUse: 'A ClickHouse cluster or query needs debugging.',
    example: '"Why is this ClickHouse query spilling to disk?"',
    why: 'Debugs ClickHouse issues from natural language.',
    advantages: ['ClickHouse-specific diagnostics without the SQL', 'Works against your configured cluster'],
  },
  redis: {
    whenToUse: 'A Redis instance needs inspecting or a key needs checking.',
    example: '"How much memory is the session keyspace using?"',
    why: 'Translates the question into redis-cli commands and discovers the instance itself.',
    advantages: ['No redis-cli access needed', 'Finds the right instance for you'],
  },
  rabbitmq: {
    whenToUse: 'A queue is backing up or a binding looks wrong.',
    example: '"Which queues have unacked messages piling up?"',
    why: 'Translates the question into rabbitmqadmin commands or Management API calls.',
    advantages: ['Queue, exchange and connection state in one answer', 'No management console needed'],
  },
  server: {
    whenToUse: 'The problem is on a Linux, macOS or Windows host rather than in the cluster.',
    example: '"Is the disk on node-3 full, and what is filling it?"',
    why: 'Acts as an SRE on the host, driving shell commands from natural language.',
    advantages: ['Host-level diagnosis without an SSH session', 'Concise, SRE-shaped answers'],
  },
  aws: {
    whenToUse: 'You want a specific AWS resource inspected or changed.',
    example: '"Show me the security groups attached to the prod ALB."',
    why: 'Drives the AWS CLI with its own resource discovery and configuration.',
    advantages: ['Covers the full AWS CLI surface', 'No region or ARN needed up front'],
  },
  gcp: {
    whenToUse: 'You want a specific GCP resource inspected or changed.',
    example: '"List the Cloud SQL instances and their current CPU."',
    why: 'Drives the gcloud CLI across Compute, GKE, Storage, Cloud SQL, Cloud Run, Logging, Monitoring, Pub/Sub, IAM and Billing.',
    advantages: ['Covers the full gcloud surface', 'Discovers the project itself'],
  },
  azure: {
    whenToUse: 'You want a specific Azure resource inspected or changed.',
    example: '"Which App Service plans are scaled above 70% CPU?"',
    why: 'Drives the Azure CLI with its own resource discovery and configuration.',
    advantages: ['Covers the full az CLI surface', 'Discovers the subscription itself'],
  },
  gcp_metrics: {
    whenToUse: 'Your metrics come from GCP Cloud Monitoring rather than Prometheus.',
    example: '"CPU and request latency for the Cloud Run service last 24h."',
    why: 'Retrieves and analyses Cloud Monitoring metrics via the gcloud CLI.',
    advantages: ['Native GCP metrics without a Prometheus exporter', 'Covers managed-service metrics too'],
  },
  azure_metrics: {
    whenToUse: 'Your metrics come from Azure Monitor rather than Prometheus.',
    example: '"Show DTU utilisation for the Azure SQL database this week."',
    why: 'Retrieves and analyses Azure Monitor metrics via the az CLI.',
    advantages: ['Native Azure metrics without an exporter', 'Covers managed-service metrics too'],
  },

  // ---- Datadog specialists -------------------------------------------
  datadog_metrics: {
    whenToUse: 'You want a metric answer straight out of Datadog.',
    example: '"What is the error rate for checkout-svc in Datadog?"',
    why: 'Queries Datadog metrics from a natural-language question.',
    advantages: ['No Datadog query syntax needed', 'Uses your existing Datadog data'],
  },
  datadog_logs: {
    whenToUse: 'Your logs are in Datadog.',
    example: '"Show Datadog logs for checkout-svc with status error."',
    why: 'Builds and runs Datadog log queries from natural language.',
    advantages: ['No Datadog log syntax needed', 'Works against your existing indexes'],
  },
  datadog_traces: {
    whenToUse: 'Your APM traces are in Datadog.',
    example: '"Which span is slowest on the checkout trace?"',
    why: 'Retrieves Datadog traces from a natural-language question.',
    advantages: ['Span-level detail without the APM UI', 'Ties traces back to the service in question'],
  },
  datadog_containers: {
    whenToUse: 'You want container, pod, node or workload state as Datadog sees it.',
    example: '"Which containers are restarting most in the prod cluster?"',
    why: 'Answers container and workload questions using Datadog data.',
    advantages: ['Covers pods, nodes and workloads in one query', 'No cluster access required'],
  },
  datadog_events: {
    whenToUse: 'You want the Datadog event stream for a window.',
    example: '"What Datadog events fired around the 14:00 spike?"',
    why: 'Retrieves Datadog events from a natural-language question.',
    advantages: ['Correlates events with the incident window', 'No event-search syntax needed'],
  },
  datadog_incident: {
    whenToUse: 'You are working a Datadog incident and need its details.',
    example: '"Summarise the open Datadog incidents for payments."',
    why: 'Retrieves Datadog incident information from natural language.',
    advantages: ['Incident context inside the investigation', 'No context switch to the Datadog UI'],
  },
  datadog_service: {
    whenToUse: 'You need APM service metadata from Datadog.',
    example: '"Which services own the checkout endpoints?"',
    why: 'Retrieves service details from Datadog.',
    advantages: ['Service ownership and metadata in one answer'],
  },
  datadog_hosts: {
    whenToUse: 'You need host-level detail from Datadog.',
    example: '"Which hosts are reporting no metrics right now?"',
    why: 'Retrieves host details from Datadog.',
    advantages: ['Spots silent hosts quickly', 'No host-search syntax needed'],
  },
  datadog_software_catalog: {
    whenToUse: 'You need ownership or metadata from the Datadog Software Catalog.',
    example: '"Who owns the payments service?"',
    why: 'Retrieves entities from the Datadog Software Catalog, optionally filtered.',
    advantages: ['Ownership lookups without leaving the chat'],
  },
};

/** Lookup helper — normalises the agent name the API returns. */
export const getAgentUsageGuidance = (agentName?: string): AgentUsageGuidance | undefined =>
  agentName ? AGENT_USAGE_GUIDANCE[agentName.toLowerCase()] : undefined;
