"""Security posture alert template: critical-only with true counts, top-5 high
fallback, severity badges, per-account Details links."""

from typing import Any, Dict, List

from notifications_server.message_templates.slack.recommendation_nudge_digest import (
    AccountRecommendations,
    DigestRecommendation,
)
from notifications_server.message_templates.slack.security_posture_alert import (
    get_security_posture_alert_message_params,
    get_security_posture_alert_message_template,
)


def _text(blocks: List[Dict[str, Any]]) -> str:
    parts: List[str] = []
    for b in blocks:
        if isinstance(b.get("text"), dict):
            parts.append(b["text"].get("text", ""))
        for e in b.get("elements", []) or []:
            if isinstance(e, dict) and isinstance(e.get("text"), str):
                parts.append(e["text"])
    return "\n".join(parts)


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


def _params(criticals: int, highs: int, critical_count=None, high_count=None):
    recs = [_rec(i, "Critical") for i in range(criticals)] + [_rec(100 + i, "High") for i in range(highs)]
    return get_security_posture_alert_message_params(
        organization_id="org-1",
        organization_name="TestOrg",
        critical_count=critical_count if critical_count is not None else criticals,
        high_count=high_count if high_count is not None else highs,
        recommendations_by_account={"acc-1": {"account_name": "prod-aws", "recommendations": [r.dict() for r in recs]}},
        base_url="https://app",
    )


class TestSecurityPostureAlert:
    def test_critical_only_with_true_count_and_badge(self):
        msg = get_security_posture_alert_message_template(_params(criticals=4, highs=3, critical_count=8))
        text = _text(msg["blocks"])
        assert "8 critical security findings across 1 account" in text
        assert "`CRITICAL`" in text
        assert "`HIGH`" not in text  # highs stay out of the message when criticals exist
        assert "+5 more in the dashboard" in text  # 8 true - 3 shown
        assert "Vulnerable image — registry/ui:2.40" in text
        assert "Acct prod-aws" in text

    def test_high_fallback_when_no_criticals(self):
        msg = get_security_posture_alert_message_template(_params(criticals=0, highs=7, high_count=7))
        text = _text(msg["blocks"])
        assert "Top 5 high-severity security findings — no criticals today" in text
        assert "`HIGH`" in text
        assert "+2 more in the dashboard" in text

    def test_singular_critical_headline(self):
        text = _text(get_security_posture_alert_message_template(_params(criticals=1, highs=0))["blocks"])
        assert "1 critical security finding across 1 account" in text

    def test_details_and_view_all_buttons(self):
        msg = get_security_posture_alert_message_template(_params(criticals=1, highs=0))
        buttons = [e for b in msg["blocks"] if b.get("type") == "actions" for e in b["elements"]]
        labels = [b["text"]["text"] for b in buttons]
        assert "Details" in labels and "View all findings" in labels
        assert all("#security/image-scan" in b["url"] for b in buttons)

    def test_counts_context_line(self):
        text = _text(get_security_posture_alert_message_template(_params(criticals=2, highs=5))["blocks"])
        assert "Critical 2" in text and "High 5 · in dashboard" in text

    def test_relative_cta_url_resolved_against_base_url(self):
        params = _params(criticals=1, highs=0)
        params.recommendations_by_account["acc-1"].recommendations[
            0
        ].cta_url = "/kubernetes/details/acc-1#security/image-scan"
        msg = get_security_posture_alert_message_template(params)
        buttons = [e for b in msg["blocks"] if b.get("type") == "actions" for e in b["elements"]]
        details = [b for b in buttons if b["text"]["text"] == "Details"][0]
        assert details["url"] == "https://app/kubernetes/details/acc-1#security/image-scan"
