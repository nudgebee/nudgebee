# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

This is an LLM-powered agentic code analysis service that performs intelligent log correlation with source code to identify and debug issues. It uses a multi-agent architecture built in Go with the langchaingo framework.

## Common Development Commands

### Build & Run
```bash
# Build the main binary
make build

# Build for Linux
make build-linux

# Run the application (starts HTTP server on port 8080)
make run

# Run CLI analysis mode
./build/code-analysis-agent --analyze \
  --repo "https://github.com/user/repo.git" \
  --logs "ERROR: Database connection failed" \
  --token "$GITHUB_TOKEN" \
  --prompt "Analyze the error"
```

### Testing
```bash
# Run all tests with race detection and coverage
make test

# Generate coverage report
make test-coverage

# Run specific test package
go test -v ./agents/...

# Run specific test function
go test -v -run TestOrchestratorAgent ./agents/

# Run E2E tests (requires GITHUB_TOKEN)
export GITHUB_TOKEN="your-token"
go test -v -run TestE2E ./api/handlers/
```

### Code Quality
```bash
# Run all checks (format, vet, lint, test)
make check

# Format code
make fmt

# Run go vet
make vet

# Run linter (requires golangci-lint)
make lint
```

### Docker
```bash
# Build Docker image
make docker-build

# Run Docker container in CLI mode
make docker-run GITHUB_TOKEN=$GITHUB_TOKEN

# Run analysis with Docker
make docker-analyze REPO=https://github.com/user/repo.git LOGS="ERROR..." TOKEN=$GITHUB_TOKEN

# Get interactive shell in container
make docker-shell
```

### Dependencies
```bash
# Download and tidy dependencies
make deps

# Setup development environment (includes installing golangci-lint)
make dev-setup
```

## Architecture

### Multi-Agent System

The system uses a **hierarchical multi-agent architecture**:

1. **OrchestratorAgent** ([agents/orchestrator_agent.go](agents/orchestrator_agent.go))
   - Central coordinator for all analysis workflows
   - Manages the working directory across all agents
   - Delegates to specialist agents based on task type
   - Handles repository cloning, PR creation, and final result assembly
   - Maintains shared tool invocation tracking across agents

2. **RouterAgent** ([agents/router_agent.go](agents/router_agent.go))
   - Analyzes incoming requests to determine which specialist agent to use
   - Routes to: CodeAgent, ErrorRCAAgent, PerformanceDebuggerAgent, SecurityAuditorAgent
   - Uses LLM to intelligently classify the task

3. **Specialist Agents** (all use ReAct planning):
   - **CodeAgent** ([agents/code_agent.go](agents/code_agent.go)) - General code analysis and correlation
   - **ErrorRCAAgent** ([agents/error_rca_agent.go](agents/error_rca_agent.go)) - Root cause analysis for errors
   - **PerformanceDebuggerAgent** ([agents/performance_debugger_agent.go](agents/performance_debugger_agent.go)) - Performance issues
   - **SecurityAuditorAgent** ([agents/security_auditor_agent.go](agents/security_auditor_agent.go)) - Security vulnerabilities

4. **CodeFixerAgent** ([agents/code_fixer_agent.go](agents/code_fixer_agent.go))
   - Implements fixes suggested by specialist agents
   - Uses minimal toolset (file_view, replace, submit_analysis) to reduce confusion
   - Can revert and rework fixes based on review feedback

5. **CodeReviewAgent** ([agents/code_review_agent.go](agents/code_review_agent.go))
   - Reviews proposed fixes before they're committed
   - Validates changes don't introduce new issues

### ReAct Planner

The **ReActPlanner** ([planners/react_planner.go](planners/react_planner.go)) is the core reasoning engine:
- Implements ReAct (Reasoning + Acting) pattern for LLM-driven tool orchestration
- Maintains conversation history with tool call/result pairs
- Provides repository context and intelligent guidance to LLM
- Tracks consecutive exploration commands to force decision-making
- **Circular reasoning detection**: Currently DISABLED (too many false positives)
  - Previous logic flagged valid persistent diagnoses as "circular"
  - Code preserved in comments for future improvement with better detection

### Tool System

Tools are defined in the [tools/](tools/) directory implementing the `core.NBTool` interface ([tools/core/interface.go](tools/core/interface.go)):

**File Operations:**
- `file_find` - Find files by pattern/name
- `file_view` - Read file contents with line numbers
- `replace` - Modify files (replace, insert_before, insert_after, verify)

**Search:**
- `grep` - Simple grep search
- `ripgrep` - Advanced ripgrep with regex support

**Git Operations:**
- `git_tool` - Execute git commands (log, show, diff, status, etc.)
- `gh_tool` - GitHub CLI operations (create PR, merge, etc.)
- `repo_clone` - Clone repositories with credential handling

**Analysis:**
- `cli_tool` - Execute arbitrary shell commands (extensive capabilities)
- `submit_analysis` - Submit final analysis results with structured data

All tools are wrapped with `TrackedToolWrapper` ([tools/tracked_wrapper.go](tools/tracked_wrapper.go)) for comprehensive invocation tracking and logging.

### LLM Client

The **llm.Client** ([llm/client.go](llm/client.go)) supports multiple providers:
- **GoogleAI** (Gemini) - default provider
- **AWS Bedrock** (Claude, etc.)
- **OpenAI** (GPT models)

Provider is configured via environment variables:
```bash
LLM_PROVIDER=googleai
LLM_MODEL_NAME=gemini-2.5-pro
LLM_PROVIDER_API_KEY=your-key
LLM_PROVIDER_REGION=us-west-2
```

### Configuration

Config is managed in [config/config.go](config/config.go) using Viper:
- Loads from `config.yaml` (optional)
- Loads from `secrets.yaml` (for sensitive data)
- Loads from mounted secrets in `/etc/secrets/nudgebee`
- All settings overridable via environment variables

Key configs:
- Server settings (port, timeouts)
- Analysis settings (workspace dir, processing time limits)
- Git settings (clone timeout, max repo size)
- Agent settings (max ReAct iterations, max log lines)
- LLM provider settings

### Session Context

The **SessionContext** ([internal/session/context.go](internal/session/context.go)) maintains state across agent executions:
- Repository context (URL, branch, local path, GitHub repo info)
- Git credentials
- Analysis ID and logs
- Workload information
- Shared tool tracker

### Structured Logging

The **Logger** ([common/logger.go](common/logger.go)) provides structured JSON logging with:
- Analysis lifecycle events (Start, StepStart, ToolCall, StepComplete, FinalAnswer, etc.)
- Tool invocation tracking via **ToolInvocationTracker** ([common/tool_tracker.go](common/tool_tracker.go))
- Context propagation (analysis_id, repo, user_id)
- Events enum for consistent logging

## Key Patterns & Conventions

### Agent Execution Flow
1. Request arrives at **AgenticAnalyzeHandler** ([api/handlers/agentic_analyze.go](api/handlers/agentic_analyze.go))
2. OrchestratorAgent clones repository if needed
3. RouterAgent selects appropriate specialist agent
4. Specialist agent uses ReAct planner to analyze with tools
5. If fix needed, CodeFixerAgent implements the fix
6. CodeReviewAgent validates the fix
7. If `raise_pr=true`, OrchestratorAgent creates PR
8. Final response returned with analysis and PR info

### Working Directory Management
- All agents share a **single working directory** set by OrchestratorAgent
- This ensures file operations, git commands, and tools all work on the same repo clone
- **After repository cloning**, OrchestratorAgent automatically detects the cloned path from tool invocations
  - Monitors tool tracker for successful `repo_clone` invocations
  - Extracts `local_path` from tool output
  - Calls `SetWorkingDirectory()` to update all agents
- Each agent's `WorkspaceDir` is updated by the orchestrator before execution
- ReAct planner's `repositoryContext.LocalPath` points to the actual cloned repo
- **Bug Fixes (2025-01)**:
  1. Added `updateWorkingDirectoryFromToolInvocations()` in OrchestratorAgent to auto-detect cloned repo path
  2. Added `.git` directory check in CodeFixerAgent before running `git diff` to gracefully handle clone failures

### Tool Minimalism for Fixer
- **CodeFixerAgent** intentionally uses minimal toolset (file_view, replace, submit_analysis)
- This reduces LLM confusion and improves fix execution speed
- RCA agents already provide exact file paths and line numbers, so fixer doesn't need search tools

### Credential Handling
The **CredentialHandler** ([internal/credentials/handler.go](internal/credentials/handler.go)) supports:
- Token authentication (GitHub PAT)
- SSH keys (base64 encoded)
- Basic auth (username/password)
- Encrypted credentials
- Environment variable references

### Error Handling
- All errors are wrapped with context using `fmt.Errorf`
- Structured errors logged with event types
- Analysis failures tracked with `AnalysisFailure` event
- Tool errors include tool name and invocation details

## Testing

### Test Structure
- Unit tests: `*_test.go` files alongside source
- E2E tests: [api/handlers/e2e_test.go](api/handlers/e2e_test.go)
- Agent tests: [agents/orchestrator_agent_test.go](agents/orchestrator_agent_test.go)

### Running Specific Tests
```bash
# Test specific agent
go test -v ./agents/ -run TestOrchestratorAgent

# Test with real repository (E2E)
export GITHUB_TOKEN=your-token
export TEST_CUSTOM_REPO_URL=https://github.com/your-org/repo.git
go test -v -run TestE2E_AnalyzeEndpoint_WithCustomRepo ./api/handlers/

# Test CLI
go test -v ./cmd/ -run TestCLI
```

## Environment Variables

### Required
- `GITHUB_TOKEN` - For cloning private repositories and creating PRs
- `LLM_PROVIDER_API_KEY` - API key for LLM provider

### Optional
- `LLM_PROVIDER` - LLM provider (default: googleai)
- `LLM_MODEL_NAME` - Model name (default: gemini-2.5-pro)
- `LLM_PROVIDER_REGION` - AWS region for Bedrock (default: us-west-2)
- `PORT` - Server port (default: 8080)
- `WORKSPACE_DIR` - Workspace directory (default: /tmp/code-analysis)
- `LOCAL_REPO_PATH` - For local development without cloning

## Development Tips

### Adding a New Agent
1. Create agent file in `agents/` directory
2. Implement agent struct with `Execute()` method
3. Initialize agent in `NewOrchestratorAgent()`
4. Add routing logic in `RouterAgent`
5. Define appropriate toolset for the agent
6. Create ReAct planner with tools and max iterations
7. Add structured logging with agent-specific events

### Adding a New Tool
1. Create tool file in `tools/` directory
2. Implement `core.NBTool` interface (Name, Description, InputSchema, Execute)
3. Define JSON schema for tool inputs
4. Add comprehensive error handling and logging
5. Wrap with `TrackedToolWrapper` for invocation tracking
6. Add to appropriate agent's toolset

### Modifying ReAct Behavior
- ReAct planner configuration in [config/config.go](config/config.go) under `AgentConfig`
- `react_max_iterations` controls maximum planning steps (default: 30)
- Pattern detection prevents infinite analysis loops
- Exploration limits force decision-making after too many file operations

### Local Development
```bash
# Use local repository to skip cloning
./build/code-analysis-agent --analyze \
  --repo "/path/to/local/repo" \
  --logs "ERROR: Something failed" \
  --prompt "Analyze this error"

# Set local repo path via environment
export LOCAL_REPO_PATH=/path/to/local/repo
```

### Debugging
- Check logs for structured events (EventStepStart, EventToolCall, etc.)
- Tool invocations tracked in ToolInvocationTracker
- ReAct planner maintains message history for debugging
- Use `--prompt` flag to guide analysis direction

## Important Files

- [cmd/main.go](cmd/main.go) - Entry point (CLI + server modes)
- [api/handlers/agentic_analyze.go](api/handlers/agentic_analyze.go) - Main request handler
- [agents/orchestrator_agent.go](agents/orchestrator_agent.go) - Central coordinator
- [planners/react_planner.go](planners/react_planner.go) - ReAct reasoning engine
- [config/config.go](config/config.go) - Configuration management
- [llm/client.go](llm/client.go) - LLM provider abstraction
- [common/logger.go](common/logger.go) - Structured logging
- [tools/core/interface.go](tools/core/interface.go) - Tool interface definition
