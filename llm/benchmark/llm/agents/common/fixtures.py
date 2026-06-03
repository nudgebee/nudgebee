"""YAML fixture discovery and loading for agent benchmarks.

Each agent stores test cases as individual YAML files under a fixtures/ directory:

    agents/<agent>/fixtures/
    ├── 001_cluster_info/
    │   └── test_case.yaml
    ├── 002_list_pods/
    │   └── test_case.yaml
    └── ...

test_case.yaml format:
    user_prompt: "give me cluster information"
    expected_output:
      - "kubectl cluster-info"
    tags:
      - command
      - easy
    before_test: |          # optional
      kubectl apply -f manifests.yaml
    after_test: |            # optional
      kubectl delete -f manifests.yaml
    setup_timeout: 300       # optional (default 300s)
"""

import logging
from pathlib import Path
from typing import Any, Dict, List, Optional, Tuple

import yaml

logger = logging.getLogger(__name__)


def find_test_cases(
    fixtures_dir: Path,
    max_tests: Optional[int] = None,
    test_indices: Optional[str] = None,
    skip_indices: Optional[str] = None,
    tag_filter: Optional[str] = None,
) -> List[Tuple[str, str, int]]:
    """Discover test cases from a fixtures directory.

    Args:
        fixtures_dir: Path to the fixtures/ directory
        max_tests: Limit to first N tests (sorted alphabetically)
        test_indices: Specific indices to run, e.g. "0,2,5-7"
        skip_indices: Indices to skip (for resume), e.g. "0,1,3"
        tag_filter: Comma-separated tags to filter by (test must have at least one)

    Returns:
        List of (file_path, test_id, global_index) tuples.
        global_index is the position in the full sorted fixture list (stable across
        resume/rerun). Used as test_index when storing results to DB.
    """
    if not fixtures_dir.exists():
        logger.warning("Fixtures directory not found: %s", fixtures_dir)
        return []

    test_case_files = sorted(fixtures_dir.glob("**/test_case.yaml"))
    all_cases = [(str(f), Path(f).parent.name, i) for i, f in enumerate(test_case_files)]

    # Filter by tags before applying index-based selection
    if tag_filter:
        required_tags = {t.strip().lower() for t in tag_filter.split(",") if t.strip()}
        if required_tags:
            filtered = []
            for fp, tid, gi in all_cases:
                try:
                    with open(fp, "r") as f:
                        tc = yaml.safe_load(f)
                    tc_tags = {t.lower() for t in (tc or {}).get("tags", [])}
                except Exception:
                    tc_tags = set()
                if tc_tags & required_tags:
                    filtered.append((fp, tid, gi))
            logger.info(
                "Tag filter %s: %d/%d tests match", required_tags, len(filtered), len(all_cases)
            )
            all_cases = filtered

    if test_indices:
        idx_set = _parse_indices(test_indices)
        # Match against global_index (stable position in full fixture list),
        # not list position — consistent with skip_indices and DB storage
        cases = [(fp, tid, gi) for fp, tid, gi in all_cases if gi in idx_set]
        logger.info(
            "Running test indices %s: %d match out of %d",
            sorted(idx_set), len(cases), len(all_cases),
        )
    elif max_tests and max_tests > 0:
        logger.info(
            "Limiting to first %d tests out of %d total", max_tests, len(all_cases)
        )
        cases = all_cases[:max_tests]
    else:
        cases = all_cases

    # Skip already-completed indices (used for resume).
    # skip_indices refers to global_index values (position in full fixture list).
    if skip_indices:
        skip_set = _parse_indices(skip_indices)
        before = len(cases)
        cases = [(fp, tid, gi) for fp, tid, gi in cases if gi not in skip_set]
        logger.info(
            "Skipping %d completed test(s), %d remaining",
            before - len(cases),
            len(cases),
        )

    return cases


def load_test_case(file_path: str) -> Dict[str, Any]:
    """Load and validate a test_case.yaml file.

    Returns:
        Dictionary with keys: user_prompt, expected_output, tags, before_test,
        after_test, setup_timeout, __path__, __id__
    """
    with open(file_path, "r") as f:
        test_case = yaml.safe_load(f)

    test_case["__path__"] = file_path
    test_case["__id__"] = Path(file_path).parent.name

    # Normalize user_prompt to string (handles YAML list syntax)
    prompt = test_case.get("user_prompt", "")
    if isinstance(prompt, list):
        test_case["user_prompt"] = " ".join(str(p) for p in prompt)

    # Normalize expected_output to list
    expected = test_case.get("expected_output", [])
    if isinstance(expected, str):
        test_case["expected_output"] = [expected]

    # Ensure tags is a list
    if "tags" not in test_case:
        test_case["tags"] = []

    return test_case


def collect_tags(fixtures_dir: Path) -> List[str]:
    """Collect all unique tags from test cases in a fixtures directory."""
    if not fixtures_dir.exists():
        return []
    tags = set()
    for f in fixtures_dir.glob("**/test_case.yaml"):
        try:
            with open(f, "r") as fh:
                tc = yaml.safe_load(fh)
            if tc:
                for t in tc.get("tags", []):
                    tags.add(t)
        except Exception:
            logger.warning("Skipping malformed YAML: %s", f)
    return sorted(tags)


def _parse_indices(indices_str: str) -> set:
    """Parse index string like '0,2,5-7' into {0, 2, 5, 6, 7}."""
    idx_set = set()
    for part in indices_str.split(","):
        part = part.strip()
        if "-" in part:
            start, end = part.split("-", 1)
            idx_set.update(range(int(start), int(end) + 1))
        else:
            idx_set.add(int(part))
    return idx_set
