package aws

import (
	"nudgebee/llm/agents/core"
	"nudgebee/llm/agents/prompts_repo"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"
	"strings"
)

func init() {
	toolDescription := `Investigate/Troubleshoot AWS Issues or Get information about AWS resources using the AWS CLI. This tool is "smart" and handles its own resource discovery. Use this agent directly to interact with AWS services (EC2, RDS, S3, Lambda, Cost Explorer) without needing separate reconnaissance. Returns AWS CLI output and summaries.`
	toolInput := "Natural Language query about AWS resources, operations or issues."
	toolOutput := "AWS CLI output or natural language response based on the query."
	core.RegisterNBAgentFactoryAndTool(AwsAgentName, func(accountId string) (core.NBAgent, error) {
		return newAwsAgent(accountId), nil
	}, toolDescription, toolInput, toolOutput)
}

func newAwsAgent(accountId string) core.NBAgent {
	return AwsAgent{
		accountId: accountId,
	}
}

type AwsAgent struct {
	accountId string
}

func (a AwsAgent) GetName() string {
	return AwsAgentName
}

func (a AwsAgent) GetNameAliases() []string {
	return []string{"AWS"}
}

func (a AwsAgent) GetDescription() string {
	return `Investigate/Troubleshoot AWS Issues or Get information about AWS resources using the AWS CLI. This tool is "smart" and handles its own resource discovery. Use this agent directly to interact with AWS services (EC2, RDS, S3, Lambda, Cost Explorer) without needing separate reconnaissance. Returns AWS CLI output and summaries.`
}

func (a AwsAgent) GetSupportedTools(ctx *security.RequestContext) []toolcore.NBTool {
	tools := []toolcore.NBTool{tools.AwsCliTool{}}
	return tools
}

func (a AwsAgent) GetSystemPrompt(ctx *security.RequestContext, query core.NBAgentRequest) core.NBAgentPrompt {

	// Load prompt from prompts repository
	promptText := prompts_repo.GetPrompt(prompts_repo.PromptAgentAws)
	instructions := strings.Split(promptText, "\n")
	constraints := []string{
		"Syntax: --group-by = Type=DIMENSION,Key=<KEY>; --filters = Name=<filter>,Values=<value>; use plurals (--filters, --group-ids)",
		"Time formats: Cost Explorer = YYYY-MM-DD, CloudTrail = ISO 8601, CloudWatch Logs CLI → Unix epoch seconds (UTC only)",
		"BEFORE proposing SG/NACL changes: check EC2 UserData first - config issues look like network issues but are NOT",
		"CRITICAL: NEVER invent resource IDs, ARNs, IPs - empty CLI results = 'not found'",
		"Evidence-based: run command → parse output → make statement (never assume)",
	}

	if config.Config.LlmServerShellToolEnabled {
		newInstructions := []string{}
		for _, inst := range instructions {
			if !strings.Contains(inst, "AWS CLI ONLY") {
				newInstructions = append(newInstructions, inst)
			}
		}
		newInstructions = append(newInstructions, core.GetWorkspaceInstructions()...)
		instructions = newInstructions
	}

	toolUsage := map[string][]string{
		tools.ToolExecuteAwsCliCommand: {
			"You can use **aws_execute** to execute AWS CLI commands.",
			"Use this tool to interact with AWS, always prefer this tool over other tools",
		},
	}

	if config.Config.LlmServerShellToolEnabled {
		toolUsage[tools.ToolExecuteAwsCliCommand] = []string{
			"You can use **aws_execute** to execute AWS CLI commands.",
			"Use this tool to interact with AWS, always prefer this tool over other tools",
			"You can use standard shell features like pipes, redirects, and substitutions.",
		}
	}

	examples := []core.NBAgentPromptExample{
		{
			Question: "Get cost breakdown by service and region for last month",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolExecuteAwsCliCommand,
					Input: "aws ce get-cost-and-usage --time-period Start=2025-12-01,End=2026-01-01 --granularity MONTHLY --metrics BlendedCost --group-by Type=DIMENSION,Key=SERVICE Type=DIMENSION,Key=REGION",
				},
			},
			Explanation: "CRITICAL: --group-by syntax is 'Type=DIMENSION,Key=<KEY>'. WRONG: 'Type=SERVICE Type=REGION' (missing DIMENSION). WRONG: '--group-by Type=SERVICE,Type=REGION' (comma between groups). RIGHT: Space-separated groups, each with full 'Type=DIMENSION,Key=' prefix.",
		},
		{
			Question: "Get RDS instance endpoint URL",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolExecuteAwsCliCommand,
					Input: "aws rds describe-db-instances --db-instance-identifier my-database --query 'DBInstances[0].Endpoint.Address' --output text --region us-east-1",
				},
			},
			Explanation: "Use --query for JMESPath extraction, --output text for clean output. JMESPath: 'DBInstances[0].field.subfield'.",
		},
		{
			Question: "List Lambda functions and filter by tag Environment=production",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolExecuteAwsCliCommand,
					Input: "aws lambda list-functions --region us-east-1",
				},
				{
					Tool:  tools.ToolExecuteAwsCliCommand,
					Input: "aws lambda list-tags --resource arn:aws:lambda:us-east-1:123456789012:function:my-function --region us-east-1",
				},
			},
			Explanation: "Lambda list-functions doesn't support tag filtering directly. Use two-step: list functions, then check tags for each.",
		},
		{
			Question: "Find EC2 instances in a specific VPC",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolExecuteAwsCliCommand,
					Input: "aws ec2 describe-instances --filters Name=vpc-id,Values=vpc-12345678 --region us-east-1",
				},
			},
			Explanation: "Use --filters (plural) with Name=<filter>,Values=<value> syntax. WRONG: '--filter Name=vpc-id,Values=...' (singular). WRONG: 'vpc-id=vpc-12345678' (missing Name=/Values= structure).",
		},
		{
			Question: "NAT Gateway is failing, find affected resources",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolExecuteAwsCliCommand,
					Input: "aws ec2 describe-nat-gateways --nat-gateway-ids nat-0123456789abcdef0 --region us-east-1",
				},
				{
					Tool:  tools.ToolExecuteAwsCliCommand,
					Input: "aws ec2 describe-route-tables --filters Name=route.nat-gateway-id,Values=nat-0123456789abcdef0 --region us-east-1",
				},
				{
					Tool:  tools.ToolExecuteAwsCliCommand,
					Input: "aws ec2 describe-instances --filters Name=subnet-id,Values=subnet-xxx --region us-east-1",
				},
			},
			Explanation: "Blast radius assessment: 1) Check NAT Gateway status, 2) Find route tables using this NAT (shows affected subnets), 3) Find resources in those subnets. All private subnet traffic through this NAT is affected.",
		},
		{
			Question: "Lambda function is timing out when accessing external API",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolExecuteAwsCliCommand,
					Input: "aws lambda get-function-configuration --function-name my-function --region us-east-1",
				},
				{
					Tool:  tools.ToolExecuteAwsCliCommand,
					Input: "aws ec2 describe-nat-gateways --filter Name=vpc-id,Values=vpc-xxx --region us-east-1",
				},
				{
					Tool:  tools.ToolExecuteAwsCliCommand,
					Input: "aws ec2 describe-security-groups --group-ids sg-xxx --region us-east-1",
				},
			},
			Explanation: "VPC Lambda timeout investigation: 1) Get Lambda VPC config (VpcConfig.SubnetIds, SecurityGroupIds), 2) If VPC-enabled, check NAT Gateway exists and is 'available', 3) Check Security Group allows outbound traffic (egress rules). Lambda in VPC MUST have NAT for external access.",
		},
		{
			Question: "Find all resources using a specific Security Group",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolExecuteAwsCliCommand,
					Input: "aws ec2 describe-network-interfaces --filters Name=group-id,Values=sg-12345678 --region us-east-1",
				},
			},
			Explanation: "Blast radius for Security Group changes. describe-network-interfaces with group-id filter shows ALL resources (EC2, Lambda, RDS, ELB, etc) using this SG via their ENIs. Essential for understanding impact before making SG changes.",
		},
		{
			Question: "RDS instance is unreachable",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolExecuteAwsCliCommand,
					Input: "aws rds describe-db-instances --db-instance-identifier my-database --region us-east-1",
				},
				{
					Tool:  tools.ToolExecuteAwsCliCommand,
					Input: "aws ec2 describe-security-groups --group-ids sg-xxx --region us-east-1",
				},
				{
					Tool:  tools.ToolExecuteAwsCliCommand,
					Input: "aws rds describe-events --source-identifier my-database --source-type db-instance --duration 60 --region us-east-1",
				},
			},
			Explanation: "RDS investigation pattern: 1) Check instance status (DBInstanceStatus), VPC, Security Groups, 2) Check SG allows inbound on DB port (3306/5432) from app CIDR/SG, 3) Check recent RDS events for modifications/reboots.",
		},
		{
			Question: "Get top SQL queries from RDS Performance Insights for instance 'main'",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolExecuteAwsCliCommand,
					Input: "aws rds describe-db-instances --db-instance-identifier main --query 'DBInstances[0].DbiResourceId' --output text --region us-east-1",
				},
				{
					Tool:  tools.ToolExecuteAwsCliCommand,
					Input: "aws pi describe-dimension-keys --service-type RDS --identifier db-XXXXX --metric db.load.avg --start-time 2026-01-22T07:00:00Z --end-time 2026-01-22T08:00:00Z --group-by '{\"Group\":\"db.sql\"}' --region us-east-1",
				},
			},
			Explanation: "Performance Insights requires DbiResourceId, NOT instance name. Step 1: Get resource ID from describe-db-instances. Step 2: Use that ID (db-XXXXX from step 1) with pi describe-dimension-keys. CRITICAL: Replace 'db-XXXXX' placeholder with actual resource ID from step 1 output.",
		},
		{
			Question: "Check IAM permissions for a Lambda execution role",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolExecuteAwsCliCommand,
					Input: "aws iam get-role --role-name my-lambda-role",
				},
				{
					Tool:  tools.ToolExecuteAwsCliCommand,
					Input: "aws iam list-attached-role-policies --role-name my-lambda-role",
				},
				{
					Tool:  tools.ToolExecuteAwsCliCommand,
					Input: "aws iam list-role-policies --role-name my-lambda-role",
				},
			},
			Explanation: "For 403 errors, check IAM: 1) get-role shows trust policy (who can assume), 2) list-attached-role-policies shows managed policies, 3) list-role-policies shows inline policies. Check all three for complete permission picture.",
		},
		{
			Question: "Find CloudWatch log groups for an application",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolExecuteAwsCliCommand,
					Input: "aws logs describe-log-groups --region us-east-1",
				},
				{
					Tool:  tools.ToolExecuteAwsCliCommand,
					Input: "aws logs describe-log-groups --log-group-name-prefix /demo/ --region us-east-1",
				},
			},
			Explanation: "CRITICAL: Discover log groups BEFORE querying logs. Step 1: List all log groups. Step 2: Filter by prefix (e.g., /demo/, /aws/lambda/, /ecs/). Common patterns: /aws/lambda/<fn>, /ecs/<cluster>, /demo/<app>. Report discovered log groups for log analysis.",
		},
	}

	promptTemplate := core.NBAgentPrompt{
		Role:         "an expert AWS administrator and SRE",
		Instructions: instructions,
		Constraints:  constraints,
		ToolUsage:    toolUsage,
		Examples:     examples,
	}

	return promptTemplate
}

func (a AwsAgent) GetPlannerType() core.AgentPlannerType {
	return core.AgentPlannerTypeReAct
}
