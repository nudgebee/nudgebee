"""Azure benchmark infrastructure lifecycle fixture (session-scoped).

Usage:
    # Deploy all scenarios, run benchmarks, then delete resource groups:
    pytest --deploy-infra llm/agents/azure/

    # Deploy but keep infrastructure after tests (for debugging):
    pytest --deploy-infra --skip-nuke llm/agents/azure/

    # Run against already-deployed infrastructure (no deploy/delete):
    pytest llm/agents/azure/
"""

import logging
import os
import subprocess
import sys
from pathlib import Path

import pytest

from llm.agents.common.lifecycle import resolve_scenarios_from_fixtures

logger = logging.getLogger(__name__)

_AZURE_TEST_DIR = Path(__file__).parent / "azure-agent-test"
_DEPLOY_SH = _AZURE_TEST_DIR / "scripts" / "deploy.sh"
_FIXTURES_DIR = Path(__file__).parent / "fixtures"

# Fallback: auto-discover all scenarios from the scenarios directory
_ALL_SCENARIOS = sorted(p.stem for p in (_AZURE_TEST_DIR / "scenarios").rglob("*.json"))


def _run(cmd, description, check=True, extra_env=None):
    """Run a shell command, streaming output to stdout."""
    logger.info("==> %s", description)
    logger.info("    cmd: %s", " ".join(str(c) for c in cmd))
    return subprocess.run(
        cmd,
        check=check,
        text=True,
        stdout=sys.stdout,
        stderr=sys.stderr,
        env={**os.environ, **(extra_env or {})},
    )


@pytest.fixture(scope="session", autouse=True)
def azure_infrastructure(request):
    """Session-scoped fixture that deploys Azure scenarios before tests and deletes after.

    Controlled by CLI flags:
      --deploy-infra  Deploy bootstrap + required scenarios in Broken mode.
      --skip-nuke     Skip the cleanup step after tests (useful for debugging).

    If --deploy-infra is not set, the fixture is a no-op (assumes infra exists).
    Resolves required scenarios from YAML fixtures based on --test-indices / --max-tests.
    """
    deploy = os.getenv("DEPLOY_INFRA", "").lower() in ("1", "true", "yes")
    skip_nuke = os.getenv("SKIP_NUKE", "").lower() in ("1", "true", "yes")

    if not deploy:
        logger.info("--deploy-infra not set; skipping Azure infrastructure deployment")
        yield
        return

    # Resolve scenarios from YAML fixtures
    test_indices_str = os.getenv("TEST_INDICES")
    max_tests_str = os.getenv("MAX_TESTS")
    max_tests = int(max_tests_str) if max_tests_str else None
    tag_filter = os.getenv("TAG_FILTER")

    scenarios = resolve_scenarios_from_fixtures(
        _FIXTURES_DIR,
        test_indices=test_indices_str,
        max_tests=max_tests,
        tag_filter=tag_filter,
    )
    if not scenarios:
        logger.info("No scenarios found in fixtures — deploying all scenarios")
        scenarios = _ALL_SCENARIOS

    location = os.getenv("AZURE_LOCATION", "eastus")
    subscription = os.getenv(
        "AZURE_SUBSCRIPTION", "19e207a9-769d-4afd-b261-10bbed2d43e8"
    )
    extra_env = {"AZURE_LOCATION": location, "AZURE_SUBSCRIPTION": subscription}

    # ---- SETUP ----
    logger.info(
        "=== Deploying Azure benchmark infrastructure (location: %s) ===", location
    )
    logger.info("    Scenarios to deploy: %s", scenarios)

    # 1. Bootstrap (shared App Service Plan) — always needed
    _run(
        ["bash", str(_DEPLOY_SH), "--bootstrap"],
        "Deploying Azure bootstrap resources",
        extra_env=extra_env,
    )

    # 2. Deploy only the required RCA scenarios in Broken mode
    failed_deploys = []
    for scenario in scenarios:
        try:
            _run(
                ["bash", str(_DEPLOY_SH), scenario, "Broken"],
                f"Deploying scenario {scenario} (Broken mode)",
                extra_env=extra_env,
            )
        except subprocess.CalledProcessError:
            logger.error(
                "Failed to deploy scenario %s — continuing with others", scenario
            )
            failed_deploys.append(scenario)

    if failed_deploys:
        logger.warning(
            "The following scenarios failed to deploy: %s. Tests may fail.",
            failed_deploys,
        )

    logger.info("=== Azure infrastructure deployment complete ===")

    # ---- YIELD (tests run here) ----
    yield

    # ---- TEARDOWN ----
    if skip_nuke:
        logger.info("--skip-nuke set; leaving Azure infrastructure in place")
        return

    logger.info("=== Deleting Azure benchmark infrastructure ===")

    delete_errors = []
    for scenario in scenarios:
        try:
            _run(
                ["bash", str(_DEPLOY_SH), "--delete", scenario],
                f"Deleting scenario {scenario} resource group",
                check=False,
                extra_env=extra_env,
            )
        except Exception as e:
            logger.error("Error deleting scenario %s: %s", scenario, e)
            delete_errors.append(scenario)

    # Delete bootstrap last
    try:
        _run(
            ["bash", str(_DEPLOY_SH), "--delete-bootstrap"],
            "Deleting Azure bootstrap resource group",
            check=False,
            extra_env=extra_env,
        )
    except Exception as e:
        logger.error("Error deleting bootstrap: %s", e)

    if delete_errors:
        logger.warning(
            "Some resource groups may not have been deleted: %s. "
            "Manual cleanup may be needed.",
            delete_errors,
        )
    else:
        logger.info("=== Azure infrastructure cleanup initiated ===")
        logger.info("    Note: Azure deletes resource groups asynchronously.")
