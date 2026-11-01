"""Security posture alert template: critical-only with true counts, top-5 high
fallback, severity badges, per-account Details links. Items are LEGACY colored
attachments (no Block Kit blocks — those make Slack stamp an "Added by {app}"
byline under the message)."""

from typing import Any, Dict, List

from notifications_server.message_templates.slack.recommendation_nudge_digest import (
    AccountRecommendations,
    DigestRecommendation,
)
from notifications_server.message_templates.slack.security_posture_alert import (
    get_security_posture_alert_message_params,
    get_security_posture_alert_message_template,
)


def _attachments(msg) -> List[Dict[str, Any]]:
    return msg.get("attachments", []) if isinstance(msg, dict) else []


def _text(msg_or_blocks) -> str:
    parts: List[str] = []
    blocks = msg_or_blocks.get("blocks", []) if isinstance(msg_or_blocks, dict) else msg_or_blocks
    for b in blocks:
        if isinstance(b.get("text"), dict):
            parts.append(b["text"].get("text", ""))
        for e in b.get("elements", []) or []:
            if isinstance(e, dict) and isinstance(e.get("text"), str):
                parts.append(e["text"])
    for a in _attachments(msg_or_blocks):
        for key in ("pretext", "text", "footer"):
            if a.get(key):
                parts.append(a[key])
    return "\n".join(parts)


def _buttons(msg) -> List[Dict[str, Any]]:
    out: List[Dict[str, Any]] = []
    for b in msg.get("blocks", []):
        if b.get("type") == "actions":
            for e in b["elements"]:
                out.append(
                    {"text": e.get("text", {}).get("text", ""), "url": e.get("url", ""), "style": e.get("style")}
                )
    for a in _attachments(msg):
        for e in a.get("actions", []) or []:
            out.append({"text": e.get("text", ""), "url": e.get("url", ""), "style": e.get("style")})
    return out


def _rec(i: int, severity: str = "Critical") -> DigestRecommendation:
    return DigestRecommendation(
        id=f"rec-{i}",
        rule_name="image_scan",
        resource_name=f"registry/ui:2.4{i}",
        finops_score=100 - i,
        finops_band="Act Now",
        severity=severity,
        category="Security",
        cta_url="https://app/kubernetes/details/acc-1?accountId=acc-1&utm=security-alert#security/image-scan",
    )


def _params(criticals: int, highs: int, critical_count=None, high_count=None, account_type: str = ""):
    recs = [_rec(i, "Critical") for i in range(criticals)] + [_rec(100 + i, "High") for i in range(highs)]
    return get_security_posture_alert_message_params(
        organization_id="org-1",
        organization_name="TestOrg",
        critical_count=critical_count if critical_count is not None else criticals,
        high_count=high_count if high_count is not None else highs,
        recommendations_by_account={
            "acc-1": {
                "account_name": "prod-aws",
                "account_type": account_type,
                "recommendations": [r.dict() for r in recs],
            }
        },
        base_url="https://app",
    )


def _multi_account_params(finops_scores=(100, 100), account_types=("", "")):
    rec1 = _rec(0, "Critical")
    rec1.finops_score = finops_scores[0]
    rec2 = _rec(1, "Critical")
    rec2.finops_score = finops_scores[1]
    return get_security_posture_alert_message_params(
        organization_id="org-1",
        organization_name="TestOrg",
        critical_count=2,
        high_count=0,
        recommendations_by_account={
            "acc-1": {"account_name": "prod-aws", "account_type": account_types[0], "recommendations": [rec1.dict()]},
            "acc-2": {"account_name": "prod-gcp", "account_type": account_types[1], "recommendations": [rec2.dict()]},
        },
        base_url="https://app",
    )


class TestSecurityPostureAlert:
    def test_critical_only_with_true_count_and_badge(self):
        msg = get_security_posture_alert_message_template(_params(criticals=4, highs=3, critical_count=8))
        text = _text(msg)
        assert "8 critical security findings in prod-aws" in text
        assert "`CRITICAL`" in text
        assert "`HIGH`" not in text  # highs stay out of the message when criticals exist
        assert "+5 more in the dashboard" in text  # 8 true - 3 shown
        assert "Vulnerable image — registry/ui:2.40" in text
        assert "Acct: prod-aws" in text

    def test_high_fallback_when_no_criticals(self):
        msg = get_security_posture_alert_message_template(_params(criticals=0, highs=7, high_count=7))
        text = _text(msg)
        assert "Top 5 high-severity security findings — no criticals today" in text
        assert "`HIGH`" in text
        assert "+2 more in the dashboard" in text

    def test_singular_critical_headline(self):
        text = _text(get_security_posture_alert_message_template(_params(criticals=1, highs=0)))
        assert "1 critical security finding in prod-aws" in text

    def test_details_and_view_all_buttons(self):
        msg = get_security_posture_alert_message_template(_params(criticals=1, highs=0))
        buttons = _buttons(msg)
        labels = [b["text"] for b in buttons]
        assert "Details" in labels and "View all findings" in labels
        assert all("#security/image-scan" in b["url"] for b in buttons)
        details = [b for b in buttons if b["text"] == "Details"][0]
        assert details.get("style") == "primary"

    def test_view_all_label_counts_total_findings(self):
        msg = get_security_posture_alert_message_template(_params(criticals=4, highs=3, critical_count=4, high_count=3))
        labels = [b["text"] for b in _buttons(msg)]
        assert "View all 7 findings" in labels

    def test_intro_and_account_chip(self):
        msg = get_security_posture_alert_message_template(_params(criticals=2, highs=3))
        text = _text(msg)
        assert "worth interrupting you for" in text
        assert "queued in the dashboard" in text
        assert "Accounts 1" in text

    def test_item_stripes_by_severity(self):
        msg = get_security_posture_alert_message_template(_params(criticals=1, highs=0))
        assert msg["attachments"][0]["color"] == "#C93A36"
        msg = get_security_posture_alert_message_template(_params(criticals=0, highs=2, high_count=2))
        assert msg["attachments"][0]["color"] == "#D97A2B"

    def test_counts_context_line(self):
        text = _text(get_security_posture_alert_message_template(_params(criticals=2, highs=5)))
        assert "Critical 2" in text and "High 5 · in dashboard" in text

    def test_relative_cta_url_resolved_against_base_url(self):
        params = _params(criticals=1, highs=0)
        params.recommendations_by_account["acc-1"].recommendations[
            0
        ].cta_url = "/kubernetes/details/acc-1#security/image-scan"
        msg = get_security_posture_alert_message_template(params)
        details = [b for b in _buttons(msg) if b["text"] == "Details"][0]
        assert details["url"] == "https://app/kubernetes/details/acc-1#security/image-scan"

    def test_no_byline_trigger_keys_in_attachments(self):
        # blocks / footer / ts on an attachment all make Slack stamp the
        # "Added by {app}" byline (verified live) — none may ship.
        for msg in (
            get_security_posture_alert_message_template(_params(criticals=2, highs=3)),
            get_security_posture_alert_message_template(_params(criticals=0, highs=7, high_count=7)),
        ):
            for a in _attachments(msg):
                assert "blocks" not in a and "footer" not in a and "ts" not in a
                assert "text" in a["mrkdwn_in"]

    def test_headline_is_a_header_block(self):
        msg = get_security_posture_alert_message_template(_params(criticals=1, highs=0))
        assert msg["blocks"][0]["type"] == "header"

    def test_multi_account_view_all_links_top_shown_finding_account(self):
        # acc-2's finding scores higher, so it's shown first in the message —
        # the footer must follow it there, not an arbitrary/first-inserted account.
        msg = get_security_posture_alert_message_template(_multi_account_params(finops_scores=(50, 90)))
        text = _text(msg)
        assert "View all findings on <" in text
        assert "acc-2" in text
        assert "acc-1" not in text.split("View all findings on <")[1]
        assert "#security/image-scan" in text

    def test_single_account_vm_routes_to_optimise_vm_tab(self):
        msg = get_security_posture_alert_message_template(_params(criticals=1, highs=0, account_type="vm"))
        details = [b for b in _buttons(msg) if b["text"] == "View all findings"][0]
        assert details["url"] == "https://app/optimise?utm=security-alert#security/vm-vulnerabilities"

    def test_single_account_cloud_routes_to_optimise_cloud_posture_tab(self):
        msg = get_security_posture_alert_message_template(_params(criticals=1, highs=0, account_type="cloud"))
        details = [b for b in _buttons(msg) if b["text"] == "View all findings"][0]
        assert details["url"] == "https://app/optimise?utm=security-alert#security/cloud-posture"

    def test_multi_account_footer_routes_by_top_account_type(self):
        msg = get_security_posture_alert_message_template(
            _multi_account_params(finops_scores=(50, 90), account_types=("kubernetes", "vm"))
        )
        text = _text(msg)
        assert "optimise?utm=security-alert#security/vm-vulnerabilities" in text
