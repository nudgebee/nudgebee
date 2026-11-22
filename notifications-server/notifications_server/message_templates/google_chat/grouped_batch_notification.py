from typing import Any, Dict

from notifications_server.message_templates.slack.grouped_batch_notification import (
    BatchFindingsSummaryParams,
    item_details_url,
    view_all_url,
)

UTM_SOURCE = "gchat"


def get_grouped_batch_findings_gchat_template(input_data: BatchFindingsSummaryParams) -> Dict[str, Any]:
    noun = "finding" if input_data.total_count == 1 else "findings"
    lines = [f"🔔 {input_data.total_count} {noun} held for this hour's digest"]
    for item in input_data.items:
        facts = " · ".join(bit for bit in (item.severity, item.account) if bit)
        lines.append(f"• *{item.title}*" + (f" — {facts}" if facts else ""))
        details_url = item_details_url(item, UTM_SOURCE)
        if details_url:
            lines.append(f"  Details: {details_url}")
    if input_data.overflow_count > 0:
        lines.append(f"+{input_data.overflow_count} more findings")
        lines.append(f"View all findings: {view_all_url(input_data, UTM_SOURCE)}")

    return {"text": "\n".join(lines)}
