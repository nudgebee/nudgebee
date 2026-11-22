from typing import Any, Dict

from notifications_server.message_templates.slack.grouped_batch_notification import (
    BatchFindingsSummaryParams,
    item_details_url,
    view_all_url,
)

NEUTRAL_COLOR = 0x94A3B8
UTM_SOURCE = "discord"


def get_discord_grouped_batch_findings_template(input_data: BatchFindingsSummaryParams) -> Dict[str, Any]:
    noun = "finding" if input_data.total_count == 1 else "findings"
    content = f"🔔 {input_data.total_count} {noun} held for this hour's digest"

    lines = []
    for item in input_data.items:
        facts = " · ".join(bit for bit in (item.severity, item.account) if bit)
        line = f"**{item.title}**" + (f"\n{facts}" if facts else "")
        details_url = item_details_url(item, UTM_SOURCE)
        if details_url:
            line += f"\n[Details]({details_url})"
        lines.append(line)
    if input_data.overflow_count > 0:
        lines.append(f"+{input_data.overflow_count} more findings")
        lines.append(f"[View All Findings]({view_all_url(input_data, UTM_SOURCE)})")

    description = "\n\n".join(lines) if lines else "No findings to display."
    embed = {"description": description, "color": NEUTRAL_COLOR}
    return {"content": content, "embeds": [embed]}
