"""VM-account scenarios: which fleet a run targets and how a fault is injected.

A `vm_event` scenario names its hosts and fault with `${NAME}` placeholders, so
the same scenario.yaml runs against any fleet registered with a forager. The
fleet is supplied through the environment, never committed: instance ids,
hostnames and SSH access are per-environment facts.

The fault itself is plain shell (`vm.inject` / `vm.restore`), typically one
`ssh <host> '<command>'` per step. Restore always runs, even when the run
fails, so a crashed scenario does not leave a host broken for the next one.
"""

from __future__ import annotations

import os
import re
import subprocess
from pathlib import Path
from typing import Any

_PLACEHOLDER = re.compile(r"\$\{([A-Za-z_][A-Za-z0-9_]*)\}")

COMMAND_TIMEOUT_SECONDS = 120
SUITE_DIR = Path(__file__).resolve().parent


def expand_env(value: Any) -> Any:
    """Replace `${NAME}` in every string of a loaded scenario. A name that is
    not set stays as the literal `${NAME}`, so the run can report it as unset
    instead of sending a nonsense host to the platform."""
    if isinstance(value, str):
        return _PLACEHOLDER.sub(lambda m: os.environ.get(m.group(1), m.group(0)), value)
    if isinstance(value, list):
        return [expand_env(v) for v in value]
    if isinstance(value, dict):
        return {k: expand_env(v) for k, v in value.items()}
    return value


def unresolved_placeholders(value: Any) -> set[str]:
    """Names of the `${NAME}` placeholders still present after expand_env."""
    if isinstance(value, str):
        return set(_PLACEHOLDER.findall(value))
    if isinstance(value, list):
        return (
            set().union(*(unresolved_placeholders(v) for v in value))
            if value
            else set()
        )
    if isinstance(value, dict):
        return (
            set().union(*(unresolved_placeholders(v) for v in value.values()))
            if value
            else set()
        )
    return set()


def run_commands(commands: list[str]) -> None:
    """Run each shell command in order, from the suite directory so scenarios
    can call `vm/fault.sh`; the first failure raises."""
    for command in commands:
        try:
            subprocess.run(
                command,
                shell=True,
                check=True,
                capture_output=True,
                text=True,
                timeout=COMMAND_TIMEOUT_SECONDS,
                cwd=SUITE_DIR,
            )
        except subprocess.CalledProcessError as exc:
            # CalledProcessError's own message omits the captured stderr, which
            # is what says why ssh or sudo refused.
            stderr = (exc.stderr or "").strip()
            raise subprocess.SubprocessError(
                f"exited {exc.returncode}: {stderr or 'no stderr'}"
            ) from exc


def restore_commands(commands: list[str]) -> str | None:
    """Run every restore command even if one fails, since a half-restored host
    is worse than a noisy log. Returns the failures joined, or None."""
    failures = []
    for command in commands:
        try:
            run_commands([command])
        except (subprocess.SubprocessError, OSError) as exc:
            failures.append(f"{command!r}: {exc}")
    return "; ".join(failures) or None
