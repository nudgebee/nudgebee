import datetime
import json
import logging
import uuid
from typing import Dict, Any, Optional

from psycopg2 import sql, extras

from db import database
from handlers.playbook_handler import process_playbooks


def _upsert_integration(
    tenant_id: str,
    cloud_account_id: str,
    provider: Optional[str],
    connection_enabled: bool,
    provider_type: str,
):
    if not provider:
        return

    status_value = "enabled" if connection_enabled else "disabled"
    upsert_integration = sql.SQL("""
        INSERT INTO integrations (id, tenant_id, type, source, name,
                                  status, created_at, updated_at, labels)
        VALUES (%s, %s, %s, %s, %s, %s, %s, %s, %s)
        ON CONFLICT (tenant_id, name, source, type) DO UPDATE
        SET status = EXCLUDED.status,
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
            status_value,
            now,
            now,
            json.dumps({}),
        ],
    )

    if not result:
        logging.warning(f"Upsert did not return an integration ID for {provider_type}={provider}")
        return
    integration_id = result[0][0] if result else None
    upsert_mapping = sql.SQL("""
        INSERT INTO integrations_cloud_accounts (integration_id, cloud_account_id, tenant_id)
        VALUES (%s, %s, %s)
        ON CONFLICT (integration_id, cloud_account_id, tenant_id) DO NOTHING
        """)

    database.run_query(
        upsert_mapping,
        [integration_id, cloud_account_id, tenant_id],
    )

    logging.info(f"Upserted integration + mapping for {provider_type}={provider}")


def handle_telemetry(tenant_id: str, cloud_account_id: str, agent_id: str, data: Dict[str, Any]):
    if "version" not in data:
        raise RuntimeError("invalid params, version is required")

    k8s_version = data.get("stats", {}).get("k8s_version", "")
    k8s_provider = data.get("stats", {}).get("provider", "")

    if data.get("playbooks"):
        process_playbooks(tenant_id, cloud_account_id, data["playbooks"])

    agent_existing_connection_status = database.run_query(
        "select connection_status from agent where id = %s", [agent_id], cursor_factory=extras.RealDictCursor
    )
    activity_stats = data.get("activity_stats") or {}
    if isinstance(activity_stats, str):
        try:
            activity_stats = json.loads(activity_stats)
        except json.JSONDecodeError:
            logging.warning(f"Invalid activity_stats format: {activity_stats}")
            activity_stats = {}
    if agent_existing_connection_status and len(agent_existing_connection_status) > 0:
        existing_conn_status = agent_existing_connection_status[0].get("connection_status") or {}
        activity_stats["relayConnection"] = existing_conn_status.get("relayConnection")

    if isinstance(activity_stats, str):
        try:
            activity_stats = json.loads(activity_stats)
        except json.JSONDecodeError:
            logging.warning(f"Invalid activity_stats format: {activity_stats}")
            activity_stats = {}

    connection_status = json.dumps(activity_stats)

    update_agent = sql.SQL("""UPDATE {}
           SET version = %s, last_connected_at = %s, status = %s,
               k8s_version = %s, connection_status = %s, k8s_provider = %s
           WHERE id = %s""").format(sql.Identifier("agent"))

    database.run_query(
        update_agent,
        [
            data["version"],
            datetime.datetime.now(datetime.timezone.utc),
            "CONNECTED",
            k8s_version,
            connection_status,
            k8s_provider,
            agent_id,
        ],
    )
    logging.info(f"Updated {agent_id} agent telemetry data")

    _upsert_integration(
        tenant_id,
        cloud_account_id,
        activity_stats.get("logsConnectionProvider"),
        activity_stats.get("logsConnection", False),
        "logsConnectionProvider",
    )

    prometheus_url = activity_stats.get("prometheusUrl")
    metrics_provider = None

    if prometheus_url:
        if "chronosphere" in prometheus_url.lower():
            metrics_provider = "chronosphere"
        else:
            metrics_provider = "prometheus"

    _upsert_integration(
        tenant_id,
        cloud_account_id,
        metrics_provider,
        activity_stats.get("prometheusConnection", False),
        "metricsConnectionProvider",
    )

    _upsert_integration(
        tenant_id,
        cloud_account_id,
        activity_stats.get("traceProvider"),
        activity_stats.get("tracesEnabled", False),
        "tracesConnectionProvider",
    )
