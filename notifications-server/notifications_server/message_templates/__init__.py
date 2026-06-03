from notifications_server.message_templates.google_chat.auto_optimize_scheduled_notification import (
    get_google_chat_auto_optimize_schedule_message_template,
)
from notifications_server.message_templates.google_chat.cloud_cost_summary import get_cloud_cost_gchat_message_template
from notifications_server.message_templates.google_chat.daily_highlight import get_gchat_daily_highlight_template
from notifications_server.message_templates.ms_teams.auto_optimize_scheduled_notification import (
    get_ms_teams_auto_pilot_scheduled_message_template,
)
from notifications_server.message_templates.ms_teams.cloud_cost_summary import get_teams_cloud_cost_message_template
from notifications_server.message_templates.ms_teams.daily_highlight import get_teams_daily_highlight_template

from notifications_server.message_templates.slack.auto_optimize_scheduled_notification import (
    get_auto_pilot_scheduled_message_params,
    get_slack_auto_pilot_scheduled_message_template,
)

from notifications_server.message_templates.slack.batched_findings import (
    get_batched_findings_message_template,
    get_batched_findings_message_params,
)
from notifications_server.message_templates.slack.cloud_cost_summary import (
    get_cloud_cost_message_template,
    get_cloud_cost_summary_message_params,
)
from notifications_server.message_templates.slack.daily_highlight import (
    get_daily_recap_message_template,
    get_daily_recap_message_params,
)
from notifications_server.message_templates.slack.default import (
    get_default_message_params,
    get_slack_default_message_template,
)
from notifications_server.message_templates.slack.events_summary import (
    get_events_summary_message_params,
    get_events_summary_message_template,
)
from notifications_server.message_templates.slack.recommendation import (
    get_recommendation_message_params,
    get_recommendation_message_template,
)
from notifications_server.message_templates.slack.slo import (
    get_slo_alert_message_params,
    get_slo_alert_message_template,
)

from notifications_server.message_templates.ms_teams.recommendation import get_teams_recommendation_message_template
from notifications_server.message_templates.google_chat.recommendation import get_gchat_recommendation_template
from notifications_server.message_templates.ms_teams.slo import get_teams_slo_alert_template
from notifications_server.message_templates.google_chat.slo import get_gchat_slo_alert_template
from notifications_server.message_templates.ms_teams.event_summary import get_teams_events_summary_template
from notifications_server.message_templates.google_chat.event_summary import get_gchat_events_summary_template

from notifications_server.message_templates.slack.grouped_slo_notification import (
    get_grouped_slo_alerts_template,
    get_slo_aggregated_message_params,
)

from notifications_server.message_templates.ms_teams.grouped_slo_notification import (
    get_grouped_slo_alerts_ms_teams_template,
)
from notifications_server.message_templates.google_chat.grouped_slo_notification import (
    get_grouped_slo_alerts_gchat_template,
)

from notifications_server.message_templates.slack.grouped_anomaly_notification import (
    get_grouped_anomaly_alerts_template,
    get_anomaly_aggregated_message_params,
)
from notifications_server.message_templates.ms_teams.grouped_anomaly_notification import (
    get_grouped_anomaly_alerts_ms_teams_template,
)
from notifications_server.message_templates.google_chat.grouped_anomaly_notification import (
    get_grouped_anomaly_alerts_gchat_template,
)

from notifications_server.message_templates.slack.generic import (
    get_generic_message_params,
    get_slack_generic_message_template,
)
from notifications_server.message_templates.ms_teams.generic import (
    get_ms_teams_generic_message_template,
)
from notifications_server.message_templates.google_chat.generic import (
    get_google_chat_generic_message_template,
)

from notifications_server.message_templates.slack.recommendation_nudge_digest import (
    get_recommendation_nudge_digest_message_params,
    get_recommendation_nudge_digest_message_template,
)
from notifications_server.message_templates.ms_teams.recommendation_nudge_digest import (
    get_teams_recommendation_nudge_digest_template,
)
from notifications_server.message_templates.google_chat.recommendation_nudge_digest import (
    get_gchat_recommendation_nudge_digest_template,
)

from notifications_server.message_templates.slack.recommendation_proactive_nudge import (
    get_recommendation_proactive_nudge_message_params,
    get_recommendation_proactive_nudge_message_template,
)
from notifications_server.message_templates.ms_teams.recommendation_proactive_nudge import (
    get_teams_recommendation_proactive_nudge_template,
)
from notifications_server.message_templates.google_chat.recommendation_proactive_nudge import (
    get_gchat_recommendation_proactive_nudge_template,
)

from notifications_server.message_templates.slack.recommendation_resolution import (
    get_recommendation_resolution_message_params,
    get_recommendation_resolution_message_template,
)
from notifications_server.message_templates.ms_teams.recommendation_resolution import (
    get_teams_recommendation_resolution_template,
)
from notifications_server.message_templates.google_chat.recommendation_resolution import (
    get_gchat_recommendation_resolution_template,
)

template_mapping = {
    "default": {
        "common_params": get_default_message_params,
        "slack": get_slack_default_message_template,
        "ms_teams": None,
        "google_chat": None,
    },
    "generic": {
        "common_params": get_generic_message_params,
        "slack": get_slack_generic_message_template,
        "ms_teams": get_ms_teams_generic_message_template,
        "google_chat": get_google_chat_generic_message_template,
    },
    "batched_findings": {
        "common_params": get_batched_findings_message_params,
        "slack": get_batched_findings_message_template,
        "ms_teams": None,
        "google_chat": None,
    },
    "daily_highlight_report": {
        "common_params": get_daily_recap_message_params,
        "slack": get_daily_recap_message_template,
        "ms_teams": get_teams_daily_highlight_template,
        "google_chat": get_gchat_daily_highlight_template,
    },
    "k8s_recommendation": {
        "common_params": get_recommendation_message_params,
        "slack": get_recommendation_message_template,
        "ms_teams": get_teams_recommendation_message_template,
        "google_chat": get_gchat_recommendation_template,
    },
    "slo_alert": {
        "common_params": get_slo_alert_message_params,
        "slack": get_slo_alert_message_template,
        "ms_teams": get_teams_slo_alert_template,
        "google_chat": get_gchat_slo_alert_template,
    },
    "auto_pilot_schedule_notification": {
        "common_params": get_auto_pilot_scheduled_message_params,
        "slack": get_slack_auto_pilot_scheduled_message_template,
        "ms_teams": get_ms_teams_auto_pilot_scheduled_message_template,
        "google_chat": get_google_chat_auto_optimize_schedule_message_template,
    },
    "events_summary": {
        "common_params": get_events_summary_message_params,
        "slack": get_events_summary_message_template,
        "ms_teams": get_teams_events_summary_template,
        "google_chat": get_gchat_events_summary_template,
    },
    "grouped_slo_alert": {
        "common_params": get_slo_aggregated_message_params,
        "slack": get_grouped_slo_alerts_template,
        "ms_teams": get_grouped_slo_alerts_ms_teams_template,
        "google_chat": get_grouped_slo_alerts_gchat_template,
    },
    "cloud_cost_summary": {
        "common_params": get_cloud_cost_summary_message_params,
        "slack": get_cloud_cost_message_template,
        "ms_teams": get_teams_cloud_cost_message_template,
        "google_chat": get_cloud_cost_gchat_message_template,
    },
    "grouped_anomaly": {
        "common_params": get_anomaly_aggregated_message_params,
        "slack": get_grouped_anomaly_alerts_template,
        "ms_teams": get_grouped_anomaly_alerts_ms_teams_template,
        "google_chat": get_grouped_anomaly_alerts_gchat_template,
    },
    "recommendation_nudge_digest": {
        "common_params": get_recommendation_nudge_digest_message_params,
        "slack": get_recommendation_nudge_digest_message_template,
        "ms_teams": get_teams_recommendation_nudge_digest_template,
        "google_chat": get_gchat_recommendation_nudge_digest_template,
    },
    "recommendation_proactive_nudge": {
        "common_params": get_recommendation_proactive_nudge_message_params,
        "slack": get_recommendation_proactive_nudge_message_template,
        "ms_teams": get_teams_recommendation_proactive_nudge_template,
        "google_chat": get_gchat_recommendation_proactive_nudge_template,
    },
    "recommendation_resolution": {
        "common_params": get_recommendation_resolution_message_params,
        "slack": get_recommendation_resolution_message_template,
        "ms_teams": get_teams_recommendation_resolution_template,
        "google_chat": get_gchat_recommendation_resolution_template,
    },
}
