package tools

import (
	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/tmc/langchaingo/llms"
)

func TestGithubCliToolCommentFormatting(t *testing.T) {

	tool := GithubCliTool{}
	sc := security.NewRequestContextForSuperAdmin()

	testCases :=
		[]struct {
			SessionId  string
			Query      string
			AccountId  string
			UserId     string
			ToolConfig string
		}{
			{
				SessionId:  "ut-tool-chain-1",
				AccountId:  os.Getenv("TEST_ACCOUNT"),
				UserId:     os.Getenv("TEST_USER"),
				Query:      `gh issue comment 15642 --repo nudgebee/nudgebee --body "## Shell Script Solution for Removing Pods Older Than 8 Hours in action-runner-1 Namespace\n\nBased on the issue description, the goal is to create a script that removes pods older than 8 hours in the action-runner-1 namespace. Here's a shell script solution:\n\nbash\n#!/bin/bash\n\n# Set the namespace\nNAMESPACE=\"action-runner-1\"\n\n# Set the age threshold in seconds (8 hours * 60 minutes * 60 seconds)\nAGE_THRESHOLD=$((8 * 60 * 60))\n\n# Get the current timestamp in seconds\nCURRENT_TIME=$(date +%s)\n\n# Get the list of pods in the specified namespace and filter by age\nPODS=$(kubectl get pods -n \"$NAMESPACE\" -o jsonpath='{range .items[*]}{.metadata.name}{\" \"}{.status.startTime}{\"\\n\"}{end}' | while read -r pod_name start_time; do\n if [[ -n \"$pod_name\" && -n \"$start_time\" ]]; then\n start_time_seconds=$(date -d \"$start_time\" +%s)\n pod_age=$((CURRENT_TIME - start_time_seconds))\n\n if [[ \"$pod_age\" -gt \"$AGE_THRESHOLD\" ]]; then\n echo \"$pod_name\"\n fi\n fi\ndone)\n\n# Delete the pods that are older than the threshold\nif [[ -n \"$PODS\" ]]; then\n echo \"Deleting pods older than 8 hours in namespace $NAMESPACE:\"\n echo \"$PODS\"\n kubectl delete pod -n \"$NAMESPACE\" $PODS\nelse\n echo \"No pods found older than 8 hours in namespace $NAMESPACE.\"\nfi\n\n\n\nExplanation:\n\n1. #!/bin/bash: Shebang line, specifies the script interpreter.\n2. NAMESPACE=\"action-runner-1\": Defines the Kubernetes namespace to target.\n3. AGE_THRESHOLD=$((8 * 60 * 60)): Sets the age threshold in seconds (8 hours).\n4. CURRENT_TIME=$(date +%s): Gets the current timestamp in seconds.\n5. kubectl get pods ...: This is the core of the script:\n * kubectl get pods -n \"$NAMESPACE\": Retrieves all pods in the specified namespace.\n * -o jsonpath='{range .items[*]}{.metadata.name}{\" \"}{.status.startTime}{\"\\n\"}{end}': Uses jsonpath to extract the pod name and start time.\n * The while read -r pod_name start_time loop iterates through each pod.\n * start_time_seconds=$(date -d \"$start_time\" +%s): Converts the pod's start time to seconds.\n * pod_age=$((CURRENT_TIME - start_time_seconds)): Calculates the pod's age in seconds.\n * if [[ \"$pod_age\" -gt \"$AGE_THRESHOLD\" ]]: Checks if the pod's age is greater than the threshold.\n * echo \"$pod_name\": If the pod is older than the threshold, its name is echoed.\n6. if [[ -n \"$PODS\" ]]: Checks if the $PODS variable is not empty (i.e., if any pods were found that are older than the threshold).\n7. kubectl delete pod -n \"$NAMESPACE\" $PODS: Deletes the pods that are older than the threshold.\n8. echo \"No pods found older than 8 hours in namespace $NAMESPACE.\": If no pods are older than the threshold, a message is printed.\n\nHow to Use:\n\n1. Save the script to a file (e.g., delete_old_pods.sh).\n2. Make the script executable: chmod +x delete_old_pods.sh.\n3. Run the script: ./delete_old_pods.sh.\n\nImportant Considerations:\n\n* Dry Run: Before running this script in a production environment, it's crucial to perform a dry run. You can modify the kubectl delete command to output the pods that would be deleted without actually deleting them. For example, replace kubectl delete pod -n \"$NAMESPACE\" $PODS with kubectl get pod -n \"$NAMESPACE\" $PODS -o yaml. Review the output carefully.\n* RBAC Permissions: Ensure that the user or service account running this script has the necessary RBAC permissions to delete pods in the action-runner-1 namespace.\n* Scheduling: To automate this process, you can schedule the script to run periodically using cron. For example, to run the script every hour, you could add the following line to your crontab:\n\n \n 0 * * * * /path/to/delete_old_pods.sh\n \n\n* Error Handling: Add error handling to the script to catch potential issues, such as network connectivity problems or insufficient permissions. Consider logging errors to a file for later analysis.\n* Alternatives: Consider using Kubernetes' built-in features like TTL (Time To Live) for finished Jobs or other resource management techniques if they are applicable to your use case. These might be more robust and easier to manage in the long run.\n* Pod Disruption Budgets (PDBs): If your application requires a certain number of pods to be available at all times, consider using PDBs to prevent the script from deleting too many pods simultaneously.\n* Testing: Thoroughly test this script in a non-production environment before deploying it to production.\n* Logging: Implement proper logging to track which pods were deleted and when. This can be helpful for auditing and troubleshooting.\n* Security: Store the script in a secure location and restrict access to authorized personnel only. Avoid hardcoding sensitive information (like credentials) in the script.\n\nThis script provides a basic solution for removing old pods. You may need to adjust it based on your specific requirements and environment. Remember to test thoroughly and implement proper error handling and logging."`,
				ToolConfig: "mayankpande88",
			},
		}
	for _, tc := range testCases {
		ntc := core.NewNbToolContext(sc, tool, tc.AccountId, tc.UserId, uuid.NewString(), uuid.NewString(), uuid.NewString(), tc.Query, []llms.MessageContent{}, "", core.NBQueryConfig{ToolConfigs: map[string]string{tool.Name(): tc.ToolConfig}}, "")
		resp, err := tool.Call(ntc, core.NBToolCallRequest{
			Command: tc.Query,
		})
		assert.Nil(t, err)
		assert.NotNil(t, resp)
	}
}
