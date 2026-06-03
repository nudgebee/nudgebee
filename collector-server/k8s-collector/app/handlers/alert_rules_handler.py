from typing import Any, Dict

from db import database

event_rule_on_conflict = (
    "ON CONFLICT(account_id,tenant_id,alert) DO UPDATE SET alert=EXCLUDED.alert, "
    "expr=EXCLUDED.expr, annotations=EXCLUDED.annotations, duration=EXCLUDED.duration, "
    "severity=EXCLUDED.severity, source=EXCLUDED.source, category=EXCLUDED.category, "
    "namespace=EXCLUDED.namespace, name=EXCLUDED.name"
)


def handle_api_based_rules(rules: Dict[str, Any], tenant: str, cloud_account_id: str):
    alert_rules_list = []
    for group in rules.get("groups"):
        group_name = group.get("name")
        for rule in group.get("rules"):
            if rule.get("type") != "alerting":
                continue
            severity = rule.get("labels", {}).get("severity", "warning").lower()
            if severity not in ("critical", "warning"):
                severity = "warning"
            alert_rule = {
                "tenant_id": tenant,
                "account_id": cloud_account_id,
                "expr": rule.get("query"),
                "annotations": rule.get("annotations", {}),
                "duration": rule.get("duration"),
                "labels": rule.get("labels", {}),
                "alert": rule.get("name"),
                "severity": severity,
                "source": "prometheus",
                "category": group_name,
                "enabled": True,
                "is_editable": False,
            }
            database.insert_data("event_rules", [alert_rule], on_conflict=event_rule_on_conflict)
            alert_rules_list.append(alert_rule)
    return alert_rules_list


def handle_crd_based_rules(rules: Dict[str, Any], tenant: str, cloud_account_id: str):
    alert_rules_list = []
    for item in rules.get("items") or []:
        namespace = item.get("metadata", {}).get("namespace")
        prometheus_rule_name = item.get("metadata", {}).get("name")
        for group in item.get("spec", {}).get("groups"):
            for rule in group.get("rules"):
                if not rule.get("alert"):
                    continue
                severity = rule.get("labels", {}).get("severity", "warning").lower()
                if severity not in ("critical", "warning"):
                    severity = "warning"

                alert_rule = {
                    "tenant_id": tenant,
                    "account_id": cloud_account_id,
                    "expr": rule.get("expr"),
                    "annotations": rule.get("annotations", {}),
                    "duration": rule.get("for"),
                    "labels": rule.get("labels", {}),
                    "alert": rule.get("alert"),
                    "severity": severity,
                    "source": "prometheus",
                    "category": group.get("name"),
                    "enabled": True,
                    "is_editable": True,
                    "namespace": namespace,
                    "name": prometheus_rule_name,
                }
                database.insert_data("event_rules", [alert_rule], on_conflict=event_rule_on_conflict)
                alert_rules_list.append(alert_rule)
    return alert_rules_list


def handle_alert_rules(rules: Dict[str, Any], tenant: str, cloud_account_id: str):
    if rules.get("api_based_rules"):
        handle_api_based_rules(rules.get("api_based_rules"), tenant, cloud_account_id)
    if rules.get("crd_based_rules"):
        handle_crd_based_rules(rules.get("crd_based_rules"), tenant, cloud_account_id)
    if rules.get("chronosphere_alert_rules"):
        handle_chrono_monitors(rules.get("chronosphere_alert_rules"), tenant, cloud_account_id)
    else:
        handle_crd_based_rules(rules, tenant, cloud_account_id)


def handle_chrono_monitors(monitors: Dict[str, Any], tenant: str, cloud_account_id: str):
    alert_rules = []
    for rule in monitors.get("monitors", []):
        series_conditions = rule.get("series_conditions", {}).get("defaults", {})
        severity = next(iter(series_conditions), None)

        if severity not in ("critical", "warning"):
            severity = "warning"

        annotations = rule.get("annotations", {})
        if "Summary" in annotations:
            annotations["description"] = annotations["Summary"]
        elif "Message" in annotations:
            annotations["description"] = annotations["Message"]
        annotations["summary"] = rule.get("name", "")
        alert_rule = {
            "tenant_id": tenant,
            "account_id": cloud_account_id,
            "expr": rule.get("prometheus_query"),
            "annotations": annotations,
            "duration": rule.get("interval_secs"),
            "labels": rule.get("labels", {}),
            "alert": rule.get("slug"),
            "severity": severity,
            "source": "chronosphere",
            "category": rule.get("collection_slug", ""),
            "enabled": True,
            "is_editable": False,
            "namespace": "",
            "name": rule.get("slug"),
        }
        alert_rules.append(alert_rule)
    database.insert_data("event_rules", alert_rules, on_conflict=event_rule_on_conflict)
    return alert_rules
