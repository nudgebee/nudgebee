import logging
from datetime import datetime

from notifications_server.message_templates.base import Emojis
from notifications_server.configs.settings import settings, URLRoutes, DEFAULT_EVIDENCES
from notifications_server.utils.transformer import Transformer, add_text_info_evidence
from notifications_server.utils.evidence import flatten_evidence_rows

LOG = logging.getLogger(__name__)


def get_markdown_message_template(finding):
    message = f"*Alert*: {finding.get('title')}\n\n"
    message += f"- *Reported At*: {str(datetime.fromisoformat(finding.get('created_at')))}\n"
    message += f"- *Namespace*: {finding.get('subject_namespace')}\n"
    message += f"- *Account*: {finding.get('cluster') or finding.get('source')}\n"
    message += f"- *Status*: {'Resolved' if finding.get('title').startswith('[RESOLVED]') else 'New'}\n\n"
    message += "*Evidences:*\n"

    for evidence in finding.get("evidences") or []:
        if evidence is None:
            continue
        if evidence.get("aggregation_key", "") == "query_failure":
            LOG.info("Query failure message are not yet implemented in G-Chat")
        else:
            message += add_finding_markdown_evidences(evidence)
    cloud_account_id = finding.get("cloud_account_id")
    _id = finding.get("id")
    if cloud_account_id and _id:
        view_details_url = settings.urls.investigate_url(
            account_id=cloud_account_id,
            finding_id=_id,
            utm_source=URLRoutes.UTMSource.GCHAT,
        )
        message += f"\n<{view_details_url}|{Emojis.Evidence.value} View Details>\u200b"
    else:
        LOG.debug(f"Missing cloud_account_id or id for finding. cloud_account_id={cloud_account_id}, id={_id}")
    return message


def _whitelist_to_markdown(data: dict) -> str:
    out = ""
    for key in DEFAULT_EVIDENCES:
        if key in data and data[key] is not None:
            out += f"- *{key}:* {data[key]}\n"
    return out


def add_finding_markdown_evidences(evidence):
    if evidence is not None and "data" in evidence:
        type_ = evidence.get("type")
        if type_ == "markdown" or type_ == "header":
            if "data" in evidence:
                evidence = evidence.get("data")

            return add_text_info_evidence(evidence).replace("**", "*") + "\n"
        elif type_ == "table":
            data = evidence.get("data")
            return Transformer.json_to_markdown_table(data) + "\n"

        data = evidence.get("data")
        flat = flatten_evidence_rows(data)
        if flat:
            return _whitelist_to_markdown(flat)

        if isinstance(data, dict):
            return _whitelist_to_markdown(data)
    return ""
