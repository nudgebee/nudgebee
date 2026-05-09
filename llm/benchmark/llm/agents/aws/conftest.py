"""AWS benchmark infrastructure lifecycle fixture (session-scoped).

Usage:
    # Deploy all scenarios, run benchmarks, then nuke:
    pytest --deploy-infra llm/agents/aws/

    # Deploy but keep infrastructure after tests (for debugging):
    pytest --deploy-infra --skip-nuke llm/agents/aws/

    # Run against already-deployed infrastructure (no deploy/nuke):
    pytest llm/agents/aws/
"""

import logging
import os
import subprocess
import sys
from pathlib import Path

import pytest

from llm.agents.common.lifecycle import resolve_scenarios_from_fixtures

logger = logging.getLogger(__name__)

_AWS_TEST_DIR = Path(__file__).parent / "aws-agent-test"
_DEPLOY_SH = _AWS_TEST_DIR / "scripts" / "deploy.sh"
_NUKE_SH = _AWS_TEST_DIR / "scripts" / "nuke.sh"
_FIXTURES_DIR = Path(__file__).parent / "fixtures"

# Fallback: auto-discover all scenarios from the scenarios directory
_ALL_SCENARIOS = sorted(p.stem for p in (_AWS_TEST_DIR / "scenarios").rglob("*.yaml"))


def _run(cmd, description, check=True):
    """Run a shell command, streaming output to stdout."""
    logger.info("==> %s", description)
    logger.info("    cmd: %s", " ".join(str(c) for c in cmd))
    return subprocess.run(
        cmd,
        check=check,
        text=True,
        stdout=sys.stdout,
        stderr=sys.stderr,
    )


@pytest.fixture(scope="session", autouse=True)
def aws_infrastructure(request):
    """Session-scoped fixture that deploys AWS scenarios before tests and nukes after.

    Controlled by CLI flags:
      --deploy-infra  Deploy bootstrap + required scenarios in Broken mode.
      --skip-nuke     Skip the nuke step after tests (useful for debugging).

    If --deploy-infra is not set, the fixture is a no-op (assumes infra exists).
    Resolves required scenarios from YAML fixtures based on --test-indices / --max-tests.
    """
    deploy = os.getenv("DEPLOY_INFRA", "").lower() in ("1", "true", "yes")
    skip_nuke = os.getenv("SKIP_NUKE", "").lower() in ("1", "true", "yes")

    if not deploy:
        logger.info("--deploy-infra not set; skipping AWS infrastructure deployment")
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

    region = os.getenv("AWS_REGION", "us-east-1")

    # ---- SETUP ----
    logger.info("=== Deploying AWS benchmark infrastructure (region: %s) ===", region)
    logger.info("    Scenarios to deploy: %s", scenarios)

    # 1. Bootstrap (shared VPC, S3 bucket) — always needed
    _run(["bash", str(_DEPLOY_SH), "--bootstrap"], "Deploying AWS bootstrap stack")

    # 2. Deploy only the required RCA scenarios in Broken mode
    failed_deploys = []
    for scenario in scenarios:
        try:
            _run(
                ["bash", str(_DEPLOY_SH), scenario, "Broken"],
                f"Deploying scenario {scenario} (Broken mode)",
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

    logger.info("=== AWS infrastructure deployment complete ===")

    # ---- YIELD (tests run here) ----
    yield

    # ---- TEARDOWN ----
    if skip_nuke:
        logger.info("--skip-nuke set; leaving AWS infrastructure in place")
        return

    logger.info("=== Nuking AWS benchmark infrastructure ===")
    try:
        subprocess.run(
            ["bash", str(_NUKE_SH)],
            input="y\n",
            check=True,
            text=True,
            stdout=sys.stdout,
            stderr=sys.stderr,
            env={**os.environ, "AWS_REGION": region},
        )
        logger.info("=== AWS infrastructure nuke complete ===")
    except subprocess.CalledProcessError as e:
        logger.error(
            "Nuke failed (exit %s); manual cleanup may be needed", e.returncode
        )
