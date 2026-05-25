from dataclasses import dataclass
from datetime import datetime

from apis.model.base_model import BaseDetails


@dataclass
class NamespaceDetails(BaseDetails):
    name: str
    is_active: str
    workload_count: int = 0
    creation_time: datetime = datetime.now()
    pod_count: int = 0
