import datetime
import json
import logging
import threading
import uuid
from typing import Dict, Any, Optional

import requests
from psycopg2 import sql

from config import Configs
from db import database
from handlers.playbook_handler import process_playbooks


def _upsert_cloud_account_attrs(cloud_account_id: str, attrs: Dict[str, str]):
    upsert_attr = sql.SQL("""
        INSERT INTO cloud_account_attrs (id, cloud_account_id, name, value, created_at, updated_at)
        VALUES (%s, %s, %s, %s, NOW(), NOW())
        ON CONFLICT (cloud_account_id, name) DO UPDATE
            SET value = EXCLUDED.value, updated_at = NOW()
            WHERE cloud_account_attrs.value IS DISTINCT FROM EXCLUDED.value
        """)
    for name, value in attrs.items():
        if not value:
            continue
        database.run_query(upsert_attr, [str(uuid.uuid4()), cloud_account_id, name, value])


# Postgres SQLSTATE undefined_column (psycopg2.errorcodes.UNDEFINED_COLUMN).
_PG_UNDEFINED_COLUMN = "42703"


def _default_claim_decision(holders) -> str:
    """What the agent's connected provider does about the account's default.

    `holders` are the account's links that hold the default column, each
    (source, status, is_self, marked): the integration's source and status,
    whether the link is this provider's, and whether the user set the default on
    it from the Integrations UI (`user_set_defaults`).

    A default is the user's when its integration is user-sourced or its link is
    marked. The agent never takes one of those while it is enabled, but still
    takes a default only an agent claim put there, so stale ones heal. Returns
    "claim", "keep", or "release" (clear this provider's own unmarked claim,
    because an enabled user default also holds the column).
    """
    picked = [h for h in holders if not h[2] and h[1] != "disabled" and (h[0] == "user" or h[3])]
    own = next((h for h in holders if h[2]), None)
    if own is not None:
        return "release" if picked and not own[3] else "keep"
    return "keep" if picked else "claim"


def _maybe_set_default_provider(
    integration_id: str,
    cloud_account_id: str,
    tenant_id: str,
    default_column: str,
):
    """Auto-mark an agent-synced provider as the account default for its
    category (logs / metrics / traces) — without ever stomping a default the
    user has explicitly chosen.

    Issue #25714: when a log provider like Loki is wired up via the agent file,
    the "Default Log Provider" toggle should turn on by itself. But a user can
    later pick a different default from the Integrations UI, including another
    agent-created integration (the ClickHouse row traces created, picked for
    logs); a subsequent heartbeat must not flip that back to the agent's
    provider. api-server marks the link when the user turns its default on
    (`user_set_defaults`), and _default_claim_decision decides from that.

    Turning off an agent-held default without picking another leaves nothing
    holding it, so the next heartbeat claims it again. That is deliberate: with
    no default the resolver routes to the agent's own provider anyway, so "off"
    could not change what serves the signal, and the toggle shows what does.

    `default_column` is one of the fixed `integrations_cloud_accounts` flag
    columns; it is code-controlled, never user input, so interpolating it as an
    identifier is safe.
    """
    # Fail-fast: every query below is scoped by tenant + account. An empty id
    # would widen the WHERE clause into a cross-tenant scan, so bail before
    # touching the DB.
    if not integration_id or not cloud_account_id or not tenant_id:
        return

    col = sql.Identifier(default_column)

    # Who, if anyone, currently holds the default for this category?
    try:
        holders = database.run_query(
            sql.SQL("""
                SELECT i.source, i.status, ica.integration_id = %s AS is_self,
                       %s = ANY(ica.user_set_defaults) AS marked
                FROM integrations_cloud_accounts ica
                JOIN integrations i ON i.id = ica.integration_id
                WHERE ica.cloud_account_id = %s
                  AND ica.tenant_id = %s
                  AND ica.{col} = true
                """).format(col=col),
            [integration_id, default_column, cloud_account_id, tenant_id],
        )
    except Exception as e:
        if getattr(e, "pgcode", None) != _PG_UNDEFINED_COLUMN:
            raise
        # This collector can run before the migration adding user_set_defaults has
        # applied, wherever it deploys independently of the migrations Job. Leave the
        # default alone until the column exists, rather than failing the rest of the
        # heartbeat's link/unlink for the remaining signals.
        logging.warning(
            f"Skipped {default_column} for agent integration {integration_id}: "
            "integrations_cloud_accounts.user_set_defaults does not exist yet"
        )
        return
    decision = _default_claim_decision(holders)

    if decision == "release":
        # Never clears a marked column, even its own: only the save path does.
        database.run_query(
            sql.SQL("""
                UPDATE integrations_cloud_accounts ica
                SET {col} = false
                WHERE ica.integration_id = %s
                  AND ica.cloud_account_id = %s
                  AND ica.tenant_id = %s
                  AND NOT (%s = ANY(ica.user_set_defaults))
                """).format(col=col),
            [integration_id, cloud_account_id, tenant_id, default_column],
        )
        logging.info(
            f"Cleared {default_column} for agent integration {integration_id} on "
            f"cloud_account {cloud_account_id}: a user-chosen default also holds it"
        )
        return
    if decision != "claim":
        return

    # Claim it. Clear any stale agent claim first so exactly one row wins, then
    # flag self.
    #
    # Both statements re-assert the condition so a user default set concurrently
    # (between the SELECT above and these writes) is never clobbered:
    #   - the reset only clears OTHER agent-sourced rows the user did not mark;
    #   - the set-self is guarded by NOT EXISTS(enabled user default). A save
    #     that commits after this guard ran still leaves two rows holding the
    #     column; the next heartbeat sees it and releases ("release" above).
    database.run_query(
        sql.SQL("""
            UPDATE integrations_cloud_accounts ica
            SET {col} = false
            FROM integrations i
            WHERE ica.integration_id = i.id
              AND ica.cloud_account_id = %s
              AND ica.tenant_id = %s
              AND ica.integration_id != %s
              AND ica.{col} = true
              AND i.source = 'agent'
              AND NOT (%s = ANY(ica.user_set_defaults))
            """).format(col=col),
        [cloud_account_id, tenant_id, integration_id, default_column],
    )
    database.run_query(
        sql.SQL("""
            UPDATE integrations_cloud_accounts ica
            SET {col} = true
            WHERE ica.integration_id = %s
              AND ica.cloud_account_id = %s
              AND ica.tenant_id = %s
              AND ica.{col} = false
              AND NOT EXISTS (
                  SELECT 1
                  FROM integrations_cloud_accounts u
                  JOIN integrations ui ON ui.id = u.integration_id
                  WHERE u.cloud_account_id = %s
                    AND u.tenant_id = %s
                    AND u.{col} = true
                    AND ui.status != 'disabled'
                    AND (ui.source = 'user' OR %s = ANY(u.user_set_defaults))
              )
            """).format(col=col),
        [integration_id, cloud_account_id, tenant_id, cloud_account_id, tenant_id, default_column],
    )
    logging.info(
        f"Set {default_column}=true for agent integration {integration_id} on "
        f"cloud_account {cloud_account_id} (no user-chosen default present)"
    )


# Name of the integration_config_values row api-server writes when a user
# disables an agent integration, and removes when they enable it again.
USER_DISABLED_CONFIG_NAME = "user_disabled"


def _unlink_integration(
    tenant_id: str,
    cloud_account_id: str,
    provider: str,
    provider_type: str,
):
    """Drop this account's own link to an agent integration it no longer has."""
    database.run_query(
        sql.SQL("""
            DELETE FROM integrations_cloud_accounts ica
            USING integrations i
            WHERE ica.integration_id = i.id
              AND i.tenant_id = %s
              AND i.name = %s
              AND i.source = 'agent'
              AND i.type = %s
              AND ica.cloud_account_id = %s
              AND ica.tenant_id = %s
              AND ica.link_role = 'own'
            """),
        [tenant_id, provider, provider, cloud_account_id, tenant_id],
    )
    logging.info(f"Removed integration mapping for {provider_type}={provider}")


def _upsert_integration(
    tenant_id: str,
    cloud_account_id: str,
    provider: Optional[str],
    connection_enabled: bool,
    provider_type: str,
    default_column: Optional[str] = None,
):
    # Fail-fast: skip when the tenant/account scope or the provider is missing,
    # rather than writing an unscoped or half-formed integration row.
    if not provider or not tenant_id or not cloud_account_id:
        return

    # One integration row serves every cluster in the tenant, so nothing about
    # a single cluster's health is written to it:
    #   - the account's link says this cluster has the provider configured, and
    #     is kept whether the signal is up or down;
    #   - the row's status is the user's switch. It is disabled only while the
    #     user-disabled marker exists, and this statement keeps it in step with
    #     that marker (which also re-enables rows an older collector disabled
    #     from a cluster's health). A Disable committed while this statement is
    #     waiting on the row is not seen until the next heartbeat;
    #   - whether the signal is up for this cluster stays in the agent's own
    #     connection_status, which is what readers consult.
    upsert_integration = sql.SQL("""
        INSERT INTO integrations (id, tenant_id, type, source, name,
                                  status, created_at, updated_at, labels)
        VALUES (%s, %s, %s, %s, %s, %s, %s, %s, %s)
        ON CONFLICT (tenant_id, name, source, type) DO UPDATE
        SET status = CASE
                WHEN EXISTS (
                    SELECT 1 FROM integration_config_values m
                    WHERE m.integration_id = integrations.id AND m.name = %s
                ) THEN 'disabled'
                ELSE 'enabled'
            END,
            updated_at = EXCLUDED.updated_at,
            labels = EXCLUDED.labels
        RETURNING id
        """)

    now = datetime.datetime.now(datetime.timezone.utc)
    result = database.run_query(
        upsert_integration,
        [
            str(uuid.uuid4()),
            tenant_id,
            provider,
            "agent",
            provider,
            "enabled",
            now,
            now,
            json.dumps({}),
            USER_DISABLED_CONFIG_NAME,
        ],
    )

    if not result:
        logging.warning(f"Upsert did not return an integration ID for {provider_type}={provider}")
        return
    integration_id = result[0][0] if result else None
    upsert_mapping = sql.SQL("""
        INSERT INTO integrations_cloud_accounts (integration_id, cloud_account_id, tenant_id, link_role)
        VALUES (%s, %s, %s, 'own')
        ON CONFLICT (integration_id, cloud_account_id, tenant_id, link_role) DO NOTHING
        """)

    database.run_query(
        upsert_mapping,
        [integration_id, cloud_account_id, tenant_id],
    )

    logging.info(f"Upserted integration + mapping for {provider_type}={provider}")

    # Auto-enable the "default <category> provider" toggle for an agent-wired
    # provider, but only when the provider is actually connected and the user
    # hasn't already chosen a default. See _maybe_set_default_provider.
    if default_column and connection_enabled:
        _maybe_set_default_provider(integration_id, cloud_account_id, tenant_id, default_column)


def _post_scanner_heartbeat(
    url: str, headers: Dict[str, str], payload: Dict[str, Any], tenant_id: str, cloud_account_id: str
) -> None:
    """Issue the heartbeat POST. Runs in a daemon thread; errors are swallowed."""
    try:
        requests.post(url, json=payload, headers=headers, timeout=5)
    except requests.exceptions.RequestException as e:
        logging.warning(
            f"scanner_heartbeat_evaluate notify failed (best-effort) tenant={tenant_id} "
            f"account={cloud_account_id}: {e}"
        )


def _notify_scanner_heartbeat(tenant_id: str, cloud_account_id: str) -> None:
    """Fire-and-forget call to api-server's scanner_heartbeat_evaluate action.

    api-server decides whether any scanners are missed (first run, or last
    run older than one cron interval) and asynchronously fires RunOne for
    each missed scanner. Restores the legacy startup-scan behaviour that
    disappeared after the Go-agent migration.

    The POST is dispatched on a daemon thread so the telemetry handler's
    request thread isn't blocked by api-server latency (up to 5s per call).
    Under load this prevents collector worker exhaustion when many agents
    heartbeat concurrently.
    """
    url = f"{Configs.SERVICE_API_SERVER_URL}/rpc/recommendation"
    headers = {
        "Content-Type": "application/json",
        "X-ACTION-TOKEN": Configs.ACTION_API_SERVER_TOKEN,
        "x-tenant-id": tenant_id,
    }
    payload = {
        "action": {"name": "scanner_heartbeat_evaluate"},
        "input": {"account_id": cloud_account_id, "tenant_id": tenant_id},
    }
    threading.Thread(
        target=_post_scanner_heartbeat,
        args=(url, headers, payload, tenant_id, cloud_account_id),
        name=f"scanner-heartbeat-{cloud_account_id}",
        daemon=True,
    ).start()


# Per signal: its provider type, the flag the agent sends to say the signal is
# connected, and the account-link column that marks its default provider.
_SIGNALS = (
    ("logsConnectionProvider", "logsConnection", "default_log_provider"),
    ("metricsConnectionProvider", "prometheusConnection", "default_metrics_provider"),
    ("tracesConnectionProvider", "tracesEnabled", "default_traces_provider"),
    # The agent's ClickHouse logs table, reported beside its logs provider
    # (nudgebee/k8s-agent#660). It links otel_clickhouse so a user can pick it for
    # logs, and keeps the link while the table is reported even if traces move
    # away, but never claims the default: the agent's own logs provider does.
    ("clickhouseLogsTable", "clickhouseLogsConnection", None),
)

# Flags whose absence from a post means off. The merge into connection_status keeps a key
# the agent stops sending, so a `true` would outlive the agent that sent it: after a
# rollback to an agent that predates the flag, ClickHouse would stay offered as the
# cluster's logs provider. Only a post that reports flags at all clears them, as only
# such a post is stamped with healthCheckedAt.
_CLEARED_WHEN_OMITTED = ("clickhouseLogsCollected",)


def _signal_providers(stats: Dict[str, Any]) -> Dict[str, Optional[str]]:
    """The provider an agent names for each signal, keyed by the signal's provider type.

    Anything but a non-empty string counts as not named.
    """

    def named(value: Any) -> Optional[str]:
        return value if isinstance(value, str) and value else None

    prometheus_url = named(stats.get("prometheusUrl"))
    metrics_provider = None

    if prometheus_url:
        if "chronosphere" in prometheus_url.lower():
            metrics_provider = "chronosphere"
        else:
            metrics_provider = "prometheus"

    return {
        "logsConnectionProvider": named(stats.get("logsConnectionProvider")),
        "metricsConnectionProvider": metrics_provider,
        "tracesConnectionProvider": named(stats.get("traceProvider")),
        "clickhouseLogsTable": "otel_clickhouse" if named(stats.get("clickhouseLogsTable")) else None,
    }


def _stored_connection_status(agent_id: str) -> Dict[str, Any]:
    rows = database.run_query(sql.SQL("SELECT connection_status FROM agent WHERE id = %s"), [agent_id])
    status = rows[0][0] if rows else None
    return status if isinstance(status, dict) else {}


def handle_telemetry(tenant_id: str, cloud_account_id: str, agent_id: str, data: Dict[str, Any]):
    if "version" not in data:
        raise RuntimeError("invalid params, version is required")

    k8s_version = data.get("stats", {}).get("k8s_version", "")
    k8s_provider = data.get("stats", {}).get("provider", "")

    if data.get("playbooks"):
        process_playbooks(tenant_id, cloud_account_id, data["playbooks"])

    activity_stats = data.get("activity_stats") or {}
    if isinstance(activity_stats, str):
        try:
            activity_stats = json.loads(activity_stats)
        except json.JSONDecodeError:
            logging.warning(f"Invalid activity_stats format: {activity_stats}")
            activity_stats = {}

    reported_at = datetime.datetime.now(datetime.timezone.utc)

    # healthCheckedAt is server-owned, like providerStatus — the agent never sends it,
    # and it survives because this merge is additive (see the note below).
    #
    # It is NOT a check the server performed: it is this collector's receive time for
    # the agent's own self-report, stamped with our clock rather than the agent's so
    # clock skew can't render as "in 4h" in the UI's relative times. Its granularity is
    # the whole activity_stats block, not one signal — a post that omits a flag still
    # refreshes it — so read it as "the agent last reported its health at", which is
    # what Agent Details shows next to an agent-served signal.
    #
    # Only stamped for a non-empty dict: an empty or malformed activity_stats refreshes
    # no flag, so dating one would claim a freshness the row does not have. The
    # isinstance guard also catches a JSON array, which the str-normalisation above
    # lets through untouched.
    reported_stats = activity_stats
    cleared_keys = []
    if isinstance(activity_stats, dict) and activity_stats:
        reported_stats = {**activity_stats, "healthCheckedAt": reported_at.strftime("%Y-%m-%dT%H:%M:%SZ")}
        cleared_keys = [key for key in _CLEARED_WHEN_OMITTED if key not in activity_stats]

    connection_status = json.dumps(reported_stats)

    # Merge activity_stats into connection_status instead of overwriting.
    # Other writers (scan_orchestrator's schedule_jobs, relay-server's datasources /
    # agentBuild / relayConnection) own their own top-level keys; a full replace
    # here used to wipe them on every heartbeat, which caused scan_orchestrator
    # to treat every scheduled scan as "never run" and re-fire it each cycle.
    # Deliberately does NOT write `status`. This heartbeat proves the runner
    # process is alive and can reach us over HTTP — it says nothing about the
    # relay websocket, which is what `status` means to every consumer
    # (relay-server gates request routing on it, and the UI shows it). Writing
    # CONNECTED here re-marked agents whose websocket was dead within 60s of
    # relay-server correctly marking them NOT_CONNECTED, so requests were
    # routed onto a queue with no consumer and timed out instead of failing
    # fast, and the UI reported a connected agent that could not be reached.
    # relay-server owns `status`; see its UpdateRelayConnectionStatus and
    # TouchRelaySession.
    #
    # Deliberately does NOT write `last_connected_at`. relay-server uses it as the
    # session's identity: its disconnect write only applies while last_connected_at
    # is no newer than the value the session's own connect write stored. Moving it
    # forward every 60s made every disconnect look stale, so a dead agent stayed
    # CONNECTED until api-server's "Agent Status Check" cron retired it ~30 min
    # later. Liveness for that cron comes from relayLastSeenAt, which relay-server
    # writes for each live session.
    #
    update_agent = sql.SQL("""UPDATE {}
           SET version = %s,
               k8s_version = %s,
               connection_status = (COALESCE(connection_status, '{{}}'::jsonb) || %s::jsonb) - %s::text[],
               k8s_provider = %s
           WHERE id = %s""").format(sql.Identifier("agent"))

    # What the agent named for each signal on its previous post. Read before the
    # merge below, which replaces a name the agent has changed.
    previous_providers = _signal_providers(_stored_connection_status(agent_id))

    database.run_query(
        update_agent,
        [
            data["version"],
            k8s_version,
            connection_status,
            cleared_keys,
            k8s_provider,
            agent_id,
        ],
    )
    logging.info(f"Updated {agent_id} agent telemetry data")

    _notify_scanner_heartbeat(tenant_id, cloud_account_id)

    stats = data.get("stats", {})
    _upsert_cloud_account_attrs(
        cloud_account_id,
        {
            "k8s_provider": stats.get("provider", ""),
            "k8s_provider_account_number": stats.get("provider_account_number", ""),
            "k8s_provider_cluster_name": stats.get("cluster_name", ""),
            "k8s_provider_region": stats.get("provider_region", ""),
            "k8s_provider_zone": stats.get("provider_zone", ""),
            "k8s_provider_project_id": stats.get("provider_project_id", ""),
            "k8s_provider_resource_group": stats.get("provider_resource_group", ""),
        },
    )

    providers = _signal_providers(activity_stats)

    for provider_type, flag, default_column in _SIGNALS:
        if not providers[provider_type]:
            continue
        _upsert_integration(
            tenant_id,
            cloud_account_id,
            providers[provider_type],
            activity_stats.get(flag, False),
            provider_type,
            default_column=default_column,
        )

    # A cluster that stops naming a provider, or names a different one, no
    # longer has the old one configured, so its link to it is dropped. The agent
    # leaves a name out of the post when the signal is not configured, and the
    # additive merge above then keeps the old name in connection_status, so this
    # repeats on later posts; by then there is no link left and it changes nothing.
    # A signal is only acted on when the post carries its flag, which the agent
    # always sends: a post without it says nothing about that signal.
    named = set(providers.values())
    for provider_type, flag, _ in _SIGNALS:
        previous = previous_providers[provider_type]
        if previous and previous not in named and flag in activity_stats:
            _unlink_integration(tenant_id, cloud_account_id, previous, provider_type)
