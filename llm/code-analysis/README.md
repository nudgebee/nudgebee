# Code Analysis Agent

A specialized Go service that performs lexical code analysis, correlating application logs with source code to help identify and debug issues.

## Features

- **Rich Command-Line Environment**: Executes a wide range of Linux commands for in-depth analysis, including `git`, `grep`, `find`, `curl`, `jq`, `ps`, `netstat`, and more.
- **Interactive LLM Tools**: Intelligent code analysis through specialized LLM tools instead of fixed algorithms
- **Advanced File & Network Operations**: Efficient file handling (`sed`, `awk`, `tar`, `unzip`) and network diagnostics (`ping`, `dig`, `nc`).
- **Smart Search Planning**: Automatically analyze logs and create optimal search strategies
- **Large Repository Support**: Handles massive codebases efficiently with intelligent filtering
- **Regex & Pattern Support**: Full regex support with optimized pattern matching
- **Git Integration**: Repository cloning with multiple authentication methods
- **Git Blame Analysis**: Map code lines to commits, authors, and pull requests  
- **Progressive Discovery**: Build understanding of codebase structure progressively
- **Multiple Auth Support**: Token, SSH key, basic auth, encrypted credentials, and environment variables

## Quick Start

### Prerequisites

- Go 1.24+
- Git
- Docker (optional)

### Installation

1. Clone the repository:
```bash
git clone https://github.com/nudgebee/code-analysis-agent.git
cd code-analysis-agent
```

2. Install dependencies:
```bash
make deps
```

3. Build the application:
```bash
make build
```

4. Run the service:
```bash
make run
```

The service will start on port 8080 by default.

### Docker

Build and run with Docker:
```bash
make docker-build
make docker-run
```

## API Usage

### Analyze Endpoint

**POST /analyze**

Perform code analysis by correlating application logs with source code.

#### Request Body

**Option 1: Remote Repository (requires cloning)**
```json
{
  "cloud_account_id": "acc-123",
  "tenant": "tenant-456",
  "workload_name": "my-app",
  "workload_namespace": "production",
  "workload_kind": "Deployment",
  "logs": "ERROR: Database connection failed at DatabaseManager.connect:42",
  "prompt": "Focus on database connectivity issues",
  "git_repository": {
    "url": "https://github.com/user/repo.git",
    "branch": "main"
  },
  "git_credentials": {
    "type": "token",
    "token": "ghp_xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"
  }
}
```

**Option 2: Local Repository (uses existing local directory)**
```json
{
  "cloud_account_id": "acc-123",
  "tenant": "tenant-456",
  "workload_name": "my-app",
  "workload_namespace": "production",
  "workload_kind": "Deployment",
  "logs": "ERROR: Database connection failed at DatabaseManager.connect:42",
  "prompt": "Focus on database connectivity issues",
  "git_repository": {
    "local_path": "/path/to/your/existing/repository",
    "branch": "main"
  }
}
```

> **💡 Local Repository Benefits:**
> - **Faster**: No cloning time, instant access to your code
> - **No Credentials**: No need to provide git credentials
> - **Development Friendly**: Perfect for testing with local changes
> - **Always Latest**: Uses your current working directory state

#### Authentication Methods

##### 1. GitHub Personal Access Token
```json
{
  "type": "token",
  "token": "ghp_xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"
}
```

##### 2. SSH Key (Base64 encoded)
```json
{
  "type": "ssh_key",
  "ssh_key": "LS0tLS1CRUdJTiBPUEVOU1NIIFBSSVZBVEUgS0VZLS0tLS0...",
  "ssh_passphrase": "optional-passphrase"
}
```

##### 3. Basic Authentication
```json
{
  "type": "basic",
  "username": "your-username",
  "password": "your-password"
}
```

##### 4. Encrypted Credentials
```json
{
  "type": "encrypted",
  "encrypted_data": "base64-encoded-encrypted-json-credentials"
}
```

##### 5. Environment Variable Reference
```json
{
  "type": "env_ref",
  "env_ref": "GIT_TOKEN_PROD"
}
```

#### Response

```json
{
  "success": true,
  "analysis_id": "analysis_1672531200",
  "matches": [
    {
      "file_path": "src/database/manager.go",
      "line_number": 42,
      "content": "func (dm *DatabaseManager) connect() error {",
      "match_type": "exact",
      "confidence": 1.0,
      "blame_info": {
        "commit_hash": "abc123...",
        "author": "John Doe",
        "author_email": "john@example.com",
        "date": "2023-01-01T12:00:00Z",
        "message": "Add database connection retry logic"
      }
    }
  ],
  "correlations": [
    {
      "log_line": "ERROR: Database connection failed at DatabaseManager.connect:42",
      "code_matches": [...],
      "severity": "error",
      "pattern": "DatabaseManager.connect"
    }
  ],
  "repository": {
    "url": "https://github.com/user/repo.git",
    "branch": "main",
    "cloned_path": "/tmp/code-analysis/repo_1672531200",
    "clone_time": "2023-01-01T12:00:00Z"
  },
  "processing_time": "2.5s"
}
```

### Health Check

**GET /health**

Check service health status.

### Service Info

**GET /info**

Get service information and capabilities.

## Configuration

The service can be configured via environment variables or config files:

### Environment Variables

- `PORT`: Server port (default: 8080)
- `WORKSPACE_DIR`: Directory for temporary repositories (default: /tmp/code-analysis)
- `MAX_PROCESSING_TIME`: Maximum analysis time (default: 30m)
- `FUZZY_THRESHOLD`: Fuzzy matching threshold (default: 0.8)
- `CLONE_TIMEOUT`: Git clone timeout (default: 5m)
- `ENCRYPTION_KEY`: Key for encrypted credentials (default: change-in-production)

### Config File

Create a `config.yaml` file:

```yaml
server:
  port: 8080
  read_timeout: 30s
  write_timeout: 30s
  max_request_size: 10485760
  shutdown_timeout: 30s

analysis:
  max_processing_time: 30m
  workspace_dir: /tmp/code-analysis
  fuzzy_threshold: 0.8
  max_results: 100

git:
  clone_timeout: 5m
  max_repo_size: 536870912
  default_branch: main

github:
  base_url: https://api.github.com
  timeout: 30s
  retry_attempts: 3

credentials:
  encryption_key: your-secret-key
  allowed_types: ["token", "ssh_key", "basic", "encrypted", "env_ref"]
```

## Development

### Running Tests

```bash
make test
```

### Code Quality

```bash
make check  # Run all checks (format, vet, lint, test)
make lint   # Run linter
make fmt    # Format code
make vet    # Run go vet
```

### Examples

```bash
make example-token  # Run example with token auth
make health-check   # Check service health
make service-info   # Get service information
```

## Architecture

The service follows a modular architecture:

- **API Layer**: Gin-based HTTP handlers
- **Analysis Engine**: Lexical search and pattern matching
- **Git Client**: Repository operations and blame analysis
- **Credential Handler**: Secure credential management
- **Tools**: Extensible tool system for different analysis types

## Security

- Credentials are handled securely with encryption support
- Temporary files are cleaned up automatically
- No credentials are logged or persisted
- Support for encrypted credential storage

## Deployment

### Kubernetes

Deploy using the provided Kubernetes manifests:

```bash
kubectl apply -f deploy/kubernetes/
```

### Docker Compose

```bash
docker-compose up -d
```

## Testing

### End-to-End Testing
Comprehensive tests against real repositories without mocking:

```bash
# Set GitHub token for repository access
export GITHUB_TOKEN="your-github-token"

# Run all E2E tests
./test_runner.sh

# Test with your repository
export TEST_CUSTOM_REPO_URL="https://github.com/your-org/your-repo.git"
go test -v -run TestE2E_AnalyzeEndpoint_WithCustomRepo
```

### System Tool Testing
Test individual system tools:

```bash
# Test search tools (ripgrep, ag, grep)
export TEST_REPO_PATH="/path/to/your/repo"
go test -v -run TestE2E_SystemSearchTool

# Test file operations (wc, head, tail, sed, find)
go test -v -run TestE2E_FileOperationsTool
```

### Performance Benchmarking
```bash
# Run performance benchmarks
export RUN_BENCHMARKS=true
./test_runner.sh
```

See `tests/e2e/README.md` for detailed testing documentation.

## Contributing

1. Fork the repository
2. Create a feature branch
3. Make your changes
4. Add tests
5. Run `make check`
6. Submit a pull request

---

## Development Context

### Project Overview
The **Code Analysis Agent** is a specialized Go service that performs lexical code analysis, correlating application logs with source code using a multi-agent architecture built with `langchaingo`.

### Architecture: Multi-Agent System
- **OrchestratorAgent**: Central coordinator for analysis workflows. Manages repository cloning, PR creation, and working directories.
- **RouterAgent**: Intelligently routes tasks to specialists: `CodeAgent`, `ErrorRCAAgent`, `PerformanceDebuggerAgent`, or `SecurityAuditorAgent`.
- **Specialist Agents**: All use **ReAct planning** (Reasoning + Acting) pattern for LLM-driven tool orchestration.
- **CodeFixerAgent**: Implements fixes using a minimal toolset (**file_view**, **replace**, **submit_analysis**) to reduce LLM confusion and improve speed.
- **CodeReviewAgent**: Validates proposed fixes before commitment.

### Core Reasoning Engine: ReAct Planner
- Implements ReAct pattern for tool orchestration.
- Tracks exploration commands to prevent infinite analysis loops.
- **Circular Reasoning Detection**: (Currently disabled to avoid false positives).

### Key Patterns & Conventions
- **Shared Working Directory**: All agents work on a single repo clone managed by the OrchestratorAgent.
- **Auto-Detection of Cloned Repo**: OrchestratorAgent monitors tool invocations to extract and update the working directory.
- **Credential Handling**: Supports tokens, SSH keys, and encrypted credentials.
- **Tool Minimalist Fixer**: CodeFixerAgent uses a restricted toolset (`file_view`, `replace`) for better accuracy.

### Common Development Commands
- `make check`: Runs format, vet, lint, and test (all-in-one check).
- `make test`: Runs unit tests with race detection and coverage.
- `make build`: Builds the main binary.
- `make docker-build`: Builds the service image.

## License

This project is licensed under the MIT License - see the LICENSE file for details.