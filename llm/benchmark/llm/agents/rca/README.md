# Root Cause Analysis (RCA) Benchmark

Integration of [Coroot AI Benchmark](https://coroot.com/ai-benchmark) methodology for evaluating AI-powered root cause analysis capabilities.

## Overview

This benchmark evaluates your AI agent's ability to diagnose production incidents using observability data (logs, metrics, traces) from the OpenTelemetry Demo application.

## Prerequisites

1. **OpenTelemetry Demo deployed** in `nudgebee-demo` namespace ✅
2. **Telemetry collection** configured (Loki, Prometheus, Tempo/Jaeger)
3. **Agent API** running (default: `localhost:9999`)
4. **Environment variables**:
   ```bash
   # Required
   ACCOUNT_ID=<your-account-id>
   TENANT_ID=<your-tenant-id>
   USER_ID=<your-user-id>
   RCA_AGENT_NAME=<your-rca-agent-name>  # e.g., "root_cause_analysis" or "k8s_debug"

   # Optional (with defaults)
   AGENT_API_URL=http://localhost:9999          # Agent API base URL
   AGENT_API_TIMEOUT=120                        # Request timeout in seconds
   RAGAS_LLM_MODEL=gemini-2.5-pro               # LLM for evaluation
   RAGAS_LLM_TEMPERATURE=0                      # LLM temperature (0-1)
   RAGAS_EMBEDDINGS_MODEL=models/text-embedding-004  # Embeddings model
   ```

## Test Scenarios

Based on Coroot's benchmark, we test 7 failure scenarios:

| Scenario | Feature Flag | Issue Type |
|----------|-------------|------------|
| Product Catalog Failure | `productCatalogFailure` | Service crashes |
| Cart Service Failure | `cartFailure` | Service unavailable |
| Recommendation Cache Failure | `recommendationCacheFailure` | Memory leak |
| Ad Service GC | `adManualGc` | GC pauses |
| Homepage Traffic Flood | `loadGeneratorFloodHomepage` | DDoS-like traffic |
| Kafka Queue Lag | `kafkaQueueProblems` | Message queue lag |
| Payment Failure | `paymentFailure` | Payment errors (50%) |

## Running the Benchmark

### Quick Start

```bash
cd llm/benchmark/llm/agents/rca
pytest benchmark_llm_rca.py -m rca -v
```

**Note**: The test will **FAIL** (raise AssertionError) if quality thresholds are not met. This makes it suitable for CI/CD pipelines to catch regressions in RCA performance.

### Run Specific Scenario

```bash
# Edit benchmark_llm_rca.py to filter scenarios
pytest benchmark_llm_rca.py -m rca -k "product_catalog" -v
```

### Environment Setup

```bash
# Set your credentials
export ACCOUNT_ID="your-account-id"
export TENANT_ID="your-tenant-id"
export USER_ID="your-user-id"
export RCA_AGENT_NAME="root_cause_analysis"

# Verify OTel demo is running
kubectl get pods -n nudgebee-demo

# Verify telemetry endpoints
kubectl port-forward -n nudgebee-demo svc/frontend-proxy 8080:8080
```

## How It Works

1. **Trigger Failure**: Enable feature flag in OTel demo
2. **Wait for Telemetry**: Allow 60-90s for data accumulation
3. **Query Agent**: Send investigation query to your RCA agent
4. **Analyze Response**: Extract root cause and verify multi-signal usage
5. **Evaluate**: Compare against expected root cause using RAGAS
6. **Cleanup**: Disable feature flag and stabilize system

## Evaluation Metrics

### RAGAS Metrics
- **Answer Similarity**: Semantic similarity to expected root cause (0-1)
- **Answer Relevancy**: Relevance of response to query (0-1)
- **Overall Accuracy**: Average of similarity and relevancy

### Custom RCA Metrics
- **Signal Coverage**: % of telemetry signals used (logs, metrics, traces)
- **Diagnosis Time**: Time to reach conclusion (seconds)
- **Service Identification**: Did it identify the correct affected service?

### Success Criteria (Enforced via Assertions)

The test will **fail** if any of these criteria are not met:

1. **Overall Accuracy** ≥ 75%
   - Average of answer similarity and answer relevancy

2. **Answer Similarity** ≥ 70%
   - Semantic match to expected root causes

3. **Answer Relevancy** ≥ 70%
   - Response relevance to investigation query

4. **Signal Coverage** ≥ 67%
   - Must use at least 2 out of 3 telemetry signals (logs, metrics, traces)

5. **Success Rate** ≥ 80%
   - At least 9 out of 11 scenarios must complete successfully

These assertions ensure the benchmark acts as a proper automated test in CI/CD pipelines.

## Report Format

Results are saved to `./report/llm_eval_report_rca_YYYYMMDD_timestamp.json`:

```json
{
  "details": [
    {
      "scenario_id": "product_catalog_failure",
      "query": "Users are reporting...",
      "rca_response": "The productcatalogservice is failing...",
      "ground_truth": "The productcatalogservice is failing...",
      "expected_service": "productcatalogservice",
      "signals_used": {
        "logs": true,
        "metrics": true,
        "traces": true
      },
      "signal_coverage": 100.0,
      "diagnosis_time_seconds": 45.2,
      "answer_similarity": 87.5,
      "answer_relevancy": 92.3
    }
  ],
  "summary": {
    "total_scenarios": 7,
    "successful_scenarios": 7,
    "failed_scenarios": 0,
    "answer_similarity": 85.2,
    "answer_relevancy": 89.7,
    "overall_accuracy": 87.45,
    "avg_signal_coverage": 95.2,
    "avg_diagnosis_time_seconds": 42.1,
    "failed_scenario_ids": []
  }
}
```

## Architecture

```
rca/
├── benchmark_llm_rca.py          # Main test file
├── data/
│   └── benchmark_rca_scenarios.json  # Test scenarios
├── report/
│   └── llm_eval_report_rca_*.json    # Results
├── utils/
│   └── feature_flag_controller.py    # K8s flag management
└── README.md                         # This file
```

## Feature Flag Controller

The `FeatureFlagController` manages OTel demo feature flags via kubectl:

```python
from utils.feature_flag_controller import FeatureFlagController

controller = FeatureFlagController(namespace="nudgebee-demo")

# Enable a scenario
controller.enable_flag("productCatalogFailure")
controller.wait_for_telemetry(60)

# Query your agent
# ... perform RCA ...

# Cleanup
controller.disable_flag("productCatalogFailure")
```

## Troubleshooting

### No RCA Response
- Verify agent API is running: `curl http://localhost:9999/health`
- Check agent name in `.env`: `RCA_AGENT_NAME=your_agent_name`
- Ensure agent can access telemetry backends

### Feature Flags Not Working
- Check flagd pod: `kubectl get pods -n nudgebee-demo | grep flagd`
- View flagd logs: `kubectl logs -n nudgebee-demo deployment/flagd`
- Verify config: `kubectl get cm flagd-config -n nudgebee-demo -o yaml`

### Low Signal Coverage
- Agent may not be querying all telemetry sources
- Check agent steps in report to see which tools were called
- Verify Loki/Prometheus/Tempo integrations are working

### Timeouts
- Increase timeout via `AGENT_API_TIMEOUT` environment variable (default: 120s)
- RCA scenarios take longer than simple queries
- Check agent execution logs for bottlenecks

## Using Different Models

### OpenAI Models
```bash
export RAGAS_LLM_MODEL="gpt-4"
export RAGAS_EMBEDDINGS_MODEL="text-embedding-3-large"
pytest benchmark_llm_rca.py -m rca
```

### Anthropic Claude
```bash
export RAGAS_LLM_MODEL="claude-3-5-sonnet-20241022"
# Note: RAGAS may require adapter for Claude
pytest benchmark_llm_rca.py -m rca
```

### Different Gemini Models
```bash
export RAGAS_LLM_MODEL="gemini-1.5-pro"
export RAGAS_LLM_TEMPERATURE=0.2
pytest benchmark_llm_rca.py -m rca
```

### Cost Optimization
```bash
# Use cheaper models for evaluation
export RAGAS_LLM_MODEL="gemini-1.5-flash"
export RAGAS_EMBEDDINGS_MODEL="models/embedding-001"
pytest benchmark_llm_rca.py -m rca
```

## Comparison with Existing Benchmarks

| Aspect | Loki/Trace Benchmarks | RCA Benchmark |
|--------|---------------------|---------------|
| **Scope** | Query generation | End-to-end diagnostics |
| **Input** | Text query | Incident + live telemetry |
| **Output** | LogQL/TraceQL query | Root cause narrative |
| **Evaluation** | Query syntax match | Diagnostic accuracy |
| **Duration** | <1s per query | 30-120s per scenario |
| **Signals** | Single (logs or traces) | Multi-signal (logs + metrics + traces) |

## Next Steps

1. **Customize Agent**: Update `RCA_AGENT_NAME` to match your agent
2. **Add Scenarios**: Create custom failure scenarios in `data/benchmark_rca_scenarios.json`
3. **Tune Thresholds**: Adjust success criteria based on your requirements
4. **CI/CD Integration**: Add to your pipeline with `pytest -m rca`
5. **Baseline**: Run multiple times to establish performance baseline

## References

- [Coroot AI Benchmark](https://coroot.com/ai-benchmark)
- [OpenTelemetry Demo](https://opentelemetry.io/docs/demo/)
- [RAGAS Framework](https://docs.ragas.io/)
