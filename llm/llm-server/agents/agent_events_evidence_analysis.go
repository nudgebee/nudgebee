package agents

import (
	"errors"
	"fmt"
	"log"
	"log/slog"
	"nudgebee/llm/agents/core"
	"nudgebee/llm/common"
	"nudgebee/llm/events"
	"nudgebee/llm/security"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/tmc/langchaingo/llms"
	"github.com/tmc/langchaingo/prompts"
)

type EvidenceMeta struct {
	EvidenceType string `json:"evidenceType"`
	Prompt       string `json:"prompt"`
}

type RCAEventData struct {
	Evidences        RCAInvestigateData `json:"evidences,omitempty"`
	CreatedAt        string             `json:"starts_at,omitempty"`
	EventTitle       string             `json:"title,omitempty"`
	EventDescription string             `json:"description,omitempty"`
	AggregationKey   string             `json:"aggregation_key,omitempty"`
	SubjectType      string             `json:"subject_type,omitempty"`
	SubjectName      string             `json:"subject_name,omitempty"`
	SubjectNamespace string             `json:"subject_namespace,omitempty"`
	SubjectNode      string             `json:"subject_node,omitempty"`
}

var evidenceDataMeta = map[string]EvidenceMeta{
	"ErrorLogData": {
		EvidenceType: "Timeseries",
		Prompt: `
You are an SRE expert specializing in investigating error logs data in Kubernetes environments with keen eye to details. Your primary goal is to analyze timeseries of error logs from the data given in context, Using which the user can understand this evidence.

## Event Being Investigated
{{ .event_context }}
Focus your analysis on findings relevant to this event.

** you always need to follow the steps provided to analyze the data **

always make sure you respond only in the format provided below, with no extra information or text.

** steps to analyze the evidence **
-> Analyze error logs with their time and point out understand the errors line number and file name for the error.
-> the final analysis should be sorted.
-> ignore repetitive logs.

evidence type:-
Error Logs

evidence data format:-
---------------------------------
String Log lines of the pod around the time of the event in the form of string.

Output format
---------------------------------
** Time in format [YYYY-MM-DD HH:mm:ss UTC] **: <Evidence type>   event description


---------------------------------
output examples:-
**2024-12-27 05:32:34 UTC**: <Error log> file was not found for path in rabbitmq.py at line 45
**2024-12-28 01:46:01 UTC**: <Error Log> cast error for string to int in rabbitmq.py at line 45

Context:-
---------------------------------
{{ .context }}`,
	},
	"PodMemory": {
		EvidenceType: "Timeseries",
		Prompt: `
You are an SRE expert specializing in investigating pod memory data for Kubernetes resource with keen eye to details. Your primary goal is to understand the pod memory patterns, and give the analysis for memory usage patterns in the form of time series in sorted order.
Using which the user can understand this memory data.

## Event Being Investigated
{{ .event_context }}
Focus your analysis on findings relevant to this event.

**  Always follow the steps provided to analyze the data **

** IMP **
always make sure you respond only in the format provided below, with no extra information or text.

## Steps to Analyze the Evidence
1. Identify and report only anomalies, trends, or spikes in the data.
2. Understand and describe each anomaly with a short explanation about the anomaly.
3. Include the anomaly description along with the data point in the final time series output.
4. Make sure you always give output in the format provided with no extra information.
5. Only provide data points with significant memory change.

evidence data format:-
---------------------------------
JSON-formatted timeseries data for Pod memory in MB with timestamp and request and limit for pod memory in bytes if set.

Output format
---------------------------------
** Time in format [YYYY-MM-DD HH:mm:ss UTC] **: <metrics type>  pod memory in MB at timestamp


output examples:-
---------------------------------
- **2024-12-28 23:31:55 UTC**: PodMemory usage spiked to 10,342 MB. Increased by 2000 MB.
- **2024-12-28 23:32:38 UTC**: PodMemory usage increased to 11,751 MB. Increased by 1409 MB.

Context:-
---------------------------------
{{ .context }}`,
	},
	"NodeMemory": {
		EvidenceType: "Timeseries",
		Prompt: `
You are an SRE expert specializing in investigating node memory data in Kubernetes environments. Your task is to analyze node memory patterns and provide insights for its memory usage. Your analysis should focus on identifying significant changes or anomalies in the memory, describing the value change, and presenting them in a sorted time series fashion.

## Event Being Investigated
{{ .event_context }}
Focus your analysis on findings relevant to this event.

## Steps to Analyze the Evidence
1. Identify and report only anomalies, trends, or spikes in the data.
2. Understand and describe each anomaly with a short explanation about the anomaly.
3. Include the anomaly description along with the data point in the final time series output.
4. Make sure you always give output in the format provided with no extra information.
5. Only provide data points with significant memory change.

## Input Data Format
- JSON-formatted time-series data for node memory in MB.
- limits: limits set for the resource if set in MB.
- node_memory_requested: requested node memory in MB.
- node_memory_used: used node memory in MB.

## Output Format
- Use the following format with no extra information:
  '''
  **[YYYY-MM-DD HH:mm:ss UTC]**: <metrics type> <Insight about Node memory in MB>
  '''

## Output Result Examples
- **2024-12-28 23:31:55 UTC**: NodeMemory usage spiked to 10,342 MB. Increased by 2000 MB 80% of the limit.
- **2024-12-28 23:32:38 UTC**: NodeMemory usage increased to 11,751 MB. Increased by 1409 MB.


Context:-
---------------------------------
{{ .context }}`,
	},
	"LastDeploymentChange": {
		EvidenceType: "Timeseries",
		Prompt: `
You are an SRE expert specializing in investigating last changes done in a resource in Kubernetes environments with keen eye to details. Your primary goal is to build a detailed analysis of the change describing the changes done in the resource. with there timestamps.

## Event Being Investigated
{{ .event_context }}
Focus your analysis on findings relevant to this event.

always make sure you respond only in the format provided below, with no extra information or text is needed.

generate single timeseries with time as last resource time explaining the changes in the resource.

evidence data format:-
---------------------------------
json-formatted:- YAML-formatted difference between the old and new for last resource configurations change and changes as well.
 last deployment time as deployment_time


Output format
---------------------------------
** Time in format [YYYY-MM-DD HH:mm:ss UTC] **:   change in the resource


---------------------------------
output examples:-
**2024-12-27 05:32:34 UTC**: <resource change> Deployment memory limits changed from 500Mi to 1000Mi.
**2024-12-28 01:46:01 UTC**: <Pod memory> PodMemory usage spiked to 2755 MB.
**2024-12-28 23:31:55 UTC**: <Node Memory> NodeMemory usage spiked to 10,342 MB.
**2024-12-28 23:32:38 UTC**: <Node Memory> NodeMemory usage increased to 11,751 MB.
**2024-12-28 23:32:38 UTC**: <Pod Event> Started container ml-k8s-server.

Context:-
---------------------------------
{{ .context }}`,
	},
	"PodEvents": {
		EvidenceType: "Timeseries",
		Prompt: `
You are an SRE expert specializing in investigating pod event data for Kubernetes resource. Your primary goal is to build a summarized time series analysis for provided pod event, The output should help the user understand the evidence effectively.

## Event Being Investigated
{{ .event_context }}
Focus your analysis on findings relevant to this event.

Always follow the steps provided to analyze the data.

Always respond strictly in the format provided below, without adding any extra information or text.

---

## Steps to Analyze the Evidence:
1. Understand every event and include it in the time series.
2. the resultant time series should be sorted by time.
3. go through the events again to find out any anomaly or insight and provide same in the analysis.
3. provide insights and anomaly in the data.

---

## Evidence Data Format:
A JSON-formatted list of events occurring in the pod.

---

## Output Format:
'**[YYYY-MM-DD HH:mm:ss UTC]**: <Evidence Type> Event description'

** insights for pod event **
- insight 1
|
|


---

## Example Output:
    **2024-12-27 05:32:34 UTC**: <Deployment Change> Deployment memory limits changed from 500Mi to 1000Mi.
    **2024-12-28 01:46:01 UTC**: <Pod Memory> PodMemory usage spiked to 2755 MB.
    **2024-12-28 23:31:55 UTC**: <Node Memory> NodeMemory usage spiked to 10,342 MB.
    **2024-12-28 23:32:38 UTC**: <Node Memory> NodeMemory usage increased to 11,751 MB.
    **2024-12-28 23:32:38 UTC**: <Pod Event> Started container ml-k8s-server.

    ** insights for pod event **
    - the readiness prob failed for pod just after initializing.



Context:-
---------------------------------
{{ .context }}`,
	},
	"NoisyNeighbours": {
		EvidenceType: "Anomalies",
		Prompt: `
You are an SRE expert specializing in diagnosing noisy neighbor data in Kubernetes environments. Your task is to analyze provided  data to detect anomalies or unusual patterns among the pods within a node. Your analysis focuses on understanding the ab normal behavior of pods and identifying potential noisy neighbors based on resource usage.

## Event Being Investigated
{{ .event_context }}
Focus your analysis on findings relevant to this event.

You are given the following information for the node under investigation:
- Total pods
- Node memory used
- Node memory requested
- Node memory allocatable

**Your Response Guidelines:**
1. Analyze all pods within the node and their respective properties.
2. Highlight anomalies or unusual patterns in pod resource usage or configuration.
3. Ensure that your response strictly adheres to the specified format. Avoid adding any extra commentary or information.

**Evidence Type:**  
Noisy Neighbors

**Steps to Analyze the Evidence:**
1. Review the evidence data provided for each pod under the 'neighbours' array.
2. Check for discrepancies between 'memory_requested', 'memory_used', and 'memory_limit'.
3. Note any pods consuming significantly more memory than requested or approaching their memory limit.
4. Assess namespace distribution, ownership, and controller behavior for potential anomalies.
5. Compare resource usage patterns against the node's memory capacity to identify potential noisy neighbors.
6. always provide the output in the specified format.

**Evidence Data Structure Description:**
The 'neighbours' array includes metadata and resource information for each pod in the node. Each entry contains the following fields:
1. **'kind'**: Ownership details, including 'api_version', 'block_owner_deletion', 'controller', 'kind', 'name', and 'uid'.
2. **'memory_limit'**: Memory limit set for the pod in MB ('Not provided' if not set).
3. **'memory_requested'**: Requested memory in MB ('Not provided' if not set).
4. **'memory_used'**: Actual memory usage in MB.
5. **'namespace'**: Namespace of the pod.
6. **'node_name'**: Node name where the pod is running.
7. **'pod_name'**: Pod name.

** output format **
- Node name: node_name
- Number of pods in node: total_pods
- Remaining memory allocatable in node: node_memory_allocatable
- Memory requested: node_memory_requested
- Memory used in node : node_memory_used

Noisy behavior detected in the following pods:
- Pod: [pod_name], Namespace: [namespace]
    - explanation of the anomaly
 |
 |
 |

Examples:-
- Pod: my-agent-abc12, Namespace: my-agent
    - High memory usage (640.39 MB) compared to requested memory (100.00 MB) 80% of total used memory in node.
- Pod: app-server-def34, Namespace: my-agent-staging
    - High memory usage (800.39 MB) compared to requested memory (150.00 MB) 70% of total used memory in node.

**Context:**
---------------------------------
{{ .context }},
		`,
	},
	"PodConfiguration": {
		EvidenceType: "Configuration",
		Prompt: `
	You are an SRE expert in Kubernetes environments with keen eye to details. Your primary goal is to describe the kubernetes configuration for a kubernetes resource, given in the data.

	## Event Being Investigated
	{{ .event_context }}
	Focus your analysis on findings relevant to this event.

	Using which the user can understand the data.
	
	** Things to take care while analysis: **
		- understand the resource configuration carefully first.
		- provide all the configuration provided in the final result.
		- Look for any misconfigurations in the resource configuration and add those to misconfiguration section if found.
	
	always make sure you respond only in the format provided below, with no extra information or text.
	
	evidence data format:-
	---------------------------------
	JSON-formatted kubernetes resource configuration at that time.
	
	
	Output format
	---------------------------------   
	** Property Name ** 
		- property value
	
	example format
	---------------------------------
	** Pod memory **
		- request: 1024 MB
		- limit: 3072 MB
	** Pod cpu **
		- request: 1
		- limit: 3
	
	** misconfigurations in the resource**
	
	Context:-
	---------------------------------
	{{ .context }}
		`,
	},
	"NodeEvents": {
		EvidenceType: "Timeseries",
		Prompt: `
You are an SRE expert specializing in Node event data in Kubernetes environments. Your primary goal is to build a brief time series analysis for provided Node events data, ensuring that the time series is sorted and presented clearly. The output should help the user understand the node events effectively.

## Event Being Investigated
{{ .event_context }}
Focus your analysis on findings relevant to this event.

Always follow the steps provided to analyze the data.

Always respond strictly in the format provided below, without adding any extra information or text.

---

## Steps to Analyze the Evidence:
1. Understand every node event.
2. Include the event to the final analysis, in simple words.

---

## Evidence Type:
**Node Event**

---

## Evidence Data Format:
A JSON-formatted list of events occurring in the pod.

---

## Output Format:
'**[YYYY-MM-DD HH:mm:ss UTC]**: <Evidence Type> Event description'

---

## Example Output:
**2024-12-27 05:32:34 UTC**: <Node event> Deployment memory limits changed from 500Mi to 1000Mi.
**2024-12-28 01:46:01 UTC**: <Node event> PodMemory usage spiked to 2755 MB.
**2024-12-28 23:31:55 UTC**: <Node event> NodeMemory usage spiked to 10,342 MB.
**2024-12-28 23:32:38 UTC**: <Node event> NodeMemory usage increased to 11,751 MB.
**2024-12-28 23:32:38 UTC**: <Node event> Started container ml-k8s-server.

Context:-
---------------------------------
{{ .context }}`,
	},
}

type RCAInvestigateData struct {
	LogData              string
	ErrorLogData         any
	PodMemory            any
	NodeMemory           any
	PodConfiguration     any
	LastDeploymentChange any
	NoisyNeighbours      any
	NodeEvents           any
	PodEvents            any
}

const AgentEvidenceAnalysis = "events_rca_report"

type AgentEventAnalysis struct {
}

const AgentEventAnalysisName = "agent_event_analysis"

func (l AgentEventAnalysis) GetPlannerType() core.AgentPlannerType {
	return core.AgentPlannerTypeReAct
}

func (l AgentEventAnalysis) GetName() string {
	return AgentEventAnalysisName
}

func (a AgentEventAnalysis) GetNameAliases() []string {
	return []string{"Event Analysis"}
}

func (l AgentEventAnalysis) GetDescription() string {
	return `Generate evidence analysis for event_id`
}

func (l AgentEventAnalysis) GetSystemPrompt(ctx *security.RequestContext, query core.NBAgentRequest) core.NBAgentPrompt {

	instructions := []string{
		`

			## Process for Creating an RCA Report

			### 1. Understand the User's Query
			Carefully analyze the user's request to identify the specific event they want investigated and documented in an RCA report.

			### 2. Use the 'executeEventsSql' Tool
			Always use the **'executeEventsSql'** tool to retrieve detailed event data. This step is mandatory to ensure all relevant event details are captured.

			### 3. Validate Query Results
			Confirm that the **'executeEventsSql'** tool has been used correctly and that the retrieved data contains all necessary details to proceed.

			### 4. Understand the Event
			Analyze the retrieved event data to gather critical details such as:
			- Event type
			- Description of the event
			- Consequences of the event
			- The exact time the event occurred

			Focus on understanding the following key fields:
			- **'created_at:'** Timestamp of the event.
			- **'title:'** Title summarizing the event.
			- **'description:'** A detailed explanation of the event.
			- **'subject_type:'** Source type (e.g., 'daemonset', 'deployment', 'node').
			- **'subject_name:'** Name of the Kubernetes resource.
			- **'subject_namespace:'** Namespace of the resource.
			- **'subject_node:'** Node associated with the resource.
			- **'evidences:'** JSON data listing evidences like logs, metrics, and alerts.

			### 5. Perform Evidence Analysis
			Use the **'EvidenceInsightsTool'** to:
			- Analyze evidences retrieved from the event data.
			- Identify logs, metrics, alerts, and other anomalies contributing to the root cause.
			- Generate insights based on the evidence to inform the RCA.

			### 6. Create the Final RCA Report
			Use the **'EvidenceRCATool'** to:
			- Compile evidence analysis into a structured RCA report.
			- Ensure the report includes all relevant findings and actionable insights.

			### 7. Respond with the Final RCA Report
			Provide the user with the final RCA report generated by the **'EvidenceRCATool'**.  
			The report should be:
			- Clear and concise
			- Detailed and evidence-backed
			- Actionable for mitigating future occurrences

			---

			## Event Data Schema

			The event data retrieved using the **'executeEventsSql'** tool will be in JSON format. Key fields to include in your analysis are:

			1. **'created_at (timestamp):'**  
			The exact time when the event was triggered.

			2. **'title (text):'**  
			A short title summarizing the event.

			3. **'description (text):'**  
			A detailed description of the event.

			4. **'subject_type (text):'**  
			The type of resource where the event originated (e.g., 'daemonset', 'deployment', 'horizontalpodautoscaler', 'node', 'pod', 'query', 'statefulset').

			5. **'subject_name (text):'**  
			The name of the Kubernetes resource associated with the event.

			6. **'subject_namespace (text):'**  
			The namespace of the Kubernetes resource.

			7. **'subject_node (text):'**  
			The node associated with the Kubernetes resource.

			8. **'evidences (JSON):'**  
			A list of evidence data related to the event, including logs, metrics, alerts, and other supporting data.			
		`,
	}

	toolUsage := map[string][]string{}

	return core.NBAgentPrompt{
		Role:         "a highly experienced SRE specializing in Kubernetes event evidence investigation",
		Instructions: instructions,
		ToolUsage:    toolUsage,
	}

}

func (p AgentEventAnalysis) GetSupportedTools(ctx *security.RequestContext) []toolcore.NBTool {
	return []toolcore.NBTool{tools.EventsExecuteTool{}, EvidenceInsightsTool{}}
}

const ToolEvidenceInsightsTool = "EvidenceInsightsTool"

func init() {
	toolcore.RegisterNBToolFactory(ToolEvidenceInsightsTool, func(accountId string) (toolcore.NBTool, error) {
		return EvidenceInsightsTool{}, nil // Register Evidence Insights tool
	})
}

type EvidenceInsightsTool struct {
}

func (m EvidenceInsightsTool) Name() string {
	return ToolEvidenceInsightsTool
}

func (m EvidenceInsightsTool) GetType() toolcore.NBToolType {
	return toolcore.NBToolTypeTool
}

func (m EvidenceInsightsTool) Description() string {
	return "Gives insights and time series for event evidence data"
}

func (m EvidenceInsightsTool) InputSchema() toolcore.ToolSchema {
	return toolcore.ToolSchema{
		Type: toolcore.ToolSchemaTypeObject,
		Properties: map[string]toolcore.ToolSchemaProperty{
			"command": {
				Type:        toolcore.ToolSchemaTypeString,
				Description: "events id to analysis",
			},
		},
		Required: []string{"command"},
	}
}

func (m EvidenceInsightsTool) GetEvidenceList(evidenceData map[string]any) []string {
	var evidenceList []string
	for k := range evidenceData["evidences"].(map[string]any) {
		evidenceList = append(evidenceList, k)
	}
	return evidenceList
}

func (m EvidenceInsightsTool) SortTimestamp(nbRequestContext toolcore.NbToolContext, input string, event_description string, event_time *time.Time) (string, error) {
	TIME_SERIES_SORT_PROMPT := `
sort the time series data in ascending order of time.
the output data should only have the time series data only in sorted order in json or text format.
always make sure you respond only in the format provided below, with no extra information or text.

**remember**
output should be a valid json if user specifically ask for json output, else it should be in the normal text format.

also add this event data as well to final time series data.
{{ .event_time }} {{ .event_description }}

don't include all timestamps after {{ .event_time }}

provided data format:-
---------------------------------
for text output
	** Time in format [YYYY-MM-DD HH:mm:ss UTC] ** event description
for json output
	{"2024-12-28 23:11:45 UTC": "PodMemory usage steady at 2755 MB.",

---------------------------------
example 
input data:-
Evidence Timeseries analysis for the event:

**2024-12-28 23:11:45 UTC**: PodMemory usage steady at 2755 MB.
**2024-12-28 23:11:45 UTC**: NodeMemory usage steady at 10086 MB.
**2024-12-28 23:17:02 UTC**: NodeMemory usage increased to 10104 MB.
**2024-12-28 23:13:55 UTC**: NodeMemory usage increased to 10098 MB.
**2024-12-28 23:15:50 UTC**: NodeMemory usage decreased to 10083 MB.

output data:-
Evidence Timeseries analysis for the event:
if asked for json output specifically.
	{"2024-12-28 23:11:45 UTC": "PodMemory usage steady at 2755 MB.",
	"2024-12-28 23:11:45 UTC": "NodeMemory usage steady at 10086 MB.",
	"2024-12-28 23:13:55 UTC": "NodeMemory usage increased to 10098 MB.",
	"2024-12-28 23:15:50 UTC": "NodeMemory usage decreased to 10083 MB.",
	"2024-12-28 23:17:02 UTC": "NodeMemory usage increased to 10104 MB."}
other cases.
	**2024-12-28 23:11:45 UTC**: PodMemory usage steady at 2755 MB.
	**2024-12-28 23:11:45 UTC**: NodeMemory usage steady at 10086 MB.
	**2024-12-28 23:13:55 UTC**: NodeMemory usage increased to 10098 MB.
	**2024-12-28 23:15:50 UTC**: NodeMemory usage decreased to 10083 MB.
	**2024-12-28 23:17:02 UTC**: NodeMemory usage increased to 10104 MB.


context:-
---------------------------------
{{ .context }}

Output format
---------------------------------	
Evidence Timeseries analysis for the event:
text format
	** Time in format [YYYY-MM-DD HH:mm:ss UTC] ** event description
json format
	{"2024-12-28 23:11:45 UTC": "PodMemory usage steady at 2755 MB.",

example format
---------------------------------
Evidence Timeseries analysis for the event:
**2024-12-28 23:11:45 UTC**: PodMemory usage steady at 2755 MB.
**2024-12-28 23:11:45 UTC**: NodeMemory usage steady at 10086 MB.
**2024-12-28 23:13:55 UTC**: NodeMemory usage increased to 10098 MB.
**2024-12-28 23:15:50 UTC**: NodeMemory usage decreased to 10083 MB.
**2024-12-28 23:17:02 UTC**: NodeMemory usage increased to 10104 MB.
 or 
{"2024-12-28 23:11:45 UTC": "PodMemory usage steady at 2755 MB.",
"2024-12-28 23:11:45 UTC": "NodeMemory usage steady at 10086 MB.",
"2024-12-28 23:13:55 UTC": "NodeMemory usage increased to 10098 MB.",
"2024-12-28 23:15:50 UTC": "NodeMemory usage decreased to 10083 MB.",
"2024-12-28 23:17:02 UTC": "NodeMemory usage increased to 10104 MB."}



	`
	finalMessage, formatErr := prompts.NewPromptTemplate(TIME_SERIES_SORT_PROMPT, []string{"context"}).Format(map[string]any{
		"context":           input,
		"event_time":        event_time.Format(time.RFC3339),
		"event_description": event_description,
	})
	if formatErr != nil {
		return "", formatErr
	}
	mc := []llms.MessageContent{llms.TextParts(llms.ChatMessageTypeHuman, finalMessage)}

	completion, err := core.GenerateAndTrackLLMContent(nbRequestContext.Ctx, nbRequestContext.UserId, nbRequestContext.AccountId, nbRequestContext.ConversationId, nbRequestContext.MessageId, AgentEvidenceAnalysis, false, mc, false, llms.WithTemperature(0.0))
	if err != nil {
		nbRequestContext.Ctx.GetLogger().Error("llm: unable to generate content", "error", err)
		return "", core.ErrLlmUnableToGenerate(err)
	}
	content := ""
	if len(completion.Choices) > 0 {
		content = completion.Choices[0].Content
	}
	return content, err
}

func (m EvidenceInsightsTool) getEventData(nbRequestContext toolcore.NbToolContext, event_id string) (events.Event, error) {
	nbRequestContext.Ctx.GetLogger().Info("RCA analyzer: Getting event data", "request", event_id)
	if _, err := uuid.Parse(event_id); err != nil {
		return events.Event{}, fmt.Errorf("invalid event_id format: %w", err)
	}
	if _, err := uuid.Parse(nbRequestContext.AccountId); err != nil {
		return events.Event{}, fmt.Errorf("invalid account_id format: %w", err)
	}
	eventTool := tools.EventsExecuteTool{}
	toolCtx := toolcore.NewNbToolContext(nbRequestContext.Ctx, eventTool, nbRequestContext.AccountId, nbRequestContext.UserId, nbRequestContext.ConversationId, nbRequestContext.MessageId, nbRequestContext.ParentAgentId, event_id, []llms.MessageContent{}, "", toolcore.NBQueryConfig{}, "")
	data, err := eventTool.Call(toolCtx, toolcore.NBToolCallRequest{
		Command: fmt.Sprintf(`select * from events where id = '%s' and cloud_account_id = '%s'`, event_id, nbRequestContext.AccountId),
	})
	if err != nil {
		return events.Event{}, err
	}
	var event []events.Event
	err = common.UnmarshalJson([]byte(data.Data), &event)
	if err != nil {
		return events.Event{}, err
	}
	if len(event) == 0 {
		return events.Event{}, errors.New("event not found")
	}
	return event[0], nil
}

func (m EvidenceInsightsTool) timestampNormalize(data any) string {
	if tsFloat, ok := data.(float64); ok {
		t := time.Unix(int64(tsFloat)/1000, 0)
		return t.UTC().Format("2006-01-02 15:04:05")
	}
	if tsString, ok := data.(string); ok {
		// Try to parse as RFC3339 first
		t, err := time.Parse(time.RFC3339, tsString)
		if err == nil {
			return t.UTC().Format("2006-01-02 15:04:05")
		}
		// Try to parse as float64 string
		if tsFloat, err := strconv.ParseFloat(tsString, 64); err == nil {
			t := time.Unix(int64(tsFloat)/1000, 0)
			return t.UTC().Format("2006-01-02 15:04:05")
		}
		// Try to parse as int64 string
		if tsInt, err := strconv.ParseInt(tsString, 10, 64); err == nil {
			t := time.Unix(tsInt, 0)
			return t.UTC().Format("2006-01-02 15:04:05")
		}
	}
	return ""

}
func (m EvidenceInsightsTool) MemoryDataNormalized(timestamps []any, values []any) map[string]string {
	finalData := map[string]string{}
	normalizedMemoryData := m.valueNormalize(values)
	normalizedTimestampData := m.timestampsNormalize(timestamps)
	for i, ts := range normalizedTimestampData {
		finalData[ts] = normalizedMemoryData[i]
	}
	return finalData
}

func (m EvidenceInsightsTool) timestampsNormalize(timestamps []any) []string {
	var finalTimestamps []string
	for _, ts := range timestamps {
		if ts == nil {
			continue
		}
		if tsFloat, ok := ts.(float64); ok {
			t := time.Unix(int64(tsFloat), 0)
			finalTimestamps = append(finalTimestamps, t.UTC().Format("2006-01-02 15:04:05"))
			continue
		}
		if tsString, ok := ts.(string); ok {
			// Try to parse as RFC3339 first
			t, err := time.Parse(time.RFC3339, tsString)
			if err == nil {
				finalTimestamps = append(finalTimestamps, t.UTC().Format("2006-01-02 15:04:05"))
				continue
			}
			// Try to parse as float64 string
			if tsFloat, err := strconv.ParseFloat(tsString, 64); err == nil {
				t := time.Unix(int64(tsFloat), 0)
				finalTimestamps = append(finalTimestamps, t.UTC().Format("2006-01-02 15:04:05"))
				continue
			}
			// Try to parse as int64 string
			if tsInt, err := strconv.ParseInt(tsString, 10, 64); err == nil {
				t := time.Unix(tsInt, 0)
				finalTimestamps = append(finalTimestamps, t.UTC().Format("2006-01-02 15:04:05"))
			} else {
				log.Println("Error parsing timestamp:", err)
			}
		}
	}
	return finalTimestamps
}

func (m EvidenceInsightsTool) valueNormalize(values []any) []string {
	var finalValues []string
	for _, val := range values {
		switch v := val.(type) {
		case float64:
			finalValues = append(finalValues, fmt.Sprintf("%.2f MB", v/float64(1024*1024)))
		case int:
			finalValues = append(finalValues, fmt.Sprintf("%d MB", v/(1024*1024)))
		case string:
			if intValue, err := strconv.Atoi(v); err == nil {
				finalValues = append(finalValues, fmt.Sprintf("%d MB", intValue/(1024*1024)))
			} else {
				log.Println("Error converting string to int:", err)
			}
		default:
			log.Println("Unsupported value type value:", v)
		}
	}
	return finalValues
}
func (m EvidenceInsightsTool) normalizePodEvent(podEventIn []any) []any {
	type PodEvent struct {
		Message     string
		EventTime   string
		EventStatus string
		EventType   string
		PodName     string
	}
	var finalPodEvents []PodEvent
	for _, event := range podEventIn {
		temp, ok := event.(map[string]any)
		if !ok {
			continue
		}
		eventData := PodEvent{}

		if msg, ok := temp["message"].(string); ok {
			eventData.Message = msg
		}
		eventData.EventTime = m.timestampNormalize(temp["time"])
		if reason, ok := temp["reason"].(string); ok {
			eventData.EventStatus = reason
		}
		if evtType, ok := temp["type"].(string); ok {
			eventData.EventType = evtType
		}
		if name, ok := temp["name"].(string); ok {
			eventData.PodName = name
		}
		finalPodEvents = append(finalPodEvents, eventData)
	}
	var result []any
	for _, event := range finalPodEvents {
		result = append(result, event)
	}
	return result
}

func (m EvidenceInsightsTool) normalizeNodeEvent(nodeEventIn []any) []any {
	type PodEvent struct {
		Message     string
		EventTime   string
		EventStatus string
		EventType   string
		PodName     string
	}
	var finalPodEvents []PodEvent
	for _, event := range nodeEventIn {
		temp := event.(map[string]any)
		eventData := PodEvent{}
		eventData.Message = temp["message"].(string)
		eventData.EventTime = m.timestampNormalize(temp["time"])
		eventData.EventStatus = temp["reason"].(string)
		eventData.EventType = temp["type"].(string)
		eventData.PodName = temp["name"].(string)
		finalPodEvents = append(finalPodEvents, eventData)
	}
	var result []any
	for _, event := range finalPodEvents {
		result = append(result, event)
	}
	return result
}

func (m EvidenceInsightsTool) normalizedMemoryDataPoint(data any) string {
	switch v := data.(type) {
	case float64:
		return fmt.Sprintf("%.2f MB", v/float64(1024*1024))
	case int:
		return fmt.Sprintf("%d MB", v/(1024*1024))
	case string:
		if intValue, err := strconv.Atoi(v); err == nil {
			return fmt.Sprintf("%d MB", intValue/(1024*1024))
		} else {
			log.Println("Error converting string to int:", err)
		}
	case nil:
		return "Not specified"
	default:
		log.Println("Unsupported value type:", v)
	}
	return ""
}
func (m EvidenceInsightsTool) normalizedNoisyNieghbour(data []any) []map[string]any {
	finalData := make([]map[string]any, 0)
	for _, neighbour := range data {
		nie := neighbour.(map[string]any)
		temp := make(map[string]any)
		temp["pod_name"] = nie["pod_name"]
		temp["memory_used"] = m.normalizedMemoryDataPoint(nie["memory_used"])

		memory := m.normalizedMemoryDataPoint(nie["memory_requested"])
		if memory == "" {
			temp["memory_requested"] = "Not provided"
		} else {
			temp["memory_requested"] = memory
		}
		temp["memory_limit"] = m.normalizedMemoryDataPoint(nie["memory_limit"])

		memory = m.normalizedMemoryDataPoint(nie["memory_limit"])
		if memory == "" {
			temp["memory_limit"] = "Not provided"
		} else {
			temp["memory_limit"] = memory
		}

		temp["pod_name"] = nie["pod_name"].(string)
		temp["namespace"] = nie["namespace"].(string)
		temp["node_name"] = nie["node_name"].(string)
		if nie["kind"] == nil {
			temp["kind"] = "Not provided"
		} else {
			temp["kind"] = nie["kind"].([]any)
		}
		finalData = append(finalData, temp)
	}
	return finalData
}

func (m EvidenceInsightsTool) parseEventData(evidences map[string]any) RCAInvestigateData {
	rcaInvestigateData := RCAInvestigateData{}
	evidenceBytes, err := common.MarshalJson(evidences)
	if err != nil {
		slog.Error("Error marshaling evidences to JSON:", "error", err)
		return RCAInvestigateData{}
	}

	var evidence events.InvestigateData
	err = common.UnmarshalJson(evidenceBytes, &evidence)
	if err != nil {
		slog.Error("Error unmarshaling JSON to common.InvestigateData:", "error", err)
		return RCAInvestigateData{}
	}

	if evidence.Deployment.Data != nil {
		evidenceData := evidence.Deployment.Data.(map[string]any)
		//for now we dont wantt to diff data as its normally very large
		// keep only insights for now
		var updatedValues []map[string]any
		for _, updated := range evidenceData["updated_values"].([]any) {
			update := updated.(map[string]any)
			updatedValues = append(updatedValues, map[string]any{"new_value": update["new"], "old_value": update["old"], "path": update["path"]})
		}
		temp := make(map[string]any)
		temp["new"] = evidenceData["new"]
		temp["old"] = evidenceData["old"]
		temp["deployment_time"] = evidenceData["start_at"]
		temp["changes"] = updatedValues
		jsonDeployment, err := common.MarshalJson(temp)
		if err != nil {
			slog.Error("Error marshaling deployment data to JSON:", "error", err)
			return RCAInvestigateData{}
		}
		var deploymentData map[string]any
		if err := common.UnmarshalJson(jsonDeployment, &deploymentData); err != nil {
			slog.Error("Error unmarshaling deployment data to map[string]any:", "error", err)
			return RCAInvestigateData{}
		}
		rcaInvestigateData.LastDeploymentChange = deploymentData
		print(string(jsonDeployment))
	}
	if len(evidence.NodeMetrics) > 0 {
		allNodeMetrics := []any{}
		for _, nodeMetric := range evidence.NodeMetrics {
			if nodeMetric.Data != nil {
				if nodeMetrics, ok := nodeMetric.Data.([]any); ok {
					for _, nm := range nodeMetrics {
						if nmMap, ok := nm.(map[string]any); ok {
							if timestamps, ok := nmMap["timestamps"].([]any); ok {
								if values, ok := nmMap["values"].([]any); ok {
									MemoryDataNormalized := m.MemoryDataNormalized(timestamps, values)
									allNodeMetrics = append(allNodeMetrics, MemoryDataNormalized)
								}
							}
						}
					}
				}
			}
		}
		if len(allNodeMetrics) > 0 {
			JsonMemoryDataNormalized, err := common.MarshalJson(allNodeMetrics)
			if err != nil {
				slog.Error("Error marshaling node metrics to JSON:", "error", err)
				return RCAInvestigateData{}
			}
			rcaInvestigateData.NodeMemory = string(JsonMemoryDataNormalized)
		}
	}
	if len(evidence.PodMetrics) > 0 {
		allPodMetrics := []any{}
		for _, podMetricEntry := range evidence.PodMetrics {
			if podMetricEntry.Data != nil {
				if podMetrics, ok := podMetricEntry.Data.(map[string]any); ok {
					temp := make(map[string]any)
					limits := "Null"
					requests := "Null"
					if podMetricsData, ok := podMetrics["data"].([]any); ok && len(podMetricsData) > 0 {
						if firstMetric, ok := podMetricsData[0].(map[string]any); ok {
							if metric, ok := firstMetric["metric"].(map[string]any); ok {
								if limitsMap, ok := metric["limits"].(map[string]any); ok {
									if memoryLimit, ok := limitsMap["memory"].(float64); ok {
										limits = strconv.FormatFloat(memoryLimit, 'f', 0, 64)
									}
								}
								if requestsMap, ok := metric["requests"].(map[string]any); ok {
									if memoryRequest, ok := requestsMap["memory"].(float64); ok {
										requests = strconv.FormatFloat(memoryRequest, 'f', 0, 64)
									}
								}
							}
							if timestamps, ok := firstMetric["timestamps"].([]any); ok {
								if values, ok := firstMetric["values"].([]any); ok {
									MemoryDataNormalized := m.MemoryDataNormalized(timestamps, values)
									temp["values"] = MemoryDataNormalized
								}
							}
						}
					}
					temp["limits"] = limits
					temp["requests"] = requests
					if len(evidence.NoisyNeighbours) > 0 && evidence.NoisyNeighbours[0].Data != nil {
						if noisyNeighboursData, ok := evidence.NoisyNeighbours[0].Data.(map[string]any); ok {
							if data, ok := noisyNeighboursData["data"].(map[string]any); ok {
								temp["node_memory_used"] = m.normalizedMemoryDataPoint(data["memory_used"])
								temp["node_memory_requested"] = m.normalizedMemoryDataPoint(data["memory_requested"])
								temp["node_memory_allocatable"] = m.normalizedMemoryDataPoint(data["memory_allocatable"])
							}
						}
					}
					allPodMetrics = append(allPodMetrics, temp)
				}
			}
		}
		if len(allPodMetrics) > 0 {
			jsonPodMemory, err := common.MarshalJson(allPodMetrics)
			if err != nil {
				slog.Error("Error marshaling pod memory to JSON:", "error", err)
				return RCAInvestigateData{}
			}
			rcaInvestigateData.PodMemory = string(jsonPodMemory)
		}
	}
	if evidence.ContainerMetrics.Data != nil {
		if podMetrics, ok := evidence.ContainerMetrics.Data.(map[string]any); ok {
			temp := make(map[string]any)
			limits := "Null"
			requests := "Null"
			if podMetricsData, ok := podMetrics["data"].([]any); ok && len(podMetricsData) > 0 {
				if firstMetric, ok := podMetricsData[0].(map[string]any); ok {
					if metric, ok := firstMetric["metric"].(map[string]any); ok {
						if limitsMap, ok := metric["limits"].(map[string]any); ok {
							if memoryLimit, ok := limitsMap["memory"].(float64); ok {
								limits = strconv.FormatFloat(memoryLimit, 'f', 0, 64)
							}
						}
						if requestsMap, ok := metric["requests"].(map[string]any); ok {
							if memoryRequest, ok := requestsMap["memory"].(float64); ok {
								requests = strconv.FormatFloat(memoryRequest, 'f', 0, 64)
							}
						}
					}
					if timestamps, ok := firstMetric["timestamps"].([]any); ok {
						if values, ok := firstMetric["values"].([]any); ok {
							temp["values"] = m.MemoryDataNormalized(timestamps, values)
						}
					}
				}
			}
			temp["limits"] = limits
			temp["requests"] = requests
			if len(evidence.NoisyNeighbours) > 0 && evidence.NoisyNeighbours[0].Data != nil {
				if noisyNeighboursData, ok := evidence.NoisyNeighbours[0].Data.(map[string]any); ok {
					if data, ok := noisyNeighboursData["data"].(map[string]any); ok {
						temp["node_memory_used"] = m.normalizedMemoryDataPoint(data["memory_used"])
						temp["node_memory_requested"] = m.normalizedMemoryDataPoint(data["memory_requested"])
						temp["node_memory_allocatable"] = m.normalizedMemoryDataPoint(data["memory_allocatable"])
					}
				}
			}
			jsonPodMemory, err := common.MarshalJson(temp)
			if err != nil {
				slog.Error("Error marshaling pod memory to JSON:", "error", err)
				return RCAInvestigateData{}
			}
			rcaInvestigateData.PodMemory = string(jsonPodMemory)
		} else {
			slog.Error("Error: ContainerMetrics is not of type map[string]any")
		}
	}
	if evidence.PodData.Data != nil {
		rcaInvestigateData.PodConfiguration = evidence.PodData
	}
	if evidence.LogData != "" {
		rcaInvestigateData.LogData = evidence.LogData
	}
	if len(evidence.ErrorLogData) > 0 {
		rcaInvestigateData.ErrorLogData = evidence.ErrorLogData
	}
	if len(evidence.NoisyNeighbours) > 0 {
		allNoisyNeighbours := []any{}
		for _, noisyNeighbourEntry := range evidence.NoisyNeighbours {
			if noisyNeighbourEntry.Data != nil {
				if noisyNeighboursData, ok := noisyNeighbourEntry.Data.(map[string]any); ok {
					if data, ok := noisyNeighboursData["data"].(map[string]any); ok {
						temp := make(map[string]any)
						temp["node_memory_used"] = m.normalizedMemoryDataPoint(data["memory_used"])
						temp["node_memory_requested"] = m.normalizedMemoryDataPoint(data["memory_requested"])
						temp["node_memory_allocatable"] = m.normalizedMemoryDataPoint(data["memory_allocatable"])
						if neighbours, ok := data["neighbours"].([]any); ok {
							temp["neighbours"] = m.normalizedNoisyNieghbour(neighbours)
						}
						temp["node_name"] = data["node_name"]
						temp["total_pods"] = data["total_pods"]
						allNoisyNeighbours = append(allNoisyNeighbours, temp)
					}
				}
			}
		}
		if len(allNoisyNeighbours) > 0 {
			jsn, _ := common.MarshalJson(allNoisyNeighbours)
			rcaInvestigateData.NoisyNeighbours = string(jsn)
		}
	}
	if len(evidence.PodEvents) > 0 {
		allPodEvents := []any{}
		for _, podEvent := range evidence.PodEvents {
			if podEvent.Data != nil {
				allPodEvents = append(allPodEvents, podEvent.Data.([]any)...)
			}
		}
		if len(allPodEvents) > 0 {
			rcaInvestigateData.PodEvents = m.normalizePodEvent(allPodEvents)
		}
	}
	if len(evidence.NodeEvents) > 0 {
		allNodeEvents := []any{}
		for _, nodeEvent := range evidence.NodeEvents {
			if nodeEvent.Data != nil {
				allNodeEvents = append(allNodeEvents, nodeEvent.Data.([]any)...)
			}
		}
		if len(allNodeEvents) > 0 {
			rcaInvestigateData.NodeEvents = m.normalizeNodeEvent(allNodeEvents)
		}
	}
	return rcaInvestigateData
}

func (m EvidenceInsightsTool) Call(nbRequestContext toolcore.NbToolContext, input toolcore.NBToolCallRequest) (toolcore.NBToolResponse, error) {
	var commandData map[string]any
	if err := common.UnmarshalJson([]byte(input.Command), &commandData); err != nil {
		return toolcore.NBToolResponse{}, fmt.Errorf("failed to parse input.command: %w", err)
	}
	eventID, ok := commandData["event_id"]
	if !ok {
		return toolcore.NBToolResponse{}, fmt.Errorf("event_id not found in input.command")
	}
	timeseriesOnly := false
	timeseriesOnly, ok = commandData["timeseries_only"].(bool)
	if !ok {
		timeseriesOnly = false
	}
	event_data, err := m.getEventData(nbRequestContext, eventID.(string))
	if err != nil {
		return toolcore.NBToolResponse{}, err
	}
	eventContext := fmt.Sprintf("Event: %s\nDescription: %s", event_data.Title, event_data.Description)
	EvidenceData := event_data.Evidences
	evidenceMap := make(map[string]any)
	marshaledEvidenceData, err := common.MarshalJson(EvidenceData)
	if err != nil {
		return toolcore.NBToolResponse{}, err
	}

	if err := common.UnmarshalJson([]byte(marshaledEvidenceData), &evidenceMap); err != nil {
		return toolcore.NBToolResponse{}, err
	}

	evidenceTimeseriesAnalysis := "Evidence Timeseries analysis for the event:\n"
	evidenceConfigurationAnalysis := "Evidence Configuration analysis for the event:\n"
	evidenceAnomalyAnalysis := "Evidence anomaly analysis for the event:\n"
	completeSummary := "Evidence summary for the event:\n"
	hasTimeseries := false
	parsedEvidence := m.parseEventData(evidenceMap)

	eventInvestigateDataMap := make(map[string]any)
	eventInvestigateDataBytes, err := common.MarshalJson(parsedEvidence)
	if err != nil {
		slog.Error("Error marshaling RCAInvestigateData to JSON:", "error", err)
		return toolcore.NBToolResponse{}, err
	}
	if err := common.UnmarshalJson(eventInvestigateDataBytes, &eventInvestigateDataMap); err != nil {
		slog.Error("Error unmarshaling JSON to map[string]any:", "error", err)
		return toolcore.NBToolResponse{}, err
	}
	for evidenceName, evidenceContent := range eventInvestigateDataMap {
		evidenceMeta, exists := evidenceDataMeta[evidenceName]
		if timeseriesOnly && evidenceMeta.EvidenceType != "Timeseries" {
			nbRequestContext.Ctx.GetLogger().Info("Skipping non-timeseries evidence", "evidenceName", evidenceName)
			continue
		}
		if !exists {
			nbRequestContext.Ctx.GetLogger().Warn("Unknown evidence type for analysis", "evidenceName", evidenceName)
			continue
		}

		if evidenceContent == nil || evidenceContent == "" {
			nbRequestContext.Ctx.GetLogger().Info("Skipping empty evidence", "evidenceName", evidenceName)
			continue
		}

		finalMessage, formatErr := prompts.NewPromptTemplate(evidenceMeta.Prompt, []string{"context", "event_context"}).Format(map[string]any{"context": evidenceContent, "event_context": eventContext})
		if formatErr != nil {
			return toolcore.NBToolResponse{}, formatErr
		}

		mc := []llms.MessageContent{llms.TextParts(llms.ChatMessageTypeHuman, finalMessage)}
		completion, err := core.GenerateAndTrackLLMContent(nbRequestContext.Ctx, nbRequestContext.UserId, nbRequestContext.AccountId, nbRequestContext.ConversationId, nbRequestContext.MessageId, AgentEvidenceAnalysis, false, mc, false, llms.WithTemperature(0.0))
		if err != nil {
			nbRequestContext.Ctx.GetLogger().Error("llm: unable to generate content", "error", err)
			return toolcore.NBToolResponse{}, core.ErrLlmUnableToGenerate(err)
		}
		content := ""
		if len(completion.Choices) > 0 {
			content = completion.Choices[0].Content
		}
		completeSummary += fmt.Sprintf("%s\n----------------------\n%s\n----------------------\n", evidenceName, content)
		print(completeSummary)

		switch evidenceMeta.EvidenceType {
		case "Configuration":
			evidenceConfigurationAnalysis += fmt.Sprintf("%s\n----------------------\n", content)
		case "Anomalies":
			evidenceAnomalyAnalysis += fmt.Sprintf("%s\n----------------------\n", content)
		default:
			hasTimeseries = true
			evidenceTimeseriesAnalysis += fmt.Sprintf("%s\n----------------------\n", content)
		}
	}
	sortedAnalysis := evidenceTimeseriesAnalysis
	if hasTimeseries && timeseriesOnly {
		// only apply sorting if we have timeseries data
		sortedInput := fmt.Sprintf("\n %s \n **  important ** \n I need sorted timeseries data in json format \n", evidenceTimeseriesAnalysis)
		sortedAnalysis, err = m.SortTimestamp(nbRequestContext, sortedInput, event_data.Title, event_data.StartsAt)
	}
	if timeseriesOnly {
		return toolcore.NBToolResponse{
			Data: sortedAnalysis,
			Type: toolcore.NBToolResponseTypeText,
		}, nil
	}
	if err != nil {
		return toolcore.NBToolResponse{}, err
	}
	finalOutput := fmt.Sprintf("Event happened at %s with description \n%s . \n\n%s \n\n %s \n\n %s ", common.FormatPresentationTime(event_data.StartsAt), event_data.Title, evidenceConfigurationAnalysis, sortedAnalysis, evidenceAnomalyAnalysis)
	return toolcore.NBToolResponse{
		Data: finalOutput,
		Type: toolcore.NBToolResponseTypeText,
	}, nil
}
