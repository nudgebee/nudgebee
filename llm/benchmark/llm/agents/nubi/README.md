# Nubi Test Framework Documentation

## Overview

The Nubi Test Framework is an automated benchmarking and evaluation system for testing the Nubi LLM assistant against
real-world Kubernetes scenarios. It evaluates the quality of LLM responses using RAGAS metrics (Answer Similarity and
Answer Relevancy).

## Architecture

### Core Components

```
nubi/
├── test_ask_nubi.py          # Main test orchestrator
├── conftest.py                # Pytest configuration
├── utils/                     # Utility modules
│   ├── __init__.py
│   └── commands.py            # Command execution with timeout & diagnostics
├── fixtures/                  # Test case definitions (145 scenarios)
│   ├── 01_how_many_pods/
│   │   ├── test_case.yaml    # Test definition
│   │   └── manifests.yaml    # K8s resources (optional)
│   ├── 10_image_pull_backoff/
│   └── ...
├── shared/                    # Shared infrastructure manifests
│   ├── loki.yaml
│   ├── prometheus.yaml
│   └── tempo.yaml
└── report/                    # Generated evaluation reports
    └── nubi_eval_report_YYYYMMDD_timestamp.json
```

### Execution Flow

```
┌─────────────────────────────────────────────────────────────────┐
│ 1. Test Discovery (find_test_cases)                            │
│    - Scans fixtures/ directory for test_case.yaml files        │
│    - Returns sorted list of test case paths                    │
└────────────────────┬────────────────────────────────────────────┘
                     │
                     ▼
┌─────────────────────────────────────────────────────────────────┐
│ 2. Session Setup                                                │
│    - results_aggregator fixture: Creates empty results list    │
│    - ragas_setup fixture: Initializes LLM & embeddings         │
└────────────────────┬────────────────────────────────────────────┘
                     │
                     ▼
┌─────────────────────────────────────────────────────────────────┐
│ 3. For Each Test Case (test_case_runner fixture)               │
│    ┌─────────────────────────────────────────────────────────┐ │
│    │ a. Load test_case.yaml                                  │ │
│    │ b. Run before_test commands (kubectl apply, etc.)      │ │
│    │ c. Yield test case to test function                    │ │
│    │ d. Run after_test commands (cleanup)                   │ │
│    └─────────────────────────────────────────────────────────┘ │
└────────────────────┬────────────────────────────────────────────┘
                     │
                     ▼
┌─────────────────────────────────────────────────────────────────┐
│ 4. Test Execution (test_fixture)                               │
│    ┌─────────────────────────────────────────────────────────┐ │
│    │ a. Send user_prompt to Nubi API (get_llm_ans)          │ │
│    │ b. Receive LLM response                                 │ │
│    │ c. Evaluate with RAGAS metrics                         │ │
│    │    - Answer Similarity: Semantic match with expected   │ │
│    │    - Answer Relevancy: Relevance to the query          │ │
│    │ d. Append results to results_aggregator                │ │
│    └─────────────────────────────────────────────────────────┘ │
└────────────────────┬────────────────────────────────────────────┘
                     │
                     ▼
┌─────────────────────────────────────────────────────────────────┐
│ 5. Report Generation (generate_report)                         │
│    - Triggered by results_aggregator fixture teardown          │
│    - Calculates aggregate metrics                              │
│    - Saves JSON report to report/ directory                    │
└─────────────────────────────────────────────────────────────────┘
```

## Environment Setup

### Clone the repository

```sh
git clone git@github.com:nudgebee/nudgebee.git
cd nudgebee/llm/benchmark/llm/nubi
```

### Required Environment Variables

Create a `.env` file in the nubi directory:

```bash
TEST_ACCOUNT=your-account-id
TEST_TENANT=your-tenant-id
TEST_USER=your-user-id
GOOGLE_API_KEY=your-google-api-key  # For RAGAS evaluation
```

### Dependencies

```bash
pip install pytest requests pyyaml python-dotenv
pip install datasets ragas langchain-google-genai
```

## Test Case Structure

### Basic test_case.yaml Format

```yaml
# User query to send to Nubi
user_prompt: "How many pods are in the test-1 namespace?"

# Expected output(s) - can be string or list
expected_output:
  - "There are 14 pods in the test-1 namespace"

# Optional: Shell commands to run before test
before_test: |
  kubectl apply -f manifests.yaml
  sleep 60

# Optional: Shell commands to run after test (cleanup)
after_test: |
  kubectl delete -f manifests.yaml

# Optional: Tags for categorization
tags:
  - counting
  - easy
  - question-answer
```

### Field Descriptions

| Field             | Type           | Required | Description                                           |
|-------------------|----------------|----------|-------------------------------------------------------|
| `user_prompt`     | string         | **Yes**  | The question/query to send to Nubi API                |
| `expected_output` | string or list | **Yes**  | Ground truth answer(s) for evaluation                 |
| `before_test`     | string         | No       | Shell commands executed before test (setup)           |
| `after_test`      | string         | No       | Shell commands executed after test (cleanup)          |
| `tags`            | list           | No       | Categorization tags (not currently used in filtering) |

### Expected Output Format

The `expected_output` field supports two formats:

**Single string:**

```yaml
expected_output: "The pod is in CrashLoopBackOff state"
```

**List of strings:**

```yaml
expected_output:
  - "There are 14 pods in the test-1 namespace"
  - "14 pods are running in test-1"
```

Multiple expected outputs are joined with newlines for evaluation.

## How to Add New Test Fixtures

### Step 1: Create Test Directory

```bash
cd nudgebee/llm/benchmark/llm/nubi/fixtures
mkdir <test_number>_<descriptive_name>

# Example:
mkdir 200_pod_memory_leak
```

### Step 2: Create test_case.yaml

```bash
cd 200_pod_memory_leak
cat > test_case.yaml << 'EOF'
user_prompt: "Why is the api-server pod consuming so much memory?"
expected_output:
  - "The api-server pod has a memory leak in the cache layer"
  - "Memory consumption is increasing over time"
before_test: |
  kubectl apply -f manifests.yaml
  sleep 30
after_test: |
  kubectl delete -f manifests.yaml
tags:
  - memory
  - debugging
  - intermediate
EOF
```

### Step 3: (Optional) Create Kubernetes Manifests

If your test requires K8s resources:

```bash
cat > manifests.yaml << 'EOF'
apiVersion: v1
kind: Namespace
metadata:
  name: test-memory
---
apiVersion: v1
kind: Pod
metadata:
  name: api-server
  namespace: test-memory
spec:
  containers:
  - name: app
    image: nginx:latest
    resources:
      limits:
        memory: "64Mi"
      requests:
        memory: "32Mi"
EOF
```

### Step 4: Test Your Fixture

Run only your new test case:

```bash
# From the nubi directory
python -m pytest test_ask_nubi.py -k "200_pod_memory_leak" -s --capture=no
```

### Step 5: Verify Report Generation

After running, check:

```bash
ls -la report/
cat report/nubi_eval_report_*.json
```

## Running Tests

### Run All Tests

```bash
cd /Users/hsundar/Nudgebee/nudgebee/llm/benchmark/llm/nubi
python -m pytest test_ask_nubi.py --capture=no
```

### Run First N Tests

```bash
# Run only first 5 fixtures (sorted alphabetically)
python -m pytest test_ask_nubi.py --max-tests=5 --capture=no

# Run first 10 fixtures
python -m pytest test_ask_nubi.py --max-tests=10 --capture=no

# Combine with other options
python -m pytest test_ask_nubi.py --max-tests=3 -s --log-cli-level=INFO --capture=no
```

**How it works:** Fixtures are discovered and sorted alphabetically by directory name. The `--max-tests` option limits execution to the first N fixtures in that sorted order.

### Run Specific Test

```bash
# By fixture name (matches the directory name)
python -m pytest test_ask_nubi.py -k "how_many_pods" --capture=no

# By test number
python -m pytest test_ask_nubi.py -k "01_how_many_pods" --capture=no

# With verbose output
python -m pytest test_ask_nubi.py -k "how_many_pods" -s --capture=no

# With logging
python -m pytest test_ask_nubi.py --log-cli-level=INFO --capture=no

# View test IDs without running
python -m pytest test_ask_nubi.py --collect-only --capture=no
```

**How it works:** Each test gets an ID based on its fixture directory name (e.g., `test_fixture[01_how_many_pods]`). The
`-k` flag matches against this ID, so you can filter by any part of the directory name.

### Run Multiple Tests by Pattern

```bash
# All tests with "logs" in the name
python -m pytest test_ask_nubi.py -k "logs" --capture=no

# All tests starting with specific numbers
python -m pytest test_ask_nubi.py -k "01_ or 02_ or 03_" --capture=no

# All tests in the 100-109 range (note: "10" also matches 104, 105, etc.)
python -m pytest test_ask_nubi.py -k "10" --capture=no

# Better: specific range using starts with
python -m pytest test_ask_nubi.py -k "100_ or 101_ or 102_" --capture=no

# Exclude certain tests
python -m pytest test_ask_nubi.py -k "not logs" --capture=no

# Combine patterns
python -m pytest test_ask_nubi.py -k "logs and not transparency" --capture=no
```

## Understanding the Report

### Report Structure

```json
{
  "details": [
    {
      "query": "How many pods are in the test-1 namespace?",
      "retrieved_contexts": ["There are 14 pods in the test-1 namespace"],
      "expected_contexts": ["There are 14 pods in the test-1 namespace"],
      "ground_truth": [["There are 14 pods in the test-1 namespace"]],
      "answer_similarity": 98.5,
      "answer_relevancy": 95.2
    }
  ],
  "summary": {
    "total_queries": 100,
    "answer_similarity": 87.3,
    "answer_relevancy": 89.1,
    "overall_accuracy": 88.2
  }
}
```

### Metrics Explained

| Metric                | Range  | Description                                                  |
|-----------------------|--------|--------------------------------------------------------------|
| **answer_similarity** | 0-100% | Semantic similarity between LLM response and expected output |
| **answer_relevancy**  | 0-100% | How relevant the answer is to the user's query               |
| **overall_accuracy**  | 0-100% | Average of similarity and relevancy                          |

### Interpreting Scores

- **90-100%**: Excellent - Response matches expected output very closely
- **75-89%**: Good - Response is semantically similar with minor differences
- **60-74%**: Fair - Response captures main points but has gaps
- **Below 60%**: Poor - Response needs improvement

## Advanced Features

### Before/After Test Commands

Commands are executed as a single bash script via the `utils/commands.py` module, which preserves all bash constructs
like for loops, if statements, heredocs, etc.

```yaml
before_test: |
  # Multiple commands are supported
  kubectl create namespace test-ns
  kubectl apply -f manifests.yaml

  # For loops work correctly
  for i in {1..60}; do
    if kubectl get pod -l app=myapp -n test-ns 2>/dev/null | grep -q myapp; then
      echo "✅ Pod exists!"
      break
    else
      echo "⏳ Waiting for pod... ($i/60)"
      sleep 5
    fi
  done

  # Heredocs work correctly
  cat <<'EOF' | kubectl apply -f -
  apiVersion: v1
  kind: ConfigMap
  metadata:
    name: my-config
  data:
    key: value
  EOF

  # If statements work correctly
  if [ "$POD_EXISTS" = false ]; then
    echo "Pod not found, exiting"
    exit 1
  fi

  # Run setup scripts
  ./setup.sh

after_test: |
  kubectl delete namespace test-ns
  ./cleanup.sh
```

**Important Notes:**

- **All commands execute as a single bash script** - No parsing/splitting, preserves all bash constructs
- **5-minute timeout (300s)** - Can be customized per test with `setup_timeout` field
- **If any command fails (non-zero exit), the test fails** - Uses `check=True` on subprocess
- **Current working directory is the fixture directory** - All relative paths are relative to fixture
- **Pod diagnostics on failure** - Automatically captures `kubectl describe` and events
- **Captured output** - Both stdout and stderr are logged for debugging

**Custom Timeout:**

```yaml
user_prompt: "Why is the deployment slow?"
expected_output:
  - "The deployment is waiting for image pull"
setup_timeout: 600  # 10 minutes instead of default 5 minutes
before_test: |
  kubectl apply -f large-deployment.yaml
  kubectl wait --for=condition=available deployment/my-app --timeout=600s
```

**Supported Bash Features:**

- ✅ For loops (`for i in {1..10}; do ... done`)
- ✅ While loops (`while condition; do ... done`)
- ✅ If statements (`if [ condition ]; then ... fi`)
- ✅ Heredocs (`cat <<EOF ... EOF`)
- ✅ Line continuations (trailing `\`)
- ✅ Comments and empty lines
- ✅ Environment variables
- ✅ Pipes and redirections

### Test Discovery Customization

By default, `find_test_cases()` returns ALL test cases. To limit during development:

```python
def find_test_cases():
    fixtures_dir = Path(__file__).parent / "fixtures"
    test_case_files = sorted(fixtures_dir.glob("**/test_case.yaml"))

    # Option 1: Return first N tests only
    return [(str(f), Path(f).parent.name) for f in test_case_files[:10]]

    # Option 2: Filter by pattern
    filtered = [f for f in test_case_files if "logs" in str(f)]
    return [(str(f), Path(f).parent.name) for f in filtered]

    # Option 3: Return all (production - default)
    return [(str(f), Path(f).parent.name) for f in test_case_files]
```

**Note:** Each tuple contains `(file_path, test_id)` where `test_id` is the directory name used for pytest test
identification.

### Debugging Failed Tests

Enable debug logging:

```bash
# See all HTTP requests/responses
python -m pytest test_ask_nubi.py -s --log-cli-level=DEBUG --capture=no

# Save output to file
python -m pytest test_ask_nubi.py -s 2>&1 | tee test_output.log --capture=no
```

The test framework logs:

- When fixtures are created/torn down
- Each API request to Nubi
- HTTP status codes and response lengths
- RAGAS evaluation results
- Report generation status

### Custom Evaluation Metrics

To add additional RAGAS metrics, modify `ragas_setup` fixture:

```python
from ragas.metrics import faithfulness, context_precision

@pytest.fixture(scope="session")
def ragas_setup():
    # IMPORTANT: As of Jan 2026, Gemini 1.x models are retired (return 404 errors)
    # Use Gemini 2.5+ models only: gemini-2.5-flash, gemini-2.5-flash-lite, gemini-2.5-pro
    llm = GoogleGenerativeAI(model="gemini-2.5-flash-lite", temperature=0)
    embeddings = GoogleGenerativeAIEmbeddings(model="models/text-embedding-004")

    # Add more metrics here
    metrics = [
        answer_similarity,
        answer_relevancy,
        faithfulness,        # New metric
        context_precision    # New metric
    ]

    return {"metrics": metrics, "llm": llm, "embeddings": embeddings}
```

## Troubleshooting

### Issue: Tests Not Discovered

**Problem:** pytest finds 0 tests

**Solution:**

```bash
# Check fixture directory structure
ls -la fixtures/

# Verify test_case.yaml files exist
find fixtures/ -name "test_case.yaml" | head -5

# Run with verbose test collection
python -m pytest test_ask_nubi.py --collect-only -v --capture=no
```

### Issue: "SYSTEM_FAILURE" Responses

**Problem:** All tests return "SYSTEM_FAILURE"

**Solution:**

1. Check Nubi API is running: `curl http://localhost:9999/health`
2. Verify environment variables: `cat .env`
3. Check API logs for errors
4. Test API manually:

```bash
curl -X POST http://localhost:9999/v1/completions/chat \
  -H "Content-Type: application/json" \
  -H "x-tenant-id: your-tenant" \
  -H "x-user-id: your-user" \
  -d '{"query": "test", "account_id": "your-account", "tenant_id": "your-tenant", "user_id": "your-user"}'
```

### Issue: Report Not Generated

**Problem:** Tests run but no report is created

**Solution:**

- Check for fixture teardown execution: Look for "results_aggregator fixture teardown" in output
- Verify report directory permissions: `ls -la report/`
- Check for Python exceptions during report generation
- The report is generated in the fixture teardown, so ensure all tests complete

### Issue: kubectl Commands Fail

**Problem:** before_test or after_test commands fail

**Solution:**

```bash
# Check kubectl access
kubectl cluster-info

# Verify context
kubectl config current-context

# Test manually from fixture directory
cd fixtures/01_how_many_pods
kubectl apply -f manifests.yaml
```

### Issue: Low Evaluation Scores

**Problem:** Scores consistently below 60%

**Possible Causes:**

1. Expected output doesn't match LLM response style
2. LLM response is verbose while expected output is concise
3. API returning errors consistently
4. Ground truth is too strict

**Solution:**

- Review actual responses in report JSON
- Adjust expected_output to be more flexible
- Use multiple expected outputs for variations
- Check RAGAS metrics settings

## Best Practices

### 1. Naming Conventions

- Use numbered prefixes for ordering: `01_`, `02_`, etc.
- Use descriptive names: `10_image_pull_backoff` not `10_test`
- Use underscores, not hyphens: `pod_crash` not `pod-crash`

### 2. Expected Outputs

- Be specific but not overly prescriptive
- Focus on key information, not exact phrasing
- Provide multiple variants if acceptable
- Avoid expecting exact formatting (line breaks, bullet points, etc.)

### 3. Test Isolation

- Always clean up resources in `after_test`
- Use unique namespaces per test to avoid conflicts
- Don't rely on execution order
- Each test should be independently runnable

### 4. Performance

- Limit `find_test_cases()` during development
- Use `python -m pytest -k` to run subsets
- Consider parallel execution for large test suites (requires isolation)
- Keep `before_test` setup time under 2 minutes

### 5. Maintenance

- Document complex test scenarios in comments
- Keep manifests simple and readable
- Version control all fixtures
- Review and update expected outputs as Nubi improves

## API Reference

### get_llm_ans(query, account_id, tenant_id, user_id)

Sends a query to the Nubi API and returns the response.

**Parameters:**

- `query` (str): The user's question
- `account_id` (str): Account ID from environment
- `tenant_id` (str): Tenant ID from environment
- `user_id` (str): User ID from environment

**Returns:**

- (str): LLM response or "SYSTEM_FAILURE" on error

**Logs:**

- Request being sent
- Response status code
- Response length
- Any errors

### generate_report(results)

Generates evaluation report from collected test results.

**Parameters:**

- `results` (list): List of test result dictionaries

**Output:**

- JSON file in `report/` directory
- Console summary with metrics

**Report Location:**

- `report/nubi_eval_report_YYYYMMDD_timestamp.json`

## Contributing

### Adding New Test Categories

1. Create fixtures with consistent naming
2. Use appropriate tags
3. Document in this README
4. Submit PR with test results

### Modifying Core Framework

- Test changes with existing fixtures first
- Maintain backward compatibility
- Update this documentation
- Run full test suite before committing

## Examples

See the `fixtures/` directory for 145 real-world examples covering:

- Basic queries (counting, resource inspection)
- Debugging scenarios (CrashLoopBackOff, ImagePullBackOff)
- Performance analysis (memory leaks, CPU usage)
- Log analysis (error detection, time-based queries)
- Complex troubleshooting (DNS issues, network policies)
- Integration with observability tools (Prometheus, New Relic, Loki, Tempo)

## FAQ

**Q: Can I run tests in parallel?**
A: Yes, with pytest-xdist, but ensure test isolation (unique namespaces).

**Q: How do I skip certain tests?**
A: Use pytest markers or modify `find_test_cases()` to filter.

**Q: Can I test against different environments?**
A: Yes, change the URL in `get_llm_ans()` or use environment variables.

**Q: What if my test needs multiple API calls?**
A: Currently the framework supports one call per test. For complex scenarios, create multiple test cases.

**Q: How do I debug a failing test?**
A: Run with `-s` flag, check logs, manually run before_test commands, verify API response in report.

## Support

For issues or questions:

1. Check this documentation
2. Review existing fixtures for examples
3. Check test logs with `-s --log-cli-level=DEBUG`
4. Contact the development team