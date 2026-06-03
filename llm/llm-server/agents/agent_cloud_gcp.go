package agents

import (
	"nudgebee/llm/agents/core"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"
)

// Tool/Agent Constants
const GcpAgentName = "gcp"

func init() { // This describes the 'gcp' agent when it is used as a tool by another agent (e.g., gcp_debug).
	toolDescription := `Interacts with Google Cloud Platform (GCP) services to retrieve information or perform actions using the gcloud CLI. Covers all GCP capabilities: Compute, GKE, Cloud Storage, Cloud SQL, Cloud Run, Cloud Functions, Cloud Logging, Cloud Monitoring, Pub/Sub, IAM, Billing, and more. This tool is "smart" and handles its own project and resource discovery — use it for any GCP investigation without needing separate reconnaissance. Returns gcloud CLI output and summaries.`
	toolInput := "Natural Language query about GCP resources or operations."
	toolOutput := "Output of gcloud Cli tool"
	core.RegisterNBAgentFactoryAndTool(GcpAgentName, func(accountId string) (core.NBAgent, error) {
		return newGcpAgent(accountId), nil
	}, toolDescription, toolInput, toolOutput)
}

func newGcpAgent(accountId string) core.NBAgent {
	return GcpAgent{
		accountId: accountId,
	}
}

type GcpAgent struct {
	accountId string
}

func (a GcpAgent) GetName() string {
	return GcpAgentName
}

func (a GcpAgent) GetNameAliases() []string {
	return []string{"GCP"}
}

func (a GcpAgent) GetDescription() string {
	return `Interacts with GCP services using the gcloud CLI. Covers all GCP capabilities: Compute, GKE, Cloud Storage, Cloud SQL, Cloud Run, Cloud Functions, Cloud Logging, Cloud Monitoring, Pub/Sub, IAM, Billing, and more. This tool is "smart" and handles its own project and resource discovery — use it for any GCP investigation without needing separate reconnaissance. Returns gcloud CLI output and summaries.`
}

func (a GcpAgent) GetSupportedTools(ctx *security.RequestContext) []toolcore.NBTool {
	tools := []toolcore.NBTool{tools.GcpCliTool{}}
	return tools
}

func (a GcpAgent) GetSystemPrompt(ctx *security.RequestContext, query core.NBAgentRequest) core.NBAgentPrompt {

	instructions := []string{
		"**Tool Name:** Your registered tool name is `gcp`. Do NOT emit variants like `gcp_cloud_logging`, `gcp_cloud_monitoring`, `gcp_logging`, `gcp_monitoring`, or `google_cloud` — the planner will fail to resolve them.",
		"**gcloud CLI Interaction:** Always use the gcloud CLI for interacting with GCP services. Avoid making assumptions about the user's GCP configuration.",
		"**Project Already Configured:** The GCP project is already pre-configured from the authenticated account credentials (via environment variable). **Do NOT run `gcloud config set project` before your commands** — it is unnecessary and will only slow down execution. Pass `--project <project_id>` as a flag directly in each command when needed.",
		"**Project and Zone/Region Awareness:** Pay attention to project, zone, and region context. If the user specifies a project, zone, or region, use it. If not, try to infer them from the query context. If no project, zone, or region can be determined, explicitly ask the user for clarification.",
		"**Resource Identification:** Clearly identify the GCP resources the user is asking about (e.g., Compute Engine instances, Cloud Storage buckets, Cloud Functions).",
		"**Command Construction:** Construct valid and efficient gcloud CLI commands. Avoid unnecessary flags or options.",
		"**Google Cloud Storage:** When working with Cloud Storage buckets, always use the 'gs://' prefix for bucket names in commands like 'gcloud storage buckets get-iam-policy' or 'gsutil acl get'.",
		"**Command Usage:** Use 'gcloud' for most operations. For Cloud Storage ACLs, prefer using 'gsutil acl get'.",
		"**Billing and Cost Management:** When asked about billing for a specific service, use 'gcloud billing skus list' and filter the results. For example, to check Gemini billing, you can use 'gcloud billing skus list | grep -i gemini'. This approach helps in identifying service-specific costs by looking at the relevant SKUs.",
		"**Service Discovery:** To discover available services or check for enabled APIs, use the 'gcloud services list' command. This is useful for understanding the available service landscape in the project.",
		"**Output Formatting:** Format the gcloud CLI output for readability, especially when dealing with large amounts of data. Consider using 'table' output format whenever possible.",
		"**Security Best Practices:** Avoid exposing sensitive information like service account keys in your commands or responses.",
		"**Error Handling and Self-Correction:** When a tool returns an error, carefully analyze the `Stderr` output.",
		"- If the error message provides a clear suggestion for how to fix the command (e.g., correcting a format string, adding a missing flag), you MUST try to correct the command and execute it again in your next step.",
		"- If the error is a permission error, identify the missing permission and inform the user. **CRITICAL: NEVER attempt to modify your own IAM permissions or bindings to gain access.** Do NOT run commands like `gcloud projects add-iam-policy-binding`, `gcloud projects set-iam-policy`, or `gsutil iam set` to grant yourself access. Report the missing permission as a finding.",
		"- If the cause of the error is unclear, use the `--help` flag on the command to get more information before trying again.",
		"**Service Account Impersonation:** Utilize 'gcloud auth impersonate-service-account' when necessary to access resources with different service accounts. If you need to switch service accounts, make sure to explicitly mention this to the user.",
		"**Cost Optimization:** Be mindful of cost optimization best practices when suggesting solutions. For example, when dealing with Cloud Storage, consider lifecycle policies or different storage classes.",
		"**Cloud Trace (primary read path):** No GA gcloud command — use the Cloud Trace v1 REST API: `curl -H \"Authorization: Bearer $(gcloud auth print-access-token)\" 'https://cloudtrace.googleapis.com/v1/projects/PROJECT_ID/traces?pageSize=20&startTime=ISO_TIMESTAMP'` to list, append `/TRACE_ID` to describe. Requires `roles/cloudtrace.user` and the `cloud-platform` or `trace.readonly` OAuth scope; `ACCESS_TOKEN_SCOPE_INSUFFICIENT` means the scope is missing, not that traces don't exist.",
		"**Cloud Trace via Cloud Logging (correlation path):** Use `gcloud logging read` with `trace=\"projects/PROJECT_ID/traces/TRACE_ID\"` to pull logs for a known trace, or `httpRequest.latency > \"500ms\"` for slow HTTP requests. This only surfaces traces whose services emit the `trace` field into logs — empty results mean \"no trace-correlated logs\" (check the Trace API above), not \"no traces.\" Always scope with `resource.type`, `timestamp >=`, `--limit`, and `--project`.",
		"**Cloud Trace fallback:** If both paths are empty or unauthorized, don't conclude the system is healthy — fall back to `severity>=ERROR`, latency/timeout patterns in textPayload (e.g., Postgres `duration:` on `cloudsql_database`), and narrow by resource labels (service, revision, pod, instance) in the recent window.",
		"**Best practices**: Use best practices for all GCP services such as IAM, Cloud Storage, Compute Engine, Cloud Functions, Cloud Run, GKE etc.",
		"**State clear assumptions**: If you are making any assumptions, please state clearly in the response.",
	}

	if config.Config.LlmServerShellToolEnabled {
		instructions = append(instructions, core.GetWorkspaceInstructions()...)
	}

	constraints := []string{
		"Do not ask for any clarification from the user, try to resolve using the available tools",
		"If a gcloud command fails, try to use the --help flag to understand the correct usage and fix the command.",
		"CRITICAL for stability: Always use limits (e.g., `--limit 50`) and specific filters when querying high-volume resources like logs, events, or large lists of instances to prevent context saturation and latency stalls.",
		"When multiple resources could match the user's query, prefer a broader-scoped query with filters (resource.type, labels, time) over guessing a single resource — guessing wrong wastes a step and produces empty results.",
	}
	toolUsage := map[string][]string{
		tools.ToolExecuteGcpCliCommand: {
			"You can use **gcloud_execute** to execute gcloud cli commands.",
			"Use this tool to interact with GCP, always prefer this tool over other tools",
		},
	}

	examples := []core.NBAgentPromptExample{
		{
			Question: "List all my compute instances",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolExecuteGcpCliCommand,
					Input: "gcloud compute instances list --format=table",
				},
			},
		},
		{
			Question: "Show details for my instance named 'my-instance' in zone 'us-central1-a'",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolExecuteGcpCliCommand,
					Input: "gcloud compute instances describe my-instance --zone=us-central1-a",
				},
			},
		},
		{
			Question: "List all my storage buckets",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolExecuteGcpCliCommand,
					Input: "gcloud storage buckets list",
				},
			},
		},
		{
			Question: "List all my billing accounts",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolExecuteGcpCliCommand,
					Input: "gcloud billing accounts list --format=table",
				},
			},
		},
		{
			Question: "List all my projects",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolExecuteGcpCliCommand,
					Input: "gcloud projects list --format=table",
				},
			},
		},
		{
			Question: "List all my GKE clusters",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolExecuteGcpCliCommand,
					Input: "gcloud container clusters list --format=table",
				},
			},
		},
		{
			Question: "Find slow traces for my Cloud Run service in the last hour",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolExecuteGcpCliCommand,
					Input: `gcloud logging read 'resource.type="cloud_run_revision" resource.labels.service_name="my-service" httpRequest.latency>"500ms" timestamp>="'"$(date -u -d '1 hour ago' +%Y-%m-%dT%H:%M:%SZ)"'"' --project=MY_PROJECT --limit=20 --format=json`,
				},
			},
		},
		{
			Question: "List SKUs for Gemini services",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolExecuteGcpCliCommand,
					Input: "gcloud billing skus list | grep -i gemini",
				},
			},
		},
	}

	promptTemplate := core.NBAgentPrompt{
		Role:         "an expert GCP administrator and SRE",
		Instructions: instructions,
		Constraints:  constraints,
		ToolUsage:    toolUsage,
		Examples:     examples,
	}

	return promptTemplate
}

func (a GcpAgent) GetPlannerType() core.AgentPlannerType {
	return core.AgentPlannerTypeReAct
}
