"""Messages that cover several accounts are sent per channel: each account is
matched on its own, and every channel gets one message holding only the
accounts its rules allow, with those accounts' numbers."""

from datetime import datetime, timezone

# Optimize digests whose producer sends `accounts`: {account_id: {account_name,
# recommendations, totals}} next to the tenant-wide fields older pods read.
ROLLUP_TYPES = ("recommendation_nudge_digest", "recommendation_proactive_nudge", "security_posture_alert")


def account_sections(message_type, parameters):
    """The per-account sections of a roll-up message, or None when it has none
    (a producer a release behind): such a message is sent whole."""
    if message_type not in ROLLUP_TYPES:
        return None
    sections = (parameters or {}).get("accounts")
    if not isinstance(sections, dict) or not sections:
        return None
    return sections


def _add(total, value):
    if isinstance(value, bool):
        return total
    if isinstance(value, (int, float)):
        return (total or 0) + value
    if isinstance(value, dict):
        merged = dict(total or {})
        for key, item in value.items():
            merged[key] = _add(merged.get(key), item)
        return merged
    return total


def has_recommendations(parameters, account_ids):
    """Whether any of these accounts has something to list. A roll-up with
    nothing to list is not sent, as producers skip an empty one."""
    sections = parameters["accounts"]
    return any((sections.get(account_id) or {}).get("recommendations") for account_id in account_ids)


def merged_parameters(parameters, account_ids):
    """The message parameters for a channel receiving these accounts: the
    recommendations of those that have any under recommendations_by_account, and
    the sum of all their totals in place of the tenant-wide ones."""
    sections = parameters["accounts"]
    kept = [account_id for account_id in account_ids if account_id in sections]
    merged = {key: value for key, value in parameters.items() if key != "accounts"}
    merged["recommendations_by_account"] = {
        account_id: {
            "account_name": sections[account_id].get("account_name") or account_id,
            "recommendations": sections[account_id]["recommendations"],
        }
        for account_id in kept
        if sections[account_id].get("recommendations")
    }
    totals = {}
    for account_id in kept:
        for key, value in (sections[account_id].get("totals") or {}).items():
            totals[key] = _add(totals.get(key), value)
    merged.update(totals)
    return merged


def _stamp(value):
    """A producer timestamp as a tz-aware datetime, or None if absent/unreadable."""
    if not value:
        return None
    try:
        parsed = datetime.fromisoformat(str(value).replace("Z", "+00:00"))
    except ValueError:
        return None
    return parsed if parsed.tzinfo else parsed.replace(tzinfo=timezone.utc)


def latest_per_key(items, key="finding_id", stamp="updated_at"):
    """One item per `key` value — its latest state — for a channel's batch of
    grouped alerts, so a firing and its resolve in the same window show once.
    `items` are newest first, as the grouped list is read. The item's own
    `stamp` decides which is latest when both carry a readable one (queue order
    is not state order: a resolve can overtake its firing); otherwise the newer
    list entry stands. Items without the key are all kept."""
    kept = []
    position = {}
    for item in items:
        value = item.get(key)
        if not value:
            kept.append(item)
            continue
        if value not in position:
            position[value] = len(kept)
            kept.append(item)
            continue
        kept_at, item_at = _stamp(kept[position[value]].get(stamp)), _stamp(item.get(stamp))
        if kept_at and item_at and item_at > kept_at:
            kept[position[value]] = item
    return kept
