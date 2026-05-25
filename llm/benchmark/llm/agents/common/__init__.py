"""Shared utilities for agent benchmarks — unified nubi format.

Modules:
    fixtures: YAML fixture discovery and loading
    lifecycle: Before/after test hooks, infra scenario resolution
    metrics: Token usage, tool names, planner response fetching
    report: Unified nubi-format report generation
    runner: Core benchmark execution loop (LLM call, retry, RAGAS eval)
    orchestrator_metric: Planner quality rubric scoring
"""
