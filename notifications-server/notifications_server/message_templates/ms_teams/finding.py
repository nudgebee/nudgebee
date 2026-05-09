import collections
import json
import logging
import os
from datetime import datetime

from notifications_server.utils.evidence import flatten_evidence_rows
from notifications_server.utils.transformer import Transformer
from notifications_server.configs.settings import DEFAULT_EVIDENCES, settings

LOG = logging.getLogger(__name__)


def get_adaptive_card_template(parameters):
    return {
        "$schema": "http://adaptivecards.io/schemas/adaptive-card.json",
        "type": "AdaptiveCard",
        "version": "1.4",
        "msteams": {"width": "full"},
        "body": [
            *nudgebee_header_column_set_template(),
            {"type": "TextBlock", "weight": "Bolder", "text": f"*Alert*: {parameters.get('title')}", "wrap": True},
            {"type": "TextBlock", "text": f"{parameters.get('description')}", "wrap": True},
            {"type": "FactSet", "facts": parameters.get("properties")},
            *parameters.get("evidences"),
        ],
        "actions": [{"type": "Action.OpenUrl", "title": "View", "url": f"{parameters.get('viewUrl')}"}],
    }


def get_attachment_body():
    return {
        "type": "Media",
        "sources": [{"mimeType": "application/pdf", "url": "https://example.com/path/to/your/file.pdf"}],
    }


def add_filtered_evidences_teams(evidence_list, data: dict):
    for key in DEFAULT_EVIDENCES:
        if key in data and data[key] is not None:
            evidence_list.append(
                {
                    "type": "Fact",
                    "title": key,
                    "value": str(data[key]),
                }
            )


def get_ms_teams_finding_message_template(finding):
    parameters = {"title": finding.get("title")}
    if finding.get("description"):
        parameters["description"] = finding.get("description")

    properties = [
        {"title": "*Reported At:*", "value": str(datetime.fromisoformat(finding.get("created_at")))},
    ]
    if finding.get("subject_namespace"):
        properties.append({"title": "*Namespace:*", "value": finding.get("subject_namespace")})

    if finding.get("cluster") or finding.get("source"):
        properties.append({"title": "*Account:*", "value": finding.get("cluster") or finding.get("source")})

    properties.append(
        {"title": "*Status:*", "value": "Resolved" if finding.get("title").startswith("[RESOLVED]") else "New"}
    )

    parameters["properties"] = properties
    parameters["evidences"] = add_evidences(finding)
    parameters["viewUrl"] = f"{os.environ.get('BASE_URL')}/investigate?id={finding.get('id')}"

    return get_adaptive_card_template(parameters)


def add_evidences(finding):
    evidence_list = []
    if finding.get("aggregation_key", "") == "query_failure":
        add_query_failure_evidences(evidence_list, finding)
    else:
        for evidence in finding.get("evidences") or []:
            add_finding_evidences(evidence, evidence_list)

    return evidence_list


def add_finding_evidences(evidence, evidence_list):
    if evidence is not None and "data" in evidence:
        data = evidence.get("data")
        type_ = evidence.get("type")
        if type_ in {"markdown", "header"}:
            if isinstance(data, dict):
                data = data.get("data", str(data))
            evidence_list.append(
                {
                    "type": "TextBlock",
                    "text": Transformer.apply_length_limit(data, 1000),
                    "isSubtle": True,
                }
            )
        elif type_ == "table":
            evidence_list.append(Transformer.json_to_teams_table(data))
        else:
            flat = flatten_evidence_rows(data)
            if flat:
                add_filtered_evidences_teams(evidence_list, flat)


def add_query_failure_evidences(evidence_list, finding):
    for key, value in (finding.get("evidences") or {}).items():
        if isinstance(value, collections.abc.Sequence) and any(value):
            value = ", ".join(str(v) for v in value if v is not None)
            formatted_value = f"```{value}```" if key == "statement" else value
            evidence_list.append({"type": "TextBlock", "text": f"*{key}:* {formatted_value}", "isSubtle": True})


def nudgebee_header_column_set_template():
    branding_name = settings.urls.branding_name
    return [
        {
            "type": "ColumnSet",
            "columns": [
                {
                    "type": "Column",
                    "items": [
                        {"type": "TextBlock", "weight": "Bolder", "text": branding_name, "wrap": True},
                    ],
                    "width": "stretch",
                },
            ],
        },
    ]
