import asyncio
import json
import logging
import os
import sys

os.environ["RAGAS_DO_NOT_TRACK"] = "true"
import re
import signal
import time
from datetime import datetime, timedelta, timezone
from pathlib import Path
from typing import List, Optional, Union

from fastapi import APIRouter, BackgroundTasks, Depends, HTTPException
from pydantic import BaseModel

from benchmark_server.middleware.auth import (
    AuthUser,
    get_authz,
    get_current_user,
)
from benchmark_server.utils import run_manager
from benchmark_server.utils.db_utils import get_user_email
from benchmark_server.utils.email_utils import send_email_async
from benchmark_server.utils.email_templates import build_benchmark_email

router = APIRouter(prefix="/agent-benchmark", tags=["agent-benchmark"])
logger = logging.getLogger(__name__)

# Track running subprocess PIDs by run_id so stop can kill them
_active_processes: dict[str, asyncio.subprocess.Process] = {}


def _register_process(run_id: str, process: asyncio.subprocess.Process):
    _active_processes[run_id] = process


def _unregister_process(run_id: str):
    _active_processes.pop(run_id, None)


def _kill_process(key: str):
    """Kill the subprocess for a given key if it's still active."""
    process = _active_processes.pop(key, None)
    if not process:
        logger.debug("No process found for key '%s'", key)
        return
    if process.returncode is not None:
        logger.debug("Process for '%s' already exited (rc=%d)", key, process.returncode)
        return
    try:
        pgid = os.getpgid(process.pid)
        # SIGTERM first, then SIGKILL to ensure child processes die
        os.killpg(pgid, signal.SIGTERM)
        logger.info(
            "Sent SIGTERM to process group for %s (pid %d, pgid %d)",
            key,
            process.pid,
            pgid,
        )
        # Give it a moment then force kill
        try:
            os.waitpid(process.pid, os.WNOHANG)
        except ChildProcessError:
            pass
        os.killpg(pgid, signal.SIGKILL)
        logger.info(
            "Sent SIGKILL to process group for %s (pid %d, pgid %d)",
            key,
            process.pid,
            pgid,
        )
    except (ProcessLookupError, OSError) as e:
        logger.debug("Process for '%s' already gone: %s", key, e)


def _kill_all_for_run(run_id: str):
    """Kill the main process and all single-test processes for a run."""
    keys = [k for k in list(_active_processes.keys()) if k == run_id or k.startswith(f"{run_id}__")]
    for key in keys:
        _kill_process(key)


def _kill_by_prefix(prefix: str):
    """Kill all processes whose key starts with the given prefix."""
    all_keys = list(_active_processes.keys())
    keys = [k for k in all_keys if k.startswith(prefix)]
    if not keys:
        logger.warning("No active processes matching prefix '%s' (active: %s)", prefix, all_keys)
        return
    for key in keys:
        _kill_process(key)
    logger.info("Killed %d process(es) matching prefix '%s'", len(keys), prefix)


# Unified benchmark test file — all agents use the same file
_UNIFIED_BENCHMARK = (
    Path(__file__).resolve().parent.parent.parent / "llm" / "agents" / "common" / "benchmark.py"
)


# ---------------------------------------------------------------------------
# Authorization helpers
# ---------------------------------------------------------------------------
# Authentication (who) lives in the JWT — see middleware/auth.py.
# Authorization (what they can do) lives in the Authz object fetched per
# request from middleware/authz.py and cached in-process by user_id.
# These helpers operate against an Authz alone — there is no "current
# tenant" cookie. Per-request tenant comes from the request payload
# (Run Benchmark) or is derived from the run's tenant_id (Run-scoped
# routes); both are validated against the user's authz tenant set.
#
# Three role tiers:
#   - regular user     — sees only the tenants they're a member of
#   - tenant_admin     — admin within a specific tenant
#   - super_admin      — system-wide; sees and acts across all tenants


def _verify_run_access(run_id: str, authz) -> None:
    """Raise 404 if the run does not exist; 403 if the caller can't reach it.

    super_admin bypasses tenant checks; everyone else must have explicit
    membership in the run's owning tenant (regardless of role within it —
    tenant_admin and user are both "in" the tenant).
    """
    tenant_id = run_manager.get_run_tenant_id(run_id)
    if tenant_id is None:
        raise HTTPException(status_code=404, detail=f"Run {run_id} not found")
    if authz.is_super_admin:
        return
    if not tenant_id or not authz.has_tenant(tenant_id):
        raise HTTPException(
            status_code=403,
            detail="You do not have access to this run.",
        )


def _require_admin(authz) -> None:
    """Raise 403 unless the caller is super_admin OR tenant_admin in any
    tenant they're a member of.

    Used to gate cluster-level operations (``/infra/*``) — infra state is
    shared cluster scope, not per-tenant, so any operator-level role is
    enough. Regular users are blocked.
    """
    if authz.is_super_admin:
        return
    for ta in authz.tenants:
        if ta.role == "tenant_admin":
            return
    raise HTTPException(
        status_code=403,
        detail="Admin role required for this operation.",
    )


def _scope_tenant_ids(authz) -> Optional[list[str]]:
    """Return the tenant_id list the caller's list-queries should filter on.

    super_admin: ``None`` — no filter, sees runs across every tenant.
    Others: list of every tenant the user is a member of (from authz).
    Empty list (a user with no tenants) means "match nothing".
    """
    if authz.is_super_admin:
        return None
    return authz.tenant_ids()


def _resolve_run_identity(user: AuthUser, authz, request) -> tuple[str, str, str]:
    """Resolve (user_id, tenant_id, account_id) for a benchmark-run request.

    Trust model:
      - ``user_id``: always the logged-in user. The user dropdown was
        removed from the UI; benchmarks run as the logged-in user period.
        ``request.user_id`` is silently ignored.
      - ``tenant_id``: must be supplied by the request (the form's
        tenant dropdown). Validated against the user's authz — must
        be a tenant they're a member of (super_admin: any tenant).
      - ``account_id``: must be present in the request, AND (for non-
        super-admin) must be in the caller's allowed account list for
        the resolved tenant.
    """
    user_id = user.user_id

    tenant_id = (getattr(request, "tenant_id", None) or "").strip()
    if not tenant_id:
        raise HTTPException(
            status_code=400,
            detail="tenant_id is required. Pick a tenant in the form.",
        )
    if not authz.is_super_admin and not authz.has_tenant(tenant_id):
        raise HTTPException(
            status_code=403,
            detail="You do not have access to the selected tenant.",
        )

    account_id = (request.account_id or "").strip()
    if not account_id:
        raise HTTPException(
            status_code=400,
            detail="account_id is required. Pick an account in the form.",
        )

    # Validate account access for non-super-admin. The user's allowed list
    # comes from authz, scoped to the resolved tenant.
    if not authz.is_super_admin:
        ta = authz.tenant(tenant_id)
        if ta is None:
            raise HTTPException(
                status_code=403,
                detail="You do not have access to the selected tenant.",
            )
        if ta.account_ids and account_id not in ta.account_ids:
            raise HTTPException(
                status_code=403,
                detail="You do not have access to the selected account.",
            )

    return user_id, tenant_id, account_id


class AgentConfig(BaseModel):
    name: str  # aws, kubectl, rca, loki, promql, trace, etc.
    tool_config: Optional[str] = None  # dev-new, aws-prod, etc. (optional)


class AgentBenchmarkRequest(BaseModel):
    account_id: Optional[str] = None  # Optional — falls back to auth context
    user_id: Optional[str] = None  # Optional — falls back to auth context
    tenant_id: Optional[str] = None  # Optional — falls back to auth context
    agent: Union[
        str, AgentConfig
    ]  # Support both "aws" string and {"name": "aws", "tool_config": "dev-new"}
    max_tests: Optional[int] = None  # Limit number of tests to run (e.g., 1, 5, 10)
    test_indices: Optional[str] = None  # Run specific tests: "0,2,5-7" (0-based)
    tag_filter: Optional[str] = None  # Comma-separated tags to filter tests by
    test_filter: Optional[str] = None  # pytest -k filter expression (nubi only)
    cc_emails: Optional[List[str]] = None  # Additional email recipients for the report
    parallel_workers: Optional[int] = (
        None  # Number of parallel test workers (default from env or 2)
    )
    run_name: Optional[str] = None  # Human-readable name for the run


class GatherBenchmarkRequest(BaseModel):
    account_id: Optional[str] = None  # Optional — falls back to auth context
    user_id: Optional[str] = None  # Optional — falls back to auth context
    tenant_id: Optional[str] = None  # Optional — falls back to auth context
    agent: Union[str, AgentConfig]
    max_tests: Optional[int] = None
    test_indices: Optional[str] = None
    tag_filter: Optional[str] = None
    test_filter: Optional[str] = None
    cc_emails: Optional[List[str]] = None


USE_ORCHESTRATOR = os.getenv("USE_ORCHESTRATOR", "false").lower() == "true"


async def run_agent_benchmark_and_notify(  # noqa: C901
    user_id: str,
    account_id: str,
    tenant_id: str,
    agent: str,
    run_id: str,
    tool_config: Optional[str] = None,
    max_tests: Optional[int] = None,
    test_indices: Optional[str] = None,
    test_filter: Optional[str] = None,
    tag_filter: Optional[str] = None,
    cc_emails: Optional[List[str]] = None,
    parallel_workers: Optional[int] = None,
    skip_indices: Optional[str] = None,
):
    """
    Runs the pytest benchmark for a specific agent and sends the report via email.
    """
    if not re.match(r"^[a-z0-9_-]+$", agent):
        logger.error(f"Invalid agent name: {agent}")
        run_manager.fail_run(run_id, f"Invalid agent name: {agent}")
        return

    logger.info(f"Starting {agent} agent benchmark for user {user_id} (account: {account_id})")

    benchmark_trigger_time = datetime.now()

    # Fetch user email from DB
    email = get_user_email(user_id)
    if not email:
        logger.error(
            f"Could not find email for user_id: {user_id}. Aborting benchmark notification."
        )
        run_manager.fail_run(run_id, f"Email not found for user_id: {user_id}")
        return

    cc_list = ", ".join(cc_emails) if cc_emails else ""
    logger.info("Report will be sent to: %s, %s", email, cc_list)

    # Define paths
    project_root = Path(__file__).resolve().parent.parent.parent
    agent_dir = project_root / "llm" / "agents" / agent
    config_file = agent_dir / "config.yaml"

    # Validate agent by checking if config.yaml exists
    if not config_file.exists():
        agents_dir = project_root / "llm" / "agents"
        available_agents = [
            d.name
            for d in sorted(agents_dir.iterdir())
            if d.is_dir() and (d / "config.yaml").exists()
        ]
        error_msg = f"config.yaml not found for agent: {agent}<br>Expected path: {config_file}"
        if available_agents:
            error_msg += f"<br><br>Available agents: {', '.join(available_agents)}"

        logger.error(f"config.yaml not found: {config_file}")
        run_manager.fail_run(run_id, f"config.yaml not found for agent: {agent}")
        await send_email_async(
            email,
            "Benchmark Setup Failed",
            error_msg,
            cc_emails=cc_emails,
        )
        return

    # --- Orchestrator path (no pytest) ---
    if USE_ORCHESTRATOR:
        try:
            from benchmark_server.orchestrator import TestOrchestrator

            default_workers = int(os.getenv("BENCHMARK_PARALLEL_WORKERS", "2"))
            workers = (
                parallel_workers if parallel_workers is not None else default_workers
            )
            orchestrator = TestOrchestrator(parallel_workers=workers)

            logger.info(
                "Using custom orchestrator (workers=%d) instead of pytest", workers
            )
            success = await asyncio.to_thread(
                orchestrator.run,
                agent_dir=str(agent_dir),
                run_id=run_id,
                account_id=account_id,
                tenant_id=tenant_id,
                user_id=user_id,
                max_tests=max_tests,
                test_indices=test_indices,
                tag_filter=tag_filter,
                skip_indices=skip_indices,
                tool_config=tool_config,
            )

            # Report & email (same as pytest path)
            run_manager.set_phase(run_id, run_manager.RunPhase.SENDING_REPORT)
            # complete_run does sync DB + HTTP I/O via reconcile_waiting_tests.
            # Off-load to a worker thread so the event loop stays free.
            is_complete = await asyncio.to_thread(run_manager.complete_run, run_id)
            if not is_complete:
                logger.info(
                    "Run %s has tests waiting for followups, skipping report email",
                    run_id,
                )
                return

            report_data = run_manager.get_report(run_id)
            if not report_data:
                run_manager.fail_run(run_id, "No test results stored")
                await send_email_async(
                    email,
                    f"{agent.upper()} Benchmark - No Results",
                    "No test results recorded. Check logs.",
                    cc_emails=cc_emails,
                )
                return

            summary = report_data.get("summary", {})
            email_body = build_benchmark_email(
                report_data,
                display_date=benchmark_trigger_time.strftime("%B %d, %Y at %H:%M UTC"),
            )

            import tempfile

            report_file = os.path.join(
                tempfile.gettempdir(), f"benchmark_report_{agent}_{run_id}.json"
            )
            with open(report_file, "w") as f:
                json.dump(report_data, f, indent=2)

            run_label = report_data.get("metadata", {}).get("run_name") or agent.upper()
            await send_email_async(
                email,
                f"{run_label} Benchmark Report - {summary.get('overall_accuracy', 'N/A')}% Accuracy",
                email_body,
                attachments=[report_file],
                cc_emails=cc_emails,
            )

            try:
                os.unlink(report_file)
            except OSError:
                pass

            logger.info(
                "Orchestrator: completed %s benchmark, emailed %s", agent, email
            )
            return

        except Exception as e:
            logger.exception("Orchestrator failed for %s", agent)
            run_manager.fail_run(run_id, f"Orchestrator error: {e}")
            await send_email_async(
                email,
                f"{agent.upper()} Benchmark Error",
                f"Orchestrator error: {e}",
                cc_emails=cc_emails,
            )
            return

    # --- Pytest path (legacy) ---
    # Construct pytest command — unified benchmark file for all agents
    # confcutdir picks up llm/agents/conftest.py (shared CLI options)
    # Agent-specific conftest (e.g. aws/azure infra) is loaded dynamically
    # by benchmark.py's pytest_configure hook via BENCHMARK_AGENT_DIR env var
    agents_dir = agent_dir.parent  # llm/agents/
    cmd = [
        "python",
        "-m",
        "pytest",
        "-s",
        str(_UNIFIED_BENCHMARK),
        "-v",
        "--log-cli-level=INFO",
        "--confcutdir",
        str(agents_dir),
        "-m",
        "benchmark",
    ]

    # Add pytest -k filter expression if provided
    if test_filter:
        cmd.extend(["-k", test_filter])

    # Add max_tests option if provided
    if max_tests is not None:
        cmd.extend(["--max-tests", str(max_tests)])

    # Add test_indices option if provided (overrides max_tests)
    if test_indices is not None:
        cmd.extend(["--test-indices", test_indices])

    # Prepare environment variables
    run_env = os.environ.copy()
    env_vars = {
        "ACCOUNT_ID": account_id,
        "USER_ID": user_id,
        "TENANT_ID": tenant_id,
        "PYTHONUNBUFFERED": "1",
        "BENCHMARK_RUN_ID": run_id,
        "BENCHMARK_AGENT_DIR": str(agent_dir),
    }

    # Check if infra covers the required scenarios for this run
    infra_state = _get_infra_state(agent)
    if infra_state and infra_state.get("status") == "deployed":
        deployed_scenarios = set(infra_state.get("scenarios") or [])
        # Resolve what this run needs
        try:
            from llm.agents.common.lifecycle import resolve_scenarios_from_fixtures

            agent_dir = Path(__file__).resolve().parent.parent.parent / "llm" / "agents" / agent
            needed = set(
                resolve_scenarios_from_fixtures(
                    agent_dir / "fixtures",
                    test_indices=test_indices,
                    max_tests=max_tests,
                    tag_filter=tag_filter,
                )
            )
        except Exception as e:
            logger.warning(
                "Failed to resolve scenarios for %s: %s, falling back to full deploy",
                agent,
                e,
            )
            needed = None  # unknown — fall back

        if needed is not None:
            missing = needed - deployed_scenarios
            if not missing:
                env_vars["DEPLOY_INFRA"] = "false"
                logger.info(
                    "Infra already deployed for %s (all %d scenarios covered), skipping deploy",
                    agent,
                    len(needed),
                )
            else:
                # Missing scenarios — fall back to full deploy via pytest conftest
                env_vars["DEPLOY_INFRA"] = "true"
                logger.info(
                    "Infra deployed for %s but missing %d scenarios (%s), will deploy via pytest",
                    agent,
                    len(missing),
                    ", ".join(sorted(missing)),
                )
        else:
            env_vars["DEPLOY_INFRA"] = "true"
    else:
        env_vars["DEPLOY_INFRA"] = "true"

    # Add tool_config if provided
    if tool_config:
        env_vars["TOOL_CONFIG"] = tool_config

    # Pass test selection to env vars (used by refactored agents)
    if max_tests is not None:
        env_vars["MAX_TESTS"] = str(max_tests)
    if test_indices is not None:
        env_vars["TEST_INDICES"] = test_indices
    if tag_filter:
        env_vars["TAG_FILTER"] = tag_filter
    if skip_indices:
        env_vars["SKIP_INDICES"] = skip_indices

    run_env.update(env_vars)

    try:
        # Run pytest from project root (unified benchmark uses absolute paths)
        logger.info(f"Executing command: {' '.join(cmd)} (in {project_root})")
        process = await asyncio.create_subprocess_exec(
            *cmd,
            cwd=str(project_root),
            stdout=asyncio.subprocess.PIPE,
            stderr=asyncio.subprocess.PIPE,
            env=run_env,
            start_new_session=True,  # new process group for clean kill
        )
        _register_process(run_id, process)

        async def log_stream(stream, level):
            while True:
                line = await stream.readline()
                if not line:
                    break
                text = line.decode().strip()
                # Skip pytest live-log separator lines
                if text.startswith("---") and "live log" in text:
                    continue
                if text:
                    logger.log(level, text)

        # Create tasks to read stdout and stderr concurrently
        await asyncio.gather(
            log_stream(process.stdout, logging.INFO),
            log_stream(process.stderr, logging.WARNING),
        )

        await process.wait()
        _unregister_process(run_id)

        if process.returncode not in [0, 1]:
            logger.error(f"Pytest execution failed with return code {process.returncode}")
            run_manager.fail_run(run_id, f"Pytest exited with code {process.returncode}")
            await send_email_async(
                email,
                f"{agent.upper()} Agent Benchmark Failed",
                f"The benchmark execution encountered an error (exit code: {process.returncode}).",
                cc_emails=cc_emails,
            )
            return

        # Assemble report from DB
        run_manager.set_phase(run_id, run_manager.RunPhase.SENDING_REPORT)
        # Same event-loop concern as the pytest path above.
        is_complete = await asyncio.to_thread(run_manager.complete_run, run_id)
        if not is_complete:
            logger.info(
                "Run %s has tests waiting for followups, skipping report email", run_id
            )
            return

        report_data = run_manager.get_report(run_id)
        if not report_data:
            logger.warning("No test results in DB for run %s", run_id)
            run_manager.fail_run(run_id, "No test results stored")
            await send_email_async(
                email,
                f"{agent.upper()} Agent Benchmark - No Results",
                "The benchmark ran but no test results were recorded."
                " This usually means all test queries failed or were skipped."
                " Please check the logs.",
                cc_emails=cc_emails,
            )
            return

        summary = report_data.get("summary", {})

        # Create email body
        email_body = build_benchmark_email(
            report_data,
            display_date=benchmark_trigger_time.strftime("%B %d, %Y at %H:%M UTC"),
        )

        # Write report to temp file for email attachment
        import tempfile

        report_file = os.path.join(
            tempfile.gettempdir(),
            f"benchmark_report_{agent}_{run_id}.json",
        )
        with open(report_file, "w") as f:
            json.dump(report_data, f, indent=2)

        # Send email
        run_label = report_data.get("metadata", {}).get("run_name") or agent.upper()
        await send_email_async(
            email,
            f"{run_label} Benchmark Report - {summary.get('overall_accuracy', 'N/A')}% Accuracy",
            email_body,
            attachments=[report_file],
            cc_emails=cc_emails,
        )

        # Clean up temp file
        try:
            os.unlink(report_file)
        except OSError:
            pass

        logger.info(f"Successfully completed {agent} benchmark and sent email to {email}")

    except Exception as e:
        logger.exception(f"Error during {agent} benchmark execution or notification")
        _unregister_process(run_id)
        run_manager.fail_run(run_id, str(e))
        await send_email_async(
            email,
            f"{agent.upper()} Agent Benchmark Error",
            f"An unexpected error occurred: {str(e)}",
            cc_emails=cc_emails,
        )


@router.post("/run", status_code=202)
async def trigger_agent_benchmark(
    request: AgentBenchmarkRequest,
    background_tasks: BackgroundTasks,
    user: AuthUser = Depends(get_current_user),
    authz=Depends(get_authz),
):
    """
    Triggers agent-specific benchmark tests in the background.
    A report will be sent to the user's email address upon completion.

    Supported agents: aws, kubectl, rca, loki, promql, trace, events, datadog, kubectl_log, es, recommendations

    Agent can be either:
    - A string: "aws"
    - An object: {"name": "aws", "tool_config": "dev-new"}
    """
    user_id, tenant_id, account_id = _resolve_run_identity(user, authz, request)

    # Parse agent - support both string and object formats
    if isinstance(request.agent, str):
        agent_name = request.agent
        tool_config = None
    else:
        agent_name = request.agent.name
        tool_config = request.agent.tool_config

    # Create a run with lifecycle tracking
    run_id = run_manager.create_run(
        agent_name,
        user_id=user_id,
        account_id=account_id,
        tenant_id=tenant_id,
        tool_config=tool_config,
        max_tests=request.max_tests,
        test_indices=request.test_indices,
        test_filter=request.test_filter,
        tag_filter=request.tag_filter,
        cc_emails=request.cc_emails,
        parallel_workers=request.parallel_workers,
        run_name=request.run_name,
    )

    background_tasks.add_task(
        run_agent_benchmark_and_notify,
        user_id,
        account_id,
        tenant_id,
        agent_name,
        run_id,
        tool_config,
        request.max_tests,
        request.test_indices,
        request.test_filter,
        request.tag_filter,
        request.cc_emails,
        request.parallel_workers,
    )

    message = f"{agent_name.upper()} agent benchmark started."
    if request.tag_filter:
        message += f" Tag filter: {request.tag_filter}."
    if request.test_indices:
        message += f" Running test indices: {request.test_indices}."
    elif request.max_tests:
        message += f" Running first {request.max_tests} test(s)."
    message += " The user will receive an email with the report."
    if request.cc_emails:
        message += f" CC: {', '.join(request.cc_emails)}"

    return {"run_id": run_id, "message": message}


# --- Static routes MUST come before parameterized /{run_id} routes ---


@router.post("/gather", status_code=201)
async def gather_benchmark_tests(
    request: GatherBenchmarkRequest,
    user: AuthUser = Depends(get_current_user),
    authz=Depends(get_authz),
):
    """Discover tests based on filters and create DB entries without running them.

    Returns the run_id and discovered test list. Tests are stored as 'pending'
    in the DB so the UI can display them. Individual tests can then be run
    via /{run_id}/run-test/{test_index}, or all at once via /{run_id}/run-all.
    """
    from llm.agents.common.fixtures import find_test_cases

    user_id, tenant_id, account_id = _resolve_run_identity(user, authz, request)

    if isinstance(request.agent, str):
        agent_name = request.agent
        tool_config = None
    else:
        agent_name = request.agent.name
        tool_config = request.agent.tool_config

    if not re.match(r"^[a-z0-9_-]+$", agent_name):
        raise HTTPException(status_code=400, detail=f"Invalid agent name: {agent_name}")

    project_root = Path(__file__).resolve().parent.parent.parent
    agent_dir = project_root / "llm" / "agents" / agent_name
    config_file = agent_dir / "config.yaml"
    if not config_file.exists():
        raise HTTPException(
            status_code=404, detail=f"config.yaml not found for agent: {agent_name}"
        )

    fixtures_dir = agent_dir / "fixtures"
    max_tests = request.max_tests if request.max_tests and request.max_tests > 0 else None
    test_cases = find_test_cases(
        fixtures_dir,
        max_tests=max_tests,
        test_indices=request.test_indices,
        tag_filter=request.tag_filter,
    )

    if not test_cases:
        raise HTTPException(
            status_code=404,
            detail=f"No test cases found for agent '{agent_name}' with the given filters.",
        )

    run_id = run_manager.create_gathered_run(
        agent=agent_name,
        test_cases=test_cases,
        user_id=user_id,
        account_id=account_id,
        tenant_id=tenant_id,
        tool_config=tool_config,
        max_tests=max_tests,
        test_indices=request.test_indices,
        test_filter=request.test_filter,
        tag_filter=request.tag_filter,
        cc_emails=request.cc_emails,
    )

    tests_summary = [{"test_id": tid, "test_index": gi} for _fp, tid, gi in test_cases]

    return {
        "run_id": run_id,
        "agent": agent_name,
        "total_tests": len(test_cases),
        "tests": tests_summary,
        "message": f"Gathered {len(test_cases)} tests for {agent_name}. "
        "Use /run-test/{test_index} to run individually or /run-all to run all.",
    }


@router.get("/agents/{agent_name}/tests")
async def list_agent_tests(agent_name: str, tag_filter: Optional[str] = None):
    """List all test cases for an agent (lightweight, no DB writes)."""
    from llm.agents.common.fixtures import find_test_cases

    if not re.match(r"^[a-z0-9_-]+$", agent_name):
        raise HTTPException(status_code=400, detail=f"Invalid agent name: {agent_name}")

    project_root = Path(__file__).resolve().parent.parent.parent
    fixtures_dir = project_root / "llm" / "agents" / agent_name / "fixtures"
    if not fixtures_dir.exists():
        raise HTTPException(
            status_code=404, detail=f"No fixtures for agent: {agent_name}"
        )

    from llm.agents.common.fixtures import load_test_case

    raw_cases = find_test_cases(fixtures_dir, tag_filter=tag_filter)
    tests = []
    for file_path, test_id, global_index in raw_cases:
        try:
            tc = load_test_case(file_path)
        except Exception:
            tc = {}
        skipped = tc.get("skip", False)
        tests.append(
            {
                "index": global_index,
                "test_id": test_id,
                "tags": tc.get("tags", []),
                "skipped": skipped,
                "skip_reason": tc.get("skip_reason", "") if skipped else "",
            }
        )
    return {"agent": agent_name, "tests": tests, "total": len(tests)}


@router.get("/agents")
async def list_available_agents():
    """List all available benchmark agents with fixture counts and config types."""
    import yaml as _yaml

    project_root = Path(__file__).resolve().parent.parent.parent
    agents_dir = project_root / "llm" / "agents"
    agents = []
    if agents_dir.exists():
        for config_file in sorted(agents_dir.glob("*/config.yaml")):
            agent_name = config_file.parent.name
            fixtures_dir = config_file.parent / "fixtures"
            fixture_count = (
                len(list(fixtures_dir.glob("*/test_case.yaml"))) if fixtures_dir.exists() else 0
            )
            # Read config_types from config.yaml
            config_types = []
            try:
                with open(config_file) as f:
                    cfg = _yaml.safe_load(f) or {}
                config_types = cfg.get("config_types", [])
            except Exception:
                pass
            # Check if agent needs infrastructure (has conftest.py + scenario fixtures)
            has_infra = (config_file.parent / "conftest.py").exists()
            agents.append(
                {
                    "name": agent_name,
                    "fixtures": fixture_count,
                    "config_types": config_types,
                    "has_infra": has_infra,
                    "path": str(config_file.relative_to(project_root)),
                }
            )
    return {"agents": agents, "total": len(agents)}


@router.get("/agents/{agent_name}/tags")
async def list_agent_tags(agent_name: str):
    """List all unique tags for a specific agent's test fixtures."""
    from llm.agents.common.fixtures import collect_tags

    project_root = Path(__file__).resolve().parent.parent.parent
    fixtures_dir = project_root / "llm" / "agents" / agent_name / "fixtures"
    if not fixtures_dir.exists():
        raise HTTPException(status_code=404, detail=f"Agent '{agent_name}' not found")
    tags = collect_tags(fixtures_dir)
    return {"agent": agent_name, "tags": tags}


# ---------------------------------------------------------------------------
# Standalone infrastructure management (not tied to a specific run)
# ---------------------------------------------------------------------------


def _get_infra_state(agent: str) -> Optional[dict]:
    """Read infra state from DB."""
    from benchmark_server.models.benchmark_run import BenchmarkInfraState
    from benchmark_server.utils.db_utils import get_db

    db = get_db()
    if not db:
        return None
    try:
        row = db.query(BenchmarkInfraState).filter_by(agent=agent).first()
        if not row:
            return None
        return {
            "status": row.status,
            "started_at": row.started_at.isoformat() if row.started_at else None,
            "finished_at": row.finished_at.isoformat() if row.finished_at else None,
            "error": row.error,
            "output": row.output,
            "test_indices": row.test_indices,
            "max_tests": row.max_tests,
            "tag_filter": row.tag_filter,
            "scenarios": row.scenarios,
        }
    finally:
        db.close()


def _infra_state_to_api(row) -> dict:
    """Convert a BenchmarkInfraState row to API dict."""
    return {
        "status": row.status,
        "started_at": row.started_at.isoformat() if row.started_at else None,
        "finished_at": row.finished_at.isoformat() if row.finished_at else None,
        "error": row.error,
        "output": row.output,
        "test_indices": row.test_indices,
        "max_tests": row.max_tests,
        "tag_filter": row.tag_filter,
        "scenarios": row.scenarios,
    }


def _set_infra_state(
    agent: str,
    status: str,
    error: Optional[str] = None,
    started_at: Optional[datetime] = None,
    finished_at: Optional[datetime] = None,
    test_indices: Optional[str] = None,
    max_tests: Optional[int] = None,
    tag_filter: Optional[str] = None,
    scenarios: Optional[list] = None,
    output: Optional[str] = None,
):
    """Upsert infra state in DB."""
    from benchmark_server.models.benchmark_run import BenchmarkInfraState
    from benchmark_server.utils.db_utils import get_db

    db = get_db()
    if not db:
        logger.warning("Cannot persist infra state: DB not configured")
        return
    try:
        row = db.query(BenchmarkInfraState).filter_by(agent=agent).first()
        if row:
            if status is not None:
                row.status = status
            if error is not None:
                row.error = error
            if started_at:
                row.started_at = started_at
            if finished_at:
                row.finished_at = finished_at
            if test_indices is not None:
                row.test_indices = test_indices
            if max_tests is not None:
                row.max_tests = max_tests
            if tag_filter is not None:
                row.tag_filter = tag_filter
            if scenarios is not None:
                row.scenarios = scenarios
            if output is not None:
                row.output = output
        else:
            row = BenchmarkInfraState(
                agent=agent,
                status=status,
                error=error,
                started_at=started_at,
                finished_at=finished_at,
                test_indices=test_indices,
                max_tests=max_tests,
                tag_filter=tag_filter,
                scenarios=scenarios,
                output=output,
            )
            db.add(row)
        db.commit()
    except Exception:
        db.rollback()
        logger.exception("Failed to persist infra state for %s", agent)
    finally:
        db.close()


INFRA_STALE_TIMEOUT_MINUTES = int(os.environ.get("BENCHMARK_INFRA_STALE_TIMEOUT_MINUTES", "120"))


def _is_infra_state_stale(state: dict) -> bool:
    """Check if an in-progress infra operation has been running longer than the stale timeout."""
    started = state.get("started_at")
    if not started:
        return True
    if isinstance(started, str):
        started = datetime.fromisoformat(started)
    return datetime.utcnow() - started > timedelta(minutes=INFRA_STALE_TIMEOUT_MINUTES)


class InfraDeployRequest(BaseModel):
    agent: str
    test_indices: Optional[str] = None
    max_tests: Optional[int] = None
    tag_filter: Optional[str] = None


class InfraNukeRequest(BaseModel):
    agent: str


@router.post("/infra/deploy", status_code=202)
async def deploy_standalone_infra(
    req: InfraDeployRequest,
    background_tasks: BackgroundTasks,
    authz=Depends(get_authz),
):
    """Deploy infrastructure for an agent independently of any run.

    This allows deploying infra once and reusing it across multiple benchmark runs
    by setting DEPLOY_INFRA=false (the default) when starting runs.

    Tenant-admin role is required: infra is shared cluster state, not
    per-tenant, so this is treated as an operator action.
    """
    _require_admin(authz)
    agent = req.agent
    project_root = Path(__file__).resolve().parent.parent.parent
    agent_dir = project_root / "llm" / "agents" / agent

    if not agent_dir.exists():
        raise HTTPException(status_code=404, detail=f"Agent '{agent}' not found")

    current = _get_infra_state(agent)
    if current and current.get("status") in ("deploying", "nuking"):
        if _is_infra_state_stale(current):
            prev = current["status"]
            new_status = "failed" if prev == "deploying" else "nuke_failed"
            _set_infra_state(
                agent,
                new_status,
                error=f"Timed out (was {prev} for >{INFRA_STALE_TIMEOUT_MINUTES} min)",
                finished_at=datetime.utcnow(),
            )
            logger.warning(
                "Auto-reset stale infra state: agent=%s was '%s' → '%s'",
                agent,
                prev,
                new_status,
            )
        else:
            raise HTTPException(
                status_code=409,
                detail=f"Infrastructure operation already in progress for '{agent}' (status: {current['status']})",
            )

    now = datetime.utcnow()
    _set_infra_state(
        agent,
        "deploying",
        started_at=now,
        output="",
        error="",
        test_indices=req.test_indices or "",
        max_tests=req.max_tests or 0,
        tag_filter=req.tag_filter or "",
    )
    background_tasks.add_task(
        _standalone_deploy_task,
        agent,
        agent_dir,
        project_root,
        req.test_indices,
        req.max_tests,
        req.tag_filter,
    )
    return {
        "agent": agent,
        "message": f"Infrastructure deployment started for '{agent}'",
    }


async def _standalone_deploy_task(
    agent: str,
    agent_dir: Path,
    project_root: Path,
    test_indices: Optional[str],
    max_tests: Optional[int],
    tag_filter: Optional[str],
):
    """Background task: deploy infrastructure for an agent (standalone)."""
    proc_key = f"infra_deploy_{agent}"
    try:
        from llm.agents.common.lifecycle import resolve_scenarios_from_fixtures

        fixtures_dir = agent_dir / "fixtures"
        scenarios = resolve_scenarios_from_fixtures(
            fixtures_dir,
            test_indices=test_indices,
            max_tests=max_tests,
            tag_filter=tag_filter,
        )

        conftest_path = agent_dir / "conftest.py"
        if not scenarios or not conftest_path.exists():
            logger.info("No infrastructure needed for agent %s", agent)
            _set_infra_state(agent, "deployed", finished_at=datetime.utcnow(), scenarios=[])
            return

        # Store resolved scenarios
        _set_infra_state(agent, "deploying", scenarios=scenarios)

        # Find the deploy script: {agent_dir}/{agent}-agent-test/scripts/deploy.sh
        deploy_script = None
        for candidate in [
            agent_dir / f"{agent}-agent-test" / "scripts" / "deploy.sh",
            agent_dir / "scripts" / "deploy.sh",
            agent_dir / "deploy.sh",
        ]:
            if candidate.exists():
                deploy_script = candidate
                break

        if not deploy_script:
            _set_infra_state(
                agent,
                "failed",
                error=f"No deploy.sh found for agent '{agent}'",
                finished_at=datetime.utcnow(),
            )
            return

        env = os.environ.copy()
        deploy_log = []

        # 1. Bootstrap (shared networking/resources) — always needed
        logger.info("Standalone infra deploy for %s: bootstrap", agent)
        bootstrap_cmd = ["bash", str(deploy_script), "--bootstrap"]
        process = await asyncio.create_subprocess_exec(
            *bootstrap_cmd,
            stdout=asyncio.subprocess.PIPE,
            stderr=asyncio.subprocess.PIPE,
            env=env,
            start_new_session=True,
        )
        _register_process(proc_key, process)
        deploy_log.append("[bootstrap] running...")
        await _stream_process_output(process, agent, deploy_log)
        await process.wait()
        _unregister_process(proc_key)

        if process.returncode != 0:
            deploy_log.append(f"[bootstrap] FAILED (exit code {process.returncode})")
            logger.error("Standalone infra bootstrap failed for %s", agent)
            _set_infra_state(
                agent,
                "failed",
                error=f"bootstrap failed (exit {process.returncode})",
                output="\n".join(deploy_log),
                finished_at=datetime.utcnow(),
            )
            return

        deploy_log.append("[bootstrap] OK")
        logger.info("Bootstrap succeeded for %s", agent)

        # 2. Deploy scenarios in parallel (they only depend on bootstrap, not each other)
        max_parallel = int(os.environ.get("BENCHMARK_INFRA_PARALLEL_DEPLOYS", "5"))
        failed_deploys = []

        async def deploy_one(scenario):
            """Deploy a single scenario, return (scenario, success, lines)."""
            lines = [f"[{scenario}] deploying..."]
            cmd = ["bash", str(deploy_script), scenario, "Broken"]
            proc = await asyncio.create_subprocess_exec(
                *cmd,
                stdout=asyncio.subprocess.PIPE,
                stderr=asyncio.subprocess.PIPE,
                env=env,
                start_new_session=True,
            )
            key = f"{proc_key}_{scenario}"
            _register_process(key, proc)
            await _stream_process_output(proc, agent, lines)
            await proc.wait()
            _unregister_process(key)
            ok = proc.returncode == 0
            lines.append(
                f"[{scenario}] {'OK' if ok else 'FAILED (exit code ' + str(proc.returncode) + ')'}"
            )
            if not ok:
                logger.error("Failed to deploy scenario %s for %s", scenario, agent)
            return scenario, ok, lines

        # Run in batches of max_parallel
        for i in range(0, len(scenarios), max_parallel):
            batch = scenarios[i : i + max_parallel]
            logger.info(
                "Deploying batch %d/%d (%d scenarios) for %s",
                i // max_parallel + 1,
                (len(scenarios) + max_parallel - 1) // max_parallel,
                len(batch),
                agent,
            )
            deploy_log.append(f"--- Batch {i // max_parallel + 1}: {', '.join(batch)} ---")
            _set_infra_state(agent, None, output="\n".join(deploy_log)[-4000:])

            results = await asyncio.gather(*[deploy_one(s) for s in batch])
            for scenario, ok, lines in results:
                deploy_log.extend(lines)
                if not ok:
                    failed_deploys.append(scenario)

        combined_output = "\n".join(deploy_log)

        # Check if operation was cancelled while we were deploying
        current = _get_infra_state(agent)
        if current and current.get("status") != "deploying":
            logger.info(
                "Deploy for %s was cancelled/reset (status=%s), not overwriting",
                agent,
                current.get("status"),
            )
            return

        if failed_deploys:
            logger.warning("Scenarios failed to deploy for %s: %s", agent, failed_deploys)
            _set_infra_state(
                agent,
                "failed",
                error=f"{len(failed_deploys)} scenario(s) failed: {', '.join(failed_deploys)}",
                output=combined_output,
                finished_at=datetime.utcnow(),
            )
        else:
            logger.info("Standalone infra deploy succeeded for %s", agent)
            _set_infra_state(
                agent, "deployed", output=combined_output, finished_at=datetime.utcnow()
            )
    except Exception as e:
        logger.exception("Standalone infra deploy error for %s", agent)
        _unregister_process(proc_key)
        _set_infra_state(agent, "failed", error=str(e), finished_at=datetime.utcnow())


@router.post("/infra/nuke", status_code=202)
async def nuke_standalone_infra(
    req: InfraNukeRequest,
    background_tasks: BackgroundTasks,
    authz=Depends(get_authz),
):
    """Nuke (destroy) all infrastructure for an agent.

    Runs the agent's nuke.sh script to tear down all deployed resources.
    Tenant-admin role is required (destructive operator action).
    """
    _require_admin(authz)
    agent = req.agent
    project_root = Path(__file__).resolve().parent.parent.parent
    agent_dir = project_root / "llm" / "agents" / agent

    if not agent_dir.exists():
        raise HTTPException(status_code=404, detail=f"Agent '{agent}' not found")

    current = _get_infra_state(agent)
    if current and current.get("status") in ("deploying", "nuking"):
        if _is_infra_state_stale(current):
            prev = current["status"]
            new_status = "failed" if prev == "deploying" else "nuke_failed"
            _set_infra_state(
                agent,
                new_status,
                error=f"Timed out (was {prev} for >{INFRA_STALE_TIMEOUT_MINUTES} min)",
                finished_at=datetime.utcnow(),
            )
            logger.warning(
                "Auto-reset stale infra state: agent=%s was '%s' → '%s'",
                agent,
                prev,
                new_status,
            )
        else:
            raise HTTPException(
                status_code=409,
                detail=f"Infrastructure operation already in progress for '{agent}' (status: {current['status']})",
            )

    # Find the nuke script (prefer Python/boto3, fall back to bash/aws-cli)
    nuke_script = None
    for candidate in [
        agent_dir / f"{agent}-agent-test" / "scripts" / "nuke.py",
        agent_dir / "scripts" / "nuke.py",
        agent_dir / f"{agent}-agent-test" / "scripts" / "nuke.sh",
        agent_dir / "scripts" / "nuke.sh",
        agent_dir / "nuke.sh",
    ]:
        if candidate.exists():
            nuke_script = candidate
            break

    if not nuke_script:
        raise HTTPException(status_code=404, detail=f"No nuke script found for agent '{agent}'")

    _set_infra_state(agent, "nuking", started_at=datetime.utcnow(), output="", error="")
    background_tasks.add_task(_standalone_nuke_task, agent, nuke_script)
    return {"agent": agent, "message": f"Infrastructure nuke started for '{agent}'"}


INFRA_LOG_FLUSH_INTERVAL = int(os.environ.get("BENCHMARK_INFRA_LOG_FLUSH_SECONDS", "10"))


async def _flush_output_to_db(agent: str, lines: list):
    """Flush output to DB in a thread pool to avoid blocking the event loop."""
    output = "\n".join(lines)[-4000:]
    loop = asyncio.get_event_loop()
    await loop.run_in_executor(None, lambda: _set_infra_state(agent, None, output=output))


async def _stream_process_output(process, agent: str, lines: list):
    """Stream stdout/stderr from a subprocess, logging each line and persisting to DB periodically."""
    last_flush = time.monotonic()
    dirty = False

    async def _read_stream(stream, prefix=""):
        nonlocal last_flush, dirty
        async for raw_line in stream:
            line = raw_line.decode().rstrip()
            if line:
                logger.info("infra[%s]: %s%s", agent, prefix, line)
                lines.append(f"{prefix}{line}")
                dirty = True
                # Flush to DB at most every N seconds, non-blocking
                now = time.monotonic()
                if now - last_flush >= INFRA_LOG_FLUSH_INTERVAL:
                    asyncio.ensure_future(_flush_output_to_db(agent, list(lines)))
                    last_flush = now
                    dirty = False

    await asyncio.gather(
        _read_stream(process.stdout),
        _read_stream(process.stderr, "[stderr] "),
    )

    # Final flush for any remaining lines
    if dirty:
        await _flush_output_to_db(agent, list(lines))


async def _standalone_nuke_task(agent: str, nuke_script: Path):
    """Background task: nuke infrastructure for an agent."""
    proc_key = f"infra_nuke_{agent}"
    output_lines = []
    try:
        # Use sys.executable for .py scripts, bash for .sh
        is_python = str(nuke_script).endswith(".py")
        if is_python:
            cmd = [sys.executable, str(nuke_script), "--yes"]
        else:
            cmd = ["bash", str(nuke_script)]

        process = await asyncio.create_subprocess_exec(
            *cmd,
            stdin=asyncio.subprocess.PIPE if not is_python else None,
            stdout=asyncio.subprocess.PIPE,
            stderr=asyncio.subprocess.PIPE,
            start_new_session=True,
        )
        _register_process(proc_key, process)

        # Send "y\n" to stdin for bash scripts (interactive confirmation)
        if not is_python and process.stdin:
            process.stdin.write(b"y\n")
            await process.stdin.drain()
            process.stdin.close()

        # Stream output live
        await _stream_process_output(process, agent, output_lines)
        await process.wait()
        _unregister_process(proc_key)

        combined_output = "\n".join(output_lines)

        # Check if operation was cancelled while we were nuking
        current = _get_infra_state(agent)
        if current and current.get("status") != "nuking":
            logger.info(
                "Nuke for %s was cancelled/reset (status=%s), not overwriting",
                agent,
                current.get("status"),
            )
            return

        if process.returncode != 0:
            error = f"exit code {process.returncode}"
            logger.error("Standalone nuke failed for %s: %s", agent, error)
            _set_infra_state(
                agent,
                "nuke_failed",
                error=error,
                output=combined_output,
                finished_at=datetime.utcnow(),
            )
        else:
            logger.info("Standalone nuke succeeded for %s", agent)
            _set_infra_state(agent, "nuked", output=combined_output, finished_at=datetime.utcnow())
    except Exception as e:
        logger.exception("Standalone nuke error for %s", agent)
        _unregister_process(proc_key)
        _set_infra_state(
            agent,
            "nuke_failed",
            error=str(e),
            output="\n".join(output_lines),
            finished_at=datetime.utcnow(),
        )


@router.post("/infra/cancel")
async def cancel_infra_operation(
    req: InfraNukeRequest,
    authz=Depends(get_authz),
):
    """Cancel an in-progress deploy or nuke operation. Admin only."""
    _require_admin(authz)
    agent = req.agent
    current = _get_infra_state(agent)
    if not current or current.get("status") not in ("deploying", "nuking"):
        raise HTTPException(
            status_code=409,
            detail=f"No active infra operation to cancel for '{agent}' (status: {current.get('status') if current else 'unknown'})",
        )

    prev_status = current["status"]
    # Kill the main process and all parallel scenario processes
    prefix = f"infra_deploy_{agent}" if prev_status == "deploying" else f"infra_nuke_{agent}"
    _kill_by_prefix(prefix)

    new_status = "failed" if prev_status == "deploying" else "nuke_failed"
    _set_infra_state(
        agent,
        new_status,
        error=f"Cancelled by user (was {prev_status})",
        finished_at=datetime.utcnow(),
    )
    return {"agent": agent, "message": f"Cancelled {prev_status} for '{agent}'"}


@router.post("/infra/reset")
async def reset_infra_state(
    req: InfraNukeRequest,
    authz=Depends(get_authz),
):
    """Force-reset a stuck infra state. Admin only.

    Use when infra is stuck in deploying/nuking after a crash or timeout
    and the cancel endpoint can't help (no running process to kill).
    """
    _require_admin(authz)
    agent = req.agent
    current = _get_infra_state(agent)
    if not current:
        raise HTTPException(status_code=404, detail=f"No infra state found for '{agent}'")

    prev_status = current["status"]

    # Kill any lingering processes (including parallel scenario deploys)
    _kill_by_prefix(f"infra_deploy_{agent}")
    _kill_by_prefix(f"infra_nuke_{agent}")

    _set_infra_state(
        agent,
        "unknown",
        error=f"Force-reset by user (was {prev_status})",
        finished_at=datetime.utcnow(),
    )
    logger.warning("Infra state force-reset: agent=%s was '%s' → 'unknown'", agent, prev_status)
    return {
        "agent": agent,
        "previous_status": prev_status,
        "message": f"Infra state reset for '{agent}'",
    }


@router.get("/infra/status")
async def get_infra_status(
    agent: Optional[str] = None,
    authz=Depends(get_authz),
):
    """Get infrastructure status for one or all agents.

    Read-only — any authenticated user can call. (No admin gate; just
    requires authentication.)
    """
    if agent:
        state = _get_infra_state(agent)
        if not state:
            return {
                "agent": agent,
                "status": "unknown",
                "message": "No infra operation recorded",
            }
        return {"agent": agent, **state}

    # Return all agents' infra states
    from benchmark_server.models.benchmark_run import BenchmarkInfraState
    from benchmark_server.utils.db_utils import get_db

    db = get_db()
    if not db:
        return {"agents": {}}
    try:
        rows = db.query(BenchmarkInfraState).all()
        agents = {row.agent: _infra_state_to_api(row) for row in rows}
        return {"agents": agents}
    finally:
        db.close()


# Orphan-recovery debounce — list_benchmark_runs is polled by every open
# dashboard tab on a 10s cadence. Without this, every poll triggers a full
# stale-run sweep. _ORPHAN_SWEEP_INTERVAL caps the work to one sweep per
# minute regardless of poll volume.
_ORPHAN_SWEEP_INTERVAL = timedelta(seconds=60)
_last_orphan_sweep_at: Optional[datetime] = None


@router.get("/runs/list")
async def list_benchmark_runs(
    page: int = 1,
    page_size: int = 20,
    agent: Optional[str] = None,
    status: Optional[str] = None,
    search: Optional[str] = None,
    user: Optional[str] = None,
    authz=Depends(get_authz),
):
    """List tracked benchmark runs with server-side pagination and filters.

    Non-admin users only see runs from tenants they're a member of.
    """
    global _last_orphan_sweep_at
    now = datetime.now(timezone.utc)
    if (
        _last_orphan_sweep_at is None
        or (now - _last_orphan_sweep_at) >= _ORPHAN_SWEEP_INTERVAL
    ):
        _last_orphan_sweep_at = now
        try:
            run_manager.recover_orphaned_runs()
        except Exception:
            logger.exception("list_runs: orphan recovery sweep failed (non-fatal)")
    return run_manager.list_runs(
        page=max(1, page),
        page_size=min(max(1, page_size), 100),
        agent=agent,
        status=status,
        search=search,
        user=user,
        tenant_ids=_scope_tenant_ids(authz),
    )


@router.delete("/runs/cleanup")
async def cleanup_benchmark_runs(
    authz=Depends(get_authz),
):
    """Remove records for completed and stopped benchmark runs.

    Non-admin users only clean up runs in tenants they're a member of.
    """
    removed = run_manager.cleanup_completed_runs(tenant_ids=_scope_tenant_ids(authz))
    return {
        "removed": removed,
        "message": f"Cleaned up {removed} completed/stopped run(s)",
    }


# --- Parameterized /{run_id} routes ---


@router.post("/{run_id}/pause")
async def pause_benchmark(
    run_id: str,
    authz=Depends(get_authz),
):
    """Pause a running benchmark. The benchmark will pause after the current query completes."""
    _verify_run_access(run_id, authz)
    try:
        run_manager.pause_run(run_id)
    except ValueError as e:
        msg = str(e)
        if "not found" in msg:
            raise HTTPException(status_code=404, detail=msg)
        raise HTTPException(status_code=409, detail=msg)
    return {"run_id": run_id, "message": f"Benchmark {run_id} paused"}


@router.post("/{run_id}/resume")
async def resume_benchmark(
    run_id: str,
    authz=Depends(get_authz),
):
    """Resume a paused benchmark."""
    _verify_run_access(run_id, authz)
    try:
        run_manager.resume_run(run_id)
    except ValueError as e:
        msg = str(e)
        if "not found" in msg:
            raise HTTPException(status_code=404, detail=msg)
        raise HTTPException(status_code=409, detail=msg)
    return {"run_id": run_id, "message": f"Benchmark {run_id} resumed"}


@router.post("/{run_id}/stop")
async def stop_benchmark(
    run_id: str,
    authz=Depends(get_authz),
):
    """Stop a running benchmark. The benchmark will stop after the current query and generate a partial report."""
    _verify_run_access(run_id, authz)
    try:
        run_manager.stop_run(run_id)
    except ValueError as e:
        msg = str(e)
        if "not found" in msg:
            raise HTTPException(status_code=404, detail=msg)
        raise HTTPException(status_code=409, detail=msg)
    # Kill the subprocess(es) — main run + any individual test processes
    _kill_all_for_run(run_id)
    return {"run_id": run_id, "message": f"Benchmark {run_id} stopped"}


@router.post("/{run_id}/run-test/{test_index}", status_code=202)
async def run_single_test(
    run_id: str,
    test_index: int,
    background_tasks: BackgroundTasks,
    authz=Depends(get_authz),
):
    """Run a single test from a gathered benchmark run.

    The test is executed in the background. After completion, the run
    returns to 'gathered' state if other tests remain pending.
    """
    _verify_run_access(run_id, authz)
    try:
        config = run_manager.set_test_running(run_id, test_index)
    except ValueError as e:
        msg = str(e)
        if "not found" in msg:
            raise HTTPException(status_code=404, detail=msg)
        raise HTTPException(status_code=409, detail=msg)

    background_tasks.add_task(_run_single_test_task, run_id, test_index, config)
    return {
        "run_id": run_id,
        "test_index": test_index,
        "test_id": config.get("test_id", ""),
        "message": f"Test {test_index} ({config.get('test_id', '')}) started",
    }


async def _run_single_test_task(run_id: str, test_index: int, config: dict):
    """Background task: run a single test case via pytest."""
    agent = config["agent"]
    project_root = Path(__file__).resolve().parent.parent.parent
    agent_dir = project_root / "llm" / "agents" / agent
    agents_dir = agent_dir.parent

    env = os.environ.copy()
    env.update(
        {
            "ACCOUNT_ID": config.get("account_id", ""),
            "USER_ID": config.get("user_id", ""),
            "TENANT_ID": config.get("tenant_id", ""),
            "PYTHONUNBUFFERED": "1",
            "BENCHMARK_RUN_ID": run_id,
            "BENCHMARK_AGENT_DIR": str(agent_dir),
            "TEST_INDICES": str(test_index),
            "DEPLOY_INFRA": "false",
        }
    )
    if config.get("tool_config"):
        env["TOOL_CONFIG"] = config["tool_config"]

    cmd = [
        "python",
        "-m",
        "pytest",
        "-s",
        str(_UNIFIED_BENCHMARK),
        "-v",
        "--log-cli-level=INFO",
        "--confcutdir",
        str(agents_dir),
        "-m",
        "benchmark",
        "--test-indices",
        str(test_index),
    ]

    # Use run_id + test_index as process key so multiple single-test runs
    # from the same gathered run don't collide
    proc_key = f"{run_id}__t{test_index}"

    try:
        logger.info("Running single test %d for run %s", test_index, run_id)
        process = await asyncio.create_subprocess_exec(
            *cmd,
            cwd=str(project_root),
            stdout=asyncio.subprocess.PIPE,
            stderr=asyncio.subprocess.PIPE,
            env=env,
            start_new_session=True,
        )
        _register_process(proc_key, process)

        async def log_stream(stream, level):
            while True:
                line = await stream.readline()
                if not line:
                    break
                text = line.decode().strip()
                if text.startswith("---") and "live log" in text:
                    continue
                if text:
                    logger.log(level, text)

        await asyncio.gather(
            log_stream(process.stdout, logging.INFO),
            log_stream(process.stderr, logging.WARNING),
        )
        await process.wait()
        _unregister_process(proc_key)

        if process.returncode not in [0, 1]:
            logger.error(
                "Single test %d failed with exit code %d for run %s",
                test_index,
                process.returncode,
                run_id,
            )
            run_manager.add_error(
                run_id, f"Test {test_index} exited with code {process.returncode}"
            )
    except Exception as e:
        logger.exception("Error running single test %d for run %s", test_index, run_id)
        _unregister_process(proc_key)
        run_manager.add_error(run_id, f"Test {test_index} error: {e}")
    finally:
        run_manager.finish_single_test(run_id, prev_state=config.get("_prev_state"))


class FollowupSubmitRequest(BaseModel):
    response: str
    agent_id: Optional[str] = (
        None  # specific agent's followup to answer (for multi-followup)
    )
    message_id: Optional[str] = None  # specific message's followup to answer
    # Idempotency key: client-supplied or server-derived (hash of
    # message_id + response). Repeat submissions with the same key are
    # deduped so "Refresh & Retry" clicks are safe.
    idempotency_key: Optional[str] = None


# Process-local dedup cache for in-flight followup submissions.
# ``key -> expiry_epoch``. Keeps memory bounded by expiring old keys.
_FOLLOWUP_IDEMPOTENCY: dict = {}
_FOLLOWUP_IDEMPOTENCY_LOCK = asyncio.Lock()
_IDEMPOTENCY_TTL_SEC = 900  # 15 min is plenty: llm-server resume is seconds


async def _claim_idempotency_key(key: str) -> bool:
    """Return True if we can proceed with this key, False if it was already claimed.

    Cleans up expired keys on every claim attempt (amortized O(1)).
    """
    import time as _time

    now = _time.time()
    async with _FOLLOWUP_IDEMPOTENCY_LOCK:
        # Sweep expired entries.
        expired = [k for k, exp in _FOLLOWUP_IDEMPOTENCY.items() if exp <= now]
        for k in expired:
            _FOLLOWUP_IDEMPOTENCY.pop(k, None)
        if key in _FOLLOWUP_IDEMPOTENCY:
            return False
        _FOLLOWUP_IDEMPOTENCY[key] = now + _IDEMPOTENCY_TTL_SEC
        return True


def _derive_idempotency_key(
    run_id: str, test_index: int, body: FollowupSubmitRequest
) -> str:
    """Fall back to a stable key if the client didn't supply one."""
    import hashlib

    src = "|".join(
        [
            run_id,
            str(test_index),
            body.message_id or "",
            body.agent_id or "",
            body.response or "",
        ]
    ).encode("utf-8")
    return hashlib.sha1(src).hexdigest()


@router.post("/{run_id}/followup/{test_index}", status_code=202)
async def submit_followup(
    run_id: str,
    test_index: int,
    body: FollowupSubmitRequest,
    background_tasks: BackgroundTasks,
    authz=Depends(get_authz),
):
    """Submit a followup response for a test in WAITING status.

    Idempotent: repeat submissions with the same ``idempotency_key`` (or
    derived key) within a 15min window return 202 without re-dispatching.
    """
    _verify_run_access(run_id, authz)

    # Idempotency — reject duplicate claims silently with 202 so clients that
    # retry on timeout don't double-fire the llm-server call.
    idem_key = body.idempotency_key or _derive_idempotency_key(run_id, test_index, body)
    claimed = await _claim_idempotency_key(idem_key)
    if not claimed:
        logger.info(
            "submit_followup: deduped repeat submission run=%s test=%d key=%s",
            run_id,
            test_index,
            idem_key[:12],
        )
        return {
            "run_id": run_id,
            "test_index": test_index,
            "idempotency_key": idem_key,
            "message": f"Followup already submitted for test {test_index} (deduped)",
        }

    try:
        followup_data = run_manager.get_test_followup_data(run_id, test_index)
    except ValueError as e:
        msg = str(e)
        if "not found" in msg:
            raise HTTPException(status_code=404, detail=msg)
        raise HTTPException(status_code=409, detail=msg)

    # Don't change test status to 'running' — keep it as 'waiting' so the UI
    # continues showing remaining followup panels. The _run_followup_task will
    # update the status when the LLM response comes back.

    fq = followup_data["followup_request"]
    # Use agent_id/message_id from the request body if provided (multi-followup),
    # otherwise fall back to the stored followup_request (single followup / backward compat)
    resolved_agent_id = body.agent_id or fq.get("agent_id", "")
    resolved_message_id = body.message_id or fq.get("message_id", "")
    try:
        from benchmark_server.utils.followup_events import emit as _fevent

        _fevent(
            "submitted",
            run_id=run_id,
            test_index=test_index,
            conversation_id=followup_data["conversation_id"],
            followup_id=resolved_message_id,
            agent_id=resolved_agent_id,
            from_state="waiting",
            to_state="submitting",
            extra={"idempotency_key": idem_key[:12]},
        )
    except Exception:
        pass

    # Clear the answered followup from the row's ``followup_request`` so the UI
    # stops showing the same form once the answer has been dispatched. Sibling
    # followups in a multi-panel ``followups`` list stay visible — only the one
    # matching this submission's (agent_id, message_id) is removed. Without
    # this, the UI keeps polling and seeing the same row state until the LLM
    # eventually returns, which can be minutes — visible regression of the
    # "stuck on waiting" bug.
    try:
        run_manager.remove_submitted_followup(
            run_id, test_index, resolved_agent_id, resolved_message_id
        )
    except Exception as _e:
        logger.warning(
            "submit_followup: failed to remove answered followup from row "
            "run=%s test=%d: %s",
            run_id,
            test_index,
            _e,
        )

    background_tasks.add_task(
        _run_followup_task,
        run_id,
        test_index,
        followup_data["conversation_id"],
        resolved_agent_id,
        resolved_message_id,
        body.response,
        followup_data["config"],
        followup_data["query"],
        followup_data["expected_answer"],
    )
    return {
        "run_id": run_id,
        "test_index": test_index,
        "idempotency_key": idem_key,
        "message": f"Followup response submitted for test {test_index}",
    }


async def _run_followup_task(  # noqa: C901
    run_id: str,
    test_index: int,
    conversation_id: str,
    agent_id: str,
    message_id: str,
    user_response: str,
    run_config: dict,
    original_query: str,
    expected_answer: str,
):
    """Background task: resume a conversation after followup and store result."""
    from benchmark_server.utils.llm_client import (
        call_llm,
        extract_conversation_id,
        extract_response_text,
    )

    account_id = run_config.get("account_id", "")
    tenant_id = run_config.get("tenant_id", "")
    user_id = run_config.get("user_id", "")
    tool_config_str = run_config.get("tool_config", "")

    llm_config = None
    if tool_config_str:
        llm_config = {
            "tool_configs": {},
            "tool_confirmations": {},
        }

    try:
        # Run blocking call_llm in a thread to avoid blocking the event loop
        loop = asyncio.get_event_loop()
        llm_result = await loop.run_in_executor(
            None,
            lambda: call_llm(
                query=user_response,
                account_id=account_id,
                tenant_id=tenant_id,
                user_id=user_id,
                config=llm_config,
                conversation_id=conversation_id,
                agent_id=agent_id,
                message_id=message_id,
            ),
        )

        if llm_result.status == "WAITING" and (
            llm_result.followups or llm_result.followup_request
        ):
            # Multi-turn followup — store as waiting again using the unified
            # ``followups`` list shape (with legacy mirrors for compat).
            from benchmark_server.orchestrator import _wrap_followups_for_storage

            followups_list = list(llm_result.followups or [])
            if not followups_list and llm_result.followup_request:
                followups_list = [llm_result.followup_request]
            run_manager.store_test_result(
                run_id,
                {
                    "test_index": test_index,
                    "status": "waiting",
                    "followup_request": _wrap_followups_for_storage(followups_list),
                    "conversation_id": conversation_id,
                },
            )
            try:
                from benchmark_server.utils.followup_events import emit as _fevent

                for fu in followups_list:
                    _fevent(
                        "waiting",
                        run_id=run_id,
                        test_index=test_index,
                        conversation_id=conversation_id,
                        followup_id=str(fu.get("message_id") or ""),
                        agent_id=str(fu.get("agent_id") or ""),
                        followup_type=str(fu.get("followup_type") or ""),
                        from_state="answered",
                        to_state="waiting",
                    )
            except Exception:
                pass
            logger.info(
                "Test %d in run %s: another followup requested (%d panel(s))",
                test_index,
                run_id,
                len(followups_list),
            )
            return

        # Non-terminal status guard: llm-server accepted the followup but
        # hasn't produced a final answer yet (IN_PROGRESS / RUNNING /
        # PROCESSING / etc — anything that's not COMPLETED or FAILED-class).
        # Without this guard we'd misinterpret the "still working" response
        # as "agent finished with empty answer", store ``status='fail'`` with
        # "Empty response after followup", and corrupt the row before the
        # real answer arrives. Instead, leave the row at waiting and let
        # reconcile_waiting_tests pick up the eventual terminal state.
        # ``submit_followup`` already cleared the answered form from the row.
        _terminal = ("COMPLETED", "FAILED", "KILLED", "TERMINATED")
        _status_upper = (llm_result.status or "").upper()
        if _status_upper not in _terminal and not (
            llm_result.error_category or llm_result.error_message
        ):
            logger.info(
                "Test %d in run %s: followup accepted, conversation status=%s; "
                "leaving row at 'waiting' for reconcile to catch terminal state",
                test_index,
                run_id,
                llm_result.status,
            )
            return

        if llm_result.data:
            answer = extract_response_text(llm_result.data)
            convo_id = extract_conversation_id(llm_result.data) or conversation_id
            session_id = llm_result.data.get("data", {}).get("session_id")

            # Fetch metrics
            token_metrics = {}
            tool_names_data = {}
            execution_trace = ""
            try:
                from llm.agents.common.metrics import (
                    get_execution_trace,
                    get_token_metrics,
                    get_tool_names,
                )

                if session_id:
                    token_metrics = (
                        get_token_metrics(session_id, account_id, tenant_id, user_id) or {}
                    )
                tool_names_data = get_tool_names(convo_id=convo_id) or {}
                if convo_id:
                    execution_trace = get_execution_trace(convo_id, account_id) or ""
            except Exception as e:
                logger.warning("Failed to fetch metrics for test %d: %s", test_index, e)

            test_failed = not answer

            # Run RAGAS evaluation
            sim_score = 0.0
            rel_score = 0.0
            planner_score = 0.0
            score_reason = ""
            if not test_failed and expected_answer:
                try:
                    from ragas.llms import LangchainLLMWrapper

                    from benchmark_server.common.llm import get_llm
                    from llm.agents.common.ragas_evaluation import evaluate_single

                    base_llm = get_llm()
                    llm = LangchainLLMWrapper(base_llm)
                    eval_result = await loop.run_in_executor(
                        None,
                        lambda: evaluate_single(original_query, answer, expected_answer, llm, None),
                    )
                    sim_score = eval_result.similarity
                    rel_score = eval_result.quality
                    score_reason = eval_result.reason

                    # Planner evaluation
                    if execution_trace:
                        from llm.agents.common.benchmark import _evaluate_planner

                        p_score, p_reason = await loop.run_in_executor(
                            None,
                            lambda: _evaluate_planner(execution_trace, original_query, llm),
                        )
                        planner_score = p_score or 0.0
                        if p_reason:
                            score_reason += f"\n[Planner] {p_reason}"
                except Exception as e:
                    logger.warning("RAGAS evaluation failed for test %d: %s", test_index, e)

            result = {
                "test_index": test_index,
                "status": "pass" if not test_failed else "fail",
                "actual_answer": answer or "SYSTEM FAILURE",
                "conversation_id": convo_id,
                "polling_conversation_id": session_id or "",
                "execution_trace": execution_trace,
                "answer_similarity": sim_score,
                "answer_relevancy": rel_score,
                "planner_relevancy": planner_score,
                "score_reason": score_reason.strip(),
                "duration_seconds": 0.0,
                "cost": token_metrics.get("cost", 0.0),
                "total_tokens": token_metrics.get("total_tokens", 0),
                "input_tokens": token_metrics.get("input_tokens", 0),
                "output_tokens": token_metrics.get("completion_tokens", 0),
                "cache_read_tokens": token_metrics.get("cached_input_tokens", 0),
                "tool_calls_total": token_metrics.get("total_tool_calls", 0),
                "tool_calls_successful": token_metrics.get("successful_tool_calls", 0),
                "tool_names": tool_names_data.get("tool_names", []),
                "model_names": token_metrics.get("model_names", []),
                "model_providers": token_metrics.get("model_providers", []),
            }
            if test_failed:
                result["error_message"] = (
                    llm_result.error_message or "Empty response after followup"
                )
                result["error_category"] = llm_result.error_category or "empty_response"
            run_manager.store_test_result(run_id, result)
        else:
            run_manager.store_test_result(
                run_id,
                {
                    "test_index": test_index,
                    "status": "fail",
                    "error_message": llm_result.error_message or "LLM call failed after followup",
                    "error_category": llm_result.error_category or "agent_failed",
                },
            )
    except Exception as e:
        logger.exception("Error in followup task for test %d in run %s", test_index, run_id)
        run_manager.store_test_result(
            run_id,
            {
                "test_index": test_index,
                "status": "fail",
                "error_message": f"Followup task error: {e}",
                "error_category": "client_error",
            },
        )
    # NOTE: Do NOT call finish_single_test here. The main benchmark subprocess
    # manages run lifecycle. Calling it here would see only the DB rows that
    # exist so far (not all 45 tests), miscalculate pending=0, and mark the
    # run as COMPLETED — causing the subprocess to skip remaining tests.


@router.post("/{run_id}/run-all", status_code=202)
async def run_all_gathered_tests(
    run_id: str,
    background_tasks: BackgroundTasks,
    authz=Depends(get_authz),
):
    """Start full execution of all pending tests in a gathered run.

    Transitions the run from 'gathered' to 'running' and executes
    all tests sequentially (same as a normal benchmark run).
    """
    _verify_run_access(run_id, authz)
    try:
        config = run_manager.start_gathered_run(run_id)
    except ValueError as e:
        msg = str(e)
        if "not found" in msg:
            raise HTTPException(status_code=404, detail=msg)
        raise HTTPException(status_code=409, detail=msg)

    background_tasks.add_task(
        run_agent_benchmark_and_notify,
        config["user_id"],
        config["account_id"],
        config["tenant_id"],
        config["agent"],
        run_id,
        config.get("tool_config"),
        config.get("max_tests"),
        config.get("test_indices"),
        config.get("test_filter"),
        config.get("tag_filter"),
        config.get("cc_emails"),
        config.get("parallel_workers"),
    )

    return {
        "run_id": run_id,
        "message": f"All tests started for {config['agent']} benchmark",
    }


@router.post("/{run_id}/rerun", status_code=202)
async def rerun_benchmark(
    run_id: str,
    background_tasks: BackgroundTasks,
    authz=Depends(get_authz),
):
    """Fresh rerun — deletes old test results and starts from scratch."""
    _verify_run_access(run_id, authz)
    try:
        config = run_manager.rerun_run(run_id)
    except ValueError as e:
        msg = str(e)
        if "not found" in msg:
            raise HTTPException(status_code=404, detail=msg)
        raise HTTPException(status_code=409, detail=msg)

    background_tasks.add_task(
        run_agent_benchmark_and_notify,
        config["user_id"],
        config["account_id"],
        config["tenant_id"],
        config["agent"],
        run_id,
        config.get("tool_config"),
        config.get("max_tests"),
        config.get("test_indices"),
        config.get("test_filter"),
        config.get("tag_filter"),
        config.get("cc_emails"),
        config.get("parallel_workers"),
    )

    return {
        "run_id": run_id,
        "message": f"Rerun triggered for {config['agent']} benchmark",
    }


class RerunTestsRequest(BaseModel):
    test_indices: List[int]  # Test indices to rerun, e.g. [3, 5, 7]


@router.post("/{run_id}/rerun-tests", status_code=202)
async def rerun_specific_tests(
    run_id: str,
    request: RerunTestsRequest,
    background_tasks: BackgroundTasks,
    authz=Depends(get_authz),
):
    """Rerun specific test cases within an already-completed benchmark run.

    Keeps all other results intact. Only the requested test indices are
    re-executed and their scores updated.

    Example: POST /agent-benchmark/4aa3c853b474/rerun-tests
             {"test_indices": [3, 5, 7]}
    """
    _verify_run_access(run_id, authz)
    try:
        config = run_manager.rerun_tests(run_id, request.test_indices)
    except ValueError as e:
        msg = str(e)
        if "not found" in msg:
            raise HTTPException(status_code=404, detail=msg)
        raise HTTPException(status_code=409, detail=msg)

    rerun_indices = config["rerun_indices"]
    indices_str = ",".join(str(i) for i in rerun_indices)

    # Compute skip_indices: all indices NOT in the rerun set
    # This way the benchmark process runs only the requested tests
    background_tasks.add_task(
        run_agent_benchmark_and_notify,
        config["user_id"],
        config["account_id"],
        config["tenant_id"],
        config["agent"],
        run_id,
        config.get("tool_config"),
        None,  # max_tests — not needed, test_indices is explicit
        indices_str,  # test_indices — only run these
        config.get("test_filter"),
        config.get("tag_filter"),
        config.get("cc_emails"),
        config.get("parallel_workers"),
    )

    return {
        "run_id": run_id,
        "test_indices": rerun_indices,
        "message": f"Rerunning {len(rerun_indices)} test(s): [{indices_str}]",
    }


@router.post("/{run_id}/restart", status_code=202)
async def restart_benchmark(
    run_id: str,
    background_tasks: BackgroundTasks,
    authz=Depends(get_authz),
):
    """Resume a failed/stopped run — keeps existing results, runs only remaining tests."""
    _verify_run_access(run_id, authz)
    try:
        config = run_manager.restart_run(run_id)
    except ValueError as e:
        msg = str(e)
        if "not found" in msg:
            raise HTTPException(status_code=404, detail=msg)
        raise HTTPException(status_code=409, detail=msg)

    completed_indices = set(config.get("completed_indices", []))

    # Compute skip_indices as a comma-separated string for the env var
    # The benchmark process will use SKIP_INDICES to skip already-passed tests
    skip_indices = (
        ",".join(str(i) for i in sorted(completed_indices)) if completed_indices else None
    )

    background_tasks.add_task(
        run_agent_benchmark_and_notify,
        config["user_id"],
        config["account_id"],
        config["tenant_id"],
        config["agent"],
        run_id,
        config.get("tool_config"),
        config.get("max_tests"),
        config.get("test_indices"),
        config.get("test_filter"),
        config.get("tag_filter"),
        config.get("cc_emails"),
        parallel_workers=config.get("parallel_workers"),
        skip_indices=skip_indices,
    )

    skipped = len(completed_indices)
    return {
        "run_id": run_id,
        "message": f"Resumed {config['agent']} benchmark, skipping {skipped} completed test(s)",
    }


@router.post("/{run_id}/re-evaluate", status_code=202)
async def re_evaluate_benchmark(
    run_id: str,
    background_tasks: BackgroundTasks,
    authz=Depends(get_authz),
):
    """Re-run RAGAS evaluation on existing test results and regenerate the report.

    Reads actual/expected answers from DB, runs batch RAGAS scoring,
    updates scores in DB, and reassembles the report JSON.
    """
    _verify_run_access(run_id, authz)
    try:
        config = run_manager.set_run_evaluating(run_id)
    except ValueError as e:
        msg = str(e)
        if "not found" in msg:
            raise HTTPException(status_code=404, detail=msg)
        raise HTTPException(status_code=409, detail=msg)

    background_tasks.add_task(_run_re_evaluation, run_id)

    return {
        "run_id": run_id,
        "message": "Re-evaluation started. Scores will be updated in DB.",
    }


def _run_re_evaluation(run_id: str):
    """Background task: re-run RAGAS evaluation on stored test results.

    Sync (not async). FastAPI BackgroundTasks dispatches sync callables to a
    thread pool via run_in_threadpool, keeping the event loop free during the
    long RAGAS batch + per-test planner evals + token-metrics HTTP calls.
    Previously async-but-blocking, which froze the entire server for ~5-10
    minutes per re-eval (every blocking LLM/HTTP call ran inline on the
    event loop).
    """
    from ragas.llms import LangchainLLMWrapper

    from benchmark_server.common.llm import get_llm
    from llm.agents.common.metrics import (
        get_execution_trace,
        get_planner_response,
        get_token_metrics,
        lookup_session_id,
    )
    from llm.agents.common.ragas_evaluation import evaluate_batch

    account_id, tenant_id, user_id, test_results = run_manager.get_test_results_for_eval(run_id)
    if not test_results:
        logger.warning("No evaluable test results for run %s", run_id)
        run_manager.add_error(run_id, "No pass results to re-evaluate")
        run_manager.finish_re_evaluation(run_id)
        return

    queries = [r["query"] for r in test_results]
    answers = [r["actual_answer"] for r in test_results]
    references = [r["expected_answer"] for r in test_results]
    # Use each test's original ``created_at`` as the evaluator's time anchor
    # so time-sensitive rubric checks compare against when the agent actually
    # produced the answer — not against the re-eval clock (which can be
    # minutes-to-days later).
    eval_times = [r.get("created_at") for r in test_results]

    base_llm = get_llm()
    llm = LangchainLLMWrapper(base_llm)

    logger.info("Re-evaluating %d test results for run %s", len(test_results), run_id)

    # --- Answer similarity + quality (batch) ---
    try:
        batch_scores = evaluate_batch(
            queries, answers, references, llm, eval_times=eval_times
        )
        for j, tr in enumerate(test_results):
            tr["_sim"] = batch_scores[j].similarity
            tr["_rel"] = batch_scores[j].quality
            tr["_reason"] = batch_scores[j].reason
    except Exception as e:
        logger.error("Re-evaluation failed for run %s: %s", run_id, e, exc_info=True)
        run_manager.add_error(run_id, f"RAGAS re-evaluation failed: {e}")
        for tr in test_results:
            tr["_sim"] = None
            tr["_rel"] = None

    # --- Planner/execution quality (per-test, needs conversation_id) ---
    for tr in test_results:
        planner_score = None
        convo_id = tr.get("conversation_id")
        # Use stored trace first, rebuild from conversation DB if not stored
        trace = tr.get("execution_trace", "")
        if not trace and convo_id and account_id:
            trace = get_execution_trace(convo_id, account_id)
            if not trace:
                trace = get_planner_response(convo_id, account_id) or ""
        tr["_trace"] = trace  # Store for saving to DB
        if trace:
            try:
                from llm.agents.common.benchmark import _evaluate_planner

                planner_score, planner_reason = _evaluate_planner(trace, tr["query"], llm)
                if planner_reason:
                    existing = tr.get("_reason", "")
                    tr["_reason"] = (existing + f"\n[Planner] {planner_reason}").strip()
            except Exception as e:
                logger.warning("Planner eval failed for test %s: %s", tr["test_id"], e)

        # --- Refetch token metrics ---
        # polling_conversation_id stores the session_id needed by the metrics API.
        # Fallback: if missing (old runs), look up session_id from conversation_id (DB UUID).
        token_update = {}
        session_id = tr.get("polling_conversation_id") or ""
        if not session_id:
            convo_uuid = tr.get("conversation_id") or ""
            if convo_uuid:
                session_id = lookup_session_id(convo_uuid) or ""
        if session_id and account_id:
            token_metrics = get_token_metrics(session_id, account_id, tenant_id, user_id)
            if token_metrics:
                token_update = {
                    "cost": token_metrics.get("cost", 0.0),
                    "total_tokens": token_metrics.get("total_tokens", 0),
                    "input_tokens": token_metrics.get("input_tokens", 0),
                    "output_tokens": token_metrics.get("completion_tokens", 0),
                    "cache_read_tokens": token_metrics.get("cached_input_tokens", 0),
                    "tool_calls_total": token_metrics.get("total_tool_calls", 0),
                    "tool_calls_successful": token_metrics.get("successful_tool_calls", 0),
                    "model_names": token_metrics.get("model_names", []),
                    "model_providers": token_metrics.get("model_providers", []),
                }
            else:
                logger.warning(
                    "No token metrics returned for test %s session_id=%s",
                    tr["test_id"],
                    session_id,
                )
        else:
            logger.warning(
                "No session_id available for test %s (polling_conversation_id=%s, conversation_id=%s)",
                tr["test_id"],
                tr.get("polling_conversation_id"),
                tr.get("conversation_id"),
            )

        run_manager.update_test_result_scores(
            run_id,
            tr["test_index"],
            answer_similarity=tr.get("_sim"),
            answer_relevancy=tr.get("_rel"),
            planner_relevancy=planner_score,
            score_reason=tr.get("_reason"),
            execution_trace=tr.get("_trace"),
            **token_update,
        )
        logger.info(
            "Updated test %s (index %d): sim=%s rel=%s planner=%s tokens=%s",
            tr["test_id"],
            tr["test_index"],
            tr.get("_sim"),
            tr.get("_rel"),
            planner_score,
            bool(token_update),
        )

    # --- Run agent-specific enricher (may override RAGAS scores) ---
    agent_name = run_manager.get_run_status(run_id).get("agent", "")
    if agent_name:
        try:
            import importlib
            import yaml
            from llm.agents.common.fixtures import load_test_case

            project_root = Path(__file__).resolve().parent.parent.parent
            agent_dir = project_root / "llm" / "agents" / agent_name
            config_path = agent_dir / "config.yaml"
            if config_path.exists():
                with open(config_path) as f:
                    agent_config = yaml.safe_load(f) or {}
                enricher_path = agent_config.get("result_enricher")
                if enricher_path:
                    mod_path, func_name = enricher_path.rsplit(".", 1)
                    enricher_fn = getattr(importlib.import_module(mod_path), func_name)
                    fixtures_dir = agent_dir / "fixtures"
                    for tr in test_results:
                        tc_path = fixtures_dir / tr["test_id"] / "test_case.yaml"
                        if not tc_path.exists():
                            continue
                        try:
                            tc = load_test_case(str(tc_path))
                            result_dict = {
                                "answer": tr.get("actual_answer", ""),
                                "answer_similarity": tr.get("_sim", 0.0),
                                "answer_relevancy": tr.get("_rel", 0.0),
                            }
                            enricher_fn(result_dict, tc, llm)
                        except Exception as e:
                            logger.warning(
                                "Re-evaluate enricher failed for %s/%s: %s",
                                agent_name,
                                tr["test_id"],
                                e,
                            )
                            continue

                        run_manager.update_test_result_scores(
                            run_id,
                            tr["test_index"],
                            answer_similarity=result_dict.get("answer_similarity"),
                            answer_relevancy=result_dict.get("answer_relevancy"),
                        )
                        logger.info(
                            "Enricher updated test %s: sim=%s rel=%s",
                            tr["test_id"],
                            result_dict.get("answer_similarity"),
                            result_dict.get("answer_relevancy"),
                        )
        except Exception as e:
            logger.warning("Failed to load re-evaluate enricher for %s: %s", agent_name, e)

    run_manager.finish_re_evaluation(run_id)
    logger.info("Re-evaluation complete for run %s", run_id)


@router.get("/{run_id}/status")
async def benchmark_status(
    run_id: str,
    authz=Depends(get_authz),
):
    """Get the current status and progress of a benchmark run."""
    _verify_run_access(run_id, authz)
    status = run_manager.get_run_status(run_id)
    if "error" in status:
        raise HTTPException(status_code=404, detail=status["error"])
    return status


@router.get("/{run_id}/report")
async def download_benchmark_report(
    run_id: str,
    authz=Depends(get_authz),
):
    """Download the JSON report for a benchmark run (assembled from DB)."""
    _verify_run_access(run_id, authz)
    status = run_manager.get_run_status(run_id)
    if "error" in status:
        raise HTTPException(status_code=404, detail=status["error"])

    report_data = run_manager.get_report(run_id)
    if not report_data:
        raise HTTPException(
            status_code=404,
            detail=f"No report found for run {run_id}. "
            "The run may still be in progress or no tests completed.",
        )

    return report_data


@router.delete("/{run_id}")
async def delete_benchmark_run(
    run_id: str,
    authz=Depends(get_authz),
):
    """Remove a benchmark run and its test results from DB.

    Cannot delete a run that is still active (running/paused).
    """
    _verify_run_access(run_id, authz)
    status = run_manager.get_run_status(run_id)
    if "error" in status:
        raise HTTPException(status_code=404, detail=status["error"])
    if status.get("state") in ("running", "paused"):
        raise HTTPException(
            status_code=409,
            detail=f"Cannot delete active run {run_id} (state: {status['state']}). Stop it first.",
        )
    run_manager.cleanup_run(run_id)
    return {"run_id": run_id, "message": f"Run {run_id} removed"}


# --- Report comparison ---


class CompareRunsRequest(BaseModel):
    baseline_run_id: str
    candidate_run_id: str
    threshold: float = 5.0


class ResendReportRequest(BaseModel):
    emails: List[str]


@router.post("/{run_id}/resend-report")
async def resend_report(
    run_id: str,
    request: ResendReportRequest,
    authz=Depends(get_authz),
):
    """Resend the benchmark report email to specified addresses."""
    _verify_run_access(run_id, authz)
    report_data = run_manager.get_report(run_id)
    if not report_data:
        raise HTTPException(status_code=404, detail=f"No report found for run {run_id}")

    summary = report_data.get("summary", {})
    metadata = report_data.get("metadata", {})
    agent = metadata.get("agent", "unknown")
    run_label = metadata.get("run_name") or agent.upper()

    email_body = build_benchmark_email(report_data)

    import tempfile

    report_file = os.path.join(
        tempfile.gettempdir(),
        f"benchmark_report_{agent}_{run_id}.json",
    )
    with open(report_file, "w") as f:
        json.dump(report_data, f, indent=2)

    try:
        for email in request.emails:
            await send_email_async(
                email,
                f"{run_label} Benchmark Report - {summary.get('overall_accuracy', 'N/A')}% Accuracy",
                email_body,
                attachments=[report_file],
            )
    finally:
        if os.path.exists(report_file):
            os.remove(report_file)

    return {"message": f"Report sent to {', '.join(request.emails)}"}


@router.post("/compare-runs")
async def compare_benchmark_runs(
    request: CompareRunsRequest,
    authz=Depends(get_authz),
):
    """Compare two benchmark runs by run_id using reports assembled from DB.

    Reuses the full compare_reports engine for summary deltas, per-test
    regressions/improvements, tag breakdowns, tool name diffs, and cost.
    """
    _verify_run_access(request.baseline_run_id, authz)
    _verify_run_access(request.candidate_run_id, authz)
    from llm.agents.common.compare_reports import (
        compute_per_query_delta,
        compute_summary_delta,
        compute_tag_delta,
    )

    baseline = run_manager.get_report(request.baseline_run_id)
    if not baseline:
        raise HTTPException(
            status_code=404,
            detail=f"No report for baseline run {request.baseline_run_id}",
        )
    candidate = run_manager.get_report(request.candidate_run_id)
    if not candidate:
        raise HTTPException(
            status_code=404,
            detail=f"No report for candidate run {request.candidate_run_id}",
        )

    b_meta = baseline.get("metadata", {})
    c_meta = candidate.get("metadata", {})

    comparison = {
        "baseline": {
            "run_id": request.baseline_run_id,
            "agent": b_meta.get("agent", ""),
            "total_queries": b_meta.get("total_queries", 0),
            "timestamp": b_meta.get("timestamp", ""),
        },
        "candidate": {
            "run_id": request.candidate_run_id,
            "agent": c_meta.get("agent", ""),
            "total_queries": c_meta.get("total_queries", 0),
            "timestamp": c_meta.get("timestamp", ""),
        },
        "threshold": request.threshold,
        "summary_delta": compute_summary_delta(baseline, candidate),
        "tag_delta": compute_tag_delta(baseline, candidate),
    }

    query_results = compute_per_query_delta(baseline, candidate, request.threshold)
    comparison.update(query_results)

    return comparison
