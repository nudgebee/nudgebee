package tasks

// SearchAliases maps task names to synonym terms used by the workflow builder's
// "Add Action" search bar. Each entry lets users discover a task by the
// integration or common name they're thinking of (e.g. typing "postgres" or
// "sql" surfaces dbms.query; typing "slack" or "ms_teams" surfaces the
// notifications tasks).
//
// Both short and long forms are listed so that substring matching on the
// frontend works whichever way the user types ("postgres" vs "postgresql",
// "ms teams" vs "microsoft teams").
var SearchAliases = map[string][]string{
	// Core
	"core.approval":      {"approval", "approve", "manual approval", "permission", "gate", "review step"},
	"core.call-workflow": {"call workflow", "sub workflow", "subworkflow", "invoke workflow", "nested workflow"},
	"core.foreach":       {"foreach", "for each", "loop", "iterate", "iteration", "parallel", "map"},
	"core.group":         {"group", "block", "bundle", "parallel group"},
	"core.print":         {"print", "log", "echo", "debug", "console"},
	"core.switch":        {"switch", "if", "if else", "branch", "condition", "conditional", "case"},
	"core.wait":          {"wait", "sleep", "delay", "pause", "timeout"},

	// Data
	"data.filter":    {"filter", "where", "jsonata", "select", "reduce list"},
	"data.transform": {"transform", "map", "reshape", "jsonata", "javascript", "js", "yaml parse", "yaml serialize", "json parse", "convert"},

	// Database
	"dbms.query": {
		"sql", "postgres", "postgresql", "psql",
		"mysql", "mssql", "sql server", "microsoft sql",
		"oracle", "clickhouse", "database", "db", "select query",
	},
	"dbms.redis.cli": {"redis", "cache", "key value", "kv", "memcache"},

	// Notifications (chat / messaging)
	"notifications.im": {
		"slack", "ms_teams", "ms teams", "microsoft teams", "teams",
		"gchat", "google chat", "chat message", "channel message",
		"instant message", "notify", "send message",
	},
	"notifications.dm":           {"slack", "direct message", "dm", "private message", "user message"},
	"notifications.email":        {"email", "mail", "smtp", "gmail", "outlook", "send email", "notify"},
	"notifications.add_reaction": {"slack", "ms_teams", "ms teams", "teams", "gchat", "google chat", "reaction", "emoji", "react"},
	"notifications.read_thread":  {"slack", "thread", "read thread", "replies", "conversation"},
	"slack.join_channel":         {"slack", "join", "channel", "add bot"},

	// Observability
	"observability.logs": {
		"logs", "log query", "cloudwatch", "aws_cloudwatch",
		"elasticsearch", "elastic", "datadog", "dynatrace",
		"newrelic", "new relic", "splunk", "k8s logs",
		"azure monitor", "stackdriver", "gcp logs",
	},
	"observability.log_groups": {"log groups", "cloudwatch", "log streams", "azure monitor", "stackdriver"},
	"observability.metrics": {
		"metrics", "prometheus", "promql", "cloudwatch metrics",
		"datadog", "dynatrace", "newrelic", "new relic", "splunk",
		"grafana", "chart", "graph",
	},
	"observability.traces": {
		"traces", "tracing", "distributed tracing", "datadog apm",
		"dynatrace", "newrelic", "new relic", "splunk",
		"jaeger", "tempo", "apm", "otel", "opentelemetry",
	},

	// Tickets & incidents
	"tickets.create":       {"create ticket", "new ticket", "jira", "github issue", "gitlab issue", "pagerduty", "zenduty", "servicenow", "incident create"},
	"tickets.update":       {"update ticket", "edit ticket", "jira", "github", "gitlab", "pagerduty", "zenduty", "servicenow"},
	"tickets.get":          {"get ticket", "fetch ticket", "read ticket", "jira", "github", "gitlab", "pagerduty", "zenduty", "servicenow"},
	"tickets.get_comments": {"get comments", "fetch comments", "ticket comments", "jira", "github", "gitlab", "pagerduty", "zenduty", "servicenow"},
	"tickets.add_comment":  {"add comment", "comment", "reply", "jira", "github", "gitlab", "pagerduty", "zenduty", "servicenow"},
	"tickets.assign":       {"assign", "assignee", "jira", "github", "gitlab", "pagerduty", "zenduty", "servicenow"},
	"tickets.transition":   {"transition", "change status", "status", "move state", "jira workflow", "jira", "github", "gitlab", "pagerduty", "zenduty", "servicenow"},
	"tickets.acknowledge":  {"acknowledge", "ack", "pagerduty", "zenduty", "incident ack"},
	"tickets.escalate":     {"escalate", "pagerduty", "zenduty", "incident escalate"},
	"tickets.resolve":      {"resolve", "close incident", "pagerduty", "zenduty", "incident resolve"},

	// AI / LLM
	"llm.summary":           {"summary", "summarize", "tldr", "ai summary", "llm summary", "claude", "gpt", "openai", "anthropic"},
	"llm.investigate":       {"investigate", "analyze", "ai investigation", "claude", "gpt", "root cause"},
	"llm.nubi":              {"nubi", "nudgebee ai", "ask ai", "infra ai", "investigate"},
	"llm.router":            {"router", "ai router", "route", "classify", "branch with ai", "decide"},
	"llm.classify":          {"classify", "classification", "categorize", "category", "ai classify"},
	"llm.event_investigate": {"event investigate", "alert investigate", "investigate event", "ai"},
	"llm.mcp_call":          {"mcp", "model context protocol", "mcp tool", "tool call"},
	"llm.a2a_call":          {"a2a", "agent to agent", "external agent", "agent call", "json rpc agent"},

	// Kubernetes
	"k8s.cli":                    {"kubectl", "kubernetes", "k8s", "cluster"},
	"k8s.vertical_rightsize":     {"vertical rightsize", "vpa", "cpu limits", "memory limits", "resize pod", "request limits"},
	"k8s.horizontal_rightsize":   {"horizontal rightsize", "hpa", "replicas", "scale out", "scale in"},
	"k8s.pv_rightsize":           {"pv rightsize", "persistent volume", "pvc", "storage resize", "disk"},
	"k8s.continuous_rightsize":   {"continuous rightsize", "autoscale", "continuous optimization"},
	"k8s.pod_delete":             {"delete pod", "kill pod", "restart pod", "pod"},
	"k8s.workload_restart":       {"restart deployment", "restart statefulset", "rollout restart", "restart workload"},
	"k8s.node_graceful_shutdown": {"drain node", "node drain", "cordon", "shutdown node", "evict node"},

	// Cloud
	"cloud.aws.cli":   {"aws", "aws cli", "ec2", "s3", "lambda", "iam", "cloudformation", "amazon"},
	"aws.cli":         {"aws", "aws cli", "ec2", "s3", "lambda", "iam", "cloudformation", "amazon"},
	"cloud.azure.cli": {"azure", "az cli", "microsoft cloud"},
	"azure.cli":       {"azure", "az cli", "microsoft cloud"},
	"cloud.gcp.cli":   {"gcp", "gcloud", "google cloud"},
	"gcp.cli":         {"gcp", "gcloud", "google cloud"},
	"cloud.k8s.cli":   {"kubectl", "kubernetes", "k8s", "eks", "aks", "gke"},

	// Source control
	"scm.github.cli": {"github", "gh", "git", "pull request", "pr", "issue", "release"},
	"scm.gitlab.cli": {"gitlab", "git", "merge request", "mr", "pipeline"},

	// CI/CD
	"cicd.argocd.cli": {"argocd", "argo", "gitops", "deployment", "sync", "rollout"},

	// Message queue
	"mq.rabbitmqadmin.cli": {"rabbit", "rabbitmq", "amqp", "queue", "message queue", "broker"},

	// Network
	"network.ping":       {"ping", "icmp", "reachability"},
	"network.tcp":        {"tcp", "port check", "socket", "connectivity"},
	"network.ssl":        {"ssl", "tls", "certificate", "cert", "https check"},
	"network.dns":        {"dns", "nslookup", "resolve", "domain", "hostname"},
	"network.ntp":        {"ntp", "time", "clock"},
	"network.whois":      {"whois", "domain info", "ip info"},
	"network.traceroute": {"traceroute", "tracert", "hop", "path"},

	// Cryptography
	"crypto.encode":  {"encode", "base64", "hex", "url encode", "encoding"},
	"crypto.decode":  {"decode", "base64", "hex", "url decode", "decoding"},
	"crypto.hash":    {"hash", "sha256", "sha1", "md5", "checksum", "digest"},
	"crypto.encrypt": {"encrypt", "aes", "rsa", "encryption", "cipher"},
	"crypto.decrypt": {"decrypt", "aes", "rsa", "decryption"},

	// Integrations
	"integrations.http": {"http", "https", "rest", "api", "webhook", "curl", "endpoint", "request"},
	"integrations.ssh":  {"ssh", "remote shell", "bash remote", "remote command"},

	// Scripting
	"scripting.run_script": {"script", "bash", "shell", "python", "run script", "command", "ssm", "run command"},

	// Events
	"events.store": {"store event", "audit", "record event", "save event", "log event"},
}

// AliasesFor returns the search synonyms registered for a task, or nil when
// the task has no aliases.
func AliasesFor(taskName string) []string {
	return SearchAliases[taskName]
}
