package handlers

import (
	"context"
	"os"
	"testing"
	"time"

	"nudgebee/code-analysis-agent/config"
	"nudgebee/code-analysis-agent/internal/credentials"
	"nudgebee/code-analysis-agent/internal/git"
)

// TestE2EPRList - End-to-end test for PR list functionality
// Set GITHUB_TOKEN environment variable before running
// Run with: GITHUB_TOKEN=your_token go test -run TestE2EPRList -v
func TestE2EPRList(t *testing.T) {
	// Skip if no tokens provided
	// githubToken := os.Getenv("GITHUB_TOKEN")
	githubToken := os.Getenv("GITLAB_TOKEN")
	if githubToken == "" {
		t.Skip("Set GITHUB_TOKEN environment variable to run this e2e test")
	}

	t.Log("=== E2E PR LIST TEST ===")
	t.Log("Testing complete pipeline from request to response")

	// Load configuration from environment variables and config files
	cfg, err := config.LoadConfig()
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	// Override specific test settings
	cfg.Analysis.WorkspaceDir = t.TempDir()
	cfg.Git.CloneTimeout = 5 * time.Minute
	cfg.Git.MaxRepoSize = 536870912 // 512MB
	cfg.Credentials.EncryptionKey = "test-key-for-e2e"
	cfg.Agent.ReActMaxIterations = 15 // Set max iterations for ReAct planner

	gitClient := git.NewGitClient(cfg.Analysis.WorkspaceDir, cfg.Git.CloneTimeout, cfg.Git.MaxRepoSize)
	credHandler := credentials.NewCredentialHandler(cfg.Credentials.EncryptionKey)

	handler, err := NewAgenticAnalyzeHandler(cfg, gitClient, credHandler)
	if err != nil {
		t.Fatalf("Failed to create handler: %v", err)
	}

	// Create request for PR listing
	req := AgenticAnalyzeRequest{
		Prompt:  "can you list last 2 pr for llm-server for prod branch",
		AgentID: "code_agent",
		GitRepository: GitRepository{
			URL:    "/tmp/test-repo",
			Branch: "main",
		},
		GitCredentials: credentials.GitCredentials{
			Type:  "token",
			Token: githubToken,
		},
	}

	t.Log("📋 Request Details:")
	t.Logf("  Prompt: %s", req.Prompt)
	t.Logf("  Agent: %s", req.AgentID)
	t.Logf("  Repo: %s", req.GitRepository.URL)
	t.Logf("  Branch: %s", req.GitRepository.Branch)

	// Execute the analysis
	t.Log("🚀 Starting analysis...")
	response, err := handler.HandleAgenticAnalyze(context.Background(), req)
	if err != nil {
		t.Fatalf("Analysis failed: %v", err)
	}

	// Validate response structure
	if response == nil || response.AgentResponse == nil {
		t.Fatal("Response or AgentResponse is nil")
	}

	t.Log("✅ Analysis completed successfully")
	t.Log("📊 Response Analysis:")
	t.Logf("  Title: %s", response.AgentResponse.Title)
	t.Logf("  Description: %s", response.AgentResponse.Description)
	t.Logf("  PR List Length: %d", len(response.AgentResponse.PRList))

	// Critical validation: Check if pr_list is populated
	if len(response.AgentResponse.PRList) == 0 {
		t.Error("❌ CRITICAL ISSUE: pr_list is empty - this is the bug we're investigating")
		t.Error("The LLM likely generated pr_list data but it was lost in the pipeline")
	} else {
		t.Log("✅ SUCCESS: pr_list is populated with data")
		for i, pr := range response.AgentResponse.PRList {
			t.Logf("  PR %d: #%d - %s (%s)", i+1, pr.Number, pr.Title, pr.State)
		}
	}

	// Additional debugging info
	t.Log("🔍 Additional Debug Info:")
	t.Logf("  Error Message: %s", response.AgentResponse.ErrorMessage)
	t.Logf("  File Path: %s", response.AgentResponse.FilePath)
	t.Logf("  Line Number: %d", response.AgentResponse.LineNumber)

	// Print raw response for inspection
	t.Log("📋 Full Response Structure:")
	t.Logf("  Agent Response Type: %T", response.AgentResponse)
	if len(response.AgentResponse.PRList) > 0 {
		t.Logf("  First PR URL: %s", response.AgentResponse.PRList[0].URL)
		t.Logf("  First PR Author: %s", response.AgentResponse.PRList[0].Author)
	}
}

func TestLogAnalysis(t *testing.T) {
	githubToken := os.Getenv("GITHUB_TOKEN")
	if githubToken == "" {
		t.Skip("Set GITHUB_TOKEN environment variable to run this e2e test")
	}

	t.Log("=== E2E LOG ANALYSIS TEST ===")

	// Load configuration from environment variables and config files
	cfg, err := config.LoadConfig()
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	cfg.Analysis.WorkspaceDir = os.TempDir()

	cfg.Git.CloneTimeout = 5 * time.Minute
	cfg.Git.MaxRepoSize = 536870912 // 512MB
	cfg.Credentials.EncryptionKey = "test-key-for-e2e"
	cfg.Agent.ReActMaxIterations = 30 // Set max iterations for ReAct planner

	gitClient := git.NewGitClient(cfg.Analysis.WorkspaceDir, cfg.Git.CloneTimeout, cfg.Git.MaxRepoSize)
	credHandler := credentials.NewCredentialHandler(cfg.Credentials.EncryptionKey)

	handler, err := NewAgenticAnalyzeHandler(cfg, gitClient, credHandler)
	if err != nil {
		t.Fatalf("Failed to create handler: %v", err)
	}

	req := AgenticAnalyzeRequest{
		Prompt:  "",
		Logs:    `{\"asctime\": \"2025-09-22 10:10:05,691\", \"levelname\": \"ERROR\", \"filename\": \"rabbitmq_client.py\", \"lineno\": 221, \"message\": \"Message processing failed (attempt 1/1)\", \"exc_info\": \"Traceback (most recent call last):\n File \\"/app/rabbitmq/rabbitmq_client.py\\", line 214, in _on_message\n self.callback(body)\n File \\"/app/rabbitmq/rabbitmq_consumers.py\\", line 20, in _discovery_callback\n run_async_discovery_handler(data)\n File \\"/app/handlers/discovery_handler.py\\", line 224, in run_async_discovery_handler\n handle_alert_rules(data[\\"data\\"], data[\\"tenant\\"], data[\\"cloud_account_id\\"])\n File \\"/app/handlers/alert_rules_handler.py\\", line 84, in handle_alert_rules\n handle_crd_based_rules(rules, tenant, cloud_account_id)\n File \\"/app/handlers/alert_rules_handler.py\\", line 44, in handle_crd_based_rules\n for item in rules.get(\\"items\\"):\n ^^^^^^^^^^^^^^^^^^\nTypeError: 'NoneType' object is not iterable\", \"taskName\": null}`,
		AgentID: "code_agent",
		GitRepository: GitRepository{
			URL:    "https://github.com/nudgebee/nudgebee.git",
			Branch: "main",
		},
		GitCredentials: credentials.GitCredentials{
			Type:  "token",
			Token: githubToken,
		},
		RaisePR: true,
	}

	t.Log("🚀 Starting direct analysis...")
	response, err := handler.HandleAgenticAnalyze(context.Background(), req)
	if err != nil {
		t.Fatalf("Analysis failed: %v", err)
	}

	if response == nil || response.AgentResponse == nil {
		t.Fatal("Response or AgentResponse is nil")
	}

	t.Log("✅ Direct analysis completed")
	t.Logf("  Title: %s", response.AgentResponse.Title)
	t.Logf("  Description length: %d chars", len(response.AgentResponse.Description))
}

// TestListCollectorServerPRs - Simple integration test: list 5 latest PRs for collector-server
// Run with: go test -run TestListCollectorServerPRs -v -timeout 5m ./api/handlers/
func TestListCollectorServerPRs(t *testing.T) {
	githubToken := os.Getenv("GITHUB_TOKEN")
	if githubToken == "" {
		t.Skip("Set GITHUB_TOKEN environment variable to run this test")
	}

	cfg, err := config.LoadConfig()
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	cfg.Analysis.WorkspaceDir = t.TempDir()
	cfg.Git.CloneTimeout = 5 * time.Minute
	cfg.Git.MaxRepoSize = 536870912
	cfg.Credentials.EncryptionKey = "test-key-for-e2e"
	cfg.Agent.ReActMaxIterations = 15

	gitClient := git.NewGitClient(cfg.Analysis.WorkspaceDir, cfg.Git.CloneTimeout, cfg.Git.MaxRepoSize)
	credHandler := credentials.NewCredentialHandler(cfg.Credentials.EncryptionKey)

	handler, err := NewAgenticAnalyzeHandler(cfg, gitClient, credHandler)
	if err != nil {
		t.Fatalf("Failed to create handler: %v", err)
	}

	req := AgenticAnalyzeRequest{
		Prompt:  "list me 5 latest PRs for collector-server",
		AgentID: "code_agent",
		GitRepository: GitRepository{
			URL:    os.Getenv("LOCAL_REPO_PATH"),
			Branch: "main",
		},
		GitCredentials: credentials.GitCredentials{
			Type:  "token",
			Token: githubToken,
		},
	}

	if req.GitRepository.URL == "" {
		req.GitRepository.URL = "/tmp/test-repo"
	}

	t.Logf("Prompt: %s", req.Prompt)
	t.Logf("Repo: %s", req.GitRepository.URL)

	response, err := handler.HandleAgenticAnalyze(context.Background(), req)
	if err != nil {
		t.Fatalf("Analysis failed: %v", err)
	}

	if response == nil || response.AgentResponse == nil {
		t.Fatal("Response is nil")
	}

	t.Logf("Title: %s", response.AgentResponse.Title)
	t.Logf("Description: %s", response.AgentResponse.Description)
	t.Logf("PR count: %d", len(response.AgentResponse.PRList))

	for i, pr := range response.AgentResponse.PRList {
		t.Logf("  PR %d: #%d - %s (%s) %s", i+1, pr.Number, pr.Title, pr.State, pr.URL)
	}
}

// TestUUIDPanicLogAnalysis - Integration test: analyze a worker pool panic caused by uuid.MustParse with empty string
// Run with: go test -run TestUUIDPanicLogAnalysis -v -timeout 10m ./api/handlers/
func TestUUIDPanicLogAnalysis(t *testing.T) {
	githubToken := os.Getenv("GITHUB_TOKEN")
	if githubToken == "" {
		t.Skip("Set GITHUB_TOKEN environment variable to run this test")
	}

	cfg, err := config.LoadConfig()
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	cfg.Analysis.WorkspaceDir = t.TempDir()
	cfg.Git.CloneTimeout = 5 * time.Minute
	cfg.Git.MaxRepoSize = 536870912
	cfg.Credentials.EncryptionKey = "test-key-for-e2e"
	cfg.Agent.ReActMaxIterations = 15

	gitClient := git.NewGitClient(cfg.Analysis.WorkspaceDir, cfg.Git.CloneTimeout, cfg.Git.MaxRepoSize)
	credHandler := credentials.NewCredentialHandler(cfg.Credentials.EncryptionKey)

	handler, err := NewAgenticAnalyzeHandler(cfg, gitClient, credHandler)
	if err != nil {
		t.Fatalf("Failed to create handler: %v", err)
	}

	errorLog := `{"time":"2026-01-31T02:06:47.936552646Z","level":"ERROR","msg":"pagerdutywebhook: failed to get PagerDuty incident","integration_id":"97105211-9770-4462-871f-c4edb7357933","error":{"message":"error unmarshaling JSON response: json: cannot unmarshal object into Go struct field Incident.incident.resolve_reason of type string","type":"*fmt.wrapError","stacktrace":"nudgebee/services/integrations.EnrichWithPagerDutyIncident(0xc0349ab410, 0xc00010fea8, {0xc02e400210?, 0xc0001103f8?}, {0xc014af6540?, 0xc042666b68?})\n\t/app/integrations/pagerduty_webhook.go:630 +0x37f\nnudgebee/services/integrations.PagerDutyWebhook.ProcessEventWebook({}, 0xc0349ab410, {0xc0349ab530, 0x1, 0xc021780480?}, {0xaf6fa0?, 0xc0003a8280?}, {0xc02dfd4000, 0x780})\n\t/app/integrations/pagerduty_webhook.go:608 +0x24f1\nnudgebee/services/integrations/core.ProcessEventWebook(0xc0349ab410, {0xc01864a380, 0x66}, 0xc036056f30, {0xc02dfd4000, 0x780})\n\t/app/integrations/core/integration_webhook.go:225 +0xdb3\nnudgebee/services/api.handlePublicWebhooksApis.genericWebhookHandler.func1(0xc01854a700)\n\t/app/api/public_webhooks.go:49 +0x39f\ngithub.com/gin-gonic/gin.(*Context).Next(0xc01854a700)\n\t/go/pkg/mod/github.com/gin-gonic/gin@v1.9.1/context.go:174 +0x2b\nmain.main.authHandlerMiddleware.func5(0xc01854a700)\n\t/app/cmd/main.go:52 +0xd2\ngithub.com/gin-gonic/gin.(*Context).Next(0xc01854a700)\n\t/go/pkg/mod/github.com/gin-gonic/gin@v1.9.1/context.go:174 +0x2b\nmain.main.traceResponseHeaderMiddleware.func4(0xc01854a700)\n\t/app/cmd/main.go:74 +0x5c\ngithub.com/gin-gonic/gin.(*Context).Next(...)\n\t/go/pkg/mod/github.com/gin-gonic/gin@v1.9.1/context.go:174\ngithub.com/Cyprinus12138/otelgin.Middleware.func1(0xc01854a700)\n\t/go/pkg/mod/github.com/!cyprinus12138/otelgin@v1.0.2/gintrace.go:155 +0x9f1\ngithub.com/gin-gonic/gin.(*Context).Next(0xc01854a700)\n\t/go/pkg/mod/github.com/gin-gonic/gin@v1.9.1/context.go:174 +0x2b\nmain.main.NewWithFilters.NewWithConfig.func6(0xc01854a700)\n\t/go/pkg/mod/github.com/samber/slog-gin@v1.10.2/middleware.go:126 +0x2c6\ngithub.com/gin-gonic/gin.(*Context).Next(...)\n\t/go/pkg/mod/github.com/gin-gonic/gin@v1.9.1/context.go:174\ngithub.com/gin-gonic/gin.CustomRecoveryWithWriter.func1(0xc01854a700)\n\t/go/pkg/mod/github.com/gin-gonic/gin@v1.9.1/recovery.go:102 +0x6f\ngithub.com/gin-gonic/gin.(*Context).Next(...)\n\t/go/pkg/mod/github.com/gin-gonic/gin@v1.9.1/context.go:174\ngithub.com/gin-gonic/gin.(*Engine).handleHTTPRequest(0xc0004141a0, 0xc01854a700)\n\t/go/pkg/mod/github.com/gin-gonic/gin@v1.9.1/gin.go:620 +0x64e\ngithub.com/gin-gonic/gin.(*Engine).ServeHTTP(0xc0004141a0, {0x2274dd0, 0xc016778ff0}, 0xc02dfd2280)\n\t/go/pkg/mod/github.com/gin-gonic/gin@v1.9.1/gin.go:576 +0x1ad\nnet/http.serverHandler.ServeHTTP({0x226bb50?}, {0x2274dd0?, 0xc016778ff0?}, 0x6?)\n\t/usr/local/go/src/net/http/server.go:3340 +0x8e\nnet/http.(*conn).serve(0xc02af858c0, {0x2277b78, 0xc014b30090})\n\t/usr/local/go/src/net/http/server.go:2109 +0x665\ncreated by net/http.(*Server).Serve in goroutine 1\n\t/usr/local/go/src/net/http/server.go:3493 +0x485\n"},"incident_id":"Q0E2TVV5HMZX1T"}`

	req := AgenticAnalyzeRequest{
		Logs:    errorLog,
		AgentID: "code_agent",
		GitRepository: GitRepository{
			URL:    os.Getenv("REPO_PATH"),
			Branch: "main",
		},
		GitCredentials: credentials.GitCredentials{
			Type:  "token",
			Token: githubToken,
		},
		RaisePR: true,
	}

	if req.GitRepository.URL == "" {
		req.GitRepository.URL = "/tmp/test-repo"
	}

	t.Logf("Logs: %s", errorLog[:80]+"...")
	t.Logf("Repo: %s", req.GitRepository.URL)

	response, err := handler.HandleAgenticAnalyze(context.Background(), req)
	if err != nil {
		t.Fatalf("Analysis failed: %v", err)
	}

	if response == nil || response.AgentResponse == nil {
		t.Fatal("Response is nil")
	}

	t.Logf("Title: %s", response.AgentResponse.Title)
	t.Logf("Description: %s", response.AgentResponse.Description)
	t.Logf("File Path: %s", response.AgentResponse.FilePath)
	t.Logf("Line Number: %d", response.AgentResponse.LineNumber)
	t.Logf("Error Message: %s", response.AgentResponse.ErrorMessage)
}
