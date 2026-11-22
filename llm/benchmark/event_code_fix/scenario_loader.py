"""Scenario discovery for the event_code_fix suite.

Deliberately not a change to llm/agents/common/fixtures.py's glob (which every
other suite shares) — this suite's scenario.yaml has a different shape
(alert payload, fixture repo, workload manifest) than test_case.yaml, and
scoping the change here keeps it from touching suites that don't use it.
"""

from __future__ import annotations

from pathlib import Path
from typing import Any

import yaml

from vm_scenario import expand_env


def load_scenarios(
    scenarios_dir: Path, only: set[str] | None = None
) -> list[dict[str, Any]]:
    """Load every scenarios/*/scenario.yaml, sorted by directory name.

    Sorted so runs are reproducible and `--limit` always picks the same
    prefix, same reasoning as swebench's load_instances.
    """
    scenarios = []
    for path in sorted(scenarios_dir.glob("*/scenario.yaml")):
        data = yaml.safe_load(path.read_text())
        data["_dir"] = path.parent
        name = data.get("name") or path.parent.name
        data["name"] = name
        if only and name not in only:
            continue
        if data.get("type") == "vm_event":
            # Unset names stay as `${NAME}`; the run reports them as a setup
            # error, so loading every scenario never needs the VM fleet.
            data = expand_env(data)
        scenarios.append(data)
    return scenarios
