import logging
import os
from datetime import datetime
from typing import Optional, Union

from controllers.base import BaseController
from handlers.spend_handler import process_spend

WINDOW_SIZE = 30  # in minutes
BUCKET_NAME = "kubernetes-metrics"  # "kubernetes_data_store"
logger = logging.getLogger(__name__)


class OpenCostController(BaseController):
    # assuming account id for dev
    def __init__(self):
        super().__init__()
        self.account: Optional[str] = None
        self.tenant: Optional[str] = None
        self.data: Optional[dict] = None
        self.start_date: Union[int, float, None] = None
        self.end_date: Union[int, float, None] = None

    def get_properties(self) -> dict:
        final_dict = {"start_time": self.start_date, "end_time": self.end_date, "cloud_id": self.account}
        return final_dict

    def clean_container_data(self, data):
        fields = ["limit_cpu", "limit_memory", "requests_cpu", "requests_memory"]
        final_data = []
        for container in data["containers"]:
            container_name = container["name"]
            resources = container["resources"]
            resources_data = {"name": container_name}
            if "limits" in resources:
                if "cpu" in resources["limits"]:
                    resources_data.update({"limit_cpu": resources["limits"]["cpu"]})
                if "memory" in resources["limits"]:
                    resources_data.update({"limit_memory": resources["limits"]["memory"]})

            if "requests" in resources:
                if "cpu" in resources["requests"]:
                    resources_data.update({"requests_cpu": resources["requests"]["cpu"]})
                if "memory" in resources["requests"]:
                    resources_data.update({"requests_memory": resources["requests"]["memory"]})
            for field in fields:
                if field not in resources_data:
                    resources_data[field] = ""
            final_data.append(resources_data)
        return final_data

    def clean_data(self) -> dict:
        final_data = {}
        levels = ["pod_level_data", "node_level_data"]
        for level in levels:
            level_data = {}
            for time_window in self.data.get(level):
                for pod_path in time_window:
                    if pod_path in level_data:
                        level_data[pod_path].append(time_window[pod_path])
                    else:
                        level_data[pod_path] = [time_window[pod_path]]
            final_data[level] = level_data

        final_pod_data = []
        for pod in self.data["pods_info"]:
            pod["containers"] = self.clean_container_data(pod)
            final_pod_data.append(pod)
        final_data["pod_info"] = final_pod_data

        return final_data

    @staticmethod
    def validate_parameters(**kwargs):
        pass

    def build_json(self) -> dict:
        final_dict = {"properties": self.get_properties(), "data": self.clean_data()}
        return final_dict

    def get_file_path(self) -> str:
        start_date = datetime.fromtimestamp(self.start_date)
        module_obj_path = os.path.join(
            str(self.account),
            str(start_date.year),
            str(start_date.month),
            str(start_date.day),
            f"{self.end_date}.json.gz",
        )
        return module_obj_path

    def store_data(self, metric_data: dict, start_date: float, end_date: float, account_id: str, tenant: str) -> None:
        self.data = metric_data
        self.start_date = start_date
        self.end_date = end_date
        self.account = account_id
        self.tenant = tenant
        process_spend(self.data, self.tenant, self.account)
        self.update_agent_last_synced_in_db(account_id=account_id, new_date=datetime.fromtimestamp(self.end_date))
