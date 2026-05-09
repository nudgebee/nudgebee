import json
import logging

from psycopg2 import extras

from db import database


def get_custom_playbooks(tenant: str, cloud_account_id: str) -> list:
    db_playbooks = database.run_query(
        "SELECT trigger_params, action_params, source"
        " FROM public.agent_playbook where tenant_id = %s and cloud_account_id = %s and processor= %s",
        [tenant, cloud_account_id, "nb_agent"],
        cursor_factory=extras.RealDictCursor,
    )

    if len(db_playbooks) == 0:
        return []

    playbooks = []
    for db_playbook in db_playbooks:
        playbook = {
            "triggers": db_playbook["trigger_params"],
            "actions": db_playbook["action_params"],
            "source": db_playbook["source"],
        }
        playbooks.append(playbook)
    return playbooks


def process_playbooks(tenant_id: str, cloud_account_id: str, playbooks: list):
    try:
        db_playbooks = {}
        for playbook in playbooks:
            for trigger in playbook.get("triggers", []):
                alert_config = trigger.get("on_prometheus_alert")
                if alert_config and (alert_name := alert_config.get("alert_name")):
                    db_playbook = process_playbook(
                        tenant_id, cloud_account_id, alert_name, "on_prometheus_alert", playbook, alert_config
                    )
                    db_playbooks[alert_name] = db_playbook
        database.insert_data(
            "agent_playbook",
            list(db_playbooks.values()),
            on_conflict="ON CONFLICT (tenant_id, cloud_account_id, alert_name) DO UPDATE SET "
            "trigger_params = EXCLUDED.trigger_params, action_params = EXCLUDED.action_params "
            "WHERE agent_playbook.source != 'user' and agent_playbook.processor = 'nb_agent'",
        )
    except Exception:
        logging.exception("Error while processing playbooks")


def process_playbook(tenant_id: str, cloud_account_id: str, alert_name: str, key: str, playbook, value):
    db_playbook = {
        "alert_name": alert_name,
        "trigger_params": json.dumps([{key: value}]),
        "action_params": json.dumps(playbook.get("actions", [])),
        "tenant_id": tenant_id,
        "cloud_account_id": cloud_account_id,
        "source": "agent",
    }
    return db_playbook
