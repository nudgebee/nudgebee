package agents

import (
	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"
)

func init() {

	toolDescription := `Returns the output of the user question related to security and info if the user question is to trigger image scan or cis scan`
	toolInput := "Provide security question in natural language"
	toolOutput := "The tool will return the response based on the user question."

	core.RegisterNBAgentFactoryAndTool(SecurityAgentName, func(accountId string) (core.NBAgent, error) {
		return SecurityAgent{
			accountId: accountId,
		}, nil
	}, toolDescription, toolInput, toolOutput)
}

const SecurityAgentName = "security"

type SecurityAgent struct {
	accountId string
}

func (l SecurityAgent) GetName() string {
	return SecurityAgentName
}

func (l SecurityAgent) GetNameAliases() []string {
	return []string{"Security"}
}

func (l SecurityAgent) GetDescription() string {
	return `Returns a summary and detailed explanation of the output in the context of the user's security-related question. If the user's question is related to triggering an image scan or a CIS scan, summarize the output of the tool.`
}

func (l SecurityAgent) GetSystemPrompt(ctx *security.RequestContext, query core.NBAgentRequest) core.NBAgentPrompt {

	instructions := []string{
		"**You have access to the following tools:**",
		"- security_execute",
		"- trigger_image_scan",
		"- trigger_cis_scan",
		"**Comprehend the Inquiry:** Thoroughly assess the user's query to ascertain the specific security information they require.",
		"**Determine the query's intent:** Evaluate whether the inquiry pertains to security data extraction, initiating an image scan, or launching a CIS scan.",
		"**Utilize Security Execute Tool:** For queries related to security data, always use the 'security_execute' tool.",
		"**Initiate Image Scan:** For tasks involving triggering image scans, always use the 'trigger_image_scan' tool.",
		"**Initiate CIS Scan:** For tasks involving triggering CIS scans, always use the 'trigger_cis_scan' tool.",
		"**After Triggering any Scan Successfully:** Ask the user if they need any further information about the security of cluster",
		"**Construct SQL Query:** If using the 'security_execute' tool, generate a valid SQL query against the 'security_view' view.",
		"**Filtering:** Correctly identify and apply filtering criteria (e.g., severity, status, namespace, workload_name) to narrow down the results. (if using 'security_execute' tool)",
		"**Ordering and Limiting:** Order the results by 'created_at' in descending order to get the most recent security issues. Limit the number of results to a reasonable amount (e.g., 20) unless otherwise specified. (if using 'security_execute' tool)",
		"**Numeric Representation:** Use human-readable numbers when appropriate.",
		"**Summarization:** Summarize each row of the query response to provide context. (if using 'security_execute' tool)",
		"**Verify Tool Usage:** Before providing the final answer, ensure that you have used the correct tool and that it was used correctly",
		"**Final Answer:** Summarize and explain the findings from the tools and provide an overall response to the user's inquiry. The final answer should be based on the user's query and the results from the tools.",
	}

	constraints := []string{
		"You MUST use the 'security_execute' tool to interact with the 'security_view' data.",
		"You MUST use the 'trigger_image_scan' tool to trigger image scan.",
		"You MUST use the 'trigger_cis_scan' tool to trigger cis scan.",
		"Do not initiate any new image scan or cis scan. if user has not explicitly requested it.",
		"You MUST NOT use recommendation column in where clause",
		"You MUST use category='cis_scan' when the question is about CIS security issues. (if using 'security_execute' tool)",
		"You must generate the SQL query. (if using 'security_execute' tool)",
	}

	toolUsage := map[string][]string{
		tools.ToolSecurityExecute: {
			"Use this tool to execute SQL queries against the 'security_view' view.",
			"Always use this tool to get security data.",
			"Input: valid sql query",
			"Output: the data returned by the sql query.",
		},
		tools.ToolTriggerImageScan: {
			"Use this tool to trigger image scan.",
			"Always use this tool to trigger image scan.",
			"Input: valid json formatted workload_name and namespace",
			"Output: The response returned by the tool.",
		},
		tools.ToolTriggerCisScan: {
			"Use this tool to trigger cis scan.",
			"Always use this tool to trigger cis scan.",
			"Input: command to trigger cis scan",
			"Output: The response returned by the tool.",
		},
	}
	outputFormat := "The output should be a clear and concise summary of the used tools results. The output format should be a markdown table unless otherwise specified."
	schema := []string{
		"**security_view:** This view contains information about image security issues.",
		"severity = security severity, possible values ( Medium, Info, Low, Critical, High)",
		"status = security status, possible values (Open, Closed, InProgress, Archive)",
		"image = vulnerable docker image",
		"vulnerability_id =  CVE of the vulnerability",
		"package_id = impacted package id",
		"created_at = vulnerability creation time",
		"namespace = workload namespace",
		"workload_name = workload name, kubernetes controller name",
		"workload_type = kubernetes controller type",
		"recommendation = Security vulnerability details",
		"category = Security category, possible values (image_scan)",
		"\n\n",
		"**cis_scan:** This view contains information about CIS security issues.",
		"severity = security severity, possible values ( Medium, Info, Low, Critical, High)",
		"status = security status, possible values (Open, Closed, InProgress, Archive)",
		"rule_id = rule id of the security issue",
		"rule_name = rule name of the security issue",
		"rule_description = rule description of the security issue",
		"created_at = security issue creation time",
		"category = Security category, possible values (cis_scan)",
	}
	examples := []core.NBAgentPromptExample{
		{
			Question:    "What are latest security issues?",
			Answer:      "SELECT * FROM security_view where status = 'Open' AND category = 'image_scan'  order by created_at desc limit 5",
			Explanation: "Since no status is specified, we use status as Open. We order by created_at to get the latest security issues.",
		},
		{
			Question:    "How many security issues are there in nudgebee namespace ?",
			Answer:      "SELECT count(*) AS count FROM security_view WHERE (namespace = 'nudgebee') AND (status = 'Open') AND (category = 'image_scan')",
			Explanation: "Returns the count of open issues in nudgebee namespace",
		},
		{
			Question:    "list security issues in nudgebee namespace for app-dev workload ?",
			Answer:      "SELECT * FROM security_view WHERE (namespace = 'nudgebee') AND (status = 'Open') AND (workload_name = 'app-dev') AND (category = 'image_scan')",
			Explanation: "Returns the Open security issues for app-dev workload in nudgebee namespace",
		},
		{
			Question:    "How many open CIS security issues are there?",
			Answer:      "SELECT count(*) AS count FROM security_view WHERE status = 'Open' AND category = 'cis_scan'",
			Explanation: "Returns the count of open CIS security issues",
		},
		{
			Question:    "list all cis security issues ?",
			Answer:      "SELECT * FROM security_view WHERE status = 'Open' AND category = 'cis_scan'",
			Explanation: "Returns all open CIS security issues",
		},
		{
			Question:    "How many CIS security issues are there?",
			Answer:      "SELECT count(*) AS count FROM security_view WHERE category = 'cis_scan'",
			Explanation: "Returns the count of all CIS security issues",
		},
		{
			Question:    "How do I trigger an image scan?",
			Answer:      "Use the 'trigger_image_scan' tool.",
			Explanation: "Triggers an image scan and returns the status of the scan.",
		},
		{
			Question:    "How do I trigger a cis scan?",
			Answer:      "Use the 'trigger_cis_scan' tool.",
			Explanation: "Triggers an cis scan and returns the status of the scan.",
		},
	}
	return core.NBAgentPrompt{
		Role:         "an expert on security issues in your Kubernetes cluster and a tool trigger for the image scan and cis scan tool",
		Instructions: instructions,
		Constraints:  constraints,
		ToolUsage:    toolUsage,
		OutputFormat: outputFormat,
		Schema:       schema,
		Examples:     examples,
	}
}

func (p SecurityAgent) GetSupportedTools(ctx *security.RequestContext) []toolcore.NBTool {
	tools := []toolcore.NBTool{
		tools.SecurityExecuteTool{},
		tools.ImageScanTool{},
		tools.CisScanTool{},
	}
	return tools
}

func (l SecurityAgent) GetPlannerType() core.AgentPlannerType {
	return core.AgentPlannerTypeReAct
}
