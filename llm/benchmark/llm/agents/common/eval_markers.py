"""Judge-failure marker contract shared by emitters and matchers.

The eval side writes these markers into score_reason when a judge crashes;
the aggregation side (benchmark_server.utils.run_manager) matches them to
keep crash artifacts out of run averages and to count judge_failures. Both
halves import from here — a drifted literal on either side silently turns
the exclusion off, which is exactly the misreporting the markers exist to
prevent. This module must stay dependency-free: the server imports it
without pulling the ragas evaluation stack.
"""

METRIC_FAILED = "[metric_failed]"

SIMILARITY_LABEL = "[Similarity]"
QUALITY_LABEL = "[Quality]"
PLANNER_LABEL = "[Planner]"

SIMILARITY_METRIC_FAILED = f"{SIMILARITY_LABEL} {METRIC_FAILED}"
QUALITY_METRIC_FAILED = f"{QUALITY_LABEL} {METRIC_FAILED}"
PLANNER_METRIC_FAILED = f"{PLANNER_LABEL} {METRIC_FAILED}"
