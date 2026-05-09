"""Fixture-level lifecycle hooks for benchmark test cases.

Handles before_test / after_test shell commands defined in test_case.yaml,
and provides infrastructure resolution from YAML fixtures for session-level
deploy/nuke operations (used by agent-specific conftest.py files).

test_case.yaml lifecycle fields:
    before_test: |              # shell commands to run before the test
        kubectl apply -f scenario.yaml
    after_test: |               # shell commands to run after the test
        kubectl delete -f scenario.yaml
    setup_timeout: 300          # max seconds for before_test (default 300)
    teardown_timeout: 120       # max seconds for after_test (default 120)
    scenario: "H08"             # cloud scenario ID for infra deployment
    type: "command"             # test type: command | rca
    agent: "aws_debug"          # agent override (defaults to parent agent)
"""

import logging
import subprocess
import time
from dataclasses import dataclass
from pathlib import Path
from typing import Any, Dict, List, Optional, Set

from .fixtures import find_test_cases, load_test_case

logger = logging.getLogger(__name__)

DEFAULT_SETUP_TIMEOUT = 300
DEFAULT_TEARDOWN_TIMEOUT = 120


@dataclass
class CommandResult:
    """Result of a command execution."""

    command: str
    test_case_id: str
    success: bool
    exit_code: Optional[int] = None
    elapsed_time: float = 0
    error_type: Optional[str] = None  # 'timeout', 'failure', or None
    error_details: Optional[str] = None
    stdout: Optional[str] = None
    stderr: Optional[str] = None

    @property
    def exit_info(self) -> str:
        """Get formatted exit information."""
        return (
            f"exit {self.exit_code}" if self.exit_code is not None else "no exit code"
        )


def run_before_test(
    test_case: Dict[str, Any], cwd: Optional[str] = None
) -> CommandResult:
    """Execute before_test commands for a fixture.

    Args:
        test_case: Loaded test case dict
        cwd: Working directory for the commands

    Returns:
        CommandResult with execution details
    """
    before_cmd = test_case.get("before_test")
    test_id = test_case.get("__id__", "unknown")
    if not before_cmd:
        return CommandResult(
            command="(no setup needed)", test_case_id=test_id, success=True
        )

    timeout = test_case.get("setup_timeout", DEFAULT_SETUP_TIMEOUT)
    logger.info("[%s] Running before_test (timeout=%ds)", test_id, timeout)
    return run_commands(
        commands=before_cmd,
        cwd=cwd or str(Path(test_case.get("__path__", ".")).parent),
        test_case_id=test_id,
        operation="setup",
        timeout=timeout,
    )


def run_after_test(
    test_case: Dict[str, Any], cwd: Optional[str] = None
) -> CommandResult:
    """Execute after_test commands for a fixture.

    Args:
        test_case: Loaded test case dict
        cwd: Working directory for the commands

    Returns:
        CommandResult with execution details
    """
    after_cmd = test_case.get("after_test")
    test_id = test_case.get("__id__", "unknown")
    if not after_cmd:
        return CommandResult(
            command="(no cleanup needed)", test_case_id=test_id, success=True
        )

    timeout = test_case.get("teardown_timeout", DEFAULT_TEARDOWN_TIMEOUT)
    logger.info("[%s] Running after_test (timeout=%ds)", test_id, timeout)
    return run_commands(
        commands=after_cmd,
        cwd=cwd or str(Path(test_case.get("__path__", ".")).parent),
        test_case_id=test_id,
        operation="cleanup",
        timeout=timeout,
    )


def run_commands(
    commands: str,
    cwd: str,
    test_case_id: str,
    operation: str = "setup",
    timeout: Optional[int] = None,
) -> CommandResult:
    """Execute shell commands as a single bash script.

    Passes the entire script to bash as-is, preserving all bash constructs
    like for loops, if statements, heredocs, line continuations, etc.

    Args:
        commands: The shell script to execute (can be multi-line)
        cwd: Working directory for command execution
        test_case_id: Test case identifier for logging and diagnostics
        operation: Operation type ("setup" or "cleanup") for logging
        timeout: Timeout in seconds (default: 300s for setup, 120s for cleanup)

    Returns:
        CommandResult with execution details
    """
    if not commands:
        return CommandResult(
            command=f"(no {operation} needed)",
            test_case_id=test_case_id,
            success=True,
        )

    start_time = time.time()
    script = commands.strip()
    actual_timeout = timeout if timeout is not None else DEFAULT_SETUP_TIMEOUT

    first_line = script.split("\n")[0][:80]
    logger.info("[%s] Executing %s: %s...", test_case_id, operation, first_line)

    try:
        result = subprocess.run(
            script,
            shell=True,
            cwd=cwd,
            capture_output=True,
            text=True,
            check=True,
            stdin=subprocess.DEVNULL,
            timeout=actual_timeout,
        )

        elapsed_time = time.time() - start_time

        if result.stdout:
            logger.debug("[%s] stdout: %s", test_case_id, result.stdout[:500])
        if result.stderr:
            logger.debug("[%s] stderr: %s", test_case_id, result.stderr[:500])

        logger.info(
            "[%s] %s completed in %.2fs",
            test_case_id,
            operation.capitalize(),
            elapsed_time,
        )

        return CommandResult(
            command=f"{operation.capitalize()}: completed",
            test_case_id=test_case_id,
            success=True,
            elapsed_time=elapsed_time,
            stdout=result.stdout,
            stderr=result.stderr,
        )

    except subprocess.TimeoutExpired as e:
        elapsed_time = time.time() - start_time
        partial_stdout = getattr(e, "stdout", "") or ""
        partial_stderr = getattr(e, "stderr", "") or ""

        extra_diagnostics = ""
        if operation == "setup":
            extra_diagnostics = _get_pod_diagnostics(test_case_id)

        output_section = ""
        if partial_stdout or partial_stderr:
            output_section = (
                f"\n\n=== PARTIAL OUTPUT (before timeout) ===\n"
                f"stdout:\n{partial_stdout}\n\n"
                f"stderr:\n{partial_stderr}"
            )

        truncated_script = _truncate_script(script)
        error_details = (
            f"=== TIMEOUT AFTER {actual_timeout}s ===\n\n"
            f"Script:\n{truncated_script}\n"
            f"{output_section}"
            f"{extra_diagnostics}\n\n"
            f"You can increase timeout by setting 'setup_timeout' in test_case.yaml"
        )

        logger.error("[%s] %s", test_case_id, error_details)

        return CommandResult(
            command=f"{operation.capitalize()} timeout",
            test_case_id=test_case_id,
            success=False,
            elapsed_time=elapsed_time,
            error_type="timeout",
            error_details=error_details,
            stdout=partial_stdout,
            stderr=partial_stderr,
        )

    except subprocess.CalledProcessError as e:
        elapsed_time = time.time() - start_time

        extra_diagnostics = ""
        if operation == "setup":
            extra_diagnostics = _get_pod_diagnostics(test_case_id)

        truncated_script = _truncate_script(script)
        error_details = (
            f"=== COMMAND FAILED ===\n"
            f"Exit code: {e.returncode}\n\n"
            f"Script:\n{truncated_script}\n\n"
            f"=== STDERR ===\n{e.stderr}\n\n"
            f"=== STDOUT ===\n{e.stdout}"
            f"{extra_diagnostics}"
        )

        logger.error("[%s] %s", test_case_id, error_details)

        return CommandResult(
            command=f"{operation.capitalize()} failed",
            test_case_id=test_case_id,
            success=False,
            exit_code=e.returncode,
            elapsed_time=elapsed_time,
            error_type="failure",
            error_details=error_details,
            stdout=e.stdout,
            stderr=e.stderr,
        )

    except Exception as e:
        elapsed_time = time.time() - start_time
        error_details = f"=== UNEXPECTED ERROR ===\n{str(e)}"
        logger.error("[%s] %s", test_case_id, error_details)

        return CommandResult(
            command=f"{operation.capitalize()} failed",
            test_case_id=test_case_id,
            success=False,
            elapsed_time=elapsed_time,
            error_type="failure",
            error_details=error_details,
        )


def _truncate_script(script: str, max_lines: int = 10) -> str:
    """Truncate long scripts for display in error messages."""
    lines = script.strip().split("\n")
    if len(lines) <= max_lines:
        return script
    truncated = (
        lines[:5] + [f"... (truncated - {len(lines) - 8} lines) ..."] + lines[-3:]
    )
    return "\n".join(truncated)


def _get_pod_diagnostics(test_case_id: str) -> str:
    """Get pod and event diagnostics for debugging setup failures."""
    diagnostics = []

    try:
        test_id = test_case_id.split("_")[0] if "_" in test_case_id else test_case_id

        diagnostic_cmd = f"kubectl get pods -A | grep -E '(^NAMESPACE|{test_id})'"
        pod_status_result = subprocess.run(
            diagnostic_cmd,
            shell=True,
            capture_output=True,
            text=True,
            timeout=5,
        )
        if pod_status_result.stdout:
            diagnostics.append(
                f"\n=== POD STATUS (command: {diagnostic_cmd}) ==="
                f"\n{pod_status_result.stdout}"
            )
        else:
            diagnostics.append(
                f"\n=== POD STATUS (command: {diagnostic_cmd}) ==="
                f"\nNo matching pods found"
            )

        ns_cmd = f"kubectl get namespaces | grep -E '{test_id}' | awk '{{print $1}}'"
        ns_result = subprocess.run(
            ns_cmd, shell=True, capture_output=True, text=True, timeout=5
        )

        namespaces_found = (
            ns_result.stdout.strip().split("\n") if ns_result.stdout.strip() else []
        )

        if namespaces_found:
            for namespace in namespaces_found:
                if namespace:
                    events_cmd = (
                        f"kubectl get events -n {namespace}"
                        f" --sort-by='.lastTimestamp' | tail -20"
                    )
                    events_result = subprocess.run(
                        events_cmd,
                        shell=True,
                        capture_output=True,
                        text=True,
                        timeout=5,
                    )
                    if events_result.stdout:
                        diagnostics.append(
                            f"\n=== RECENT EVENTS in {namespace}"
                            f" (command: {events_cmd}) ==="
                            f"\n{events_result.stdout}"
                        )
        else:
            namespace = f"app-{test_id}" if test_id.isdigit() else f"test-{test_id}"
            diagnostics.append(
                f"\n=== NAMESPACES ===\nNo namespaces found matching"
                f" pattern '{test_id}' (tried default: {namespace})"
            )

        return "\n".join(diagnostics)

    except Exception as e:
        return f"\n\n=== DIAGNOSTICS ERROR ===\nFailed to get pod diagnostics: {e}"


# ---------------------------------------------------------------------------
# Infrastructure resolution from YAML fixtures
# ---------------------------------------------------------------------------


def resolve_scenarios_from_fixtures(
    fixtures_dir: Path,
    test_indices: Optional[str] = None,
    max_tests: Optional[int] = None,
    tag_filter: Optional[str] = None,
) -> List[str]:
    """Resolve which cloud scenarios need to be deployed based on YAML fixtures.

    Reads the fixtures directory, applies test selection filters, and collects
    unique scenario IDs from RCA-type test cases.

    Args:
        fixtures_dir: Path to the agent's fixtures/ directory
        test_indices: Optional test index filter (e.g. "0,2,5-7")
        max_tests: Optional max tests limit
        tag_filter: Optional comma-separated tags to filter by

    Returns:
        Sorted list of unique scenario IDs needed for the selected tests
    """
    test_cases = find_test_cases(
        fixtures_dir, max_tests=max_tests, test_indices=test_indices,
        tag_filter=tag_filter,
    )
    if not test_cases:
        logger.warning(
            "No test cases found in %s for scenario resolution", fixtures_dir
        )
        return []

    scenarios: Set[str] = set()
    for file_path, test_id, _gi in test_cases:
        tc = load_test_case(file_path)
        scenario = tc.get("scenario")
        if scenario:
            scenarios.add(scenario)

    result = sorted(scenarios)
    if result:
        logger.info(
            "Resolved %d scenarios from %d fixtures: %s",
            len(result),
            len(test_cases),
            result,
        )
    else:
        logger.info("No scenario fields found in %d fixtures", len(test_cases))
    return result


def get_rca_fixtures(
    fixtures_dir: Path,
    test_indices: Optional[str] = None,
    max_tests: Optional[int] = None,
) -> List[Dict[str, Any]]:
    """Load all RCA-type fixtures that require infrastructure.

    Returns:
        List of loaded test cases that have type="rca" or a scenario field
    """
    test_cases = find_test_cases(
        fixtures_dir, max_tests=max_tests, test_indices=test_indices
    )
    rca_cases = []
    for file_path, test_id, _gi in test_cases:
        tc = load_test_case(file_path)
        if tc.get("type") == "rca" or tc.get("scenario"):
            rca_cases.append(tc)
    return rca_cases


def needs_infrastructure(fixtures_dir: Path) -> bool:
    """Check if any fixture in the directory requires infrastructure deployment."""
    test_cases = find_test_cases(fixtures_dir)
    for file_path, test_id, _gi in test_cases:
        tc = load_test_case(file_path)
        if tc.get("scenario") or tc.get("before_test"):
            return True
    return False
