import html
from enum import Enum

from pydantic import BaseModel

from notifications_server.configs.settings import public_ip, settings

JPG_SUFFIX = ".jpg"
PNG_SUFFIX = ".png"
SVG_SUFFIX = ".svg"
PUBLIC_IP = public_ip()


class BaseBlock(BaseModel):
    hidden: bool = False


class Emojis(Enum):
    Explain = "📘"
    Recommend = "🛠"
    Alert = "🚨"
    Event = "‼️"
    Summary = "✉️"
    Description = "🗒️"
    Clock = "⏰"
    Calendar = "📅"
    Source = "⚙️"
    Reporter = "🥷🏻"
    Investigate = "🔬"
    Resource = "📦"
    Cluster = "🖥️️"
    Namespace = "🏷️"
    Evidence = "🔍"
    ThumbsUp = "👍🏼"
    New = "💡"
    Resolved = "✅"
    Rocket = "🚀"
    Dollar = "💲"
    Brain = "🧠"
    Thanks = "😊"
    Question = "❓"
    Choice = "👍🏼"
    Robot = "🤖"
    Sparkles = "✨"


class FindingSeverity(Enum):
    DEBUG = 0
    INFO = 1
    LOW = 2
    MEDIUM = 3
    HIGH = 4

    @staticmethod
    def from_severity(severity: str) -> "FindingSeverity":
        if severity == "DEBUG":
            return FindingSeverity.DEBUG
        elif severity == "INFO":
            return FindingSeverity.INFO
        elif severity == "LOW":
            return FindingSeverity.LOW
        elif severity == "MEDIUM":
            return FindingSeverity.MEDIUM
        elif severity == "HIGH":
            return FindingSeverity.HIGH

        raise ValueError(f"Unknown severity {severity}")

    def to_emoji(self) -> str:
        """Returns empty string - emojis removed for cleaner notifications."""
        return ""


class FindingStatus(Enum):
    FIRING = 0
    RESOLVED = 1

    def to_color_hex(self) -> str:
        if self == FindingStatus.RESOLVED:
            return "#00B302"

        return "#EF311F"

    def to_color_decimal(self) -> str:
        if self == FindingStatus.RESOLVED:
            return "45826"

        return "15675679"

    def to_emoji(self) -> str:
        if self == FindingStatus.RESOLVED:
            return "✅"

        return "🔥"

    def to_string(self) -> str:
        if self == FindingStatus.RESOLVED:
            return "Resolved"

        return "Open"


def render_success_page(tool: str, url: str) -> str:
    branding_name = settings.urls.branding_name
    return f"""<!DOCTYPE html> <html lang="en"> <head> <meta charset="UTF-8"> <title>Redirecting...</title> </head>
    <body style="padding: 20px; font-family: Verdana, sans-serif; text-align: center; background-color: #f4f4f4;
    margin: 0;"> <h2 style="color: #333; font-size: 24px;">Thank you!</h2> <p style="color: #555; font-size:
    16px;"> Your {tool} installation is complete, please click below to redirect to {branding_name}.</p>
  <a href="{html.escape(url)}" style="display: inline-block; padding: 10px 20px;
  margin-top: 20px; font-size: 16px; color: #fff; background-color: #FACF39; border: none; border-radius: 5px;
  text-decoration: none; cursor: pointer;">Go to {branding_name}</a>
</body>
</html>
"""


def feedback_message():
    return [
        {"type": "section", "text": {"type": "mrkdwn", "text": "*Was this response helpful?*"}},
        {
            "type": "actions",
            "elements": [
                {
                    "type": "button",
                    "text": {"type": "plain_text", "emoji": True, "text": "👍 Yes"},
                    "value": (
                        '{"body":{"action_name": "ai_chat_feedback", "action_params": {"feedback": "positive","useful":'
                        " true}}}"
                    ),
                    "action_id": "ai_chat_feedback_useful",
                },
                {
                    "type": "button",
                    "text": {"type": "plain_text", "emoji": True, "text": "👎 No"},
                    "value": (
                        '{"body":{"action_name": "ai_chat_feedback","action_params": {"feedback": "negative","useful":'
                        " false}}}"
                    ),
                },
            ],
        },
    ]
