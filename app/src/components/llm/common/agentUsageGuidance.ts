/**
 * Static usage guidance for the system-agent catalog rendered by
 * `ListAgents`. The agent list API (`ai_list_agents` -> llm-server
 * `AgentDto`) only carries name / description / status / tools, so the
 * "when to use it" narrative lives here and is shown in the "Usage"
 * column's hover panel.
 *
 * The primary entries below are generated from the product Agent Catalog
 * sheet (brief / whenToUse / why / notFor). A handful of legacy entries
 * (helm, redis, rabbitmq, aws, gcp, azure, loggithub, events_rca_report)
 * are agents not covered by that sheet and keep their original
 * when/example/why/advantages shape — that is why every field except
 * `whenToUse` and `why` is optional.
 *
 * Keys are the agent's registered `name` (lowercase, as returned by the
 * API). Agents with no entry -- and every user-created agent -- render an
 * em dash instead of the info icon.
 */
import { fillBrandTokens } from '@hooks/useTenantBranding';

export interface AgentUsageGuidance {
  /** One-line "what it is" summary (catalog entries). */
  brief?: string;
  /** The situation that should make a user reach for this agent. */
  whenToUse: string;
  /** What the agent does that answers the question. */
  why: string;
  /** When NOT to reach for it, and where to go instead (catalog entries). */
  notFor?: string;
  /** A question phrased the way a user would actually ask it (legacy entries). */
  example?: string;
  /** What it saves the user, versus doing it by hand (legacy entries). */
  advantages?: string[];
}

export const AGENT_USAGE_GUIDANCE: Record<string, AgentUsageGuidance> = {
  // ---- Catalog agents (generated from the Agent Catalog sheet) --------
  k8s_orchestrator: {
    brief:
      'The general-purpose SRE for your cluster: runs kubectl and helm directly, and pulls in log, metric and trace specialists only when it needs them.',
    whenToUse:
      'Start here whenever the problem is somewhere in Kubernetes but you do not yet know where. "Why is checkout-service crashlooping?", "pods stuck pending in prod", "did last night\'s deploy break the API?", "is this node unhealthy?"',
    why: 'This is the default front door for "something is wrong in Kubernetes". It is deliberately given a small, principle-level prompt rather than a fixed script, so it keeps working on clusters and failure modes nobody wrote a playbook for. It finds its own specialists at runtime instead of you having to know which sub-agent holds the answer.',
    notFor:
      'Problems on the cloud side of the boundary - a broken load balancer, RDS failover, IAM or quota errors (use the AWS / GCP / Azure Troubleshooter). Cost questions (FinOps Cost Advisor).',
  },
  k8s_orchestrator_native: {
    brief: 'Same job as the Kubernetes Troubleshooter, but every cluster read and write goes straight through kubectl instead of through sub-agents.',
    whenToUse:
      'Direct cluster diagnostics where kubectl is the answer: describe a resource, read live events, exec into a container, apply a change. Also when historical metrics or traces are a side question rather than the main one.',
    why: 'Fewer hops means faster and more literal answers when you already know you want cluster-side truth - describe output, live events, an exec into a pod, or an actual change. {brand} selects it automatically when you or the caller have declared K8s-native intent.',
    notFor:
      'Accounts whose logs and metrics only exist in Datadog, Loki or Elasticsearch - the standard Kubernetes Troubleshooter routes to those properly.',
  },
  aws_orchestrator: {
    brief: 'The SRE for the AWS account itself: runs the aws CLI directly and reaches specialists on demand.',
    whenToUse:
      '"Is the load balancer healthy?", "why did RDS fail over?", "check the NAT gateway", "are we hitting a service quota?", or any AWS dependency of a service that is misbehaving.',
    why: 'Cluster tooling stops at the cluster edge. This agent reasons about the AWS side - EC2, RDS, ELB, IAM, quotas, networking - where the failure is in the account rather than in your workload.',
    notFor: 'In-cluster failures (Kubernetes Troubleshooter). Spend and savings (FinOps Cost Advisor).',
  },
  gcp_orchestrator: {
    brief: 'The SRE for the GCP project itself: runs gcloud directly and reaches specialists on demand.',
    whenToUse:
      '"Why is the backend service unhealthy?", "did Cloud SQL restart?", "check firewall rules for this service", or any GCP dependency of a failing workload.',
    why: 'Same role as the AWS Troubleshooter, on Google Cloud: it owns the GCP-side investigation (GCE, Cloud SQL, load balancers, IAM, quotas) that cluster tooling cannot see.',
    notFor: 'In-cluster failures (Kubernetes Troubleshooter). Spend and savings (FinOps Cost Advisor).',
  },
  azure_orchestrator: {
    brief: 'The SRE for the Azure subscription itself: runs the az CLI directly and reaches specialists on demand.',
    whenToUse: '"Why is the App Service returning 502s?", "did the SQL database throttle?", "check the NSG rules for this subnet".',
    why: 'Same role as the AWS and GCP troubleshooters, on Azure: it owns the subscription-side investigation - App Service, AKS control plane, SQL, networking, quotas.',
    notFor: 'In-cluster failures (Kubernetes Troubleshooter). Spend and savings (FinOps Cost Advisor).',
  },
  datadog_orchestrator: {
    brief: 'Plans and walks a Datadog-based investigation step by step, across logs, metrics, traces and monitors.',
    whenToUse:
      '"Debug this in Datadog", triage that starts from a Datadog monitor or alert, or any investigation where every signal you care about is already in Datadog.',
    why: 'When Datadog is your source of truth, this agent sequences the individual Datadog readers for you and returns an explicit step-by-step plan you can follow or audit, instead of you choosing each reader yourself.',
    notFor: 'Accounts where Datadog is not the primary backend - use the Kubernetes or cloud troubleshooter instead.',
  },
  aws_observability: {
    brief:
      'Deep-dives one AWS problem across CloudWatch Logs, Metrics, Alarms, X-Ray traces and CloudTrail together, and can look up unfamiliar AWS errors on the web.',
    whenToUse:
      '"What happened to this Lambda at 03:00?", "correlate this alarm with traces", "who changed this and when?", or any AWS incident where the answer spans more than one signal.',
    why: 'The single-signal AWS readers each answer one question well. This one correlates all five signals in a single investigation - alarm to metric to trace to log line to the audit event that caused it - and is the only AWS agent that can research an error string it does not recognise.',
    notFor: 'Non-AWS telemetry, and cluster-internal problems (Kubernetes Troubleshooter).',
  },
  logs: {
    brief:
      'Answers "what do the logs say?" against whichever backend you have - Kubernetes, Loki, Elasticsearch, Datadog or SigNoz - and finds the right pod and namespace itself.',
    whenToUse:
      'Any log question: fetching application or container logs, searching by keyword or time range, diagnosing a crashing pod from its output, correlating logs across services.',
    why: 'It is the only log agent that changes depth based on what you asked. A causal question ("why did it fail?") triggers a multi-pass investigation loop over saved log files; "list errors" triggers per-signature aggregation; "tail the last 10 minutes" triggers one cheap fetch. It also does its own resource discovery, so you never have to look up a pod name first.',
    notFor:
      'Performance numbers over time (Metrics Analyst). Running kubectl (Kubernetes Troubleshooter). Kubernetes events (Event & Alert Investigator).',
  },
  loganalysis: {
    brief: 'Reads log data you already have, works out the root cause, and points at the source file, filename and line number behind it.',
    whenToUse:
      'After the logs are in hand: "what actually caused these errors, and where in the code?" Also good for summarising a noisy log dump into a few actionable findings.',
    why: 'It is the only log agent that hands back a code location. That makes it the bridge between an operational symptom and the code change that fixes it.',
    notFor: 'Fetching the logs in the first place (Log Investigator). Proposing or raising the actual fix (Code Analyzer & Fixer).',
  },
  fetch_logs: {
    brief: 'Turns one natural-language log question into the correct backend query and runs it. No investigation loop, no follow-ups.',
    whenToUse: 'You already know exactly which logs you want. In practice this is mostly called by other agents rather than chosen by a person.',
    why: 'It is the cheap, predictable building block the bigger log agents are built on. Single shot: one question in, one result set out.',
    notFor: 'Root-cause work - it will fetch, not reason. Use the Log Investigator.',
  },
  aws_logs: {
    brief:
      'Reads and analyses CloudWatch logs - application, Lambda, ECS/EKS and CloudTrail audit logs - through the aws CLI, and quotes real log lines back.',
    whenToUse: 'Your logs only exist in CloudWatch. Lambda output, ECS or EKS container logs, or CloudTrail audit questions.',
    why: 'It is the logs backend for AWS accounts that have no Loki, Elasticsearch or Datadog. It discovers log groups and streams before it queries, so you do not need to know the group name.',
    notFor: 'Accounts that do have a dedicated log stack - the Log Investigator will route better.',
  },
  gcp_logs: {
    brief:
      'Reads and analyses Cloud Logging entries - GKE containers, Compute and serverless, Cloud SQL, load balancers, audit logs - through gcloud.',
    whenToUse: 'Your logs only exist in Cloud Logging. GKE container output, Cloud Run or Cloud Functions logs, or an audit-log question.',
    why: 'It is the logs backend for GCP projects with no Loki, Elasticsearch or Datadog. It discovers the resource type and labels before querying, so filters are correct without you writing them.',
    notFor: 'Accounts with a dedicated log stack - use the Log Investigator.',
  },
  azure_logs: {
    brief:
      'Reads and analyses Azure logs - Log Analytics and AKS container logs, App Service logs, VM diagnostics, Activity Log - through the az CLI.',
    whenToUse: 'Your logs only exist in Azure. AKS container output, App Service logs, or an Activity Log audit question.',
    why: 'It is the logs backend for Azure accounts with no Loki, Elasticsearch or Datadog. It works out the log source (which workspace, which resource) before querying and cites concrete lines.',
    notFor: 'Accounts with a dedicated log stack - use the Log Investigator.',
  },
  datadog_logs: {
    brief: 'Fetches and explains logs from Datadog for a plain-English question.',
    whenToUse: '"Show me errors for this service in Datadog over the last hour."',
    why: 'Direct line to Datadog log search when Datadog is where your logs live. It writes the query and runs it in one step.',
    notFor: 'Getting a query you can paste into a dashboard without running it (Datadog Log Query Writer).',
  },
  datadog_log_query: {
    brief: 'Translates a plain-English question into a Datadog log query string, and stops there.',
    whenToUse: '"Give me the Datadog query for 5xx on checkout in staging" - typically as a step inside a larger flow.',
    why: 'Separating query generation from execution means the query can be reviewed, reused in a dashboard or monitor, or run by another agent. Useful when you want the search, not the results.',
    notFor: 'Actually getting log lines back (Datadog Logs Reader).',
  },
  signoz_logs: {
    brief: 'Fetches and explains logs from SigNoz for a plain-English question.',
    whenToUse: '"Show me error logs for this service in SigNoz."',
    why: 'Direct line to SigNoz log search for accounts running SigNoz as their observability stack.',
    notFor: 'Getting the query without running it (SigNoz Log Query Writer).',
  },
  signoz_log_query_generator: {
    brief: 'Translates a plain-English question into a SigNoz log query, and stops there.',
    whenToUse: 'As a step inside a larger SigNoz flow, or when you want a reusable saved search.',
    why: 'Same split as on the Datadog side: build the query once, then review it, reuse it, or hand it to whatever will run it.',
    notFor: 'Returning log lines (SigNoz Logs Reader).',
  },
  metrics: {
    brief: 'Answers questions about numbers over time from Kubernetes, Prometheus or Datadog - and can draw the chart.',
    whenToUse:
      'CPU, memory and network utilisation, performance trends, SLO and SLA data, threshold breaches, custom queries, and any request that ends in "...and show me a graph".',
    why: 'It is the backend-agnostic front door for metrics: you ask in plain English, it picks the source, writes the query, and can return a graph rather than a wall of numbers. Charting is the capability most of the narrower metric readers lack.',
    notFor: 'Log analysis (Log Investigator). Kubernetes resource state (Kubernetes Troubleshooter). Kubernetes events (Event & Alert Investigator).',
  },
  prometheus: {
    brief: 'Prometheus specialist: turns plain-English questions into PromQL, runs them, and charts the result. Discovers the metric names itself.',
    whenToUse: 'Any Prometheus question where you do not want to hand-write PromQL, and any request for a Prometheus-based chart.',
    why: 'Prometheus discovery is genuinely hard - thousands of metric names, unclear labels. This agent does that reconnaissance for you, so you can ask for "memory usage of the payments pods" without knowing whether the metric is container_memory_working_set_bytes.',
    notFor: 'Datadog or CloudWatch metrics (use the Metrics Analyst or the cloud-specific reader).',
  },
  promql_query: {
    brief: 'Turns a plain-English question into a valid PromQL expression, and stops there.',
    whenToUse: '"Give me the PromQL for p99 latency by service", or as a building block inside automation and dashboards.',
    why: 'Sometimes the query is the deliverable: for a dashboard panel, an alerting rule, or a runbook step. This agent produces one without executing anything.',
    notFor: 'Getting the actual numbers or a chart (Prometheus Metrics Expert).',
  },
  aws_metrics: {
    brief:
      'Reads and analyses CloudWatch metrics and alarms - CPU, memory, disk, network, latency, error rate, managed-service metrics - through the aws CLI.',
    whenToUse: 'Your metrics only exist in CloudWatch, or you are asking about an AWS managed service (RDS, ELB, Lambda) or an alarm state.',
    why: 'It is the metrics backend for AWS accounts with no Prometheus, Datadog or Elasticsearch, and it handles its own metric-name and resource discovery so you do not need the exact namespace or dimension.',
    notFor: 'Accounts with Prometheus or Datadog - the Metrics Analyst routes better.',
  },
  gcp_metrics: {
    brief: 'Reads and analyses Cloud Monitoring metrics - CPU, memory, disk, network, latency, error rate, managed-service metrics - through gcloud.',
    whenToUse: 'Your metrics only exist in Cloud Monitoring, or you are asking about a GCP managed service.',
    why: 'It is the metrics backend for GCP projects with no Prometheus, Datadog or Elasticsearch, and it resolves metric types and resources on its own.',
    notFor: 'Accounts with Prometheus or Datadog - use the Metrics Analyst.',
  },
  azure_metrics: {
    brief:
      'Reads and analyses Azure Monitor metrics - CPU, memory, disk, network, DTU, request latency and error rate, managed-service metrics - through the az CLI.',
    whenToUse: 'Your metrics only exist in Azure Monitor, or you are asking about an Azure managed service such as SQL DTU consumption.',
    why: 'It is the metrics backend for Azure accounts with no Prometheus, Datadog or Elasticsearch, and it discovers metric names and resources itself.',
    notFor: 'Accounts with Prometheus or Datadog - use the Metrics Analyst.',
  },
  datadog_metrics: {
    brief: 'Fetches and explains metrics from Datadog for a plain-English question.',
    whenToUse: '"What was the error rate for checkout in Datadog yesterday?"',
    why: 'Direct line to Datadog metrics, including browsing the available metric and label names before querying.',
    notFor: 'Getting the query without running it (Datadog Metrics Query Writer).',
  },
  datadog_metrics_query: {
    brief: 'Turns a plain-English question into a Datadog metrics query, and stops there.',
    whenToUse: 'Dashboard and monitor authoring, or as a step inside a larger Datadog flow.',
    why: 'Produces a query you can put on a dashboard or into a monitor, without running it.',
    notFor: 'Returning metric values (Datadog Metrics Reader).',
  },
  elastic_search_metrics: {
    brief: 'Reads metrics out of Elasticsearch or OpenSearch using aggregation DSL queries.',
    whenToUse: 'Your metrics live in Elasticsearch or OpenSearch indices.',
    why: 'For shops that store metrics in Elasticsearch rather than a purpose-built TSDB, this is the agent that speaks aggregation DSL so you do not have to.',
    notFor: "Log search in Elasticsearch - that is the Log Investigator's job.",
  },
  traces: {
    brief: 'Answers questions about distributed traces held in ClickHouse - one request followed across every service it touched.',
    whenToUse: '"Where is the latency in this request?", "which downstream call is failing?", "show me slow traces for checkout".',
    why: "Traces answer the question logs and metrics cannot: which hop in the chain actually spent the time or threw the error. This is the default trace agent for accounts using {brand}'s own ClickHouse trace store.",
    notFor: 'Accounts on Datadog APM or a cloud-native tracer - use the matching reader below.',
  },
  aws_traces: {
    brief: 'Reads and analyses X-Ray traces - latency, slow traces, service maps, segment detail - and correlates trace-tagged logs from CloudWatch.',
    whenToUse: 'Your traces only exist in X-Ray. "Which segment is slow?", "show me the service map", "find the logs for this trace".',
    why: 'It is the traces backend for AWS accounts with no ClickHouse, Jaeger or Datadog. The useful part is the correlation: it can jump from a slow segment to the log lines carrying the same trace ID.',
    notFor: 'Accounts with a dedicated tracing stack - use the Trace Investigator.',
  },
  gcp_traces: {
    brief:
      'Reads and analyses Cloud Trace traces - latency, slow spans, service dependencies - and correlates trace-tagged entries from Cloud Logging.',
    whenToUse: 'Your traces only exist in Cloud Trace.',
    why: 'It is the traces backend for GCP projects with no ClickHouse, Jaeger or Datadog, and it links spans back to matching log entries.',
    notFor: 'Accounts with a dedicated tracing stack - use the Trace Investigator.',
  },
  azure_traces: {
    brief: 'Reads Azure distributed traces through Application Insights - request and dependency latency, failures, end-to-end transactions.',
    whenToUse: 'Your traces only exist in Application Insights and the workload is instrumented for it.',
    why: 'It is the traces backend for Azure accounts with no ClickHouse, Jaeger or Datadog. Note the hard prerequisite: it can only answer where Application Insights is actually configured for the workload.',
    notFor: 'Workloads with no Application Insights configured - it will have nothing to read.',
  },
  datadog_traces: {
    brief: 'Fetches and explains traces from Datadog APM for a plain-English question.',
    whenToUse: '"Show me the slowest traces for this service in Datadog."',
    why: 'Direct line to Datadog APM when that is where your traces live.',
    notFor: 'Non-Datadog trace backends.',
  },
  events_v2: {
    brief:
      'Same coverage as the Event & Alert Investigator, but answers through deterministic structured tools instead of asking a model to write SQL.',
    whenToUse: 'Anything you would ask the Event & Alert Investigator. Prefer this one where it is available.',
    why: 'Model-generated SQL is the least predictable part of the original agent. v2 uses fixed, tested tools for the common questions and only falls back to raw SQL for filters those tools genuinely cannot express - so the same question gives the same answer.',
    notFor: 'Nothing in particular - this is a strict improvement in reliability over v1.',
  },
  events: {
    brief:
      'Answers "what happened, and when?" - alerts, config changes, deployments, Kubernetes events, anomalies, SLO violations and incident investigations.',
    whenToUse:
      '"What changed before this broke?", "show me alerts for this namespace today", "why did this OOM?", "what deployments went out this morning?"',
    why: "Most outages are explained by a change, not by a metric. This agent owns {brand}'s event timeline and can explain why an event was triaged the way it was, show the evidence behind it, and assemble related events into one incident.",
    notFor: 'Raw pod logs (Log Investigator) or metric trends (Metrics Analyst) - though this agent is usually the right first stop before either.',
  },
  webhook_subject_name_extractor: {
    brief:
      'Given an incoming monitoring webhook, works out which running Kubernetes workload it is actually about. Returns one service name, or "Not Found".',
    whenToUse: 'Automatically, in the alert ingestion path. Rarely something you invoke by hand.',
    why: 'Alerts arrive with vendor-specific, inconsistent naming. Everything downstream - triage, routing, auto-remediation - depends on resolving that to a real workload first. It is a single-shot classifier over your running services, so it is fast and cheap enough to run on every alert.',
    notFor: 'Investigating the alert once it is matched (Event & Alert Investigator).',
  },
  datadog_events: {
    brief: 'Fetches event information from Datadog for a plain-English question.',
    whenToUse: '"What events fired in Datadog around 14:00?"',
    why: 'Reads the Datadog event stream directly - deploy markers, monitor state changes and custom events posted by your own tooling.',
    notFor: "{brand}'s own event timeline (Event & Alert Investigator). Declared incidents (Datadog Incident Reader).",
  },
  datadog_incident: {
    brief: 'Fetches incident information from Datadog Incident Management for a plain-English question.',
    whenToUse: '"What incidents are open?", "summarise incident 412".',
    why: 'Incidents are the human-declared layer above raw events - who is on it, what state it is in, what the timeline says. This agent reads that layer.',
    notFor: 'Raw events (Datadog Events Reader). Creating tickets (Ticket Manager).',
  },
  postgres: {
    brief: 'Investigates PostgreSQL by turning plain-English questions into SQL. Finds the database and instance itself.',
    whenToUse: '"Why is this query slow?", "are there blocking locks?", "how many connections are in use?", or any read-only data question.',
    why: 'No reconnaissance step needed: point it at a question, not at a connection string. It knows the system views that answer performance questions - locks, bloat, slow queries, connection saturation - so you do not have to remember them.',
    notFor: 'Schema migrations or writes as part of a fix - route those through Remediation or your normal change process.',
  },
  mysql: {
    brief: 'Investigates MySQL by turning plain-English questions into SQL. Finds the database itself.',
    whenToUse: '"What is holding this lock?", "show me the slowest queries", "is replication lagging?"',
    why: 'Same contract as the PostgreSQL agent, on MySQL: performance investigation and data questions without you writing the SQL or locating the instance.',
    notFor: 'Writes and schema changes as part of a fix.',
  },
  mssql: {
    brief: 'Investigates Microsoft SQL Server by turning plain-English questions into T-SQL. Finds the database itself.',
    whenToUse: '"Why is CPU high on this instance?", "what is blocking?", "show me expensive queries".',
    why: 'Same contract as the other database agents, speaking T-SQL and the SQL Server DMVs that answer health and performance questions.',
    notFor: 'Writes and schema changes as part of a fix.',
  },
  oracle: {
    brief: 'Investigates Oracle Database by turning plain-English questions into Oracle SQL. Finds the database and instance itself.',
    whenToUse: '"What are the top wait events?", "which session is blocking?", "is this tablespace filling up?"',
    why: "Same contract as the other database agents, with Oracle's own performance views - so you can ask about waits and sessions without knowing the V$ view names.",
    notFor: 'Writes and schema changes as part of a fix.',
  },
  clickhouse: {
    brief: 'Investigates ClickHouse issues from plain-English questions.',
    whenToUse: '"Why are these queries slow?", "is the merge queue backing up?", or when trace data looks incomplete.',
    why: "ClickHouse is both a customer database and the store behind {brand}'s own traces, so problems here can look like missing telemetry rather than a database fault.",
    notFor: "Reading traces stored in ClickHouse - that is the Trace Investigator's job, and it is much easier to use.",
  },
  service_dependency_graph: {
    brief:
      'Works out what a service depends on and what depends on it, across Kubernetes and AWS, GCP and Azure resources, using the Knowledge Graph.',
    whenToUse: 'Before or early in an investigation: "what does checkout talk to?", "who calls this database?", "what will this change affect?"',
    why: 'It is the only agent that spans cluster and cloud in one topology. That makes it the fastest way to establish blast radius - who else breaks if this breaks - before you spend time in logs.',
    notFor: 'Live request paths and latency (Trace Investigator). Current health (Kubernetes Troubleshooter).',
  },
  datadog_service: {
    brief: 'Returns service details from Datadog APM for a plain-English question.',
    whenToUse: '"Which services exist in this environment?", "what is the health of this service?"',
    why: 'The service-level index of your Datadog estate: what services exist, how they are performing, how they relate.',
    notFor: 'Individual traces (Datadog APM Trace Reader). Catalogue ownership metadata (Datadog Software Catalog Reader).',
  },
  datadog_containers: {
    brief: "Answers container, pod, node and workload questions using Datadog's infrastructure data.",
    whenToUse: '"Which pods are restarting?", "list containers on this node", where Datadog is the source of truth.',
    why: 'For Datadog-first shops, this reads workload state without needing cluster credentials - useful when {brand} can see Datadog but not the cluster directly.',
    notFor: 'Clusters {brand} can reach directly - kubectl through the Kubernetes Troubleshooter is more accurate and current.',
  },
  datadog_hosts: {
    brief: 'Returns host details from Datadog for a plain-English question.',
    whenToUse: '"Which hosts are not reporting?", "list hosts in this environment".',
    why: 'Host-level view - which machines exist, how they are tagged, whether they are reporting - from the Datadog side.',
    notFor: 'Container and pod level questions (Datadog Container Inventory).',
  },
  datadog_software_catalog: {
    brief: 'Retrieves entities from the Datadog Software Catalog, optionally filtered.',
    whenToUse: '"Who owns the payments service?", "list all services owned by team X".',
    why: 'The catalogue is where ownership, team and metadata live. This is the agent that answers "who owns this?" - the question that decides where a ticket goes.',
    notFor: 'Runtime health or performance (Datadog Service Inventory).',
  },
  datadog_containers_query: {
    brief: 'Turns a plain-English question into a Datadog container query, and stops there.',
    whenToUse: 'As a step inside a larger Datadog flow.',
    why: 'Query-only counterpart to the Container Inventory agent, for reuse in dashboards, monitors or a larger flow.',
    notFor: 'Returning container data (Datadog Container Inventory).',
  },
  code_analyzer: {
    brief: 'Reads your source code to find the real cause of a failure, then proposes the fix - up to opening a pull request.',
    whenToUse:
      '"Why does this service throw this error?", "find the bug behind this stack trace", "open a PR fixing this". Also the correct agent for any task that modifies files in a Git repository - refactors, migrations, bug fixes.',
    why: 'It is the only agent that crosses from operations into code: it correlates a runtime symptom (a log line, a stack trace, a failing service) with the source that produced it. Everything else in this catalogue tells you what broke; this one can tell you which line broke it, and change it.',
    notFor: 'Simple file lookups or running a shell command. Checking connectivity or infrastructure state - those are plain shell work.',
  },
  github: {
    brief:
      'Works with GitHub around the code: issues, PR review and merge state, workflow runs and their logs, run artifacts, releases, branches, comments and labels.',
    whenToUse:
      '"Why did this workflow fail?", "what is the review state of PR 412?", "list open issues with this label", "download the artifacts from that failed run".',
    why: "It handles CI triage end to end - including downloading a failed run's artifacts to work out why it failed - and finds the right repo and org itself. The strict boundary is deliberate: it reads the scaffolding around code, never the code itself.",
    notFor: 'Reading, analysing or changing source code, and raising PRs that carry code changes. All of that goes to Code Analyzer & Fixer.',
  },
  gitlab: {
    brief:
      'Works with GitLab around the code: issues, MR review and merge state, pipelines and jobs, job logs and artifacts, releases, branches, comments and labels. Can also read source files.',
    whenToUse:
      '"Why did this pipeline fail?", "show me the MR review state", "read this file from the repo", "download the failed job\'s artifacts".',
    why: 'Same role as GitHub Operations, with one extra capability - it can read source files and directory listings through the repository API. It still must not write code: modification stays with the code agent.',
    notFor: 'Modifying source code or raising MRs that carry code changes - use Code Analyzer & Fixer.',
  },
  finops: {
    brief: 'Analyses cloud spend, surfaces savings, and backs each recommendation with evidence - then can apply it or raise a ticket for it.',
    whenToUse: '"Where is our spend going?", "what can we safely cut?", "why did the bill jump last week?", "apply this rightsizing recommendation".',
    why: 'It is a supervisor, not a report. It combines spend summaries and forecasts with live metrics and cluster state, so a rightsizing recommendation is justified by observed usage rather than a static rule - and it can carry that recommendation through to a proposed change, an applied change, or a tracked ticket.',
    notFor: 'What an AI conversation cost to run - that is the AI Session Cost Reviewer, despite the similar name.',
  },
  recommendations: {
    brief:
      "Returns {brand}'s recommendations - rightsizing, security, infra upgrade, spot, configuration, K8s version - along with what has already been tried on each.",
    whenToUse: '"What rightsizing recommendations are open?", "has anyone acted on this one?", "show me security recommendations for this cluster".',
    why: 'The resolution history is the part that matters: it shows the PRs, tickets, deployment changes and previous attempt outcomes attached to a recommendation, so you do not re-litigate something the team already rejected or already shipped.',
    notFor: 'A full cost investigation with evidence and follow-through (FinOps Cost Advisor).',
  },
  cost_optimizer: {
    brief:
      'Reviews a finished {brand} conversation and says how to run it cheaper: which calls could use a lighter model, which agents were redundant, where retries burned spend. Give it a session ID.',
    whenToUse: 'After an expensive or slow conversation, and when tuning an automation that runs often. Input is the session ID, not a question.',
    why: 'This is about the cost of the AI, not the cost of your cloud - the one agent in this catalogue whose subject is {brand} itself. It is how you tune expensive agent flows before rolling them out widely.',
    notFor:
      'Cloud bills and infrastructure savings - that is the FinOps Cost Advisor. The similar names are the single most common mix-up in this list.',
  },
  remediation: {
    brief: 'Owns the whole fix: drafts a remediation plan, takes your edits to it, and runs the approved commands with safety checks.',
    whenToUse:
      'Once you know what the fix is and want it applied: restart, scale, roll back, patch a config. Also when you want the plan written down for review before anything runs.',
    why: 'It keeps the plan and the execution in one place, so what gets run is the thing you approved - with your modifications - rather than a fresh improvisation. Safety checks gate the commands that need them.',
    notFor: 'Diagnosis - bring a cause with you (Kubernetes Troubleshooter, Log Investigator). Repeatable scheduled work (Automation Manager).',
  },
  automation: {
    brief:
      'Runs and manages automations: list, inspect, trigger, pause, resume, version, publish and roll back - and delegates construction of new ones to the Automation Builder.',
    whenToUse: '"Run the node-drain automation", "why did last night\'s run fail?", "pause this automation", "roll back to the previous version".',
    why: 'It is the control plane for everything already built: execution history, retriggering a failed run, dry runs, and full version management including making a version live or restoring an old one.',
    notFor: 'Designing a new automation from scratch - it hands that to the Automation Builder.',
  },
  automation_builder: {
    brief:
      'Builds new automation definitions through a plan-then-build flow: extract intent, propose a plan for your approval, build, then validate in a loop.',
    whenToUse: 'Creating a new automation, or substantially reworking one. Usually reached through the Automation Manager rather than directly.',
    why: 'The approval gate before building is the point - you see and correct the plan before any definition is written, and the validation loop means what it hands back actually runs.',
    notFor: 'Running or managing existing automations (Automation Manager).',
  },
  security: {
    brief: 'Answers security questions and runs scans on demand - container image scans and CIS benchmark scans - then explains the results.',
    whenToUse: '"What vulnerabilities are in this image?", "run a CIS scan on this cluster", "are we exposed to this CVE?"',
    why: 'It both reads existing findings and triggers new scans, and explains output in the context of your question rather than dumping a raw report.',
    notFor: 'Fixing the finding - route that to Remediation, Code Analyzer & Fixer, or a ticket.',
  },
  tickets_v2: {
    brief:
      'Creates and manages tickets across Jira, GitHub, GitLab, ServiceNow, PagerDuty, ZenDuty and Freshdesk - create, list, comment, read comments, fetch details.',
    whenToUse: '"Raise a Jira ticket for this OOM", "list open tickets in PROJ", "add a comment to PROJ-123", "what does this ticket say?"',
    why: 'One agent for every tracker you use, so the same request works regardless of platform. It also asks you for missing details rather than guessing project keys or fields.',
    notFor: 'Nothing significant - this supersedes the older Jira-only agent.',
  },
  tickets: {
    brief: 'Searches Jira issues, updates fields such as priority, status and labels, and adds comments.',
    whenToUse: 'Only where an existing flow already depends on it, or for Jira field updates the newer agent does not cover.',
    why: 'It predates the multi-platform Ticket Manager and is Jira-only. Its one remaining edge is direct field updates on existing issues.',
    notFor: 'New work - prefer the Ticket Manager, which covers Jira plus six other platforms.',
  },
  server: {
    brief: 'An SRE for individual Linux, macOS and Windows machines, working through the shell.',
    whenToUse: '"Is this host out of disk?", "why did the service not start?", "check the process list on this box".',
    why: 'Not everything runs in Kubernetes. This is the agent for a plain VM or bare-metal host: disk, processes, services, packages, connectivity - the questions kubectl cannot answer.',
    notFor: 'Anything inside a cluster (Kubernetes Troubleshooter). Cloud control-plane questions (AWS / GCP / Azure Troubleshooter).',
  },
  websearch: {
    brief: 'Searches internal documentation, skills and the web, or reads a specific page, to answer a question.',
    whenToUse: '"Do we have a runbook for this?", "is this Kubernetes error a known upstream bug?", "read this vendor page".',
    why: 'Selects relevant sources and searches them in parallel. When sources conflict, it prioritizes internal documentation in the answer.',
    notFor: 'Live system state - it searches documents, not your infrastructure.',
  },
  visualizer: {
    brief:
      'Produces Mermaid.js diagrams - flowcharts, architecture diagrams, timelines, and line, bar and pie charts - from a description or a data flow.',
    whenToUse: '"Draw the request flow", "chart this as a timeline", "diagram these dependencies".',
    why: 'It turns an explanation into something you can put in a doc, a ticket or a postmortem. Incident timelines and architecture sketches are its strongest use.',
    notFor: 'Live metric charts - the Metrics Analyst and Prometheus Metrics Expert draw those from real data.',
  },
  toolllm: {
    brief: 'Passes your question and context to the model and returns a complete answer with nothing dropped.',
    whenToUse: 'Composing or reformatting an answer from data you already have. Mostly used as a final step by other agents.',
    why: 'It is a pass-through for when no tool is required and completeness matters - summarising, reformatting, or reasoning over data another agent already gathered. It has no tools of its own, so the quality of what you get back is entirely the quality of what you put in.',
    notFor: 'Anything that needs live data - it cannot fetch anything.',
  },
  prompt_refinement: {
    brief: 'Rewrites a prompt to be clearer, more specific and more effective, and hands back the improved version.',
    whenToUse: 'When an agent keeps misunderstanding you, and when writing a prompt that will live inside an automation.',
    why: 'Most weak agent answers come from vague prompts. This agent fixes the input rather than the output, which is the cheaper place to fix it - especially for prompts saved into automations and run repeatedly.',
    notFor: 'Answering the question itself - it returns a better prompt, not a result.',
  },
  clarification: {
    brief: 'Stops and asks you for a confirmation or a missing detail.',
    whenToUse: 'Called by other agents. You would not normally pick it yourself.',
    why: 'It is how an agent flow pauses for a human instead of guessing - the safety valve before a destructive action or an ambiguous choice.',
    notFor: 'Doing any actual work.',
  },

  // ---- Legacy agents (not in the catalog sheet; original shape) ------
  events_rca_report: {
    whenToUse: 'An incident is over and you need a written root-cause analysis for it.',
    example: '"Write the RCA report for event 4f21c9."',
    why: 'Generates a full RCA report for a given event ID.',
    advantages: ['Turns raw evidence into a shareable report', 'Consistent RCA structure every time'],
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
};

/** Lookup helper -- normalises the agent name the API returns. */
// The catalog copy above carries a `{brand}` placeholder rather than a literal
// product name, and it is filled in HERE rather than in the table. The table is
// a module-level constant: anything it interpolated would be evaluated at import
// time, before /api/public/app_config resolves, and would latch the house brand
// forever for a white-label tenant (same class as the `NUDGEBEE SYSTEM AGENT`
// badge latch). Substituting inside the getter makes it a call-time read.
export const getAgentUsageGuidance = (agentName?: string): AgentUsageGuidance | undefined => {
  const entry = agentName ? AGENT_USAGE_GUIDANCE[agentName.toLowerCase()] : undefined;
  if (!entry) return undefined;
  return {
    ...entry,
    brief: entry.brief ? fillBrandTokens(entry.brief) : entry.brief,
    whenToUse: fillBrandTokens(entry.whenToUse),
    why: fillBrandTokens(entry.why),
    notFor: entry.notFor ? fillBrandTokens(entry.notFor) : entry.notFor,
    example: entry.example ? fillBrandTokens(entry.example) : entry.example,
    advantages: entry.advantages ? entry.advantages.map(fillBrandTokens) : entry.advantages,
  };
};
