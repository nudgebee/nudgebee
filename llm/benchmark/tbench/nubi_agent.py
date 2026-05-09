"""
NuBi terminal-bench agent.

Bridges terminal-bench's BaseAgent interface to NuBi's HTTP API using the
client-tool protocol: NuBi reasons and plans; each shell_execute call is
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
import os
import time
from pathlib import Path

import httpx
from terminal_bench.agents.base_agent import AgentResult, BaseAgent
from terminal_bench.agents.failure_mode import FailureMode
from terminal_bench.terminal.models import TerminalCommand
from terminal_bench.terminal.tmux_session import TmuxSession

logger = logging.getLogger(__name__)

_SHELL_TOOL_SCHEMA = {
    "name": "shell_execute",
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

_DEFAULT_CMD_TIMEOUT = float(os.environ.get("NUBI_CMD_TIMEOUT", "600"))


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
        self._task_timeout = int(os.environ.get("NUBI_TASK_TIMEOUT", "1800"))
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
        with httpx.Client(timeout=30.0) as client:
            conv_id = self._start_conversation(client, instruction)
            if not conv_id:
                return AgentResult(failure_mode=FailureMode.UNKNOWN_AGENT_ERROR)

            self._log(log_path, f"[start] conversation_id={conv_id}")

            deadline = time.monotonic() + self._task_timeout
            while time.monotonic() < deadline:
                conv = self._poll(client, conv_id)
                if conv is not None:
                    status = self._top_status(conv)
                    self._log(log_path, f"[poll] status={status}")

                    if status == _WAITING_FOR_TOOL:
                        self._execute_client_tools(
                            client, conv, conv_id, session, log_path
                        )
                    elif status in _TERMINAL_STATUS_DONE:
                        return AgentResult()
                    elif status in _TERMINAL_STATUS_FAIL:
                        return AgentResult(failure_mode=FailureMode.UNKNOWN_AGENT_ERROR)

                time.sleep(self._poll_interval)

            self._log(log_path, "[timeout] task did not complete in time")
            return AgentResult(failure_mode=FailureMode.AGENT_TIMEOUT)

    # ------------------------------------------------------------------
    # NuBi API calls
    # ------------------------------------------------------------------

    def _start_conversation(self, client: httpx.Client, instruction: str) -> str | None:
        try:
            resp = client.post(
                f"{self._url}/v1/completions/chat",
                headers=self._headers,
                json={
                    "query": f"@{self._agent_name} {instruction}",
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

    def _poll(self, client: httpx.Client, conv_id: str) -> dict | None:
        try:
            resp = client.post(
                f"{self._url}/v1/completions/chat_get",
                headers=self._headers,
                json={"conversation_id": conv_id, "account_id": self._account_id},
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

                    output = self._run_in_terminal(session, command)
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

    def _run_in_terminal(self, session: TmuxSession, command: str) -> str:
        if not command:
            return ""
        # tb's blocking implementation appends "; tmux wait -S done" to the
        # last line of the command (see tmux_session.py _prepare_keys). For
        # multi-line commands this pastes the trailer onto e.g. an "EOF"
        # heredoc terminator, breaking the syntax and hanging bash for the
        # full per-command timeout. Wrap any multi-line command in a single
        # `bash <<<` invocation via base64 so the trailer never lands inside
        # the user's command body.
        if "\n" in command:
            b64 = base64.b64encode(command.encode("utf-8")).decode("ascii")
            command = f"printf %s '{b64}' | base64 -d | bash"
        session.get_incremental_output()  # drain buffer before running
        try:
            session.send_command(
                TerminalCommand(
                    command=command, block=True, max_timeout_sec=_DEFAULT_CMD_TIMEOUT
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
                f"{int(_DEFAULT_CMD_TIMEOUT)}s and was interrupted.\n"
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
