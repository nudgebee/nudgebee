"""pod_right_sizing recommendations arrive as two rows sharing a rule_name but
differing by category (RightSizing / Configuration). The daily-recap renderers
used to print rule_name only, so the two rows looked like an accidental
duplicate. format_recommendation_label tags them apart; every other rule is
left untouched."""

from notifications_server.message_templates.slack.daily_highlight import (
    Recommendation,
    format_recommendation_label,
)


def _rec(rule_name, category, savings=0.0):
    return Recommendation(
        category=category,
        rule_name=rule_name,
        count=1,
        account_id="a1",
        sum_estimated_savings=savings,
    )


def test_pod_right_sizing_rightsizing_category_is_tagged():
    label = format_recommendation_label(_rec("pod_right_sizing", "RightSizing", 443.16))
    assert label == "Pod Right Sizing (resize requests)"


def test_pod_right_sizing_configuration_category_is_tagged():
    label = format_recommendation_label(_rec("pod_right_sizing", "Configuration"))
    assert label == "Pod Right Sizing (set requests, best practice)"


def test_pod_right_sizing_unknown_category_falls_back_to_plain_name():
    assert format_recommendation_label(_rec("pod_right_sizing", "Something")) == "Pod Right Sizing"


def test_other_rules_are_never_tagged_even_under_rightsizing_category():
    assert format_recommendation_label(_rec("unused_pvc", "RightSizing")) == "Unused Pvc"
    assert format_recommendation_label(_rec("pv_rightsize", "RightSizing")) == "Pv Rightsize"
