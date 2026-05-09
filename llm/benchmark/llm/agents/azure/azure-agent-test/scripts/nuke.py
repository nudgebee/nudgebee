#!/usr/bin/env python3
"""Nuke all azure-agent-test resource groups using az CLI.

Usage:
    python nuke.py              # interactive confirmation
    python nuke.py --yes        # skip confirmation (for CI/automation)
"""

import argparse
import json
import os
import subprocess
import sys
import time

SUBSCRIPTION = os.environ.get(
    "AZURE_SUBSCRIPTION", "19e207a9-769d-4afd-b261-10bbed2d43e8"
)
RG_PREFIX = "azure-agent-test"
MANAGED_BY_TAG = "nudgebee-benchmark"
WAIT_TIMEOUT = int(os.environ.get("NUKE_WAIT_TIMEOUT", "600"))
POLL_INTERVAL = 15


def az(*args):
    """Run an az CLI command and return parsed JSON output."""
    cmd = ["az"] + list(args) + ["--subscription", SUBSCRIPTION, "-o", "json"]
    result = subprocess.run(cmd, capture_output=True, text=True)
    if result.returncode != 0:
        raise RuntimeError(f"az {' '.join(args)} failed: {result.stderr.strip()}")
    return json.loads(result.stdout) if result.stdout.strip() else None


def get_resource_groups():
    """Find azure-agent-test resource groups. Prefers tagged, falls back to prefix."""
    try:
        # Try tagged first
        tagged_query = (
            f"[?starts_with(name, '{RG_PREFIX}-') "
            f"&& tags.\"managed-by\" == '{MANAGED_BY_TAG}'].name"
        )
        tagged = az("group", "list", "--query", tagged_query) or []
        if tagged:
            # Check if there are untagged ones we're skipping
            all_query = f"[?starts_with(name, '{RG_PREFIX}-')].name"
            all_rgs = az("group", "list", "--query", all_query) or []
            skipped = len(all_rgs) - len(tagged)
            if skipped:
                print(f"    Skipped {skipped} resource group(s) without managed-by={MANAGED_BY_TAG} tag")
            return sorted(tagged)

        # No tagged RGs — fall back to prefix match (handles old/untagged resources)
        all_query = f"[?starts_with(name, '{RG_PREFIX}-')].name"
        all_rgs = az("group", "list", "--query", all_query) or []
        if all_rgs:
            print(f"    No tagged RGs found, falling back to prefix match ({len(all_rgs)} RG(s))")
        return sorted(all_rgs)
    except RuntimeError as e:
        print(f"    Warning: failed to list resource groups: {e}")
        return []


def rg_exists(rg_name):
    """Check if a resource group still exists."""
    result = subprocess.run(
        ["az", "group", "exists", "--name", rg_name, "--subscription", SUBSCRIPTION],
        capture_output=True, text=True,
    )
    return result.stdout.strip().lower() == "true"


def delete_rg_and_wait(rg_name):
    """Delete a resource group and wait for completion."""
    print(f"==> Deleting {rg_name}...")
    try:
        subprocess.run(
            ["az", "group", "delete", "--name", rg_name, "--yes", "--no-wait",
             "--subscription", SUBSCRIPTION],
            capture_output=True, text=True, check=True,
        )
    except subprocess.CalledProcessError as e:
        print(f"    WARNING: Failed to start delete for {rg_name}: {e.stderr}")
        return False

    elapsed = 0
    while elapsed < WAIT_TIMEOUT:
        if not rg_exists(rg_name):
            print(f"    {rg_name} deleted.")
            return True
        print(f"    {rg_name}: deleting ({elapsed}s/{WAIT_TIMEOUT}s)")
        time.sleep(POLL_INTERVAL)
        elapsed += POLL_INTERVAL

    print(f"    WARNING: {rg_name} timed out after {WAIT_TIMEOUT}s")
    return False


def main():
    parser = argparse.ArgumentParser(description="Nuke azure-agent-test resource groups")
    parser.add_argument("--yes", "-y", action="store_true", help="Skip confirmation")
    args = parser.parse_args()

    print("==> Finding all azure-agent-test resource groups...")
    rgs = get_resource_groups()

    if not rgs:
        print("    No resource groups found.")
        return

    print("Found resource groups:")
    for rg in rgs:
        print(f"    {rg}")

    if not args.yes:
        confirm = input("Delete ALL of these? (y/N) ").strip().lower()
        if confirm not in ("y", "yes"):
            print("Aborted.")
            return

    # Separate bootstrap from scenario resource groups
    bootstrap = f"{RG_PREFIX}-bootstrap"
    scenario_rgs = [rg for rg in rgs if rg != bootstrap]
    has_bootstrap = bootstrap in rgs
    failed = []

    # Start all scenario deletes (--no-wait makes them async)
    for rg in scenario_rgs:
        print(f"==> Starting delete for {rg}...")
        try:
            subprocess.run(
                ["az", "group", "delete", "--name", rg, "--yes", "--no-wait",
                 "--subscription", SUBSCRIPTION],
                capture_output=True, text=True, check=True,
            )
        except subprocess.CalledProcessError as e:
            print(f"    WARNING: Failed to start delete for {rg}: {e.stderr}")
            failed.append(rg)

    # Wait for all scenario deletes
    for rg in scenario_rgs:
        if rg in failed:
            continue
        print(f"    Waiting for {rg}...")
        elapsed = 0
        while elapsed < WAIT_TIMEOUT:
            if not rg_exists(rg):
                print(f"    {rg} deleted.")
                break
            print(f"    {rg}: deleting ({elapsed}s/{WAIT_TIMEOUT}s)")
            time.sleep(POLL_INTERVAL)
            elapsed += POLL_INTERVAL
        else:
            print(f"    WARNING: {rg} timed out after {WAIT_TIMEOUT}s")
            failed.append(rg)

    # Delete bootstrap last
    if has_bootstrap:
        if not delete_rg_and_wait(bootstrap):
            failed.append(bootstrap)

    if failed:
        print(f"==> WARNING: {len(failed)} resource group(s) failed to delete: {', '.join(failed)}")
        sys.exit(1)
    else:
        print("==> All resource groups deleted.")


if __name__ == "__main__":
    main()
