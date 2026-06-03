"""Pytest configuration for all agent benchmark tests."""

import importlib
import importlib.util
import os
import sys
from pathlib import Path


def pytest_addoption(parser):
    """Add custom command line options to control which tests to run."""
    parser.addoption(
        "--max-tests",
        action="store",
        type=int,
        default=None,
        help="Maximum number of tests to run (runs first N tests from test data)",
    )
    parser.addoption(
        "--test-indices",
        action="store",
        type=str,
        default=None,
        help=(
            "Comma-separated test indices to run (0-based). "
            "Supports ranges: '0,2,5-7' runs tests 0, 2, 5, 6, 7"
        ),
    )
    parser.addoption(
        "--account-id",
        action="store",
        default=None,
        help="Filter benchmark queries by account_id",
    )
    parser.addoption(
        "--cc-emails",
        action="store",
        default=None,
        help="Comma-separated CC email addresses for benchmark report",
    )
    parser.addoption(
        "--date-from",
        action="store",
        default=None,
        help="Filter DB conversations from this date (YYYY-MM-DD)",
    )
    parser.addoption(
        "--date-to",
        action="store",
        default=None,
        help="Filter DB conversations up to this date (YYYY-MM-DD)",
    )


def parse_test_indices(indices_str):
    """Parse a string like '0,2,5-7' into a sorted list of ints [0, 2, 5, 6, 7]."""
    result = set()
    for part in indices_str.split(","):
        part = part.strip()
        if "-" in part:
            start, end = part.split("-", 1)
            result.update(range(int(start), int(end) + 1))
        else:
            result.add(int(part))
    return sorted(result)


def pytest_configure(config):
    """Store custom configuration and load agent-specific conftest."""
    max_tests = config.getoption("--max-tests", default=None)
    if max_tests:
        config.max_tests = max_tests

    # Dynamically load agent-specific conftest.py (e.g. aws/azure infra fixtures)
    agent_dir = os.getenv("BENCHMARK_AGENT_DIR", "")
    if not agent_dir:
        return
    agent_conftest = Path(agent_dir) / "conftest.py"
    if not agent_conftest.exists():
        return
    agent_name = Path(agent_dir).name
    mod_name = f"_agent_conftest_{agent_name}"
    if mod_name in sys.modules:
        return
    spec = importlib.util.spec_from_file_location(mod_name, str(agent_conftest))
    mod = importlib.util.module_from_spec(spec)
    sys.modules[mod_name] = mod
    spec.loader.exec_module(mod)
    config.pluginmanager.register(mod, mod_name)
