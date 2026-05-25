package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"nudgebee/code-analysis-agent/agents"
	"nudgebee/code-analysis-agent/api/handlers"
	"nudgebee/code-analysis-agent/common"
	"nudgebee/code-analysis-agent/config"
	"nudgebee/code-analysis-agent/internal/credentials"
	"nudgebee/code-analysis-agent/internal/git"
	"nudgebee/code-analysis-agent/llm"

	"github.com/gin-gonic/gin"
)

// Environment variable names for receiving large payloads from llm-server
const (
	envCodeAgentLogs   = "CODE_AGENT_LOGS"
	envCodeAgentPrompt = "CODE_AGENT_PROMPT"
	defaultPrompt      = "Analyze the logs for errors"
)

func authHandlerMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Bypass auth for health/info probes and CORS preflight requests
		if c.Request.URL.Path == "/health" || c.Request.URL.Path == "/info" || c.Request.Method == http.MethodOptions {
			c.Next()
			return
		}

		token := os.Getenv("NB_WORKSPACE_TOKEN")
		if token == "" {
			log.Printf("WARNING: NB_WORKSPACE_TOKEN is not configured, rejecting %s request to %s", c.Request.Method, c.Request.URL.Path)
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": "workspace token not configured"})
			return
		}

		authHeader := c.GetHeader("X-Workspace-Token")
		if authHeader != token {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
			return
		}

		c.Next()
	}
}

func main() {
	// Define command-line flags
	var (
		analyze          = flag.Bool("analyze", false, "Run CLI analysis mode")
		serverMode       = flag.Bool("server", false, "Start web server mode")
		repoURL          = flag.String("repo", "", "Git repository URL")
		logs             = flag.String("logs", "", "Application logs to analyze")
		branch           = flag.String("branch", "main", "Git branch to analyze")
		token            = flag.String("token", "", "GitHub token (or set GITHUB_TOKEN env var)")
		prompt           = flag.String("prompt", defaultPrompt, "Analysis prompt")
		agent            = flag.String("agent", "code_agent", "Agent to use (default: code_agent)")
		eventId          = flag.String("eventid", "", "Event ID for tracking (optional)")
		recommendationId = flag.String("recommendationid", "", "Recommendation ID for tracking (optional)")
		accountId        = flag.String("accountid", "", "Account ID for recommendation link (optional)")
		health           = flag.Bool("health", false, "Health check")
		version          = flag.Bool("version", false, "Show version")
		conversationId   = flag.String("conversationid", "", "Conversation ID for context (optional)")
		gitProvider      = flag.String("provider", "", "Git provider (github, gitlab, or auto-detect if empty)")
		followup         = flag.Bool("followup", false, "Follow up on an existing PR (address CI failures and review comments)")
		prURL            = flag.String("pr-url", "", "PR URL to follow up on")
	)

	// Custom boolean flag for raisepr that handles both --raisepr and --raisepr=false syntax
	var raisePRValue string
	flag.StringVar(&raisePRValue, "raisepr", "", "Automatically raise a pull request if a fix is generated (true/false, default: false)")

	flag.Parse()

	// Debug: Log parsed flags (redact secrets to prevent credential leakage in logs)
	log.Printf("DEBUG CLI: All flags parsed - analyze=%v, repo='%s', logs_len=%d, branch='%s', token='[REDACTED len=%d]', prompt='%s', agent='%s', raisePRValue='%s', eventId='%s', recommendationId='%s', accountId='%s'",
		*analyze, *repoURL, len(*logs), *branch, len(*token), *prompt, *agent, raisePRValue, *eventId, *recommendationId, *accountId)
	log.Printf("DEBUG CLI: Raw args: %v", redactArgs(os.Args))

	// Version info
	if *version {
		fmt.Println("Code Analysis Agent v1.0.0")
		return
	}

	// Health check
	if *health {
		fmt.Println("OK")
		return
	}

	// Load configuration
	cfg, err := config.LoadConfig()
	if err != nil {
		// Create a basic logger for initialization errors
		initLogger := common.NewLogger("init", "config", "system", nil)
		initLogger.Error(common.EventAnalysisFailure, "Failed to load configuration", err, nil)
		log.Fatalf("Failed to load configuration: %v", err)
	}

	// Check if CLI analysis mode
	if *analyze {
		raisePR := parseRaisePR()
		log.Printf("Raise pr %v", raisePR)

		// Fall back to environment variables for large payloads
		// that may exceed ARG_MAX when passed as command-line arguments
		logsValue := *logs
		if logsValue == "" {
			if envLogs := os.Getenv(envCodeAgentLogs); envLogs != "" {
				logsValue = envLogs
			}
		}
		promptValue := *prompt
		if promptValue == defaultPrompt {
			if envPrompt := os.Getenv(envCodeAgentPrompt); envPrompt != "" {
				promptValue = envPrompt
			}
		}

		runCLIAnalysis(cfg, *repoURL, logsValue, *branch, *token, promptValue, *agent, *eventId, *recommendationId, *accountId, raisePR, *conversationId, *gitProvider)
		return
	}

	// Check if PR followup mode
	if *followup {
		runFollowup(cfg, *repoURL, *prURL, *branch, *token, *gitProvider)
		return
	}

	// If no flags provided, show help
	if len(os.Args) == 1 && !*serverMode {
		fmt.Println("Code Analysis CLI - Intelligent log analysis with source code correlation")
		fmt.Println("")
		fmt.Println("Usage:")
		fmt.Println("  code-analysis-cli [OPTIONS]")
		fmt.Println("")
		fmt.Println("CLI Analysis Mode (default):")
		fmt.Println("  # General Code Agent (no repo/token needed):")
		fmt.Println("  code-analysis-cli --analyze \\")
		fmt.Println("    --prompt='Explain the main function in main.go'")
		fmt.Println("")
		fmt.Println("  # Log Analysis (requires repo and token):")
		fmt.Println("  code-analysis-cli --analyze \\")
		fmt.Println("    --repo='https://github.com/user/repo.git' \\")
		fmt.Println("    --logs='ERROR: TypeError at line 42' \\")
		fmt.Println("    --token='ghp_xxxxx' \\")
		fmt.Println("    --raisepr=true \\")
		fmt.Println("    --eventid='event_12345'")
		fmt.Println("")
		fmt.Println("  # Code Correlation Analysis (requires repo and token):")
		fmt.Println("  code-analysis-cli --analyze \\")
		fmt.Println("    --repo='https://github.com/user/repo.git' \\")
		fmt.Println("    --prompt='Check anomaly logic for recent changes' \\")
		fmt.Println("    --token='ghp_xxxxx'")
		fmt.Println("")
		fmt.Println("  # Docker Usage:")
		fmt.Println("  docker run --rm -it -e GITHUB_TOKEN=ghp_xxx \\")
		fmt.Println("    -v \"$(pwd):/workspace\" nudgebee/code-analysis-cli \\")
		fmt.Println("    code-analysis-cli --analyze --repo https://github.com/user/repo.git --logs 'ERROR: ...'")
		fmt.Println("")
		fmt.Println("Server Mode:")
		fmt.Println("  code-analysis-cli --server              # Start web server on port 8080")
		fmt.Println("")
		fmt.Println("Options:")
		flag.PrintDefaults()
		fmt.Println("")
		fmt.Println("Environment Variables:")
		fmt.Println("  GITHUB_TOKEN              GitHub access token")
		fmt.Println("  LLM_PROVIDER               LLM provider (googleai, bedrock, openai)")
		fmt.Println("  LLM_MODEL_NAME             LLM model name")
		fmt.Println("  LLM_PROVIDER_API_KEY       LLM API key")
		fmt.Println("  LLM_PROVIDER_REGION        LLM provider region")
		fmt.Println("")
		fmt.Println("Docker Shortcuts:")
		fmt.Println("  # Available as symlinks in container:")
		fmt.Println("  analyze --help             # Same as code-analysis-cli")
		fmt.Println("  ca --help                  # Short alias")
		fmt.Println("")
		return
	}

	// Setup Gin router
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()

	// Add middleware
	router.Use(gin.Recovery())
	router.Use(gin.Logger())
	router.Use(authHandlerMiddleware())

	// CORS middleware for development
	router.Use(func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", "*")
		c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Workspace-Token")

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}

		c.Next()
	})

	// Setup routes
	// Agentic handler — always initialize gitClient and credHandler in server mode
	// because /analyze receives repository URLs dynamically in each request
	gitClient := git.NewGitClient(cfg.Analysis.WorkspaceDir, cfg.Git.CloneTimeout, cfg.Git.MaxRepoSize)
	credHandler := credentials.NewCredentialHandler(cfg.Credentials.EncryptionKey)

	agenticHandler, err := handlers.NewAgenticAnalyzeHandler(cfg, gitClient, credHandler)
	if err != nil {
		// Log initialization failure but don't fatal, allowing server to start
		// The /analyze endpoint will need to handle the nil agenticHandler
		initLogger := common.NewLogger("init", "agentic_handler", "system", nil)
		initLogger.Error(common.EventAnalysisFailure, "Failed to initialize agentic handler - analysis endpoint will be disabled", err, nil)
		// log.Printf("Warning: Failed to initialize agentic handler: %v", err)
	}

	// Initialize execution handler
	executionHandler := handlers.NewExecutionHandler(cfg)

	// Initialize file handler
	fileHandler := handlers.NewFileHandler(cfg)

	// API routes
	v1 := router.Group("/api/v1")
	{
		if agenticHandler != nil {
			v1.POST("/analyze", agenticHandler.HandleAnalyze)
			v1.GET("/status/*id", agenticHandler.HandleStatus)
		} else {
			v1.POST("/analyze", func(c *gin.Context) {
				c.JSON(http.StatusServiceUnavailable, gin.H{
					"error": "Analysis service unavailable - failed to initialize LLM client",
				})
			})
		}
		v1.POST("/execute", executionHandler.HandleExecute)

		// File operations
		files := v1.Group("/files")
		{
			files.GET("/list", fileHandler.HandleListFiles)
			files.GET("/content", fileHandler.HandleGetFile)
			files.POST("/read-batch", fileHandler.HandleBatchReadFile)
			files.POST("/save", fileHandler.HandleSaveFile)
			files.DELETE("/delete", fileHandler.HandleDeleteFile)
		}
	}

	// Root routes
	if agenticHandler != nil {
		router.POST("/analyze", agenticHandler.HandleAnalyze)
		router.GET("/status/*id", agenticHandler.HandleStatus)
	} else {
		router.POST("/analyze", func(c *gin.Context) {
			c.JSON(http.StatusServiceUnavailable, gin.H{
				"error": "Analysis service unavailable - failed to initialize LLM client",
			})
		})
	}
	router.POST("/execute", executionHandler.HandleExecute)

	// Health check
	router.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"status":    "healthy",
			"service":   "code-analysis-agent",
			"version":   "1.0.0",
			"timestamp": time.Now().Format(time.RFC3339),
		})
	})

	// Info endpoint
	router.GET("/info", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"service":     "code-analysis-cli",
			"version":     "3.0.0",
			"description": "CLI-focused LLM-powered agentic code analysis with intelligent tool orchestration",
			"capabilities": []string{
				"llm_driven_analysis",
				"agentic_tool_orchestration",
				"react_planner_integration",
				"intelligent_code_correlation",
				"system_search_tools",
				"ripgrep_ag_grep_find",
				"advanced_file_operations",
				"intelligent_search_planning",
				"git_repository_cloning",
				"git_blame_analysis",
				"log_correlation",
				"regex_pattern_matching",
				"large_repository_support",
				"progressive_discovery",
			},
			"system_tools": []string{
				"File & Text: cat, ls, grep, find, sed, awk, wc, head, tail, less, tree, jq, yq, unzip, tar",
				"Process & System: ps, top, lsof, free, uptime, date, hostname",
				"Networking: ping, dig, nslookup, netstat, telnet, nc, curl, wget",
				"Version Control: git, gh",
			},
			"supported_auth": []string{
				"token",
				"ssh_key",
				"basic",
				"encrypted",
				"env_ref",
			},
			"endpoints": map[string]string{
				"analyze": "POST /analyze - Perform agentic code analysis",
				"execute": "POST /execute - Remote command execution in isolated workspace",
				"health":  "GET /health - Health check",
				"info":    "GET /info - Service information",
			},
		})
	})

	// Start server
	server := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.Server.Port),
		Handler:      router,
		ReadTimeout:  cfg.Server.ReadTimeout,
		WriteTimeout: cfg.Server.WriteTimeout,
	}

	// Create server logger
	serverLogger := common.NewLogger("server_init", "http_server", "system", map[string]any{
		"port": cfg.Server.Port,
	})
	serverLogger.Log(common.EventAnalysisStart, "Starting code analysis agent server", map[string]any{
		"port": cfg.Server.Port,
		"endpoints": map[string]string{
			"health":  fmt.Sprintf("http://localhost:%d/health", cfg.Server.Port),
			"info":    fmt.Sprintf("http://localhost:%d/info", cfg.Server.Port),
			"analyze": fmt.Sprintf("http://localhost:%d/analyze", cfg.Server.Port),
			"execute": fmt.Sprintf("http://localhost:%d/execute", cfg.Server.Port),
		},
	})

	// Initializing the server in a goroutine so that
	// it won't block the graceful shutdown handling below
	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			serverLogger.Error(common.EventAnalysisFailure, "Server failed to start", err, nil)
			log.Fatalf("Server failed to start: %v", err)
		}
	}()

	// Wait for interrupt signal
	// Use a buffered channel to prevent missed signals
	quit := make(chan os.Signal, 1)

	// SIGINT (Ctrl+C), SIGTERM (docker stop), SIGQUIT (Ctrl+\), SIGHUP (terminal closed)
	signal.Notify(quit, os.Interrupt, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT, syscall.SIGHUP)

	log.Println("Server is running. Press Ctrl+C to stop.")

	// Wait for signal - this will block until a signal is received
	sig := <-quit
	log.Printf("Received signal: %v. Starting graceful shutdown...", sig)

	serverLogger.Log(common.EventAnalysisComplete, "Shutting down server", map[string]any{
		"shutdown_timeout": cfg.Server.ShutdownTimeout.String(),
	})

	ctx, cancel := context.WithTimeout(context.Background(), cfg.Server.ShutdownTimeout)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		serverLogger.Error(common.EventAnalysisFailure, "Server forced to shutdown", err, nil)
		log.Fatalf("Server forced to shutdown: %v", err)
	}

	serverLogger.Log(common.EventAnalysisComplete, "Server exited successfully", nil)
	os.Exit(0)
}

// parseRaisePR parses the raisePRValue string flag into a boolean
func parseRaisePR() bool {
	// This function should access the raisePRValue variable from main
	// Since we can't access it directly, we'll use os.Args to check for the flag
	for _, arg := range os.Args {
		if arg == "--raisepr=true" || arg == "--raisepr" {
			return true
		}
		if arg == "--raisepr=false" {
			return false
		}
	}
	return false // default
}

var sensitiveFlags = []string{"--token", "-token"}

func redactArgs(args []string) []string {
	safe := make([]string, len(args))
	copy(safe, args)
	for i := 0; i < len(safe); i++ {
		if safe[i] == "--" {
			break
		}
		for _, f := range sensitiveFlags {
			if strings.HasPrefix(safe[i], f+"=") {
				safe[i] = f + "=[REDACTED]"
				break
			}
			if safe[i] == f && i+1 < len(safe) {
				safe[i+1] = "[REDACTED]"
				i++
				break
			}
		}
	}
	return safe
}

func runCLIAnalysis(cfg *config.Config, repoURL, logs, branch, token, prompt, agent, eventId, recommendationId, accountId string, raisePR bool, conversationId, gitProvider string) {
	// Make logs optional for code correlation scenarios
	if logs == "" && prompt == "Analyze the logs for errors" {
		log.Fatal("Logs are required (--logs) for log analysis, or provide a specific --prompt for code correlation")
	}

	// Get token from env if not provided (check both GITHUB_TOKEN and GITLAB_TOKEN)
	if token == "" {
		token = os.Getenv("GITHUB_TOKEN")
	}
	if token == "" {
		token = os.Getenv("GITLAB_TOKEN")
	}
	// Determine if this is a remote repository or local path
	isRemoteRepo := strings.HasPrefix(repoURL, "http://") || strings.HasPrefix(repoURL, "https://") || strings.HasPrefix(repoURL, "git@")

	// Git token is only required for remote repositories
	if repoURL != "" && isRemoteRepo && token == "" {
		log.Fatal("Git token is required (--token, GITHUB_TOKEN, or GITLAB_TOKEN env var) for remote repositories")
	}

	// Generate analysis ID for tracking
	var analysisID string
	if eventId != "" {
		analysisID = fmt.Sprintf("event_%s", eventId)
	} else {
		analysisID = fmt.Sprintf("cli_analysis_%d", time.Now().Unix())
	}

	// Initialize structured logger
	loggerContext := map[string]any{
		"repo":   repoURL,
		"branch": branch,
		"prompt": prompt,
	}
	if eventId != "" {
		loggerContext["event_id"] = eventId
	}
	logger := common.NewLogger(analysisID, repoURL, "cli-user", loggerContext)

	// Log analysis start
	logger.AnalysisStart(repoURL, len(logs))
	logger.Log(common.EventAnalysisStart, "Starting CLI analysis", map[string]any{
		"analysis_id": analysisID,
		"repository":  repoURL,
		"branch":      branch,
		"logs_length": len(logs),
	})

	// Initialize agentic handler
	var gitClient *git.GitClient
	var credHandler *credentials.CredentialHandler

	// Initialize GitClient and CredentialHandler only if a repository URL is provided
	if repoURL != "" {
		gitClient = git.NewGitClient(cfg.Analysis.WorkspaceDir, cfg.Git.CloneTimeout, cfg.Git.MaxRepoSize)
		credHandler = credentials.NewCredentialHandler(cfg.Credentials.EncryptionKey)
	}

	agenticHandler, err := handlers.NewAgenticAnalyzeHandler(cfg, gitClient, credHandler)
	if err != nil {
		log.Fatalf("Failed to create agentic handler: %v", err)
	}

	// Create analysis request
	// Only use logs if actually provided via --logs flag
	analysisLogs := logs

	// Create GitRepository based on whether it's remote or local
	var gitRepo handlers.GitRepository
	log.Printf("DEBUG CLI: Creating GitRepository - repoURL='%s', isRemoteRepo=%v", repoURL, isRemoteRepo)

	if repoURL != "" {
		if isRemoteRepo {
			// Normalize GitHub URLs to ensure .git suffix
			normalizedURL := repoURL
			if strings.Contains(repoURL, "github.com") && !strings.HasSuffix(repoURL, ".git") {
				normalizedURL = repoURL + ".git"
			}

			gitRepo = handlers.GitRepository{
				URL:      normalizedURL,
				Branch:   branch,
				Provider: gitProvider,
			}
			log.Printf("DEBUG CLI: Created remote GitRepository - URL='%s', Branch='%s', Provider='%s'", gitRepo.URL, gitRepo.Branch, gitRepo.Provider)
		} else {
			gitRepo = handlers.GitRepository{
				LocalPath: repoURL,
				Branch:    branch,
				Provider:  gitProvider,
			}
			log.Printf("DEBUG CLI: Created local GitRepository - LocalPath='%s', Branch='%s', Provider='%s'", gitRepo.LocalPath, gitRepo.Branch, gitRepo.Provider)
		}
	} else {
		log.Printf("DEBUG CLI: No repository URL provided, GitRepository will be empty")
	}

	req := handlers.AgenticAnalyzeRequest{
		Logs:          analysisLogs,
		Prompt:        prompt,
		AgentID:       agent,
		GitRepository: gitRepo,
		GitCredentials: credentials.GitCredentials{
			Type:  "token",
			Token: token,
		},
		RaisePR:          raisePR,
		EventId:          eventId,
		RecommendationId: recommendationId,
		AccountId:        accountId,
		ConversationId:   conversationId,
	}

	// Debug: Log the final request before sending
	log.Printf("DEBUG CLI: Final request created - GitRepository.URL='%s', GitRepository.LocalPath='%s', GitRepository.Branch='%s', Logs_len=%d",
		req.GitRepository.URL, req.GitRepository.LocalPath, req.GitRepository.Branch, len(req.Logs))

	// Run analysis
	ctx := context.Background()
	response, err := agenticHandler.HandleAgenticAnalyze(ctx, req)
	if err != nil {
		logger.AnalysisFailure(err, "handler_execution")
		log.Fatalf("Analysis failed: %v", err)
	}

	// Log final result
	logger.FinalAnswer(response.AgentResponse, "agent")
	logger.AnalysisComplete(response, 1)

	// Log the final result using structured logging
	logger.Result(common.EventFinalAnswer, "Analysis completed with result", response)
	logger.Log(common.EventAnalysisComplete, "CLI analysis completed", map[string]any{
		"analysis_id": analysisID,
	})
}

func runFollowup(cfg *config.Config, repoURL, prURL, branch, token, gitProvider string) {
	if prURL == "" {
		log.Fatal("PR URL is required (--pr-url)")
	}

	// Parse PR number from URL
	prNumber, err := agents.ParsePRNumber(prURL)
	if err != nil {
		log.Fatalf("Failed to parse PR number from URL: %v", err)
	}

	// Get token from env if not provided
	if token == "" {
		token = os.Getenv("GITHUB_TOKEN")
	}
	if token == "" {
		token = os.Getenv("GITLAB_TOKEN")
	}
	if token == "" {
		log.Fatal("Git token is required (--token, GITHUB_TOKEN, or GITLAB_TOKEN env var)")
	}

	// Determine if remote repo
	isRemoteRepo := strings.HasPrefix(repoURL, "http://") || strings.HasPrefix(repoURL, "https://") || strings.HasPrefix(repoURL, "git@")

	// Generate analysis ID
	analysisID := fmt.Sprintf("pr_followup_%d_%d", prNumber, time.Now().Unix())

	// Initialize logger
	loggerContext := map[string]any{
		"repo":      repoURL,
		"branch":    branch,
		"pr_url":    prURL,
		"pr_number": prNumber,
	}
	logger := common.NewLogger(analysisID, repoURL, "cli-user", loggerContext)
	logger.Log(common.EventAnalysisStart, "Starting PR followup", loggerContext)

	// Determine workspace directory
	workspaceDir := cfg.Analysis.WorkspaceDir

	if repoURL != "" && isRemoteRepo {
		// Clone the repo and checkout the PR branch
		gitClient := git.NewGitClient(cfg.Analysis.WorkspaceDir, cfg.Git.CloneTimeout, cfg.Git.MaxRepoSize)
		gitClient.SetLogger(logger)
		credHandler := credentials.NewCredentialHandler(cfg.Credentials.EncryptionKey)

		creds := credentials.GitCredentials{
			Type:  "token",
			Token: token,
		}
		resolvedCreds, err := credHandler.ResolveCredentials(creds)
		if err != nil {
			log.Fatalf("Failed to resolve credentials: %v", err)
		}

		// Normalize URL
		normalizedURL := repoURL
		if !strings.HasSuffix(repoURL, ".git") &&
			(strings.Contains(repoURL, "github.com") || strings.Contains(repoURL, "gitlab.com")) {
			normalizedURL = repoURL + ".git"
		}

		clonedPath, err := gitClient.CloneRepository(context.Background(), normalizedURL, resolvedCreds)
		if err != nil {
			log.Fatalf("Failed to clone repository: %v", err)
		}
		workspaceDir = clonedPath
		logger.Log(common.EventRepoCloneComplete, "Repository cloned", map[string]any{"path": clonedPath})

		// Checkout the PR branch
		if branch != "" && branch != "main" && branch != "master" {
			checkoutCmd := exec.Command("git", "checkout", branch)
			checkoutCmd.Dir = clonedPath
			if output, checkoutErr := checkoutCmd.CombinedOutput(); checkoutErr != nil {
				// Try fetch + checkout for remote branches
				fetchCmd := exec.Command("git", "fetch", "origin", branch)
				fetchCmd.Dir = clonedPath
				if _, fetchErr := fetchCmd.CombinedOutput(); fetchErr != nil {
					log.Fatalf("Failed to fetch branch %s: %v", branch, fetchErr)
				}
				checkoutCmd2 := exec.Command("git", "checkout", "-b", branch, "origin/"+branch)
				checkoutCmd2.Dir = clonedPath
				if output2, checkoutErr2 := checkoutCmd2.CombinedOutput(); checkoutErr2 != nil {
					log.Fatalf("Failed to checkout branch %s: %v\nOutput: %s\nPrevious output: %s", branch, checkoutErr2, string(output2), string(output))
				}
			}
			logger.Log(common.EventStepComplete, "Checked out branch", map[string]any{"branch": branch})
		}
	} else if repoURL != "" {
		// Local repo path
		workspaceDir = repoURL
	}

	// Initialize LLM client
	llmClient, err := llm.NewClient(cfg)
	if err != nil {
		log.Fatalf("Failed to initialize LLM client: %v", err)
	}

	// Create and execute the PR followup agent
	agent := agents.NewPRFollowupAgent(cfg, llmClient, logger, workspaceDir, token, gitProvider)

	req := agents.PRFollowupRequest{
		RepoURL:  repoURL,
		Branch:   branch,
		PRNumber: prNumber,
		PRURL:    prURL,
		Provider: gitProvider,
	}

	result, err := agent.Execute(context.Background(), req)
	if err != nil {
		logger.AnalysisFailure(err, "pr_followup_execution")
		log.Fatalf("PR followup failed: %v", err)
	}

	// Output result
	logger.Result(common.EventFinalAnswer, "PR followup completed", result)
	logger.Log(common.EventAnalysisComplete, "PR followup completed", map[string]any{
		"analysis_id": analysisID,
		"success":     result.Success,
	})
}
