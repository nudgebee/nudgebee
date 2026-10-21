"""Drives one scenario through the real pipeline: inject a synthetic alert,
look up the event it produced, then run diagnosis+fix via /v1/analyze/event/rca.

No organic detection wait (see docs/code-analysis-eval-spec.md's revision
note) — the alert is posted directly through the real, already-supported
prometheus-alertmanager webhook, so turnaround is webhook-processing latency,
not a metric-evaluation cycle.
"""

from __future__ import annotations

import os
import sys
import time
import uuid
from datetime import datetime, timezone
from typing import Any

import requests

from telemetry_scorer import event_resource_id

# The attribute names the UI's "Link repository" writes and the llm-server reads
# (services_server.GetVMServiceCodeLink): the workload git annotation with the
# service's node-agent container_id appended. Change all three together.
SERVICE_GIT_REPO = "workloads.nudgebee.com/git.repo"
SERVICE_GIT_BRANCH = "workloads.nudgebee.com/git.branch"

# A connection that drops or times out mid-poll; HTTP error statuses are not
# included, they still fail the call.
TRANSIENT_NETWORK_ERRORS = (requests.ConnectionError, requests.Timeout)

TERMINAL_OK = {"completed"}
TERMINAL_BAD = {"failed", "terminated", "killed"}


def env(name: str, default: str | None = None) -> str:
    v = os.environ.get(name, default)
    if v is None:
        sys.exit(f"{name} must be set")
    return v


def event_wait_seconds() -> int:
    """How long to wait for the platform to ingest a posted alert. The event
    worker is shared: on dev it usually inserts an event within seconds, but a
    burst of other webhook traffic has held one back by about 2.5 minutes, so
    the default leaves room for that. EVENT_WAIT_SECONDS overrides it."""
    return int(os.environ.get("EVENT_WAIT_SECONDS", "300"))


class IngestionTimeout(Exception):
    """The injected alert never produced a queryable event in time.

    Distinct from a diagnosis or fix failure — see the `ingestion_timeout`
    bucket in the spec. Expected to be rare; a long wait here means webhook
    processing is actually stuck, not "detection hasn't happened yet".
    """


class EventCodeFixClient:
    def __init__(
        self,
        account_id: str | None = None,
        tenant_id: str | None = None,
        user_id: str | None = None,
    ) -> None:
        # api-server: alert injection + event lookup
        self.api_url = env("API_SERVER_URL").rstrip("/")
        self.api_token = env("ACTION_API_SERVER_TOKEN")
        # Both full URLs incl. ?token=...; each is required only by the scenario
        # type that posts to it, so a VM-only run needs no Alertmanager webhook.
        self.webhook_url = os.environ.get("ALERTMANAGER_WEBHOOK_URL")
        self.event_webhook_url = os.environ.get("EVENT_WEBHOOK_URL")
        self.event_webhook_integration_id = os.environ.get(
            "EVENT_WEBHOOK_INTEGRATION_ID"
        )
        # llm-server: diagnosis + fix
        self.llm_url = env("LLM_SERVER_URL").rstrip("/")
        self.llm_token = env("LLM_SERVER_TOKEN")
        # The dashboard passes the run's selected account; the CLI falls back
        # to the BENCHMARK_* env vars.
        self.account_id = account_id or env("BENCHMARK_ACCOUNT_ID")
        self.tenant_id = tenant_id or env("BENCHMARK_TENANT_ID")
        self.user_id = user_id or os.environ.get("BENCHMARK_USER_ID", "")
        # (resource id, container id) of every service link this run wrote, so
        # unlink_service_repos can clear exactly those.
        self._linked: list[tuple[str, str]] = []

    # --- stage 1: inject the alert -----------------------------------------

    def post_alert(self, scenario: dict[str, Any]) -> None:
        alert = scenario["alert"]
        now = datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")
        payload = {
            "alerts": [
                {
                    "status": "firing",
                    "labels": {
                        "alertname": alert["alertname"],
                        **alert.get("labels", {}),
                    },
                    "annotations": {
                        "summary": alert.get("summary", alert["alertname"]),
                        "description": alert["log_lines"],
                    },
                    "startsAt": now,
                    "endsAt": "",
                    "fingerprint": alert["fingerprint"],
                    "generatorURL": "",
                }
            ],
            "externalURL": "",
        }
        # Observed ~14s even for a trivially-rejected payload against a local
        # dev stack — RabbitMQ publish appears to fail (credential mismatch
        # between api-server's and llm-server's .env RABBIT_MQ_* values is a
        # likely cause locally) and ProcessStoredWebhook falls back to
        # synchronous processing, which the handler already tolerates. 30s
        # cut it close; 60s gives headroom without masking a real hang.
        resp = requests.post(
            self.webhook_url or env("ALERTMANAGER_WEBHOOK_URL"),
            json=payload,
            timeout=60,
        )
        resp.raise_for_status()

    def post_vm_event(self, scenario: dict[str, Any]) -> str:
        """Post the scenario's alert to the account's generic `event_webhook`
        integration and return the fingerprint the event is stored under.

        Not Alertmanager: that route builds Kubernetes subjects from
        deployment/namespace labels. The event webhook keeps the labels as
        sent, and the platform links the event to a VM from `instance_id` or
        `instance` (and to a service from `container_id`), which is the path a
        real VM alert takes. The integration scopes the fingerprint
        (`ew:<integration id>:fp:<fingerprint>`), so the lookup must too.

        The scenario's fingerprint gets a per-run suffix. The platform keys an
        investigation by it, so a fixed one makes every rerun reuse the first
        run's finished investigation: no new queries, and nothing measured.
        """
        url = self.event_webhook_url or env("EVENT_WEBHOOK_URL")
        integration_id = self.event_webhook_integration_id or env(
            "EVENT_WEBHOOK_INTEGRATION_ID"
        )
        alert = scenario["alert"]
        labels = alert.get("labels", {})
        fingerprint = f"{alert['fingerprint']}-{uuid.uuid4().hex[:8]}"
        payload = {
            "events": [
                {
                    "id": fingerprint,
                    "fingerprint": fingerprint,
                    "rule_name": alert["alertname"],
                    "title": alert.get("summary", alert["alertname"]),
                    "status": "firing",
                    "severity": labels.get("severity", "high"),
                    "description": alert["log_lines"],
                    "starts_at": datetime.now(timezone.utc).strftime(
                        "%Y-%m-%dT%H:%M:%SZ"
                    ),
                    "source_tool": "event-code-fix-benchmark",
                    "labels": {k: str(v) for k, v in labels.items()},
                }
            ]
        }
        resp = requests.post(url, json=payload, timeout=60)
        resp.raise_for_status()
        return f"ew:{integration_id}:fp:{fingerprint}"

    def inject(self, scenario: dict[str, Any]) -> str:
        """Post the scenario's alert by its type; return the fingerprint to
        look the resulting event up by."""
        if scenario.get("type") == "vm_event":
            return self.post_vm_event(scenario)
        self.post_alert(scenario)
        return scenario["alert"]["fingerprint"]

    # --- stage 2: look up the event the alert produced ----------------------

    def wait_for_event(
        self,
        fingerprint: str,
        timeout_seconds: int | None = None,
        poll_interval: int = 5,
    ) -> str:
        """Poll events_v2 (via the dashboard query engine) for a row matching
        `fingerprint`, and return its id.

        Raises IngestionTimeout if none appears within budget — see the
        `ingestion_timeout` bucket in the spec: this is an infra/harness
        signal, not a diagnosis or fix result.

        Request/response shapes (EntityQueryRequest, QueryRequest,
        BinaryWhereClause) are verified against
        api-server/services/dashboard/model.go and services/query/entity_query.go.
        Confirmed live against a real local run (2026-09-18): the response is
        flat — `{"columns": [...], "rows": [...]}` — with no "data" wrapper.
        """
        if timeout_seconds is None:
            timeout_seconds = event_wait_seconds()
        deadline = time.monotonic() + timeout_seconds
        query = {
            "account_ids": [self.account_id],
            "datasource": "nudgebee",
            "query": {
                "table": "events_v2",
                "columns": [{"name": "id"}],
                "where": {"_binary": {"fingerprint": {"_eq": fingerprint}}},
                "limit": 1,
            },
        }
        while time.monotonic() < deadline:
            try:
                resp = requests.post(
                    f"{self.api_url}/rpc/dashboard",
                    json={
                        "action": {"name": "dashboards_execute_entity_query"},
                        "input": query,
                    },
                    headers={
                        "X-ACTION-TOKEN": self.api_token,
                        "x-tenant-id": self.tenant_id,
                    },
                    timeout=30,
                )
            except TRANSIENT_NETWORK_ERRORS:
                # A dropped port-forward or a restarting pod: keep polling until
                # the deadline instead of failing the scenario on one blip.
                time.sleep(poll_interval)
                continue
            resp.raise_for_status()
            resp_json = resp.json()
            rows = (resp_json.get("data") or resp_json).get("rows") or []
            if rows:
                return rows[0][0]
            time.sleep(poll_interval)
        raise IngestionTimeout(
            f"no event with fingerprint={fingerprint} within {timeout_seconds}s"
        )

    # --- stage 2b: link a VM service to its repository -----------------------

    def link_service_repo(
        self, event_id: str, container_id: str, repo: str, branch: str = ""
    ) -> None:
        """Link the service the event is about to a repository, as the UI's
        "Link repository" does, so the investigation can reach the code-fix
        stage. Done after the event exists because the link hangs off the VM
        the platform resolved for it, and before the RCA reads it."""
        resource_id = event_resource_id(event_id)
        if resource_id is None:
            raise RuntimeError(
                f"event {event_id} has no VM resource to link (or APP_DATABASE_URL is unset)"
            )
        self._upsert_service_link(resource_id, container_id, repo, branch)
        self._linked.append((resource_id, container_id))

    def unlink_service_repos(self) -> None:
        """Clear every link this run wrote. An empty value is how the UI unlinks."""
        linked, self._linked = self._linked, []
        for resource_id, container_id in linked:
            self._upsert_service_link(resource_id, container_id, "", "")

    def _upsert_service_link(
        self, resource_id: str, container_id: str, repo: str, branch: str
    ) -> None:
        objects = [
            {
                "resource_id": resource_id,
                "account_id": self.account_id,
                "name": f"{name}/{container_id}",
                "value": value,
                "labels": "{}",
            }
            for name, value in (
                (SERVICE_GIT_REPO, repo),
                (SERVICE_GIT_BRANCH, branch),
            )
        ]
        resp = requests.post(
            f"{self.api_url}/rpc/tenant",
            json={
                "action": {"name": "cloud_resource_attributes_upsert"},
                "input": {"objects": objects},
            },
            headers={
                "X-ACTION-TOKEN": self.api_token,
                "x-tenant-id": self.tenant_id,
            },
            timeout=30,
        )
        resp.raise_for_status()

    # --- stage 3: diagnosis + fix -------------------------------------------

    def run_rca(
        self, event_id: str, poll_interval: int = 10, timeout_seconds: int = 900
    ) -> dict[str, Any]:
        """POST /v1/analyze/event/rca and poll until terminal.

        Returns the final EventAnalysisResponse as a dict. Diagnosis
        (analysis/investigation/summary) and fix (git_diff/status_reason) both
        land in this same response when the fix stage ran — see the spec.
        """
        body = {
            "input": {
                "event_id": event_id,
                "account_id": self.account_id,
                "generate": True,
            }
        }
        headers = {"X-ACTION-TOKEN": self.llm_token, "x-tenant-id": self.tenant_id}

        deadline = time.monotonic() + timeout_seconds
        while time.monotonic() < deadline:
            try:
                resp = requests.post(
                    f"{self.llm_url}/v1/analyze/event/rca",
                    json=body,
                    headers=headers,
                    timeout=60,
                )
            except TRANSIENT_NETWORK_ERRORS:
                # Each poll re-posts the same request and the platform keys the
                # analysis by event, so retrying after a dropped connection
                # does not start a second investigation.
                time.sleep(poll_interval)
                continue
            resp.raise_for_status()
            data = (resp.json().get("data")) or resp.json()
            status = str(data.get("status", "")).lower()
            if status in TERMINAL_OK:
                return data
            if status in TERMINAL_BAD:
                return data
            time.sleep(poll_interval)
        return {"status": "TIMEOUT", "event_id": event_id}
