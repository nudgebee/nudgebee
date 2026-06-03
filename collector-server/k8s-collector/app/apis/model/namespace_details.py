from dataclasses import dataclass, field
from datetime import datetime

from apis.model.base_model import BaseDetails
from utils.datetime_utils import utc_now


@dataclass
class NamespaceDetails(BaseDetails):
    name: str
    is_active: str
    workload_count: int = 0
    creation_time: datetime = field(default_factory=utc_now)
    pod_count: int = 0
