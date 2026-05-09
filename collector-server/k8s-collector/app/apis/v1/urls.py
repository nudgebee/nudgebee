from apis.base.urls import UrlConfig
from apis.routes import Routes
from apis.v1.auth.api import AuthValidate
from apis.v1.autopilot.api import AutopilotHandler
from apis.v1.discovery.api import Discovery
from apis.v1.events.api import Events
from apis.v1.metric_data.api import OpenCostData
from apis.v1.playbook.api import PlaybookHandler
from apis.v1.spend.api import Spends
from apis.v1.task.api import Task
from apis.v1.telemetry.api import Telemetry
from apis.v1.timestamp.api import OpenCostAtrributes

NAMESPACE = "/v1"


URL_PATTERNS = [
    Routes("/opencost/attributes", OpenCostAtrributes),
    Routes("/opencost/data", OpenCostData),
    Routes("/k8s/events", Events),
    Routes("/k8s/discovery/<resource_type>", Discovery),
    Routes("/k8s/discovery", Discovery),
    Routes("/k8s/spend", Spends),
    Routes("/k8s/telemetry", Telemetry),
    Routes("/k8s/tasks", Task),
    Routes("/k8s/tasks/<task_id>", Task),
    Routes("/auth/validate", AuthValidate),
    Routes("/k8s/runbook/action/output", AutopilotHandler),
    Routes("/k8s/playbook", PlaybookHandler),
]

URL_CONFIG = UrlConfig(NAMESPACE)
URL_CONFIG.registers_urls(URL_PATTERNS)
