package tools

import (
	"nudgebee/llm/tools/core"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestInferAwsVerbType(t *testing.T) {
	tests := []struct {
		name     string
		command  string
		expected core.ToolRequestType
	}{
		// Help flags — always read
		{"help flag on service", "aws ec2 --help", core.ToolRequestTypeRead},
		{"short help flag", "aws s3 -h", core.ToolRequestTypeRead},
		{"version flag", "aws --version", core.ToolRequestTypeRead},
		{"help on aws itself", "aws --help", core.ToolRequestTypeRead},
		{"help after verb", "aws ec2 describe-instances --help", core.ToolRequestTypeRead},

		// Read operations
		{"describe instances", "aws ec2 describe-instances --instance-ids i-1234567890abcdef0", core.ToolRequestTypeRead},
		{"list functions", "aws lambda list-functions", core.ToolRequestTypeRead},
		{"get user", "aws iam get-user --user-name Bob", core.ToolRequestTypeRead},
		{"s3 ls", "aws s3 ls s3://my-bucket", core.ToolRequestTypeRead},
		{"wait", "aws ec2 wait instance-running --instance-ids i-123", core.ToolRequestTypeRead},
		{"batch-get", "aws dynamodb batch-get-item --request-items file://items.json", core.ToolRequestTypeRead},
		{"scan", "aws dynamodb scan --table-name MyTable", core.ToolRequestTypeRead},
		{"help", "aws ec2 help", core.ToolRequestTypeRead},
		{"cloudwatch start-query (read, not state change)", "aws logs start-query --log-group-name /aws/lambda/my-func --start-time 1775722800 --end-time 1775725200 --query-string \"fields @timestamp, @message\"", core.ToolRequestTypeRead},
		{"athena start-query-execution (read)", "aws athena start-query-execution --query-string \"SELECT * FROM my_table\" --result-configuration OutputLocation=s3://bucket/", core.ToolRequestTypeRead},
		{"cloudwatch logs start-live-tail (read)", "aws logs start-live-tail --log-group-identifiers arn:aws:logs:us-east-1:123456789012:log-group:my-logs", core.ToolRequestTypeRead},

		// Create operations
		{"create instance", "aws ec2 run-instances --image-id ami-12345 --count 1", core.ToolRequestTypeCreate},
		{"launch template", "aws ec2 create-launch-template --launch-template-name MyTemplate", core.ToolRequestTypeCreate},
		{"put item", "aws dynamodb put-item --table-name MyTable --item file://item.json", core.ToolRequestTypeCreate},
		{"s3 cp", "aws s3 cp file.txt s3://bucket/file.txt", core.ToolRequestTypeCreate},
		{"s3 sync", "aws s3 sync . s3://bucket/", core.ToolRequestTypeCreate},

		// Update operations
		{"modify instance", "aws ec2 modify-instance-attribute --instance-id i-123 --no-source-dest-check", core.ToolRequestTypeUpdate},
		{"tag resource", "aws ec2 create-tags --resources i-123 --tags Key=Name,Value=MyVM", core.ToolRequestTypeUpdate},
		{"enable logging", "aws s3api put-bucket-logging --bucket mybucket", core.ToolRequestTypeUpdate},
		{"stop instances (state change, not delete)", "aws ec2 stop-instances --instance-ids i-1234567890abcdef0", core.ToolRequestTypeUpdate},
		{"start instances (state change)", "aws ec2 start-instances --instance-ids i-1234567890abcdef0", core.ToolRequestTypeUpdate},

		// Delete operations
		{"terminate instances", "aws ec2 terminate-instances --instance-ids i-1234567890abcdef0", core.ToolRequestTypeDelete},
		{"delete bucket", "aws s3api delete-bucket --bucket my-bucket", core.ToolRequestTypeDelete},
		{"s3 rm", "aws s3 rm s3://bucket/file.txt", core.ToolRequestTypeDelete},
		{"s3 rb", "aws s3 rb s3://bucket --force", core.ToolRequestTypeDelete},
		{"deregister image", "aws ec2 deregister-image --image-id ami-12345", core.ToolRequestTypeDelete},
		{"revoke security group", "aws ec2 revoke-security-group-ingress --group-id sg-123", core.ToolRequestTypeDelete},

		// Positional args — verb should still be detected correctly
		{"s3 cp with positional args", "aws s3 cp source.txt s3://bucket/dest.txt", core.ToolRequestTypeCreate},
		{"s3 mv with positional args", "aws s3 mv s3://bucket/old.txt s3://bucket/new.txt", core.ToolRequestTypeCreate},

		// Positional args with verb-like names must NOT cause false positives
		{"s3 cp with verb-like filename", "aws s3 cp delete-report.csv s3://bucket/", core.ToolRequestTypeCreate},
		{"s3 cp with remove-prefix filename", "aws s3 cp remove-old-data.tar s3://bucket/", core.ToolRequestTypeCreate},
		{"s3 sync with verb-like dir name", "aws s3 sync update-scripts/ s3://bucket/", core.ToolRequestTypeCreate},

		// Edge cases
		{"empty command", "", ""},
		{"only aws", "aws", ""},
		{"service name only, no recognized verb", "aws ec2", ""},
		{"unknown verb falls through", "aws ec2 frobnicate --foo bar", ""},
		{"without aws prefix", "ec2 describe-instances", core.ToolRequestTypeRead},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := inferAwsVerbType(tt.command)
			assert.Equal(t, tt.expected, result, "command: %s", tt.command)
		})
	}
}

func TestInferAzureVerbType(t *testing.T) {
	tests := []struct {
		name     string
		command  string
		expected core.ToolRequestType
	}{
		// Help flags — always read
		{"help flag on service", "az vm --help", core.ToolRequestTypeRead},
		{"short help flag", "az storage -h", core.ToolRequestTypeRead},
		{"version flag", "az --version", core.ToolRequestTypeRead},
		{"help on az itself", "az --help", core.ToolRequestTypeRead},
		{"help after verb", "az vm list --help", core.ToolRequestTypeRead},

		// Read operations
		{"list vms", "az vm list", core.ToolRequestTypeRead},
		{"show vm", "az vm show --resource-group myRG --name myVM", core.ToolRequestTypeRead},
		{"list with resource group", "az vm list --resource-group myRG", core.ToolRequestTypeRead},
		{"get aks credentials", "az aks get-credentials --resource-group myRG --name myCluster", core.ToolRequestTypeRead},
		{"help", "az vm help", core.ToolRequestTypeRead},

		// Create operations
		{"create vm", "az vm create --resource-group myRG --name myVM --image Ubuntu2204", core.ToolRequestTypeCreate},
		{"set extension (update, not create)", "az vm extension set --resource-group myRG --vm-name myVM", core.ToolRequestTypeUpdate},
		{"import db", "az sql db import --resource-group myRG", core.ToolRequestTypeCreate},

		// Update operations — state changes
		{"stop vm (state change, not delete)", "az vm stop --resource-group myRG --name myVM", core.ToolRequestTypeUpdate},
		{"start vm (state change, not create)", "az vm start --resource-group myRG --name myVM", core.ToolRequestTypeUpdate},
		{"deallocate vm (state change, not delete)", "az vm deallocate --resource-group myRG --name myVM", core.ToolRequestTypeUpdate},
		{"restart vm", "az vm restart --resource-group myRG --name myVM", core.ToolRequestTypeUpdate},
		{"update vm", "az vm update --resource-group myRG --name myVM --set tags.env=prod", core.ToolRequestTypeUpdate},
		{"resize vm", "az vm resize --resource-group myRG --name myVM --size Standard_DS3_v2", core.ToolRequestTypeUpdate},
		{"redeploy vm", "az vm redeploy --resource-group myRG --name myVM", core.ToolRequestTypeUpdate},

		// Delete operations
		{"delete vm", "az vm delete --resource-group myRG --name myVM", core.ToolRequestTypeDelete},
		{"remove resource", "az resource delete --ids /subscriptions/.../myResource", core.ToolRequestTypeDelete},
		{"purge keyvault", "az keyvault purge --name myVault", core.ToolRequestTypeDelete},

		// Positional args — verb should still be detected correctly
		{"stop with positional vm name", "az vm stop myVM --resource-group myRG", core.ToolRequestTypeUpdate},
		{"create with positional name", "az group create myGroup --location eastus", core.ToolRequestTypeCreate},

		// Positional args with verb-like names must NOT cause false positives
		{"create vm with verb-like name", "az vm create remove-old-backup --resource-group myRG", core.ToolRequestTypeCreate},
		{"stop vm with verb-like name", "az vm stop delete-queue-worker --resource-group myRG", core.ToolRequestTypeUpdate},

		// Edge cases
		{"empty command", "", ""},
		{"only az", "az", ""},
		{"unknown verb falls through", "az vm frobnicate --name myVM", ""},
		{"without az prefix", "vm list", core.ToolRequestTypeRead},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := inferAzureVerbType(tt.command)
			assert.Equal(t, tt.expected, result, "command: %s", tt.command)
		})
	}
}

func TestInferGcpVerbType(t *testing.T) {
	tests := []struct {
		name     string
		command  string
		expected core.ToolRequestType
	}{
		// Help flags — always read
		{"help flag on service", "gcloud monitoring --help", core.ToolRequestTypeRead},
		{"short help flag", "gcloud compute instances -h", core.ToolRequestTypeRead},
		{"version flag", "gcloud --version", core.ToolRequestTypeRead},
		{"help on gcloud itself", "gcloud --help", core.ToolRequestTypeRead},
		{"help after verb", "gcloud compute instances list --help", core.ToolRequestTypeRead},

		// Read operations
		{"list instances", "gcloud compute instances list --project my-project", core.ToolRequestTypeRead},
		{"describe instance", "gcloud compute instances describe my-instance --zone us-central1-a", core.ToolRequestTypeRead},
		{"get iam policy", "gcloud projects get-iam-policy my-project", core.ToolRequestTypeRead},
		{"search resources", "gcloud asset search-all-resources --scope projects/my-project", core.ToolRequestTypeRead},

		// Create operations
		{"create instance", "gcloud compute instances create my-instance --zone us-central1-a", core.ToolRequestTypeCreate},
		{"deploy function", "gcloud functions deploy my-func --runtime nodejs18", core.ToolRequestTypeCreate},
		{"run job", "gcloud run jobs execute my-job", core.ToolRequestTypeCreate},

		// Update operations — state changes and modifications
		{"stop instance (state change, not delete)", "gcloud compute instances stop my-instance --zone us-central1-a", core.ToolRequestTypeUpdate},
		{"start instance (state change)", "gcloud compute instances start my-instance --zone us-central1-a", core.ToolRequestTypeUpdate},
		{"update instance", "gcloud compute instances update my-instance --update-labels env=prod", core.ToolRequestTypeUpdate},
		{"remove-tags (update, not delete)", "gcloud compute instances remove-tags my-instance --tags tag1,tag2", core.ToolRequestTypeUpdate},
		{"add-tags", "gcloud compute instances add-tags my-instance --tags tag1,tag2", core.ToolRequestTypeUpdate},
		{"reset instance", "gcloud compute instances reset my-instance", core.ToolRequestTypeUpdate},
		{"add-iam-policy-binding", "gcloud projects add-iam-policy-binding my-project --member user:foo@bar.com --role roles/viewer", core.ToolRequestTypeUpdate},

		// Delete operations
		{"delete instance", "gcloud compute instances delete my-instance --zone us-central1-a", core.ToolRequestTypeDelete},
		{"drain node", "gcloud container node-pools drain my-pool", core.ToolRequestTypeDelete},
		{"revoke iam", "gcloud projects remove-iam-policy-binding my-project --member user:foo@bar.com", core.ToolRequestTypeDelete},

		// Positional args — verb should still be detected correctly (critical fix)
		{"stop with positional instance name", "gcloud compute instances stop my-instance", core.ToolRequestTypeUpdate},
		{"create with positional name", "gcloud compute instances create my-new-vm --zone us-central1-a", core.ToolRequestTypeCreate},
		{"remove-tags with positional args", "gcloud compute instances remove-tags my-instance --tags tag1", core.ToolRequestTypeUpdate},
		{"list under Cloud Run (run is resource group, not verb)", "gcloud run services list", core.ToolRequestTypeRead},

		// Positional args with verb-like names must NOT cause false positives
		{"stop instance with verb-like name", "gcloud compute instances stop delete-old-cache", core.ToolRequestTypeUpdate},
		{"create instance with verb-like name", "gcloud compute instances create start-worker-node --zone us-central1-a", core.ToolRequestTypeCreate},

		// Edge cases
		{"empty command", "", ""},
		{"only gcloud", "gcloud", ""},
		{"unknown verb falls through", "gcloud compute instances frobnicate --zone us-central1-a", ""},
		{"without gcloud prefix", "compute instances list", core.ToolRequestTypeRead},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := inferGcpVerbType(tt.command)
			assert.Equal(t, tt.expected, result, "command: %s", tt.command)
		})
	}
}

func TestVerbExtractionSkipsPositionalArgs(t *testing.T) {
	// This test specifically validates the fix for the critical bug where
	// positional arguments were mistaken for verbs, causing misclassification.
	tests := []struct {
		name     string
		provider string
		command  string
		expected core.ToolRequestType
	}{
		// AWS: positional args after verb
		{"aws s3 cp source dest", "aws", "aws s3 cp myfile.txt s3://bucket/myfile.txt", core.ToolRequestTypeCreate},

		// Azure: positional VM name after verb
		{"az vm stop vmname", "azure", "az vm stop myProductionVM", core.ToolRequestTypeUpdate},
		{"az vm deallocate vmname", "azure", "az vm deallocate myProductionVM", core.ToolRequestTypeUpdate},
		{"az vm start vmname", "azure", "az vm start myProductionVM", core.ToolRequestTypeUpdate},

		// GCP: positional instance name after verb
		{"gcp stop with instance name", "gcp", "gcloud compute instances stop my-production-instance", core.ToolRequestTypeUpdate},
		{"gcp delete with instance name", "gcp", "gcloud compute instances delete my-old-instance", core.ToolRequestTypeDelete},
		{"gcp describe with instance name", "gcp", "gcloud compute instances describe my-instance", core.ToolRequestTypeRead},
		{"gcp remove-tags with instance name", "gcp", "gcloud compute instances remove-tags my-instance --tags http-server", core.ToolRequestTypeUpdate},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var result core.ToolRequestType
			switch tt.provider {
			case "aws":
				result = inferAwsVerbType(tt.command)
			case "azure":
				result = inferAzureVerbType(tt.command)
			case "gcp":
				result = inferGcpVerbType(tt.command)
			}
			assert.Equal(t, tt.expected, result, "command: %s", tt.command)
		})
	}
}

func TestExtractCommandFromToolInput(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"json with command", `{"command":"az bot update --help"}`, "az bot update --help"},
		{"json with extra fields", `{"command":"aws s3 ls","region":"us-east-1"}`, "aws s3 ls"},
		{"plain command string", "az vm list --help", "az vm list --help"},
		{"invalid json", "{not json", "{not json"},
		{"empty string", "", ""},
		{"json with empty command", `{"command":""}`, `{"command":""}`},
		{"json without command field", `{"query":"something"}`, `{"query":"something"}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractCommandFromToolInput(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestInferVerbType_JSONInput(t *testing.T) {
	// Verify that JSON-wrapped inputs are classified correctly.
	// This tests the fix for the bug where {"command":"az bot update --help"}
	// was misclassified as "update" because the JSON wasn't unpacked first.
	tests := []struct {
		name     string
		provider string
		input    string
		expected core.ToolRequestType
	}{
		{"azure help in json", "azure", `{"command":"az bot update --help"}`, core.ToolRequestTypeRead},
		{"azure update in json", "azure", `{"command":"az vm update --name myVM"}`, core.ToolRequestTypeUpdate},
		{"azure list in json", "azure", `{"command":"az vm list"}`, core.ToolRequestTypeRead},
		{"azure delete in json", "azure", `{"command":"az vm delete --name myVM"}`, core.ToolRequestTypeDelete},
		{"aws help in json", "aws", `{"command":"aws ec2 describe-instances --help"}`, core.ToolRequestTypeRead},
		{"aws create in json", "aws", `{"command":"aws ec2 run-instances --image-id ami-123"}`, core.ToolRequestTypeCreate},
		{"gcp help in json", "gcp", `{"command":"gcloud compute instances list --help"}`, core.ToolRequestTypeRead},
		{"gcp delete in json", "gcp", `{"command":"gcloud compute instances delete my-instance"}`, core.ToolRequestTypeDelete},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := extractCommandFromToolInput(tt.input)
			var result core.ToolRequestType
			switch tt.provider {
			case "aws":
				result = inferAwsVerbType(cmd)
			case "azure":
				result = inferAzureVerbType(cmd)
			case "gcp":
				result = inferGcpVerbType(cmd)
			}
			assert.Equal(t, tt.expected, result, "input: %s", tt.input)
		})
	}
}

func TestFallbackConsistency(t *testing.T) {
	// All providers should return empty string for unrecognized verbs,
	// allowing the caller to fall through to LLM-based classification.
	unknownCommands := []struct {
		name    string
		command string
		inferFn func(string) core.ToolRequestType
	}{
		{"aws unknown verb", "aws ec2 frobnicate --instance-id i-123", inferAwsVerbType},
		{"azure unknown verb", "az vm frobnicate --name myVM", inferAzureVerbType},
		{"gcp unknown verb", "gcloud compute instances frobnicate --zone us-central1-a", inferGcpVerbType},
	}

	for _, tt := range unknownCommands {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.inferFn(tt.command)
			assert.Equal(t, core.ToolRequestType(""), result,
				"unrecognized verbs should return empty string for LLM fallthrough, got: %s", result)
		})
	}
}
