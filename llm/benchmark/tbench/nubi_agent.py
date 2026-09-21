"""
NuBi terminal-bench agent.

Bridges terminal-bench's BaseAgent interface to NuBi's HTTP API using the
client-tool protocol: NuBi reasons and plans; each tbench_shell_execute call is
intercepted, executed in the real terminal-bench Docker container via TmuxSession,
and the output is fed back to NuBi before it continues.

Required env vars:
    NUBI_URL          NuBi base URL, e.g. http://127.0.0.1:8005
    NUBI_TOKEN        Auth token value
    NUBI_ACCOUNT_ID   cloud_accounts.id where the @tbench agent is installed
    NUBI_TENANT_ID    tenant id that owns the account

Optional env vars:
    NUBI_TOKEN_HEADER   Header name (default: X-ACTION-TOKEN)
    NUBI_USER_ID        User id (default: "" -> tenant-admin path)
    NUBI_AGENT_NAME     NuBi agent to route requests to (default: tbench)
    NUBI_POLL_INTERVAL  Seconds between chat_get polls (default: 2)
    NUBI_CMD_TIMEOUT    Per-shell-command wallclock cap (default: 600)
    NUBI_TASK_TIMEOUT   Max seconds per task (default: 1800)

Usage:
    tb run \\
      --dataset terminal-bench-core==0.1.1 \\
      --agent-import-path tbench.nubi_agent:NuBiAgent
"""

import base64
import json
import logging
import math
import os
import re
import tempfile
import threading
import time
import uuid
from pathlib import Path

import httpx
from terminal_bench.agents.base_agent import AgentResult, BaseAgent
from terminal_bench.agents.failure_mode import FailureMode
from terminal_bench.terminal.models import TerminalCommand
from terminal_bench.terminal.tmux_session import TmuxSession

logger = logging.getLogger(__name__)

_SHELL_TOOL_SCHEMA = {
    "name": "tbench_shell_execute",
    "description": (
        "Execute a non-interactive shell command in the terminal environment "
        "and return the combined stdout/stderr output. Chain multiple commands "
        "with && when order matters. Do not use interactive tools (vim, top, python REPL)."
    ),
    "input": {
        "type": "object",
        "properties": {
            "command": {
                "type": "string",
                "description": "Shell command to execute, e.g. 'ls -la /tmp && cat /etc/os-release'",
            }
        },
        "required": ["command"],
    },
}

_TERMINAL_STATUS_DONE = {"COMPLETED"}
_TERMINAL_STATUS_FAIL = {"FAILED", "TERMINATED", "KILLED"}
_WAITING_FOR_TOOL = "WAITING_FOR_CLIENT_TOOL"
_AGENT_WAITING = "waiting_for_client_tool"


def _positive_float_env(name: str, default: str) -> float:
    raw = os.environ.get(name, default)
    try:
        value = float(raw)
    except ValueError as exc:
        raise ValueError(
            f"Invalid {name}: {raw!r}; expected a positive number"
        ) from exc
    if not math.isfinite(value) or value <= 0:
        raise ValueError(f"Invalid {name}: {raw!r}; expected a positive number")
    return value


_DEFAULT_CMD_TIMEOUT = float(os.environ.get("NUBI_CMD_TIMEOUT", "600"))
_ORPHAN_CHECK_INTERVAL = _positive_float_env("NUBI_ORPHAN_CHECK_INTERVAL", "2")
_TIMEOUT_GRACE = float(os.environ.get("NUBI_TIMEOUT_GRACE", "15"))

# Commands are delivered to the container via copy_to_container + `bash <script>`
# by default (NUBI_SHELL_INLINE_THRESHOLD=0). The keystroke stream only ever
# carries a short `bash /tmp/nubi_cmd_X.sh` invocation, so tb's "; tmux wait -S
# done" trailer always lands cleanly regardless of the LLM's command body.
#
# Why all-tempfile by default:
#   - Multi-line bodies (heredocs, case branches): trailer lands inside the
#     body and hangs bash for the full per-command timeout.
#   - Long one-liners: the send-keys streaming path can deadlock on ~10KB
#     content (observed on heredocs in baseline runs; see baseline_report.md
#     issue #5).
#   - Even small one-liners can flake: a 14-byte `printf > file` hung in a
#     hello-world run when run as the second command of a session — likely a
#     state-dependent tmux interaction. Always-tempfile sidesteps the entire
#     class.
#
# Cost is ~100-300ms per command (Docker exec for copy + rm) — negligible
# vs the 5-50s LLM round-trip per turn. Soft cost: agent.cast asciinema
# recording shows `bash /tmp/nubi_cmd_X.sh` instead of typed commands; the
# original bodies are still preserved in commands.txt and nubi_agent.log
# `[exec]` lines.
#
# Set NUBI_SHELL_INLINE_THRESHOLD to a positive byte count to send commands
# strictly shorter than that (and without literal newlines) inline via tmux
# send-keys instead. Provided as an emergency knob; not recommended for
# normal benchmark runs.
_SHELL_INLINE_THRESHOLD = int(os.environ.get("NUBI_SHELL_INLINE_THRESHOLD", "0"))
_CONTAINER_SCRIPT_DIR = "/tmp"
_NONINTERACTIVE_ENV = "export PAGER=cat GIT_PAGER=cat SYSTEMD_PAGER=cat MANPAGER=cat"
_ENVIRONMENT_EXECUTABLES = (
    "bash sh cat ls find grep sed awk sort uniq wc head tail jq "
    "printf chmod cp mv rm mkdir touch date "
    "python3 python node npm go ruby rustc gcc g++ make git curl wget openssl "
    "iptables nft ufw fail2ban"
)
_VERSION_EXECUTABLES = (
    "python3 python node npm go ruby rustc gcc g++ make git curl wget openssl"
)


def _extract_balanced_json_object(s: str) -> str | None:
    """Find the first balanced `{...}` substring at the JSON-object level.

    The planner occasionally emits the `tool_input` JSON envelope with trailing
    junk (e.g. a stray `]]>` from a CDATA artifact) or the LLM wraps the schema
    around the bare command. This walks the string char-by-char honoring string
    literals + escapes so an embedded `}` doesn't fool us, and returns the
    matched object substring.
    """
    start = s.find("{")
    if start < 0:
        return None
    depth = 0
    in_string = False
    escape = False
    for i in range(start, len(s)):
        c = s[i]
        if escape:
            escape = False
            continue
        if c == "\\":
            escape = True
            continue
        if c == '"':
            in_string = not in_string
            continue
        if in_string:
            continue
        if c == "{":
            depth += 1
        elif c == "}":
            depth -= 1
            if depth == 0:
                return s[start : i + 1]
    return None


class NuBiAgent(BaseAgent):
    """terminal-bench BaseAgent backed by NuBi via the client-tool protocol."""

    @staticmethod
    def name() -> str:
        return "nubi"

    def __init__(self, **kwargs) -> None:
        super().__init__(**kwargs)
        self._url = os.environ["NUBI_URL"].rstrip("/")
        self._token_header = os.environ.get("NUBI_TOKEN_HEADER", "X-ACTION-TOKEN")
        self._token = os.environ["NUBI_TOKEN"]
        self._account_id = os.environ["NUBI_ACCOUNT_ID"]
        self._tenant_id = os.environ["NUBI_TENANT_ID"]
        self._user_id = os.environ.get("NUBI_USER_ID", "")
        self._poll_interval = int(os.environ.get("NUBI_POLL_INTERVAL", "2"))
        timeout_override = os.environ.get("NUBI_TASK_TIMEOUT")
        self._task_timeout_override = (
            _positive_float_env("NUBI_TASK_TIMEOUT", "1800")
            if timeout_override is not None
            else None
        )
        self._task_timeout = self._task_timeout_override or 1800.0
        self._agent_name = os.environ.get("NUBI_AGENT_NAME", "tbench")

    @property
    def _headers(self) -> dict:
        return {
            self._token_header: self._token,
            "x-tenant-id": self._tenant_id,
            "Content-Type": "application/json",
        }

    # ------------------------------------------------------------------
    # BaseAgent interface
    # ------------------------------------------------------------------

    def perform_task(
        self,
        instruction: str,
        session: TmuxSession,
        logging_dir: Path | None = None,
    ) -> AgentResult:
        log_path = logging_dir / "nubi_agent.log" if logging_dir else None
        if logging_dir:
            logging_dir.mkdir(parents=True, exist_ok=True)

        try:
            return self._run(instruction, session, log_path)
        except Exception as exc:
            logger.exception("nubi_agent: unexpected error")
            self._log(log_path, f"[error] {exc}")
            return AgentResult(failure_mode=FailureMode.UNKNOWN_AGENT_ERROR)

    # ------------------------------------------------------------------
    # Core loop
    # ------------------------------------------------------------------

    def _run(
        self, instruction: str, session: TmuxSession, log_path: Path | None
    ) -> AgentResult:
        environment = self._discover_environment(session)
        self._log(log_path, f"[environment] {environment.replace(chr(10), '; ')}")
        with httpx.Client(timeout=30.0) as client:
            conv_id = self._start_conversation(client, instruction, environment)
            if not conv_id:
                return AgentResult(failure_mode=FailureMode.UNKNOWN_AGENT_ERROR)

            self._log(log_path, f"[start] conversation_id={conv_id}")

            # terminal-bench runs perform_task() in an executor thread. Its
            # asyncio timeout stops awaiting that thread but cannot cancel the
            # synchronous function. Without an independent lifecycle watcher,
            # a timed-out NuBi conversation keeps running while terminal-bench
            # starts the test session (and can remain WAITING_FOR_CLIENT_TOOL
            # after the trial container is removed).
            watchdog_stop = threading.Event()
            watchdog = threading.Thread(
                target=self._watch_for_harness_exit,
                args=(conv_id, session, watchdog_stop, log_path),
                daemon=True,
                name=f"nubi-tbench-watchdog-{conv_id[:8]}",
            )
            watchdog.start()

            try:
                task_timeout, timeout_source = self._resolve_task_timeout(log_path)
                self._log(
                    log_path,
                    f"[budget] adapter={task_timeout:g}s source={timeout_source}",
                )
                deadline = time.monotonic() + task_timeout
                while time.monotonic() < deadline:
                    remaining = deadline - time.monotonic()
                    conv = self._poll(
                        client, conv_id, timeout_sec=min(30.0, max(1.0, remaining))
                    )
                    if conv is not None:
                        status = self._top_status(conv)
                        self._log(log_path, f"[poll] status={status}")

                        if status == _WAITING_FOR_TOOL:
                            self._execute_client_tools(
                                client,
                                conv,
                                conv_id,
                                session,
                                log_path,
                                deadline,
                            )
                        elif status in _TERMINAL_STATUS_DONE:
                            return AgentResult()
                        elif status in _TERMINAL_STATUS_FAIL:
                            return AgentResult(
                                failure_mode=FailureMode.UNKNOWN_AGENT_ERROR
                            )

                    remaining = deadline - time.monotonic()
                    if remaining > 0:
                        time.sleep(min(self._poll_interval, remaining))

                self._log(log_path, "[timeout] task did not complete in time")
                self._stop_conversation(client, conv_id, log_path)
                return AgentResult(failure_mode=FailureMode.AGENT_TIMEOUT)
            finally:
                watchdog_stop.set()
                watchdog.join(timeout=_ORPHAN_CHECK_INTERVAL + 1)

    def _resolve_task_timeout(self, log_path: Path | None) -> tuple[float, str]:
        """Finish before terminal-bench's effective agent timeout.

        terminal-bench stores the run-wide override/multiplier in ``tb.lock``
        and the native timeout in the selected task's ``task.yaml``. Reading
        those artifacts keeps this adapter aligned with mixed-timeout datasets
        without changing benchmark limits.
        """
        if self._task_timeout_override is not None:
            return self._task_timeout_override, "env"
        if log_path is None:
            return self._task_timeout, "default"

        try:
            run_dir = log_path.parents[3]
            task_id = log_path.parents[2].name
            lock = json.loads((run_dir / "tb.lock").read_text())
            if not isinstance(lock, dict):
                raise ValueError("tb.lock root must be an object")
            run_config = lock.get("run_config")
            if not isinstance(run_config, dict):
                run_config = {}
            harness_timeout = run_config.get("global_agent_timeout_sec")
            source = "tb.lock:global"

            if harness_timeout is None:
                dataset = lock.get("dataset")
                if not isinstance(dataset, dict):
                    dataset = {}
                local_path = dataset.get("local_path")
                task_yaml = None
                if local_path:
                    task_yaml = Path(local_path) / task_id / "task.yaml"
                elif "name" in dataset and "version" in dataset:
                    task_yaml = (
                        Path.home()
                        / ".cache"
                        / "terminal-bench"
                        / str(dataset["name"])
                        / str(dataset["version"])
                        / task_id
                        / "task.yaml"
                    )
                match = None
                if task_yaml is not None and task_yaml.exists():
                    match = re.search(
                        r"^max_agent_timeout_sec:\s*([0-9.]+)\s*$",
                        task_yaml.read_text(),
                        flags=re.MULTILINE,
                    )
                native_timeout = float(match.group(1)) if match else 360.0
                multiplier = float(run_config.get("global_timeout_multiplier") or 1.0)
                harness_timeout = native_timeout * multiplier
                source = "task.yaml"

            return max(1.0, float(harness_timeout) - _TIMEOUT_GRACE), source
        except Exception as exc:
            logger.warning(
                "nubi_agent: unable to resolve terminal-bench timeout: %s", exc
            )
            return self._task_timeout, "default"

    # ------------------------------------------------------------------
    # NuBi API calls
    # ------------------------------------------------------------------

    def _start_conversation(
        self, client: httpx.Client, instruction: str, environment: str
    ) -> str | None:
        query = (
            f"@{self._agent_name} {instruction}\n\n"
            "<terminal_environment>\n"
            "The benchmark adapter already verified these capabilities in the "
            "task container:\n"
            f"{environment}\n"
            "Reuse this map. Do not repeat generic OS, working-directory, or "
            "executable discovery unless a command contradicts it.\n"
            "</terminal_environment>"
        )
        try:
            resp = client.post(
                f"{self._url}/v1/completions/chat",
                headers=self._headers,
                json={
                    "query": query,
                    "account_id": self._account_id,
                    "user_id": self._user_id,
                    "tenant_id": self._tenant_id,
                    "async": True,
                    "client_tools": [_SHELL_TOOL_SCHEMA],
                },
            )
            resp.raise_for_status()
            return (resp.json().get("data") or {}).get("conversation_id")
        except Exception as exc:
            logger.error("nubi_agent: start_conversation failed: %s", exc)
            return None

    def _discover_environment(self, session: TmuxSession) -> str:
        """Return a bounded, non-secret capability map from the task container."""
        probe = (
            'printf "working_directory=%s\\n" "$PWD"; '
            'printf "kernel="; uname -srm 2>/dev/null || printf "unavailable\\n"; '
            "if [ -r /etc/os-release ]; then "
            "sed -n 's/^PRETTY_NAME=/os=/p' /etc/os-release | head -1; "
            "fi; "
            f'printf "checked_executables={_ENVIRONMENT_EXECUTABLES}\\n"; '
            'printf "available_executables="; first=1; missing=""; '
            f"for name in {_ENVIRONMENT_EXECUTABLES}; do "
            'path=$(command -v "$name" 2>/dev/null) || { missing="$missing $name"; continue; }; '
            'if [ "$first" -eq 0 ]; then printf ","; fi; '
            'printf "%s:%s" "$name" "$path"; first=0; '
            'done; printf "\\nunavailable_executables=%s\\n" "${missing# }"; '
            'printf "versions="; first=1; '
            f"for name in {_VERSION_EXECUTABLES}; do "
            'command -v "$name" >/dev/null 2>&1 || continue; '
            'case "$name" in '
            "go) version=$(go version 2>&1 | head -1) ;; "
            "openssl) version=$(openssl version 2>&1 | head -1) ;; "
            '*) version=$("$name" --version 2>&1 | head -1) ;; '
            "esac; "
            'if [ "$first" -eq 0 ]; then printf " | "; fi; '
            'printf "%s:%s" "$name" "$version"; first=0; '
            'done; printf "\\ncurrent_directory_listing:\\n"; '
            "ls -la . 2>/dev/null | head -40 | sed 's/^/  /'"
        )
        try:
            result = session.container.exec_run(["sh", "-lc", probe])
            if result.exit_code != 0:
                raise RuntimeError(f"probe exited {result.exit_code}")
            output = result.output
            if isinstance(output, bytes):
                output = output.decode("utf-8", errors="replace")
            output = re.sub(r"[\x00-\x08\x0b\x0c\x0e-\x1f\x7f]", "?", str(output))
            lines = [line.rstrip() for line in output.splitlines() if line.strip()]
            truncated = "\n".join(lines)[:6000]
            escaped = (
                truncated.replace("&", "&amp;")
                .replace("<", "&lt;")
                .replace(">", "&gt;")
            )
            return escaped or "capability_map=unavailable"
        except Exception as exc:
            logger.warning("nubi_agent: environment discovery failed: %s", exc)
            return "capability_map=unavailable"

    def _poll(
        self,
        client: httpx.Client,
        conv_id: str,
        timeout_sec: float = 30.0,
    ) -> dict | None:
        try:
            resp = client.post(
                f"{self._url}/v1/completions/chat_get",
                headers=self._headers,
                json={"conversation_id": conv_id, "account_id": self._account_id},
                timeout=timeout_sec,
            )
            resp.raise_for_status()
            return resp.json()
        except Exception as exc:
            logger.warning("nubi_agent: poll error: %s", exc)
            return None

    def _submit_tool_results(
        self,
        client: httpx.Client,
        conv_id: str,
        message_id: str,
        agent_id: str,
        results: list[dict],
    ) -> None:
        try:
            resp = client.post(
                f"{self._url}/v1/completions/client-tool-result",
                headers=self._headers,
                json={
                    "conversation_id": conv_id,
                    "message_id": message_id,
                    "agent_id": agent_id,
                    "account_id": self._account_id,
                    "async": True,
                    "results": results,
                },
            )
            resp.raise_for_status()
        except Exception as exc:
            logger.error("nubi_agent: submit_tool_results failed: %s", exc)

    def _stop_conversation(
        self,
        client: httpx.Client,
        conv_id: str,
        log_path: Path | None,
    ) -> bool:
        """Best-effort, idempotent termination of server-side benchmark work."""
        try:
            resp = client.post(
                f"{self._url}/v1/completions/chat_stop",
                headers=self._headers,
                json={
                    "conversation_id": conv_id,
                    "account_id": self._account_id,
                    "user_id": self._user_id,
                },
                timeout=10.0,
            )
            resp.raise_for_status()
            self._log(log_path, "[stop] conversation terminated")
            return True
        except Exception as exc:
            logger.warning("nubi_agent: stop_conversation failed: %s", exc)
            self._log(log_path, f"[stop-error] {exc}")
            return False

    def _watch_for_harness_exit(
        self,
        conv_id: str,
        session: TmuxSession,
        stop: threading.Event,
        log_path: Path | None,
    ) -> None:
        """Stop NuBi when terminal-bench moves from the agent to its tests.

        asyncio cannot kill the executor thread running this synchronous agent.
        The appearance of terminal-bench's ``tests`` tmux session is therefore
        the first reliable signal that the harness has abandoned perform_task.
        Container removal is the fallback signal for setup/test failures.
        """
        unavailable_checks = 0
        while not stop.wait(_ORPHAN_CHECK_INTERVAL):
            try:
                session.container.reload()
                container_running = session.container.status == "running"
                tests_started = False
                if container_running:
                    result = session.container.exec_run(
                        ["tmux", "has-session", "-t", "tests"]
                    )
                    tests_started = result.exit_code == 0
            except Exception:
                container_running = False
                tests_started = False

            if container_running and not tests_started:
                unavailable_checks = 0
                continue

            if not container_running:
                unavailable_checks += 1
                # Do not kill a healthy task because of one transient Docker
                # API error. A removed container remains unavailable.
                if unavailable_checks < 2:
                    continue

            reason = "tests_started" if tests_started else "container_stopped"
            self._log(log_path, f"[watchdog] harness exited agent phase: {reason}")
            if tests_started:
                try:
                    session.container.exec_run(
                        ["tmux", "send-keys", "-t", "agent", "C-c"]
                    )
                except Exception:
                    logger.warning(
                        "nubi_agent: failed to interrupt orphaned agent command"
                    )

            with httpx.Client(timeout=30.0) as client:
                self._stop_conversation(client, conv_id, log_path)
            return

    # ------------------------------------------------------------------
    # Client-tool execution
    # ------------------------------------------------------------------

    def _execute_client_tools(
        self,
        client: httpx.Client,
        conv: dict,
        conv_id: str,
        session: TmuxSession,
        log_path: Path | None,
        deadline: float,
    ) -> None:
        data = conv.get("data", conv)
        messages = data.get("llm_conversation_messages", [])

        for msg in messages:
            for agent in msg.get("llm_conversation_agents", []):
                if agent.get("status") != _AGENT_WAITING:
                    continue

                agent_id = str(agent.get("id", ""))
                message_id = str(agent.get("message_id", ""))
                tool_calls = self._parse_tool_calls(agent.get("agent_step_response"))
                if not tool_calls:
                    continue

                results = []
                for tc in tool_calls:
                    tool_id = str(tc.get("tool_id", ""))
                    command = self._extract_command(tc.get("tool_input"))
                    self._log(log_path, f"[exec] {command!r}")

                    remaining = max(1.0, deadline - time.monotonic())
                    output = self._run_in_terminal(
                        session,
                        command,
                        max_timeout_sec=min(_DEFAULT_CMD_TIMEOUT, remaining),
                    )
                    self._log(log_path, f"[output] {len(output)} chars")
                    results.append(
                        {"tool_id": tool_id, "result": output, "status": "SUCCESS"}
                    )

                if results:
                    self._submit_tool_results(
                        client, conv_id, message_id, agent_id, results
                    )
                    return  # submit one agent's tools per poll cycle

    def _parse_tool_calls(self, step_response: str | list | None) -> list[dict]:
        if step_response is None:
            return []
        if isinstance(step_response, list):
            return [tc for tc in step_response if isinstance(tc, dict)]
        try:
            parsed = json.loads(step_response)
        except (json.JSONDecodeError, TypeError):
            return []
        if isinstance(parsed, list):
            return [tc for tc in parsed if isinstance(tc, dict)]
        return [parsed] if isinstance(parsed, dict) else []

    def _extract_command(self, tool_input: str | dict | None) -> str:
        if not tool_input:
            return ""
        if isinstance(tool_input, dict):
            return tool_input.get("command", "")
        if not isinstance(tool_input, str):
            return str(tool_input)

        # Direct parse — the well-formed case.
        try:
            parsed = json.loads(tool_input)
        except (json.JSONDecodeError, ValueError):
            parsed = None
        if isinstance(parsed, dict):
            return parsed.get("command", "")
        if parsed is not None:
            return str(parsed)

        # Fallback: planner sometimes emits trailing artifacts (e.g. "]]>") or
        # extra noise after the JSON object. Locate the first balanced {...}
        # substring and try to parse just that.
        candidate = _extract_balanced_json_object(tool_input)
        if candidate is not None:
            try:
                parsed = json.loads(candidate)
                if isinstance(parsed, dict) and "command" in parsed:
                    return parsed["command"]
            except (json.JSONDecodeError, ValueError):
                pass
        return tool_input

    def _run_in_terminal(
        self,
        session: TmuxSession,
        command: str,
        max_timeout_sec: float = _DEFAULT_CMD_TIMEOUT,
    ) -> str:
        if not command:
            return ""
        command = self._with_noninteractive_env(command)
        if self._needs_script_delivery(command):
            delivered = self._deliver_via_script(session, command)
            if delivered is not None:
                return self._send_and_capture(
                    session, delivered, max_timeout_sec=max_timeout_sec
                )
            # copy_to_container failed — fall back to base64 inline.
            b64 = base64.b64encode(command.encode("utf-8")).decode("ascii")
            command = f"printf %s '{b64}' | base64 -d | bash"
        return self._send_and_capture(session, command, max_timeout_sec=max_timeout_sec)

    @staticmethod
    def _with_noninteractive_env(command: str) -> str:
        """Disable implicit pagers while preserving the command's shell syntax."""
        return f"{_NONINTERACTIVE_ENV}\n{command}"

    @staticmethod
    def _needs_script_delivery(command: str) -> bool:
        # Default (NUBI_SHELL_INLINE_THRESHOLD=0): always go via tempfile.
        # The keystroke stream then only ever carries `bash /tmp/X.sh`, so
        # tb's "; tmux wait -S done" trailer can never land inside a
        # heredoc / case-branch body and never has to stream large content.
        # When threshold > 0, allow strictly-shorter newline-free commands
        # to be sent inline as a small per-command latency optimisation.
        if _SHELL_INLINE_THRESHOLD <= 0:
            return True
        return "\n" in command or len(command) >= _SHELL_INLINE_THRESHOLD

    def _deliver_via_script(self, session: TmuxSession, command: str) -> str | None:
        """Drop `command` into a script inside the container; return the bash
        invocation that runs it, or None if delivery failed.
        """
        script_id = uuid.uuid4().hex[:12]
        container_filename = f"nubi_cmd_{script_id}.sh"
        try:
            with tempfile.NamedTemporaryFile(
                mode="w", suffix=".sh", delete=False, encoding="utf-8"
            ) as fh:
                fh.write("#!/usr/bin/env bash\n")
                fh.write(command)
                if not command.endswith("\n"):
                    fh.write("\n")
                local_path = Path(fh.name)
            session.copy_to_container(
                local_path,
                container_dir=_CONTAINER_SCRIPT_DIR,
                container_filename=container_filename,
            )
        except Exception as exc:
            logger.warning(
                "nubi_agent: copy_to_container failed, falling back to inline send: %s",
                exc,
            )
            return None
        finally:
            try:
                local_path.unlink(missing_ok=True)  # type: ignore[name-defined]
            except Exception:
                pass
        # Cleanup the in-container script after running so we don't accumulate
        # files across many turns. `rm -f` on a fixed path can't fail-out the
        # surrounding bash.
        return (
            f"bash {_CONTAINER_SCRIPT_DIR}/{container_filename}; "
            f"rc=$?; rm -f {_CONTAINER_SCRIPT_DIR}/{container_filename}; (exit $rc)"
        )

    def _send_and_capture(
        self,
        session: TmuxSession,
        command: str,
        max_timeout_sec: float = _DEFAULT_CMD_TIMEOUT,
    ) -> str:
        session.get_incremental_output()  # drain buffer before running
        try:
            session.send_command(
                TerminalCommand(
                    command=command, block=True, max_timeout_sec=max_timeout_sec
                )
            )
        except TimeoutError:
            # Free the prompt so the next command starts cleanly, then return
            # whatever output we captured so NuBi can decide what to do next.
            try:
                session.send_keys(["C-c"], block=False)
            except Exception:
                logger.warning("nubi_agent: failed to send Ctrl-C after timeout")
            partial = session.get_incremental_output()
            return (
                f"[TIMEOUT] command did not finish within "
                f"{int(max_timeout_sec)}s and was interrupted.\n"
                f"Partial output:\n{partial}"
            )
        return session.get_incremental_output()

    # ------------------------------------------------------------------
    # Helpers
    # ------------------------------------------------------------------

    def _top_status(self, conv: dict) -> str:
        data = conv.get("data", conv)
        if isinstance(data, dict):
            return str(data.get("status", "")).upper()
        return ""

    def _log(self, log_path: Path | None, message: str) -> None:
        logger.info(message)
        if log_path:
            with log_path.open("a") as fh:
                fh.write(f"{time.strftime('%H:%M:%S')} {message}\n")
